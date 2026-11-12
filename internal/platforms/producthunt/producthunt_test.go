package producthunt

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

func TestProductHunt(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer ph" {
			t.Errorf("auth %q", r.Header.Get("Authorization"))
		}
		w.Write([]byte(`{"data":{"user":{"followersCount":12,"madePosts":{"edges":[{"node":{"id":"p1","name":"SwypeKids","url":"https://www.producthunt.com/posts/swypekids",
			"createdAt":"2026-10-01T07:00:00Z","votesCount":150,"commentsCount":2,"comments":{"edges":[
			{"node":{"id":"c1","body":"Congrats!","createdAt":"2026-10-01T08:00:00Z","url":"https://www.producthunt.com/posts/swypekids#c1","user":{"username":"maker2"}}},
			{"node":{"id":"c2","body":"Thanks","user":{"username":"ol1n"}}}]}}}]}}}}`))
	}))
	defer srv.Close()
	old := creds.Getenv
	creds.Getenv = func(k string) string {
		if k == "PRODUCTHUNT_TOKEN" {
			return "ph"
		}
		return ""
	}
	defer func() { creds.Getenv = old }()
	a := &Adapter{HTTP: srv.Client(), Base: srv.URL}
	acct := config.Account{Platform: "producthunt", Handle: "ol1n", AuthRef: "PRODUCTHUNT"}
	b, _, err := a.Collect(context.Background(), collectors.Request{Account: acct, Capability: platform.CapProfile})
	if err != nil || len(b.Snapshots) != 2 || *b.Snapshots[0].Likes != 150 || *b.Snapshots[1].Followers != 12 || len(b.Inbound) != 0 {
		t.Fatalf("%+v %v", b, err)
	}
	b, _, err = a.Collect(context.Background(), collectors.Request{Account: acct, Capability: platform.CapInbound})
	if err != nil || len(b.Inbound) != 1 || b.Inbound[0].Author != "maker2" || len(b.Snapshots) != 0 {
		t.Fatalf("%+v %v", b, err)
	}
}
