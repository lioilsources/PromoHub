package hub

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/lioilsources/promohub/internal/collectors"
	"github.com/lioilsources/promohub/internal/differ"
	"github.com/lioilsources/promohub/internal/rules"
	"github.com/lioilsources/promohub/internal/store"
	"github.com/lioilsources/promohub/internal/store/db"
)

// Source says where a batch came from.
type Source struct {
	RunID      string // Apify run ID; makes snapshots idempotent per run
	AccountKey string // "platform:handle" of the owner, "" for keyword searches
}

// IngestResult counts what was new in a batch.
type IngestResult struct {
	NewInbound   int
	NewContent   int
	NewSnapshots int
	Events       int
	Matched      []int64     // sent drafts this batch confirmed as published
	Fresh        []freshPost // new videos of mine that may deserve a campaign
}

type freshPost struct {
	contentID       int64
	platform, title string
	url             string
}

// Ingest stores a batch and queues the events its rules fire. Everything runs on q, which the
// caller wraps in a transaction: either the whole run lands (with its events) or nothing does.
// Duplicates (same inbound external_id, same run re-delivered) are skipped and fire no rules.
func (h *Hub) Ingest(ctx context.Context, q *db.Queries, src Source, b collectors.Batch, takenAt time.Time) (IngestResult, error) {
	var res IngestResult
	acctID, hasAcct := h.accountIDs[src.AccountKey]
	var runID *string
	if src.RunID != "" {
		runID = &src.RunID
	}

	for _, s := range b.Snapshots {
		var contentID int64
		var title string
		if s.Content != nil {
			p := db.UpsertContentItemParams{
				SourcePlatform: s.Platform, ExternalID: s.Content.ExternalID,
				Url: nz(s.Content.URL), Title: nz(s.Content.Title), Body: nz(s.Content.Body),
				PublishedAt: s.Content.PublishedAt, AppID: h.appIDFor(s.Content.Title + " " + s.Content.Body),
			}
			if hasAcct {
				p.AccountID = &acctID
			}
			row, err := q.UpsertContentItem(ctx, p)
			if err != nil {
				return res, fmt.Errorf("content %s/%s: %w", s.Platform, s.Content.ExternalID, err)
			}
			contentID = row.ID
			title = firstLine(s.Content.Title, s.Content.Body)
			if row.Inserted {
				res.NewContent++
				if hasAcct {
					m, err := h.matchSent(ctx, q, acctID, row.ID, &contentRef{s.Content.ExternalID, s.Content.URL, s.Content.Body}, takenAt)
					if err != nil {
						return res, fmt.Errorf("match sent drafts: %w", err)
					}
					res.Matched = append(res.Matched, m...)
				}
				if s.Platform == "youtube" && s.Content.PublishedAt != nil && takenAt.Sub(*s.Content.PublishedAt) < freshContent {
					res.Fresh = append(res.Fresh, freshPost{row.ID, s.Platform, firstLine(s.Content.Title, s.Content.Body), s.Content.URL})
				}
				n, err := h.fire(ctx, q, rules.OnContentNew, contentVars(s), map[string]any{
					"platform": s.Platform, "url": s.Content.URL, "title": title,
				})
				if err != nil {
					return res, err
				}
				res.Events += n
			}
		} else if !hasAcct {
			continue // account-level metrics need an owner account
		}

		cur := differ.Metrics{TakenAt: takenAt, Likes: s.Likes, Views: s.Views, Reposts: s.Reposts,
			Comments: s.Comments, Followers: s.Followers, Extra: s.Extra}
		prev, base, err := h.history(ctx, q, contentID, acctID, takenAt)
		if err != nil {
			return res, err
		}
		extra, _ := json.Marshal(nonNil(s.Extra))
		p := db.InsertMetricSnapshotParams{
			Platform: s.Platform, TakenAt: takenAt, Likes: s.Likes, Views: s.Views, Reposts: s.Reposts,
			Comments: s.Comments, Followers: s.Followers, Extra: extra, SourceRunID: runID,
		}
		if contentID != 0 {
			p.ContentItemID = &contentID
		} else {
			p.AccountID = &acctID
		}
		if _, err := q.InsertMetricSnapshot(ctx, p); store.NoRows(err) {
			continue // this run already stored it
		} else if err != nil {
			return res, fmt.Errorf("snapshot: %w", err)
		}
		res.NewSnapshots++

		v := differ.Vars(cur, prev, base)
		scope := "account"
		if contentID != 0 {
			scope = "content"
		}
		v.Str["platform"], v.Str["scope"] = s.Platform, scope
		payload := map[string]any{"platform": s.Platform, "scope": scope, "metrics": metricsMap(cur), "deltas": differ.Deltas(v)}
		if s.Content != nil {
			v.Str["url"], v.Str["text"] = s.Content.URL, s.Content.Body
			payload["url"], payload["title"] = s.Content.URL, title
		} else {
			payload["account"] = src.AccountKey
		}
		n, err := h.fire(ctx, q, rules.OnMetricSnapshot, v, payload)
		if err != nil {
			return res, err
		}
		res.Events += n
		if prev != nil {
			n, err := h.fire(ctx, q, rules.OnMetricDelta, v, payload)
			if err != nil {
				return res, err
			}
			res.Events += n
		}
	}

	for _, in := range b.Inbound {
		appID, appSlug := h.appFor(in.Text)
		_, err := q.InsertInboundItem(ctx, db.InsertInboundItemParams{
			Platform: in.Platform, Kind: in.Kind, ExternalID: in.ExternalID, AppID: appID,
			Author: nz(in.Author), Text: nz(in.Text), Rating: in.Rating, Url: nz(in.URL), CreatedAt: in.CreatedAt,
		})
		if store.NoRows(err) {
			continue
		} else if err != nil {
			return res, fmt.Errorf("inbound %s/%s: %w", in.Platform, in.ExternalID, err)
		}
		res.NewInbound++
		h.metrics.Inc("promohub_inbound_new_total", "platform", in.Platform, "kind", in.Kind)

		v := rules.Vars{Num: map[string]float64{}, Str: map[string]string{
			"platform": in.Platform, "kind": in.Kind, "author": in.Author, "text": in.Text, "url": in.URL, "app": appSlug,
		}}
		payload := map[string]any{
			"platform": in.Platform, "kind": in.Kind, "author": in.Author, "text": in.Text,
			"url": in.URL, "app": appSlug, "external_id": in.ExternalID,
		}
		if in.Rating != nil {
			v.Num["rating"] = float64(*in.Rating)
			payload["rating"] = *in.Rating
		}
		if in.CreatedAt != nil {
			payload["created_at"] = in.CreatedAt.Format(time.RFC3339)
		}
		n, err := h.fire(ctx, q, "inbound."+in.Kind, v, payload)
		if err != nil {
			return res, err
		}
		res.Events += n
	}
	return res, nil
}

