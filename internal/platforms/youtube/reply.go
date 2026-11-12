package youtube

import (
	"context"
	"net/http"
	"net/url"

	"github.com/lioilsources/promohub/internal/config"
	"github.com/lioilsources/promohub/internal/creds"
	"github.com/lioilsources/promohub/internal/platforms/httpx"
	"github.com/lioilsources/promohub/internal/publish"
)

// Reply answers a top-level comment (comments.insert, needs the youtube.force-ssl OAuth scope).
func (p Publisher) Reply(ctx context.Context, acct config.Account, to publish.InboundRef, text string) (publish.Result, error) {
	t, err := creds.OAuthToken(ctx, p.Tokens, acct)
	if err != nil {
		return publish.Result{}, publish.Reject(err)
	}
	base := p.Base
	if base == "" {
		base = BaseURL
	}
	c := &httpx.Client{Service: "youtube", BaseURL: base, HTTP: p.HTTP, Secrets: []string{t.AccessToken},
		Header: http.Header{"Authorization": {"Bearer " + t.AccessToken}}}
	var res struct {
		ID string `json:"id"`
	}
	body := map[string]any{"snippet": map[string]any{"parentId": to.ExternalID, "textOriginal": text}}
	if err := c.Post(ctx, "/comments?"+url.Values{"part": {"snippet"}}.Encode(), body, &res); err != nil {
		return publish.Result{}, err
	}
	return publish.Result{ExternalID: res.ID, URL: to.URL}, nil
}
