package ifttt

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestTrigger(t *testing.T) {
	var gotPath, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		if strings.Contains(r.URL.Path, "fail") {
			http.Error(w, "bad key secretkey", http.StatusUnauthorized)
			return
		}
		w.Write([]byte("Congratulations!"))
	}))
	defer srv.Close()

	c := NewClient("secretkey")
	c.BaseURL = srv.URL
	if err := c.Trigger(context.Background(), "promo_mention", map[string]string{"text": "hi"}); err != nil {
		t.Fatal(err)
	}
	if gotPath != "/trigger/promo_mention/json/with/key/secretkey" || gotBody != `{"text":"hi"}` {
		t.Fatalf("path=%s body=%s", gotPath, gotBody)
	}

	err := c.Trigger(context.Background(), "fail", nil)
	se, ok := err.(*StatusError)
	if !ok || se.Code != 401 || se.Retryable() || strings.Contains(err.Error(), "secretkey") {
		t.Fatalf("err = %v", err)
	}

	c.BaseURL = "http://127.0.0.1:1"
	if err := c.Trigger(context.Background(), "x", nil); err == nil || strings.Contains(err.Error(), "secretkey") {
		t.Fatalf("transport error leaks key: %v", err)
	}
}

func TestExternalID(t *testing.T) {
	cases := []struct{ platform, url, want string }{
		{"youtube", "https://www.youtube.com/watch?v=abc123&t=1", "abc123"},
		{"youtube", "https://youtu.be/abc123", "abc123"},
		{"youtube", "https://www.youtube.com/shorts/xyz", "xyz"},
		{"bluesky", "https://bsky.app/profile/ol1n.bsky.social/post/3kabc", "3kabc"},
		{"instagram", "https://www.instagram.com/reel/Cx1/", "Cx1"},
		{"youtube", "not a url", "not a url"},
	}
	for _, c := range cases {
		if got := ExternalID(c.platform, c.url); got != c.want {
			t.Errorf("%s %s = %q, want %q", c.platform, c.url, got, c.want)
		}
	}
}

func TestParseInbound(t *testing.T) {
	in, err := ParseInbound([]byte(`{"url":"u","published_at":"October 29, 2026 at 10:00AM"}`), "YouTube_New_Video")
	if err != nil || in.Event != EventYouTubeNewVideo || in.Published() == nil || in.Published().Hour() != 10 {
		t.Fatalf("%+v %v", in, err)
	}
	if _, err := ParseInbound([]byte(`{}`), ""); err == nil {
		t.Fatal("expected missing event error")
	}
}
