// Package devto reads my dev.to articles and followers and publishes articles with a
// canonical_url pointing at my blog. The API key is <auth_ref>_API_KEY (Settings → Extensions).
package devto

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/lioilsources/promohub/internal/collectors"
	"github.com/lioilsources/promohub/internal/config"
	"github.com/lioilsources/promohub/internal/creds"
	"github.com/lioilsources/promohub/internal/platform"
	"github.com/lioilsources/promohub/internal/platforms/httpx"
	"github.com/lioilsources/promohub/internal/publish"
)

const (
	BaseURL      = "https://dev.to/api"
	SecretSuffix = "API_KEY"
)

type Adapter struct {
	HTTP *http.Client
	Base string
}

func New(hc *http.Client) *Adapter { return &Adapter{HTTP: hc} }

func (a *Adapter) Platform() string { return "devto" }

func (a *Adapter) Ready(acct config.Account, _ string) error {
	_, err := creds.Static(acct, SecretSuffix)
	return err
}

func (a *Adapter) client(acct config.Account) (*httpx.Client, error) {
	k, err := creds.Static(acct, SecretSuffix)
	if err != nil {
		return nil, err
	}
	base := a.Base
	if base == "" {
		base = BaseURL
	}
	// dev.to wants a versioned Accept header and blocks requests without a User-Agent.
	return &httpx.Client{Service: "devto", BaseURL: base, HTTP: a.HTTP, Secrets: []string{k}, Header: http.Header{
		"Api-Key": {k}, "User-Agent": {"promohub"}, "Accept": {"application/vnd.forem.api-v1+json"},
	}}, nil
}

type Article struct {
	ID                   int64  `json:"id"`
	Title                string `json:"title"`
	URL                  string `json:"url"`
	PublishedAt          string `json:"published_at"`
	PageViewsCount       *int64 `json:"page_views_count"`
	PublicReactionsCount *int64 `json:"public_reactions_count"`
	CommentsCount        *int64 `json:"comments_count"`
}

type Comment struct {
	IDCode    string                    `json:"id_code"`
	BodyHTML  string                    `json:"body_html"`
	CreatedAt string                    `json:"created_at"`
	User      struct{ Username string } `json:"user"`
	Children  []Comment                 `json:"children"`
}

func (a *Adapter) Collect(ctx context.Context, r collectors.Request) (collectors.Batch, json.RawMessage, error) {
	c, err := a.client(r.Account)
	if err != nil {
		return collectors.Batch{}, nil, err
	}
	var arts []Article
	if err := c.Get(ctx, "/articles/me/published", url.Values{"per_page": {"30"}}, &arts); err != nil {
		return collectors.Batch{}, nil, err
	}
	switch r.Capability {
	case platform.CapProfile:
		n := int64(0)
		for page := 1; page <= 20; page++ {
			var fs []json.RawMessage
			if err := c.Get(ctx, "/followers/users", url.Values{"per_page": {"1000"}, "page": {strconv.Itoa(page)}}, &fs); err != nil {
				return collectors.Batch{}, nil, err
			}
			n += int64(len(fs))
			if len(fs) < 1000 {
				break
			}
		}
		return MapProfile(n, arts), nil, nil
	case platform.CapInbound:
		var b collectors.Batch
		for i, art := range arts {
			if i == 10 || art.CommentsCount != nil && *art.CommentsCount == 0 {
				continue
			}
			var cs []Comment
			if err := c.Get(ctx, "/comments", url.Values{"a_id": {strconv.FormatInt(art.ID, 10)}}, &cs); err != nil {
				return b, nil, err
			}
			cb := MapComments(r.Account.Handle, art, cs)
			b.Inbound, b.Skipped = append(b.Inbound, cb.Inbound...), b.Skipped+cb.Skipped
		}
		return b, nil, nil
	}
	return collectors.Batch{}, nil, fmt.Errorf("devto: unknown capability %q", r.Capability)
}

// MapProfile maps follower count and my articles (article ID is the external ID).
func MapProfile(followers int64, arts []Article) collectors.Batch {
	var b collectors.Batch
	for _, a := range arts {
		b.Snapshots = append(b.Snapshots, collectors.Snapshot{
			Platform: "devto",
			Content:  &collectors.Content{ExternalID: strconv.FormatInt(a.ID, 10), URL: a.URL, Title: a.Title, PublishedAt: httpx.Time(a.PublishedAt)},
			Views:    a.PageViewsCount, Likes: a.PublicReactionsCount, Comments: a.CommentsCount,
		})
	}
	b.Snapshots = append(b.Snapshots, collectors.Snapshot{Platform: "devto", Followers: &followers})
	return b
}

var reTags = regexp.MustCompile(`<[^>]+>`)

// MapComments flattens the comment tree of one article, minus my own comments.
func MapComments(me string, art Article, cs []Comment) collectors.Batch {
	var b collectors.Batch
	var walk func([]Comment)
	walk = func(cs []Comment) {
		for _, c := range cs {
			if c.IDCode == "" || strings.EqualFold(c.User.Username, me) {
				b.Skipped++
			} else {
				b.Inbound = append(b.Inbound, collectors.Inbound{
					Platform: "devto", Kind: "comment", ExternalID: c.IDCode, Author: c.User.Username,
					Text: strings.TrimSpace(html.UnescapeString(reTags.ReplaceAllString(c.BodyHTML, ""))),
					URL:  "https://dev.to/" + c.User.Username + "/comment/" + c.IDCode, CreatedAt: httpx.Time(c.CreatedAt),
				})
			}
			walk(c.Children)
		}
	}
	walk(cs)
	return b
}

// Publisher posts an article. The draft text is the markdown body; extra["title"] the title;
// the campaign link becomes canonical_url so the blog keeps the SEO.
type Publisher struct{ *Adapter }

func (p Publisher) Ready(acct config.Account) error { return p.Adapter.Ready(acct, "") }

func (p Publisher) Publish(ctx context.Context, acct config.Account, post publish.Post) (publish.Result, error) {
	c, err := p.client(acct)
	if err != nil {
		return publish.Result{}, publish.Reject(err)
	}
	if post.Title == "" {
		return publish.Result{}, publish.Reject(errors.New("dev.to needs a title (first line of /post)"))
	}
	art := map[string]any{"title": post.Title, "body_markdown": post.Text, "published": true}
	if l := post.Link; l != "" {
		art["canonical_url"] = l
	}
	if t := post.Extra["tags"]; t != "" {
		art["tags"] = strings.Split(t, ",")
	}
	var res Article
	if err := c.Post(ctx, "/articles", map[string]any{"article": art}, &res); err != nil {
		return publish.Result{}, err
	}
	return publish.Result{ExternalID: strconv.FormatInt(res.ID, 10), URL: res.URL}, nil
}
