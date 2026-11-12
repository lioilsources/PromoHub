// Package facebook reads my Facebook Page (followers, posts, comments) with the page token
// from /connect facebook:<page>. Publishing defaults to IFTTT: posts made by an app in
// Development mode are visible only to the app's roles. The api path is here for when the
// app goes Live.
package facebook

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"

	"github.com/lioilsources/promohub/internal/collectors"
	"github.com/lioilsources/promohub/internal/config"
	"github.com/lioilsources/promohub/internal/creds"
	"github.com/lioilsources/promohub/internal/platform"
	"github.com/lioilsources/promohub/internal/platforms/instagram"
	"github.com/lioilsources/promohub/internal/platforms/meta"
	"github.com/lioilsources/promohub/internal/publish"
)

const BaseURL = "https://graph.facebook.com/" + meta.Version

type Adapter struct {
	HTTP   *http.Client
	Base   string
	Tokens creds.TokenSource
}

func New(hc *http.Client, ts creds.TokenSource) *Adapter { return &Adapter{HTTP: hc, Tokens: ts} }

func (a *Adapter) Platform() string { return "facebook" }

func (a *Adapter) Ready(acct config.Account, _ string) error {
	_, err := meta.Token(context.Background(), a.Tokens, acct)
	return err
}

func (a *Adapter) graph(ctx context.Context, acct config.Account) (*meta.Graph, string, error) {
	t, err := meta.Token(ctx, a.Tokens, acct)
	if err != nil {
		return nil, "", err
	}
	page := t.PageID
	if page == "" {
		page = acct.ExternalID
	}
	if page == "" {
		return nil, "", errors.New("facebook: page ID unknown (external_id)")
	}
	base := a.Base
	if base == "" {
		base = BaseURL
	}
	return meta.New("facebook", base, a.HTTP, t.AccessToken), page, nil
}

type Post struct {
	ID          string `json:"id"`
	Message     string `json:"message"`
	Permalink   string `json:"permalink_url"`
	CreatedTime string `json:"created_time"`
	Shares      *struct {
		Count int64 `json:"count"`
	} `json:"shares"`
	Reactions summary `json:"reactions"`
	Comments  summary `json:"comments"`
}

type summary struct {
	Summary *struct {
		TotalCount int64 `json:"total_count"`
	} `json:"summary"`
}

func (s summary) ptr() *int64 {
	if s.Summary == nil {
		return nil
	}
	return &s.Summary.TotalCount
}

