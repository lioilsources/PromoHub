// Package hackernews watches Hacker News: my karma and stories (Firebase API), comments on my
// stories and mentions of my apps (Algolia search_by_date). Posting stays manual (submit link).
package hackernews

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/lioilsources/promohub/internal/collectors"
	"github.com/lioilsources/promohub/internal/config"
	"github.com/lioilsources/promohub/internal/platform"
	"github.com/lioilsources/promohub/internal/platforms/httpx"
)

const (
	FirebaseURL = "https://hacker-news.firebaseio.com/v0"
	AlgoliaURL  = "https://hn.algolia.com/api/v1"
)

type Adapter struct {
	HTTP     *http.Client
	Firebase string
	Algolia  string
}

func New(hc *http.Client) *Adapter { return &Adapter{HTTP: hc} }

func (a *Adapter) Platform() string { return "hackernews" }

func (a *Adapter) Ready(config.Account, string) error { return nil }

func (a *Adapter) fb() *httpx.Client {
	return &httpx.Client{Service: "hackernews", BaseURL: or(a.Firebase, FirebaseURL), HTTP: a.HTTP}
}

func (a *Adapter) algolia() *httpx.Client {
	return &httpx.Client{Service: "hn-algolia", BaseURL: or(a.Algolia, AlgoliaURL), HTTP: a.HTTP}
}

func or(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

type Hit struct {
	ObjectID    string `json:"objectID"`
	Title       string `json:"title"`
	URL         string `json:"url"`
	Author      string `json:"author"`
	Points      *int64 `json:"points"`
	NumComments *int64 `json:"num_comments"`
	CommentText string `json:"comment_text"`
	StoryTitle  string `json:"story_title"`
	StoryID     int64  `json:"story_id"`
	CreatedAt   string `json:"created_at"`
	CreatedAtI  int64  `json:"created_at_i"`
}

type hits struct {
	Hits []Hit `json:"hits"`
}

type cursor struct {
	Since int64 `json:"since,omitempty"` // created_at_i of the newest hit seen
}

func (a *Adapter) Collect(ctx context.Context, r collectors.Request) (collectors.Batch, json.RawMessage, error) {
	user := r.Account.Handle
	switch r.Capability {
	case platform.CapProfile:
		var u struct {
			Karma *int64 `json:"karma"`
		}
		if err := a.fb().Get(ctx, "/user/"+url.PathEscape(user)+".json", nil, &u); err != nil {
			return collectors.Batch{}, nil, err
		}
		var h hits
		if err := a.algolia().Get(ctx, "/search_by_date", url.Values{"tags": {"story,author_" + user}, "hitsPerPage": {"30"}}, &h); err != nil {
			return collectors.Batch{}, nil, err
		}
		return MapProfile(u.Karma, h.Hits), nil, nil
	case platform.CapSearch:
		var c cursor
		_ = json.Unmarshal(r.Cursor, &c)
		var b collectors.Batch
		newest := c.Since
		for _, term := range r.Terms {
			q := url.Values{"query": {term}, "tags": {"(story,comment)"}, "hitsPerPage": {"30"}}
			if c.Since > 0 {
				q.Set("numericFilters", "created_at_i>"+strconv.FormatInt(c.Since, 10))
			}
			var h hits
			if err := a.algolia().Get(ctx, "/search_by_date", q, &h); err != nil {
				return b, nil, fmt.Errorf("search %q: %w", term, err)
			}
			mb, n := MapMentions(user, term, h.Hits)
			b.Inbound, b.Skipped = append(b.Inbound, mb.Inbound...), b.Skipped+mb.Skipped
			newest = max(newest, n)
		}
		out, _ := json.Marshal(cursor{Since: newest})
		return b, out, nil
	}
	return collectors.Batch{}, nil, fmt.Errorf("hackernews: unknown capability %q", r.Capability)
}

// ItemURL is the discussion page of a story or comment.
func ItemURL(id string) string { return "https://news.ycombinator.com/item?id=" + id }

// MapProfile maps karma (HN has no followers; karma is the account metric) and my stories.
func MapProfile(karma *int64, stories []Hit) collectors.Batch {
	var b collectors.Batch
	for _, s := range stories {
		if s.ObjectID == "" {
			b.Skipped++
			continue
		}
		b.Snapshots = append(b.Snapshots, collectors.Snapshot{
			Platform: "hackernews",
			Content:  &collectors.Content{ExternalID: s.ObjectID, URL: ItemURL(s.ObjectID), Title: s.Title, Body: s.URL, PublishedAt: httpx.Time(s.CreatedAt)},
			Likes:    s.Points, Comments: s.NumComments,
		})
	}
	if karma != nil {
		b.Snapshots = append(b.Snapshots, collectors.Snapshot{Platform: "hackernews", Followers: karma, Extra: map[string]int64{"karma": *karma}})
	}
	return b
}

var reTags = regexp.MustCompile(`<[^>]+>`)

// MapMentions keeps hits that really contain the term (Algolia also matches stems), minus mine.
func MapMentions(me, term string, hs []Hit) (collectors.Batch, int64) {
	var b collectors.Batch
	var newest int64
	for _, h := range hs {
		newest = max(newest, h.CreatedAtI)
		text := h.Title
		if h.CommentText != "" {
			text = strings.TrimSpace(html.UnescapeString(reTags.ReplaceAllString(strings.ReplaceAll(h.CommentText, "<p>", "\n"), "")))
		}
		if h.ObjectID == "" || strings.EqualFold(h.Author, me) || !strings.Contains(strings.ToLower(text+" "+h.URL), strings.ToLower(term)) {
			b.Skipped++
			continue
		}
		kind := "mention"
		if h.CommentText != "" && h.StoryTitle != "" {
			text = h.StoryTitle + "\n\n" + text
		}
		var at *time.Time
		if h.CreatedAtI > 0 {
			t := time.Unix(h.CreatedAtI, 0).UTC()
			at = &t
		}
		b.Inbound = append(b.Inbound, collectors.Inbound{
			Platform: "hackernews", Kind: kind, ExternalID: h.ObjectID, Author: h.Author, Text: text,
			URL: ItemURL(h.ObjectID), CreatedAt: at,
		})
	}
	return b, newest
}
