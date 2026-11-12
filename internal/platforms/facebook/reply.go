package facebook

import (
	"context"
	"net/url"

	"github.com/lioilsources/promohub/internal/config"
	"github.com/lioilsources/promohub/internal/publish"
)

// Reply answers a comment on a page post, as the page.
func (p Publisher) Reply(ctx context.Context, acct config.Account, to publish.InboundRef, text string) (publish.Result, error) {
	g, _, err := p.graph(ctx, acct)
	if err != nil {
		return publish.Result{}, publish.Reject(err)
	}
	var res struct {
		ID string `json:"id"`
	}
	if err := g.Post(ctx, "/"+to.ExternalID+"/comments", url.Values{"message": {text}}, &res); err != nil {
		return publish.Result{}, err
	}
	return publish.Result{ExternalID: res.ID, URL: to.URL}, nil
}
