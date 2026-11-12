// Package collectors defines the platform-neutral model every source maps into: Apify datasets
// (apifysrc) and the official-API adapters in internal/platforms.
package collectors

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/lioilsources/promohub/internal/config"
)

// Content is one of my own posts/videos.
type Content struct {
	ExternalID  string
	URL         string
	Title       string
	Body        string
	PublishedAt *time.Time
}

// Snapshot holds metrics either for an account (Content == nil) or for one content item.
type Snapshot struct {
	Platform  string
	Content   *Content
	Likes     *int64
	Views     *int64
	Reposts   *int64
	Comments  *int64
	Followers *int64
	Extra     map[string]int64
}

// Inbound is something someone else wrote: a comment, review, mention or reply.
type Inbound struct {
	Platform   string
	Kind       string // comment | review | mention | reply
	ExternalID string
	Author     string
	Text       string
	URL        string
	Rating     *int32
	CreatedAt  *time.Time
}

// Batch is the result of one collection pass.
type Batch struct {
	Snapshots []Snapshot
	Inbound   []Inbound
	Skipped   int // items that could not be mapped (no ID, someone else's post, …)
}

// Request is one scheduled pass of a collector.
type Request struct {
	Account    config.Account
	Capability string          // platform.CapProfile | CapInbound | CapSearch
	Cursor     json.RawMessage // what the previous pass returned; nil on the first pass
	Terms      []string        // search terms (app names and keywords) for CapSearch
}

// Collector is a read adapter of one platform.
type Collector interface {
	Platform() string
	// Ready reports whether the account has what the capability needs (secrets, IDs).
	// A *creds.MissingError means "not connected yet".
	Ready(acct config.Account, capability string) error
	// Collect returns the batch and the cursor for the next pass (nil keeps the old one).
	Collect(ctx context.Context, r Request) (Batch, json.RawMessage, error)
}

// RateLimited asks the scheduler to wait until Until before the next attempt.
type RateLimited struct {
	Until time.Time
	Err   error
}

func (e *RateLimited) Error() string {
	return fmt.Sprintf("rate limited until %s: %v", e.Until.Format(time.RFC3339), e.Err)
}

func (e *RateLimited) Unwrap() error { return e.Err }

// Int64 is a helper for adapters that read optional counters.
func Int64(v int64) *int64 { return &v }
