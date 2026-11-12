package mastodon

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lioilsources/promohub/internal/creds"
	"github.com/lioilsources/promohub/internal/publish"
)

func TestPublishWithMediaAndIdempotencyKey(t *testing.T) {
	polls := 0
	var status map[string]any
	var key string
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v2/media", func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(1 << 20); err != nil || r.FormValue("description") != "alt" {
			t.Errorf("multipart: %v %q", err, r.FormValue("description"))
		}
		w.WriteHeader(http.StatusAccepted)
		w.Write([]byte(`{"id":"m1","url":null}`))
	})
	mux.HandleFunc("GET /api/v1/media/m1", func(w http.ResponseWriter, r *http.Request) {
		polls++
		if polls < 2 {
			w.WriteHeader(http.StatusPartialContent)
			w.Write([]byte(`{"id":"m1","url":null}`))
			return
		}
		w.Write([]byte(`{"id":"m1","url":"https://files/m1.png"}`))
	})
	mux.HandleFunc("POST /api/v1/statuses", func(w http.ResponseWriter, r *http.Request) {
		key = r.Header.Get("Idempotency-Key")
		_ = json.NewDecoder(r.Body).Decode(&status)
		w.Write([]byte(`{"id":"1176","url":"https://mastodon.social/@ol1n/1176"}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	old := creds.Getenv
	creds.Getenv = func(k string) string {
		if k == "MASTODON_MAIN_TOKEN" {
			return "tok"
		}
		return ""
	}
	defer func() { creds.Getenv = old }()
	a := New(srv.Client())
	a.Base, a.PollEvery = srv.URL, time.Millisecond
	img := filepath.Join(t.TempDir(), "a.png")
	os.WriteFile(img, []byte("\x89PNG"), 0o600)
	res, err := Publisher{a}.Publish(context.Background(), acct, publish.Post{Text: "Hi #swypekids", Lang: "cs", AttemptKey: "k-42",
		Media: []publish.Media{{Path: img, Mime: "image/png", Alt: "alt"}}})
	if err != nil {
		t.Fatal(err)
	}
	if res.ExternalID != "1176" || key != "k-42" || polls != 2 {
		t.Fatalf("%+v key=%q polls=%d", res, key, polls)
	}
	ids, _ := status["media_ids"].([]any)
	if status["visibility"] != "public" || status["language"] != "cs" || len(ids) != 1 || !strings.Contains(status["status"].(string), "#swypekids") {
		t.Fatalf("%+v", status)
	}
}
