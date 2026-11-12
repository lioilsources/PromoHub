// Package threads reads and publishes through the Threads API (graph.threads.net). My own
// account works as a Threads tester of the app without App Review.
package threads

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/lioilsources/promohub/internal/collectors"
	"github.com/lioilsources/promohub/internal/config"
	"github.com/lioilsources/promohub/internal/creds"
	"github.com/lioilsources/promohub/internal/platform"
	"github.com/lioilsources/promohub/internal/platforms/instagram"
	"github.com/lioilsources/promohub/internal/platforms/meta"
	"github.com/lioilsources/promohub/internal/publish"
)

const (
	BaseURL      = "https://graph.threads.net/v1.0"
	recent       = 25
	withInsights = 10
)

type Adapter struct {
	HTTP      *http.Client
	Base      string
	Tokens    creds.TokenSource
	PollEvery time.Duration
}

func New(hc *http.Client, ts creds.TokenSource) *Adapter { return &Adapter{HTTP: hc, Tokens: ts} }

func (a *Adapter) Platform() string { return "threads" }

func (a *Adapter) Ready(acct config.Account, _ string) error {
	_, err := meta.Token(context.Background(), a.Tokens, acct)
	return err
}

func (a *Adapter) graph(ctx context.Context, acct config.Account) (*meta.Graph, error) {
	t, err := meta.Token(ctx, a.Tokens, acct)
	if err != nil {
		return nil, err
	}
	base := a.Base
	if base == "" {
		base = BaseURL
	}
	return meta.New("threads", base, a.HTTP, t.AccessToken), nil
}

type Post struct {
	ID        string `json:"id"`
	Text      string `json:"text"`
	Username  string `json:"username"`
	Permalink string `json:"permalink"`
	Shortcode string `json:"shortcode"`
	Timestamp string `json:"timestamp"`
	MediaType string `json:"media_type"`
}

func (a *Adapter) Collect(ctx context.Context, r collectors.Request) (collectors.Batch, json.RawMessage, error) {
	g, err := a.graph(ctx, r.Account)
	if err != nil {
		return collectors.Batch{}, nil, err
	}
	var me struct {
		ID       string `json:"id"`
		Username string `json:"username"`
	}
	if err := g.Get(ctx, "/me", url.Values{"fields": {"id,username"}}, &me); err != nil {
		return collectors.Batch{}, nil, err
	}
	var posts struct {
		Data []Post `json:"data"`
	}
	q := url.Values{"fields": {"id,text,username,permalink,shortcode,timestamp,media_type"}, "limit": {fmt.Sprint(recent)}}
	if err := g.Get(ctx, "/me/threads", q, &posts); err != nil {
		return collectors.Batch{}, nil, err
	}
	// Reposts of other people's posts have no insights or replies of mine.
	own := posts.Data[:0:0]
	for _, p := range posts.Data {
		if p.MediaType != "REPOST_FACADE" {
			own = append(own, p)
		}
	}
	switch r.Capability {
	case platform.CapProfile:
		var f meta.Insights
		if err := g.Get(ctx, "/me/threads_insights", url.Values{"metric": {"followers_count"}}, &f); err != nil {
			return collectors.Batch{}, nil, err
		}
		ins := map[string]map[string]int64{}
		for i, p := range own {
			if i == withInsights {
				break
			}
			var in meta.Insights
			if err := g.Get(ctx, "/"+p.ID+"/insights", url.Values{"metric": {"views,likes,replies,reposts,quotes,shares"}}, &in); err == nil {
				ins[p.ID] = in.Map()
			}
		}
		return MapProfile(meta.Ptr(f.Map(), "followers_count"), posts.Data, ins), nil, nil
	case platform.CapInbound:
		var b collectors.Batch
		var ms struct {
			Data []Post `json:"data"`
		}
		if err := g.Get(ctx, "/me/mentions", url.Values{"fields": {"id,text,username,permalink,timestamp"}}, &ms); err == nil {
			mb := MapInbound(me.Username, "mention", ms.Data)
			b.Inbound, b.Skipped = append(b.Inbound, mb.Inbound...), b.Skipped+mb.Skipped
		}
		for i, p := range own {
			if i == withInsights {
				break
			}
			var rs struct {
				Data []Post `json:"data"`
			}
			if err := g.Get(ctx, "/"+p.ID+"/replies", url.Values{"fields": {"id,text,username,permalink,timestamp"}}, &rs); err != nil {
				return b, nil, err
			}
			rb := MapInbound(me.Username, "reply", rs.Data)
			b.Inbound, b.Skipped = append(b.Inbound, rb.Inbound...), b.Skipped+rb.Skipped
		}
		return b, nil, nil
	}
	return collectors.Batch{}, nil, fmt.Errorf("threads: unknown capability %q", r.Capability)
}

