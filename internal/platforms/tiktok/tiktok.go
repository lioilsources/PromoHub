// Package tiktok reads my TikTok account (Display API: user.info.stats, video.list) and hands
// videos over as inbox drafts (Content Posting API, video.upload): TikTok notifies me in the
// app, where I add the sound and publish. Direct Post needs an audited app, so it is not used.
package tiktok

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"time"

	"github.com/lioilsources/promohub/internal/collectors"
	"github.com/lioilsources/promohub/internal/config"
	"github.com/lioilsources/promohub/internal/creds"
	"github.com/lioilsources/promohub/internal/platform"
	"github.com/lioilsources/promohub/internal/platforms/httpx"
	"github.com/lioilsources/promohub/internal/publish"
)

const (
	BaseURL   = "https://open.tiktokapis.com"
	chunkSize = 10 << 20 // 5–64 MB per chunk; the last one absorbs the remainder
	maxSingle = 64 << 20
)

type Adapter struct {
	HTTP   *http.Client
	Base   string
	Tokens creds.TokenSource
}

func New(hc *http.Client, ts creds.TokenSource) *Adapter { return &Adapter{HTTP: hc, Tokens: ts} }

func (a *Adapter) Platform() string { return "tiktok" }

func (a *Adapter) Ready(acct config.Account, _ string) error {
	_, err := creds.OAuthToken(context.Background(), a.Tokens, acct)
	return err
}

func (a *Adapter) api(ctx context.Context, acct config.Account) (*httpx.Client, error) {
	t, err := creds.OAuthToken(ctx, a.Tokens, acct)
	if err != nil {
		return nil, err
	}
	base := a.Base
	if base == "" {
		base = BaseURL
	}
	return &httpx.Client{Service: "tiktok", BaseURL: base, HTTP: a.HTTP, Secrets: []string{t.AccessToken},
		Header: http.Header{"Authorization": {"Bearer " + t.AccessToken}}}, nil
}

// apiError is the error object every TikTok answer carries; code "ok" means success.
type apiError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	LogID   string `json:"log_id"`
}

func (e apiError) err() error {
	if e.Code == "" || e.Code == "ok" {
		return nil
	}
	return fmt.Errorf("tiktok: %s: %s", e.Code, e.Message)
}

type User struct {
	Username       string `json:"username"`
	FollowerCount  *int64 `json:"follower_count"`
	FollowingCount *int64 `json:"following_count"`
	LikesCount     *int64 `json:"likes_count"`
	VideoCount     *int64 `json:"video_count"`
}

type Video struct {
	ID           string `json:"id"`
	Title        string `json:"title"`
	Description  string `json:"video_description"`
	CreateTime   int64  `json:"create_time"`
	ShareURL     string `json:"share_url"`
	ViewCount    *int64 `json:"view_count"`
	LikeCount    *int64 `json:"like_count"`
	CommentCount *int64 `json:"comment_count"`
	ShareCount   *int64 `json:"share_count"`
}

func (a *Adapter) Collect(ctx context.Context, r collectors.Request) (collectors.Batch, json.RawMessage, error) {
	if r.Capability != platform.CapProfile {
		return collectors.Batch{}, nil, fmt.Errorf("tiktok: unknown capability %q", r.Capability)
	}
	c, err := a.api(ctx, r.Account)
	if err != nil {
		return collectors.Batch{}, nil, err
	}
	var u struct {
		Data struct {
			User User `json:"user"`
		} `json:"data"`
		Error apiError `json:"error"`
	}
	if err := c.Get(ctx, "/v2/user/info/", url.Values{"fields": {"open_id,username,follower_count,following_count,likes_count,video_count"}}, &u); err != nil {
		return collectors.Batch{}, nil, err
	}
	if err := u.Error.err(); err != nil {
		return collectors.Batch{}, nil, err
	}
	var v struct {
		Data struct {
			Videos []Video `json:"videos"`
		} `json:"data"`
		Error apiError `json:"error"`
	}
	fields := "id,title,video_description,create_time,share_url,view_count,like_count,comment_count,share_count"
	if err := c.Do(ctx, http.MethodPost, "/v2/video/list/", url.Values{"fields": {fields}}, map[string]int{"max_count": 20}, &v); err != nil {
		return collectors.Batch{}, nil, err
	}
	if err := v.Error.err(); err != nil {
		return collectors.Batch{}, nil, err
	}
	return MapProfile(u.Data.User, v.Data.Videos), nil, nil
}

