package hub

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	_ "time/tzdata" // Europe/Prague in the distroless image

	"github.com/lioilsources/promohub/internal/config"
	"github.com/lioilsources/promohub/internal/media"
	"github.com/lioilsources/promohub/internal/platform"
	"github.com/lioilsources/promohub/internal/publish"
	"github.com/lioilsources/promohub/internal/store"
	"github.com/lioilsources/promohub/internal/store/db"
	"github.com/lioilsources/promohub/internal/tgbot"
)

// The approval bot (Prompts/01-ACCOUNTS_PLAN.md §8.4). Only updates from TELEGRAM_ADMIN_IDS
// are handled; everything else is dropped and logged. Texts that others wrote are never sent
// with a parse mode.

const (
	kvOffset    = "tg:offset"
	pollTimeout = 50 * time.Second
	mediaTTL    = 7 * 24 * time.Hour
)

var urlRe = regexp.MustCompile(`https?://[^\s<>"]+`)

var prague = func() *time.Location {
	l, err := time.LoadLocation("Europe/Prague")
	if err != nil {
		return time.UTC
	}
	return l
}()

// botLoop long-polls Telegram. The offset lives in kv, so a restart neither replays nor skips.
func (h *Hub) botLoop(ctx context.Context) {
	for ctx.Err() == nil {
		off := int64(0)
		if v, err := h.store.GetKV(ctx, kvOffset); err == nil {
			off, _ = strconv.ParseInt(v, 10, 64)
		}
		ups, err := h.bot.Updates(ctx, off, pollTimeout)
		if err != nil {
			if ctx.Err() == nil {
				h.log.Warn("telegram getUpdates", "err", err)
				h.sleep(ctx, 10*time.Second)
			}
			continue
		}
		for _, u := range ups {
			h.handleUpdate(ctx, u)
			if err := h.store.SetKV(ctx, db.SetKVParams{Key: kvOffset, Value: strconv.FormatInt(u.UpdateID+1, 10)}); err != nil {
				h.log.Error("save telegram offset", "err", err)
			}
		}
	}
}

func (h *Hub) sleep(ctx context.Context, d time.Duration) {
	select {
	case <-ctx.Done():
	case <-time.After(d):
	}
}

func (h *Hub) isAdmin(id int64) bool {
	for _, a := range h.admins {
		if a == id {
			return true
		}
	}
	return false
}

func (h *Hub) handleUpdate(ctx context.Context, u tgbot.Update) {
	switch {
	case u.CallbackQuery != nil:
		cq := u.CallbackQuery
		if !h.isAdmin(cq.From.ID) || cq.Message == nil {
			h.log.Warn("telegram callback from non-admin dropped", "user", cq.From.ID)
			return
		}
		msg := h.handleCallback(ctx, cq.Message.Chat.ID, cq.Data)
		_ = h.bot.Answer(ctx, cq.ID, msg)
	case u.Message != nil:
		m := u.Message
		if m.From == nil || !h.isAdmin(m.From.ID) {
			h.log.Warn("telegram message from non-admin dropped", "chat", m.Chat.ID)
			return
		}
		if reply := h.handleMessage(ctx, m); reply != "" {
			_, _ = h.bot.Send(ctx, m.Chat.ID, reply, tgbot.Opts{ReplyTo: m.MessageID})
		}
	}
}

func (h *Hub) handleMessage(ctx context.Context, m *tgbot.Message) string {
	text := m.Text
	if text == "" {
		text = m.Caption
	}
	if m.ReplyToMessage != nil {
		key := replyKey(m.Chat.ID, m.ReplyToMessage.MessageID)
		if action, err := h.store.GetKV(ctx, key); err == nil {
			out, done := h.handleReply(ctx, action, text)
			if done {
				_ = h.store.DeleteKV(ctx, key)
			}
			return out
		}
	}
	cmd, rest, _ := strings.Cut(strings.TrimSpace(text), " ")
	cmd, _, _ = strings.Cut(cmd, "@") // /post@promohub_bot
	switch cmd {
	case "/post":
		return h.cmdPost(ctx, m, rest)
	case "/start", "/help":
		return helpText
	case "":
		return ""
	}
	return "Neznámý příkaz. " + helpText
}

