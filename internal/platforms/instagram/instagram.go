// Package instagram reads and publishes through the Instagram API with Instagram Login
// (graph.instagram.com, professional account, no Facebook Page needed). The OAuth token comes
// from the hub's credentials store (/connect instagram:handle).
package instagram

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/lioilsources/promohub/internal/collectors"
	"github.com/lioilsources/promohub/internal/config"
	"github.com/lioilsources/promohub/internal/creds"
	"github.com/lioilsources/promohub/internal/platform"
	"github.com/lioilsources/promohub/internal/platforms/httpx"
	"github.com/lioilsources/promohub/internal/platforms/meta"
	"github.com/lioilsources/promohub/internal/publish"
)

const (
	BaseURL      = "https://graph.instagram.com/" + meta.Version
	recent       = 25
	withInsights = 10 // insights cost one call per post
)

type Adapter struct {
	HTTP      *http.Client
	Base      string
	Tokens    creds.TokenSource
	PollEvery time.Duration
}

func New(hc *http.Client, ts creds.TokenSource) *Adapter { return &Adapter{HTTP: hc, Tokens: ts} }

func (a *Adapter) Platform() string { return "instagram" }

func (a *Adapter) Ready(acct config.Account, _ string) error {
	_, err := meta.Token(context.Background(), a.Tokens, acct)
	return err
}

func (a *Adapter) graph(ctx context.Context, acct config.Account) (*meta.Graph, creds.Token, error) {
	t, err := meta.Token(ctx, a.Tokens, acct)
	if err != nil {
		return nil, t, err
	}
	base := a.Base
	if base == "" {
		base = BaseURL
	}
	return meta.New("instagram", base, a.HTTP, t.AccessToken), t, nil
}

type Me struct {
	UserID         string `json:"user_id"`
	Username       string `json:"username"`
	FollowersCount *int64 `json:"followers_count"`
	FollowsCount   *int64 `json:"follows_count"`
	MediaCount     *int64 `json:"media_count"`
}

type Media struct {
	ID            string `json:"id"`
	Caption       string `json:"caption"`
	MediaType     string `json:"media_type"`
	Permalink     string `json:"permalink"`
	Shortcode     string `json:"shortcode"`
	Timestamp     string `json:"timestamp"` // 2026-11-12T10:00:00+0000
	LikeCount     *int64 `json:"like_count"`
	CommentsCount *int64 `json:"comments_count"`
}

type Comment struct {
	ID        string `json:"id"`
	Text      string `json:"text"`
	Username  string `json:"username"`
	Timestamp string `json:"timestamp"`
}

func (a *Adapter) Collect(ctx context.Context, r collectors.Request) (collectors.Batch, json.RawMessage, error) {
	g, _, err := a.graph(ctx, r.Account)
	if err != nil {
		return collectors.Batch{}, nil, err
	}
	var me Me
	if err := g.Get(ctx, "/me", url.Values{"fields": {"user_id,username,followers_count,follows_count,media_count"}}, &me); err != nil {
		return collectors.Batch{}, nil, err
	}
	var media struct {
		Data []Media `json:"data"`
	}
	n := recent
	if r.Capability == platform.CapInbound {
		n = withInsights
	}
	q := url.Values{"fields": {"id,caption,media_type,permalink,shortcode,timestamp,like_count,comments_count"}, "limit": {strconv.Itoa(n)}}
	if err := g.Get(ctx, "/me/media", q, &media); err != nil {
		return collectors.Batch{}, nil, err
	}
	switch r.Capability {
	case platform.CapProfile:
		ins := map[string]map[string]int64{}
		for i, m := range media.Data {
			if i == withInsights {
				break
			}
			var in meta.Insights
			// Some media types reject some metrics; a missing insight is not a failed pass.
			if err := g.Get(ctx, "/"+m.ID+"/insights", url.Values{"metric": {"views,reach,saved,shares"}}, &in); err == nil {
				ins[m.ID] = in.Map()
			}
		}
		return MapProfile(me, media.Data, ins), nil, nil
	case platform.CapInbound:
		var b collectors.Batch
		for _, m := range media.Data {
			if m.CommentsCount != nil && *m.CommentsCount == 0 {
				continue
			}
			var cs struct {
				Data []Comment `json:"data"`
			}
			if err := g.Get(ctx, "/"+m.ID+"/comments", url.Values{"fields": {"id,text,username,timestamp"}, "limit": {"50"}}, &cs); err != nil {
				return b, nil, err
			}
			cb := MapComments(me.Username, m, cs.Data)
			b.Inbound = append(b.Inbound, cb.Inbound...)
			b.Skipped += cb.Skipped
		}
		return b, nil, nil
	}
	return collectors.Batch{}, nil, fmt.Errorf("instagram: unknown capability %q", r.Capability)
}

// Time parses Graph timestamps ("2026-11-12T10:00:00+0000").
func Time(s string) *time.Time {
	t, err := time.Parse("2006-01-02T15:04:05-0700", s)
	if err != nil {
		return httpx.Time(s)
	}
	t = t.UTC()
	return &t
}

