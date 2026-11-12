package facebook

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

type tokens struct{}

func (tokens) Token(context.Context, config.Account) (creds.Token, error) {
	return creds.Token{AccessToken: "page-token", PageID: "222"}, nil
}

func TestPage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/222":
			w.Write([]byte(`{"followers_count":150,"fan_count":140,"id":"222"}`))
		case "/222/posts":
			w.Write([]byte(`{"data":[{"id":"222_1","message":"Nová verze","permalink_url":"https://www.facebook.com/ol1n/posts/pfbid1","created_time":"2026-11-10T10:00:00+0000",
				"shares":{"count":3},"reactions":{"data":[],"summary":{"total_count":20}},"comments":{"data":[],"summary":{"total_count":1}}}]}`))
		case "/222_1/comments":
			w.Write([]byte(`{"data":[{"id":"222_1_c1","message":"Super","created_time":"2026-11-10T11:00:00+0000","from":{"id":"9","name":"Jana"}},
				{"id":"222_1_c2","message":"Díky","from":{"id":"222","name":"Ol1n"}}]}`))
		default:
			t.Errorf("unexpected %s", r.URL)
		}
	}))
	defer srv.Close()
	a := New(srv.Client(), tokens{})
	a.Base = srv.URL
	acct := config.Account{Platform: "facebook", Handle: "ol1n", ExternalID: "222"}
	b, _, err := a.Collect(context.Background(), collectors.Request{Account: acct, Capability: platform.CapProfile})
	if err != nil || len(b.Snapshots) != 2 || *b.Snapshots[0].Likes != 20 || *b.Snapshots[0].Reposts != 3 || *b.Snapshots[1].Followers != 150 {
		t.Fatalf("%+v %v", b, err)
	}
	b, _, err = a.Collect(context.Background(), collectors.Request{Account: acct, Capability: platform.CapInbound})
	if err != nil || len(b.Inbound) != 1 || b.Inbound[0].Author != "Jana" {
		t.Fatalf("%+v %v", b, err)
	}
}