const helpText = `/post [slug] [cs|en] text… [odkaz] [media=ID]
Vytvoří kampaň a pošle draft pro každý účet. Fotka s popiskem /post … se přiloží.
Delší video nahraj přes make media a přidej media=ID.`

// cmdPost parses "/post [app] [lang] [media=ID…] text… [link]" and proposes a campaign.
func (h *Hub) cmdPost(ctx context.Context, m *tgbot.Message, rest string) string {
	in := CampaignInput{}
	words := strings.Fields(rest)
	used := 0
opts:
	for len(words) > 0 {
		w := words[0]
		switch {
		case in.AppSlug == "" && h.appSlug(w) != "":
			in.AppSlug = h.appSlug(w)
		case w == "cs" || w == "en":
			in.Lang = w
		case strings.HasPrefix(w, "media="):
			id, err := strconv.ParseInt(strings.TrimPrefix(w, "media="), 10, 64)
			if err != nil {
				return "media= chce číslo z make media"
			}
			if _, err := h.store.GetMediaAsset(ctx, id); err != nil {
				return fmt.Sprintf("médium %d neexistuje", id)
			}
			in.MediaIDs = append(in.MediaIDs, id)
		default:
			break opts
		}
		words = words[1:]
		used++
	}
	body := strings.TrimSpace(dropFields(rest, used)) // keeps the line breaks of the text
	if links := urlRe.FindAllString(body, -1); len(links) > 0 {
		in.Link = links[len(links)-1]
		body = strings.TrimSpace(strings.Replace(body, in.Link, "", 1))
	}
	if first, more, ok := strings.Cut(body, "\n"); ok && strings.TrimSpace(more) != "" {
		in.Title = strings.TrimSpace(first)
	}
	in.Body = body
	if in.AppSlug == "" {
		if a, ok := h.files.AppFor(body); ok {
			in.AppSlug = a.Slug
		}
	}
	if in.Body == "" && in.Link == "" {
		return helpText
	}
	if id, err := h.attachFromMessage(ctx, m); err != nil {
		return "Médium se nepodařilo uložit: " + err.Error()
	} else if id != 0 {
		in.MediaIDs = append(in.MediaIDs, id)
	}
	camp, drafts, err := h.CreateCampaign(ctx, in)
	if err != nil {
		h.log.Error("create campaign", "err", err)
		return "Kampaň se nepodařilo vytvořit: " + err.Error()
	}
	if len(drafts) == 0 {
		return "Žádný účet nebere tuhle appku a jazyk (accounts.yaml: apps, lang, publish)."
	}
	h.announce(ctx, m.Chat.ID, camp, drafts)
	return ""
}

// dropFields removes the first n whitespace-separated fields of s and keeps the rest verbatim.
func dropFields(s string, n int) string {
	for ; n > 0; n-- {
		s = strings.TrimLeft(s, " \t\n")
		i := strings.IndexAny(s, " \t\n")
		if i < 0 {
			return ""
		}
		s = s[i:]
	}
	return s
}

// appSlug matches an option word exactly ("swypekids", "#swypekids"); "SwypeKids" stays text.
func (h *Hub) appSlug(w string) string {
	w = strings.TrimPrefix(w, "#")
	for _, a := range h.files.Apps {
		if a.Slug == w {
			return a.Slug
		}
	}
	return ""
}

// attachFromMessage stores the photo/video/document of a /post message as a media asset.
func (h *Hub) attachFromMessage(ctx context.Context, m *tgbot.Message) (int64, error) {
	var fileID, name string
	switch {
	case len(m.Photo) > 0:
		p := m.Photo[len(m.Photo)-1] // largest size
		fileID, name = p.FileID, fmt.Sprintf("tg-%d.jpg", m.MessageID)
	case m.Video != nil:
		fileID, name = m.Video.FileID, nameOr(m.Video.FileName, fmt.Sprintf("tg-%d.mp4", m.MessageID))
	case m.Document != nil:
		fileID, name = m.Document.FileID, nameOr(m.Document.FileName, fmt.Sprintf("tg-%d", m.MessageID))
	default:
		return 0, nil
	}
	rc, err := h.bot.Download(ctx, fileID)
	if err != nil {
		return 0, err
	}
	defer rc.Close()
	a, err := h.SaveMedia(ctx, rc, name, "")
	return a.ID, err
}

