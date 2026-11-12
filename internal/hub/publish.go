package hub

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/lioilsources/promohub/internal/composer"
	"github.com/lioilsources/promohub/internal/config"
	"github.com/lioilsources/promohub/internal/ifttt"
	"github.com/lioilsources/promohub/internal/platform"
	"github.com/lioilsources/promohub/internal/publish"
	"github.com/lioilsources/promohub/internal/rules"
	"github.com/lioilsources/promohub/internal/store"
	"github.com/lioilsources/promohub/internal/store/db"
)

const (
	publishTimeout = 3 * time.Minute
	matchWindow    = 48 * time.Hour // a sent (IFTTT/manual) draft is matched to a collected post this long
	mediaGrace     = 24 * time.Hour // media stay public this long after the planned publish time
	maxAutoRetries = 3              // idempotent platforms only
	freshContent   = 48 * time.Hour // newer content of mine proposes a campaign
)

// idempotent platforms dedup on the attempt key, so an uncertain attempt may be retried.
var idempotent = map[string]bool{"mastodon": true}

// CampaignInput is what /post or a new video of mine proposes.
type CampaignInput struct {
	AppSlug         string
	Lang            string
	Title           string
	Body            string
	Link            string
	MediaIDs        []int64
	SourceContentID *int64
}

// CreateCampaign composes one draft per account that takes the campaign's app and language.
// Accounts whose path is not available yet (no API publisher, no IFTTT key) get a manual draft.
func (h *Hub) CreateCampaign(ctx context.Context, in CampaignInput) (db.Campaign, []db.Draft, error) {
	if in.Lang == "" {
		in.Lang = "en"
	}
	var app *config.App
	var appID *int64
	for i := range h.files.Apps {
		if h.files.Apps[i].Slug == in.AppSlug {
			app = &h.files.Apps[i]
			if id, ok := h.appIDs[in.AppSlug]; ok {
				appID = &id
			}
		}
	}
	mediaJSON, _ := json.Marshal(nonNilIDs(in.MediaIDs))
	camp, err := h.store.InsertCampaign(ctx, db.InsertCampaignParams{
		AppID: appID, SourceContentItemID: in.SourceContentID, Lang: in.Lang, Title: nz(in.Title),
		Body: in.Body, Link: nz(in.Link), Media: mediaJSON,
	})
	if err != nil {
		return camp, nil, err
	}
	cc := composer.Campaign{Slug: fmt.Sprintf("c%d", camp.ID), App: app, Lang: in.Lang, Title: in.Title,
		Body: in.Body, Link: in.Link, HasMedia: len(in.MediaIDs) > 0}
	var targets []config.Account
	for _, a := range h.files.Accounts.Accounts {
		if a.Publish != platform.ViaNone && a.Promotes(in.AppSlug) && a.Speaks(in.Lang) {
			targets = append(targets, a)
		}
	}
	bodies := h.llmBodies(ctx, cc, targets)
	var drafts []db.Draft
	for _, a := range targets {
		c := cc
		if b := strings.TrimSpace(bodies[a.Key()]); b != "" {
			c.Body = b
		}
		d := composer.Compose(a, c, h.ownHosts)
		via, note := h.effectiveVia(a)
		if note != "" {
			d.Extra["note"] = note
		}
		extra, _ := json.Marshal(d.Extra)
		row, err := h.store.InsertDraft(ctx, db.InsertDraftParams{
			CampaignID: camp.ID, AccountID: h.accountIDs[a.Key()], Via: string(via), Text: d.Text, Extra: extra,
		})
		if err != nil && !store.NoRows(err) {
			return camp, drafts, fmt.Errorf("draft %s: %w", a.Key(), err)
		}
		drafts = append(drafts, row)
	}
	return camp, drafts, nil
}

// llmBodies asks the LLM for per-account bodies; any failure means composer v0 for all.
func (h *Hub) llmBodies(ctx context.Context, c composer.Campaign, accts []config.Account) map[string]string {
	if h.llm == nil || len(accts) == 0 {
		return nil
	}
	var ts []composer.Target
	for _, a := range accts {
		spec, _ := platform.Lookup(a.Platform)
		lang := c.Lang
		if a.Lang != "any" {
			lang = a.Lang
		}
		ts = append(ts, composer.Target{Key: a.Key(), Platform: a.Platform, Limit: spec.TextLimit, Lang: lang})
	}
	out, err := h.llm.Rewrite(ctx, c, ts)
	if err != nil {
		h.log.Warn("llm composer failed, using v0", "err", err)
		return nil
	}
	return out
}

