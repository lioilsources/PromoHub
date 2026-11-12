// Package publish is the platform-neutral model of an outgoing post and the Publisher
// interface the adapters in internal/platforms implement.
package publish

import (
	"context"
	"errors"
	"net/http"

	"github.com/lioilsources/promohub/internal/config"
	"github.com/lioilsources/promohub/internal/platforms/httpx"
)

// Media is one attached file. Path is local (for upload APIs), URL public (for pull APIs).
type Media struct {
	ID         int64
	Path       string
	URL        string
	Mime       string
	Alt        string
	Width      int
	Height     int
	Bytes      int64
	DurationMs int
}

func (m Media) IsVideo() bool { return len(m.Mime) > 6 && m.Mime[:6] == "video/" }

func (m Media) IsImage() bool { return len(m.Mime) > 6 && m.Mime[:6] == "image/" }

// Post is an approved draft ready to go out.
type Post struct {
	DraftID    int64
	Text       string
	Title      string
	Link       string // also inside Text where the platform shows links inline
	Lang       string
	Media      []Media
	Extra      map[string]string
	AttemptKey string // stable per attempt; sent as Idempotency-Key where supported
}

// Result of a publish. Sent means "handed over, no post ID" (IFTTT, manual, TikTok inbox).
type Result struct {
	ExternalID string
	URL        string
	Sent       bool
	Note       string // what Olin still has to do by hand (TikTok: finish the draft in the app)
}

type Publisher interface {
	Platform() string
	Ready(acct config.Account) error
	Publish(ctx context.Context, acct config.Account, p Post) (Result, error)
}

// Rejected wraps an error that happened before anything could have been posted (validation,
// upload refused, auth). The draft goes to failed and can be approved again.
type Rejected struct{ Err error }

func (e *Rejected) Error() string { return e.Err.Error() }
func (e *Rejected) Unwrap() error { return e.Err }

// Reject marks err as safe to retry by hand.
func Reject(err error) error {
	if err == nil {
		return nil
	}
	return &Rejected{Err: err}
}

// Outcome of a failed publish.
type Outcome int

const (
	Failed  Outcome = iota // nothing was posted
	Unknown                // the post may exist; ask before retrying
)

// Classify decides whether a failed publish may have posted: an explicit Rejected or a 4xx
// answer means no; a timeout, transport error or 5xx means maybe.
func Classify(err error) Outcome {
	var r *Rejected
	if errors.As(err, &r) {
		return Failed
	}
	if c := httpx.Code(err); c >= 400 && c < 500 && c != http.StatusRequestTimeout {
		return Failed
	}
	return Unknown
}

// InboundRef identifies the comment or mention a reply answers.
type InboundRef struct {
	ExternalID string
	URL        string
	Author     string
}

// Replier answers an inbound item on its platform. Olin writes the text in Telegram, so a
// reply needs no further approval.
type Replier interface {
	Platform() string
	Reply(ctx context.Context, acct config.Account, to InboundRef, text string) (Result, error)
}
