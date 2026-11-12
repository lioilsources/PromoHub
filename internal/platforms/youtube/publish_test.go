package youtube

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/lioilsources/promohub/internal/collectors"
	"github.com/lioilsources/promohub/internal/config"
	"github.com/lioilsources/promohub/internal/creds"
	"github.com/lioilsources/promohub/internal/publish"
)

type tokens struct{}

func (tokens) Token(context.Context, config.Account) (creds.Token, error) {
	return creds.Token{AccessToken: "ya29"}, nil
}

func TestAddAnalytics(t *testing.T) {
	b := collectors.Batch{Snapshots: []collectors.Snapshot{{Content: &collectors.Content{ExternalID: "vid1"}}, {}}}
	AddAnalytics(&b, [][]any{{"vid1", 900.0, 120.0, 33.0, 4.0}, {"other", 1.0, 1.0, 1.0, 0.0}})
	if e := b.Snapshots[0].Extra; e["watch_minutes_28d"] != 120 || e["avg_view_seconds_28d"] != 33 || e["subscribers_gained_28d"] != 4 {
		t.Fatalf("%+v", e)
	}
}

func TestResumableUpload(t *testing.T) {
	var meta map[string]map[string]any
	var body []byte
	var srvURL string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/videos":
			if r.URL.Query().Get("uploadType") != "resumable" || r.Header.Get("X-Upload-Content-Length") != "5" || r.Header.Get("Authorization") != "Bearer ya29" {
				t.Errorf("%s %v", r.URL, r.Header)
			}
			_ = json.NewDecoder(r.Body).Decode(&meta)
			w.Header().Set("Location", srvURL+"/session/1")
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodPut && r.URL.Path == "/session/1":
			body, _ = io.ReadAll(r.Body)
			w.Write([]byte(`{"id":"newvid"}`))
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL)
		}
	}))
	defer srv.Close()
	srvURL = srv.URL
	a := &Adapter{HTTP: srv.Client(), Tokens: tokens{}, UploadBase: srv.URL, Now: func() time.Time { return time.Unix(0, 0) }}
	p := filepath.Join(t.TempDir(), "s.mp4")
	os.WriteFile(p, []byte("video"), 0o600)
	res, err := Publisher{a}.Publish(context.Background(), config.Account{Platform: "youtube", Handle: "ol1n"},
		publish.Post{Title: "SwypeKids 2.0", Text: "desc", Lang: "en", Media: []publish.Media{{Path: p, Mime: "video/mp4"}}})
	if err != nil || res.ExternalID != "newvid" || string(body) != "video" || meta["snippet"]["title"] != "SwypeKids 2.0" || meta["status"]["privacyStatus"] != "public" {
		t.Fatalf("%+v %v %q %+v", res, err, body, meta)
	}
}
