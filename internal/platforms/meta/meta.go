// Package meta holds what the Instagram, Threads and Facebook adapters share: a Graph API
// client that sends the OAuth token, form posts and container polling.
package meta

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/lioilsources/promohub/internal/config"
	"github.com/lioilsources/promohub/internal/creds"
	"github.com/lioilsources/promohub/internal/platforms/httpx"
)

// Version of the Graph APIs (graph.facebook.com, graph.instagram.com).
const Version = "v24.0"

// Graph is one authenticated Graph API host.
type Graph struct {
	c     *httpx.Client
	token string
}

// New returns a Graph client for base (e.g. https://graph.instagram.com/v24.0) and token.
func New(service, base string, hc *http.Client, token string) *Graph {
	return &Graph{c: &httpx.Client{Service: service, BaseURL: base, HTTP: hc, Secrets: []string{token}}, token: token}
}

// Token fetches the account's OAuth token or returns creds.NotConnected.
func Token(ctx context.Context, ts creds.TokenSource, acct config.Account) (creds.Token, error) {
	return creds.OAuthToken(ctx, ts, acct)
}

func (g *Graph) Get(ctx context.Context, path string, q url.Values, out any) error {
	if q == nil {
		q = url.Values{}
	}
	q.Set("access_token", g.token)
	return g.c.Get(ctx, path, q, out)
}

// Post sends form values (the Graph APIs take forms, not JSON).
func (g *Graph) Post(ctx context.Context, path string, form url.Values, out any) error {
	form.Set("access_token", g.token)
	c := *g.c
	c.Header = http.Header{"Content-Type": {"application/x-www-form-urlencoded"}}
	return c.Do(ctx, http.MethodPost, path, nil, strings.NewReader(form.Encode()), out)
}

// Wait polls a media container until field reports one of done, or fails on ERROR/EXPIRED.
func (g *Graph) Wait(ctx context.Context, id, field string, every time.Duration, tries int) error {
	for i := 0; i < tries; i++ {
		var st map[string]any
		if err := g.Get(ctx, "/"+id, url.Values{"fields": {field + ",error_message"}}, &st); err != nil {
			return err
		}
		switch s, _ := st[field].(string); s {
		case "FINISHED", "PUBLISHED":
			return nil
		case "ERROR", "EXPIRED":
			msg, _ := st["error_message"].(string)
			return fmt.Errorf("container %s: %s %s", id, s, msg)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(every):
		}
	}
	return fmt.Errorf("container %s still processing", id)
}

// Insights is the data[] answer of /{id}/insights.
type Insights struct {
	Data []struct {
		Name   string `json:"name"`
		Values []struct {
			Value int64 `json:"value"`
		} `json:"values"`
		TotalValue *struct {
			Value int64 `json:"value"`
		} `json:"total_value"`
	} `json:"data"`
}

// Map returns metric name → value.
func (in Insights) Map() map[string]int64 {
	out := map[string]int64{}
	for _, d := range in.Data {
		switch {
		case d.TotalValue != nil:
			out[d.Name] = d.TotalValue.Value
		case len(d.Values) > 0:
			out[d.Name] = d.Values[0].Value
		}
	}
	return out
}

// Ptr returns a pointer to m[k] when present.
func Ptr(m map[string]int64, k string) *int64 {
	if v, ok := m[k]; ok {
		return &v
	}
	return nil
}
