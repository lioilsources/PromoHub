// Package mastodon reads (and in wave 2 publishes to) an account on a Mastodon-compatible
// server: Mastodon, GoToSocial, Akkoma, Pixelfed. The token comes from the instance's
// Settings → Development → New application (<auth_ref>_TOKEN).
//
// Full-text search is opt-in per instance and narrow, so "search" means hashtag timelines:
// hashtags derived from app names plus the account's followed hashtags, filtered to posts
// that name one of my apps.
package mastodon

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"unicode"

	"github.com/lioilsources/promohub/internal/collectors"
	"github.com/lioilsources/promohub/internal/config"
	"github.com/lioilsources/promohub/internal/creds"
	"github.com/lioilsources/promohub/internal/platform"
	"github.com/lioilsources/promohub/internal/platforms/httpx"
)

// SecretSuffix: <auth_ref>_TOKEN.
const SecretSuffix = "TOKEN"

type Adapter struct {
	HTTP *http.Client
	Base string // overrides https://<instance> in tests

	mu  sync.Mutex
	ids map[string]string // account key → my account ID
}

func New(hc *http.Client) *Adapter { return &Adapter{HTTP: hc, ids: map[string]string{}} }

func (a *Adapter) Platform() string { return "mastodon" }

func (a *Adapter) Ready(acct config.Account, capability string) error {
	if capability == platform.CapInbound {
		_, err := creds.Static(acct, SecretSuffix)
		return err
	}
	return nil
}

func (a *Adapter) client(acct config.Account) *httpx.Client {
	base := a.Base
	if base == "" {
		base = "https://" + acct.APIHost()
	}
	c := &httpx.Client{Service: "mastodon", BaseURL: base, HTTP: a.HTTP}
	if tok, err := creds.Static(acct, SecretSuffix); err == nil {
		c.Header = http.Header{"Authorization": {"Bearer " + tok}}
		c.Secrets = []string{tok}
	}
	return c
}

// Wire types.

type Account struct {
	ID             string `json:"id"`
	Acct           string `json:"acct"`
	URL            string `json:"url"`
	FollowersCount *int64 `json:"followers_count"`
	FollowingCount *int64 `json:"following_count"`
	StatusesCount  *int64 `json:"statuses_count"`
}

type Status struct {
	ID                 string  `json:"id"`
	URL                string  `json:"url"`
	URI                string  `json:"uri"`
	CreatedAt          string  `json:"created_at"`
	Content            string  `json:"content"`
	Visibility         string  `json:"visibility"`
	InReplyToAccountID *string `json:"in_reply_to_account_id"`
	Reblog             *Status `json:"reblog"`
	FavouritesCount    *int64  `json:"favourites_count"`
	ReblogsCount       *int64  `json:"reblogs_count"`
	RepliesCount       *int64  `json:"replies_count"`
	QuotesCount        *int64  `json:"quotes_count"`
	Account            Account `json:"account"`
}

type Notification struct {
	ID        string  `json:"id"`
	Type      string  `json:"type"`
	CreatedAt string  `json:"created_at"`
	Account   Account `json:"account"`
	Status    *Status `json:"status"`
}

func (a *Adapter) Collect(ctx context.Context, r collectors.Request) (collectors.Batch, json.RawMessage, error) {
	switch r.Capability {
	case platform.CapProfile:
		b, err := a.profile(ctx, r.Account)
		return b, nil, err
	case platform.CapInbound:
		return a.notifications(ctx, r.Account, r.Cursor)
	case platform.CapSearch:
		return a.hashtags(ctx, r.Account, r.Terms, r.Cursor)
	}
	return collectors.Batch{}, nil, fmt.Errorf("mastodon: unknown capability %q", r.Capability)
}

func (a *Adapter) me(ctx context.Context, acct config.Account) (Account, error) {
	var me Account
	err := a.client(acct).Get(ctx, "/api/v1/accounts/lookup", url.Values{"acct": {acct.User()}}, &me)
	if err == nil {
		a.mu.Lock()
		if a.ids == nil {
			a.ids = map[string]string{}
		}
		a.ids[acct.Key()] = me.ID
		a.mu.Unlock()
	}
	return me, err
}

