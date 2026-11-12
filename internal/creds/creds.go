// Package creds resolves the secrets an adapter needs. Static secrets live in the environment
// under the account's auth_ref prefix (BLUESKY_MAIN + "_APP_PASSWORD"); they never appear in YAML.
package creds

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/lioilsources/promohub/internal/config"
)

// OAuth is the auth_ref value of accounts whose tokens live encrypted in the credentials table.
const OAuth = "oauth"

// Prefix is the env prefix of an account's static secrets: its auth_ref, or the upper-cased
// platform name when auth_ref is empty.
func Prefix(a config.Account) string {
	if a.AuthRef != "" && a.AuthRef != OAuth {
		return a.AuthRef
	}
	return strings.ToUpper(a.Platform)
}

// Name is the env variable holding one static secret of an account.
func Name(a config.Account, suffix string) string { return Prefix(a) + "_" + suffix }

// Getenv is replaceable in tests.
var Getenv = os.Getenv

// Static returns a static secret, or a MissingError naming the variable to set.
func Static(a config.Account, suffix string) (string, error) {
	n := Name(a, suffix)
	if v := strings.TrimSpace(Getenv(n)); v != "" {
		return v, nil
	}
	return "", &MissingError{Var: n}
}

// MissingError means the account is configured but its secret is not set yet. The scheduler
// treats it as "not connected" rather than as a failing collector.
type MissingError struct{ Var string }

func (e *MissingError) Error() string { return fmt.Sprintf("%s is not set", e.Var) }

// Token is an OAuth grant the hub keeps encrypted in the credentials table.
type Token struct {
	AccessToken      string     `json:"access_token"`
	RefreshToken     string     `json:"refresh_token,omitempty"`
	UserID           string     `json:"user_id,omitempty"` // platform user ID (Instagram, Threads, TikTok open_id)
	PageID           string     `json:"page_id,omitempty"` // Facebook: AccessToken is this page's token
	Scopes           string     `json:"scopes,omitempty"`
	ExpiresAt        *time.Time `json:"expires_at,omitempty"`
	RefreshExpiresAt *time.Time `json:"refresh_expires_at,omitempty"`
}

// TokenSource hands adapters the current OAuth token of an account.
type TokenSource interface {
	Token(ctx context.Context, acct config.Account) (Token, error)
}

// NotConnected is the MissingError of an OAuth account without a token yet.
func NotConnected(acct config.Account) error {
	return &MissingError{Var: "OAuth token (/connect " + acct.Key() + ")"}
}

// OAuthToken returns the account's token or a MissingError when there is none.
func OAuthToken(ctx context.Context, ts TokenSource, acct config.Account) (Token, error) {
	if ts == nil {
		return Token{}, NotConnected(acct)
	}
	return ts.Token(ctx, acct)
}
