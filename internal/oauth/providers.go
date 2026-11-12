package oauth

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/lioilsources/promohub/internal/config"
	"github.com/lioilsources/promohub/internal/creds"
)

// GraphVersion is the Facebook Graph API version (supported for about two years).
const GraphVersion = "v24.0"

func init() {
	for _, p := range []*Provider{instagram, threads, facebook, youtube, tiktok} {
		Registry[p.Name] = p
	}
}

type tokenAnswer struct {
	AccessToken      string `json:"access_token"`
	RefreshToken     string `json:"refresh_token"`
	ExpiresIn        int64  `json:"expires_in"`
	RefreshExpiresIn int64  `json:"refresh_expires_in"`
	UserID           any    `json:"user_id"` // number on Threads, string on Instagram
	OpenID           string `json:"open_id"`
	Scope            string `json:"scope"`
	Permissions      string `json:"permissions"`
	Data             []struct {
		AccessToken string `json:"access_token"`
		UserID      any    `json:"user_id"`
		Permissions string `json:"permissions"`
	} `json:"data"` // Instagram Login wraps the short-lived token in data[]
	Error            string `json:"error"`
	ErrorDescription string `json:"error_description"`
}

func (a tokenAnswer) user() string {
	id := a.UserID
	if id == nil && len(a.Data) > 0 {
		id = a.Data[0].UserID
	}
	switch v := id.(type) {
	case string:
		return v
	case float64:
		return fmt.Sprintf("%.0f", v)
	}
	return a.OpenID
}

func (a tokenAnswer) token() string {
	if a.AccessToken == "" && len(a.Data) > 0 {
		return a.Data[0].AccessToken
	}
	return a.AccessToken
}

func (a tokenAnswer) check() error {
	if a.token() == "" {
		if a.Error != "" {
			return fmt.Errorf("oauth: %s: %s", a.Error, a.ErrorDescription)
		}
		return errors.New("oauth: no access_token in answer")
	}
	return nil
}

// Instagram API with Instagram Login: no Facebook Page needed. Short-lived token → 60-day token,
// refreshable once it is a day old.
var instagram = &Provider{
	Name: "instagram", AuthURL: "https://www.instagram.com/oauth/authorize",
	Scopes:    []string{"instagram_business_basic", "instagram_business_content_publish", "instagram_business_manage_comments", "instagram_business_manage_insights"},
	ClientEnv: "INSTAGRAM_APP_ID", SecretEnv: "INSTAGRAM_APP_SECRET", RefreshBefore: 20 * 24 * time.Hour,
	Exchange: func(ctx context.Context, c Client, code string, _ config.Account) (creds.Token, error) {
		code = strings.TrimSuffix(code, "#_")
		var short tokenAnswer
		err := FormPost(ctx, c.API("instagram", c.Host("instagram-auth", "https://api.instagram.com")), "/oauth/access_token", url.Values{
			"client_id": {c.ID}, "client_secret": {c.Secret}, "grant_type": {"authorization_code"}, "redirect_uri": {c.Redirect}, "code": {code},
		}, &short)
		if err == nil {
			err = short.check()
		}
		if err != nil {
			return creds.Token{}, err
		}
		var long tokenAnswer
		err = c.API("instagram", c.Host("instagram", "https://graph.instagram.com")).Get(ctx, "/access_token", url.Values{
			"grant_type": {"ig_exchange_token"}, "client_secret": {c.Secret}, "access_token": {short.token()},
		}, &long)
		if err == nil {
			err = long.check()
		}
		if err != nil {
			return creds.Token{}, err
		}
		perms := short.Permissions
		if perms == "" && len(short.Data) > 0 {
			perms = short.Data[0].Permissions
		}
		return creds.Token{AccessToken: long.token(), UserID: short.user(), Scopes: perms, ExpiresAt: c.ExpiresIn(long.ExpiresIn)}, nil
	},
	Refresh: func(ctx context.Context, c Client, t creds.Token) (creds.Token, error) {
		var a tokenAnswer
		err := c.API("instagram", c.Host("instagram", "https://graph.instagram.com")).Get(ctx, "/refresh_access_token", url.Values{
			"grant_type": {"ig_refresh_token"}, "access_token": {t.AccessToken},
		}, &a)
		if err == nil {
			err = a.check()
		}
		if err != nil {
			return t, err
		}
		t.AccessToken, t.ExpiresAt = a.token(), c.ExpiresIn(a.ExpiresIn)
		return t, nil
	},
}