func nameOr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// SaveMedia stores a file and registers it for /media/{token}.
func (h *Hub) SaveMedia(ctx context.Context, r io.Reader, name, alt string) (db.MediaAsset, error) {
	s, err := media.Save(h.env.MediaDir, r, name)
	if err != nil {
		return db.MediaAsset{}, err
	}
	p := db.InsertMediaAssetParams{Token: media.Token(), Path: s.Path, Name: s.Name, Mime: s.Mime, Bytes: s.Bytes,
		Sha256: s.Sha256, Alt: nz(alt), ExpiresAt: h.now().Add(mediaTTL)}
	if s.Width > 0 {
		w, hh := int32(s.Width), int32(s.Height)
		p.Width, p.Height = &w, &hh
	}
	return h.store.InsertMediaAsset(ctx, p)
}

// announce sends the campaign header and one card per draft.
func (h *Hub) announce(ctx context.Context, chat int64, c db.Campaign, drafts []db.Draft) {
	head := fmt.Sprintf("Kampaň #%d · %s · %s · %d draftů", c.ID, h.appName(c.AppID), c.Lang, len(drafts))
	if l := deref(c.Link); l != "" {
		head += "\n" + l
	}
	id, err := h.bot.Send(ctx, chat, head, tgbot.Opts{Keyboard: tgbot.Keyboard{{
		{Text: "✅ Schválit vše", Data: fmt.Sprintf("c:%d:a", c.ID)},
		{Text: "⏭ Zahodit vše", Data: fmt.Sprintf("c:%d:s", c.ID)},
	}}})
	if err != nil {
		h.log.Error("announce campaign", "campaign", c.ID, "err", err)
		return
	}
	_ = h.store.SetCampaignMessage(ctx, db.SetCampaignMessageParams{ID: c.ID, TgChatID: &chat, TgMessageID: &id})
	for _, d := range drafts {
		acct := h.accounts[d.AccountID]
		text, kb := h.card(d, acct)
		mid, err := h.bot.Send(ctx, chat, text, tgbot.Opts{Keyboard: kb})
		if err != nil {
			h.log.Error("draft card", "draft", d.ID, "err", err)
			continue
		}
		_ = h.store.SetDraftMessage(ctx, db.SetDraftMessageParams{ID: d.ID, TgChatID: &chat, TgMessageID: &mid})
	}
}

func (h *Hub) appName(id *int64) string {
	if id == nil {
		return "bez appky"
	}
	for slug, aid := range h.appIDs {
		if aid == *id {
			for _, a := range h.files.Apps {
				if a.Slug == slug {
					return a.Name
				}
			}
		}
	}
	return "?"
}

var statusLine = map[string]string{
	"approved":   "→ schváleno, čeká na odeslání",
	"publishing": "→ publikuji…",
	"published":  "✅ publikováno",
	"sent":       "📨 předáno",
	"failed":     "❌ selhalo",
	"skipped":    "⏭ přeskočeno",
	"unknown":    "❓ nejisté, viz dotaz níže",
}