// history returns the previous snapshot and the 24 h baseline for one subject.
func (h *Hub) history(ctx context.Context, q *db.Queries, contentID, acctID int64, takenAt time.Time) (prev, base *differ.Metrics, err error) {
	var p, b db.MetricSnapshot
	cut := takenAt.Add(-24 * time.Hour)
	if contentID != 0 {
		p, err = q.LatestContentSnapshot(ctx, &contentID)
	} else {
		p, err = q.LatestAccountSnapshot(ctx, &acctID)
	}
	if store.NoRows(err) {
		return nil, nil, nil
	} else if err != nil {
		return nil, nil, err
	}
	if contentID != 0 {
		b, err = q.ContentSnapshotAtOrBefore(ctx, db.ContentSnapshotAtOrBeforeParams{ContentItemID: &contentID, TakenAt: cut})
		if store.NoRows(err) {
			b, err = q.EarliestContentSnapshot(ctx, &contentID)
		}
	} else {
		b, err = q.AccountSnapshotAtOrBefore(ctx, db.AccountSnapshotAtOrBeforeParams{AccountID: &acctID, TakenAt: cut})
		if store.NoRows(err) {
			b, err = q.EarliestAccountSnapshot(ctx, &acctID)
		}
	}
	if err != nil {
		return nil, nil, err
	}
	return toMetrics(p), toMetrics(b), nil
}

func toMetrics(s db.MetricSnapshot) *differ.Metrics {
	m := &differ.Metrics{TakenAt: s.TakenAt, Likes: s.Likes, Views: s.Views, Reposts: s.Reposts, Comments: s.Comments, Followers: s.Followers}
	_ = json.Unmarshal(s.Extra, &m.Extra)
	return m
}

