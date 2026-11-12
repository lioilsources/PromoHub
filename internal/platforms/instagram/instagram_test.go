package instagram

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lioilsources/promohub/internal/collectors"
	"github.com/lioilsources/promohub/internal/config"
	"github.com/lioilsources/promohub/internal/creds"
	"github.com/lioilsources/promohub/internal/platform"
	"github.com/lioilsources/promohub/internal/publish"
)

// Answers follow the Instagram API with Instagram Login reference (IG User, IG Media,
// insights, comments, content publishing).

type tokens struct{}

func (tokens) Token(context.Context, config.Account) (creds.Token, error) {
	return creds.Token{AccessToken: "ig-token"}, nil
}

var acct = config.Account{Platform: "instagram", Handle: "ol1n.apps", AuthRef: "oauth"}

func server(t *testing.T) (*Adapter, *[]string) {
	var mu sync.Mutex
	var posts []string
	polls := map[string]int{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if r.Form.Get("access_token") != "ig-token" {
			w.WriteHeader(http.StatusBadRequest)
			w.Write([]byte(`{"error":{"message":"Invalid OAuth access token","code":190}}`))
			return
		}
		mu.Lock()
		defer mu.Unlock()
		switch {
		case r.URL.Path == "/me":
			w.Write([]byte(`{"user_id":"1784","username":"ol1n.apps","followers_count":321,"follows_count":10,"media_count":2}`))
		case r.URL.Path == "/me/media" && r.Method == http.MethodGet:
			w.Write([]byte(`{"data":[
				{"id":"m1","caption":"SwypeKids 2.0","media_type":"IMAGE","permalink":"https://www.instagram.com/p/DXa1/","shortcode":"DXa1","timestamp":"2026-11-10T10:00:00+0000","like_count":40,"comments_count":2},
				{"id":"m2","caption":"old","media_type":"VIDEO","permalink":"https://www.instagram.com/reel/DXa0/","shortcode":"DXa0","timestamp":"2026-11-01T10:00:00+0000","like_count":5,"comments_count":0}]}`))
		case r.URL.Path == "/m1/insights":
			w.Write([]byte(`{"data":[{"name":"views","values":[{"value":900}]},{"name":"saved","values":[{"value":7}]}]}`))
		case r.URL.Path == "/m2/insights":
			w.WriteHeader(http.StatusBadRequest)
			w.Write([]byte(`{"error":{"message":"metric not supported"}}`))
		case r.URL.Path == "/m1/comments":
			w.Write([]byte(`{"data":[{"id":"c1","text":"wow","username":"fan","timestamp":"2026-11-10T11:00:00+0000"},{"id":"c2","text":"díky","username":"ol1n.apps","timestamp":"2026-11-10T12:00:00+0000"}]}`))
		case r.URL.Path == "/me/media" && r.Method == http.MethodPost:
			id := "ct" + string(rune('0'+len(posts)))
			posts = append(posts, r.Form.Encode())
			w.Write([]byte(`{"id":"` + id + `"}`))
		case strings.HasPrefix(r.URL.Path, "/ct") && r.URL.Query().Get("fields") == "status_code,error_message":
			polls[r.URL.Path]++
			if polls[r.URL.Path] < 2 {
				w.Write([]byte(`{"status_code":"IN_PROGRESS"}`))
				return
			}
			w.Write([]byte(`{"status_code":"FINISHED"}`))
		case r.URL.Path == "/me/media_publish":
			posts = append(posts, "publish:"+r.Form.Get("creation_id"))
			w.Write([]byte(`{"id":"17999"}`))
		case r.URL.Path == "/17999":
			w.Write([]byte(`{"permalink":"https://www.instagram.com/p/DNEW/","shortcode":"DNEW"}`))
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL)
		}
	}))
	t.Cleanup(srv.Close)
	a := New(srv.Client(), tokens{})
	a.Base, a.PollEvery = srv.URL, time.Millisecond
	return a, &posts
}

func TestProfileAndComments(t *testing.T) {
	a, _ := server(t)
	b, _, err := a.Collect(context.Background(), collectors.Request{Account: acct, Capability: platform.CapProfile})
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Snapshots) != 3 {
		t.Fatalf("%+v", b)
	}
	p := b.Snapshots[0]
	if p.Content.ExternalID != "DXa1" || *p.Views != 900 || p.Extra["saved"] != 7 || *p.Likes != 40 || p.Content.PublishedAt.Day() != 10 {
		t.Fatalf("%+v %+v", p, p.Content)
	}
	if platform.ExternalID("instagram", p.Content.URL) != "DXa1" || b.Snapshots[1].Views != nil {
		t.Fatal("ids or missing insight")
	}
	if *b.Snapshots[2].Followers != 321 {
		t.Fatal("followers")
	}
	b, _, err = a.Collect(context.Background(), collectors.Request{Account: acct, Capability: platform.CapInbound})
	if err != nil || len(b.Inbound) != 1 || b.Inbound[0].Author != "fan" || b.Skipped != 1 {
		t.Fatalf("%+v %v", b, err)
	}
}

func TestPublishCarousel(t *testing.T) {
	a, posts := server(t)
	res, err := Publisher{a}.Publish(context.Background(), acct, publish.Post{Text: "Nová verze #swypekids", Media: []publish.Media{
		{URL: "https://promo/media/a", Mime: "image/png", Alt: "menu"},
		{URL: "https://promo/media/b", Mime: "video/mp4"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if res.ExternalID != "DNEW" || res.URL != "https://www.instagram.com/p/DNEW/" {
		t.Fatalf("%+v", res)
	}
	got := strings.Join(*posts, "\n")
	for _, want := range []string{"image_url=https%3A%2F%2Fpromo%2Fmedia%2Fa", "is_carousel_item=true", "media_type=VIDEO", "media_type=CAROUSEL", "children=ct0%2Cct1", "publish:ct2"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %s in\n%s", want, got)
		}
	}
	if _, err := (Publisher{a}).Publish(context.Background(), acct, publish.Post{Text: "text only"}); err == nil || publish.Classify(err) != publish.Failed {
		t.Fatalf("text-only must be rejected: %v", err)
	}
}