type Comment struct {
	ID          string `json:"id"`
	Message     string `json:"message"`
	CreatedTime string `json:"created_time"`
	Permalink   string `json:"permalink_url"`
	From        *struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"from"`
}

func (a *Adapter) Collect(ctx context.Context, r collectors.Request) (collectors.Batch, json.RawMessage, error) {
	g, page, err := a.graph(ctx, r.Account)
	if err != nil {
		return collectors.Batch{}, nil, err
	}
	var posts struct {
		Data []Post `json:"data"`
	}
	q := url.Values{"fields": {"id,message,permalink_url,created_time,shares,reactions.summary(total_count).limit(0),comments.summary(total_count).limit(0)"}, "limit": {"25"}}
	if err := g.Get(ctx, "/"+page+"/posts", q, &posts); err != nil {
		return collectors.Batch{}, nil, err
	}
	switch r.Capability {
	case platform.CapProfile:
		var p struct {
			FollowersCount *int64 `json:"followers_count"`
			FanCount       *int64 `json:"fan_count"`
		}
		if err := g.Get(ctx, "/"+page, url.Values{"fields": {"followers_count,fan_count"}}, &p); err != nil {
			return collectors.Batch{}, nil, err
		}
		return MapProfile(p.FollowersCount, p.FanCount, posts.Data), nil, nil
	case platform.CapInbound:
		var b collectors.Batch
		for i, p := range posts.Data {
			if i == 10 {
				break
			}
			if c := p.Comments.ptr(); c != nil && *c == 0 {
				continue
			}
			var cs struct {
				Data []Comment `json:"data"`
			}
			if err := g.Get(ctx, "/"+p.ID+"/comments", url.Values{"fields": {"id,message,created_time,permalink_url,from{id,name}"}, "limit": {"50"}}, &cs); err != nil {
				return b, nil, err
			}
			cb := MapComments(page, cs.Data)
			b.Inbound, b.Skipped = append(b.Inbound, cb.Inbound...), b.Skipped+cb.Skipped
		}
		return b, nil, nil
	}
	return collectors.Batch{}, nil, fmt.Errorf("facebook: unknown capability %q", r.Capability)
}

// MapProfile maps page followers and recent posts.
func MapProfile(followers, fans *int64, posts []Post) collectors.Batch {
	var b collectors.Batch
	for _, p := range posts {
		if p.ID == "" {
			b.Skipped++
			continue
		}
		var shares *int64
		if p.Shares != nil {
			shares = &p.Shares.Count
		}
		b.Snapshots = append(b.Snapshots, collectors.Snapshot{
			Platform: "facebook",
			Content:  &collectors.Content{ExternalID: p.ID, URL: p.Permalink, Body: p.Message, PublishedAt: instagram.Time(p.CreatedTime)},
			Likes:    p.Reactions.ptr(), Comments: p.Comments.ptr(), Reposts: shares,
		})
	}
	if followers != nil {
		extra := map[string]int64{}
		if fans != nil {
			extra["fans"] = *fans
		}
		b.Snapshots = append(b.Snapshots, collectors.Snapshot{Platform: "facebook", Followers: followers, Extra: extra})
	}
	return b
}

// MapComments maps comments by others (the page's own replies are skipped).
func MapComments(page string, cs []Comment) collectors.Batch {
	var b collectors.Batch
	for _, c := range cs {
		if c.ID == "" || c.From != nil && c.From.ID == page {
			b.Skipped++
			continue
		}
		author := ""
		if c.From != nil {
			author = c.From.Name // without pages_read_user_content "from" is missing
		}
		b.Inbound = append(b.Inbound, collectors.Inbound{
			Platform: "facebook", Kind: "comment", ExternalID: c.ID, Author: author, Text: c.Message,
			URL: c.Permalink, CreatedAt: instagram.Time(c.CreatedTime),
		})
	}
	return b
}

// Publisher posts to the page feed (link post) or photos (with an image).
type Publisher struct{ *Adapter }

func (p Publisher) Ready(acct config.Account) error { return p.Adapter.Ready(acct, "") }

func (p Publisher) Publish(ctx context.Context, acct config.Account, post publish.Post) (publish.Result, error) {
	g, page, err := p.graph(ctx, acct)
	if err != nil {
		return publish.Result{}, publish.Reject(err)
	}
	var res struct {
		ID     string `json:"id"`
		PostID string `json:"post_id"`
	}
	msg := post.Text
	if l := post.Extra["link"]; l != "" {
		msg += "\n\n" + l
	}
	for _, m := range post.Media {
		if m.IsImage() {
			if err := g.Post(ctx, "/"+page+"/photos", url.Values{"url": {m.URL}, "caption": {msg}}, &res); err != nil {
				return publish.Result{}, err
			}
			id := res.PostID
			if id == "" {
				id = res.ID
			}
			return publish.Result{ExternalID: id, URL: "https://www.facebook.com/" + id}, nil
		}
	}
	f := url.Values{"message": {post.Text}}
	if l := post.Extra["link"]; l != "" {
		f.Set("link", l)
	}
	if err := g.Post(ctx, "/"+page+"/feed", f, &res); err != nil {
		return publish.Result{}, err
	}
	return publish.Result{ExternalID: res.ID, URL: "https://www.facebook.com/" + res.ID}, nil
}