func (a *Adapter) myID(ctx context.Context, acct config.Account) (string, error) {
	a.mu.Lock()
	id := a.ids[acct.Key()]
	a.mu.Unlock()
	if id != "" {
		return id, nil
	}
	me, err := a.me(ctx, acct)
	return me.ID, err
}

func (a *Adapter) profile(ctx context.Context, acct config.Account) (collectors.Batch, error) {
	me, err := a.me(ctx, acct)
	if err != nil {
		return collectors.Batch{}, err
	}
	var sts []Status
	q := url.Values{"limit": {"30"}, "exclude_replies": {"true"}, "exclude_reblogs": {"true"}}
	if err := a.client(acct).Get(ctx, "/api/v1/accounts/"+url.PathEscape(me.ID)+"/statuses", q, &sts); err != nil {
		return collectors.Batch{}, err
	}
	return MapProfile(me, sts), nil
}

// MapProfile maps my account and its recent public statuses to snapshots.
func MapProfile(me Account, sts []Status) collectors.Batch {
	var b collectors.Batch
	for _, s := range sts {
		if s.Reblog != nil || s.Account.ID != me.ID || s.ID == "" {
			b.Skipped++
			continue
		}
		extra := map[string]int64{}
		if s.QuotesCount != nil {
			extra["quotes"] = *s.QuotesCount
		}
		b.Snapshots = append(b.Snapshots, collectors.Snapshot{
			Platform: "mastodon",
			Content: &collectors.Content{
				ExternalID: s.ID, URL: s.URL, Body: Text(s.Content), PublishedAt: httpx.Time(s.CreatedAt),
			},
			Likes: s.FavouritesCount, Reposts: s.ReblogsCount, Comments: s.RepliesCount, Extra: extra,
		})
	}
	if me.FollowersCount != nil {
		extra := map[string]int64{}
		if me.StatusesCount != nil {
			extra["posts"] = *me.StatusesCount
		}
		if me.FollowingCount != nil {
			extra["follows"] = *me.FollowingCount
		}
		b.Snapshots = append(b.Snapshots, collectors.Snapshot{Platform: "mastodon", Followers: me.FollowersCount, Extra: extra})
	}
	return b
}

type idCursor struct {
	SinceID string            `json:"since_id,omitempty"`
	Tags    map[string]string `json:"tags,omitempty"` // hashtag → since_id
}

func (a *Adapter) notifications(ctx context.Context, acct config.Account, cur json.RawMessage) (collectors.Batch, json.RawMessage, error) {
	myID, err := a.myID(ctx, acct)
	if err != nil {
		return collectors.Batch{}, nil, err
	}
	var c idCursor
	_ = json.Unmarshal(cur, &c)
	q := url.Values{"types[]": {"mention"}, "limit": {"40"}}
	if c.SinceID != "" {
		q.Set("since_id", c.SinceID)
	}
	var ns []Notification
	if err := a.client(acct).Get(ctx, "/api/v1/notifications", q, &ns); err != nil {
		return collectors.Batch{}, nil, err
	}
	b, newest := MapNotifications(acct.APIHost(), myID, ns)
	if newest == "" {
		newest = c.SinceID
	}
	out, _ := json.Marshal(idCursor{SinceID: newest})
	return b, out, nil
}

// MapNotifications maps mention notifications; a mention that replies to one of my statuses
// is a reply. IDs are namespaced by instance because status IDs are local to it.
func MapNotifications(host, myID string, ns []Notification) (collectors.Batch, string) {
	var b collectors.Batch
	newest := ""
	for _, n := range ns {
		if newerID(n.ID, newest) {
			newest = n.ID
		}
		if n.Type != "mention" || n.Status == nil || n.Account.ID == myID {
			b.Skipped++
			continue
		}
		kind := "mention"
		if n.Status.InReplyToAccountID != nil && *n.Status.InReplyToAccountID == myID {
			kind = "reply"
		}
		b.Inbound = append(b.Inbound, inbound(host, kind, *n.Status))
	}
	return b, newest
}

