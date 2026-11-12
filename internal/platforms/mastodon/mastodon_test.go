package mastodon

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/lioilsources/promohub/internal/collectors"
	"github.com/lioilsources/promohub/internal/config"
	"github.com/lioilsources/promohub/internal/creds"
	"github.com/lioilsources/promohub/internal/platform"
)

// testdata/account.json and statuses.json are trimmed real answers of mastodon.social for
// @Gargron (2026-11-12). Notifications follow the documented Notification entity.

func setup(t *testing.T) (*Adapter, *string) {
	t.Helper()
	read := func(n string) []byte {
		b, err := os.ReadFile("testdata/" + n)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	var gotQuery string
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/accounts/lookup", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("acct") != "Gargron" {
			t.Errorf("acct=%q", r.URL.Query().Get("acct"))
		}
		w.Write(read("account.json"))
	})
	mux.HandleFunc("GET /api/v1/accounts/1/statuses", func(w http.ResponseWriter, r *http.Request) { w.Write(read("statuses.json")) })
	mux.HandleFunc("GET /api/v1/notifications", func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		if r.Header.Get("Authorization") != "Bearer tok-123" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Write([]byte(`[
		 {"id":"905","type":"mention","created_at":"2026-11-12T10:00:00.000Z","account":{"id":"77","acct":"fan@fosstodon.org"},
		  "status":{"id":"1175","url":"https://fosstodon.org/@fan/1","content":"<p>@Gargron reply &amp; thanks</p>","in_reply_to_account_id":"1",
		            "created_at":"2026-11-12T10:00:00.000Z","account":{"id":"77","acct":"fan@fosstodon.org"}}},
		 {"id":"904","type":"mention","created_at":"2026-11-12T09:00:00.000Z","account":{"id":"78","acct":"other"},
		  "status":{"id":"1174","url":"https://mastodon.social/@other/2","content":"<p>hey <span class=\"h-card\">@Gargron</span><br>look</p>",
		            "in_reply_to_account_id":null,"created_at":"2026-11-12T09:00:00.000Z","account":{"id":"78","acct":"other"}}},
		 {"id":"903","type":"favourite","account":{"id":"79","acct":"x"}}
		]`))
	})
	mux.HandleFunc("GET /api/v1/timelines/tag/{tag}", func(w http.ResponseWriter, r *http.Request) {
		switch r.PathValue("tag") {
		case "swypekids":
			w.Write([]byte(`[{"id":"2001","url":"https://a.example/@x/1","content":"<p>My kid loves <a href=\"t\">#<span>SwypeKids</span></a></p>","account":{"id":"5","acct":"x@a.example"}},
			                 {"id":"2000","url":"https://mastodon.social/@Gargron/9","content":"<p>#SwypeKids update</p>","account":{"id":"1","acct":"Gargron"}}]`))
		case "indiedev":
			w.Write([]byte(`[{"id":"2003","content":"<p>unrelated #indiedev</p>","account":{"id":"6","acct":"y"}}]`))
		default:
			w.Write([]byte(`[]`))
		}
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	a := New(srv.Client())
	a.Base = srv.URL
	old := creds.Getenv
	creds.Getenv = func(k string) string {
		if k == "MASTODON_MAIN_TOKEN" {
			return "tok-123"
		}
		return ""
	}
	t.Cleanup(func() { creds.Getenv = old })
	return a, &gotQuery
}

var acct = config.Account{Platform: "mastodon", Handle: "Gargron@mastodon.social", AuthRef: "MASTODON_MAIN", Hashtags: []string{"indiedev"}}

func TestProfileReal(t *testing.T) {
	a, _ := setup(t)
	b, _, err := a.Collect(context.Background(), collectors.Request{Account: acct, Capability: platform.CapProfile})
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Snapshots) != 3 {
		t.Fatalf("snapshots=%d skipped=%d", len(b.Snapshots), b.Skipped)
	}
	p := b.Snapshots[0]
	if p.Content.ExternalID != "117598326059120853" || *p.Likes != 33 || *p.Reposts != 5 || *p.Comments != 3 ||
		platform.ExternalID("mastodon", p.Content.URL) != p.Content.ExternalID {
		t.Fatalf("%+v %+v", p, p.Content)
	}
	if a := b.Snapshots[2]; *a.Followers != 381344 || a.Extra["posts"] != 82838 {
		t.Fatalf("account %+v", a)
	}
}

func TestNotifications(t *testing.T) {
	a, q := setup(t)
	b, cur, err := a.Collect(context.Background(), collectors.Request{Account: acct, Capability: platform.CapInbound,
		Cursor: json.RawMessage(`{"since_id":"900"}`)})
	if err != nil {
		t.Fatal(err)
	}
	if *q != "limit=40&since_id=900&types%5B%5D=mention" {
		t.Fatalf("query %s", *q)
	}
	if len(b.Inbound) != 2 || b.Skipped != 1 {
		t.Fatalf("%+v", b)
	}
	r, m := b.Inbound[0], b.Inbound[1]
	if r.Kind != "reply" || r.Text != "@Gargron reply & thanks" || r.ExternalID != "mastodon.social/1175" {
		t.Fatalf("reply %+v", r)
	}
	if m.Kind != "mention" || m.Text != "hey @Gargron\nlook" {
		t.Fatalf("mention %+v", m)
	}
	if string(cur) != `{"since_id":"905"}` {
		t.Fatalf("cursor %s", cur)
	}
}

func TestHashtagSearch(t *testing.T) {
	a, _ := setup(t)
	b, cur, err := a.Collect(context.Background(), collectors.Request{Account: acct, Capability: platform.CapSearch, Terms: []string{"SwypeKids"}})
	if err != nil {
		t.Fatal(err)
	}
	// Mine and the unrelated #indiedev post are dropped.
	if len(b.Inbound) != 1 || b.Inbound[0].Author != "x@a.example" || b.Skipped != 2 {
		t.Fatalf("%+v", b)
	}
	if string(cur) != `{"tags":{"indiedev":"2003","swypekids":"2001"}}` {
		t.Fatalf("cursor %s", cur)
	}
}

func TestTags(t *testing.T) {
	got := Tags([]string{"Swype Kids", "Hajánek", "olin.now"}, []string{"#IndieDev", "hajánek"})
	want := []string{"swypekids", "hajánek", "olinnow", "indiedev"}
	if len(got) != len(want) {
		t.Fatalf("%v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("%v", got)
		}
	}
}
