package hub

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/lioilsources/promohub/internal/composer"
	"github.com/lioilsources/promohub/internal/sparkbroker"
	"github.com/lioilsources/promohub/internal/store/db"
	"github.com/lioilsources/promohub/internal/tgbot"
)

// The night LLM window (Prompts/01-ACCOUNTS_PLAN.md §13/8, agreed with Director): the hub
// keeps the queue; once a night it looks for pending campaigns and only then leases the LLM
// from spark-broker, composes, and releases the lease right away. No pending campaign means
// no lease, so the GPU never switches profile for nothing.

const (
	kvWindowLast = "llm_window:last"
	windowSpan   = 6 * time.Hour // how long after At a missed window may still start
)

// Window configures the night LLM window.
type Window struct {
	Broker    *sparkbroker.Client
	At        string // HH:MM Prague time
	Project   string
	Priority  int
	Services  []string
	LeaseMin  int
	GrantWait time.Duration // give up (campaigns wait for the next night) after this
	Poll      time.Duration
	Heartbeat time.Duration
	Retries   int           // LLM 5xx / unreachable answers per campaign before failing
	RetryWait time.Duration // between them
}

// windowDue reports whether tonight's window should run now.
func (h *Hub) windowDue(ctx context.Context) (bool, string) {
	now := h.now().In(prague)
	at, err := time.ParseInLocation("15:04", h.window.At, prague)
	if err != nil {
		return false, ""
	}
	start := time.Date(now.Year(), now.Month(), now.Day(), at.Hour(), at.Minute(), 0, 0, prague)
	// Only in the hours after At: a hub started at 23:00 must not open "tonight's" window then.
	if now.Before(start) || now.After(start.Add(windowSpan)) {
		return false, ""
	}
	day := now.Format("2006-01-02")
	if last, err := h.store.GetKV(ctx, kvWindowLast); err == nil && last == day {
		return false, ""
	}
	return true, day
}

// llmWindowTick runs from the worker loop; at most one window per day.
func (h *Hub) llmWindowTick(ctx context.Context) error {
	if h.window == nil {
		return nil
	}
	due, day := h.windowDue(ctx)
	if !due {
		return nil
	}
	// Mark the day first: a crash in the middle leaves the campaigns pending for tomorrow
	// instead of re-leasing in a loop.
	if err := h.store.SetKV(ctx, db.SetKVParams{Key: kvWindowLast, Value: day}); err != nil {
		return err
	}
	pending, err := h.store.PendingLLMCampaigns(ctx)
	if err != nil {
		return err
	}
	if len(pending) == 0 {
		h.log.Info("llm window: nothing pending, no lease")
		return nil
	}
	return h.runWindow(ctx, pending)
}