// MapProfile maps follower count and recent posts with their insights.
func MapProfile(followers *int64, posts []Post, ins map[string]map[string]int64) collectors.Batch {
	var b collectors.Batch
	for _, p := range posts {
		id := p.Shortcode
		if id == "" {
			id = platform.ExternalID("threads", p.Permalink)
		}
		if id == "" || strings.HasPrefix(id, "http") || p.MediaType == "REPOST_FACADE" {
			b.Skipped++
			continue
		}
		in := ins[p.ID]
		extra := map[string]int64{}
		for _, k := range []string{"quotes", "shares"} {
			if v, ok := in[k]; ok {
				extra[k] = v
			}
		}
		b.Snapshots = append(b.Snapshots, collectors.Snapshot{
			Platform: "threads",
			Content:  &collectors.Content{ExternalID: id, URL: p.Permalink, Body: p.Text, PublishedAt: instagram.Time(p.Timestamp)},
			Views:    meta.Ptr(in, "views"), Likes: meta.Ptr(in, "likes"), Comments: meta.Ptr(in, "replies"),
			Reposts: meta.Ptr(in, "reposts"), Extra: extra,
		})
	}
	if followers != nil {
		b.Snapshots = append(b.Snapshots, collectors.Snapshot{Platform: "threads", Followers: followers})
	}
	return b
}

// MapInbound maps mentions or replies, minus my own posts.
func MapInbound(me, kind string, ps []Post) collectors.Batch {
	var b collectors.Batch
	for _, p := range ps {
		if p.ID == "" || strings.EqualFold(p.Username, me) {
			b.Skipped++
			continue
		}
		b.Inbound = append(b.Inbound, collectors.Inbound{
			Platform: "threads", Kind: kind, ExternalID: p.ID, Author: p.Username, Text: p.Text,
			URL: p.Permalink, CreatedAt: instagram.Time(p.Timestamp),
		})
	}
	return b
}

// Publisher creates a container and publishes it: text (with a link attachment), image,
// video or carousel.
type Publisher struct{ *Adapter }

func (p Publisher) Ready(acct config.Account) error { return p.Adapter.Ready(acct, "") }

func (p Publisher) Publish(ctx context.Context, acct config.Account, post publish.Post) (publish.Result, error) {
	g, err := p.graph(ctx, acct)
	if err != nil {
		return publish.Result{}, publish.Reject(err)
	}
	every := p.PollEvery
	if every == 0 {
		every = 5 * time.Second
	}
	var media []publish.Media
	for _, m := range post.Media {
		if m.IsImage() || strings.HasPrefix(m.Mime, "video/") {
			media = append(media, m)
		}
	}
	item := func(m publish.Media, carousel bool) url.Values {
		f := url.Values{}
		if m.IsImage() {
			f.Set("media_type", "IMAGE")
			f.Set("image_url", m.URL)
		} else {
			f.Set("media_type", "VIDEO")
			f.Set("video_url", m.URL)
		}
		if m.Alt != "" {
			f.Set("alt_text", m.Alt)
		}
		if carousel {
			f.Set("is_carousel_item", "true")
		}
		return f
	}
	create := func(f url.Values) (string, error) {
		var res struct {
			ID string `json:"id"`
		}
		if err := g.Post(ctx, "/me/threads", f, &res); err != nil {
			return "", err
		}
		return res.ID, nil
	}
	var f url.Values
	switch {
	case len(media) == 0:
		f = url.Values{"media_type": {"TEXT"}}
		if l := post.Extra["link"]; l != "" {
			f.Set("link_attachment", l)
		}
	case len(media) == 1:
		f = item(media[0], false)
	default:
		var kids []string
		for i, m := range media {
			if i == 20 {
				break
			}
			id, err := create(item(m, true))
			if err == nil {
				err = g.Wait(ctx, id, "status", every, 60)
			}
			if err != nil {
				return publish.Result{}, publish.Reject(err)
			}
			kids = append(kids, id)
		}
		f = url.Values{"media_type": {"CAROUSEL"}, "children": {strings.Join(kids, ",")}}
	}
	f.Set("text", post.Text)
	id, err := create(f)
	if err == nil && len(media) > 0 {
		err = g.Wait(ctx, id, "status", every, 60)
	}
	if err != nil {
		return publish.Result{}, publish.Reject(err)
	}
	var pub struct {
		ID string `json:"id"`
	}
	if err := g.Post(ctx, "/me/threads_publish", url.Values{"creation_id": {id}}, &pub); err != nil {
		return publish.Result{}, err
	}
	var got Post
	if err := g.Get(ctx, "/"+pub.ID, url.Values{"fields": {"permalink,shortcode"}}, &got); err != nil || got.Shortcode == "" {
		return publish.Result{ExternalID: pub.ID}, nil
	}
	return publish.Result{ExternalID: got.Shortcode, URL: got.Permalink}, nil
}