// card renders a draft and its buttons for its current state.
func (h *Hub) card(d db.Draft, acct config.Account) (string, tgbot.Keyboard) {
	spec, _ := platform.Lookup(acct.Platform)
	extra := map[string]string{}
	_ = json.Unmarshal(d.Extra, &extra)
	var b strings.Builder
	limit := ""
	if spec.TextLimit > 0 {
		limit = fmt.Sprintf(" · %d/%d", spec.Length(d.Text), spec.TextLimit)
	}
	fmt.Fprintf(&b, "#d%d · %s · %s · %s%s\n", d.ID, spec.Title, acct.Handle, d.Via, limit)
	if t := extra["title"]; t != "" {
		fmt.Fprintf(&b, "Titulek: %s\n", t)
	}
	b.WriteString("──────\n")
	b.WriteString(d.Text)
	if l := extra["link"]; l != "" {
		fmt.Fprintf(&b, "\n🔗 %s", l)
	}
	if n := extra["note"]; n != "" {
		fmt.Fprintf(&b, "\nℹ️ ručně, protože: %s", n)
	}
	if s := statusLine[d.Status]; s != "" {
		b.WriteString("\n\n" + s)
		if d.ScheduledAt != nil && d.Status == "approved" {
			fmt.Fprintf(&b, " v %s", d.ScheduledAt.In(prague).Format("2. 1. 15:04"))
		}
		if u := deref(d.PublishedUrl); u != "" {
			b.WriteString(": " + u)
		}
		if e := deref(d.Error); e != "" && d.Status != "published" {
			b.WriteString(": " + oneLine(e, 300))
		}
	}
	id := d.ID
	switch d.Status {
	case "draft", "failed":
		return b.String(), tgbot.Keyboard{{
			{Text: "✅ Schválit", Data: fmt.Sprintf("d:%d:a", id)},
			{Text: "✏️ Upravit", Data: fmt.Sprintf("d:%d:e", id)},
			{Text: "🕒 Později", Data: fmt.Sprintf("d:%d:t", id)},
			{Text: "⏭", Data: fmt.Sprintf("d:%d:s", id)},
		}}
	case "approved":
		return b.String(), tgbot.Keyboard{{{Text: "⏭ Zrušit", Data: fmt.Sprintf("d:%d:s", id)}}}
	case "sent":
		return b.String(), tgbot.Keyboard{{{Text: "🔗 Vyšlo, pošlu URL", Data: fmt.Sprintf("u:%d:y", id)}}}
	}
	return b.String(), nil
}

func (h *Hub) refreshCard(ctx context.Context, d db.Draft, acct config.Account) {
	if h.bot == nil || d.TgChatID == nil || d.TgMessageID == nil {
		return
	}
	if fresh, err := h.store.GetDraft(ctx, d.ID); err == nil {
		d = fresh
	}
	text, kb := h.card(d, acct)
	if err := h.bot.Edit(ctx, *d.TgChatID, *d.TgMessageID, text, tgbot.Opts{Keyboard: kb}); err != nil {
		h.log.Debug("edit card", "draft", d.ID, "err", err)
	}
}

// adminChat is where proposals and questions go when no chat is known.
func (h *Hub) adminChat(d *db.Draft) int64 {
	if d != nil && d.TgChatID != nil {
		return *d.TgChatID
	}
	if len(h.admins) > 0 {
		return h.admins[0]
	}
	return 0
}

func (h *Hub) askUnknown(ctx context.Context, d db.Draft, acct config.Account) {
	if h.bot == nil {
		return
	}
	text := fmt.Sprintf("❓ #d%d %s %s: nevím, jestli post vyšel (%s). Zkontroluj profil.", d.ID, acct.Platform, acct.Handle, oneLine(deref(d.Error), 200))
	_, err := h.bot.Send(ctx, h.adminChat(&d), text, tgbot.Opts{Keyboard: tgbot.Keyboard{{
		{Text: "✅ Vyšlo, pošlu URL", Data: fmt.Sprintf("u:%d:y", d.ID)},
		{Text: "🔁 Nevyšlo, znovu", Data: fmt.Sprintf("u:%d:r", d.ID)},
		{Text: "⏭ Zahodit", Data: fmt.Sprintf("d:%d:s", d.ID)},
	}}})
	if err != nil {
		h.log.Error("ask unknown", "draft", d.ID, "err", err)
	}
}

