package hub

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/lioilsources/promohub/internal/notify"
	"github.com/lioilsources/promohub/internal/rules"
	"github.com/lioilsources/promohub/internal/store/db"
)

// retryable is implemented by the status errors of ifttt and notify.
type retryable interface{ Retryable() bool }

func (h *Hub) deliverPending(ctx context.Context) error {
	for {
		evs, err := h.store.PendingEvents(ctx, db.PendingEventsParams{MaxAttempts: int32(h.env.EventMaxAttempts), Lim: 50})
		if err != nil {
			return err
		}
		for _, e := range evs {
			h.deliverOne(ctx, e)
		}
		if len(evs) < 50 {
			return nil
		}
	}
}

func (h *Hub) deliverOne(ctx context.Context, e db.Event) {
	target := deref(e.Target)
	kind, _, _ := strings.Cut(target, ":")
	dctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	err := h.deliver(dctx, target, e.Payload)
	cancel()
	if err == nil {
		h.metrics.Inc("promohub_events_total", "target", kind, "result", "sent")
		if merr := h.store.MarkEventSent(ctx, e.ID); merr != nil {
			h.log.Error("mark event sent", "event", e.ID, "err", merr)
		}
		return
	}
	msg := err.Error()
	var r retryable
	if errors.As(err, &r) && !r.Retryable() {
		h.metrics.Inc("promohub_events_total", "target", kind, "result", "rejected")
		h.log.Error("event rejected, giving up", "event", e.ID, "target", target, "err", msg)
		if merr := h.store.GiveUpEvent(ctx, db.GiveUpEventParams{ID: e.ID, LastError: &msg, MaxAttempts: int32(h.env.EventMaxAttempts)}); merr != nil {
			h.log.Error("give up event", "event", e.ID, "err", merr)
		}
		return
	}
	h.metrics.Inc("promohub_events_total", "target", kind, "result", "retry")
	backoff := time.Duration(30<<min(e.Attempts, 7)) * time.Second // 30 s … ~1 h
	h.log.Warn("event delivery failed", "event", e.ID, "target", target, "attempt", e.Attempts+1, "retry_in", backoff, "err", msg)
	if merr := h.store.MarkEventFailed(ctx, db.MarkEventFailedParams{ID: e.ID, LastError: &msg, NextAttemptAt: h.now().Add(backoff)}); merr != nil {
		h.log.Error("mark event failed", "event", e.ID, "err", merr)
	}
}

func (h *Hub) deliver(ctx context.Context, target string, payload []byte) error {
	kind, name, _ := strings.Cut(target, ":")
	switch kind {
	case rules.TargetIFTTT:
		return h.ifttt.Trigger(ctx, name, json.RawMessage(payload))
	case rules.TargetTelegram:
		chat := h.files.Rules.Sinks.Telegram[name]
		if chat == "" {
			return fmt.Errorf("sinks.telegram.%s is not configured", name)
		}
		if sent, err := h.sendInbound(ctx, chat, payload); sent {
			return err
		}
		return h.telegram.Send(ctx, chat, payloadMessage(payload))
	case rules.TargetDiscord:
		hook := h.files.Rules.Sinks.Discord[name]
		if hook == "" {
			return fmt.Errorf("sinks.discord.%s is not configured", name)
		}
		return notify.Discord(ctx, hook, payloadMessage(payload))
	}
	return fmt.Errorf("unknown target %q", target)
}

func payloadMessage(b []byte) string {
	var p struct {
		Message string `json:"message"`
	}
	_ = json.Unmarshal(b, &p)
	return p.Message
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
