package bluesky

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/lioilsources/promohub/internal/config"
	"github.com/lioilsources/promohub/internal/platforms/httpx"
	"github.com/lioilsources/promohub/internal/publish"
)

const maxBlob = 1_000_000 // bytes; the PDS rejects larger images

// PublishReady: publishing always needs the app password.
func (a *Adapter) PublishReady(acct config.Account) error { return a.Ready(acct, "inbound") }

// Publisher adapts the Bluesky adapter to publish.Publisher.
type Publisher struct{ *Adapter }

func (p Publisher) Ready(acct config.Account) error { return p.PublishReady(acct) }

func (p Publisher) Publish(ctx context.Context, acct config.Account, post publish.Post) (publish.Result, error) {
	return p.Adapter.Publish(ctx, acct, post)
}

// Publish creates an app.bsky.feed.post record: text with link and hashtag facets, and
// either an images embed or a link card for post.Extra["link"].
func (a *Adapter) Publish(ctx context.Context, acct config.Account, post publish.Post) (publish.Result, error) {
	c, s, err := a.authed(ctx, acct)
	if err != nil {
		return publish.Result{}, publish.Reject(err)
	}
	now := time.Now
	if a.Now != nil {
		now = a.Now
	}
	rec := map[string]any{
		"$type":     "app.bsky.feed.post",
		"text":      post.Text,
		"createdAt": now().UTC().Format("2006-01-02T15:04:05.000Z"),
	}
	if post.Lang != "" {
		rec["langs"] = []string{post.Lang}
	}
	if f := Facets(post.Text); len(f) > 0 {
		rec["facets"] = f
	}
	var images []map[string]any
	for _, m := range post.Media {
		if !m.IsImage() || len(images) == 4 {
			continue
		}
		blob, err := a.uploadBlob(ctx, c, m.Path, m.Mime)
		if err != nil {
			return publish.Result{}, publish.Reject(fmt.Errorf("upload %s: %w", m.Path, err))
		}
		img := map[string]any{"alt": m.Alt, "image": blob}
		if m.Width > 0 && m.Height > 0 {
			img["aspectRatio"] = map[string]int{"width": m.Width, "height": m.Height}
		}
		images = append(images, img)
	}
	link := post.Extra["link"]
	switch {
	case len(images) > 0:
		rec["embed"] = map[string]any{"$type": "app.bsky.embed.images", "images": images}
	case link != "":
		rec["embed"] = a.linkCard(ctx, c, link)
	}
	var out struct {
		URI string `json:"uri"`
	}
	err = c.Post(ctx, "/xrpc/com.atproto.repo.createRecord", map[string]any{
		"repo": s.Did, "collection": "app.bsky.feed.post", "record": rec,
	}, &out)
	if err != nil {
		return publish.Result{}, err
	}
	rkey := RKey(out.URI)
	return publish.Result{ExternalID: rkey, URL: PostURL(s.Handle, rkey)}, nil
}

func (a *Adapter) uploadBlob(ctx context.Context, c *httpx.Client, path, mime string) (json.RawMessage, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(b) > maxBlob {
		return nil, fmt.Errorf("image is %d bytes, Bluesky takes at most %d", len(b), maxBlob)
	}
	up := *c
	up.Header = c.Header.Clone()
	up.Header.Set("Content-Type", mime)
	var res struct {
		Blob json.RawMessage `json:"blob"`
	}
	if err := up.Do(ctx, http.MethodPost, "/xrpc/com.atproto.repo.uploadBlob", nil, bytes.NewReader(b), &res); err != nil {
		return nil, err
	}
	return res.Blob, nil
}

