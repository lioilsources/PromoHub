// Package discord reads my server's member counts from a permanent invite (external_id is the
// invite code). GET /invites/{code}?with_counts=true needs no auth.
package discord

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"

	"github.com/lioilsources/promohub/internal/collectors"
	"github.com/lioilsources/promohub/internal/config"
	"github.com/lioilsources/promohub/internal/platform"
	"github.com/lioilsources/promohub/internal/platforms/httpx"
)

const BaseURL = "https://discord.com/api/v10"

type Adapter struct {
	HTTP *http.Client
	Base string
}

func New(hc *http.Client) *Adapter { return &Adapter{HTTP: hc} }

func (a *Adapter) Platform() string { return "discord" }

func (a *Adapter) Ready(acct config.Account, _ string) error {
	if acct.ExternalID == "" {
		return errors.New("discord: external_id must be a permanent invite code")
	}
	return nil
}

type Invite struct {
	ApproximateMemberCount   *int64 `json:"approximate_member_count"`
	ApproximatePresenceCount *int64 `json:"approximate_presence_count"`
}

func (a *Adapter) Collect(ctx context.Context, r collectors.Request) (collectors.Batch, json.RawMessage, error) {
	if r.Capability != platform.CapProfile {
		return collectors.Batch{}, nil, fmt.Errorf("discord: unknown capability %q", r.Capability)
	}
	if err := a.Ready(r.Account, r.Capability); err != nil {
		return collectors.Batch{}, nil, err
	}
	base := a.Base
	if base == "" {
		base = BaseURL
	}
	c := &httpx.Client{Service: "discord", BaseURL: base, HTTP: a.HTTP}
	var inv Invite
	if err := c.Get(ctx, "/invites/"+url.PathEscape(r.Account.ExternalID), url.Values{"with_counts": {"true"}}, &inv); err != nil {
		return collectors.Batch{}, nil, err
	}
	s := collectors.Snapshot{Platform: "discord", Followers: inv.ApproximateMemberCount, Extra: map[string]int64{}}
	if inv.ApproximatePresenceCount != nil {
		s.Extra["online"] = *inv.ApproximatePresenceCount
	}
	return collectors.Batch{Snapshots: []collectors.Snapshot{s}}, nil, nil
}
