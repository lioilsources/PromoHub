package hub

import (
	"context"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/lioilsources/promohub/internal/config"
	"github.com/lioilsources/promohub/internal/oauth"
	"github.com/lioilsources/promohub/internal/rules"
	"github.com/lioilsources/promohub/internal/store/db"
	"github.com/lioilsources/promohub/internal/tgbot"
)

const (
	tokenCheckEvery = 6 * time.Hour
	authWarnBefore  = 7 * 24 * time.Hour
)

// ConnectURL is the link /connect hands out; it is valid for oauth.StateTTL.
func (h *Hub) ConnectURL(acct config.Account) (string, error) {
	if h.oauth == nil {
		return "", fmt.Errorf("PROMOHUB_SECRET_KEY is not set, the hub cannot keep OAuth tokens")
	}
	p, err := oauth.Lookup(acct.Platform)
	if err != nil {
		return "", err
	}
	if c := h.oauth.Clients[p.Name]; c.ID == "" || c.Secret == "" {
		return "", fmt.Errorf("%s / %s are not set", p.ClientEnv, p.SecretEnv)
	}
	return h.env.PublicBaseURL + "/oauth/" + acct.Platform + "/start?state=" + url.QueryEscape(oauth.State(h.oauth.Vault, acct.Key(), h.now())), nil
}

// RedirectURI is what the platform's app settings must list.
func RedirectURI(base, platformName string) string {
	return base + "/oauth/" + platformName + "/callback"
}

func (h *Hub) oauthAccount(r *http.Request) (*oauth.Provider, config.Account, error) {
	if h.oauth == nil {
		return nil, config.Account{}, fmt.Errorf("OAuth is not configured")
	}
	p, err := oauth.Lookup(r.PathValue("platform"))
	if err != nil {
		return nil, config.Account{}, err
	}
	key, err := oauth.ParseState(h.oauth.Vault, r.URL.Query().Get("state"), h.now())
	if err != nil {
		return nil, config.Account{}, err
	}
	acct, ok := h.files.Account(key)
	if !ok || acct.Platform != p.Name {
		return nil, config.Account{}, fmt.Errorf("account %s is not in accounts.yaml", key)
	}
	return p, acct, nil
}

func (h *Hub) oauthStart(w http.ResponseWriter, r *http.Request) {
	p, _, err := h.oauthAccount(r)
	if err != nil {
		oauthPage(w, http.StatusBadRequest, "Odkaz nefunguje", err.Error())
		return
	}
	c := h.oauth.Clients[p.Name]
	http.Redirect(w, r, p.AuthorizeURL(c.ID, c.Redirect, r.URL.Query().Get("state")), http.StatusFound)
}

