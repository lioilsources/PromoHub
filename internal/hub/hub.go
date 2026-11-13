// Package hub wires config, store, Apify, rules and the outbound targets together:
// webhook handlers, Apify run processing and the event outbox.
package hub

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"sync"
	"time"

	"github.com/lioilsources/promohub/internal/apify"
	"github.com/lioilsources/promohub/internal/collectors"
	"github.com/lioilsources/promohub/internal/collectors/apifysrc"
	"github.com/lioilsources/promohub/internal/composer"
	"github.com/lioilsources/promohub/internal/config"
	"github.com/lioilsources/promohub/internal/ifttt"
	"github.com/lioilsources/promohub/internal/metrics"
	"github.com/lioilsources/promohub/internal/notify"
	"github.com/lioilsources/promohub/internal/oauth"
	"github.com/lioilsources/promohub/internal/publish"
	"github.com/lioilsources/promohub/internal/rules"
	"github.com/lioilsources/promohub/internal/store"
	"github.com/lioilsources/promohub/internal/store/db"
	"github.com/lioilsources/promohub/internal/tgbot"
)

// defaultMaxItems caps dataset reads for tasks without max_items.
const defaultMaxItems = 1000

type Hub struct {
	log      *slog.Logger
	env      config.Env
	files    *config.Files
	rules    *rules.Engine
	store    *store.Store
	apify    apify.API
	ifttt    *ifttt.Client
	telegram *notify.Telegram
	metrics  *metrics.Registry
	now      func() time.Time
	rand     func() float64

	collectors     map[string]collectors.Collector // platform → read adapter
	publishers     map[string]publish.Publisher    // platform → API publisher
	repliers       map[string]publish.Replier      // platform → reply from Telegram
	accounts       map[int64]config.Account        // accounts.id → account
	ownHosts       []string
	llm            *composer.LLM
	bot            *tgbot.Client // nil: no approvals, so nothing is published
	admins         []int64
	after          func(time.Duration, func()) // time.AfterFunc, replaceable in tests
	kickMu         sync.Mutex
	kicked         map[string]time.Time
	oauth          *oauth.Store // nil without PROMOHUB_SECRET_KEY
	window         *Window      // night LLM window; nil = compose right away
	lastTokenCheck time.Time
	jobs           map[jobKey]job
	terms          []string

	accountIDs map[string]int64 // "platform:handle" → accounts.id
	appIDs     map[string]int64 // slug → apps.id

	wakeRuns    chan struct{}
	wakeEvents  chan struct{}
	wakePublish chan struct{}
}

type Deps struct {
	Log      *slog.Logger
	Env      config.Env
	Files    *config.Files
	Rules    *rules.Engine
	Store    *store.Store
	Apify    apify.API
	IFTTT    *ifttt.Client
	Telegram *notify.Telegram
	Metrics  *metrics.Registry
	// Collectors are the official-API read adapters by platform (internal/platforms).
	Collectors []collectors.Collector
	Publishers []publish.Publisher
	// Bot and Admins enable the approval bot; without them nothing is published.
	Bot    *tgbot.Client
	Admins []int64
	LLM    *composer.LLM
	OAuth  *oauth.Store
	Window *Window
}

func New(d Deps) *Hub {
	h := &Hub{
		log: d.Log, env: d.Env, files: d.Files, rules: d.Rules, store: d.Store, apify: d.Apify,
		ifttt: d.IFTTT, telegram: d.Telegram, metrics: d.Metrics, now: time.Now, rand: rand.Float64,
		collectors: map[string]collectors.Collector{}, jobs: map[jobKey]job{}, terms: searchTerms(d.Files),
		publishers: map[string]publish.Publisher{}, accounts: map[int64]config.Account{}, ownHosts: composer.OwnHosts(d.Files.Apps),
		llm: d.LLM, bot: d.Bot, admins: d.Admins, kicked: map[string]time.Time{},
		oauth:       d.OAuth,
		after:       func(d time.Duration, f func()) { time.AfterFunc(d, f) },
		wakePublish: make(chan struct{}, 1),
		accountIDs:  map[string]int64{}, appIDs: map[string]int64{},
		wakeRuns: make(chan struct{}, 1), wakeEvents: make(chan struct{}, 1),
	}
	if h.metrics == nil {
		h.metrics = metrics.New()
	}
	h.metrics.Help("promohub_http_requests_total", "HTTP requests by route and status.")
	h.metrics.Help("promohub_apify_runs_total", "Apify runs by processing result.")
	h.metrics.Help("promohub_inbound_new_total", "New inbound items (comments, mentions, reviews).")
	h.metrics.Help("promohub_events_total", "Outbound event deliveries by target kind and result.")
	h.metrics.Help("promohub_collect_total", "Official-API collector passes by platform, capability and result.")
	for _, c := range d.Collectors {
		h.collectors[c.Platform()] = c
	}
	h.repliers = map[string]publish.Replier{}
	for _, p := range d.Publishers {
		h.publishers[p.Platform()] = p
		if r, ok := p.(publish.Replier); ok {
			h.repliers[p.Platform()] = r
		}
	}
	h.metrics.Help("promohub_publish_total", "Draft outcomes by platform, path and status.")
	return h
}

