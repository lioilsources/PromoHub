package threads

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lioilsources/promohub/internal/collectors"
	"github.com/lioilsources/promohub/internal/config"
	"github.com/lioilsources/promohub/internal/creds"
	"github.com/lioilsources/promohub/internal/platform"
	"github.com/lioilsources/promohub/internal/publish"
)

type tokens struct{}

func (tokens) Token(context.Context, config.Account) (creds.Token, error) {
	return creds.Token{AccessToken: "th-token"}, nil
}

var acct = config.Account{Platform: "threads", Handle: "ol1n", AuthRef: "oauth"}

func TestThreads(t *testing.T) {
	var created []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		switch r.URL.Path {
		case "/me":
			w.Write([]byte(`{"id":"99","username":"ol1n"}`))
		case "/me/threads":
			if r.Method == http.MethodPost {
				created = append(created, r.Form.Encode())
				w.Write([]byte(`{"id":"ct1"}`))
				return
			}
			w.Write([]byte(`{"data":[{"id":"t1","text":"Hajánek","permalink":"https://www.threads.net/@ol1n/post/DTh1","shortcode":"DTh1","timestamp":"2026-11-11T08:00:00+0000","media_type":"TEXT_POST"},
				{"id":"t2","media_type":"REPOST_FACADE","permalink":"https://www.threads.net/@x/post/DZ"}]}`))
		case "/me/threads_insights":
			w.Write([]byte(`{"data":[{"name":"followers_count","total_value":{"value":58}}]}`))
		case "/t1/insights":
			w.Write([]byte(`{"data":[{"name":"views","values":[{"value":300}]},{"name":"likes","values":[{"value":12}]},{"name":"replies","values":[{"value":2}]},{"name":"quotes","values":[{"value":1}]}]}`))
		case "/me/mentions":
			w.Write([]byte(`{"data":[{"id":"x1","text":"@ol1n hi","username":"fan","timestamp":"2026-11-11T09:00:00+0000"}]}`))
		case "/t1/replies":
			w.Write([]byte(`{"data":[{"id":"r1","text":"nice","username":"fan2"},{"id":"r2","text":"thanks","username":"ol1n"}]}`))
		case "/me/threads_publish":
			w.Write([]byte(`{"id":"pub1"}`))
		case "/pub1":
			w.Write([]byte(`{"permalink":"https://www.threads.net/@ol1n/post/DNEW","shortcode":"DNEW"}`))
		default:
			t.Errorf("unexpected %s", r.URL)
		}
	}))
	defer srv.Close()
	a := New(srv.Client(), tokens{})
	a.Base = srv.URL
	b, _, err := a.Collect(context.Background(), collectors.Request{Account: acct, Capability: platform.CapProfile})
	if err != nil || len(b.Snapshots) != 2 || b.Skipped != 1 {
		t.Fatalf("%+v %v", b, err)
	}
	if p := b.Snapshots[0]; p.Content.ExternalID != "DTh1" || *p.Views != 300 || *p.Comments != 2 || p.Extra["quotes"] != 1 || *b.Snapshots[1].Followers != 58 {
		t.Fatalf("%+v", p)
	}
	b, _, err = a.Collect(context.Background(), collectors.Request{Account: acct, Capability: platform.CapInbound})
	if err != nil || len(b.Inbound) != 2 || b.Inbound[0].Kind != "mention" || b.Inbound[1].Kind != "reply" {
		t.Fatalf("%+v %v", b, err)
	}
	res, err := Publisher{a}.Publish(context.Background(), acct, publish.Post{Text: "Nový díl", Extra: map[string]string{"link": "https://olin.now/h"}})
	if err != nil || res.ExternalID != "DNEW" || !strings.Contains(created[0], "link_attachment=https%3A%2F%2Folin.now%2Fh") || !strings.Contains(created[0], "media_type=TEXT") {
		t.Fatalf("%+v %v %v", res, err, created)
	}
}
