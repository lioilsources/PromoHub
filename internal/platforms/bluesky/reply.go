package bluesky

import (
	"context"
	"errors"
	"net/url"
	"time"

	"github.com/lioilsources/promohub/internal/config"
	"github.com/lioilsources/promohub/internal/publish"
)

type strongRef struct {
	URI string `json:"uri"`
	CID string `json:"cid"`
}

// Reply posts an answer in the thread of to.ExternalID (an at:// URI).
func (p Publisher) Reply(ctx context.Context, acct config.Account, to publish.InboundRef, text string) (publish.Result, error) {
	c, s, err := p.authed(ctx, acct)
	if err != nil {
		return publish.Result{}, publish.Reject(err)
	}
	var res struct {
		Posts []struct {
			URI    string `json:"uri"`
			CID    string `json:"cid"`
			Record struct {
				Reply *struct {
					Root strongRef `json:"root"`
				} `json:"reply"`
			} `json:"record"`
		} `json:"posts"`
	}
	if err := c.Get(ctx, "/xrpc/app.bsky.feed.getPosts", url.Values{"uris": {to.ExternalID}}, &res); err != nil {
		return publish.Result{}, publish.Reject(err)
	}
	if len(res.Posts) == 0 {
		return publish.Result{}, publish.Reject(errors.New("bluesky: the post no longer exists"))
	}
	parent := strongRef{URI: res.Posts[0].URI, CID: res.Posts[0].CID}
	root := parent
	if r := res.Posts[0].Record.Reply; r != nil && r.Root.URI != "" {
		root = r.Root
	}
	now := time.Now
	if p.Now != nil {
		now = p.Now
	}
	rec := map[string]any{
		"$type": "app.bsky.feed.post", "text": text, "createdAt": now().UTC().Format("2006-01-02T15:04:05.000Z"),
		"reply": map[string]any{"root": root, "parent": parent},
	}
	if f := Facets(text); len(f) > 0 {
		rec["facets"] = f
	}
	var out struct {
		URI string `json:"uri"`
	}
	if err := c.Post(ctx, "/xrpc/com.atproto.repo.createRecord", map[string]any{"repo": s.Did, "collection": "app.bsky.feed.post", "record": rec}, &out); err != nil {
		return publish.Result{}, err
	}
	return publish.Result{ExternalID: RKey(out.URI), URL: PostURL(s.Handle, RKey(out.URI))}, nil
}
