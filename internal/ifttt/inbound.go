package ifttt

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/lioilsources/promohub/internal/platform"
)

// Inbound events IFTTT applets send to POST /webhooks/ifttt[/{event}]. IFTTT Webhooks requests
// are unsigned, so they are only ever triggers: they register my own content or start a
// whitelisted Apify task, never publish anything.
const (
	EventYouTubeNewVideo  = "youtube_new_video"
	EventBlueskyNewPost   = "bluesky_new_post"
	EventInstagramNewPost = "instagram_new_post"
	EventRunTask          = "run_task"
)

// ContentPlatform maps a "new content by me" event to its platform.
var ContentPlatform = map[string]string{
	EventYouTubeNewVideo:  "youtube",
	EventBlueskyNewPost:   "bluesky",
	EventInstagramNewPost: "instagram",
}

// Inbound is the JSON body an applet sends; applets fill it with ingredients, e.g.
// {"url":"{{Url}}","title":"{{Title}}","published_at":"{{PublishedAt}}"}.
type Inbound struct {
	Event       string `json:"event"`
	URL         string `json:"url"`
	Title       string `json:"title"`
	Body        string `json:"body"`
	PublishedAt string `json:"published_at"`
	Task        string `json:"task"`
}

func ParseInbound(b []byte, pathEvent string) (Inbound, error) {
	var in Inbound
	if len(strings.TrimSpace(string(b))) > 0 {
		if err := json.Unmarshal(b, &in); err != nil {
			return in, fmt.Errorf("ifttt: %w", err)
		}
	}
	if pathEvent != "" {
		in.Event = pathEvent
	}
	in.Event = strings.ToLower(strings.TrimSpace(in.Event))
	if in.Event == "" {
		return in, fmt.Errorf("ifttt: no event (path /webhooks/ifttt/{event} or \"event\" field)")
	}
	return in, nil
}

// IFTTT renders dates like "October 29, 2026 at 10:00AM".
var dateLayouts = []string{time.RFC3339, "January 2, 2006 at 03:04PM", "January 2, 2006 at 3:04PM", "2006-01-02 15:04:05"}

func (in Inbound) Published() *time.Time {
	s := strings.TrimSpace(in.PublishedAt)
	for _, l := range dateLayouts {
		if t, err := time.Parse(l, s); err == nil {
			t = t.UTC()
			return &t
		}
	}
	return nil
}

// ExternalID derives a stable ID from a post URL so that the same post seen later by an
// Apify scrape or an API collector dedups against it (see platform.ExternalID).
func ExternalID(platform_, raw string) string { return platform.ExternalID(platform_, raw) }
