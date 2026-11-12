package telegram

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/lioilsources/promohub/internal/collectors"
	"github.com/lioilsources/promohub/internal/config"
	"github.com/lioilsources/promohub/internal/creds"
	"github.com/lioilsources/promohub/internal/platform"
)

func TestMemberCount(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/bot123:abc/getChatMemberCount" || r.URL.Query().Get("chat_id") != "@ol1n_apps" {
			t.Errorf("%s", r.URL)
		}
		w.Write([]byte(`{"ok":true,"result":42}`))
	}))
	defer srv.Close()
	old := creds.Getenv
	creds.Getenv = func(k string) string {
		if k == "TELEGRAM_BOT_TOKEN" {
			return "123:abc"
		}
		return ""
	}
	defer func() { creds.Getenv = old }()
	a := New(srv.Client())
	a.Base = srv.URL
	b, _, err := a.Collect(context.Background(), collectors.Request{Account: config.Account{Platform: "telegram", Handle: "ol1n_apps"}, Capability: platform.CapProfile})
	if err != nil || *b.Snapshots[0].Followers != 42 {
		t.Fatalf("%+v %v", b, err)
	}
}
