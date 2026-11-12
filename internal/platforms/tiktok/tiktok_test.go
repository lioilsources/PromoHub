package tiktok

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lioilsources/promohub/internal/collectors"
	"github.com/lioilsources/promohub/internal/config"
	"github.com/lioilsources/promohub/internal/creds"
	"github.com/lioilsources/promohub/internal/platform"
	"github.com/lioilsources/promohub/internal/publish"
)

// Answers follow the Display API (user/info, video/list) and Content Posting API (inbox
// video init + PUT upload) references.

type tokens struct{}

func (tokens) Token(context.Context, config.Account) (creds.Token, error) {
	return creds.Token{AccessToken: "act.1"}, nil
}

var acct = config.Account{Platform: "tiktok", Handle: "ol1n", AuthRef: "oauth"}

func TestChunks(t *testing.T) {
	if c, n := Chunks(3 << 20); c != 3<<20 || n != 1 {
		t.Fatal(c, n)
	}
	if c, n := Chunks(95 << 20); c != 10<<20 || n != 9 { // last chunk: 15 MB
		t.Fatal(c, n)
	}
}

func TestProfileAndInboxUpload(t *testing.T) {
	var got []byte
	var rng string
	var srvURL string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/upload" && r.Header.Get("Authorization") != "Bearer act.1" {
			t.Errorf("auth %q", r.Header.Get("Authorization"))
		}
		switch r.URL.Path {
		case "/v2/user/info/":
			w.Write([]byte(`{"data":{"user":{"username":"ol1n","follower_count":77,"likes_count":900,"video_count":5}},"error":{"code":"ok"}}`))
		case "/v2/video/list/":
			w.Write([]byte(`{"data":{"videos":[{"id":"7665","video_description":"SwypeKids","create_time":1784664542,"share_url":"https://www.tiktok.com/@ol1n/video/7665","view_count":1000,"like_count":50}],"has_more":false},"error":{"code":"ok"}}`))
		case "/v2/post/publish/inbox/video/init/":
			var in map[string]map[string]any
			_ = json.NewDecoder(r.Body).Decode(&in)
			if in["source_info"]["source"] != "FILE_UPLOAD" || in["source_info"]["total_chunk_count"] != float64(1) {
				t.Errorf("%+v", in)
			}
			w.Write([]byte(`{"data":{"publish_id":"v_inbox_1","upload_url":"` + srvURL + `/upload"},"error":{"code":"ok"}}`))
		case "/upload":
			rng = r.Header.Get("Content-Range")
			got, _ = io.ReadAll(r.Body)
			w.WriteHeader(http.StatusCreated)
		}
	}))
	defer srv.Close()
	srvURL = srv.URL
	a := New(srv.Client(), tokens{})
	a.Base = srv.URL
	b, _, err := a.Collect(context.Background(), collectors.Request{Account: acct, Capability: platform.CapProfile})
	if err != nil || len(b.Snapshots) != 2 || b.Snapshots[0].Content.ExternalID != "7665" || *b.Snapshots[1].Followers != 77 {
		t.Fatalf("%+v %v", b, err)
	}
	if platform.ExternalID("tiktok", b.Snapshots[0].Content.URL) != "7665" {
		t.Fatal("id mismatch with the Apify path")
	}
	p := filepath.Join(t.TempDir(), "v.mp4")
	os.WriteFile(p, []byte("0123456789"), 0o600)
	res, err := Publisher{a}.Publish(context.Background(), acct, publish.Post{Text: "x", Media: []publish.Media{{Path: p, Mime: "video/mp4"}}})
	if err != nil || !res.Sent || res.ExternalID != "v_inbox_1" || !strings.Contains(res.Note, "inbox") {
		t.Fatalf("%+v %v", res, err)
	}
	if string(got) != "0123456789" || rng != "bytes 0-9/10" {
		t.Fatalf("%q %q", got, rng)
	}
}
