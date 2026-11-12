// Package bluesky reads (and in wave 2 publishes to) a Bluesky account over XRPC.
//
// Public reads (profile, author feed) go to the public AppView without auth. Notifications and
// search need a session: com.atproto.server.createSession with an app password on the PDS,
// which proxies app.bsky.* calls to the AppView. searchPosts on public.api.bsky.app answers
// 403, so search always goes through the PDS.
package bluesky

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/lioilsources/promohub/internal/collectors"
	"github.com/lioilsources/promohub/internal/config"
	"github.com/lioilsources/promohub/internal/creds"
	"github.com/lioilsources/promohub/internal/platform"
	"github.com/lioilsources/promohub/internal/platforms/httpx"
)

const (
	PublicAppView = "https://public.api.bsky.app"
	// SecretSuffix: <auth_ref>_APP_PASSWORD.
	SecretSuffix = "APP_PASSWORD"
	feedLimit    = 30
)

type Adapter struct {
	HTTP *http.Client
	// AppView and PDS override the hosts in tests ("" = public AppView / account PDS).
	AppView string
	PDS     string
	Now     func() time.Time

	mu       sync.Mutex
	sessions map[string]*session // account key → session
}

type session struct {
	AccessJwt string `json:"accessJwt"`
	Did       string `json:"did"`
	Handle    string `json:"handle"`
	PDS       string `json:"-"`
	at        time.Time
}

func New(hc *http.Client) *Adapter { return &Adapter{HTTP: hc, sessions: map[string]*session{}} }

func (a *Adapter) Platform() string { return "bluesky" }

func (a *Adapter) Ready(acct config.Account, capability string) error {
	if capability == platform.CapProfile {
		return nil
	}
	_, err := creds.Static(acct, SecretSuffix)
	return err
}

func (a *Adapter) Collect(ctx context.Context, r collectors.Request) (collectors.Batch, json.RawMessage, error) {
	switch r.Capability {
	case platform.CapProfile:
		b, err := a.profile(ctx, r.Account)
		return b, nil, err
	case platform.CapInbound:
		return a.notifications(ctx, r.Account, r.Cursor)
	case platform.CapSearch:
		return a.search(ctx, r.Account, r.Terms, r.Cursor)
	}
	return collectors.Batch{}, nil, fmt.Errorf("bluesky: unknown capability %q", r.Capability)
}

func (a *Adapter) public() *httpx.Client {
	base := a.AppView
	if base == "" {
		base = PublicAppView
	}
	return &httpx.Client{Service: "bluesky", BaseURL: base, HTTP: a.HTTP}
}

// Wire types (only the fields the hub reads).

type Profile struct {
	Did            string `json:"did"`
	Handle         string `json:"handle"`
	FollowersCount *int64 `json:"followersCount"`
	FollowsCount   *int64 `json:"followsCount"`
	PostsCount     *int64 `json:"postsCount"`
}

type Author struct {
	Did    string `json:"did"`
	Handle string `json:"handle"`
}

type PostRecord struct {
	Text      string `json:"text"`
	CreatedAt string `json:"createdAt"`
}

type PostView struct {
	URI           string     `json:"uri"`
	Author        Author     `json:"author"`
	Record        PostRecord `json:"record"`
	LikeCount     *int64     `json:"likeCount"`
	RepostCount   *int64     `json:"repostCount"`
	ReplyCount    *int64     `json:"replyCount"`
	QuoteCount    *int64     `json:"quoteCount"`
	BookmarkCount *int64     `json:"bookmarkCount"`
	IndexedAt     string     `json:"indexedAt"`
}

type FeedItem struct {
	Post   PostView        `json:"post"`
	Reason json.RawMessage `json:"reason"` // set for reposts
}

type Notification struct {
	URI       string     `json:"uri"`
	Author    Author     `json:"author"`
	Reason    string     `json:"reason"` // mention | reply | quote | like | repost | follow | …
	Record    PostRecord `json:"record"`
	IndexedAt string     `json:"indexedAt"`
}

func (a *Adapter) profile(ctx context.Context, acct config.Account) (collectors.Batch, error) {
	c := a.public()
	actor := acct.Handle
	if acct.ExternalID != "" {
		actor = acct.ExternalID
	}
	var p Profile
	if err := c.Get(ctx, "/xrpc/app.bsky.actor.getProfile", url.Values{"actor": {actor}}, &p); err != nil {
		return collectors.Batch{}, err
	}
	var feed struct {
		Feed []FeedItem `json:"feed"`
	}
	q := url.Values{"actor": {p.Did}, "limit": {fmt.Sprint(feedLimit)}, "filter": {"posts_no_replies"}}
	if err := c.Get(ctx, "/xrpc/app.bsky.feed.getAuthorFeed", q, &feed); err != nil {
		return collectors.Batch{}, err
	}
	return MapProfile(p, feed.Feed), nil
}

