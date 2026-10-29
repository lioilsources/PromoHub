package hub

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lioilsources/promohub/internal/apify"
	"github.com/lioilsources/promohub/internal/config"
	"github.com/lioilsources/promohub/internal/ifttt"
	"github.com/lioilsources/promohub/internal/notify"
	"github.com/lioilsources/promohub/internal/rules"
	"github.com/lioilsources/promohub/internal/store"
)

// Integration tests need Postgres: make test-db (or set PROMOHUB_TEST_DATABASE_URL).

type fakeApify struct {
	mu       sync.Mutex
	runs     map[string]apify.Run
	datasets map[string]string
	started  []string
}

func (f *fakeApify) GetRun(_ context.Context, id string) (apify.Run, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.runs[id], nil
}

func (f *fakeApify) DatasetItems(_ context.Context, id string, _ int64) ([]json.RawMessage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []json.RawMessage
	err := json.Unmarshal([]byte(f.datasets[id]), &out)
	return out, err
}

func (f *fakeApify) RunTask(_ context.Context, id string, _ int64) (apify.Run, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.started = append(f.started, id)
	return apify.Run{ID: "started-" + id}, nil
}

type triggered struct {
	event   string
	payload map[string]any
}

type env struct {
	t       *testing.T
	hub     *Hub
	srv     *httptest.Server
	apify   *fakeApify
	st      *store.Store
	mu      sync.Mutex
	sent    []triggered
	failFor string
}

const (
	apifySecret = "apify-secret-0123456789abcdef"
	iftttSecret = "ifttt-secret-0123456789abcdef"
)

func setup(t *testing.T) *env {
	url := os.Getenv("PROMOHUB_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("PROMOHUB_TEST_DATABASE_URL not set (make test-db)")
	}
	ctx := context.Background()
	st, err := store.Open(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.Pool.Exec(ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
		t.Fatal(err)
	}
	if err := store.Migrate(url); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(st.Close)

	e := &env{t: t, st: st, apify: &fakeApify{runs: map[string]apify.Run{}, datasets: map[string]string{}}}
	iftttSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ev := strings.Split(r.URL.Path, "/")[2]
		e.mu.Lock()
		defer e.mu.Unlock()
		if ev == e.failFor {
			http.Error(w, "boom", http.StatusBadGateway)
			return
		}
		var p map[string]any
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &p)
		e.sent = append(e.sent, triggered{ev, p})
	}))
	t.Cleanup(iftttSrv.Close)

	files := &config.Files{
		Apps: []config.App{{Slug: "swypekids", Name: "SwypeKids"}},
		Accounts: config.Accounts{
			Accounts: []config.Account{{Platform: "x", Handle: "ol1n"}, {Platform: "youtube", Handle: "ol1n"}},
			ApifyTasks: []config.ApifyTask{
				{Name: "x-mentions", ID: "TASK_M", Kind: "x-mentions", Account: "x:ol1n"},
				{Name: "x-profile", ID: "TASK_P", Kind: "x-profile", Account: "x:ol1n"},
			},
		},
		Rules: config.Rules{Rules: []config.RuleSpec{
			{Name: "mention", On: rules.OnInboundMention, When: `text matches /swypekids/i`, Then: config.StringSet{"ifttt:promo_mention"}},
			{Name: "viral", On: rules.OnMetricDelta, When: "views_delta_24h > 1000", Then: config.StringSet{"ifttt:promo_metric_jump"}},
			{Name: "milestone", On: rules.OnMetricSnapshot, When: `scope == "account" and followers crosses [500]`, Then: config.StringSet{"ifttt:promo_milestone"}},
			{Name: "new_video", On: rules.OnContentNew, When: `platform == "youtube"`, Then: config.StringSet{"ifttt:promo_new_video"}},
		}},
	}
	eng, err := rules.New(files.Rules)
	if err != nil {
		t.Fatal(err)
	}
	ic := ifttt.NewClient("k")
	ic.BaseURL = iftttSrv.URL
	e.hub = New(Deps{
		Log:   slog.New(slog.NewTextHandler(io.Discard, nil)),
		Env:   config.Env{ApifyWebhookSecret: apifySecret, IFTTTInboundSecret: iftttSecret, EventMaxAttempts: 3, RunMaxAttempts: 3, WorkerInterval: time.Hour},
		Files: files, Rules: eng, Store: st, Apify: e.apify, IFTTT: ic, Telegram: &notify.Telegram{},
	})
	if err := e.hub.SyncConfig(ctx); err != nil {
		t.Fatal(err)
	}
	e.srv = httptest.NewServer(e.hub.Handler())
	t.Cleanup(e.srv.Close)
	return e
}