// sendManualCard is the manual publish path: the text to copy, the compose link and a button
// that asks for the URL afterwards.
func (h *Hub) sendManualCard(ctx context.Context, d db.Draft, acct config.Account, p publish.Post) error {
	if h.bot == nil {
		return errors.New("telegram bot is not configured")
	}
	spec, _ := platform.Lookup(acct.Platform)
	var b strings.Builder
	fmt.Fprintf(&b, "📋 Ručně: %s %s (#d%d)\n", html.EscapeString(spec.Title), html.EscapeString(acct.Handle), d.ID)
	if p.Title != "" {
		fmt.Fprintf(&b, "\nTitulek:\n<code>%s</code>\n", html.EscapeString(p.Title))
	}
	fmt.Fprintf(&b, "\nText:\n<code>%s</code>\n", html.EscapeString(p.Text))
	if p.Link != "" && !strings.Contains(p.Text, p.Link) {
		fmt.Fprintf(&b, "\nOdkaz:\n<code>%s</code>\n", html.EscapeString(p.Link))
	}
	for _, m := range p.Media {
		fmt.Fprintf(&b, "\nMédium: %s", html.EscapeString(m.URL))
	}
	row := []tgbot.Button{{Text: "🔗 Vyšlo, pošlu URL", Data: fmt.Sprintf("u:%d:y", d.ID)}}
	text := p.Text
	if p.Link != "" && !strings.Contains(text, p.Link) {
		text += "\n\n" + p.Link
	}
	if u := platform.Intent(acct.Platform, acct.APIHost(), text, p.Link, p.Title); u != "" && len(u) < 2000 {
		row = append([]tgbot.Button{{Text: "✍️ Otevřít " + spec.Title, URL: u}}, row...)
	}
	_, err := h.bot.Send(ctx, h.adminChat(&d), b.String(), tgbot.Opts{HTML: true, Keyboard: tgbot.Keyboard{row}})
	return err
}

func replyKey(chat, msg int64) string { return fmt.Sprintf("tg:reply:%d:%d", chat, msg) }

// prompt sends a ForceReply question and remembers what the answer is for.
func (h *Hub) prompt(ctx context.Context, chat int64, text, placeholder, action string) {
	id, err := h.bot.Send(ctx, chat, text, tgbot.Opts{ForceReply: true, Placeholder: placeholder})
	if err != nil {
		h.log.Error("telegram prompt", "err", err)
		return
	}
	_ = h.store.SetKV(ctx, db.SetKVParams{Key: replyKey(chat, id), Value: action})
}

// handleCallback runs a button press and returns the toast text.
func (h *Hub) handleCallback(ctx context.Context, chat int64, data string) string {
	parts := strings.Split(data, ":")
	if len(parts) != 3 {
		return "?"
	}
	id, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		return "?"
	}
	kind, act := parts[0], parts[2]
	if kind == "c" {
		return h.campaignAction(ctx, id, act)
	}
	d, err := h.store.GetDraft(ctx, id)
	if err != nil {
		return "draft neexistuje"
	}
	acct := h.accounts[d.AccountID]
	switch kind + ":" + act {
	case "d:a":
		row, err := h.approveDraft(ctx, d, nil)
		if err != nil {
			return err.Error()
		}
		h.refreshCard(ctx, row, acct)
		return "schváleno"
	case "d:s":
		row, err := h.store.SkipDraft(ctx, id)
		if err != nil {
			return "nejde přeskočit (" + d.Status + ")"
		}
		h.refreshCard(ctx, row, acct)
		return "přeskočeno"
	case "d:e":
		h.prompt(ctx, chat, fmt.Sprintf("Nový text pro #d%d (%s):", id, acct.Key()), "nový text", fmt.Sprintf("edit:%d", id))
		return ""
	case "d:t":
		h.prompt(ctx, chat, fmt.Sprintf("Kdy publikovat #d%d? Např. 18:30, zítra 9:00, 2026-11-20 10:00, +2h (čas Praha)", id), "18:30", fmt.Sprintf("when:%d", id))
		return ""
	case "u:y":
		h.prompt(ctx, chat, fmt.Sprintf("Pošli URL postu #d%d (%s):", id, acct.Key()), "https://…", fmt.Sprintf("url:%d", id))
		return ""
	case "u:r":
		if _, err := h.store.RequeueUnknownDraft(ctx, id); err != nil {
			return "nejde (" + d.Status + ")"
		}
		row, _ := h.store.GetDraft(ctx, id)
		h.refreshCard(ctx, row, acct)
		nudge(h.wakePublish)
		return "zkusím znovu"
	}
	return "?"
}