// MapProfile turns a profile and its author feed into snapshots. Reposts and other people's
// posts in the feed are skipped.
func MapProfile(p Profile, feed []FeedItem) collectors.Batch {
	var b collectors.Batch
	for _, it := range feed {
		pv := it.Post
		if len(it.Reason) > 0 && string(it.Reason) != "null" || pv.Author.Did != p.Did {
			b.Skipped++
			continue
		}
		rkey := RKey(pv.URI)
		if rkey == "" {
			b.Skipped++
			continue
		}
		extra := map[string]int64{}
		if pv.QuoteCount != nil {
			extra["quotes"] = *pv.QuoteCount
		}
		if pv.BookmarkCount != nil {
			extra["bookmarks"] = *pv.BookmarkCount
		}
		b.Snapshots = append(b.Snapshots, collectors.Snapshot{
			Platform: "bluesky",
			Content: &collectors.Content{
				ExternalID: rkey, URL: PostURL(p.Handle, rkey), Body: pv.Record.Text,
				PublishedAt: httpx.Time(pv.Record.CreatedAt),
			},
			Likes: pv.LikeCount, Reposts: pv.RepostCount, Comments: pv.ReplyCount, Extra: extra,
		})
	}
	if p.FollowersCount != nil {
		extra := map[string]int64{}
		if p.PostsCount != nil {
			extra["posts"] = *p.PostsCount
		}
		if p.FollowsCount != nil {
			extra["follows"] = *p.FollowsCount
		}
		b.Snapshots = append(b.Snapshots, collectors.Snapshot{Platform: "bluesky", Followers: p.FollowersCount, Extra: extra})
	}
	return b
}

// RKey is the record key, the last segment of an at:// URI.
func RKey(uri string) string {
	if !strings.HasPrefix(uri, "at://") {
		return ""
	}
	i := strings.LastIndexByte(uri, '/')
	return uri[i+1:]
}

// PostURL is the bsky.app link of a post. uriOrHandle may be a handle or a DID.
func PostURL(actor, rkey string) string {
	return "https://bsky.app/profile/" + actor + "/post/" + rkey
}

// WebURL turns an at:// post URI into its bsky.app link.
func WebURL(uri, handle string) string {
	rest := strings.TrimPrefix(uri, "at://")
	did, _, _ := strings.Cut(rest, "/")
	if handle == "" {
		handle = did
	}
	return PostURL(handle, RKey(uri))
}

type timeCursor struct {
	Since string `json:"since"` // newest indexedAt / createdAt already seen
}

func (a *Adapter) notifications(ctx context.Context, acct config.Account, cur json.RawMessage) (collectors.Batch, json.RawMessage, error) {
	c, s, err := a.authed(ctx, acct)
	if err != nil {
		return collectors.Batch{}, nil, err
	}
	var res struct {
		Notifications []Notification `json:"notifications"`
	}
	q := url.Values{"limit": {"50"}, "reasons": {"mention", "reply", "quote"}}
	if err := c.Get(ctx, "/xrpc/app.bsky.notification.listNotifications", q, &res); err != nil {
		return collectors.Batch{}, nil, err
	}
	var tc timeCursor
	_ = json.Unmarshal(cur, &tc)
	b, newest := MapNotifications(s.Did, res.Notifications, tc.Since)
	return b, mustJSON(timeCursor{Since: newest}), nil
}

// MapNotifications keeps mentions, replies and quotes newer than since and returns the newest
// indexedAt seen. Dedup is by at:// URI, the same key search results use.
func MapNotifications(myDid string, ns []Notification, since string) (collectors.Batch, string) {
	var b collectors.Batch
	newest := since
	for _, n := range ns {
		if n.IndexedAt > newest {
			newest = n.IndexedAt
		}
		kind := map[string]string{"mention": "mention", "quote": "mention", "reply": "reply"}[n.Reason]
		if kind == "" || n.Author.Did == myDid || (since != "" && n.IndexedAt <= since) {
			b.Skipped++
			continue
		}
		b.Inbound = append(b.Inbound, collectors.Inbound{
			Platform: "bluesky", Kind: kind, ExternalID: n.URI, Author: n.Author.Handle,
			Text: n.Record.Text, URL: WebURL(n.URI, n.Author.Handle), CreatedAt: httpx.Time(n.Record.CreatedAt),
		})
	}
	return b, newest
}

