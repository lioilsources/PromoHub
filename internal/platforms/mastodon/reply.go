package mastodon

import (
	"context"
	"errors"
	"strings"

	"github.com/lioilsources/promohub/internal/config"
	"github.com/lioilsources/promohub/internal/publish"
)

// Reply answers a status seen on my instance (inbound IDs are "<instance>/<status id>").
func (p Publisher) Reply(ctx context.Context, acct config.Account, to publish.InboundRef, text string) (publish.Result, error) {
	host, id, ok := strings.Cut(to.ExternalID, "/")
	if !ok || host != acct.APIHost() {
		return publish.Result{}, publish.Reject(errors.New("mastodon: the status is not from this account's instance"))
	}
	if to.Author != "" && !strings.Contains(text, "@"+to.Author) {
		text = "@" + to.Author + " " + text // Mastodon notifies only mentioned accounts
	}
	c := p.client(acct)
	var st Status
	if err := c.Post(ctx, "/api/v1/statuses", map[string]any{"status": text, "in_reply_to_id": id, "visibility": "public"}, &st); err != nil {
		return publish.Result{}, err
	}
	return publish.Result{ExternalID: st.ID, URL: st.URL}, nil
}
