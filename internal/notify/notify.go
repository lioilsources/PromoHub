// Package notify sends rule targets that do not go through IFTTT:
// Telegram (Bot API sendMessage) and Discord (incoming webhook).
package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// StatusError is a non-2xx answer from Telegram or Discord.
type StatusError struct {
	Service string
	Code    int
	Body    string
}

func (e *StatusError) Error() string {
	return fmt.Sprintf("%s: HTTP %d: %s", e.Service, e.Code, e.Body)
}

func (e *StatusError) Retryable() bool { return e.Code == http.StatusTooManyRequests || e.Code >= 500 }

var httpClient = &http.Client{Timeout: 20 * time.Second}

func postJSON(ctx context.Context, service, u, secret string, body any) error {
	b, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(b))
	if err != nil {
		return redact(service, err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := httpClient.Do(req)
	if err != nil {
		return redact(service, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		rb, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		msg := strings.TrimSpace(string(rb))
		if secret != "" {
			msg = strings.ReplaceAll(msg, secret, "***")
		}
		return &StatusError{Service: service, Code: resp.StatusCode, Body: msg}
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	return nil
}

// redact drops the URL from transport errors: both bot tokens and webhook URLs are secrets.
func redact(service string, err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		return fmt.Errorf("%s: %s: %w", service, ue.Op, ue.Err)
	}
	return err
}

type Telegram struct {
	Token   string
	BaseURL string
}

func (t *Telegram) Send(ctx context.Context, chatID, text string) error {
	if t.Token == "" {
		return errors.New("telegram: TELEGRAM_BOT_TOKEN is not set")
	}
	base := t.BaseURL
	if base == "" {
		base = "https://api.telegram.org"
	}
	text = truncate(text, 4000)
	return postJSON(ctx, "telegram", base+"/bot"+t.Token+"/sendMessage", t.Token, map[string]any{
		"chat_id":                  chatID,
		"text":                     text,
		"disable_web_page_preview": true,
	})
}

// Discord posts to an incoming webhook URL.
func Discord(ctx context.Context, webhookURL, text string) error {
	text = truncate(text, 1900)
	return postJSON(ctx, "discord", webhookURL, "", map[string]any{
		"content":          text,
		"allowed_mentions": map[string]any{"parse": []string{}}, // scraped text must not ping anyone
	})
}

// truncate cuts at a rune boundary so multi-byte text (Czech diacritics) stays valid UTF-8.
func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
