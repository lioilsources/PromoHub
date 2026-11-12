package hackernews

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/lioilsources/promohub/internal/collectors"
	"github.com/lioilsources/promohub/internal/config"
	"github.com/lioilsources/promohub/internal/platform"
)

// Trimmed real answers (2026-11-12): Firebase user/pg, Algolia stories by pg, a comment search
// (authors anonymised).
const (
	user    = `{"id":"pg","karma":157316,"created":1160418092,"submitted":[39667625,37278578]}`
	stories = `{"hits":[{"objectID":"37278345","title":"If you're interested in eye-tracking, I'm interested in funding you",
		"url":"https://twitter.com/paulg/status/1695596853864321055","author":"pg","points":374,"num_comments":209,
		"created_at":"2023-08-27T00:51:13Z","created_at_i":1693097473,"story_id":37278345}]}`
	search = `{"hits":[
		{"objectID":"50465166","comment_text":"I built it in <i>Go</i> &amp; SwypeKids uses it<p>more","author":"someone","story_id":50338726,
		 "story_title":"Ask HN: Who wants to be hired? (November 2026)","created_at":"2026-11-12T09:59:49Z","created_at_i":1794477589},
		{"objectID":"50464727","comment_text":"swiping kids, unrelated","author":"other","created_at_i":1794473936},
		{"objectID":"50464000","title":"Show HN: SwypeKids","url":"https://olin.now","author":"pg","created_at_i":1794470000}]}`
)

func TestHN(t *testing.T) {
	var numeric string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/user/pg.json":
			w.Write([]byte(user))
		case r.URL.Query().Get("tags") == "story,author_pg":
			w.Write([]byte(stories))
		default:
			numeric = r.URL.Query().Get("numericFilters")
			w.Write([]byte(search))
		}
	}))
	defer srv.Close()
	a := &Adapter{HTTP: srv.Client(), Firebase: srv.URL, Algolia: srv.URL}
	acct := config.Account{Platform: "hackernews", Handle: "pg"}
	b, _, err := a.Collect(context.Background(), collectors.Request{Account: acct, Capability: platform.CapProfile})
	if err != nil || len(b.Snapshots) != 2 || *b.Snapshots[0].Likes != 374 || *b.Snapshots[1].Followers != 157316 ||
		platform.ExternalID("hackernews", b.Snapshots[0].Content.URL) != "37278345" {
		t.Fatalf("%+v %v", b, err)
	}
	b, cur, err := a.Collect(context.Background(), collectors.Request{Account: acct, Capability: platform.CapSearch, Terms: []string{"SwypeKids"},
		Cursor: []byte(`{"since":1794400000}`)})
	if err != nil || numeric != "created_at_i>1794400000" {
		t.Fatalf("%v %q", err, numeric)
	}
	// The stem match and my own Show HN are dropped.
	if len(b.Inbound) != 1 || b.Inbound[0].Author != "someone" || b.Skipped != 2 || string(cur) != `{"since":1794477589}` {
		t.Fatalf("%+v %s", b, cur)
	}
	if in := b.Inbound[0]; in.Text != "Ask HN: Who wants to be hired? (November 2026)\n\nI built it in Go & SwypeKids uses it\nmore" {
		t.Fatalf("%q", in.Text)
	}
}