// effectiveVia degrades a configured publish path that cannot work in this process.
func (h *Hub) effectiveVia(a config.Account) (platform.Via, string) {
	switch a.Publish {
	case platform.ViaAPI:
		p, ok := h.publishers[a.Platform]
		if !ok {
			return platform.ViaManual, "no API publisher for " + a.Platform + " yet"
		}
		if err := p.Ready(a); err != nil {
			return platform.ViaManual, err.Error()
		}
	case platform.ViaIFTTT:
		if h.ifttt == nil || h.ifttt.Key == "" {
			return platform.ViaManual, "IFTTT_KEY is not set"
		}
	}
	return a.Publish, ""
}

// approveDraft checks the draft against the platform limit and the account's daily cap.
func (h *Hub) approveDraft(ctx context.Context, d db.Draft, at *time.Time) (db.Draft, error) {
	acct, ok := h.accounts[d.AccountID]
	if !ok {
		return d, errors.New("účet už není v accounts.yaml")
	}
	spec, _ := platform.Lookup(acct.Platform)
	if !spec.Fits(d.Text) {
		return d, fmt.Errorf("text má %d/%d, zkrať ho", spec.Length(d.Text), spec.TextLimit)
	}
	if acct.Platform == "pinterest" && !h.campaignHasImage(ctx, d.CampaignID) {
		return d, errors.New("Pinterest potřebuje obrázek")
	}
	if d.Via == string(platform.ViaAPI) {
		ms, _ := h.campaignMedia(ctx, d.CampaignID)
		for _, m := range ms {
			if m.IsVideo() {
				if why := spec.CheckVideo(m.Bytes, m.DurationMs); why != "" {
					return d, errors.New(why)
				}
			}
		}
	}
	now := h.now()
	if acct.MaxPerDay > 0 {
		n, err := h.store.ApprovedSince(ctx, db.ApprovedSinceParams{AccountID: d.AccountID, ID: d.ID, ApprovedAt: ptr(now.Add(-24 * time.Hour))})
		if err != nil {
			return d, err
		}
		if n >= int64(acct.MaxPerDay) {
			return d, fmt.Errorf("%s má limit %d postů za 24 h", acct.Key(), acct.MaxPerDay)
		}
	}
	row, err := h.store.ApproveDraft(ctx, db.ApproveDraftParams{Now: now, ScheduledAt: at, ID: d.ID})
	if store.NoRows(err) {
		return d, fmt.Errorf("draft je ve stavu %s", d.Status)
	} else if err != nil {
		return d, err
	}
	until := now
	if at != nil {
		until = *at
	}
	h.extendMedia(ctx, d.CampaignID, until.Add(mediaGrace))
	nudge(h.wakePublish)
	return row, nil
}

func (h *Hub) campaignHasImage(ctx context.Context, campaignID int64) bool {
	ms, _ := h.campaignMedia(ctx, campaignID)
	for _, m := range ms {
		if m.IsImage() {
			return true
		}
	}
	return false
}

func (h *Hub) extendMedia(ctx context.Context, campaignID int64, until time.Time) {
	ms, _ := h.campaignMedia(ctx, campaignID)
	for _, m := range ms {
		_ = h.store.ExtendMedia(ctx, db.ExtendMediaParams{ID: m.ID, ExpiresAt: until})
	}
}

func (h *Hub) campaignMedia(ctx context.Context, campaignID int64) ([]publish.Media, error) {
	c, err := h.store.GetCampaign(ctx, campaignID)
	if err != nil {
		return nil, err
	}
	var ids []int64
	_ = json.Unmarshal(c.Media, &ids)
	var out []publish.Media
	for _, id := range ids {
		a, err := h.store.GetMediaAsset(ctx, id)
		if err != nil {
			return nil, fmt.Errorf("media %d: %w", id, err)
		}
		m := publish.Media{ID: a.ID, Path: a.Path, URL: h.env.PublicBaseURL + "/media/" + a.Token, Mime: a.Mime,
			Alt: deref(a.Alt), Bytes: a.Bytes}
		if a.Width != nil && a.Height != nil {
			m.Width, m.Height = int(*a.Width), int(*a.Height)
		}
		if a.DurationMs != nil {
			m.DurationMs = int(*a.DurationMs)
		}
		out = append(out, m)
	}
	return out, nil
}

