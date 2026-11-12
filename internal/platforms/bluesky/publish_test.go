package bluesky

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

	"github.com/lioilsources/promohub/internal/publish"
)

func TestFacetsUseByteOffsets(t *testing.T) {
	text := "Hajánek 👶 je venku https://olin.now/h. #uspávánky #2026"
	fs := Facets(text)
	if len(fs) != 2 { // "#2026" is digits only, not a tag
		t.Fatalf("%+v", fs)
	}
	link := fs[0]["index"].(map[string]int)
	if got := text[link["byteStart"]:link["byteEnd"]]; got != "https://olin.now/h" {
		t.Fatalf("link facet covers %q", got)
	}
	tag := fs[1]["index"].(map[string]int)
	if got := text[tag["byteStart"]:tag["byteEnd"]]; got != "#uspávánky" {
		t.Fatalf("tag facet covers %q", got)
	}
}

func TestParseOG(t *testing.T) {
	og := parseOG(`<html><head><meta property="og:title" content="SwypeKids &amp; friends">
		<meta name="description" content="plain"><meta content='https://x/i.png' property='og:image'></head>`)
	if og.title != "SwypeKids & friends" || og.description != "plain" || og.image != "https://x/i.png" {
		t.Fatalf("%+v", og)
	}
}

func TestPublishWithImageAndCard(t *testing.T) {
	withSecret(t, "app-pass-1234")
	var records []map[string]any
	var uploads int
	mux := http.NewServeMux()
	mux.HandleFunc("POST /xrpc/com.atproto.server.createSession", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"accessJwt":"jwt-1","did":"did:plc:me","handle":"ol1n.bsky.social"}`))
	})
	mux.HandleFunc("POST /xrpc/com.atproto.repo.uploadBlob", func(w http.ResponseWriter, r *http.Request) {
		uploads++
		if r.Header.Get("Content-Type") != "image/png" {
			t.Errorf("blob type %q", r.Header.Get("Content-Type"))
		}
		io.Copy(io.Discard, r.Body)
		w.Write([]byte(`{"blob":{"$type":"blob","ref":{"$link":"bafk"},"mimeType":"image/png","size":4}}`))
	})
	mux.HandleFunc("POST /xrpc/com.atproto.repo.createRecord", func(w http.ResponseWriter, r *http.Request) {
		var in map[string]any
		_ = json.NewDecoder(r.Body).Decode(&in)
		records = append(records, in["record"].(map[string]any))
		w.Write([]byte(`{"uri":"at://did:plc:me/app.bsky.feed.post/3new","cid":"c"}`))
	})
	mux.HandleFunc("GET /page", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(`<meta property="og:title" content="Page title">`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	a := New(srv.Client())
	a.AppView, a.PDS = srv.URL, srv.URL
	a.Now = func() time.Time { return time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC) }
	p := Publisher{a}

	img := filepath.Join(t.TempDir(), "a.png")
	os.WriteFile(img, []byte("\x89PNG"), 0o600)
	res, err := p.Publish(context.Background(), acct, publish.Post{Text: "New #swypekids", Lang: "en",
		Media: []publish.Media{{Path: img, Mime: "image/png", Alt: "screenshot", Width: 3, Height: 2}}})
	if err != nil {
		t.Fatal(err)
	}
	if res.ExternalID != "3new" || res.URL != "https://bsky.app/profile/ol1n.bsky.social/post/3new" || uploads != 1 {
		t.Fatalf("%+v uploads=%d", res, uploads)
	}
	if e := records[0]["embed"].(map[string]any); e["$type"] != "app.bsky.embed.images" || records[0]["createdAt"] != "2026-11-12T10:00:00.000Z" {
		t.Fatalf("%+v", records[0])
	}

	_, err = p.Publish(context.Background(), acct, publish.Post{Text: "Read this", Extra: map[string]string{"link": srv.URL + "/page"}})
	if err != nil {
		t.Fatal(err)
	}
	ext := records[1]["embed"].(map[string]any)["external"].(map[string]any)
	if ext["title"] != "Page title" || ext["uri"] != srv.URL+"/page" {
		t.Fatalf("%+v", ext)
	}
}

func TestPublishRejectsOnBadPassword(t *testing.T) {
	withSecret(t, "wrong")
	a := adapter(t, new(string))
	_, err := Publisher{a}.Publish(context.Background(), acct, publish.Post{Text: "x"})
	if err == nil || publish.Classify(err) != publish.Failed {
		t.Fatalf("login failure must be a clean failure: %v", err)
	}
}

func TestPublishVideoEmbed(t *testing.T) {
	withSecret(t, "app-pass-1234")
	var rec map[string]any
	mux := http.NewServeMux()
	mux.HandleFunc("POST /xrpc/com.atproto.server.createSession", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"accessJwt":"jwt-1","did":"did:plc:me","handle":"ol1n.bsky.social"}`))
	})
	mux.HandleFunc("POST /xrpc/com.atproto.repo.uploadBlob", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Content-Type") != "video/mp4" {
			t.Errorf("type %q", r.Header.Get("Content-Type"))
		}
		w.Write([]byte(`{"blob":{"$type":"blob","mimeType":"video/mp4","size":5}}`))
	})
	mux.HandleFunc("POST /xrpc/com.atproto.repo.createRecord", func(w http.ResponseWriter, r *http.Request) {
		var in map[string]any
		_ = json.NewDecoder(r.Body).Decode(&in)
		rec = in["record"].(map[string]any)
		w.Write([]byte(`{"uri":"at://did:plc:me/app.bsky.feed.post/3vid"}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	a := New(srv.Client())
	a.AppView, a.PDS = srv.URL, srv.URL
	p := filepath.Join(t.TempDir(), "v.mp4")
	os.WriteFile(p, []byte("video"), 0o600)
	if _, err := (Publisher{a}).Publish(context.Background(), acct, publish.Post{Text: "clip", Media: []publish.Media{{Path: p, Mime: "video/mp4", Width: 1080, Height: 1920}}}); err != nil {
		t.Fatal(err)
	}
	if e := rec["embed"].(map[string]any); e["$type"] != "app.bsky.embed.video" || e["aspectRatio"] == nil {
		t.Fatalf("%+v", e)
	}
}
