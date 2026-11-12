package hub

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/lioilsources/promohub/internal/platform"
	"github.com/lioilsources/promohub/internal/publish"
	"github.com/lioilsources/promohub/internal/store"
	"github.com/lioilsources/promohub/internal/store/db"
	"github.com/lioilsources/promohub/internal/tgbot"
)

// sendInbound delivers an inbound.* event to a Telegram sink through the bot with a
// "↩️ Odpovědět" button when the platform can take a reply. ok=false: use the plain sender.
func (h *Hub) sendInbound(ctx context.Context, chat string, payload []byte) (bool, error) {
	if h.bot == nil {
		return false, nil
	}
	var p struct {
		Type      string `json:"type"`
		Platform  string `json:"platform"`
		Message   string `json:"message"`
		InboundID int64  `json:"inbound_id"`
	}
	if json.Unmarshal(payload, &p) != nil || !strings.HasPrefix(p.Type, "inbound.") || p.InboundID == 0 {
		return false, nil
	}
	if _, ok := h.repliers[p.Platform]; !ok {
		return false, nil
	}
	id, err := strconv.ParseInt(chat, 10, 64)
	if err != nil {
		return false, nil // @channel sinks get the plain message
	}
	_, err = h.bot.Send(ctx, id, p.Message, tgbot.Opts{Keyboard: tgbot.Keyboard{{
		{Text: "↩️ Odpovědět", Data: fmt.Sprintf("r:%d:x", p.InboundID)},
	}}})
	return true, err
}

// replyPrompt asks for the reply text to an inbound item.
func (h *Hub) replyPrompt(ctx context.Context, chat, id int64) string {
	it, err := h.store.GetInboundItem(ctx, id)
	if err != nil {
		return "položka neexistuje"
	}
	if it.RepliedAt != nil {
		return "už zodpovězeno: " + deref(it.ReplyUrl)
	}
	h.prompt(ctx, chat, fmt.Sprintf("Odpověď pro %s (%s):", deref(it.Author), it.Platform), "tvoje odpověď", fmt.Sprintf("reply:%d", id))
	return ""
}

// sendReply posts Olin's answer to an inbound item.
func (h *Hub) sendReply(ctx context.Context, id int64, text string) (string, bool) {
	it, err := h.store.GetInboundItem(ctx, id)
	if err != nil {
		return "položka neexistuje", true
	}
	r, ok := h.repliers[it.Platform]
	if !ok {
		return "na " + it.Platform + " hub odpovídat neumí", true
	}
	acct, ok := h.accounts[derefID(it.AccountID)]
	if !ok {
		// Keyword searches have no owner; answer from my only account on the platform.
		if id := h.firstAccount(it.Platform); id != nil {
			acct, ok = h.accounts[*id]
		}
	}
	if !ok {
		return "nevím, ze kterého účtu odpovědět", true
	}
	spec, _ := platform.Lookup(acct.Platform)
	if !spec.Fits(text) {
		return fmt.Sprintf("Moc dlouhé: %d/%d. Odpověz znovu na otázku výše.", spec.Length(text), spec.TextLimit), false
	}
	rctx, cancel := context.WithTimeout(ctx, publishTimeout)
	defer cancel()
	res, err := r.Reply(rctx, acct, publish.InboundRef{ExternalID: it.ExternalID, URL: deref(it.Url), Author: deref(it.Author)}, text)
	if err != nil {
		h.log.Warn("reply failed", "inbound", id, "account", acct.Key(), "err", err)
		if publish.Classify(err) == publish.Unknown {
			return "Nevím, jestli odpověď odešla (" + oneLine(err.Error(), 150) + "). Zkontroluj to na platformě.", true
		}
		return "Odpověď se nepodařila: " + oneLine(err.Error(), 200), true
	}
	_ = h.store.MarkInboundReplied(ctx, db.MarkInboundRepliedParams{ID: id, ReplyUrl: nz(res.URL)})
	h.metrics.Inc("promohub_publish_total", "platform", acct.Platform, "via", "reply", "result", "published")
	if res.URL != "" {
		return "Odesláno: " + res.URL, true
	}
	return "Odesláno.", true
}

func derefID(p *int64) int64 {
	if p == nil {
		return 0
	}
	return *p
}

// cmdStats compares the accounts: followers now and their change over 7 and 30 days, posts in
// the last 30 days and the mean engagement per post.
func (h *Hub) cmdStats(ctx context.Context) string {
	now := h.now()
	content, err := h.store.AccountContentStats(ctx, ptr(now.Add(-30*24*time.Hour)))
	if err != nil {
		return "Statistiky se nepodařilo načíst: " + err.Error()
	}
	byAcct := map[int64]db.AccountContentStatsRow{}
	for _, c := range content {
		byAcct[c.AccountID] = c
	}
	var b strings.Builder
	w := tabwriter.NewWriter(&b, 0, 0, 1, ' ', 0)
	fmt.Fprintln(w, "účet\tsled.\tΔ7d\tΔ30d\tpostů\teng/post")
	rows := 0
	for _, a := range h.files.Accounts.Accounts {
		id := h.accountIDs[a.Key()]
		cur, err := h.store.LatestAccountSnapshot(ctx, &id)
		c := byAcct[id]
		if err != nil && c.Posts == 0 {
			continue // nothing collected yet
		}
		f, d7, d30 := "–", "–", "–"
		if err == nil && cur.Followers != nil {
			f = compact(*cur.Followers)
			d7 = h.delta(ctx, id, *cur.Followers, now.Add(-7*24*time.Hour))
			d30 = h.delta(ctx, id, *cur.Followers, now.Add(-30*24*time.Hour))
		}
		eng := "–"
		if c.Posts > 0 {
			eng = strconv.FormatFloat(math.Round(c.Engagement*10)/10, 'f', -1, 64)
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%d\t%s\n", shortKey(a.Platform, a.User()), f, d7, d30, c.Posts, eng)
		rows++
	}
	w.Flush()
	if rows == 0 {
		return "Zatím žádná data. Collectory se rozjedou po prvním průchodu."
	}
	return "<pre>" + htmlEscape(b.String()) + "</pre>"
}

func (h *Hub) delta(ctx context.Context, id, cur int64, at time.Time) string {
	s, err := h.store.AccountSnapshotAtOrBefore(ctx, db.AccountSnapshotAtOrBeforeParams{AccountID: &id, TakenAt: at})
	if store.NoRows(err) || err != nil || s.Followers == nil {
		return "–"
	}
	return fmt.Sprintf("%+d", cur-*s.Followers)
}

var platformShort = map[string]string{
	"bluesky": "bsky", "mastodon": "masto", "instagram": "ig", "facebook": "fb", "youtube": "yt", "tiktok": "tt",
	"threads": "th", "telegram": "tg", "discord": "dc", "hackernews": "hn", "producthunt": "ph", "devto": "dev",
	"linkedin": "li", "pinterest": "pin", "reddit": "rd", "x": "x",
}

func shortKey(p, user string) string {
	s := platformShort[p]
	if s == "" {
		s = p
	}
	k := s + ":" + user
	if r := []rune(k); len(r) > 16 {
		k = string(r[:15]) + "…"
	}
	return k
}

func compact(n int64) string {
	switch {
	case n >= 1_000_000:
		return strconv.FormatFloat(float64(n)/1e6, 'f', 1, 64) + "M"
	case n >= 10_000:
		return strconv.FormatFloat(float64(n)/1e3, 'f', 1, 64) + "k"
	}
	return strconv.FormatInt(n, 10)
}

func htmlEscape(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(s)
}