// publishDue hands every due approved draft to its path, one at a time.
func (h *Hub) publishDue(ctx context.Context) error {
	for ctx.Err() == nil {
		d, err := h.store.ClaimDueDraft(ctx, db.ClaimDueDraftParams{AttemptKey: ptr(attemptKey()), Now: h.now()})
		if store.NoRows(err) {
			return nil
		} else if err != nil {
			return err
		}
		h.publishOne(ctx, d)
	}
	return ctx.Err()
}

func attemptKey() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func (h *Hub) publishOne(ctx context.Context, d db.Draft) {
	acct, ok := h.accounts[d.AccountID]
	if !ok {
		h.finish(ctx, d, "failed", publish.Result{}, errors.New("account no longer configured"))
		return
	}
	camp, err := h.store.GetCampaign(ctx, d.CampaignID)
	if err != nil {
		h.log.Error("publish: campaign", "draft", d.ID, "err", err)
		return // stays in publishing → unknown on restart, asked in Telegram
	}
	ms, err := h.campaignMedia(ctx, d.CampaignID)
	if err != nil {
		h.finish(ctx, d, "failed", publish.Result{}, err)
		return
	}
	extra := map[string]string{}
	_ = json.Unmarshal(d.Extra, &extra)
	post := publish.Post{DraftID: d.ID, Text: d.Text, Title: extra["title"], Link: extra["link"], Lang: camp.Lang,
		Media: ms, Extra: extra, AttemptKey: deref(d.AttemptKey)}
	if post.Link == "" {
		post.Link = deref(camp.Link)
	}
	h.refreshCard(ctx, d, acct)

	pctx, cancel := context.WithTimeout(ctx, publishTimeout)
	defer cancel()
	var res publish.Result
	switch platform.Via(d.Via) {
	case platform.ViaAPI:
		p, ok := h.publishers[acct.Platform]
		if !ok {
			err = publish.Reject(fmt.Errorf("no API publisher for %s", acct.Platform))
			break
		}
		res, err = p.Publish(pctx, acct, post)
	case platform.ViaIFTTT:
		img := ""
		for _, m := range ms {
			if m.IsImage() {
				img = m.URL
				break
			}
		}
		err = h.ifttt.TriggerValues(pctx, acct.IFTTTEvent, post.Text, post.Link, img)
		var se *ifttt.StatusError
		if errors.As(err, &se) && !se.Retryable() {
			err = publish.Reject(err)
		}
		res.Sent = true
	case platform.ViaManual:
		err = h.sendManualCard(pctx, d, acct, post)
		if err != nil {
			err = publish.Reject(err)
		}
		res.Sent = true
	default:
		err = publish.Reject(fmt.Errorf("unknown via %q", d.Via))
	}

	switch {
	case err == nil && res.Sent:
		h.finish(ctx, d, "sent", res, nil)
		if res.Note != "" {
			h.sendNote(ctx, d, acct, post, res.Note)
		}
		if d.Via == string(platform.ViaIFTTT) {
			h.kickProfileTask(acct)
		}
	case err == nil:
		h.finish(ctx, d, "published", res, nil)
	case publish.Classify(err) == publish.Failed:
		h.finish(ctx, d, "failed", res, err)
	case idempotent[acct.Platform] && d.Attempts < maxAutoRetries:
		h.log.Warn("publish uncertain, retrying with the same idempotency key", "draft", d.ID, "err", err)
		if _, rerr := h.store.RetryDraft(ctx, d.ID); rerr != nil {
			h.log.Error("retry draft", "draft", d.ID, "err", rerr)
		}
	default:
		h.finish(ctx, d, "unknown", res, err)
	}
}