// MapProfile maps my account and recent media; the shortcode is the canonical ID.
func MapProfile(me Me, media []Media, ins map[string]map[string]int64) collectors.Batch {
	var b collectors.Batch
	for _, m := range media {
		id := m.Shortcode
		if id == "" {
			id = platform.ExternalID("instagram", m.Permalink)
		}
		if id == "" || strings.HasPrefix(id, "http") {
			b.Skipped++
			continue
		}
		in := ins[m.ID]
		extra := map[string]int64{}
		for _, k := range []string{"reach", "saved", "shares"} {
			if v, ok := in[k]; ok {
				extra[k] = v
			}
		}
		b.Snapshots = append(b.Snapshots, collectors.Snapshot{
			Platform: "instagram",
			Content:  &collectors.Content{ExternalID: id, URL: m.Permalink, Body: m.Caption, PublishedAt: Time(m.Timestamp)},
			Likes:    m.LikeCount, Comments: m.CommentsCount, Views: meta.Ptr(in, "views"), Extra: extra,
		})
	}
	if me.FollowersCount != nil {
		extra := map[string]int64{}
		if me.MediaCount != nil {
			extra["posts"] = *me.MediaCount
		}
		if me.FollowsCount != nil {
			extra["follows"] = *me.FollowsCount
		}
		b.Snapshots = append(b.Snapshots, collectors.Snapshot{Platform: "instagram", Followers: me.FollowersCount, Extra: extra})
	}
	return b
}

// MapComments maps comments on one of my posts, minus my own replies.
func MapComments(myUsername string, m Media, cs []Comment) collectors.Batch {
	var b collectors.Batch
	for _, c := range cs {
		if c.ID == "" || strings.EqualFold(c.Username, myUsername) {
			b.Skipped++
			continue
		}
		b.Inbound = append(b.Inbound, collectors.Inbound{
			Platform: "instagram", Kind: "comment", ExternalID: c.ID, Author: c.Username, Text: c.Text,
			URL: m.Permalink, CreatedAt: Time(c.Timestamp),
		})
	}
	return b
}

// Publisher posts images, carousels and Reels. Instagram fetches media from public URLs
// (/media/{token}); a text-only post is impossible.
type Publisher struct{ *Adapter }

func (p Publisher) Ready(acct config.Account) error { return p.Adapter.Ready(acct, "") }

func (p Publisher) Publish(ctx context.Context, acct config.Account, post publish.Post) (publish.Result, error) {
	g, _, err := p.graph(ctx, acct)
	if err != nil {
		return publish.Result{}, publish.Reject(err)
	}
	var items []publish.Media
	for _, m := range post.Media {
		if m.IsImage() || strings.HasPrefix(m.Mime, "video/") {
			items = append(items, m)
		}
	}
	if len(items) == 0 {
		return publish.Result{}, publish.Reject(errors.New("instagram needs an image or a video"))
	}
	if len(items) > 10 {
		items = items[:10]
	}
	every := p.PollEvery
	if every == 0 {
		every = 5 * time.Second
	}
	container := func(m publish.Media, carousel bool) (string, error) {
		f := url.Values{}
		if m.IsImage() {
			f.Set("image_url", m.URL)
		} else {
			f.Set("media_type", "REELS")
			if carousel {
				f.Set("media_type", "VIDEO")
			}
			f.Set("video_url", m.URL)
		}
		if carousel {
			f.Set("is_carousel_item", "true")
		} else {
			f.Set("caption", post.Text)
		}
		if m.Alt != "" && m.IsImage() {
			f.Set("alt_text", m.Alt)
		}
		var res struct {
			ID string `json:"id"`
		}
		if err := g.Post(ctx, "/me/media", f, &res); err != nil {
			return "", err
		}
		return res.ID, g.Wait(ctx, res.ID, "status_code", every, 60)
	}
	var creation string
	if len(items) == 1 {
		creation, err = container(items[0], false)
	} else {
		var kids []string
		for _, m := range items {
			id, err := container(m, true)
			if err != nil {
				return publish.Result{}, publish.Reject(err)
			}
			kids = append(kids, id)
		}
		var res struct {
			ID string `json:"id"`
		}
		err = g.Post(ctx, "/me/media", url.Values{"media_type": {"CAROUSEL"}, "children": {strings.Join(kids, ",")}, "caption": {post.Text}}, &res)
		if err == nil {
			creation = res.ID
			err = g.Wait(ctx, creation, "status_code", every, 60)
		}
	}
	if err != nil {
		return publish.Result{}, publish.Reject(err) // nothing is public before media_publish
	}
	var pub struct {
		ID string `json:"id"`
	}
	if err := g.Post(ctx, "/me/media_publish", url.Values{"creation_id": {creation}}, &pub); err != nil {
		return publish.Result{}, err
	}
	var m Media
	if err := g.Get(ctx, "/"+pub.ID, url.Values{"fields": {"permalink,shortcode"}}, &m); err != nil || m.Shortcode == "" {
		return publish.Result{ExternalID: pub.ID}, nil // published; the next collect fills in the link
	}
	return publish.Result{ExternalID: m.Shortcode, URL: m.Permalink}, nil
}