// linkCard builds an app.bsky.embed.external from the page's Open Graph tags. Fetch problems
// only make the card plainer; they never block the post.
func (a *Adapter) linkCard(ctx context.Context, c *httpx.Client, link string) map[string]any {
	ext := map[string]any{"uri": link, "title": link, "description": ""}
	og := fetchOG(ctx, a.HTTP, link)
	if og.title != "" {
		ext["title"] = og.title
	}
	ext["description"] = og.description
	if og.image != "" {
		if blob, err := a.uploadRemote(ctx, c, og.image); err == nil {
			ext["thumb"] = blob
		}
	}
	return map[string]any{"$type": "app.bsky.embed.external", "external": ext}
}

func (a *Adapter) uploadRemote(ctx context.Context, c *httpx.Client, u string) (json.RawMessage, error) {
	b, mime, err := get(ctx, a.HTTP, u, maxBlob)
	if err != nil {
		return nil, err
	}
	if !strings.HasPrefix(mime, "image/") {
		return nil, fmt.Errorf("thumb is %s", mime)
	}
	tmp, err := os.CreateTemp("", "og-*")
	if err != nil {
		return nil, err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return nil, err
	}
	tmp.Close()
	return a.uploadBlob(ctx, c, tmp.Name(), mime)
}

type ogTags struct{ title, description, image string }

var reMeta = regexp.MustCompile(`(?is)<meta\s+[^>]*>`)
var reAttr = regexp.MustCompile(`(?is)(property|name|content)\s*=\s*("([^"]*)"|'([^']*)')`)

func fetchOG(ctx context.Context, hc *http.Client, u string) ogTags {
	b, mime, err := get(ctx, hc, u, 1<<20)
	if err != nil || !strings.Contains(mime, "html") {
		return ogTags{}
	}
	return parseOG(string(b))
}

func parseOG(page string) ogTags {
	var og ogTags
	for _, tag := range reMeta.FindAllString(page, 200) {
		var key, content string
		for _, m := range reAttr.FindAllStringSubmatch(tag, -1) {
			v := m[3] + m[4]
			if strings.EqualFold(m[1], "content") {
				content = html.UnescapeString(v)
			} else {
				key = strings.ToLower(v)
			}
		}
		switch key {
		case "og:title":
			og.title = content
		case "og:description":
			og.description = content
		case "description":
			if og.description == "" {
				og.description = content
			}
		case "og:image":
			og.image = content
		}
	}
	return og
}

func get(ctx context.Context, hc *http.Client, u string, limit int64) ([]byte, string, error) {
	if hc == nil {
		hc = httpx.DefaultHTTP
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("User-Agent", "promohub (+link card)")
	resp, err := hc.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return nil, "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if int64(len(b)) > limit {
		return nil, "", fmt.Errorf("larger than %d bytes", limit)
	}
	return b, resp.Header.Get("Content-Type"), err
}

var reTag = regexp.MustCompile(`(^|\s)(#[\p{L}\p{N}_]*[\p{L}_][\p{L}\p{N}_]*)`)

// Facets marks links and hashtags with UTF-8 byte offsets, as the lexicon requires.
func Facets(text string) []map[string]any {
	var out []map[string]any
	for _, m := range reURLFacet.FindAllStringIndex(text, -1) {
		uri := strings.TrimRight(text[m[0]:m[1]], ".,;:!?)")
		end := m[0] + len(uri)
		out = append(out, facet(m[0], end, map[string]any{"$type": "app.bsky.richtext.facet#link", "uri": uri}))
	}
	for _, m := range reTag.FindAllStringSubmatchIndex(text, -1) {
		start, end := m[4], m[5]
		tag := text[start+1 : end]
		if len(tag) > 64 {
			continue
		}
		out = append(out, facet(start, end, map[string]any{"$type": "app.bsky.richtext.facet#tag", "tag": tag}))
	}
	return out
}

var reURLFacet = regexp.MustCompile(`https?://[^\s<>"]+`)

func facet(start, end int, feature map[string]any) map[string]any {
	return map[string]any{
		"index":    map[string]int{"byteStart": start, "byteEnd": end},
		"features": []map[string]any{feature},
	}
}
