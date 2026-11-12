package composer

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lioilsources/promohub/internal/config"
	"github.com/lioilsources/promohub/internal/platform"
)

var app = &config.App{Slug: "swypekids", Name: "SwypeKids", Hashtags: []string{"kidsapps", "#indiedev"}, Links: map[string]string{"web": "https://olin.now/swypekids"}}

func campaign(body string) Campaign {
	return Campaign{Slug: "c12", App: app, Lang: "en", Body: body, Link: "https://olin.now/swypekids"}
}

func TestComposePerPlatform(t *testing.T) {
	own := OwnHosts([]config.App{*app})
	c := campaign("SwypeKids 2.0 is out: offline mode and a new swipe game.")
	cases := map[string]func(Draft) bool{
		"mastodon": func(d Draft) bool {
			return strings.Contains(d.Text, "https://olin.now/swypekids?utm_campaign=c12&utm_medium=social&utm_source=mastodon") &&
				strings.HasSuffix(d.Text, "#kidsapps #indiedev")
		},
		"bluesky": func(d Draft) bool {
			return !strings.Contains(d.Text, "https://") && strings.Contains(d.Extra["link"], "utm_source=bluesky")
		},
		"x":        func(d Draft) bool { return strings.Contains(d.Text, "utm_source=x") && !strings.Contains(d.Text, "#") },
		"facebook": func(d Draft) bool { return !strings.Contains(d.Text, "https://") && d.Extra["link"] != "" },
		"reddit":   func(d Draft) bool { return d.Extra["title"] == "SwypeKids 2.0 is out: offline mode and a new swipe game." },
		"instagram": func(d Draft) bool {
			return strings.Contains(d.Text, "Link in bio.") && !strings.Contains(d.Text, "https://")
		},
	}
	for name, ok := range cases {
		acct := config.Account{Platform: name, Handle: "h@mastodon.social"}
		if d := Compose(acct, c, own); !ok(d) {
			t.Errorf("%s: %q %v", name, d.Text, d.Extra)
		}
	}
}

func TestComposeTrimsToLimit(t *testing.T) {
	body := strings.Repeat("Hajánek uspává děti 👶 ", 40)
	for _, name := range []string{"bluesky", "x", "mastodon"} {
		spec, _ := platform.Lookup(name)
		d := Compose(config.Account{Platform: name, Handle: "h@m.s"}, campaign(body), nil)
		if !spec.Fits(d.Text) || !strings.Contains(d.Text, "…") {
			t.Errorf("%s: %d/%d %q", name, spec.Length(d.Text), spec.TextLimit, d.Text)
		}
		if name == "mastodon" && !strings.Contains(d.Text, "https://olin.now/swypekids") {
			t.Errorf("link must survive trimming: %q", d.Text)
		}
	}
}

func TestUTMLeavesForeignLinks(t *testing.T) {
	if got := UTM("https://apps.apple.com/app/id1", "x", "c", []string{"olin.now"}); got != "https://apps.apple.com/app/id1" {
		t.Fatal(got)
	}
}

func TestLLMRewrite(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Messages []struct{ Content string } `json:"messages"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		if r.URL.Path != "/v1/chat/completions" || !strings.Contains(req.Messages[1].Content, `"account":"bluesky:a"`) {
			t.Errorf("%s %+v", r.URL.Path, req)
		}
		w.Write([]byte("{\"choices\":[{\"message\":{\"content\":\"```json\\n{\\\"bluesky:a\\\":\\\"New SwypeKids!\\\"}\\n```\"}}]}"))
	}))
	defer srv.Close()
	l := &LLM{BaseURL: srv.URL + "/v1", Model: "m", HTTP: srv.Client()}
	out, err := l.Rewrite(context.Background(), campaign("x"), []Target{{Key: "bluesky:a", Platform: "bluesky", Limit: 300, Lang: "en"}})
	if err != nil || out["bluesky:a"] != "New SwypeKids!" {
		t.Fatalf("%v %v", out, err)
	}
	if _, err := (&LLM{}).Rewrite(context.Background(), campaign("x"), nil); err == nil {
		t.Fatal("unconfigured LLM must error so callers fall back")
	}
}