func (e *env) post(path, body string) (int, map[string]any) {
	e.t.Helper()
	resp, err := http.Post(e.srv.URL+path, "application/json", strings.NewReader(body))
	if err != nil {
		e.t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func (e *env) webhook(runID, taskID string) (int, map[string]any) {
	return e.post("/webhooks/apify?secret="+apifySecret,
		`{"eventType":"ACTOR.RUN.SUCCEEDED","eventData":{"actorTaskId":"`+taskID+`","actorRunId":"`+runID+`"},"resource":{"id":"`+runID+`","status":"SUCCEEDED"}}`)
}

func (e *env) addRun(id, task, items string, finished time.Time) {
	e.apify.mu.Lock()
	defer e.apify.mu.Unlock()
	cost := 0.01
	e.apify.runs[id] = apify.Run{ID: id, TaskID: task, Status: "SUCCEEDED", DatasetID: "ds-" + id, FinishedAt: &finished, CostUSD: &cost}
	e.apify.datasets["ds-"+id] = items
}

// work drains both workers once, like the background loops do.
func (e *env) work() {
	e.t.Helper()
	ctx := context.Background()
	if err := e.hub.processPendingRuns(ctx); err != nil {
		e.t.Fatal(err)
	}
	if err := e.hub.deliverPending(ctx); err != nil {
		e.t.Fatal(err)
	}
}

func (e *env) count(sql string) int {
	e.t.Helper()
	var n int
	if err := e.st.Pool.QueryRow(context.Background(), sql).Scan(&n); err != nil {
		e.t.Fatal(err)
	}
	return n
}

func (e *env) sentEvents() []triggered {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]triggered(nil), e.sent...)
}

func TestApifyWebhookEndToEnd(t *testing.T) {
	e := setup(t)
	mentions := `[
		{"id":"100","text":"SwypeKids is lovely","author":{"userName":"fan"},"url":"https://x.com/fan/status/100"},
		{"id":"101","text":"unrelated tweet","author":{"userName":"other"}},
		{"id":"102","text":"my own SwypeKids promo","author":{"userName":"ol1n"}}
	]`
	e.addRun("run1", "TASK_M", mentions, time.Now())

	if code, _ := e.post("/webhooks/apify?secret=wrong", `{}`); code != http.StatusUnauthorized {
		t.Fatalf("bad secret: %d", code)
	}
	if code, _ := e.webhook("runX", "TASK_EVIL"); code != http.StatusForbidden {
		t.Fatalf("unknown task: %d", code)
	}
	if code, out := e.webhook("run1", "TASK_M"); code != http.StatusAccepted {
		t.Fatalf("webhook: %d %v", code, out)
	}
	if code, out := e.webhook("run1", "TASK_M"); code != http.StatusOK || out["status"] != "duplicate" {
		t.Fatalf("redelivery: %d %v", code, out)
	}
	e.work()

	if n := e.count("SELECT count(*) FROM inbound_items"); n != 2 {
		t.Fatalf("inbound items = %d, want 2 (own post dropped)", n)
	}
	sent := e.sentEvents()
	if len(sent) != 1 || sent[0].event != "promo_mention" || sent[0].payload["author"] != "fan" || sent[0].payload["app"] != "swypekids" {
		t.Fatalf("sent = %+v", sent)
	}
	if !strings.Contains(sent[0].payload["message"].(string), "SwypeKids is lovely") {
		t.Fatalf("message = %v", sent[0].payload["message"])
	}
	if n := e.count("SELECT count(*) FROM apify_runs WHERE state = 'processed' AND items = 3 AND new_items = 2 AND cost_usd = 0.01"); n != 1 {
		t.Fatal("run not recorded as processed")
	}

	// A new run returning overlapping items: only the new mention fires.
	e.addRun("run2", "TASK_M", `[
		{"id":"100","text":"SwypeKids is lovely","author":{"userName":"fan"}},
		{"id":"103","text":"swypekids again","author":{"userName":"fan3"}}
	]`, time.Now())
	e.webhook("run2", "TASK_M")
	e.work()
	if n := e.count("SELECT count(*) FROM inbound_items"); n != 3 {
		t.Fatalf("inbound items = %d, want 3", n)
	}
	if sent := e.sentEvents(); len(sent) != 2 || sent[1].payload["external_id"] != "103" {
		t.Fatalf("sent = %+v", sent)
	}
	if n := e.count("SELECT count(*) FROM events WHERE target IS NOT NULL AND sent_at IS NULL"); n != 0 {
		t.Fatalf("%d undelivered events", n)
	}
}

