package discord

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/lioilsources/promohub/internal/collectors"
	"github.com/lioilsources/promohub/internal/config"
	"github.com/lioilsources/promohub/internal/platform"
)

// Trimmed real answer of GET /invites/discord-developers?with_counts=true (2026-11-12).
const invite = `{"type":0,"code":"discord-developers","expires_at":null,"guild":{"id":"613425648685547541","name":"Discord Developers"},
"approximate_member_count":311074,"approximate_presence_count":76370}`

func TestInviteCounts(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/invites/discord-developers" || r.URL.Query().Get("with_counts") != "true" {
			t.Errorf("%s", r.URL)
		}
		w.Write([]byte(invite))
	}))
	defer srv.Close()
	a := New(srv.Client())
	a.Base = srv.URL
	b, _, err := a.Collect(context.Background(), collectors.Request{
		Account: config.Account{Platform: "discord", Handle: "dd", ExternalID: "discord-developers"}, Capability: platform.CapProfile})
	if err != nil {
		t.Fatal(err)
	}
	if s := b.Snapshots[0]; *s.Followers != 311074 || s.Extra["online"] != 76370 {
		t.Fatalf("%+v", s)
	}
	if a.Ready(config.Account{Platform: "discord", Handle: "dd"}, platform.CapProfile) == nil {
		t.Fatal("invite code is required")
	}
}
