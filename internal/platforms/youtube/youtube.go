// Package youtube reads my channel with the YouTube Data API v3 and an API key
// (<auth_ref>_API_KEY, default YOUTUBE_API_KEY). Quota: every call here costs 1 unit of the
// default 10 000/day; a profile pass is 3 calls, an inbound pass 1.
//
// Uploads stay manual (platform registry): videos.insert from an unaudited project is
// locked private.
package youtube

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"

	"github.com/lioilsources/promohub/internal/collectors"
	"github.com/lioilsources/promohub/internal/config"
	"github.com/lioilsources/promohub/internal/creds"
	"github.com/lioilsources/promohub/internal/platform"
	"github.com/lioilsources/promohub/internal/platforms/httpx"
)

const (
	BaseURL      = "https://www.googleapis.com/youtube/v3"
	SecretSuffix = "API_KEY"
	recentVideos = 25
)

type Adapter struct {
	HTTP *http.Client
	Base string

	mu       sync.Mutex
	channels map[string]channelInfo // account key → resolved channel
}

type channelInfo struct{ ID, Uploads string }

func New(hc *http.Client) *Adapter { return &Adapter{HTTP: hc, channels: map[string]channelInfo{}} }

func (a *Adapter) Platform() string { return "youtube" }

func (a *Adapter) Ready(acct config.Account, _ string) error {
	_, err := key(acct)
	return err
}

// key falls back to YOUTUBE_API_KEY so a shared key works for several channels.
func key(acct config.Account) (string, error) {
	k, err := creds.Static(acct, SecretSuffix)
	if err == nil {
		return k, nil
	}
	if v := strings.TrimSpace(creds.Getenv("YOUTUBE_API_KEY")); v != "" {
		return v, nil
	}
	return "", err
}

func (a *Adapter) client(acct config.Account) (*httpx.Client, string, error) {
	k, err := key(acct)
	if err != nil {
		return nil, "", err
	}
	base := a.Base
	if base == "" {
		base = BaseURL
	}
	return &httpx.Client{Service: "youtube", BaseURL: base, HTTP: a.HTTP, Secrets: []string{k}}, k, nil
}

// Wire types. The API returns counters as decimal strings.

type Count string

func (c Count) Ptr() *int64 {
	if c == "" {
		return nil
	}
	n, err := strconv.ParseInt(string(c), 10, 64)
	if err != nil {
		return nil
	}
	return &n
}

type Channel struct {
	ID         string `json:"id"`
	Statistics struct {
		SubscriberCount       Count `json:"subscriberCount"`
		ViewCount             Count `json:"viewCount"`
		VideoCount            Count `json:"videoCount"`
		HiddenSubscriberCount bool  `json:"hiddenSubscriberCount"`
	} `json:"statistics"`
	ContentDetails struct {
		RelatedPlaylists struct {
			Uploads string `json:"uploads"`
		} `json:"relatedPlaylists"`
	} `json:"contentDetails"`
}

type Video struct {
	ID      string `json:"id"`
	Snippet struct {
		Title       string `json:"title"`
		Description string `json:"description"`
		PublishedAt string `json:"publishedAt"`
	} `json:"snippet"`
	Statistics struct {
		ViewCount     Count `json:"viewCount"`
		LikeCount     Count `json:"likeCount"`
		CommentCount  Count `json:"commentCount"`
		FavoriteCount Count `json:"favoriteCount"`
	} `json:"statistics"`
}

type CommentThread struct {
	ID      string `json:"id"`
	Snippet struct {
		VideoID         string `json:"videoId"`
		TopLevelComment struct {
			ID      string `json:"id"`
			Snippet struct {
				AuthorDisplayName string `json:"authorDisplayName"`
				AuthorChannelID   struct {
					Value string `json:"value"`
				} `json:"authorChannelId"`
				TextOriginal string `json:"textOriginal"`
				PublishedAt  string `json:"publishedAt"`
			} `json:"snippet"`
		} `json:"topLevelComment"`
		TotalReplyCount int64 `json:"totalReplyCount"`
	} `json:"snippet"`
}

func (a *Adapter) Collect(ctx context.Context, r collectors.Request) (collectors.Batch, json.RawMessage, error) {
	switch r.Capability {
	case platform.CapProfile:
		b, err := a.profile(ctx, r.Account)
		return b, nil, err
	case platform.CapInbound:
		b, err := a.comments(ctx, r.Account)
		return b, nil, err
	}
	return collectors.Batch{}, nil, fmt.Errorf("youtube: unknown capability %q", r.Capability)
}

