package hub

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lioilsources/promohub/internal/composer"
	"github.com/lioilsources/promohub/internal/sparkbroker"
)

// fakeBroker mimics spark-broker's job API.
type fakeBroker struct {
	mu        sync.Mutex
	leases    int
	state     string // what GET returns
	done      []map[string]any
	cancelled int
}

func (f *fakeBroker) server(t *testing.T) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/jobs":
			var in sparkbroker.SubmitReq
			_ = json.NewDecoder(r.Body).Decode(&in)
			if in.Project != "promohub" || in.Priority != 15 || in.Stages[0].Services[0] != "qwen36" || in.Stages[0].Mode != "lease" {
				t.Errorf("%+v", in)
			}
			f.leases++
			w.Write([]byte(`{"id":"1113-1","state":"queued"}`))
		case r.Method == http.MethodGet:
			w.Write([]byte(`{"id":"1113-1","state":"` + f.state + `","waiting":"user window comfy"}`))
		case strings.HasSuffix(r.URL.Path, "/heartbeat"):
			w.Write([]byte(`{}`))
		case strings.HasSuffix(r.URL.Path, "/done"):
			var in map[string]any
			_ = json.NewDecoder(r.Body).Decode(&in)
			f.done = append(f.done, in)
			w.Write([]byte(`{}`))
		case r.Method == http.MethodDelete:
			f.cancelled++
			w.Write([]byte(`{}`))
		}
	}))
}

func windowEnv(t *testing.T, llmStatus *int) (*env, *fakeTelegram, *fakeBroker, *time.Time) {
	e, tg, _ := publishEnv(t)
	fb := &fakeBroker{state: "running"}
	bs := fb.server(t)
	t.Cleanup(bs.Close)
	llm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if *llmStatus != 200 {
			w.WriteHeader(*llmStatus)
			return
		}
		w.Write([]byte(`{"choices":[{"message":{"content":"{\"bluesky:ol1n.bsky.social\":\"LLM text for Bluesky\"}"}}]}`))
	}))
	t.Cleanup(llm.Close)
	h := e.hub
	h.llm = &composer.LLM{BaseURL: llm.URL, Model: "openclaw-default", HTTP: llm.Client()}
	h.window = &Window{Broker: &sparkbroker.Client{Base: bs.URL, HTTP: bs.Client()}, At: "00:15", Project: "promohub", Priority: 15,
		Services: []string{"qwen36"}, LeaseMin: 20, GrantWait: 50 * time.Millisecond, Poll: time.Millisecond, Heartbeat: time.Hour,
		Retries: 3, RetryWait: time.Millisecond}
	clock := time.Date(2030, 1, 1, 22, 0, 0, 0, time.UTC) // 23:00 Prague
	h.now = func() time.Time { return clock }
	return e, tg, fb, &clock
}

func TestWindowComposesPendingAtNight(t *testing.T) {
	status := 200
	e, tg, fb, clock := windowEnv(t, &status)
	ctx := context.Background()
	e.message("/post swypekids SwypeKids gets offline mode")
	if !strings.Contains(tg.lastSent()["text"].(string), "počká na noční LLM okno") || e.count(`SELECT count(*) FROM drafts`) != 0 {
		t.Fatalf("%+v", tg.lastSent())
	}
	// 23:00: not yet.
	_ = e.hub.llmWindowTick(ctx)
	if fb.leases != 0 {
		t.Fatal("window ran before 00:15")
	}
	*clock = clock.Add(90 * time.Minute) // 00:30 Prague
	if err := e.hub.llmWindowTick(ctx); err != nil {
		t.Fatal(err)
	}
	var text string
	_ = e.st.Pool.QueryRow(ctx, `SELECT d.text FROM drafts d JOIN accounts a ON a.id = d.account_id WHERE a.platform = 'bluesky'`).Scan(&text)
	if fb.leases != 1 || !strings.HasPrefix(text, "LLM text for Bluesky") || len(fb.done) != 1 || fb.done[0]["error"] != "" {
		t.Fatalf("leases=%d text=%q done=%+v", fb.leases, text, fb.done)
	}
	if e.count(`SELECT count(*) FROM campaigns WHERE llm_status = 'done'`) != 1 {
		t.Fatal("campaign not marked done")
	}
	// Same night again: nothing.
	_ = e.hub.llmWindowTick(ctx)
	if fb.leases != 1 {
		t.Fatal("second window in one night")
	}
	// Next night with nothing pending: no lease at all.
	*clock = clock.Add(24 * time.Hour)
	_ = e.hub.llmWindowTick(ctx)
	if fb.leases != 1 {
		t.Fatal("leased with nothing pending")
	}
}

func TestWindowNowSkipsTheQueue(t *testing.T) {
	status := 200
	e, _, fb, _ := windowEnv(t, &status)
	e.message("/post now swypekids Urgent SwypeKids fix")
	if e.count(`SELECT count(*) FROM drafts`) == 0 || e.count(`SELECT count(*) FROM campaigns WHERE llm_status = 'none'`) != 1 || fb.leases != 0 {
		t.Fatal("/post now must compose right away without the LLM")
	}
}

func TestWindowModelDownAndNotGranted(t *testing.T) {
	status := 503
	e, tg, fb, clock := windowEnv(t, &status)
	ctx := context.Background()
	e.message("/post swypekids One")
	e.message("/post swypekids Two")
	*clock = clock.Add(90 * time.Minute)
	_ = e.hub.llmWindowTick(ctx)
	if len(fb.done) != 1 || !strings.Contains(fb.done[0]["error"].(string), "LLM neodpovídá") {
		t.Fatalf("%+v", fb.done)
	}
	if e.count(`SELECT count(*) FROM campaigns WHERE llm_status = 'pending'`) != 2 || e.count(`SELECT count(*) FROM drafts`) != 0 {
		t.Fatal("campaigns must stay pending for the next night")
	}
	// Next night the SPARK stays busy: the lease is withdrawn, campaigns still wait.
	status = 200
	fb.state = "queued"
	*clock = clock.Add(24 * time.Hour)
	_ = e.hub.llmWindowTick(ctx)
	if fb.cancelled != 1 || !strings.Contains(tg.lastSent()["text"].(string), "SPARK nebyl volný") || e.count(`SELECT count(*) FROM campaigns WHERE llm_status = 'pending'`) != 2 {
		t.Fatalf("cancelled=%d %+v", fb.cancelled, tg.lastSent())
	}
}