func (h *Hub) runWindow(ctx context.Context, pending []db.Campaign) error {
	w := h.window
	job, err := w.Broker.Lease(ctx, w.Project, fmt.Sprintf("publish-promo: %d kampaní", len(pending)), w.Services, w.LeaseMin, w.Priority)
	if err != nil {
		h.tellAdmin(ctx, "⏳ LLM okno: broker nedostupný ("+oneLine(err.Error(), 150)+"). Kampaně počkají na další noc.")
		return fmt.Errorf("llm window lease: %w", err)
	}
	h.log.Info("llm window: lease requested", "job", job.ID, "campaigns", len(pending))
	if _, err := w.Broker.WaitRunning(ctx, job.ID, w.Poll, w.GrantWait); err != nil {
		var ng *sparkbroker.NotGranted
		if errors.As(err, &ng) {
			_ = w.Broker.Cancel(ctx, job.ID)
			h.tellAdmin(ctx, fmt.Sprintf("⏳ LLM okno: SPARK nebyl volný do %s (%s). %d kampaní počká na další noc, nebo pošli /post now.",
				w.GrantWait, oneLine(ng.Job.Waiting, 120), len(pending)))
			return nil
		}
		h.tellAdmin(ctx, "⏳ LLM okno selhalo: "+oneLine(err.Error(), 200)+". Kampaně počkají na další noc.")
		return err
	}

	hbCtx, stop := context.WithCancel(ctx)
	defer stop()
	go func() {
		t := time.NewTicker(w.Heartbeat)
		defer t.Stop()
		for {
			select {
			case <-hbCtx.Done():
				return
			case <-t.C:
				if err := w.Broker.Heartbeat(hbCtx, job.ID); err != nil && hbCtx.Err() == nil {
					h.log.Warn("llm window heartbeat", "job", job.ID, "err", err)
				}
			}
		}
	}()

	ok, failed := 0, 0
	failure := ""
	for _, camp := range pending {
		if failure != "" {
			failed++ // the model is down; the rest waits too
			continue
		}
		slug := h.slugOf(camp.AppID)
		cc, targets := h.composerInput(camp, slug)
		bodies, err := h.rewriteWithRetry(ctx, cc, llmTargets(cc, targets))
		if err != nil {
			failed++
			if composer.Unavailable(err) {
				failure = "LLM neodpovídá: " + oneLine(err.Error(), 150)
			} else {
				h.log.Warn("llm window: campaign not rewritten", "campaign", camp.ID, "err", err)
			}
			continue
		}
		drafts, err := h.composeDrafts(ctx, camp, cc, targets, bodies)
		if err != nil {
			failed++
			h.log.Error("llm window: drafts", "campaign", camp.ID, "err", err)
			continue
		}
		if err := h.store.SetCampaignLLMStatus(ctx, db.SetCampaignLLMStatusParams{ID: camp.ID, LlmStatus: "done"}); err != nil {
			h.log.Error("llm window: status", "campaign", camp.ID, "err", err)
		}
		ok++
		if h.bot != nil && len(h.admins) > 0 && len(drafts) > 0 {
			h.announce(ctx, h.admins[0], camp, drafts)
		}
	}
	stop()
	metrics := map[string]sparkbroker.Count{"campaigns": {OK: ok, Failed: failed}}
	if err := w.Broker.Done(ctx, job.ID, metrics, failure); err != nil {
		h.log.Warn("llm window done", "job", job.ID, "err", err)
	}
	h.log.Info("llm window closed", "job", job.ID, "ok", ok, "failed", failed, "failure", failure)
	if failed > 0 {
		h.tellAdmin(ctx, fmt.Sprintf("⏳ LLM okno: %d kampaní hotovo, %d počká na další noc%s. Hned bez LLM: /post now …", ok, failed, suffix(failure)))
	}
	return nil
}

// rewriteWithRetry retries "model not up yet" answers (500/503, no connection) a few times.
func (h *Hub) rewriteWithRetry(ctx context.Context, c composer.Campaign, ts []composer.Target) (map[string]string, error) {
	var err error
	for i := 0; i <= h.window.Retries; i++ {
		if i > 0 {
			h.sleep(ctx, h.window.RetryWait)
		}
		var out map[string]string
		out, err = h.llm.Rewrite(ctx, c, ts)
		if err == nil || !composer.Unavailable(err) {
			return out, err
		}
	}
	return nil, err
}

func (h *Hub) slugOf(appID *int64) string {
	if appID == nil {
		return ""
	}
	for slug, id := range h.appIDs {
		if id == *appID {
			return slug
		}
	}
	return ""
}

func (h *Hub) tellAdmin(ctx context.Context, text string) {
	h.log.Info("admin notice", "text", text)
	if h.bot == nil || len(h.admins) == 0 {
		return
	}
	if _, err := h.bot.Send(ctx, h.admins[0], text, tgbot.Opts{}); err != nil {
		h.log.Error("admin notice", "err", err)
	}
}

func suffix(s string) string {
	if s == "" {
		return ""
	}
	return " (" + s + ")"
}
