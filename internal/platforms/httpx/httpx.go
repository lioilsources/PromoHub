// Package httpx is the small JSON-over-HTTP client the platform adapters share: base URL for
// tests, secret redaction in errors and Retry-After handling.
package httpx

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/lioilsources/promohub/internal/collectors"
)

// DefaultHTTP is shared by adapters that are not given a client.
var DefaultHTTP = &http.Client{Timeout: 30 * time.Second}

// Client calls one API host. Secrets lists values that must never appear in an error.
type Client struct {
	Service string
	BaseURL string
	HTTP    *http.Client
	Header  http.Header
	Secrets []string
	Now     func() time.Time
}

// StatusError is a non-2xx answer.
type StatusError struct {
	Service string
	Code    int
	Body    string
}

func (e *StatusError) Error() string {
	return fmt.Sprintf("%s: HTTP %d: %s", e.Service, e.Code, e.Body)
}

func (e *StatusError) Retryable() bool { return e.Code == http.StatusTooManyRequests || e.Code >= 500 }

// Code returns the HTTP status of err, or 0.
func Code(err error) int {
	var se *StatusError
	if errors.As(err, &se) {
		return se.Code
	}
	return 0
}

// Get decodes the JSON answer of GET path?q into out.
func (c *Client) Get(ctx context.Context, path string, q url.Values, out any) error {
	return c.Do(ctx, http.MethodGet, path, q, nil, out)
}

// Post sends body as JSON and decodes the answer into out (out may be nil).
func (c *Client) Post(ctx context.Context, path string, body, out any) error {
	return c.Do(ctx, http.MethodPost, path, nil, body, out)
}

// Do runs one request. body is JSON-encoded unless it is an io.Reader (sent as is).
func (c *Client) Do(ctx context.Context, method, path string, q url.Values, body, out any) error {
	u := c.BaseURL + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	var rd io.Reader
	ctype := ""
	switch b := body.(type) {
	case nil:
	case io.Reader:
		rd = b
	default:
		buf, err := json.Marshal(b)
		if err != nil {
			return err
		}
		rd, ctype = bytes.NewReader(buf), "application/json"
	}
	req, err := http.NewRequestWithContext(ctx, method, u, rd)
	if err != nil {
		return c.redact(err)
	}
	for k, vs := range c.Header {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	if ctype != "" && req.Header.Get("Content-Type") == "" {
		req.Header.Set("Content-Type", ctype)
	}
	if req.Header.Get("Accept") == "" {
		req.Header.Set("Accept", "application/json")
	}
	hc := c.HTTP
	if hc == nil {
		hc = DefaultHTTP
	}
	resp, err := hc.Do(req)
	if err != nil {
		return c.redact(err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return c.redact(err)
	}
	if resp.StatusCode/100 != 2 {
		se := &StatusError{Service: c.Service, Code: resp.StatusCode, Body: c.scrub(strings.TrimSpace(string(raw[:min(len(raw), 512)])))}
		if resp.StatusCode == http.StatusTooManyRequests {
			return &collectors.RateLimited{Until: c.retryAt(resp.Header), Err: se}
		}
		return se
	}
	if out == nil || len(raw) == 0 {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("%s: decode %s: %w", c.Service, path, err)
	}
	return nil
}

// retryAt reads Retry-After (seconds or HTTP date) and the common x-ratelimit-reset headers.
func (c *Client) retryAt(h http.Header) time.Time {
	now := time.Now
	if c.Now != nil {
		now = c.Now
	}
	if s := h.Get("Retry-After"); s != "" {
		if n, err := strconv.Atoi(s); err == nil {
			return now().Add(time.Duration(n) * time.Second)
		}
		if t, err := http.ParseTime(s); err == nil {
			return t
		}
	}
	for _, k := range []string{"X-Ratelimit-Reset", "Ratelimit-Reset"} {
		if s := h.Get(k); s != "" {
			if n, err := strconv.ParseInt(s, 10, 64); err == nil {
				if n > 1e9 {
					return time.Unix(n, 0)
				}
				return now().Add(time.Duration(n) * time.Second)
			}
			if t, err := time.Parse(time.RFC3339, s); err == nil {
				return t
			}
		}
	}
	return now().Add(15 * time.Minute)
}

func (c *Client) scrub(s string) string {
	for _, x := range c.Secrets {
		if x != "" {
			s = strings.ReplaceAll(s, x, "***")
		}
	}
	return s
}

// redact drops the URL from transport errors: some APIs (YouTube) take the key in the query.
func (c *Client) redact(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		return fmt.Errorf("%s: %s: %s", c.Service, ue.Op, c.scrub(ue.Err.Error()))
	}
	return fmt.Errorf("%s: %s", c.Service, c.scrub(err.Error()))
}

// Time parses RFC 3339 timestamps; empty or invalid input gives nil.
func Time(s string) *time.Time {
	if s == "" {
		return nil
	}
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return nil
	}
	t = t.UTC()
	return &t
}
