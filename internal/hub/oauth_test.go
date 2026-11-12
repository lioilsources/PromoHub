package hub

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/lioilsources/promohub/internal/config"
	"github.com/lioilsources/promohub/internal/oauth"
	"github.com/lioilsources/promohub/internal/platform"
	"github.com/lioilsources/promohub/internal/rules"
	"github.com/lioilsources/promohub/internal/vault"
)

func TestOAuthConnectStoreRefresh(t *testing.T) {
	e := setup(t)
	clock := time.Date(2030, 1, 1, 12, 0, 0, 0, time.UTC)
	e.hub.now = func() time.Time { return clock }
	refreshOK := true
	ig := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/oauth/access_token":
			w.Write([]byte(`{"data":[{"access_token":"short","user_id":"1784"}]}`))
		case "/access_token":
			w.Write([]byte(`{"access_token":"long-1","expires_in":5184000}`))
		case "/refresh_access_token":
			if !refreshOK {
				w.WriteHeader(http.StatusBadRequest)
				w.Write([]byte(`{"error":{"message":"Session has expired"}}`))
				return
			}
			w.Write([]byte(`{"access_token":"long-2","expires_in":5184000}`))
		}
	}))
	defer ig.Close()
	v, _ := vault.New("000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f")
	h := e.hub
	h.env.PublicBaseURL = e.srv.URL
	h.oauth = &oauth.Store{Q: e.st.Queries, Vault: v, Now: h.now, Clients: map[string]oauth.Client{"instagram": {
		ID: "app", Secret: "sec", Redirect: e.srv.URL + "/oauth/instagram/callback", HTTP: ig.Client(), Now: h.now,
		Hosts: map[string]string{"instagram-auth": ig.URL, "instagram": ig.URL}}}}
	acct := config.Account{Platform: "instagram", Handle: "ol1n.apps", AuthRef: "oauth", Read: platform.ViaAPI, Publish: platform.ViaAPI, Lang: "en"}
	h.files.Accounts.Accounts = append(h.files.Accounts.Accounts, acct)
	h.files.Rules.Rules = append(h.files.Rules.Rules, config.RuleSpec{Name: "auth", On: rules.OnAuthExpiring, Then: config.StringSet{"ifttt:promo_alert"}})
	h.rules, _ = rules.New(h.files.Rules)
	if err := h.SyncConfig(context.Background()); err != nil {
		t.Fatal(err)
	}

	link, err := h.ConnectURL(acct)
	if err != nil {
		t.Fatal(err)
	}
	noFollow := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := noFollow.Get(link)
	if err != nil {
		t.Fatal(err)
	}
	loc, _ := url.Parse(resp.Header.Get("Location"))
	if resp.StatusCode != http.StatusFound || loc.Host != "www.instagram.com" || loc.Query().Get("client_id") != "app" {
		t.Fatalf("%d %s", resp.StatusCode, loc)
	}
	state := loc.Query().Get("state")
	resp, err = http.Get(e.srv.URL + "/oauth/instagram/callback?code=abc%23_&state=" + url.QueryEscape(state))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), "Hotovo") {
		t.Fatalf("%d %s", resp.StatusCode, body)
	}
	var ct []byte
	_ = e.st.Pool.QueryRow(context.Background(), `SELECT ciphertext FROM credentials`).Scan(&ct)
	if len(ct) == 0 || strings.Contains(string(ct), "long-1") {
		t.Fatal("token must be stored encrypted")
	}
	tok, err := h.oauth.Token(context.Background(), acct)
	if err != nil || tok.AccessToken != "long-1" || tok.UserID != "1784" {
		t.Fatalf("%+v %v", tok, err)
	}
	// A forged or replayed-late state is refused.
	resp, _ = http.Get(e.srv.URL + "/oauth/instagram/callback?code=x&state=forged")
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("forged state: %d", resp.StatusCode)
	}

	// 45 days later the token is inside the 20-day window and gets refreshed.
	clock = clock.Add(45 * 24 * time.Hour)
	if err := h.checkTokens(context.Background()); err != nil {
		t.Fatal(err)
	}
	if tok, _ := h.oauth.Token(context.Background(), acct); tok.AccessToken != "long-2" {
		t.Fatalf("not refreshed: %+v", tok)
	}
	// 55 days after that the refresh fails within the last week: one warning per day.
	clock = clock.Add(55 * 24 * time.Hour)
	refreshOK = false
	_ = h.checkTokens(context.Background())
	clock = clock.Add(7 * time.Hour)
	_ = h.checkTokens(context.Background())
	if err := h.deliverPending(context.Background()); err != nil {
		t.Fatal(err)
	}
	alerts := 0
	for _, s := range e.sentEvents() {
		if s.event == "promo_alert" {
			alerts++
		}
	}
	if alerts != 1 {
		t.Fatalf("alerts %d", alerts)
	}
}
