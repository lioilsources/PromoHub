package discord

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/lioilsources/promohub/internal/config"
	"github.com/lioilsources/promohub/internal/creds"
	"github.com/lioilsources/promohub/internal/platforms/httpx"
	"github.com/lioilsources/promohub/internal/publish"
)

// WebhookSuffix: <auth_ref>_WEBHOOK holds the announcement channel's webhook URL.
const WebhookSuffix = "WEBHOOK"

// Publisher posts to the announcement channel through its webhook.
type Publisher struct{ *Adapter }

func (p Publisher) Ready(acct config.Account) error {
	_, err := creds.Static(acct, WebhookSuffix)
	return err
}

func (p Publisher) Publish(ctx context.Context, acct config.Account, post publish.Post) (publish.Result, error) {
	hook, err := creds.Static(acct, WebhookSuffix)
	if err != nil {
		return publish.Result{}, publish.Reject(err)
	}
	u, err := url.Parse(hook)
	if err != nil || !strings.HasPrefix(u.Path, "/api/webhooks/") {
		return publish.Result{}, publish.Reject(fmt.Errorf("discord: %s is not a webhook URL", creds.Name(acct, WebhookSuffix)))
	}
	base := u.Scheme + "://" + u.Host
	if p.Base != "" {
		base = p.Base
	}
	c := &httpx.Client{Service: "discord", BaseURL: base, HTTP: p.HTTP, Secrets: []string{hook, u.Path}}
	payload := map[string]any{"content": post.Text, "allowed_mentions": map[string]any{"parse": []string{}}}
	var body any = payload
	for _, m := range post.Media {
		if !m.IsImage() {
			continue
		}
		mp, ctype, err := multipartBody(payload, m)
		if err != nil {
			return publish.Result{}, publish.Reject(err)
		}
		body = mp
		c.Header = http.Header{"Content-Type": {ctype}}
		break
	}
	var msg struct {
		ID        string `json:"id"`
		ChannelID string `json:"channel_id"`
		GuildID   string `json:"guild_id"`
	}
	if err := c.Do(ctx, http.MethodPost, u.Path, url.Values{"wait": {"true"}}, body, &msg); err != nil {
		return publish.Result{}, err
	}
	link := ""
	if msg.GuildID != "" {
		link = "https://discord.com/channels/" + msg.GuildID + "/" + msg.ChannelID + "/" + msg.ID
	}
	return publish.Result{ExternalID: msg.ID, URL: link}, nil
}

func multipartBody(payload map[string]any, m publish.Media) (io.Reader, string, error) {
	f, err := os.Open(m.Path)
	if err != nil {
		return nil, "", err
	}
	defer f.Close()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	pj, _ := json.Marshal(payload)
	_ = w.WriteField("payload_json", string(pj))
	part, err := w.CreateFormFile("files[0]", filepath.Base(m.Path))
	if err != nil {
		return nil, "", err
	}
	if _, err := io.Copy(part, f); err != nil {
		return nil, "", err
	}
	w.Close()
	return &buf, w.FormDataContentType(), nil
}
