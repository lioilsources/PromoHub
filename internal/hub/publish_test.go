package hub

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lioilsources/promohub/internal/collectors"
	"github.com/lioilsources/promohub/internal/config"
	"github.com/lioilsources/promohub/internal/platform"
	"github.com/lioilsources/promohub/internal/publish"
	"github.com/lioilsources/promohub/internal/store/db"
	"github.com/lioilsources/promohub/internal/tgbot"
)

// fakeTelegram records what the bot sends and edits.
type fakeTelegram struct {
	mu    sync.Mutex
	next  int64
	sent  []map[string]any
	edits []map[string]any
}

func (f *fakeTelegram) handler(t *testing.T) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var in map[string]any
		_ = json.NewDecoder(r.Body).Decode(&in)
		f.mu.Lock()
		defer f.mu.Unlock()
		method := r.URL.Path[strings.LastIndexByte(r.URL.Path, '/')+1:]
		switch method {
		case "sendMessage":
			f.next++
			f.sent = append(f.sent, in)
			json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": map[string]any{"message_id": f.next}})
		case "editMessageText":
			f.edits = append(f.edits, in)
			w.Write([]byte(`{"ok":true,"result":true}`))
		default:
			w.Write([]byte(`{"ok":true,"result":true}`))
		}
	})
}

func (f *fakeTelegram) lastSent() map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.sent[len(f.sent)-1]
}

func (f *fakeTelegram) sentTexts() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, m := range f.sent {
		out = append(out, m["text"].(string))
	}
	return out
}

type fakePublisher struct {
	mu    sync.Mutex
	posts []publish.Post
	err   error
	hang  bool
}

func (f *fakePublisher) Platform() string           { return "bluesky" }
func (f *fakePublisher) Ready(config.Account) error { return nil }
func (f *fakePublisher) Publish(_ context.Context, _ config.Account, p publish.Post) (publish.Result, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.posts = append(f.posts, p)
	if f.err != nil {
		return publish.Result{}, f.err
	}
	return publish.Result{ExternalID: "3pub", URL: "https://bsky.app/profile/ol1n.bsky.social/post/3pub"}, nil
}

const admin = int64(4242)

func publishEnv(t *testing.T) (*env, *fakeTelegram, *fakePublisher) {
	e := setup(t)
	tg := &fakeTelegram{}
	srv := httptest.NewServer(tg.handler(t))
	t.Cleanup(srv.Close)
	pub := &fakePublisher{}
	h := e.hub
	h.files.Apps[0].Hashtags = []string{"kidsapps"}
	h.files.Accounts.Accounts = []config.Account{
		{Platform: "bluesky", Handle: "ol1n.bsky.social", Read: platform.ViaNone, Publish: platform.ViaAPI, Lang: "en"},
		{Platform: "x", Handle: "ol1n", Read: platform.ViaApify, Publish: platform.ViaIFTTT, IFTTTEvent: "publish_x", Lang: "en"},
		{Platform: "reddit", Handle: "ol1n", Read: platform.ViaApify, Publish: platform.ViaManual, Lang: "en", MaxPerDay: 1},
		{Platform: "mastodon", Handle: "ol1n@m.social", Read: platform.ViaNone, Publish: platform.ViaAPI, Lang: "cs"},
	}
	h.files.Accounts.ApifyTasks = nil
	h.bot = &tgbot.Client{Token: "123:abc", Base: srv.URL, HTTP: srv.Client()}
	h.admins = []int64{admin}
	h.publishers = map[string]publish.Publisher{"bluesky": pub}
	h.ownHosts = []string{"olin.now"}
	h.after = func(time.Duration, func()) {}
	clock := time.Date(2030, 1, 1, 12, 0, 0, 0, time.UTC)
	h.now = func() time.Time { return clock }
	if err := h.SyncConfig(context.Background()); err != nil {
		t.Fatal(err)
	}
	return e, tg, pub
}

