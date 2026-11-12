// Package oauth runs the authorization-code flows of the platforms whose tokens the hub keeps
// itself (Instagram, Threads, Facebook Page; YouTube and TikTok in wave 4), stores the tokens
// encrypted and refreshes them.
package oauth

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/lioilsources/promohub/internal/config"
	"github.com/lioilsources/promohub/internal/creds"
	"github.com/lioilsources/promohub/internal/platforms/httpx"
	"github.com/lioilsources/promohub/internal/store"
	"github.com/lioilsources/promohub/internal/store/db"
	"github.com/lioilsources/promohub/internal/vault"
)

// StateTTL bounds how long a /connect link works.
const StateTTL = 10 * time.Minute

// Store keeps tokens in the credentials table, encrypted per account, and refreshes them
// when they are close to expiry.
type Store struct {
	Q       *db.Queries
	Vault   *vault.Vault
	Clients map[string]Client // provider name → app credentials
	Now     func() time.Time

	mu sync.Mutex // one refresh at a time: TikTok rotates refresh tokens
}

// Token implements creds.TokenSource.
func (s *Store) Token(ctx context.Context, acct config.Account) (creds.Token, error) {
	if s == nil || s.Q == nil || s.Vault == nil {
		return creds.Token{}, creds.NotConnected(acct)
	}
	a, err := s.Q.AccountByKey(ctx, db.AccountByKeyParams{Platform: acct.Platform, Handle: acct.Handle})
	if err != nil {
		return creds.Token{}, creds.NotConnected(acct)
	}
	t, err := s.Get(ctx, a.ID, acct)
	if err != nil {
		return t, err
	}
	t, _, err = s.RefreshIfDue(ctx, a.ID, acct, t)
	return t, err
}

// RefreshIfDue renews a token within its provider's RefreshBefore window. refreshed reports
// whether it did; on error the old token is returned with the error.
func (s *Store) RefreshIfDue(ctx context.Context, accountID int64, acct config.Account, t creds.Token) (creds.Token, bool, error) {
	p, err := Lookup(acct.Platform)
	if err != nil || p.Refresh == nil || t.ExpiresAt == nil {
		return t, false, nil
	}
	now := time.Now
	if s.Now != nil {
		now = s.Now
	}
	if t.ExpiresAt.Sub(now()) > p.RefreshBefore {
		return t, false, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if fresh, err := s.Get(ctx, accountID, acct); err == nil && fresh.ExpiresAt != nil && fresh.ExpiresAt.Sub(now()) > p.RefreshBefore {
		return fresh, false, nil // another goroutine refreshed it meanwhile
	}
	c, ok := s.Clients[p.Name]
	if !ok {
		return t, false, fmt.Errorf("oauth: %s app credentials are not set", p.Name)
	}
	nt, err := p.Refresh(ctx, c, t)
	if err != nil {
		msg := err.Error()
		_ = s.Q.CredentialError(ctx, db.CredentialErrorParams{AccountID: accountID, LastError: &msg})
		if t.ExpiresAt.After(now()) {
			return t, false, nil // still valid; the daily check alerts
		}
		return t, false, err
	}
	return nt, true, s.Put(ctx, accountID, p.Name, nt)
}

// Get decrypts the token of an account ID.
func (s *Store) Get(ctx context.Context, accountID int64, acct config.Account) (creds.Token, error) {
	row, err := s.Q.GetCredential(ctx, accountID)
	if store.NoRows(err) {
		return creds.Token{}, creds.NotConnected(acct)
	} else if err != nil {
		return creds.Token{}, err
	}
	pt, err := s.Vault.Open(row.Ciphertext, row.Nonce, accountID)
	if err != nil {
		return creds.Token{}, err
	}
	var t creds.Token
	if err := json.Unmarshal(pt, &t); err != nil {
		return creds.Token{}, err
	}
	return t, nil
}

// Put encrypts and stores a token.
func (s *Store) Put(ctx context.Context, accountID int64, provider string, t creds.Token) error {
	b, _ := json.Marshal(t)
	ct, nonce := s.Vault.Seal(b, accountID)
	return s.Q.PutCredential(ctx, db.PutCredentialParams{
		AccountID: accountID, Provider: provider, Ciphertext: ct, Nonce: nonce, Scopes: nz(t.Scopes),
		ExpiresAt: t.ExpiresAt, RefreshExpiresAt: t.RefreshExpiresAt,
	})
}

// State is a signed, expiring token naming the account a /connect link is for.
func State(v *vault.Vault, accountKey string, now time.Time) string {
	n := make([]byte, 9)
	_, _ = rand.Read(n)
	msg := base64.RawURLEncoding.EncodeToString([]byte(accountKey)) + "." +
		strconv.FormatInt(now.Add(StateTTL).Unix(), 10) + "." + base64.RawURLEncoding.EncodeToString(n)
	return msg + "." + v.Sign(msg)
}

// ParseState verifies a state and returns its account key.
func ParseState(v *vault.Vault, s string, now time.Time) (string, error) {
	i := strings.LastIndexByte(s, '.')
	if i < 0 || !v.Verify(s[:i], s[i+1:]) {
		return "", errors.New("oauth: bad state")
	}
	parts := strings.Split(s[:i], ".")
	if len(parts) != 3 {
		return "", errors.New("oauth: bad state")
	}
	exp, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil || now.Unix() > exp {
		return "", errors.New("oauth: link expired, run /connect again")
	}
	key, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return "", errors.New("oauth: bad state")
	}
	return string(key), nil
}

