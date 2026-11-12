package devto

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/lioilsources/promohub/internal/collectors"
	"github.com/lioilsources/promohub/internal/config"
	"github.com/lioilsources/promohub/internal/creds"
	"github.com/lioilsources/promohub/internal/platform"
	"github.com/lioilsources/promohub/internal/publish"
)

func TestDevto(t *testing.T) {
	var posted map[string]map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Api-Key") != "k" || r.Header.Get("Accept") != "application/vnd.forem.api-v1+json" {
			t.Errorf("headers %v", r.Header)
		}
		switch r.URL.Path {
		case "/articles/me/published":
			// Shape of a real /articles item (2026-11-12) plus the private page_views_count.
			w.Write([]byte(`[{"id":5024286,"title":"Long Live Rails","url":"https://dev.to/ol1n/long-live-rails-3p21","public_reactions_count":72,
				"comments_count":2,"page_views_count":900,"published_at":"2026-11-12T15:30:50Z"}]`))
		case "/followers/users":
			w.Write([]byte(`[{"id":1},{"id":2},{"id":3}]`))
		case "/comments":
			w.Write([]byte(`[{"id_code":"c1","body_html":"<p>Nice &amp; clean</p>","user":{"username":"fan"},"children":[
				{"id_code":"c2","body_html":"<p>thanks</p>","user":{"username":"ol1n"},"children":[]}]}]`))
		case "/articles":
			_ = json.NewDecoder(r.Body).Decode(&posted)
			w.Write([]byte(`{"id":99,"url":"https://dev.to/ol1n/new-99"}`))
		}
	}))
	defer srv.Close()
	old := creds.Getenv
	creds.Getenv = func(k string) string {
		if k == "DEVTO_MAIN_API_KEY" {
			return "k"
		}
		return ""
	}
	defer func() { creds.Getenv = old }()
	a := &Adapter{HTTP: srv.Client(), Base: srv.URL}
	acct := config.Account{Platform: "devto", Handle: "ol1n", AuthRef: "DEVTO_MAIN"}
	b, _, err := a.Collect(context.Background(), collectors.Request{Account: acct, Capability: platform.CapProfile})
	if err != nil || len(b.Snapshots) != 2 || *b.Snapshots[0].Views != 900 || *b.Snapshots[1].Followers != 3 {
		t.Fatalf("%+v %v", b, err)
	}
	b, _, err = a.Collect(context.Background(), collectors.Request{Account: acct, Capability: platform.CapInbound})
	if err != nil || len(b.Inbound) != 1 || b.Inbound[0].Text != "Nice & clean" || b.Skipped != 1 {
		t.Fatalf("%+v %v", b, err)
	}
	res, err := Publisher{a}.Publish(context.Background(), acct, publish.Post{Title: "How SwypeKids works", Text: "# body", Link: "https://olin.now/blog/x"})
	if err != nil || res.ExternalID != "99" || posted["article"]["canonical_url"] != "https://olin.now/blog/x" || posted["article"]["published"] != true {
		t.Fatalf("%+v %v %+v", res, err, posted)
	}
}