func TestMetricDeltasAndMilestones(t *testing.T) {
	e := setup(t)
	t0 := time.Now().Add(-30 * time.Hour)
	e.addRun("p1", "TASK_P", `[{"id":"1","text":"launch","viewCount":100,"likeCount":5,"author":{"userName":"ol1n","followers":480}}]`, t0)
	e.webhook("p1", "TASK_P")
	e.work()
	if n := len(e.sentEvents()); n != 0 {
		t.Fatalf("first snapshot fired %d events", n)
	}

	e.addRun("p2", "TASK_P", `[{"id":"1","text":"launch","viewCount":2500,"likeCount":40,"author":{"userName":"ol1n","followers":530}}]`, time.Now())
	e.webhook("p2", "TASK_P")
	e.work()

	got := map[string]map[string]any{}
	for _, s := range e.sentEvents() {
		got[s.event] = s.payload
	}
	if p := got["promo_metric_jump"]; p == nil || p["deltas"].(map[string]any)["views_delta_24h"].(float64) != 2400 {
		t.Fatalf("viral event: %+v", got)
	}
	if got["promo_milestone"] == nil {
		t.Fatalf("milestone event missing: %+v", got)
	}
	if n := e.count("SELECT count(*) FROM metric_snapshots"); n != 4 {
		t.Fatalf("snapshots = %d, want 4 (post + account × 2 runs)", n)
	}
	if n := e.count("SELECT count(*) FROM content_items"); n != 1 {
		t.Fatalf("content items = %d", n)
	}
}

func TestOutboxRetries(t *testing.T) {
	e := setup(t)
	e.failFor = "promo_mention"
	e.addRun("r", "TASK_M", `[{"id":"5","text":"SwypeKids!","author":{"userName":"a"}}]`, time.Now())
	e.webhook("r", "TASK_M")
	e.work()
	if n := e.count("SELECT count(*) FROM events WHERE target IS NOT NULL AND sent_at IS NULL AND attempts = 1 AND last_error LIKE '%502%'"); n != 1 {
		t.Fatal("failed delivery not scheduled for retry")
	}
	e.mu.Lock()
	e.failFor = ""
	e.mu.Unlock()
	if _, err := e.st.Pool.Exec(context.Background(), "UPDATE events SET next_attempt_at = now()"); err != nil {
		t.Fatal(err)
	}
	e.work()
	if len(e.sentEvents()) != 1 {
		t.Fatal("retry did not deliver")
	}
}

func TestIFTTTInbound(t *testing.T) {
	e := setup(t)
	path := "/webhooks/ifttt/youtube_new_video?secret=" + iftttSecret
	body := `{"url":"https://www.youtube.com/watch?v=vid1","title":"SwypeKids trailer","published_at":"October 29, 2026 at 10:00AM"}`
	if code, out := e.post(path, body); code != http.StatusOK || out["status"] != "created" {
		t.Fatalf("%d %v", code, out)
	}
	if _, out := e.post(path, body); out["status"] != "duplicate" {
		t.Fatalf("second post: %v", out)
	}
	e.work()
	if s := e.sentEvents(); len(s) != 1 || s[0].event != "promo_new_video" {
		t.Fatalf("sent = %+v", s)
	}
	if n := e.count("SELECT count(*) FROM content_items WHERE external_id = 'vid1' AND app_id IS NOT NULL AND account_id IS NOT NULL"); n != 1 {
		t.Fatal("content item not attributed")
	}

	if code, _ := e.post("/webhooks/ifttt/run_task?secret="+iftttSecret, `{"task":"nope"}`); code != http.StatusForbidden {
		t.Fatalf("unknown task: %d", code)
	}
	if code, _ := e.post("/webhooks/ifttt/run_task?secret="+iftttSecret, `{"task":"x-mentions"}`); code != http.StatusAccepted {
		t.Fatalf("run_task: %d", code)
	}
	if len(e.apify.started) != 1 || e.apify.started[0] != "TASK_M" {
		t.Fatalf("started = %v", e.apify.started)
	}
	if code, _ := e.post("/webhooks/ifttt/scan?secret=nope", `{}`); code != http.StatusUnauthorized {
		t.Fatalf("bad secret: %d", code)
	}
}
