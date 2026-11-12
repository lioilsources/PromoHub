package hub

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/lioilsources/promohub/internal/collectors"
	"github.com/lioilsources/promohub/internal/config"
	"github.com/lioilsources/promohub/internal/creds"
	"github.com/lioilsources/promohub/internal/platform"
	"github.com/lioilsources/promohub/internal/rules"
	"github.com/lioilsources/promohub/internal/store/db"
)

const (
	collectTimeout = 2 * time.Minute
	maxBackoff     = 6 * time.Hour
	notConnected   = 6 * time.Hour // re-check a missing secret this often (it shows in `promohub accounts`)
	failAlertAt    = 3             // consecutive failures that fire account.error (once)
)

type jobKey struct {
	accountID  int64
	capability string
}

// job is one scheduled (account, capability) pass of an official-API read adapter.
type job struct {
	acct  config.Account
	cap   string
	every time.Duration
	c     collectors.Collector
}

// planJobs derives the schedule from accounts.yaml: every account read via api whose platform
// has an adapter, one job per capability the registry lists.
func (h *Hub) planJobs(ctx context.Context) error {
	h.jobs = map[jobKey]job{}
	for _, a := range h.files.Accounts.Accounts {
		if a.Read != platform.ViaAPI {
			continue
		}
		c, ok := h.collectors[a.Platform]
		if !ok {
			h.log.Warn("no read adapter yet", "account", a.Key())
			continue
		}
		spec, _ := platform.Lookup(a.Platform)
		id := h.accountIDs[a.Key()]
		for _, capability := range spec.Capabilities() {
			if capability == platform.CapSearch && len(h.terms) == 0 {
				continue
			}
			if err := h.store.EnsureCollectorState(ctx, db.EnsureCollectorStateParams{AccountID: id, Capability: capability}); err != nil {
				return fmt.Errorf("collector state %s/%s: %w", a.Key(), capability, err)
			}
			h.jobs[jobKey{id, capability}] = job{acct: a, cap: capability, every: spec.Intervals[capability], c: c}
		}
	}
	return nil
}

// searchTerms are the words a keyword search looks for: app names and their keywords.
func searchTerms(f *config.Files) []string {
	var out []string
	for _, a := range f.Apps {
		out = append(out, a.Name)
		out = append(out, a.Keywords...)
	}
	return out
}

// collectDue runs every job whose next_run_at has passed, one at a time.
func (h *Hub) collectDue(ctx context.Context) error {
	rows, err := h.store.DueCollectorStates(ctx, h.now())
	if err != nil {
		return err
	}
	for _, row := range rows {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		j, ok := h.jobs[jobKey{row.AccountID, row.Capability}]
		if !ok {
			// No longer configured (read path changed, account removed): park it.
			if err := h.store.PostponeCollector(ctx, db.PostponeCollectorParams{
				AccountID: row.AccountID, Capability: row.Capability, NextRunAt: h.now().Add(24 * time.Hour),
			}); err != nil {
				return err
			}
			continue
		}
		h.runJob(ctx, j, row)
	}
	return nil
}

