package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/lioilsources/promohub/internal/collectors"
	"github.com/lioilsources/promohub/internal/platform"
	"github.com/lioilsources/promohub/internal/platforms/bluesky"
	"github.com/lioilsources/promohub/internal/platforms/discord"
	"github.com/lioilsources/promohub/internal/platforms/mastodon"
	"github.com/lioilsources/promohub/internal/platforms/telegram"
	"github.com/lioilsources/promohub/internal/platforms/youtube"
	"github.com/lioilsources/promohub/internal/publish"
	"github.com/lioilsources/promohub/internal/store"
)

// readAdapters are the official-API collectors (Prompts/01-ACCOUNTS_PLAN.md §3.1).
func readAdapters(hc *http.Client) []collectors.Collector {
	return []collectors.Collector{
		bluesky.New(hc), mastodon.New(hc), youtube.New(hc), telegram.New(hc), discord.New(hc),
	}
}

func adapterFor(hc *http.Client, name string) collectors.Collector {
	for _, c := range readAdapters(hc) {
		if c.Platform() == name {
			return c
		}
	}
	return nil
}

func configDir() string {
	if d := os.Getenv("CONFIG_DIR"); d != "" {
		return d
	}
	return "."
}

// accounts prints every account with its read/publish path and the state of each capability:
// whether its secret is set and, when DATABASE_URL is reachable, the last pass.
func accounts() error {
	files, _, err := loadConfig(configDir())
	if err != nil {
		return err
	}
	type state struct {
		lastOK  *time.Time
		fails   int32
		lastErr string
	}
	states := map[string]state{} // "platform:handle/capability"
	if url := os.Getenv("DATABASE_URL"); url != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if st, err := store.Open(ctx, url); err == nil {
			defer st.Close()
			accts, _ := st.ListAccounts(ctx)
			keys := map[int64]string{}
			for _, a := range accts {
				keys[a.ID] = a.Platform + ":" + a.Handle
			}
			rows, _ := st.ListCollectorStates(ctx)
			for _, r := range rows {
				states[keys[r.AccountID]+"/"+r.Capability] = state{r.LastOkAt, r.FailCount, deref(r.LastError)}
			}
		} else {
			fmt.Fprintln(os.Stderr, "db not reachable, showing config only:", err)
		}
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ACCOUNT\tREAD\tPUBLISH\tCAPABILITY\tSTATUS\tLAST OK\tFAILS\tLAST ERROR")
	for _, a := range files.Accounts.Accounts {
		spec, _ := platform.Lookup(a.Platform)
		pub := string(a.Publish)
		if a.Publish == platform.ViaIFTTT {
			pub += " (" + a.IFTTTEvent + ")"
		}
		caps := spec.Capabilities()
		c := adapterFor(nil, a.Platform)
		if a.Read != platform.ViaAPI || c == nil || len(caps) == 0 {
			status := "-"
			switch {
			case a.Read == platform.ViaApify:
				status = "via Apify tasks"
			case a.Read == platform.ViaAPI:
				status = "adapter in a later wave"
			}
			fmt.Fprintf(w, "%s\t%s\t%s\t-\t%s\t\t\t\n", a.Key(), a.Read, pub, status)
			continue
		}
		for _, capability := range caps {
			status := "ready"
			if err := c.Ready(a, capability); err != nil {
				status = err.Error()
			}
			s := states[a.Key()+"/"+capability]
			last := ""
			if s.lastOK != nil {
				last = s.lastOK.Local().Format("2006-01-02 15:04")
			}
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%d\t%s\n", a.Key(), a.Read, pub, capability, status, last, s.fails, oneLine(s.lastErr, 60))
		}
	}
	return w.Flush()
}

// probe runs one adapter pass against the real API and prints the mapped batch as JSON without
// touching the DB. PROBE_RAW=1 also dumps the raw HTTP answers to stderr (for test fixtures —
// trim them and strip tokens before committing).
func probe(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: promohub probe <platform:handle> [profile|inbound|search]")
	}
	files, _, err := loadConfig(configDir())
	if err != nil {
		return err
	}
	acct, ok := files.Account(args[0])
	if !ok {
		return fmt.Errorf("account %q is not in accounts.yaml", args[0])
	}
	hc := &http.Client{Timeout: 30 * time.Second}
	if os.Getenv("PROBE_RAW") == "1" {
		hc.Transport = dumpTransport{http.DefaultTransport}
	}
	c := adapterFor(hc, acct.Platform)
	if c == nil {
		return fmt.Errorf("no read adapter for %s", acct.Platform)
	}
	spec, _ := platform.Lookup(acct.Platform)
	caps := spec.Capabilities()
	if len(args) > 1 {
		caps = []string{args[1]}
	}
	var terms []string
	for _, a := range files.Apps {
		terms = append(terms, a.Name)
		terms = append(terms, a.Keywords...)
	}
	out := map[string]any{}
	for _, capability := range caps {
		if err := c.Ready(acct, capability); err != nil {
			out[capability] = map[string]string{"error": err.Error()}
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		b, cur, err := c.Collect(ctx, collectors.Request{Account: acct, Capability: capability, Terms: terms})
		cancel()
		if err != nil {
			out[capability] = map[string]string{"error": err.Error()}
			continue
		}
		out[capability] = map[string]any{"batch": b, "cursor": json.RawMessage(nonEmpty(cur))}
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}

func nonEmpty(b []byte) []byte {
	if len(b) == 0 {
		return []byte("null")
	}
	return b
}

type dumpTransport struct{ next http.RoundTripper }

func (d dumpTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	resp, err := d.next.RoundTrip(r)
	if err != nil {
		return resp, err
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	resp.Body = io.NopCloser(bytes.NewReader(b))
	fmt.Fprintf(os.Stderr, "### %s %s → %d\n%s\n\n", r.Method, r.URL.Path, resp.StatusCode, b)
	return resp, nil
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func oneLine(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}

// publishers are the official-API publish paths (wave 2).
func publishers(hc *http.Client) []publish.Publisher {
	return []publish.Publisher{
		bluesky.Publisher{Adapter: bluesky.New(hc)},
		mastodon.Publisher{Adapter: mastodon.New(hc)},
		telegram.Publisher{Adapter: telegram.New(hc)},
		discord.Publisher{Adapter: discord.New(hc)},
	}
}