// MapProfile maps account stats and recent videos (video ID = the Apify mapping's ID).
func MapProfile(u User, vids []Video) collectors.Batch {
	var b collectors.Batch
	for _, v := range vids {
		if v.ID == "" {
			b.Skipped++
			continue
		}
		var at *time.Time
		if v.CreateTime > 0 {
			t := time.Unix(v.CreateTime, 0).UTC()
			at = &t
		}
		body := v.Description
		if body == "" {
			body = v.Title
		}
		b.Snapshots = append(b.Snapshots, collectors.Snapshot{
			Platform: "tiktok",
			Content:  &collectors.Content{ExternalID: v.ID, URL: v.ShareURL, Body: body, PublishedAt: at},
			Views:    v.ViewCount, Likes: v.LikeCount, Comments: v.CommentCount, Reposts: v.ShareCount,
		})
	}
	if u.FollowerCount != nil {
		extra := map[string]int64{}
		if u.LikesCount != nil {
			extra["total_likes"] = *u.LikesCount
		}
		if u.VideoCount != nil {
			extra["videos"] = *u.VideoCount
		}
		b.Snapshots = append(b.Snapshots, collectors.Snapshot{Platform: "tiktok", Followers: u.FollowerCount, Extra: extra})
	}
	return b
}

// Publisher uploads the video to my TikTok inbox as a draft.
type Publisher struct{ *Adapter }

func (p Publisher) Ready(acct config.Account) error { return p.Adapter.Ready(acct, "") }

// Chunks splits size into TikTok upload chunks: one when it fits 64 MB, else 10 MB pieces with
// the remainder added to the last.
func Chunks(size int64) (chunk int64, count int) {
	if size <= maxSingle {
		return size, 1
	}
	return chunkSize, int(size / chunkSize)
}

func (p Publisher) Publish(ctx context.Context, acct config.Account, post publish.Post) (publish.Result, error) {
	var vid *publish.Media
	for i := range post.Media {
		if post.Media[i].IsVideo() {
			vid = &post.Media[i]
			break
		}
	}
	if vid == nil {
		return publish.Result{}, publish.Reject(errors.New("tiktok needs a video"))
	}
	c, err := p.api(ctx, acct)
	if err != nil {
		return publish.Result{}, publish.Reject(err)
	}
	f, err := os.Open(vid.Path)
	if err != nil {
		return publish.Result{}, publish.Reject(err)
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return publish.Result{}, publish.Reject(err)
	}
	size := st.Size()
	chunk, count := Chunks(size)
	var init struct {
		Data struct {
			PublishID string `json:"publish_id"`
			UploadURL string `json:"upload_url"`
		} `json:"data"`
		Error apiError `json:"error"`
	}
	err = c.Post(ctx, "/v2/post/publish/inbox/video/init/", map[string]any{"source_info": map[string]any{
		"source": "FILE_UPLOAD", "video_size": size, "chunk_size": chunk, "total_chunk_count": count,
	}}, &init)
	if err == nil {
		err = init.Error.err()
	}
	if err != nil {
		return publish.Result{}, publish.Reject(err)
	}
	for i := 0; i < count; i++ {
		start := int64(i) * chunk
		end := start + chunk
		if i == count-1 {
			end = size
		}
		if err := p.put(ctx, init.Data.UploadURL, io.NewSectionReader(f, start, end-start), start, end, size, vid.Mime); err != nil {
			return publish.Result{}, publish.Reject(fmt.Errorf("upload chunk %d/%d: %w", i+1, count, err))
		}
	}
	return publish.Result{Sent: true, ExternalID: init.Data.PublishID,
		Note: "video je v TikTok inboxu jako draft. Otevři notifikaci v appce, vlož popisek a zveřejni."}, nil
}

func (p Publisher) put(ctx context.Context, u string, body io.Reader, start, end, total int64, mime string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, u, body)
	if err != nil {
		return errors.New("bad upload URL")
	}
	req.ContentLength = end - start
	req.Header.Set("Content-Type", mime)
	req.Header.Set("Content-Range", "bytes "+strconv.FormatInt(start, 10)+"-"+strconv.FormatInt(end-1, 10)+"/"+strconv.FormatInt(total, 10))
	hc := p.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: 10 * time.Minute}
	}
	resp, err := hc.Do(req)
	if err != nil {
		return errors.New("upload failed") // the upload URL is a credential; keep it out of errors
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return nil
}
