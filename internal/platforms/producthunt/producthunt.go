// Package producthunt watches my Product Hunt launches (votes, comments) through the GraphQL
// API v2 with a developer token (<auth_ref>_TOKEN). Launching stays manual.
package producthunt

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/lioilsources/promohub/internal/collectors"
	"github.com/lioilsources/promohub/internal/config"
	"github.com/lioilsources/promohub/internal/creds"
	"github.com/lioilsources/promohub/internal/platform"
	"github.com/lioilsources/promohub/internal/platforms/httpx"
)

const (
	BaseURL      = "https://api.producthunt.com/v2/api/graphql"
	SecretSuffix = "TOKEN"
)

type Adapter struct {
	HTTP *http.Client
	Base string
}

func New(hc *http.Client) *Adapter { return &Adapter{HTTP: hc} }

func (a *Adapter) Platform() string { return "producthunt" }

func (a *Adapter) Ready(acct config.Account, _ string) error {
	_, err := creds.Static(acct, SecretSuffix)
	return err
}

const query = `query($u: String!) { user(username: $u) {
  followersCount
  madePosts(first: 20) { edges { node {
    id name slug url createdAt votesCount commentsCount
    comments(first: 20) { edges { node { id body createdAt url user { username } } } }
  } } }
} }`

type Response struct {
	Data struct {
		User *struct {
			FollowersCount *int64 `json:"followersCount"`
			MadePosts      struct {
				Edges []struct {
					Node Post `json:"node"`
				} `json:"edges"`
			} `json:"madePosts"`
		} `json:"user"`
	} `json:"data"`
	Errors []struct {
		Message string `json:"message"`
	} `json:"errors"`
}

type Post struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	URL           string `json:"url"`
	CreatedAt     string `json:"createdAt"`
	VotesCount    *int64 `json:"votesCount"`
	CommentsCount *int64 `json:"commentsCount"`
	Comments      struct {
		Edges []struct {
			Node struct {
				ID        string `json:"id"`
				Body      string `json:"body"`
				CreatedAt string `json:"createdAt"`
				URL       string `json:"url"`
				User      struct {
					Username string `json:"username"`
				} `json:"user"`
			} `json:"node"`
		} `json:"edges"`
	} `json:"comments"`
}

func (a *Adapter) Collect(ctx context.Context, r collectors.Request) (collectors.Batch, json.RawMessage, error) {
	tok, err := creds.Static(r.Account, SecretSuffix)
	if err != nil {
		return collectors.Batch{}, nil, err
	}
	base := a.Base
	if base == "" {
		base = BaseURL
	}
	c := &httpx.Client{Service: "producthunt", BaseURL: base, HTTP: a.HTTP, Secrets: []string{tok},
		Header: http.Header{"Authorization": {"Bearer " + tok}}}
	var res Response
	if err := c.Post(ctx, "", map[string]any{"query": query, "variables": map[string]string{"u": r.Account.Handle}}, &res); err != nil {
		return collectors.Batch{}, nil, err
	}
	if len(res.Errors) > 0 {
		return collectors.Batch{}, nil, fmt.Errorf("producthunt: %s", res.Errors[0].Message)
	}
	if res.Data.User == nil {
		return collectors.Batch{}, nil, errors.New("producthunt: user not found")
	}
	b := Map(r.Account.Handle, res)
	if r.Capability == platform.CapProfile {
		b.Inbound = nil
	} else {
		b.Snapshots = nil
	}
	return b, nil, nil
}

// Map turns the answer into launch snapshots, a follower snapshot and comments by others.
func Map(me string, res Response) collectors.Batch {
	var b collectors.Batch
	u := res.Data.User
	for _, e := range u.MadePosts.Edges {
		p := e.Node
		b.Snapshots = append(b.Snapshots, collectors.Snapshot{
			Platform: "producthunt",
			Content:  &collectors.Content{ExternalID: p.ID, URL: p.URL, Title: p.Name, PublishedAt: httpx.Time(p.CreatedAt)},
			Likes:    p.VotesCount, Comments: p.CommentsCount,
		})
		for _, ce := range p.Comments.Edges {
			c := ce.Node
			if c.ID == "" || c.User.Username == me {
				b.Skipped++
				continue
			}
			b.Inbound = append(b.Inbound, collectors.Inbound{
				Platform: "producthunt", Kind: "comment", ExternalID: c.ID, Author: c.User.Username,
				Text: c.Body, URL: c.URL, CreatedAt: httpx.Time(c.CreatedAt),
			})
		}
	}
	if u.FollowersCount != nil {
		b.Snapshots = append(b.Snapshots, collectors.Snapshot{Platform: "producthunt", Followers: u.FollowersCount})
	}
	return b
}