// Threads API: same shape as Instagram on graph.threads.net.
var threads = &Provider{
	Name: "threads", AuthURL: "https://threads.net/oauth/authorize",
	Scopes:    []string{"threads_basic", "threads_content_publish", "threads_manage_insights", "threads_manage_replies", "threads_read_replies", "threads_manage_mentions"},
	ClientEnv: "THREADS_APP_ID", SecretEnv: "THREADS_APP_SECRET", RefreshBefore: 20 * 24 * time.Hour,
	Exchange: func(ctx context.Context, c Client, code string, _ config.Account) (creds.Token, error) {
		api := c.API("threads", c.Host("threads", "https://graph.threads.net"))
		var short tokenAnswer
		err := FormPost(ctx, api, "/oauth/access_token", url.Values{
			"client_id": {c.ID}, "client_secret": {c.Secret}, "grant_type": {"authorization_code"}, "redirect_uri": {c.Redirect}, "code": {code},
		}, &short)
		if err == nil {
			err = short.check()
		}
		if err != nil {
			return creds.Token{}, err
		}
		var long tokenAnswer
		err = api.Get(ctx, "/access_token", url.Values{"grant_type": {"th_exchange_token"}, "client_secret": {c.Secret}, "access_token": {short.token()}}, &long)
		if err == nil {
			err = long.check()
		}
		if err != nil {
			return creds.Token{}, err
		}
		return creds.Token{AccessToken: long.token(), UserID: short.user(), ExpiresAt: c.ExpiresIn(long.ExpiresIn)}, nil
	},
	Refresh: func(ctx context.Context, c Client, t creds.Token) (creds.Token, error) {
		var a tokenAnswer
		err := c.API("threads", c.Host("threads", "https://graph.threads.net")).Get(ctx, "/refresh_access_token", url.Values{
			"grant_type": {"th_refresh_token"}, "access_token": {t.AccessToken},
		}, &a)
		if err == nil {
			err = a.check()
		}
		if err != nil {
			return t, err
		}
		t.AccessToken, t.ExpiresAt = a.token(), c.ExpiresIn(a.ExpiresIn)
		return t, nil
	},
}

// Facebook Login → long-lived user token → the page token of the account's page, which does
// not expire.
var facebook = &Provider{
	Name: "facebook", AuthURL: "https://www.facebook.com/" + GraphVersion + "/dialog/oauth",
	Scopes:    []string{"pages_show_list", "pages_read_engagement", "pages_read_user_content", "read_insights"},
	ClientEnv: "FACEBOOK_APP_ID", SecretEnv: "FACEBOOK_APP_SECRET",
	Exchange: func(ctx context.Context, c Client, code string, acct config.Account) (creds.Token, error) {
		api := c.API("facebook", c.Host("facebook", "https://graph.facebook.com"))
		var short, long tokenAnswer
		err := api.Get(ctx, "/"+GraphVersion+"/oauth/access_token", url.Values{
			"client_id": {c.ID}, "client_secret": {c.Secret}, "redirect_uri": {c.Redirect}, "code": {code},
		}, &short)
		if err == nil {
			err = short.check()
		}
		if err == nil {
			err = api.Get(ctx, "/"+GraphVersion+"/oauth/access_token", url.Values{
				"grant_type": {"fb_exchange_token"}, "client_id": {c.ID}, "client_secret": {c.Secret}, "fb_exchange_token": {short.token()},
			}, &long)
		}
		if err == nil {
			err = long.check()
		}
		if err != nil {
			return creds.Token{}, err
		}
		var pages struct {
			Data []struct {
				ID          string `json:"id"`
				AccessToken string `json:"access_token"`
			} `json:"data"`
		}
		if err := api.Get(ctx, "/"+GraphVersion+"/me/accounts", url.Values{"fields": {"id,name,access_token"}, "access_token": {long.token()}}, &pages); err != nil {
			return creds.Token{}, err
		}
		for _, p := range pages.Data {
			if p.ID == acct.ExternalID || (acct.ExternalID == "" && len(pages.Data) == 1) {
				return creds.Token{AccessToken: p.AccessToken, PageID: p.ID}, nil
			}
		}
		return creds.Token{}, fmt.Errorf("oauth: page %q not among the %d pages this login manages (set external_id to the Page ID)", acct.ExternalID, len(pages.Data))
	},
}

