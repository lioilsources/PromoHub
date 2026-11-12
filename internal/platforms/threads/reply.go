package threads

import (
	"context"
	"net/url"

	"github.com/lioilsources/promohub/internal/config"
	"github.com/lioilsources/promohub/internal/publish"
)

// Reply answers a reply or mention (reply_to_id) with a text post.
func (p Publisher) Reply(ctx context.Context, acct config.Account, to publish.InboundRef, text string) (publish.Result, error) {
	g, err := p.graph(ctx, acct)
	if err != nil {
		return publish.Result{}, publish.Reject(err)
	}
	var c struct {
		ID string `json:"id"`
	}
	if err := g.Post(ctx, "/me/threads", url.Values{"media_type": {"TEXT"}, "text": {text}, "reply_to_id": {to.ExternalID}}, &c); err != nil {
		return publish.Result{}, publish.Reject(err)
	}
	var pub struct {
		ID string `json:"id"`
	}
	if err := g.Post(ctx, "/me/threads_publish", url.Values{"creation_id": {c.ID}}, &pub); err != nil {
		return publish.Result{}, err
	}
	var got Post
	_ = g.Get(ctx, "/"+pub.ID, url.Values{"fields": {"permalink,shortcode"}}, &got)
	return publish.Result{ExternalID: pub.ID, URL: got.Permalink}, nil
}