// channel resolves my channel by external_id (UC…) or by handle.
func (a *Adapter) channel(ctx context.Context, acct config.Account) (Channel, error) {
	c, k, err := a.client(acct)
	if err != nil {
		return Channel{}, err
	}
	q := url.Values{"part": {"statistics,contentDetails"}, "key": {k}}
	if acct.ExternalID != "" {
		q.Set("id", acct.ExternalID)
	} else {
		q.Set("forHandle", "@"+strings.TrimPrefix(acct.Handle, "@"))
	}
	var res struct {
		Items []Channel `json:"items"`
	}
	if err := c.Get(ctx, "/channels", q, &res); err != nil {
		return Channel{}, err
	}
	if len(res.Items) == 0 {
		return Channel{}, errors.New("youtube: channel not found (set external_id to the UC… channel ID)")
	}
	ch := res.Items[0]
	a.mu.Lock()
	if a.channels == nil {
		a.channels = map[string]channelInfo{}
	}
	a.channels[acct.Key()] = channelInfo{ID: ch.ID, Uploads: ch.ContentDetails.RelatedPlaylists.Uploads}
	a.mu.Unlock()
	return ch, nil
}

func (a *Adapter) profile(ctx context.Context, acct config.Account) (collectors.Batch, error) {
	ch, err := a.channel(ctx, acct)
	if err != nil {
		return collectors.Batch{}, err
	}
	c, k, _ := a.client(acct)
	var pl struct {
		Items []struct {
			ContentDetails struct {
				VideoID string `json:"videoId"`
			} `json:"contentDetails"`
		} `json:"items"`
	}
	q := url.Values{"part": {"contentDetails"}, "playlistId": {ch.ContentDetails.RelatedPlaylists.Uploads},
		"maxResults": {strconv.Itoa(recentVideos)}, "key": {k}}
	if err := c.Get(ctx, "/playlistItems", q, &pl); err != nil {
		return collectors.Batch{}, err
	}
	var ids []string
	for _, it := range pl.Items {
		ids = append(ids, it.ContentDetails.VideoID)
	}
	var vids struct {
		Items []Video `json:"items"`
	}
	if len(ids) > 0 {
		q := url.Values{"part": {"snippet,statistics"}, "id": {strings.Join(ids, ",")}, "key": {k}}
		if err := c.Get(ctx, "/videos", q, &vids); err != nil {
			return collectors.Batch{}, err
		}
	}
	return MapProfile(ch, vids.Items), nil
}

// MapProfile maps channel statistics and recent uploads.
func MapProfile(ch Channel, vids []Video) collectors.Batch {
	var b collectors.Batch
	for _, v := range vids {
		if v.ID == "" {
			b.Skipped++
			continue
		}
		extra := map[string]int64{}
		b.Snapshots = append(b.Snapshots, collectors.Snapshot{
			Platform: "youtube",
			Content: &collectors.Content{
				ExternalID: v.ID, URL: "https://www.youtube.com/watch?v=" + v.ID, Title: v.Snippet.Title,
				Body: v.Snippet.Description, PublishedAt: httpx.Time(v.Snippet.PublishedAt),
			},
			Views: v.Statistics.ViewCount.Ptr(), Likes: v.Statistics.LikeCount.Ptr(),
			Comments: v.Statistics.CommentCount.Ptr(), Extra: extra,
		})
	}
	s := ch.Statistics
	if f := s.SubscriberCount.Ptr(); f != nil && !s.HiddenSubscriberCount {
		extra := map[string]int64{}
		if v := s.ViewCount.Ptr(); v != nil {
			extra["channel_views"] = *v
		}
		if v := s.VideoCount.Ptr(); v != nil {
			extra["videos"] = *v
		}
		b.Snapshots = append(b.Snapshots, collectors.Snapshot{Platform: "youtube", Followers: f, Extra: extra})
	}
	return b
}

func (a *Adapter) comments(ctx context.Context, acct config.Account) (collectors.Batch, error) {
	a.mu.Lock()
	info, ok := a.channels[acct.Key()]
	a.mu.Unlock()
	if !ok {
		if acct.ExternalID != "" {
			info.ID = acct.ExternalID
		} else {
			ch, err := a.channel(ctx, acct)
			if err != nil {
				return collectors.Batch{}, err
			}
			info.ID = ch.ID
		}
	}
	c, k, err := a.client(acct)
	if err != nil {
		return collectors.Batch{}, err
	}
	var res struct {
		Items []CommentThread `json:"items"`
	}
	q := url.Values{"part": {"snippet"}, "allThreadsRelatedToChannelId": {info.ID}, "order": {"time"},
		"maxResults": {"50"}, "textFormat": {"plainText"}, "key": {k}}
	if err := c.Get(ctx, "/commentThreads", q, &res); err != nil {
		return collectors.Batch{}, err
	}
	return MapComments(info.ID, res.Items), nil
}

// MapComments maps top-level comments on my videos, minus my own.
func MapComments(myChannel string, ts []CommentThread) collectors.Batch {
	var b collectors.Batch
	for _, t := range ts {
		c := t.Snippet.TopLevelComment
		if c.ID == "" || c.Snippet.AuthorChannelID.Value == myChannel {
			b.Skipped++
			continue
		}
		b.Inbound = append(b.Inbound, collectors.Inbound{
			Platform: "youtube", Kind: "comment", ExternalID: c.ID, Author: c.Snippet.AuthorDisplayName,
			Text: c.Snippet.TextOriginal, CreatedAt: httpx.Time(c.Snippet.PublishedAt),
			URL: "https://www.youtube.com/watch?v=" + t.Snippet.VideoID + "&lc=" + c.ID,
		})
	}
	return b
}