func (h *Hub) runJob(ctx context.Context, j job, row db.CollectorState) {
	id := row.AccountID
	start := h.now().UTC()
	if err := j.c.Ready(j.acct, j.cap); err != nil {
		h.collectFailed(ctx, j, row, start, err)
		return
	}
	cctx, cancel := context.WithTimeout(ctx, collectTimeout)
	b, cursor, err := j.c.Collect(cctx, collectors.Request{Account: j.acct, Capability: j.cap, Cursor: row.Cursor, Terms: h.terms})
	cancel()
	if err == nil && j.cap == platform.CapProfile && !hasAccountSnapshot(b) {
		// An empty answer that claims success is how scrapers and APIs fail silently.
		err = errors.New("profile returned no follower count")
	}
	if err != nil {
		h.collectFailed(ctx, j, row, start, err)
		return
	}
	// The run ID makes a pass idempotent: a retry of the same slot hits the snapshot unique index.
	src := Source{RunID: fmt.Sprintf("%s:%d:%d", j.cap, id, row.NextRunAt.Unix()), AccountKey: j.acct.Key()}
	var res IngestResult
	err = h.store.Tx(ctx, func(q *db.Queries) error {
		var err error
		if res, err = h.Ingest(ctx, q, src, b, start); err != nil {
			return err
		}
		var cur []byte
		if len(cursor) > 0 {
			cur = cursor
		}
		return q.CollectorOK(ctx, db.CollectorOKParams{
			Cursor: cur, NextRunAt: start.Add(h.jitter(j.every)), RanAt: &start, AccountID: id, Capability: j.cap,
		})
	})
	if err != nil {
		h.collectFailed(ctx, j, row, start, fmt.Errorf("ingest: %w", err))
		return
	}
	h.metrics.Inc("promohub_collect_total", "platform", j.acct.Platform, "capability", j.cap, "result", "ok")
	h.log.Info("collected", "account", j.acct.Key(), "capability", j.cap, "skipped", b.Skipped,
		"new_inbound", res.NewInbound, "new_content", res.NewContent, "snapshots", res.NewSnapshots, "events", res.Events)
	if res.Events > 0 {
		nudge(h.wakeEvents)
	}
}

func (h *Hub) collectFailed(ctx context.Context, j job, row db.CollectorState, at time.Time, cause error) {
	id := row.AccountID
	msg := cause.Error()
	var missing *creds.MissingError
	var limited *collectors.RateLimited
	counts, next, result := true, time.Time{}, "failed"
	switch {
	case errors.As(cause, &missing):
		counts, next, result, msg = false, at.Add(notConnected), "not_connected", "not connected: "+msg
	case errors.As(cause, &limited):
		counts, next, result = false, limited.Until, "rate_limited"
	}
	h.metrics.Inc("promohub_collect_total", "platform", j.acct.Platform, "capability", j.cap, "result", result)
	err := h.store.Tx(ctx, func(q *db.Queries) error {
		if next.IsZero() {
			// Backoff: interval (at most 15 min) · 2^previous failures, capped at 6 h.
			next = at.Add(min(maxBackoff, min(j.every, 15*time.Minute)<<min(row.FailCount, 10)))
		}
		fails, err := q.CollectorFailed(ctx, db.CollectorFailedParams{
			NextRunAt: next, RanAt: &at, LastError: &msg, Counts: counts, AccountID: id, Capability: j.cap,
		})
		if err != nil {
			return err
		}
		if !counts || fails != failAlertAt {
			return nil
		}
		v := rules.Vars{Num: map[string]float64{"fails": float64(fails)}, Str: map[string]string{
			"platform": j.acct.Platform, "account": j.acct.Key(), "capability": j.cap, "error": msg,
		}}
		_, err = h.fire(ctx, q, rules.OnAccountError, v, map[string]any{
			"platform": j.acct.Platform, "account": j.acct.Key(), "capability": j.cap, "error": msg, "fails": fails,
		})
		return err
	})
	if err != nil {
		h.log.Error("record collector failure", "account", j.acct.Key(), "capability", j.cap, "err", err)
		return
	}
	level := h.log.Warn
	if result == "not_connected" {
		level = h.log.Info
	}
	level("collect failed", "account", j.acct.Key(), "capability", j.cap, "result", result, "next", next, "err", msg)
	nudge(h.wakeEvents)
}

// jitter spreads passes by ±10 % so accounts on one platform do not poll in lockstep.
func (h *Hub) jitter(d time.Duration) time.Duration {
	return d + time.Duration((h.rand()-0.5)*0.2*float64(d))
}

func hasAccountSnapshot(b collectors.Batch) bool {
	for _, s := range b.Snapshots {
		if s.Content == nil && s.Followers != nil {
			return true
		}
	}
	return false
}
