package hub

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/lioilsources/promohub/internal/apify"
	"github.com/lioilsources/promohub/internal/ifttt"
	"github.com/lioilsources/promohub/internal/rules"
	"github.com/lioilsources/promohub/internal/store"
	"github.com/lioilsources/promohub/internal/store/db"
)

const maxBody = 1 << 20

func (h *Hub) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", h.healthz)
	mux.Handle("GET /metrics", h.metrics.Handler())
	apifyLimit := newLimiter(60, time.Minute)
	iftttLimit := newLimiter(30, time.Minute)
	mux.Handle("POST /webhooks/apify", apifyLimit.wrap(http.HandlerFunc(h.apifyWebhook)))
	mux.Handle("POST /webhooks/ifttt", iftttLimit.wrap(http.HandlerFunc(h.iftttWebhook)))
	mux.Handle("POST /webhooks/ifttt/{event}", iftttLimit.wrap(http.HandlerFunc(h.iftttWebhook)))
	mux.Handle("GET /media/{token}", newLimiter(120, time.Minute).wrap(http.HandlerFunc(h.serveMedia)))
	oauthLimit := newLimiter(30, time.Minute)
	mux.Handle("GET /oauth/{platform}/start", oauthLimit.wrap(http.HandlerFunc(h.oauthStart)))
	mux.Handle("GET /oauth/{platform}/callback", oauthLimit.wrap(http.HandlerFunc(h.oauthCallback)))
	mux.Handle("/oauth/{platform}/deauthorize", oauthLimit.wrap(http.HandlerFunc(h.oauthDeauthorize)))
	return h.logRequests(mux)
}

func (h *Hub) healthz(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	if err := h.store.Pool.Ping(ctx); err != nil {
		http.Error(w, "db: "+err.Error(), http.StatusServiceUnavailable)
		return
	}
	w.Write([]byte("ok\n"))
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

// apifyWebhook accepts ACTOR.RUN.SUCCEEDED for whitelisted tasks, records the run and leaves the
// dataset download to the run worker, so Apify gets its answer well within its 30 s timeout.
func (h *Hub) apifyWebhook(w http.ResponseWriter, r *http.Request) {
	if !apify.SecretOK(r.URL.Query().Get("secret"), h.env.ApifyWebhookSecret) {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "bad secret"})
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBody))
	if err != nil {
		writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"error": err.Error()})
		return
	}
	wh, err := apify.ParseWebhook(body)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if wh.EventType != "" && wh.EventType != "ACTOR.RUN.SUCCEEDED" {
		h.log.Info("apify webhook ignored", "event", wh.EventType, "run", wh.RunID())
		writeJSON(w, http.StatusOK, map[string]string{"status": "ignored"})
		return
	}
	task, ok := h.files.TaskByID(wh.TaskID())
	if !ok {
		h.log.Warn("apify webhook for unknown task", "task", wh.TaskID(), "run", wh.RunID())
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "task not whitelisted"})
		return
	}
	status := wh.Resource.Status
	_, err = h.store.InsertApifyRun(r.Context(), db.InsertApifyRunParams{
		RunID: wh.RunID(), TaskID: task.ID, TaskName: task.Name, ActorID: nz(wh.EventData.ActorID), Status: nz(status),
	})
	if store.NoRows(err) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "duplicate"})
		return
	} else if err != nil {
		h.log.Error("insert apify run", "run", wh.RunID(), "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db"})
		return
	}
	nudge(h.wakeRuns)
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "queued", "run": wh.RunID(), "task": task.Name})
}

// iftttWebhook takes triggers from IFTTT applets. Requests are unsigned, so the only effects are
// registering my own content and starting a whitelisted task with its stored input.
func (h *Hub) iftttWebhook(w http.ResponseWriter, r *http.Request) {
	if !apify.SecretOK(r.URL.Query().Get("secret"), h.env.IFTTTInboundSecret) {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "bad secret"})
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBody))
	if err != nil {
		writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"error": err.Error()})
		return
	}
	in, err := ifttt.ParseInbound(body, r.PathValue("event"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	ctx := r.Context()
	logType := "ifttt." + in.Event
	if _, err := h.store.InsertEvent(ctx, db.InsertEventParams{Type: logType, Payload: body2json(body)}); err != nil {
		h.log.Error("log ifttt event", "err", err)
	}

	if platform, ok := ifttt.ContentPlatform[in.Event]; ok {
		h.iftttContent(w, r, platform, in)
		return
	}
	switch in.Event {
	case ifttt.EventRunTask:
		task, ok := h.files.TaskByName(in.Task)
		if !ok {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "task not whitelisted"})
			return
		}
		run, err := h.apify.RunTask(ctx, task.ID, task.MaxItems)
		if err != nil {
			h.log.Error("start apify task", "task", task.Name, "err", err)
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": "apify"})
			return
		}
		h.log.Info("apify task started from ifttt", "task", task.Name, "run", run.ID)
		writeJSON(w, http.StatusAccepted, map[string]string{"status": "started", "run": run.ID})
	default:
		// Logged above; handlers for /scan etc. arrive in phase 2.
		h.log.Info("ifttt event logged without handler", "event", in.Event)
		writeJSON(w, http.StatusAccepted, map[string]string{"status": "logged"})
	}
}