// Google OAuth for YouTube Analytics (and uploads after the API audit). The consent screen must
// be "In production": in "Testing" refresh tokens die after 7 days.
var youtube = &Provider{
	Name: "youtube", AuthURL: "https://accounts.google.com/o/oauth2/v2/auth", ScopeSep: " ",
	Scopes:    []string{"https://www.googleapis.com/auth/youtube.readonly", "https://www.googleapis.com/auth/yt-analytics.readonly", "https://www.googleapis.com/auth/youtube.force-ssl"},
	ClientEnv: "GOOGLE_CLIENT_ID", SecretEnv: "GOOGLE_CLIENT_SECRET", RefreshBefore: 5 * time.Minute,
	AuthExtra: url.Values{"access_type": {"offline"}, "prompt": {"consent"}, "include_granted_scopes": {"true"}},
	Exchange: func(ctx context.Context, c Client, code string, _ config.Account) (creds.Token, error) {
		var a tokenAnswer
		err := FormPost(ctx, c.API("google", c.Host("google", "https://oauth2.googleapis.com")), "/token", url.Values{
			"client_id": {c.ID}, "client_secret": {c.Secret}, "grant_type": {"authorization_code"}, "redirect_uri": {c.Redirect}, "code": {code},
		}, &a)
		if err == nil {
			err = a.check()
		}
		if err != nil {
			return creds.Token{}, err
		}
		if a.RefreshToken == "" {
			return creds.Token{}, errors.New("oauth: Google sent no refresh token (revoke promohub at myaccount.google.com/permissions and connect again)")
		}
		return creds.Token{AccessToken: a.token(), RefreshToken: a.RefreshToken, Scopes: a.Scope, ExpiresAt: c.ExpiresIn(a.ExpiresIn)}, nil
	},
	Refresh: func(ctx context.Context, c Client, t creds.Token) (creds.Token, error) {
		var a tokenAnswer
		err := FormPost(ctx, c.API("google", c.Host("google", "https://oauth2.googleapis.com")), "/token", url.Values{
			"client_id": {c.ID}, "client_secret": {c.Secret}, "grant_type": {"refresh_token"}, "refresh_token": {t.RefreshToken},
		}, &a)
		if err == nil {
			err = a.check()
		}
		if err != nil {
			return t, err
		}
		t.AccessToken, t.ExpiresAt = a.token(), c.ExpiresIn(a.ExpiresIn)
		return t, nil
	},
}

// TikTok Login Kit: access tokens live 24 h, refresh tokens 365 days.
var tiktok = &Provider{
	Name: "tiktok", AuthURL: "https://www.tiktok.com/v2/auth/authorize/", ClientParam: "client_key",
	Scopes:    []string{"user.info.basic", "user.info.stats", "video.list", "video.upload"},
	ClientEnv: "TIKTOK_CLIENT_KEY", SecretEnv: "TIKTOK_CLIENT_SECRET", RefreshBefore: time.Hour,
	Exchange: func(ctx context.Context, c Client, code string, _ config.Account) (creds.Token, error) {
		return tiktokToken(ctx, c, url.Values{"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {c.Redirect}}, creds.Token{})
	},
	Refresh: func(ctx context.Context, c Client, t creds.Token) (creds.Token, error) {
		return tiktokToken(ctx, c, url.Values{"grant_type": {"refresh_token"}, "refresh_token": {t.RefreshToken}}, t)
	},
}

func tiktokToken(ctx context.Context, c Client, form url.Values, old creds.Token) (creds.Token, error) {
	form.Set("client_key", c.ID)
	form.Set("client_secret", c.Secret)
	var a tokenAnswer
	err := FormPost(ctx, c.API("tiktok", c.Host("tiktok", "https://open.tiktokapis.com")), "/v2/oauth/token/", form, &a)
	if err == nil {
		err = a.check()
	}
	if err != nil {
		return old, err
	}
	t := creds.Token{AccessToken: a.token(), RefreshToken: a.RefreshToken, UserID: a.user(), Scopes: a.Scope,
		ExpiresAt: c.ExpiresIn(a.ExpiresIn), RefreshExpiresAt: c.ExpiresIn(a.RefreshExpiresIn)}
	if t.RefreshToken == "" {
		t.RefreshToken, t.RefreshExpiresAt = old.RefreshToken, old.RefreshExpiresAt
	}
	return t, nil
}