// Provider describes one platform's authorization-code flow.
type Provider struct {
	Name        string
	AuthURL     string
	Scopes      []string
	ScopeSep    string
	ClientParam string // "client_id" (default) or "client_key" (TikTok)
	ClientEnv   string
	SecretEnv   string
	AuthExtra   url.Values
	// Exchange turns the code into a long-lived token for acct.
	Exchange func(ctx context.Context, c Client, code string, acct config.Account) (creds.Token, error)
	// Refresh renews a token; nil when the platform's tokens do not expire.
	Refresh func(ctx context.Context, c Client, t creds.Token) (creds.Token, error)
	// RefreshBefore is how long before expiry Refresh runs.
	RefreshBefore time.Duration
}

// Client carries what Exchange and Refresh need.
type Client struct {
	ID, Secret, Redirect string
	HTTP                 *http.Client
	Now                  func() time.Time
	// Hosts overrides API hosts in tests (name → base URL).
	Hosts map[string]string
}

// Host returns the base URL for a named API host, honouring test overrides.
func (c Client) Host(name, def string) string {
	if h, ok := c.Hosts[name]; ok {
		return h
	}
	return def
}

func (c Client) API(service, base string) *httpx.Client {
	return &httpx.Client{Service: service, BaseURL: base, HTTP: c.HTTP, Secrets: []string{c.Secret}}
}

// ExpiresIn turns an expires_in answer into an absolute time.
func (c Client) ExpiresIn(sec int64) *time.Time {
	if sec <= 0 {
		return nil
	}
	now := time.Now
	if c.Now != nil {
		now = c.Now
	}
	t := now().Add(time.Duration(sec) * time.Second).UTC()
	return &t
}

// AuthorizeURL is where /oauth/{platform}/start redirects.
func (p *Provider) AuthorizeURL(clientID, redirect, state string) string {
	q := url.Values{}
	for k, v := range p.AuthExtra {
		q[k] = v
	}
	param := p.ClientParam
	if param == "" {
		param = "client_id"
	}
	sep := p.ScopeSep
	if sep == "" {
		sep = ","
	}
	q.Set(param, clientID)
	q.Set("redirect_uri", redirect)
	q.Set("response_type", "code")
	q.Set("scope", strings.Join(p.Scopes, sep))
	q.Set("state", state)
	return p.AuthURL + "?" + q.Encode()
}

// FormPost posts form values and decodes JSON (token endpoints take forms, not JSON).
func FormPost(ctx context.Context, c *httpx.Client, path string, form url.Values, out any) error {
	cc := *c
	cc.Header = http.Header{"Content-Type": {"application/x-www-form-urlencoded"}}
	return cc.Do(ctx, http.MethodPost, path, nil, strings.NewReader(form.Encode()), out)
}

func nz(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// ErrNoProvider is returned for platforms without an OAuth flow.
var ErrNoProvider = errors.New("oauth: platform has no OAuth flow")

// Registry maps platform name → provider. Providers register in providers.go.
var Registry = map[string]*Provider{}

func Lookup(platform string) (*Provider, error) {
	p, ok := Registry[platform]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrNoProvider, platform)
	}
	return p, nil
}