func (a *Adapter) search(ctx context.Context, acct config.Account, terms []string, cur json.RawMessage) (collectors.Batch, json.RawMessage, error) {
	if len(terms) == 0 {
		return collectors.Batch{}, nil, nil
	}
	c, s, err := a.authed(ctx, acct)
	if err != nil {
		return collectors.Batch{}, nil, err
	}
	var tc timeCursor
	_ = json.Unmarshal(cur, &tc)
	var b collectors.Batch
	newest := tc.Since
	for _, term := range terms {
		q := url.Values{"q": {term}, "sort": {"latest"}, "limit": {"25"}}
		if tc.Since != "" {
			q.Set("since", tc.Since)
		}
		var res struct {
			Posts []PostView `json:"posts"`
		}
		if err := c.Get(ctx, "/xrpc/app.bsky.feed.searchPosts", q, &res); err != nil {
			return b, nil, fmt.Errorf("search %q: %w", term, err)
		}
		sb, n := MapSearch(s.Did, res.Posts)
		b.Inbound = append(b.Inbound, sb.Inbound...)
		b.Skipped += sb.Skipped
		if n > newest {
			newest = n
		}
	}
	return b, mustJSON(timeCursor{Since: newest}), nil
}

// MapSearch maps searchPosts hits to mentions, dropping my own posts.
func MapSearch(myDid string, posts []PostView) (collectors.Batch, string) {
	var b collectors.Batch
	newest := ""
	for _, p := range posts {
		if p.Record.CreatedAt > newest {
			newest = p.Record.CreatedAt
		}
		if p.Author.Did == myDid || p.URI == "" {
			b.Skipped++
			continue
		}
		b.Inbound = append(b.Inbound, collectors.Inbound{
			Platform: "bluesky", Kind: "mention", ExternalID: p.URI, Author: p.Author.Handle,
			Text: p.Record.Text, URL: WebURL(p.URI, p.Author.Handle), CreatedAt: httpx.Time(p.Record.CreatedAt),
		})
	}
	return b, newest
}

// authed returns a PDS client with a fresh session. Access tokens live ~2 h; the hub simply
// creates a new session after 90 minutes instead of juggling refresh tokens.
func (a *Adapter) authed(ctx context.Context, acct config.Account) (*httpx.Client, *session, error) {
	s, err := a.Session(ctx, acct)
	if err != nil {
		return nil, nil, err
	}
	h := http.Header{}
	h.Set("Authorization", "Bearer "+s.AccessJwt)
	return &httpx.Client{Service: "bluesky", BaseURL: s.PDS, HTTP: a.HTTP, Header: h, Secrets: []string{s.AccessJwt}}, s, nil
}

// Session logs in with the app password (cached for 90 minutes).
func (a *Adapter) Session(ctx context.Context, acct config.Account) (*session, error) {
	pw, err := creds.Static(acct, SecretSuffix)
	if err != nil {
		return nil, err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if s := a.sessions[acct.Key()]; s != nil && time.Since(s.at) < 90*time.Minute {
		return s, nil
	}
	pds := a.PDS
	if pds == "" {
		pds = "https://" + acct.APIHost()
	}
	c := &httpx.Client{Service: "bluesky", BaseURL: pds, HTTP: a.HTTP, Secrets: []string{pw}}
	var s session
	err = c.Post(ctx, "/xrpc/com.atproto.server.createSession", map[string]string{"identifier": acct.Handle, "password": pw}, &s)
	if err != nil {
		if httpx.Code(err) == http.StatusUnauthorized {
			return nil, errors.New("bluesky: app password rejected (" + creds.Name(acct, SecretSuffix) + ")")
		}
		return nil, err
	}
	s.PDS, s.at = pds, time.Now()
	if a.sessions == nil {
		a.sessions = map[string]*session{}
	}
	a.sessions[acct.Key()] = &s
	return &s, nil
}

// Forget drops a cached session (after a 401 on a call).
func (a *Adapter) Forget(acct config.Account) {
	a.mu.Lock()
	delete(a.sessions, acct.Key())
	a.mu.Unlock()
}

func mustJSON(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}