func inbound(host, kind string, s Status) collectors.Inbound {
	u := s.URL
	if u == "" {
		u = s.URI
	}
	return collectors.Inbound{
		Platform: "mastodon", Kind: kind, ExternalID: host + "/" + s.ID, Author: s.Account.Acct,
		Text: Text(s.Content), URL: u, CreatedAt: httpx.Time(s.CreatedAt),
	}
}

func (a *Adapter) hashtags(ctx context.Context, acct config.Account, terms []string, cur json.RawMessage) (collectors.Batch, json.RawMessage, error) {
	var c idCursor
	_ = json.Unmarshal(cur, &c)
	if c.Tags == nil {
		c.Tags = map[string]string{}
	}
	var b collectors.Batch
	for _, tag := range Tags(terms, acct.Hashtags) {
		q := url.Values{"limit": {"40"}}
		if s := c.Tags[tag]; s != "" {
			q.Set("since_id", s)
		}
		var sts []Status
		if err := a.client(acct).Get(ctx, "/api/v1/timelines/tag/"+url.PathEscape(tag), q, &sts); err != nil {
			return b, nil, fmt.Errorf("tag %s: %w", tag, err)
		}
		tb, newest := MapTagTimeline(acct.APIHost(), acct.User(), terms, sts)
		b.Inbound = append(b.Inbound, tb.Inbound...)
		b.Skipped += tb.Skipped
		if newest != "" {
			c.Tags[tag] = newest
		}
	}
	out, _ := json.Marshal(c)
	return b, out, nil
}

// MapTagTimeline keeps statuses that name one of my apps, minus my own.
func MapTagTimeline(host, myUser string, terms []string, sts []Status) (collectors.Batch, string) {
	var b collectors.Batch
	newest := ""
	for _, s := range sts {
		if newerID(s.ID, newest) {
			newest = s.ID
		}
		text := Text(s.Content)
		mine := strings.EqualFold(s.Account.Acct, myUser) || strings.EqualFold(s.Account.Acct, myUser+"@"+host)
		if mine || s.Reblog != nil || !mentionsAny(text, terms) {
			b.Skipped++
			continue
		}
		b.Inbound = append(b.Inbound, inbound(host, "mention", s))
	}
	return b, newest
}

// Tags returns the hashtags to follow: app terms turned into tags plus the configured ones.
func Tags(terms, extra []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, t := range append(append([]string(nil), terms...), extra...) {
		tag := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(t), "#"))
		tag = strings.Map(func(r rune) rune {
			if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' {
				return r
			}
			return -1
		}, tag)
		if tag != "" && !seen[tag] {
			seen[tag] = true
			out = append(out, tag)
		}
	}
	return out
}

func mentionsAny(text string, terms []string) bool {
	t := strings.ToLower(text)
	for _, term := range terms {
		if term = strings.ToLower(strings.TrimSpace(term)); term != "" && strings.Contains(t, term) {
			return true
		}
		// "#SwypeKids" in text for the term "Swype Kids".
		if tag := strings.ReplaceAll(term, " ", ""); tag != "" && strings.Contains(t, tag) {
			return true
		}
	}
	return false
}

// newerID compares snowflake-style numeric IDs as strings (longer = newer).
func newerID(a, b string) bool {
	if len(a) != len(b) {
		return len(a) > len(b)
	}
	return a > b
}

var (
	reBreak = regexp.MustCompile(`(?i)<br\s*/?>|</p>\s*<p[^>]*>`)
	reTag   = regexp.MustCompile(`<[^>]+>`)
)

// Text turns status HTML into plain text.
func Text(h string) string {
	h = reBreak.ReplaceAllString(h, "\n")
	h = reTag.ReplaceAllString(h, "")
	return strings.TrimSpace(html.UnescapeString(h))
}
