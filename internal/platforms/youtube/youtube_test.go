package youtube

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
)

// Answers follow the documented channels/playlistItems/videos/commentThreads resources.

func setup(t *testing.T, key string) *Adapter {
	mux := http.NewServeMux()
	check := func(r *http.Request) {
		if r.URL.Query().Get("key") != "yt-key-1" {
			t.Errorf("%s without key", r.URL.Path)
		}
	}
	mux.HandleFunc("GET /channels", func(w http.ResponseWriter, r *http.Request) {
		check(r)
		if r.URL.Query().Get("forHandle") != "@ol1n" {
			t.Errorf("forHandle=%q", r.URL.Query().Get("forHandle"))
		}
		w.Write([]byte(`{"items":[{"id":"UCme","statistics":{"viewCount":"5400","subscriberCount":"120","hiddenSubscriberCount":false,"videoCount":"12"},
			"contentDetails":{"relatedPlaylists":{"uploads":"UUme"}}}]}`))
	})
	mux.HandleFunc("GET /playlistItems", func(w http.ResponseWriter, r *http.Request) {
		check(r)
		w.Write([]byte(`{"items":[{"contentDetails":{"videoId":"vid1"}},{"contentDetails":{"videoId":"vid2"}}]}`))
	})
	mux.HandleFunc("GET /videos", func(w http.ResponseWriter, r *http.Request) {
		check(r)
		if r.URL.Query().Get("id") != "vid1,vid2" {
			t.Errorf("id=%q", r.URL.Query().Get("id"))
		}
		w.Write([]byte(`{"items":[{"id":"vid1","snippet":{"title":"SwypeKids trailer","description":"d","publishedAt":"2026-11-01T10:00:00Z"},
			"statistics":{"viewCount":"900","likeCount":"40","commentCount":"3"}},
			{"id":"vid2","snippet":{"title":"Short","publishedAt":"2026-11-02T10:00:00Z"},"statistics":{"viewCount":"50"}}]}`))
	})
	mux.HandleFunc("GET /commentThreads", func(w http.ResponseWriter, r *http.Request) {
		check(r)
		w.Write([]byte(`{"items":[
			{"id":"t1","snippet":{"videoId":"vid1","topLevelComment":{"id":"c1","snippet":{"authorDisplayName":"@fan","authorChannelId":{"value":"UCfan"},
			 "textOriginal":"love it","publishedAt":"2026-11-03T10:00:00Z"}}}},
			{"id":"t2","snippet":{"videoId":"vid1","topLevelComment":{"id":"c2","snippet":{"authorDisplayName":"@ol1n","authorChannelId":{"value":"UCme"},
			 "textOriginal":"thanks","publishedAt":"2026-11-03T11:00:00Z"}}}}]}`))
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte(`{"error":{"message":"key=yt-key-1 quotaExceeded"}}`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	old := creds.Getenv
	creds.Getenv = func(k string) string {
		if k == "YOUTUBE_API_KEY" {
			return key
		}
		return ""
	}
	t.Cleanup(func() { creds.Getenv = old })
	a := New(srv.Client())
	a.Base = srv.URL
	return a
}

var acct = config.Account{Platform: "youtube", Handle: "ol1n"}

func TestProfileAndComments(t *testing.T) {
	a := setup(t, "yt-key-1")
	b, _, err := a.Collect(context.Background(), collectors.Request{Account: acct, Capability: platform.CapProfile})
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Snapshots) != 3 {
		t.Fatalf("%+v", b)
	}
	v := b.Snapshots[0]
	if v.Content.ExternalID != "vid1" || *v.Views != 900 || *v.Likes != 40 || v.Content.Title != "SwypeKids trailer" ||
		platform.ExternalID("youtube", v.Content.URL) != "vid1" {
		t.Fatalf("%+v", v)
	}
	if b.Snapshots[1].Likes != nil {
		t.Fatal("missing likeCount must stay nil")
	}
	if ch := b.Snapshots[2]; *ch.Followers != 120 || ch.Extra["videos"] != 12 {
		t.Fatalf("%+v", ch)
	}
	b, _, err = a.Collect(context.Background(), collectors.Request{Account: acct, Capability: platform.CapInbound})
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Inbound) != 1 || b.Inbound[0].ExternalID != "c1" || b.Inbound[0].Kind != "comment" || b.Skipped != 1 ||
		b.Inbound[0].URL != "https://www.youtube.com/watch?v=vid1&lc=c1" {
		t.Fatalf("%+v", b)
	}
}

func TestKeyNeverInErrors(t *testing.T) {
	a := setup(t, "yt-key-1")
	c, _, _ := a.client(acct)
	err := c.Get(context.Background(), "/search", nil, nil)
	if err == nil || strings.Contains(err.Error(), "yt-key-1") {
		t.Fatalf("err %v", err)
	}
	if err := setup(t, "").Ready(acct, platform.CapProfile); err == nil {
		t.Fatal("expected missing key")
	}
}