func (h *Hub) campaignAction(ctx context.Context, id int64, act string) string {
	ds, err := h.store.CampaignDrafts(ctx, id)
	if err != nil {
		return "kampaň neexistuje"
	}
	ok, bad := 0, []string{}
	for _, d := range ds {
		if d.Status != "draft" {
			continue
		}
		acct := h.accounts[d.AccountID]
		var row db.Draft
		if act == "a" {
			row, err = h.approveDraft(ctx, d, nil)
		} else {
			row, err = h.store.SkipDraft(ctx, d.ID)
		}
		if err != nil {
			bad = append(bad, fmt.Sprintf("#d%d: %v", d.ID, err))
			continue
		}
		ok++
		h.refreshCard(ctx, row, acct)
	}
	msg := fmt.Sprintf("%d hotovo", ok)
	if len(bad) > 0 {
		msg += "; " + strings.Join(bad, "; ")
	}
	return oneLine(msg, 190)
}

// handleReply answers a ForceReply prompt. done=false keeps the prompt open (bad input).
func (h *Hub) handleReply(ctx context.Context, action, text string) (string, bool) {
	kind, idStr, _ := strings.Cut(action, ":")
	id, _ := strconv.ParseInt(idStr, 10, 64)
	d, err := h.store.GetDraft(ctx, id)
	if err != nil {
		return "draft neexistuje", true
	}
	acct := h.accounts[d.AccountID]
	text = strings.TrimSpace(text)
	switch kind {
	case "edit":
		spec, _ := platform.Lookup(acct.Platform)
		if !spec.Fits(text) {
			return fmt.Sprintf("Pořád moc dlouhé: %d/%d. Odpověz znovu na otázku výše.", spec.Length(text), spec.TextLimit), false
		}
		row, err := h.store.SetDraftText(ctx, db.SetDraftTextParams{ID: id, Text: text, Extra: d.Extra})
		if store.NoRows(err) {
			return "Draft už nejde upravit (" + d.Status + ").", true
		} else if err != nil {
			return err.Error(), false
		}
		h.refreshCard(ctx, row, acct)
		return "Upraveno, schval tlačítkem na kartě.", true
	case "when":
		at, err := parseWhen(text, h.now())
		if err != nil {
			return err.Error() + " Odpověz znovu na otázku výše.", false
		}
		row, err := h.approveDraft(ctx, d, &at)
		if err != nil {
			return err.Error(), true
		}
		h.refreshCard(ctx, row, acct)
		return "Naplánováno na " + at.In(prague).Format("2. 1. 15:04") + ".", true
	case "url":
		u := urlRe.FindString(text)
		if _, err := url.Parse(u); err != nil || u == "" {
			return "To není URL. Odpověz znovu na otázku výše.", false
		}
		if _, err := h.confirmPublished(ctx, d, u); err != nil {
			return err.Error(), true
		}
		return "Díky, sleduju metriky.", true
	}
	return "?", true
}

// parseWhen reads "18:30", "zítra 9:00", "2026-11-20 10:00" or "+2h" in Prague time.
func parseWhen(s string, now time.Time) (time.Time, error) {
	s = strings.ToLower(strings.TrimSpace(s))
	local := now.In(prague)
	if strings.HasPrefix(s, "+") {
		d, err := time.ParseDuration(strings.TrimPrefix(s, "+"))
		if err != nil || d <= 0 {
			return time.Time{}, errors.New("Nerozumím, zkus +2h nebo +30m.")
		}
		return now.Add(d), nil
	}
	day := local
	if rest, ok := strings.CutPrefix(s, "zítra"); ok {
		day, s = local.AddDate(0, 0, 1), strings.TrimSpace(rest)
	} else if rest, ok := strings.CutPrefix(s, "zitra"); ok {
		day, s = local.AddDate(0, 0, 1), strings.TrimSpace(rest)
	}
	if t, err := time.ParseInLocation("2006-01-02 15:04", s, prague); err == nil {
		if !t.After(now) {
			return time.Time{}, errors.New("Ten čas už byl.")
		}
		return t, nil
	}
	hm, err := time.Parse("15:04", s)
	if err != nil {
		if hm, err = time.Parse("15:04", "0"+s); err != nil {
			return time.Time{}, errors.New("Nerozumím času.")
		}
	}
	t := time.Date(day.Year(), day.Month(), day.Day(), hm.Hour(), hm.Minute(), 0, 0, prague)
	if !t.After(now) {
		t = t.AddDate(0, 0, 1)
	}
	return t, nil
}

func oneLine(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}