func (h *Hub) iftttContent(w http.ResponseWriter, r *http.Request, platform string, in ifttt.Inbound) {
	if in.URL == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "url is required"})
		return
	}
	ctx := r.Context()
	acctID := h.firstAccount(platform)
	var res struct {
		inserted bool
		fresh    IngestResult
	}
	err := h.store.Tx(ctx, func(q *db.Queries) error {
		row, err := q.UpsertContentItem(ctx, db.UpsertContentItemParams{
			AccountID: acctID, AppID: h.appIDFor(in.Title + " " + in.Body), SourcePlatform: platform,
			ExternalID: ifttt.ExternalID(platform, in.URL), Url: nz(in.URL), Title: nz(in.Title), Body: nz(in.Body),
			PublishedAt: in.Published(),
		})
		if err != nil {
			return err
		}
		res.inserted = row.Inserted
		if !row.Inserted {
			return nil
		}
		if platform == "youtube" {
			res.fresh.Fresh = append(res.fresh.Fresh, freshPost{row.ID, platform, firstLine(in.Title, in.Body), in.URL})
		}
		v := rules.Vars{Num: map[string]float64{}, Str: map[string]string{"platform": platform, "url": in.URL, "title": in.Title, "text": in.Body}}
		_, err = h.fire(ctx, q, rules.OnContentNew, v, map[string]any{"platform": platform, "url": in.URL, "title": firstLine(in.Title, in.Body)})
		return err
	})
	if err != nil {
		h.log.Error("ifttt content", "platform", platform, "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db"})
		return
	}
	h.afterIngest(ctx, res.fresh)
	nudge(h.wakeEvents)
	status := "duplicate"
	if res.inserted {
		status = "created"
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": status})
}

// firstAccount returns my account on a platform when there is exactly one.
func (h *Hub) firstAccount(platform string) *int64 {
	var found *int64
	for _, a := range h.files.Accounts.Accounts {
		if a.Platform != platform {
			continue
		}
		if found != nil {
			return nil
		}
		id := h.accountIDs[a.Key()]
		found = &id
	}
	return found
}

// body2json stores the raw body if it is JSON and wraps it otherwise.
func body2json(b []byte) []byte {
	if json.Valid(b) && len(b) > 0 {
		return b
	}
	out, _ := json.Marshal(map[string]string{"raw": string(b)})
	return out
}

type statusWriter struct {
	http.ResponseWriter
	code int
}

func (s *statusWriter) WriteHeader(c int) { s.code = c; s.ResponseWriter.WriteHeader(c) }

// logRequests logs without the query string: it carries the webhook secret.
func (h *Hub) logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w, code: http.StatusOK}
		next.ServeHTTP(sw, r)
		route := r.Pattern
		if route == "" {
			route = "unmatched"
		}
		h.metrics.Inc("promohub_http_requests_total", "route", route, "code", strconv.Itoa(sw.code))
		if r.URL.Path != "/healthz" && r.URL.Path != "/metrics" {
			path := r.URL.Path
			if strings.HasPrefix(path, "/media/") {
				path = "/media/…" // the token is the key
			}
			h.log.Info("http", "method", r.Method, "path", path, "code", sw.code, "dur_ms", time.Since(start).Milliseconds())
		}
	})
}

// limiter is a global token bucket per endpoint. Webhook traffic is tiny, so per-IP buckets
// (and trusting X-Forwarded-For behind the tunnel) are not worth it.
type limiter struct {
	mu     sync.Mutex
	tokens float64
	max    float64
	rate   float64 // tokens per second
	last   time.Time
}

func newLimiter(n int, per time.Duration) *limiter {
	return &limiter{tokens: float64(n), max: float64(n), rate: float64(n) / per.Seconds(), last: time.Now()}
}

func (l *limiter) allow() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	l.tokens = min(l.max, l.tokens+now.Sub(l.last).Seconds()*l.rate)
	l.last = now
	if l.tokens < 1 {
		return false
	}
	l.tokens--
	return true
}

func (l *limiter) wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !l.allow() {
			w.Header().Set("Retry-After", "10")
			writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "rate limited"})
			return
		}
		next.ServeHTTP(w, r)
	})
}

// serveMedia hands out an uploaded file to platforms that pull media from a URL. The token is
// the only key; expired or unknown tokens look the same (404).
func (h *Hub) serveMedia(w http.ResponseWriter, r *http.Request) {
	a, err := h.store.MediaByToken(r.Context(), db.MediaByTokenParams{Token: r.PathValue("token"), ExpiresAt: h.now()})
	if err != nil {
		http.NotFound(w, r)
		return
	}
	f, err := os.Open(a.Path)
	if err != nil {
		h.log.Error("media file missing", "media", a.ID, "err", err)
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	w.Header().Set("Content-Type", a.Mime)
	w.Header().Set("Cache-Control", "private, max-age=3600")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	http.ServeContent(w, r, a.Name, a.CreatedAt, f)
}
