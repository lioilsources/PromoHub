package hub

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/lioilsources/promohub/internal/collectors"
	"github.com/lioilsources/promohub/internal/config"
	"github.com/lioilsources/promohub/internal/creds"
	"github.com/lioilsources/promohub/internal/platform"
	"github.com/lioilsources/promohub/internal/rules"
)

type fakeCollector struct {
	mu      sync.Mutex
	calls   map[string]int
	cursors map[string]string
	fail    error
	ready   error
}

func (f *fakeCollector) Platform() string { return "bluesky" }

func (f *fakeCollector) Ready(config.Account, string) error { return f.ready }

func (f *fakeCollector) Collect(_ context.Context, r collectors.Request) (collectors.Batch, json.RawMessage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls[r.Capability]++
	f.cursors[r.Capability] = string(r.Cursor)
	if f.fail != nil {
		return collectors.Batch{}, nil, f.fail
	}
	n := int64(450 + 40*f.calls[r.Capability]) // 490, then 530: crosses 500 on the second pass
	switch r.Capability {
	case platform.CapProfile:
		likes := int64(5)
		return collectors.Batch{Snapshots: []collectors.Snapshot{
			{Platform: "bluesky", Content: &collectors.Content{ExternalID: "3kpost", URL: "https://bsky.app/profile/ol1n.bsky.social/post/3kpost", Body: "hello"}, Likes: &likes},
			{Platform: "bluesky", Followers: &n},
		}}, nil, nil
	case platform.CapInbound:
		return collectors.Batch{Inbound: []collectors.Inbound{{Platform: "bluesky", Kind: "mention", ExternalID: "at://did:plc:fan/app.bsky.feed.post/1",
			Author: "fan.bsky.social", Text: "SwypeKids rocks"}}}, json.RawMessage(`{"since":"x"}`), nil
	}
	return collectors.Batch{}, nil, nil
}

// collectEnv adds a Bluesky account read via api with a fake adapter and a fixed clock.
func collectEnv(t *testing.T) (*env, *fakeCollector, *time.Time) {
	e := setup(t)
	f := &fakeCollector{calls: map[string]int{}, cursors: map[string]string{}}
	e.hub.files.Accounts.Accounts = append(e.hub.files.Accounts.Accounts,
		config.Account{Platform: "bluesky", Handle: "ol1n.bsky.social", Read: platform.ViaAPI})
	e.hub.files.Rules.Rules = append(e.hub.files.Rules.Rules,
		config.RuleSpec{Name: "account_error", On: rules.OnAccountError, Then: config.StringSet{"ifttt:promo_alert"}})
	eng, err := rules.New(e.hub.files.Rules)
	if err != nil {
		t.Fatal(err)
	}
	e.hub.rules = eng
	e.hub.collectors = map[string]collectors.Collector{"bluesky": f}
	e.hub.terms = nil // no search job
	clock := time.Date(2030, 1, 1, 12, 0, 0, 0, time.UTC)
	e.hub.now = func() time.Time { return clock }
	e.hub.rand = func() float64 { return 0.5 }
	if err := e.hub.SyncConfig(context.Background()); err != nil {
		t.Fatal(err)
	}
	return e, f, &clock
}

func (e *env) collect() {
	e.t.Helper()
	if err := e.hub.collectDue(context.Background()); err != nil {
		e.t.Fatal(err)
	}
	if err := e.hub.deliverPending(context.Background()); err != nil {
		e.t.Fatal(err)
	}
}

func TestSchedulerRunsOncePerSlotAndDedups(t *testing.T) {
	e, f, clock := collectEnv(t)
	e.collect()
	if f.calls["profile"] != 1 || f.calls["inbound"] != 1 {
		t.Fatalf("calls %v", f.calls)
	}
	if n := e.count(`SELECT count(*) FROM metric_snapshots`); n != 2 {
		t.Fatalf("snapshots %d", n)
	}
	if ev := e.sentEvents(); len(ev) != 1 || ev[0].event != "promo_mention" {
		t.Fatalf("events %+v", ev)
	}
	// Same clock: nothing is due, nothing runs twice.
	e.collect()
	if f.calls["profile"] != 1 || f.calls["inbound"] != 1 {
		t.Fatalf("ran again %v", f.calls)
	}
	// After the inbound interval only inbound runs, gets its cursor back and dedups the mention.
	*clock = clock.Add(11 * time.Minute)
	e.collect()
	if f.calls["profile"] != 1 || f.calls["inbound"] != 2 || f.cursors["inbound"] != `{"since": "x"}` {
		t.Fatalf("calls %v cursors %v", f.calls, f.cursors)
	}
	if n := e.count(`SELECT count(*) FROM inbound_items`); n != 1 {
		t.Fatalf("inbound %d", n)
	}
	// After the profile interval the follower count crosses 500 → milestone fires.
	*clock = clock.Add(3 * time.Hour)
	e.collect()
	if f.calls["profile"] != 2 {
		t.Fatalf("calls %v", f.calls)
	}
	if ev := e.sentEvents(); len(ev) != 2 || ev[1].event != "promo_milestone" {
		t.Fatalf("events %+v", ev)
	}
}

func TestSchedulerFailuresAlertOnce(t *testing.T) {
	e, f, clock := collectEnv(t)
	f.fail = errors.New("boom")
	for i := 0; i < 5; i++ {
		e.collect()
		*clock = clock.Add(7 * time.Hour)
	}
	alerts := 0
	for _, ev := range e.sentEvents() {
		if ev.event == "promo_alert" {
			alerts++
		}
	}
	if alerts != 2 { // one per capability, at the third failure only
		t.Fatalf("alerts %d: %+v", alerts, e.sentEvents())
	}
	// Recovery resets the count.
	f.fail = nil
	e.collect()
	if n := e.count(`SELECT count(*) FROM collector_state WHERE fail_count = 0 AND last_error IS NULL`); n != 2 {
		t.Fatalf("not reset: %d", n)
	}
}

func TestSchedulerMissingSecretIsNotAFailure(t *testing.T) {
	e, f, clock := collectEnv(t)
	f.ready = &creds.MissingError{Var: "BLUESKY_MAIN_APP_PASSWORD"}
	for i := 0; i < 4; i++ {
		e.collect()
		*clock = clock.Add(7 * time.Hour)
	}
	if f.calls["profile"] != 0 || len(e.sentEvents()) != 0 {
		t.Fatalf("calls %v events %+v", f.calls, e.sentEvents())
	}
	if n := e.count(`SELECT count(*) FROM collector_state WHERE fail_count = 0 AND last_error LIKE 'not connected:%'`); n != 2 {
		t.Fatalf("state %d", n)
	}
}

func TestSchedulerRejectsEmptyProfile(t *testing.T) {
	e, _, _ := collectEnv(t)
	e.hub.collectors["bluesky"] = emptyCollector{}
	if err := e.hub.planJobs(context.Background()); err != nil {
		t.Fatal(err)
	}
	e.collect()
	if n := e.count(`SELECT count(*) FROM collector_state WHERE capability = 'profile' AND fail_count = 1`); n != 1 {
		t.Fatal("empty profile must count as a failure")
	}
}

type emptyCollector struct{}

func (emptyCollector) Platform() string                   { return "bluesky" }
func (emptyCollector) Ready(config.Account, string) error { return nil }
func (emptyCollector) Collect(context.Context, collectors.Request) (collectors.Batch, json.RawMessage, error) {
	return collectors.Batch{}, nil, nil
}
