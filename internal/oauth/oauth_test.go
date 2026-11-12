package oauth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/lioilsources/promohub/internal/config"
	"github.com/lioilsources/promohub/internal/vault"
)

func TestState(t *testing.T) {
	v, _ := vault.New("000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f")
	now := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	s := State(v, "instagram:ol1n", now)
	if k, err := ParseState(v, s, now.Add(time.Minute)); err != nil || k != "instagram:ol1n" {
		t.Fatalf("%q %v", k, err)
	}
	if _, err := ParseState(v, s, now.Add(StateTTL+time.Second)); err == nil {
		t.Fatal("expired state accepted")
	}
	if _, err := ParseState(v, strings.Replace(s, "a", "b", 1), now); err == nil {
		t.Fatal("tampered state accepted")
	}
}

func TestAuthorizeURL(t *testing.T) {
	u, _ := url.Parse(youtube.AuthorizeURL("cid", "https://h/oauth/youtube/callback", "st"))
	q := u.Query()
	if q.Get("access_type") != "offline" || q.Get("prompt") != "consent" || !strings.Contains(q.Get("scope"), " ") || q.Get("state") != "st" {
		t.Fatal(u)
	}
	u, _ = url.Parse(tiktok.AuthorizeURL("ck", "r", "st"))
	if u.Query().Get("client_key") != "ck" || u.Query().Get("client_id") != "" {
		t.Fatal(u)
	}
}

func TestInstagramExchange(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/oauth/access_token":
			_ = r.ParseForm()
			if r.Form.Get("code") != "abc" || r.Form.Get("client_secret") != "sec" {
				t.Errorf("form %v", r.Form)
			}
			w.Write([]byte(`{"data":[{"access_token":"short","user_id":"1784","permissions":"instagram_business_basic"}]}`))
		case "/access_token":
			if r.URL.Query().Get("grant_type") != "ig_exchange_token" || r.URL.Query().Get("access_token") != "short" {
				t.Errorf("%s", r.URL)
			}
			w.Write([]byte(`{"access_token":"long","token_type":"bearer","expires_in":5184000}`))
		}
	}))
	defer srv.Close()
	now := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	c := Client{ID: "id", Secret: "sec", Redirect: "r", HTTP: srv.Client(), Now: func() time.Time { return now },
		Hosts: map[string]string{"instagram-auth": srv.URL, "instagram": srv.URL}}
	tok, err := instagram.Exchange(context.Background(), c, "abc#_", config.Account{})
	if err != nil {
		t.Fatal(err)
	}
	if tok.AccessToken != "long" || tok.UserID != "1784" || !tok.ExpiresAt.Equal(now.Add(60*24*time.Hour)) {
		t.Fatalf("%+v", tok)
	}
}

func TestFacebookPicksThePage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/oauth/access_token"):
			w.Write([]byte(`{"access_token":"user-token","expires_in":5000000}`))
		case strings.HasSuffix(r.URL.Path, "/me/accounts"):
			w.Write([]byte(`{"data":[{"id":"111","name":"Other","access_token":"p1"},{"id":"222","name":"Ol1n Apps","access_token":"p2"}]}`))
		}
	}))
	defer srv.Close()
	c := Client{ID: "id", Secret: "sec", HTTP: srv.Client(), Hosts: map[string]string{"facebook": srv.URL}}
	tok, err := facebook.Exchange(context.Background(), c, "code", config.Account{ExternalID: "222"})
	if err != nil || tok.AccessToken != "p2" || tok.PageID != "222" {
		t.Fatalf("%+v %v", tok, err)
	}
	if _, err := facebook.Exchange(context.Background(), c, "code", config.Account{ExternalID: "333"}); err == nil {
		t.Fatal("unknown page must fail")
	}
}