func (e *env) message(text string) {
	e.hub.handleUpdate(context.Background(), tgbot.Update{Message: &tgbot.Message{
		MessageID: 1, From: &tgbot.User{ID: admin}, Chat: tgbot.Chat{ID: admin}, Text: text}})
}

func (e *env) press(data string) {
	e.hub.handleUpdate(context.Background(), tgbot.Update{CallbackQuery: &tgbot.CallbackQuery{
		ID: "cb", From: tgbot.User{ID: admin}, Message: &tgbot.Message{Chat: tgbot.Chat{ID: admin}}, Data: data}})
}

func (e *env) reply(to int64, text string) {
	e.hub.handleUpdate(context.Background(), tgbot.Update{Message: &tgbot.Message{
		MessageID: 99, From: &tgbot.User{ID: admin}, Chat: tgbot.Chat{ID: admin}, Text: text,
		ReplyToMessage: &tgbot.Message{MessageID: to}}})
}

func (e *env) draftStatus(platformName string) string {
	e.t.Helper()
	var s string
	err := e.st.Pool.QueryRow(context.Background(),
		`SELECT d.status FROM drafts d JOIN accounts a ON a.id = d.account_id WHERE a.platform = $1 ORDER BY d.id DESC LIMIT 1`, platformName).Scan(&s)
	if err != nil {
		e.t.Fatal(err)
	}
	return s
}

func (e *env) draftID(platformName string) int64 {
	e.t.Helper()
	var id int64
	_ = e.st.Pool.QueryRow(context.Background(),
		`SELECT d.id FROM drafts d JOIN accounts a ON a.id = d.account_id WHERE a.platform = $1 ORDER BY d.id DESC LIMIT 1`, platformName).Scan(&id)
	return id
}

func TestPostApprovePublish(t *testing.T) {
	e, tg, pub := publishEnv(t)
	e.message("/post swypekids SwypeKids 2.0 is out\nOffline mode is here. https://olin.now/swypekids")
	texts := tg.sentTexts()
	// Header plus bluesky, x, reddit; the cs-only Mastodon account is left out.
	if len(texts) != 4 || !strings.HasPrefix(texts[0], "Kampaň #") {
		t.Fatalf("%q", texts)
	}
	if n := e.count(`SELECT count(*) FROM drafts`); n != 3 {
		t.Fatalf("drafts %d", n)
	}
	e.press("c:1:a")
	if err := e.hub.publishDue(context.Background()); err != nil {
		t.Fatal(err)
	}
	if s := e.draftStatus("bluesky"); s != "published" {
		t.Fatalf("bluesky %s", s)
	}
	p := pub.posts[0]
	if !strings.Contains(p.Text, "#kidsapps") || !strings.Contains(p.Link, "utm_source=bluesky") {
		t.Fatalf("%+v", p)
	}
	if n := e.count(`SELECT count(*) FROM content_items WHERE source_platform = 'bluesky' AND external_id = '3pub'`); n != 1 {
		t.Fatal("published post must become a content item")
	}
	// X went to IFTTT with value1..3; Reddit got a manual card with a compose link.
	var x triggered
	for _, s := range e.sentEvents() {
		if s.event == "publish_x" {
			x = s
		}
	}
	if !strings.Contains(x.payload["value1"].(string), "SwypeKids 2.0") || e.draftStatus("x") != "sent" {
		t.Fatalf("ifttt %+v status %s", x, e.draftStatus("x"))
	}
	card := tg.lastSent()
	if e.draftStatus("reddit") != "sent" || !strings.Contains(card["text"].(string), "Ručně: Reddit") ||
		!strings.Contains(mustJSON(card["reply_markup"]), "reddit.com/submit") {
		t.Fatalf("manual card %+v", card)
	}
	// "Vyšlo" on the manual card asks for the URL; the answer publishes the draft.
	rid := e.draftID("reddit")
	e.press("u:" + itoa(rid) + ":y")
	prompt := tg.lastSent()
	e.reply(int64(len(tg.sentTexts())), "tady https://www.reddit.com/r/kids/comments/1abc/swypekids/")
	if s := e.draftStatus("reddit"); s != "published" || prompt["reply_markup"] == nil {
		t.Fatalf("reddit %s", s)
	}
	if n := e.count(`SELECT count(*) FROM content_items WHERE source_platform = 'reddit' AND external_id = '1abc'`); n != 1 {
		t.Fatal("pasted URL must register the post")
	}
}

