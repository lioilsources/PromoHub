package hub

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lioilsources/promohub/internal/collectors"
	"github.com/lioilsources/promohub/internal/config"
	"github.com/lioilsources/promohub/internal/publish"
	"github.com/lioilsources/promohub/internal/rules"
	"github.com/lioilsources/promohub/internal/store/db"
)

type fakeReplier struct {
	mu      sync.Mutex
	replies []string
	to      []publish.InboundRef
}

func (f *fakeReplier) Platform() string { return "bluesky" }
func (f *fakeReplier) Reply(_ context.Context, _ config.Account, to publish.InboundRef, text string) (publish.Result, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.replies, f.to = append(f.replies, text), append(f.to, to)
	return publish.Result{URL: "https://bsky.app/profile/ol1n.bsky.social/post/3rep"}, nil
}

func TestReplyFromTelegram(t *testing.T) {
	e, tg, _ := publishEnv(t)
	h := e.hub
	r := &fakeReplier{}
	h.repliers = map[string]publish.Replier{"bluesky": r}
	h.files.Rules.Sinks.Telegram = map[string]string{"me": "4242"}
	h.files.Rules.Rules = []config.RuleSpec{{Name: "m", On: rules.OnInboundMention, Then: config.StringSet{"telegram:me"}}}
	h.rules, _ = rules.New(h.files.Rules)
	ctx := context.Background()
	err := e.st.Tx(ctx, func(q *db.Queries) error {
		_, err := h.Ingest(ctx, q, Source{RunID: "s1", AccountKey: "bluesky:ol1n.bsky.social"}, collectors.Batch{Inbound: []collectors.Inbound{{
			Platform: "bluesky", Kind: "mention", ExternalID: "at://did:plc:fan/app.bsky.feed.post/1", Author: "fan.bsky.social", Text: "love SwypeKids",
		}}}, h.now())
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := h.deliverPending(ctx); err != nil {
		t.Fatal(err)
	}
	card := tg.lastSent()
	if !strings.Contains(mustJSON(card["reply_markup"]), `r:1:x`) {
		t.Fatalf("no reply button: %+v", card)
	}
	e.press("r:1:x")
	e.reply(int64(len(tg.sentTexts())), "Díky moc!")
	if len(r.replies) != 1 || r.replies[0] != "Díky moc!" || r.to[0].ExternalID != "at://did:plc:fan/app.bsky.feed.post/1" {
		t.Fatalf("%+v", r)
	}
	if n := e.count(`SELECT count(*) FROM inbound_items WHERE replied_at IS NOT NULL AND reply_url LIKE '%3rep'`); n != 1 {
		t.Fatal("reply not recorded")
	}
	if !strings.Contains(tg.lastSent()["text"].(string), "Odesláno") {
		t.Fatalf("%+v", tg.lastSent())
	}
}

func TestStats(t *testing.T) {
	e, _, _ := publishEnv(t)
	h := e.hub
	ctx := context.Background()
	now := h.now()
	for i, f := range []int64{100, 130, 160} {
		at := now.Add(time.Duration(i-2) * 10 * 24 * time.Hour) // 20 days ago, 10 days ago, now
		followers := f
		err := e.st.Tx(ctx, func(q *db.Queries) error {
			_, err := h.Ingest(ctx, q, Source{RunID: "p" + itoa(int64(i)), AccountKey: "bluesky:ol1n.bsky.social"}, collectors.Batch{Snapshots: []collectors.Snapshot{
				{Platform: "bluesky", Followers: &followers},
				{Platform: "bluesky", Content: &collectors.Content{ExternalID: "3k" + itoa(int64(i)), Body: "post", PublishedAt: &at}, Likes: &followers},
			}}, at)
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	out := h.cmdStats(ctx)
	// Δ30d is unknown: tracking started 20 days ago.
	if !strings.Contains(out, "bsky:ol1n") || !strings.Contains(out, "160   +30 –    3     130") {
		t.Fatalf("%s", out)
	}
}
