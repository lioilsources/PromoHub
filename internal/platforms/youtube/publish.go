package youtube

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/lioilsources/promohub/internal/config"
	"github.com/lioilsources/promohub/internal/creds"
	"github.com/lioilsources/promohub/internal/platforms/httpx"
	"github.com/lioilsources/promohub/internal/publish"
)

// Publisher uploads with videos.insert (resumable). Only for publish: api, i.e. after the
// API audit: uploads from an unaudited project are locked private.
type Publisher struct{ *Adapter }

func (p Publisher) Ready(acct config.Account) error {
	_, err := creds.OAuthToken(context.Background(), p.Tokens, acct)
	return err
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
		return publish.Result{}, publish.Reject(errors.New("youtube needs a video"))
	}
	t, err := creds.OAuthToken(ctx, p.Tokens, acct)
	if err != nil {
		return publish.Result{}, publish.Reject(err)
	}
	base := p.UploadBase
	if base == "" {
		base = "https://www.googleapis.com/upload/youtube/v3"
	}
	title := post.Title
	if title == "" {
		title, _, _ = strings.Cut(post.Text, "\n")
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
	c := &httpx.Client{Service: "youtube-upload", BaseURL: base, HTTP: p.HTTP, Secrets: []string{t.AccessToken}, Header: http.Header{
		"Authorization":           {"Bearer " + t.AccessToken},
		"X-Upload-Content-Type":   {vid.Mime},
		"X-Upload-Content-Length": {strconv.FormatInt(st.Size(), 10)},
	}}
	meta := map[string]any{
		"snippet": map[string]any{"title": title, "description": post.Text, "categoryId": "28", "defaultLanguage": post.Lang},
		"status":  map[string]any{"privacyStatus": "public"},
	}
	loc, err := p.start(ctx, c, meta)
	if err != nil {
		return publish.Result{}, publish.Reject(err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, loc, f)
	if err != nil {
		return publish.Result{}, publish.Reject(errors.New("bad upload session URL"))
	}
	req.ContentLength = st.Size()
	req.Header.Set("Content-Type", vid.Mime)
	hc := p.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: 30 * time.Minute}
	}
	resp, err := hc.Do(req)
	if err != nil {
		return publish.Result{}, errors.New("youtube: upload interrupted") // may or may not have finished
	}
	defer resp.Body.Close()
	var v struct {
		ID string `json:"id"`
	}
	if resp.StatusCode/100 != 2 {
		return publish.Result{}, &httpx.StatusError{Service: "youtube-upload", Code: resp.StatusCode}
	}
	if err := json.NewDecoder(resp.Body).Decode(&v); err != nil || v.ID == "" {
		return publish.Result{}, fmt.Errorf("youtube: upload answer without video ID")
	}
	return publish.Result{ExternalID: v.ID, URL: "https://www.youtube.com/watch?v=" + v.ID}, nil
}

// start opens a resumable session and returns its upload URL (Location).
func (p Publisher) start(ctx context.Context, c *httpx.Client, meta map[string]any) (string, error) {
	var loc string
	hc := c.HTTP
	if hc == nil {
		hc = httpx.DefaultHTTP
	}
	cc := *c
	cc.HTTP = &http.Client{Timeout: hc.Timeout, Transport: locTransport{next: hc.Transport, loc: &loc}}
	if err := cc.Post(ctx, "/videos?"+url.Values{"uploadType": {"resumable"}, "part": {"snippet,status"}}.Encode(), meta, nil); err != nil {
		return "", err
	}
	if loc == "" {
		return "", errors.New("youtube: no resumable upload URL")
	}
	return loc, nil
}

// locTransport captures the Location header of the session-start answer.
type locTransport struct {
	next http.RoundTripper
	loc  *string
}

func (t locTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	next := t.next
	if next == nil {
		next = http.DefaultTransport
	}
	resp, err := next.RoundTrip(r)
	if err == nil {
		*t.loc = resp.Header.Get("Location")
	}
	return resp, err
}
