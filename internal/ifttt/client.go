// Package ifttt talks to the IFTTT Webhooks service in both directions:
// Client triggers applets, Inbound parses calls that applets make to the hub.
package ifttt

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

const DefaultBaseURL = "https://maker.ifttt.com"

// Client triggers Webhooks events with an arbitrary JSON body
// (POST /trigger/{event}/json/with/key/{key}); applets read it as JsonPayload.
type Client struct {
	BaseURL string
	Key     string
	HTTP    *http.Client
}

func NewClient(key string) *Client {
	return &Client{BaseURL: DefaultBaseURL, Key: key, HTTP: &http.Client{Timeout: 20 * time.Second}}
}

// StatusError is a non-2xx answer. Retryable tells the outbox whether to try again.
type StatusError struct {
	Code int
	Body string
}

func (e *StatusError) Error() string { return fmt.Sprintf("ifttt: HTTP %d: %s", e.Code, e.Body) }

func (e *StatusError) Retryable() bool { return e.Code == http.StatusTooManyRequests || e.Code >= 500 }

func (c *Client) Trigger(ctx context.Context, event string, payload any) error {
	if c.Key == "" {
		return errors.New("ifttt: IFTTT_KEY is not set")
	}
	b, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	u := c.BaseURL + "/trigger/" + url.PathEscape(event) + "/json/with/key/" + url.PathEscape(c.Key)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(b))
	if err != nil {
		return redact(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return redact(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return &StatusError{Code: resp.StatusCode, Body: strings.ReplaceAll(strings.TrimSpace(string(body)), c.Key, "***")}
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	return nil
}

// redact drops the URL from transport errors: the Webhooks key is part of the path.
func redact(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		return fmt.Errorf("ifttt: %s: %w", ue.Op, ue.Err)
	}
	return err
}