// finish stores the outcome, registers a published post as my content and fires publish.*.
func (h *Hub) finish(ctx context.Context, d db.Draft, status string, res publish.Result, cause error) {
	acct := h.accounts[d.AccountID]
	now := h.now().UTC()
	var row db.Draft
	err := h.store.Tx(ctx, func(q *db.Queries) error {
		p := db.FinishDraftParams{Status: status, ID: d.ID, ExternalID: nz(res.ExternalID), Url: nz(res.URL)}
		if status == "published" || status == "sent" {
			p.PublishedAt = &now
		}
		if cause != nil {
			p.Error = ptr(cause.Error())
		}
		if status == "published" {
			id, err := h.registerContent(ctx, q, d, acct, res.ExternalID, res.URL, now)
			if err != nil {
				return err
			}
			p.ContentItemID = id
		}
		var err error
		if row, err = q.FinishDraft(ctx, p); err != nil {
			return err
		}
		return h.firePublish(ctx, q, row, acct)
	})
	if err != nil {
		h.log.Error("finish draft", "draft", d.ID, "status", status, "err", err)
		return
	}
	h.metrics.Inc("promohub_publish_total", "platform", acct.Platform, "via", d.Via, "result", status)
	h.log.Info("draft finished", "draft", d.ID, "account", acct.Key(), "via", d.Via, "status", status, "url", res.URL, "err", errString(cause))
	h.refreshCard(ctx, row, acct)
	if status == "unknown" {
		h.askUnknown(ctx, row, acct)
	}
	nudge(h.wakeEvents)
}

// registerContent upserts the published post into content_items so collectors attach metrics
// to it instead of seeing a "new" post.
func (h *Hub) registerContent(ctx context.Context, q *db.Queries, d db.Draft, acct config.Account, extID, url string, at time.Time) (*int64, error) {
	if extID == "" && url == "" {
		return nil, nil
	}
	if url != "" {
		if id := platform.ExternalID(acct.Platform, url); id != url || extID == "" {
			extID = id
		}
	}
	camp, err := q.GetCampaign(ctx, d.CampaignID)
	if err != nil {
		return nil, err
	}
	acctID := d.AccountID
	row, err := q.UpsertContentItem(ctx, db.UpsertContentItemParams{
		AccountID: &acctID, AppID: camp.AppID, SourcePlatform: acct.Platform, ExternalID: extID,
		Url: nz(url), Body: nz(d.Text), PublishedAt: &at,
	})
	if err != nil {
		return nil, fmt.Errorf("content: %w", err)
	}
	return &row.ID, nil
}

func (h *Hub) firePublish(ctx context.Context, q *db.Queries, d db.Draft, acct config.Account) error {
	on := ""
	switch d.Status {
	case "published":
		on = rules.OnPublishDone
	case "failed", "unknown":
		on = rules.OnPublishFailed
	default:
		return nil
	}
	v := rules.Vars{Num: map[string]float64{}, Str: map[string]string{
		"platform": acct.Platform, "account": acct.Key(), "via": d.Via, "status": d.Status,
		"url": deref(d.PublishedUrl), "error": deref(d.Error),
	}}
	_, err := h.fire(ctx, q, on, v, map[string]any{
		"platform": acct.Platform, "account": acct.Key(), "via": d.Via, "status": d.Status,
		"url": deref(d.PublishedUrl), "error": deref(d.Error), "draft": d.ID,
	})
	return err
}

// confirmPublished links a sent/unknown draft to the post at url (Olin pasted it).
func (h *Hub) confirmPublished(ctx context.Context, d db.Draft, url string) (db.Draft, error) {
	acct, ok := h.accounts[d.AccountID]
	if !ok {
		return d, errors.New("účet už není v accounts.yaml")
	}
	var row db.Draft
	err := h.store.Tx(ctx, func(q *db.Queries) error {
		id, err := h.registerContent(ctx, q, d, acct, "", url, h.now().UTC())
		if err != nil {
			return err
		}
		if row, err = q.MarkDraftPublished(ctx, db.MarkDraftPublishedParams{
			Url: nz(url), ExternalID: nz(platform.ExternalID(acct.Platform, url)), ContentItemID: id, ID: d.ID,
		}); err != nil {
			return err
		}
		return h.firePublish(ctx, q, row, acct)
	})
	if store.NoRows(err) {
		return d, fmt.Errorf("draft je ve stavu %s", d.Status)
	}
	if err == nil {
		h.refreshCard(ctx, row, acct)
		nudge(h.wakeEvents)
	}
	return row, err
}