// SyncConfig upserts accounts.yaml and apps.yaml into the DB and caches their IDs.
func (h *Hub) SyncConfig(ctx context.Context) error {
	for _, a := range h.files.Accounts.Accounts {
		id, err := h.store.UpsertAccount(ctx, db.UpsertAccountParams{
			Platform: a.Platform, Handle: a.Handle, ExternalID: nz(a.ExternalID), AuthRef: nz(a.AuthRef),
		})
		if err != nil {
			return fmt.Errorf("account %s: %w", a.Key(), err)
		}
		h.accountIDs[a.Key()] = id
		h.accounts[id] = a
	}
	for _, a := range h.files.Apps {
		links, _ := json.Marshal(a.Links)
		if a.Links == nil {
			links = []byte("{}")
		}
		id, err := h.store.UpsertApp(ctx, db.UpsertAppParams{
			Slug: a.Slug, Name: a.Name, AppstoreID: nz(a.AppStoreID), PlayPackage: nz(a.PlayPackage), Links: links,
		})
		if err != nil {
			return fmt.Errorf("app %s: %w", a.Slug, err)
		}
		h.appIDs[a.Slug] = id
	}
	return h.planJobs(ctx)
}

func nudge(c chan struct{}) {
	select {
	case c <- struct{}{}:
	default:
	}
}

// Run starts the background workers and blocks until ctx ends.
func (h *Hub) Run(ctx context.Context) {
	go h.loop(ctx, "runs", h.wakeRuns, h.processPendingRuns)
	go h.loop(ctx, "collect", nil, h.collectDue)
	go h.loop(ctx, "tokens", nil, h.checkTokens)
	go h.loop(ctx, "llm-window", nil, h.llmWindowTick)
	if h.bot != nil {
		if err := h.recoverStranded(ctx); err != nil {
			h.log.Error("recover stranded drafts", "err", err)
		}
		go h.botLoop(ctx)
		go h.loop(ctx, "publish", h.wakePublish, h.publishDue)
	} else {
		h.log.Warn("telegram bot not configured (TELEGRAM_BOT_TOKEN, TELEGRAM_ADMIN_IDS): publishing is off")
	}
	h.loop(ctx, "events", h.wakeEvents, h.deliverPending)
}

func (h *Hub) loop(ctx context.Context, name string, wake chan struct{}, fn func(context.Context) error) {
	t := time.NewTicker(h.env.WorkerInterval)
	defer t.Stop()
	for {
		if err := fn(ctx); err != nil && !errors.Is(err, context.Canceled) {
			h.log.Error("worker", "worker", name, "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-wake:
		}
	}
}

func (h *Hub) processPendingRuns(ctx context.Context) error {
	runs, err := h.store.RetryableApifyRuns(ctx, int32(h.env.RunMaxAttempts))
	if err != nil {
		return err
	}
	for _, r := range runs {
		if err := h.ProcessRun(ctx, r.RunID); err != nil {
			backoff := time.Duration(1<<min(r.Attempts, 6)) * time.Minute
			msg := err.Error()
			h.log.Warn("apify run failed", "run", r.RunID, "task", r.TaskName, "attempt", r.Attempts+1, "err", msg)
			h.metrics.Inc("promohub_apify_runs_total", "result", "failed")
			if ferr := h.store.MarkApifyRunFailed(ctx, db.MarkApifyRunFailedParams{
				RunID: r.RunID, LastError: &msg, NextAttemptAt: h.now().Add(backoff),
			}); ferr != nil {
				return ferr
			}
		}
	}
	return nil
}

// ProcessRun fetches a run's dataset and ingests it. The run is re-read from the Apify API with
// our token, so a forged webhook can at most make us look at a run in our own account.
func (h *Hub) ProcessRun(ctx context.Context, runID string) error {
	row, err := h.store.GetApifyRun(ctx, runID)
	if err != nil {
		return err
	}
	task, ok := h.files.TaskByID(row.TaskID)
	if !ok {
		return fmt.Errorf("task %s is no longer whitelisted", row.TaskID)
	}
	run, err := h.apify.GetRun(ctx, runID)
	if err != nil {
		return fmt.Errorf("get run: %w", err)
	}
	if run.TaskID != task.ID {
		return fmt.Errorf("run belongs to task %q, webhook claimed %q", run.TaskID, task.ID)
	}
	if run.Status != "SUCCEEDED" {
		return fmt.Errorf("run status %s", run.Status)
	}
	limit := task.MaxItems
	if limit <= 0 {
		limit = defaultMaxItems
	}
	items, err := h.apify.DatasetItems(ctx, run.DatasetID, limit)
	if err != nil {
		return fmt.Errorf("dataset %s: %w", run.DatasetID, err)
	}
	owner := ""
	if a, ok := h.files.Account(task.Account); ok {
		owner = a.Handle
	}
	batch, err := apifysrc.Map(task.Kind, owner, items)
	if err != nil {
		return err
	}
	takenAt := h.now().UTC()
	if run.FinishedAt != nil {
		takenAt = run.FinishedAt.UTC()
	}

	var res IngestResult
	err = h.store.Tx(ctx, func(q *db.Queries) error {
		var err error
		res, err = h.Ingest(ctx, q, Source{RunID: runID, AccountKey: task.Account}, batch, takenAt)
		if err != nil {
			return err
		}
		n, nn := int32(len(items)), int32(res.NewInbound+res.NewContent)
		status := run.Status
		return q.MarkApifyRunProcessed(ctx, db.MarkApifyRunProcessedParams{
			RunID: runID, Status: &status, DatasetID: nz(run.DatasetID), StartedAt: run.StartedAt,
			FinishedAt: run.FinishedAt, Items: &n, NewItems: &nn, CostUsd: run.CostUSD,
		})
	})
	if err != nil {
		return err
	}
	h.metrics.Inc("promohub_apify_runs_total", "result", "processed")
	h.log.Info("apify run processed", "run", runID, "task", task.Name, "items", len(items), "skipped", batch.Skipped,
		"new_inbound", res.NewInbound, "new_content", res.NewContent, "snapshots", res.NewSnapshots, "events", res.Events)
	if res.Events > 0 {
		nudge(h.wakeEvents)
	}
	h.afterIngest(ctx, res)
	return nil
}
