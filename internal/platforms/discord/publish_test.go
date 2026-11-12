package discord

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lioilsources/promohub/internal/config"
	"github.com/lioilsources/promohub/internal/creds"
	"github.com/lioilsources/promohub/internal/publish"
)

func TestPublishWaitsForMessage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var in map[string]any
		_ = json.NewDecoder(r.Body).Decode(&in)
		if r.URL.Path != "/api/webhooks/1/secret" || r.URL.Query().Get("wait") != "true" || in["content"] != "hi @everyone" {
			t.Errorf("%s %+v", r.URL, in)
		}
		if am := in["allowed_mentions"].(map[string]any); len(am["parse"].([]any)) != 0 {
			t.Error("mentions must be disabled")
		}
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"message":"bad","url":"/api/webhooks/1/secret"}`))
	}))
	defer srv.Close()
	old := creds.Getenv
	creds.Getenv = func(k string) string {
		if k == "DISCORD_ANNOUNCE_WEBHOOK" {
			return "https://discord.com/api/webhooks/1/secret"
		}
		return ""
	}
	defer func() { creds.Getenv = old }()
	a := New(srv.Client())
	a.Base = srv.URL
	_, err := Publisher{a}.Publish(context.Background(), config.Account{Platform: "discord", Handle: "s", AuthRef: "DISCORD_ANNOUNCE"}, publish.Post{Text: "hi @everyone"})
	if err == nil || strings.Contains(err.Error(), "secret") || publish.Classify(err) != publish.Failed {
		t.Fatalf("4xx must fail cleanly without leaking the webhook: %v", err)
	}
}