// fire evaluates the rules for a trigger and queues one event per target.
func (h *Hub) fire(ctx context.Context, q *db.Queries, on string, v rules.Vars, payload map[string]any) (int, error) {
	n := 0
	for _, m := range h.rules.Evaluate(on, v) {
		p := make(map[string]any, len(payload)+3)
		for k, x := range payload {
			p[k] = x
		}
		p["type"], p["rule"] = on, m.Rule
		p["message"] = message(on, p)
		b, err := json.Marshal(p)
		if err != nil {
			return n, err
		}
		for _, t := range m.Targets {
			rule, target := m.Rule, t
			if _, err := q.InsertEvent(ctx, db.InsertEventParams{Type: on, Rule: &rule, Target: &target, Payload: b}); err != nil {
				return n, fmt.Errorf("event: %w", err)
			}
			n++
		}
	}
	return n, nil
}

// message is a ready-to-send one-liner for Telegram/Discord and for applets that just forward text.
func message(on string, p map[string]any) string {
	s := func(k string) string { x, _ := p[k].(string); return x }
	var b strings.Builder
	switch {
	case strings.HasPrefix(on, "inbound."):
		fmt.Fprintf(&b, "[%s %s] %s", s("platform"), s("kind"), s("author"))
		if r, ok := p["rating"].(int32); ok {
			fmt.Fprintf(&b, " %s", strings.Repeat("★", int(r))+strings.Repeat("☆", max(0, 5-int(r))))
		}
		if a := s("app"); a != "" {
			fmt.Fprintf(&b, " · %s", a)
		}
		fmt.Fprintf(&b, "\n%s", truncate(s("text"), 600))
	case on == rules.OnPublishDone:
		fmt.Fprintf(&b, "[%s] published (%s) %s", s("platform"), s("via"), s("account"))
	case on == rules.OnPublishFailed:
		fmt.Fprintf(&b, "[%s] publish %s %s: %s", s("platform"), s("status"), s("account"), truncate(s("error"), 300))
	case on == rules.OnAuthExpiring:
		fmt.Fprintf(&b, "[%s] %s login: %s", s("platform"), s("account"), truncate(s("error"), 300))
	case on == rules.OnAccountError:
		fmt.Fprintf(&b, "[%s] %s %s failing: %s", s("platform"), s("account"), s("capability"), truncate(s("error"), 300))
	case on == rules.OnContentNew:
		fmt.Fprintf(&b, "[%s] new post: %s", s("platform"), truncate(s("title"), 200))
	default:
		subject := s("title")
		if subject == "" {
			subject = s("account")
		}
		fmt.Fprintf(&b, "[%s] %s", s("platform"), truncate(subject, 120))
		if m, ok := p["metrics"].(map[string]int64); ok {
			d, _ := p["deltas"].(map[string]int64)
			for _, k := range []string{"views", "likes", "reposts", "comments", "followers"} {
				x, ok := m[k]
				if !ok {
					continue
				}
				fmt.Fprintf(&b, "\n%s %d", k, x)
				if dd := d[k+"_delta_24h"]; dd != 0 {
					fmt.Fprintf(&b, " (%+d / 24h)", dd)
				} else if dd := d[k+"_delta"]; dd != 0 {
					fmt.Fprintf(&b, " (%+d)", dd)
				}
			}
		}
	}
	if u := s("url"); u != "" {
		fmt.Fprintf(&b, "\n%s", u)
	}
	fmt.Fprintf(&b, "\n— %s", s("rule"))
	return b.String()
}

func contentVars(s collectors.Snapshot) rules.Vars {
	return rules.Vars{Num: map[string]float64{}, Str: map[string]string{
		"platform": s.Platform, "url": s.Content.URL, "text": s.Content.Body, "title": s.Content.Title,
	}}
}

func metricsMap(m differ.Metrics) map[string]int64 {
	out := map[string]int64{}
	for k, v := range map[string]*int64{"likes": m.Likes, "views": m.Views, "reposts": m.Reposts, "comments": m.Comments, "followers": m.Followers} {
		if v != nil {
			out[k] = *v
		}
	}
	for k, v := range m.Extra {
		out[k] = v
	}
	return out
}

func (h *Hub) appFor(text string) (*int64, string) {
	a, ok := h.files.AppFor(text)
	if !ok {
		return nil, ""
	}
	id, ok := h.appIDs[a.Slug]
	if !ok {
		return nil, a.Slug
	}
	return &id, a.Slug
}

func (h *Hub) appIDFor(text string) *int64 {
	id, _ := h.appFor(text)
	return id
}

func nz(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func nonNil(m map[string]int64) map[string]int64 {
	if m == nil {
		return map[string]int64{}
	}
	return m
}

func firstLine(title, body string) string {
	if title != "" {
		return title
	}
	l, _, _ := strings.Cut(strings.TrimSpace(body), "\n")
	return truncate(l, 140)
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
