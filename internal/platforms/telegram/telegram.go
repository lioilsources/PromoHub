// Package telegram reads the subscriber count of my public Telegram channel through the hub's
// bot (TELEGRAM_BOT_TOKEN), which must be an admin of the channel.
package telegram

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/lioilsources/promohub/internal/collectors"
	"github.com/lioilsources/promohub/internal/config"
	"github.com/lioilsources/promohub/internal/creds"
	"github.com/lioilsources/promohub/internal/platform"
	"github.com/lioilsources/promohub/internal/platforms/httpx"
)

const BaseURL = "https://api.telegram.org"

type Adapter struct {
	HTTP *http.Client
	Base string
}

func New(hc *http.Client) *Adapter { return &Adapter{HTTP: hc} }

func (a *Adapter) Platform() string { return "telegram" }

func token() (string, error) {
	if t := strings.TrimSpace(creds.Getenv("TELEGRAM_BOT_TOKEN")); t != "" {
		return t, nil
	}
	return "", &creds.MissingError{Var: "TELEGRAM_BOT_TOKEN"}
}

func (a *Adapter) Ready(config.Account, string) error { _, err := token(); return err }

// ChatID is the channel's chat_id: external_id (numeric or @name) or @handle.
func ChatID(acct config.Account) string {
	if acct.ExternalID != "" {
		return acct.ExternalID
	}
	return "@" + strings.TrimPrefix(acct.Handle, "@")
}

func (a *Adapter) Collect(ctx context.Context, r collectors.Request) (collectors.Batch, json.RawMessage, error) {
	if r.Capability != platform.CapProfile {
		return collectors.Batch{}, nil, fmt.Errorf("telegram: unknown capability %q", r.Capability)
	}
	tok, err := token()
	if err != nil {
		return collectors.Batch{}, nil, err
	}
	base := a.Base
	if base == "" {
		base = BaseURL
	}
	c := &httpx.Client{Service: "telegram", BaseURL: base + "/bot" + tok, HTTP: a.HTTP, Secrets: []string{tok}}
	var res struct {
		OK     bool   `json:"ok"`
		Result int64  `json:"result"`
		Desc   string `json:"description"`
	}
	if err := c.Get(ctx, "/getChatMemberCount", url.Values{"chat_id": {ChatID(r.Account)}}, &res); err != nil {
		return collectors.Batch{}, nil, err
	}
	if !res.OK {
		return collectors.Batch{}, nil, fmt.Errorf("telegram: %s", res.Desc)
	}
	n := res.Result
	return collectors.Batch{Snapshots: []collectors.Snapshot{{Platform: "telegram", Followers: &n}}}, nil, nil
}