// matchSent pairs a freshly collected post of an account with a draft that went out through
// IFTTT or by hand (no post ID came back) when the texts start alike.
func (h *Hub) matchSent(ctx context.Context, q *db.Queries, acctID, contentID int64, c *contentRef, takenAt time.Time) ([]int64, error) {
	ds, err := q.SentDraftsForAccount(ctx, db.SentDraftsForAccountParams{AccountID: acctID, PublishedAt: ptr(takenAt.Add(-matchWindow))})
	if err != nil || len(ds) == 0 {
		return nil, err
	}
	var matched []int64
	for _, d := range ds {
		if !similar(d.Text, c.body) {
			continue
		}
		row, err := q.MarkDraftPublished(ctx, db.MarkDraftPublishedParams{
			Url: nz(c.url), ExternalID: nz(c.externalID), ContentItemID: &contentID, ID: d.ID,
		})
		if err != nil {
			return matched, err
		}
		if err := h.firePublish(ctx, q, row, h.accounts[acctID]); err != nil {
			return matched, err
		}
		matched = append(matched, d.ID)
		break // one post confirms one draft
	}
	return matched, nil
}

type contentRef struct{ externalID, url, body string }

// similar compares the first 40 letters and digits of two texts, ignoring links and case.
func similar(a, b string) bool {
	na, nb := norm(a), norm(b)
	if len(na) < 8 || len(nb) < 8 {
		return false
	}
	n := min(len(na), len(nb), 40)
	return string(na[:n]) == string(nb[:n])
}

func norm(s string) []rune {
	s = urlRe.ReplaceAllString(s, " ")
	var out []rune
	for _, r := range strings.ToLower(s) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			out = append(out, r)
		}
	}
	return out
}

// kickProfileTask runs the account's Apify profile task 10 minutes after an IFTTT publish, so
// the sent draft gets matched to its post soon (at most once an hour per task).
func (h *Hub) kickProfileTask(acct config.Account) {
	if h.apify == nil {
		return
	}
	for _, t := range h.files.Accounts.ApifyTasks {
		if t.Account != acct.Key() || !strings.HasSuffix(t.Kind, "-profile") {
			continue
		}
		h.kickMu.Lock()
		last := h.kicked[t.Name]
		if !last.IsZero() && h.now().Sub(last) < time.Hour {
			h.kickMu.Unlock()
			return
		}
		h.kicked[t.Name] = h.now()
		h.kickMu.Unlock()
		task := t
		h.after(10*time.Minute, func() {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			if _, err := h.apify.RunTask(ctx, task.ID, task.MaxItems); err != nil {
				h.log.Warn("kick profile task", "task", task.Name, "err", err)
			}
		})
		return
	}
}

// recoverStranded turns drafts a crash left in publishing into unknown and asks about each.
func (h *Hub) recoverStranded(ctx context.Context) error {
	ds, err := h.store.StrandedDrafts(ctx)
	if err != nil {
		return err
	}
	for _, d := range ds {
		acct := h.accounts[d.AccountID]
		h.log.Warn("draft stranded in publishing", "draft", d.ID, "account", acct.Key())
		h.refreshCard(ctx, d, acct)
		h.askUnknown(ctx, d, acct)
	}
	return nil
}

func nonNilIDs(ids []int64) []int64 {
	if ids == nil {
		return []int64{}
	}
	return ids
}

func ptr[T any](v T) *T { return &v }

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// afterIngest runs the follow-ups of a committed batch: refresh the cards of matched drafts
// and propose campaigns for fresh videos.
func (h *Hub) afterIngest(ctx context.Context, res IngestResult) {
	for _, id := range res.Matched {
		if d, err := h.store.GetDraft(ctx, id); err == nil {
			h.refreshCard(ctx, d, h.accounts[d.AccountID])
		}
	}
	for _, f := range res.Fresh {
		h.proposeFor(ctx, f)
	}
}

// proposeFor drafts a cross-post campaign for a new video and sends it for approval.
func (h *Hub) proposeFor(ctx context.Context, f freshPost) {
	if h.bot == nil || len(h.admins) == 0 {
		return
	}
	slug := ""
	if a, ok := h.files.AppFor(f.title); ok {
		slug = a.Slug
	}
	id := f.contentID
	camp, drafts, err := h.CreateCampaign(ctx, CampaignInput{AppSlug: slug, Title: f.title, Body: f.title, Link: f.url, SourceContentID: &id})
	if err != nil {
		h.log.Error("propose campaign", "content", f.contentID, "err", err)
		return
	}
	// The video's own account would just repost itself.
	var rest []db.Draft
	for _, d := range drafts {
		if h.accounts[d.AccountID].Platform == f.platform {
			_, _ = h.store.SkipDraft(ctx, d.ID)
			continue
		}
		rest = append(rest, d)
	}
	if len(rest) > 0 {
		h.announce(ctx, h.admins[0], camp, rest)
	}
}
