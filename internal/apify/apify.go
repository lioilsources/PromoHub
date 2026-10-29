// Package apify wraps apify-client-go with the few calls the hub needs and parses
// the ACTOR.RUN.* webhook that Apify sends to /webhooks/apify.
package apify

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"time"

	apifyclient "github.com/apify/apify-client-go"
)

// Run is the part of an Apify run the hub stores.
type Run struct {
	ID         string
	ActorID    string
	TaskID     string
	Status     string
	DatasetID  string
	StartedAt  *time.Time
	FinishedAt *time.Time
	CostUSD    *float64
}

// API is what the hub needs from Apify; tests swap in a fake.
type API interface {
	GetRun(ctx context.Context, runID string) (Run, error)
	DatasetItems(ctx context.Context, datasetID string, limit int64) ([]json.RawMessage, error)
	RunTask(ctx context.Context, taskID string, maxItems int64) (Run, error)
}

type Client struct {
	c *apifyclient.ApifyClient
}

func New(token string) *Client {
	return &Client{c: apifyclient.NewClient(
		apifyclient.WithToken(token),
		apifyclient.WithUserAgentSuffix("promohub"),
		apifyclient.WithMaxRetries(4),
	)}
}

func toRun(r apifyclient.ActorRun) Run {
	out := Run{
		ID: r.ID, ActorID: r.ActID, TaskID: r.ActorTaskID, Status: r.Status,
		DatasetID: r.DefaultDatasetID, StartedAt: r.StartedAt, FinishedAt: r.FinishedAt,
	}
	// usageTotalUsd is not a typed field in the client; it lands in Extra.
	if raw, ok := r.Extra["usageTotalUsd"]; ok {
		var f float64
		if json.Unmarshal(raw, &f) == nil {
			out.CostUSD = &f
		}
	}
	return out
}

func (c *Client) GetRun(ctx context.Context, runID string) (Run, error) {
	r, found, err := c.c.Run(runID).Get(ctx)
	if err != nil {
		return Run{}, err
	}
	if !found {
		return Run{}, fmt.Errorf("apify: run %s not found", runID)
	}
	return toRun(r), nil
}

// DatasetItems reads up to limit clean items (0 = all). Datasets here are small (maxItems ≤ a few hundred).
func (c *Client) DatasetItems(ctx context.Context, datasetID string, limit int64) ([]json.RawMessage, error) {
	clean := true
	opts := apifyclient.DatasetListItemsOptions{Clean: &clean}
	if limit > 0 {
		opts.Limit = &limit
	}
	chunk := int64(500)
	it := apifyclient.IterateDatasetItems[json.RawMessage](c.c.Dataset(datasetID), opts, &chunk)
	var out []json.RawMessage
	for {
		item, err := it.Next(ctx)
		if err != nil {
			return out, err
		}
		if item == nil {
			return out, nil
		}
		out = append(out, *item)
	}
}

// RunTask starts a saved task with its stored input. The hub never passes input overrides
// built from scraped content (see PROMO_HUB_PLAN.md §12).
func (c *Client) RunTask(ctx context.Context, taskID string, maxItems int64) (Run, error) {
	opts := apifyclient.TaskStartOptions{}
	if maxItems > 0 {
		opts.MaxItems = &maxItems
	}
	r, err := c.c.Task(taskID).Start(ctx, nil, opts)
	if err != nil {
		return Run{}, err
	}
	return toRun(r), nil
}

// Webhook is the default Apify webhook payload template.
type Webhook struct {
	UserID    string `json:"userId"`
	CreatedAt string `json:"createdAt"`
	EventType string `json:"eventType"`
	EventData struct {
		ActorID     string `json:"actorId"`
		ActorTaskID string `json:"actorTaskId"`
		ActorRunID  string `json:"actorRunId"`
	} `json:"eventData"`
	Resource struct {
		ID          string `json:"id"`
		ActID       string `json:"actId"`
		ActorTaskID string `json:"actorTaskId"`
		Status      string `json:"status"`
	} `json:"resource"`
}

// RunID prefers eventData and falls back to the resource.
func (w Webhook) RunID() string {
	if w.EventData.ActorRunID != "" {
		return w.EventData.ActorRunID
	}
	return w.Resource.ID
}

func (w Webhook) TaskID() string {
	if w.EventData.ActorTaskID != "" {
		return w.EventData.ActorTaskID
	}
	return w.Resource.ActorTaskID
}

func ParseWebhook(b []byte) (Webhook, error) {
	var w Webhook
	if err := json.Unmarshal(b, &w); err != nil {
		return w, fmt.Errorf("apify webhook: %w", err)
	}
	if w.RunID() == "" {
		return w, fmt.Errorf("apify webhook: no run ID")
	}
	return w, nil
}

// SecretOK compares in constant time.
func SecretOK(got, want string) bool {
	return want != "" && subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
}
