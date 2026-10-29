// Package collectors defines the platform-neutral model every source maps into.
// Phase 1 has only apifysrc; the free-API collectors (YouTube, Bluesky, …) follow in phase 2.
package collectors

import (
	"context"
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

type Collector interface {
	Name() string
	Collect(ctx context.Context, acct config.Account) (Batch, error)
}
