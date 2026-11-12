package bluesky

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/lioilsources/promohub/internal/collectors"
	"github.com/lioilsources/promohub/internal/config"
	"github.com/lioilsources/promohub/internal/creds"
	"github.com/lioilsources/promohub/internal/platform"
)

// testdata/profile.json and feed.json are trimmed real answers of the public AppView for
// bsky.app (2026-11-12); repost authors are anonymised. notifications.json follows the
// app.bsky.notification.listNotifications schema.

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func server(t *testing.T, auth *string) *httptest.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /xrpc/app.bsky.actor.getProfile", func(w http.ResponseWriter, r *http.Request) {
		w.Write(fixture(t, "profile.json"))
	})
	mux.HandleFunc("GET /xrpc/app.bsky.feed.getAuthorFeed", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("filter") != "posts_no_replies" {
			t.Errorf("filter = %q", r.URL.Query().Get("filter"))
		}
		w.Write(fixture(t, "feed.json"))
	})
	mux.HandleFunc("POST /xrpc/com.atproto.server.createSession", func(w http.ResponseWriter, r *http.Request) {
		var in map[string]string
		_ = json.NewDecoder(r.Body).Decode(&in)
		if in["password"] != "app-pass-1234" {
			w.WriteHeader(http.StatusUnauthorized)
			w.Write([]byte(`{"error":"AuthenticationRequired"}`))
			return
		}
		w.Write([]byte(`{"accessJwt":"jwt-1","did":"did:plc:me","handle":"ol1n.bsky.social"}`))
	})
	mux.HandleFunc("GET /xrpc/app.bsky.notification.listNotifications", func(w http.ResponseWriter, r *http.Request) {
		*auth = r.Header.Get("Authorization")
		w.Write(fixture(t, "notifications.json"))
	})
	mux.HandleFunc("GET /xrpc/app.bsky.feed.searchPosts", func(w http.ResponseWriter, r *http.Request) {
		*auth = r.Header.Get("Authorization")
		w.Write([]byte(`{"posts":[
			{"uri":"at://did:plc:fan/app.bsky.feed.post/3x1","author":{"did":"did:plc:fan","handle":"fan.bsky.social"},
			 "record":{"text":"Hajánek je super","createdAt":"2026-11-12T10:00:00.000Z"}},
			{"uri":"at://did:plc:me/app.bsky.feed.post/3x2","author":{"did":"did:plc:me","handle":"ol1n.bsky.social"},
			 "record":{"text":"my own Hajánek post","createdAt":"2026-11-12T11:00:00.000Z"}}]}`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func adapter(t *testing.T, auth *string) *Adapter {
	srv := server(t, auth)
	a := New(srv.Client())
	a.AppView, a.PDS = srv.URL, srv.URL
	return a
}

var acct = config.Account{Platform: "bluesky", Handle: "ol1n.bsky.social", AuthRef: "BLUESKY_MAIN"}

func withSecret(t *testing.T, v string) {
	old := creds.Getenv
	creds.Getenv = func(k string) string {
		if k == "BLUESKY_MAIN_APP_PASSWORD" {
			return v
		}
		return ""
	}
	t.Cleanup(func() { creds.Getenv = old })
}

func TestProfileRealFeed(t *testing.T) {
	var auth string
	b, _, err := adapter(t, &auth).Collect(context.Background(), collectors.Request{Account: config.Account{Platform: "bluesky", Handle: "bsky.app"}, Capability: platform.CapProfile})
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Snapshots) != 2 || b.Skipped != 2 {
		t.Fatalf("snapshots=%d skipped=%d", len(b.Snapshots), b.Skipped)
	}
	p := b.Snapshots[0]
	if p.Content.ExternalID != "3n22yg6lg2k2f" || p.Content.URL != "https://bsky.app/profile/bsky.app/post/3n22yg6lg2k2f" ||
		*p.Likes != 1867 || *p.Reposts != 337 || *p.Comments != 157 || p.Extra["quotes"] != 117 || p.Content.PublishedAt == nil {
		t.Fatalf("post: %+v %+v", p, p.Content)
	}
	if a := b.Snapshots[1]; a.Content != nil || *a.Followers != 35634797 || a.Extra["posts"] != 920 {
		t.Fatalf("account: %+v", a)
	}
	if platform.ExternalID("bluesky", p.Content.URL) != p.Content.ExternalID {
		t.Fatal("URL and external ID disagree")
	}
}

func TestNotifications(t *testing.T) {
	withSecret(t, "app-pass-1234")
	var auth string
	a := adapter(t, &auth)
	b, cur, err := a.Collect(context.Background(), collectors.Request{Account: acct, Capability: platform.CapInbound,
		Cursor: json.RawMessage(`{"since":"2026-11-11T00:00:00.000Z"}`)})
	if err != nil {
		t.Fatal(err)
	}
	if auth != "Bearer jwt-1" {
		t.Fatalf("auth header %q", auth)
	}
	if len(b.Inbound) != 2 || b.Skipped != 2 {
		t.Fatalf("inbound=%d skipped=%d", len(b.Inbound), b.Skipped)
	}
	m, r := b.Inbound[0], b.Inbound[1]
	if m.Kind != "mention" || m.Author != "fan.bsky.social" || m.URL != "https://bsky.app/profile/fan.bsky.social/post/3n2aaaaaaaa2a" {
		t.Fatalf("mention: %+v", m)
	}
	if r.Kind != "reply" || r.Text != "agreed" {
		t.Fatalf("reply: %+v", r)
	}
	if string(cur) != `{"since":"2026-11-12T09:30:00.000Z"}` {
		t.Fatalf("cursor %s", cur)
	}
	// Next pass with the returned cursor: nothing new.
	b, _, _ = a.Collect(context.Background(), collectors.Request{Account: acct, Capability: platform.CapInbound, Cursor: cur})
	if len(b.Inbound) != 0 {
		t.Fatalf("second pass: %+v", b.Inbound)
	}
}

func TestSearchGoesThroughPDSAndDropsMine(t *testing.T) {
	withSecret(t, "app-pass-1234")
	var auth string
	b, _, err := adapter(t, &auth).Collect(context.Background(), collectors.Request{Account: acct, Capability: platform.CapSearch, Terms: []string{"Hajánek"}})
	if err != nil {
		t.Fatal(err)
	}
	if auth == "" || len(b.Inbound) != 1 || b.Inbound[0].Author != "fan.bsky.social" || b.Skipped != 1 {
		t.Fatalf("auth=%q %+v", auth, b)
	}
}

func TestMissingAndWrongPassword(t *testing.T) {
	withSecret(t, "")
	a := adapter(t, new(string))
	var me *creds.MissingError
	if err := a.Ready(acct, platform.CapInbound); !errors.As(err, &me) || me.Var != "BLUESKY_MAIN_APP_PASSWORD" {
		t.Fatalf("ready: %v", err)
	}
	if err := a.Ready(acct, platform.CapProfile); err != nil {
		t.Fatalf("profile needs no secret: %v", err)
	}
	withSecret(t, "wrong-password")
	_, _, err := a.Collect(context.Background(), collectors.Request{Account: acct, Capability: platform.CapInbound})
	if err == nil || contains(err.Error(), "wrong-password") {
		t.Fatalf("err %v", err)
	}
}

func contains(s, sub string) bool { return strings.Contains(s, sub) }