func (h *Hub) oauthCallback(w http.ResponseWriter, r *http.Request) {
	p, acct, err := h.oauthAccount(r)
	if err != nil {
		oauthPage(w, http.StatusBadRequest, "Odkaz nefunguje", err.Error())
		return
	}
	if e := r.URL.Query().Get("error"); e != "" {
		oauthPage(w, http.StatusOK, "Zamítnuto", e+": "+r.URL.Query().Get("error_description"))
		return
	}
	code := r.URL.Query().Get("code")
	if code == "" {
		oauthPage(w, http.StatusBadRequest, "Chybí kód", "Platforma nevrátila code.")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	tok, err := p.Exchange(ctx, h.oauth.Clients[p.Name], code, acct)
	if err != nil {
		h.log.Warn("oauth exchange", "account", acct.Key(), "err", err)
		oauthPage(w, http.StatusBadGateway, "Výměna kódu selhala", err.Error())
		return
	}
	if err := h.oauth.Put(ctx, h.accountIDs[acct.Key()], p.Name, tok); err != nil {
		h.log.Error("store token", "account", acct.Key(), "err", err)
		oauthPage(w, http.StatusInternalServerError, "Uložení selhalo", "Zkus to znovu.")
		return
	}
	h.log.Info("oauth connected", "account", acct.Key(), "expires", tok.ExpiresAt)
	if h.bot != nil && len(h.admins) > 0 {
		_, _ = h.bot.Send(ctx, h.admins[0], "✅ "+acct.Key()+" připojeno.", tgbot.Opts{})
	}
	// Collectors that were "not connected" can run now.
	_, _ = h.store.Pool.Exec(ctx, `UPDATE collector_state SET next_run_at = now() WHERE account_id = $1`, h.accountIDs[acct.Key()])
	oauthPage(w, http.StatusOK, "Hotovo", acct.Key()+" je připojený. Okno můžeš zavřít.")
}

// oauthDeauthorize acknowledges Meta's deauthorize and data-deletion pings.
func (h *Hub) oauthDeauthorize(w http.ResponseWriter, r *http.Request) {
	h.log.Warn("oauth deauthorize ping", "platform", r.PathValue("platform"))
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func oauthPage(w http.ResponseWriter, code int, title, msg string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(code)
	fmt.Fprintf(w, `<!doctype html><meta charset="utf-8"><meta name="viewport" content="width=device-width"><title>promohub</title>`+
		`<body style="font-family:system-ui;margin:2em"><h1>%s</h1><p>%s</p>`, html.EscapeString(title), html.EscapeString(msg))
}

// cmdConnect answers /connect platform:handle with the OAuth link.
func (h *Hub) cmdConnect(ctx context.Context, chat int64, key string) string {
	acct, ok := h.files.Account(strings.TrimSpace(key))
	if !ok {
		var keys []string
		for _, a := range h.files.Accounts.Accounts {
			if _, err := oauth.Lookup(a.Platform); err == nil {
				keys = append(keys, a.Key())
			}
		}
		return "Použití: /connect platform:handle. OAuth účty: " + strings.Join(keys, ", ")
	}
	u, err := h.ConnectURL(acct)
	if err != nil {
		return err.Error()
	}
	_, err = h.bot.Send(ctx, chat, "Připoj "+acct.Key()+" (odkaz platí 10 minut):", tgbot.Opts{Keyboard: tgbot.Keyboard{{{Text: "🔐 Připojit", URL: u}}}})
	if err != nil {
		return err.Error()
	}
	return ""
}

// checkTokens refreshes OAuth tokens before they expire and warns once a day about tokens
// that are about to die or failed to refresh.
func (h *Hub) checkTokens(ctx context.Context) error {
	if h.oauth == nil || h.now().Sub(h.lastTokenCheck) < tokenCheckEvery {
		return nil
	}
	h.lastTokenCheck = h.now()
	rows, err := h.store.ListCredentials(ctx)
	if err != nil {
		return err
	}
	for _, row := range rows {
		acct, ok := h.accounts[row.AccountID]
		if !ok {
			continue
		}
		t, err := h.oauth.Get(ctx, row.AccountID, acct)
		if err == nil {
			var refreshed bool
			t, refreshed, err = h.oauth.RefreshIfDue(ctx, row.AccountID, acct, t)
			if refreshed {
				h.log.Info("oauth token refreshed", "account", acct.Key(), "expires", t.ExpiresAt)
			}
		}
		problem := ""
		switch {
		case err != nil:
			problem = err.Error()
		case t.ExpiresAt != nil && t.ExpiresAt.Sub(h.now()) < authWarnBefore && t.RefreshToken == "":
			problem = "token expires " + t.ExpiresAt.In(prague).Format("2. 1. 15:04")
		case t.RefreshExpiresAt != nil && t.RefreshExpiresAt.Sub(h.now()) < authWarnBefore:
			problem = "refresh token expires " + t.RefreshExpiresAt.In(prague).Format("2. 1. 15:04")
		}
		if t.ExpiresAt != nil {
			h.metrics.Set("promohub_token_expiry_seconds", t.ExpiresAt.Sub(h.now()).Seconds(), "account", acct.Key())
		}
		if problem != "" {
			h.warnAuth(ctx, acct, problem)
		}
	}
	return nil
}

func (h *Hub) warnAuth(ctx context.Context, acct config.Account, problem string) {
	key := "auth_alert:" + acct.Key() + ":" + h.now().In(prague).Format("2006-01-02")
	if _, err := h.store.GetKV(ctx, key); err == nil {
		return
	}
	_ = h.store.SetKV(ctx, db.SetKVParams{Key: key, Value: problem})
	err := h.store.Tx(ctx, func(q *db.Queries) error {
		v := rules.Vars{Num: map[string]float64{}, Str: map[string]string{"platform": acct.Platform, "account": acct.Key(), "error": problem}}
		_, err := h.fire(ctx, q, rules.OnAuthExpiring, v, map[string]any{"platform": acct.Platform, "account": acct.Key(), "error": problem})
		return err
	})
	if err != nil {
		h.log.Error("auth warning", "account", acct.Key(), "err", err)
	}
	if h.bot != nil && len(h.admins) > 0 {
		_, _ = h.bot.Send(ctx, h.admins[0], "🔐 "+acct.Key()+": "+problem+". Obnov přes /connect "+acct.Key(), tgbot.Opts{})
	}
	nudge(h.wakeEvents)
}