func TestSentDraftMatchedByCollect(t *testing.T) {
	e, _, _ := publishEnv(t)
	e.message("/post swypekids Big update for SwypeKids today")
	e.press("d:" + itoa(e.draftID("x")) + ":a")
	_ = e.hub.publishDue(context.Background())
	if e.draftStatus("x") != "sent" {
		t.Fatal(e.draftStatus("x"))
	}
	// The Apify profile scrape later finds the tweet.
	res, err := e.ingestX(context.Background(), "Big update for SwypeKids today https://t.co/x")
	if err != nil || len(res.Matched) != 1 || e.draftStatus("x") != "published" {
		t.Fatalf("%+v %v %s", res, err, e.draftStatus("x"))
	}
}

func TestStrandedDraftBecomesUnknown(t *testing.T) {
	e, tg, pub := publishEnv(t)
	e.message("/post swypekids Crash test for SwypeKids")
	e.press("d:" + itoa(e.draftID("bluesky")) + ":a")
	// Simulate a crash after the claim: the draft is left in publishing.
	if _, err := e.st.Pool.Exec(context.Background(), `UPDATE drafts SET status = 'publishing' WHERE status = 'approved'`); err != nil {
		t.Fatal(err)
	}
	if err := e.hub.recoverStranded(context.Background()); err != nil {
		t.Fatal(err)
	}
	if s := e.draftStatus("bluesky"); s != "unknown" {
		t.Fatal(s)
	}
	if !strings.Contains(tg.lastSent()["text"].(string), "nevím, jestli post vyšel") {
		t.Fatalf("%+v", tg.lastSent())
	}
	// Nothing is retried on its own …
	_ = e.hub.publishDue(context.Background())
	if len(pub.posts) != 0 {
		t.Fatal("an unknown draft must not be republished automatically")
	}
	// … only after "Nevyšlo, znovu".
	e.press("u:" + itoa(e.draftID("bluesky")) + ":r")
	_ = e.hub.publishDue(context.Background())
	if len(pub.posts) != 1 || e.draftStatus("bluesky") != "published" {
		t.Fatalf("posts %d status %s", len(pub.posts), e.draftStatus("bluesky"))
	}
}

func TestUncertainPublishAsksAndRejectedFails(t *testing.T) {
	e, _, pub := publishEnv(t)
	pub.err = errors.New("connection reset")
	e.message("/post swypekids Flaky network for SwypeKids")
	e.press("d:" + itoa(e.draftID("bluesky")) + ":a")
	_ = e.hub.publishDue(context.Background())
	if s := e.draftStatus("bluesky"); s != "unknown" {
		t.Fatal(s)
	}
	pub.err = publish.Reject(errors.New("image too big"))
	e.message("/post swypekids Second try for SwypeKids")
	e.press("d:" + itoa(e.draftID("bluesky")) + ":a")
	_ = e.hub.publishDue(context.Background())
	if s := e.draftStatus("bluesky"); s != "failed" {
		t.Fatal(s)
	}
}

