// Package sparkbroker is a client of the spark-broker queue that owns the SPARK GPU. The hub
// never starts anything on SPARK itself: it asks for a lease, waits until the broker reports
// the environment running, heartbeats while it works and reports done (or fail) at the end.
// The API is the one sparkctl uses (AiStack services/spark-broker).
package sparkbroker

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/lioilsources/promohub/internal/platforms/httpx"
)

// Job states the hub cares about.
const (
	Queued    = "queued"
	Running   = "running"
	Done      = "done"
	Failed    = "failed"
	Cancelled = "cancelled"
	Orphaned  = "orphaned"
)

type Client struct {
	Base  string // SPARK_BROKER, e.g. http://<spark-host>:8100
	Token string // SPARK_BROKER_TOKEN; sent only when set
	HTTP  *http.Client
}

func (c *Client) api() *httpx.Client {
	h := http.Header{}
	if c.Token != "" {
		h.Set("Authorization", "Bearer "+c.Token)
	}
	hc := c.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: 30 * time.Second}
	}
	return &httpx.Client{Service: "spark-broker", BaseURL: c.Base, HTTP: hc, Header: h, Secrets: []string{c.Token}}
}

type Stage struct {
	Services    []string `json:"services"`
	Mode        string   `json:"mode"`
	LeaseMin    int      `json:"lease_min,omitempty"`
	EstimateMin int      `json:"estimate_min,omitempty"`
}

type SubmitReq struct {
	Project  string  `json:"project"`
	Session  string  `json:"session"`
	Title    string  `json:"title"`
	Priority int     `json:"priority"`
	Stages   []Stage `json:"stages"`
}

type Job struct {
	ID       string `json:"id"`
	State    string `json:"state"`
	Priority int    `json:"priority"`
	Waiting  string `json:"waiting"`
	Error    string `json:"error"`
}

// Final reports a state the job never leaves.
func (j Job) Final() bool {
	return j.State == Done || j.State == Failed || j.State == Cancelled || j.State == Orphaned
}

// Count is one metric of the done summary that goes to Telegram.
type Count struct {
	OK     int `json:"ok"`
	Failed int `json:"failed"`
}

// Lease asks for services for minutes at priority.
func (c *Client) Lease(ctx context.Context, project, title string, services []string, minutes, priority int) (Job, error) {
	var j Job
	err := c.api().Post(ctx, "/v1/jobs", SubmitReq{
		Project: project, Session: "promohub", Title: title, Priority: priority,
		Stages: []Stage{{Services: services, Mode: "lease", LeaseMin: minutes, EstimateMin: minutes}},
	}, &j)
	return j, err
}

func (c *Client) Get(ctx context.Context, id string) (Job, error) {
	var j Job
	err := c.api().Get(ctx, "/v1/jobs/"+url.PathEscape(id), nil, &j)
	return j, err
}

func (c *Client) Heartbeat(ctx context.Context, id string) error {
	return c.api().Post(ctx, "/v1/jobs/"+url.PathEscape(id)+"/heartbeat", map[string]any{"metrics": nil}, nil)
}

// Done ends the lease; a non-empty failure reports it as failed (the broker tells Telegram).
func (c *Client) Done(ctx context.Context, id string, metrics map[string]Count, failure string) error {
	return c.api().Post(ctx, "/v1/jobs/"+url.PathEscape(id)+"/done", map[string]any{"metrics": metrics, "error": failure}, nil)
}

// Cancel withdraws a job that never got to run.
func (c *Client) Cancel(ctx context.Context, id string) error {
	return c.api().Do(ctx, http.MethodDelete, "/v1/jobs/"+url.PathEscape(id), nil, nil, nil)
}

// WaitRunning polls until the job runs, ends or the deadline passes.
func (c *Client) WaitRunning(ctx context.Context, id string, every, max time.Duration) (Job, error) {
	deadline := time.Now().Add(max)
	for {
		j, err := c.Get(ctx, id)
		if err != nil {
			return j, err
		}
		switch {
		case j.State == Running:
			return j, nil
		case j.Final():
			return j, fmt.Errorf("lease %s ended %s: %s", id, j.State, j.Error)
		case time.Now().After(deadline):
			return j, &NotGranted{Job: j}
		}
		select {
		case <-ctx.Done():
			return j, ctx.Err()
		case <-time.After(every):
		}
	}
}

// NotGranted means the broker did not hand out the lease in time.
type NotGranted struct{ Job Job }

func (e *NotGranted) Error() string {
	return fmt.Sprintf("lease %s still %s: %s", e.Job.ID, e.Job.State, e.Job.Waiting)
}