func TestLimitsOnApproval(t *testing.T) {
	e, tg, _ := publishEnv(t)
	e.message("/post swypekids First SwypeKids post")
	e.press("d:" + itoa(e.draftID("reddit")) + ":a")
	e.message("/post swypekids Second SwypeKids post")
	e.press("d:" + itoa(e.draftID("reddit")) + ":a")
	if s := e.draftStatus("reddit"); s != "draft" {
		t.Fatalf("max_per_day must block the second approval: %s", s)
	}
	// Edit with a text over the Bluesky limit keeps the prompt open.
	e.press("d:" + itoa(e.draftID("bluesky")) + ":e")
	promptID := int64(len(tg.sentTexts()))
	e.reply(promptID, strings.Repeat("dlouhé ", 60))
	if !strings.Contains(tg.lastSent()["text"].(string), "Pořád moc dlouhé") {
		t.Fatalf("%+v", tg.lastSent())
	}
	e.reply(promptID, "Kratší text")
	var text string
	_ = e.st.Pool.QueryRow(context.Background(), `SELECT text FROM drafts WHERE id = $1`, e.draftID("bluesky")).Scan(&text)
	if text != "Kratší text" {
		t.Fatal(text)
	}
}

func TestNonAdminIgnored(t *testing.T) {
	e, tg, _ := publishEnv(t)
	e.hub.handleUpdate(context.Background(), tgbot.Update{Message: &tgbot.Message{
		MessageID: 1, From: &tgbot.User{ID: 666}, Chat: tgbot.Chat{ID: 666}, Text: "/post swypekids hacked"}})
	if len(tg.sentTexts()) != 0 || e.count(`SELECT count(*) FROM campaigns`) != 0 {
		t.Fatal("non-admin must be ignored")
	}
}

func TestParseWhen(t *testing.T) {
	now := time.Date(2030, 1, 1, 12, 0, 0, 0, time.UTC) // 13:00 in Prague
	for in, want := range map[string]string{
		"18:30":            "2030-01-01 18:30",
		"9:00":             "2030-01-02 09:00",
		"zítra 9:00":       "2030-01-02 09:00",
		"2030-02-03 10:15": "2030-02-03 10:15",
		"+2h":              "2030-01-01 15:00",
	} {
		got, err := parseWhen(in, now)
		if err != nil || got.In(prague).Format("2006-01-02 15:04") != want {
			t.Errorf("%q → %v %v, want %s", in, got.In(prague), err, want)
		}
	}
	if _, err := parseWhen("včera", now); err == nil {
		t.Error("garbage must fail")
	}
}

func itoa(n int64) string { b, _ := json.Marshal(n); return string(b) }

func mustJSON(v any) string { b, _ := json.Marshal(v); return string(b) }

func (e *env) ingestX(ctx context.Context, body string) (IngestResult, error) {
	var res IngestResult
	err := e.st.Tx(ctx, func(q *db.Queries) error {
		var err error
		res, err = e.hub.Ingest(ctx, q, Source{RunID: "scrape-1", AccountKey: "x:ol1n"}, collectors.Batch{Snapshots: []collectors.Snapshot{
			{Platform: "x", Content: &collectors.Content{ExternalID: "777", URL: "https://x.com/ol1n/status/777", Body: body}},
		}}, e.hub.now())
		return err
	})
	return res, err
}

func TestVideoLimitsOnApproval(t *testing.T) {
	e, _, pub := publishEnv(t)
	ctx := context.Background()
	dur, w, hh := int32(200_000), int32(1080), int32(1920)
	a, err := e.st.InsertMediaAsset(ctx, db.InsertMediaAssetParams{Token: "tok", Path: "/nonexistent.mp4", Name: "v.mp4", Mime: "video/mp4",
		Bytes: 20 << 20, Sha256: "x", Width: &w, Height: &hh, DurationMs: &dur, ExpiresAt: e.hub.now().Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	e.message("/post swypekids media=" + itoa(a.ID) + " Trailer for SwypeKids")
	e.press("d:" + itoa(e.draftID("bluesky")) + ":a")
	if s := e.draftStatus("bluesky"); s != "draft" || len(pub.posts) != 0 {
		t.Fatalf("a 200 s video must not be approved for Bluesky: %s", s)
	}
}
