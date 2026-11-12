// Package platform is the registry of social platforms: limits, read/publish paths, polling
// intervals and how post URLs map to external IDs. It is the only place that knows such
// per-platform facts; adapters in internal/platforms/<name> implement the API calls.
package platform

import (
	"net/url"
	"strings"
	"time"
)

// Via is how the hub reaches a platform for one direction (read or publish).
type Via string

const (
	ViaAPI    Via = "api"    // official API, implemented by an adapter
	ViaApify  Via = "apify"  // Apify task (accounts.yaml apify_tasks)
	ViaIFTTT  Via = "ifttt"  // IFTTT Webhooks applet; fire-and-forget, no post ID back
	ViaManual Via = "manual" // Telegram card with the text and a compose link
	ViaNone   Via = "none"
)

// Capabilities a read adapter can collect.
const (
	CapProfile = "profile" // followers plus metrics of my recent posts
	CapInbound = "inbound" // comments, replies and mentions of my account
	CapSearch  = "search"  // keyword mentions of my apps by anyone
)

// LengthUnit says how a platform counts post length.
type LengthUnit int

const (
	Runes     LengthUnit = iota // Unicode code points
	Graphemes                   // user-perceived characters (Bluesky)
	XWeighted                   // X: CJK and emoji count double
)

// Spec describes one platform. Limits are defaults checked against the official docs on
// 2026-11-12 (docs/PLATFORMS.md); per-instance limits (Mastodon) are read at runtime.
type Spec struct {
	Name  string
	Title string

	Read       Via   // default read path
	Publish    Via   // default publish path
	ReadVias   []Via // allowed read paths
	PubVias    []Via // allowed publish paths
	Intervals  map[string]time.Duration
	HandleHelp string // shown by `promohub check` when a handle looks wrong

	TextLimit int        // max post length in Unit; 0 = no practical limit
	Unit      LengthUnit // how TextLimit is counted
	URLWeight int        // a URL counts as this many characters (0 = its real length)
	MaxImages int
	TitleMax  int // for platforms with a separate title (YouTube, Reddit, Pinterest, dev.to)

	// Video limits of the publish path; VideoMB == 0 means the path takes no video.
	VideoMB      int
	VideoSeconds int
}

var all = []Spec{
	{Name: "bluesky", Title: "Bluesky", Read: ViaAPI, Publish: ViaAPI,
		ReadVias: v(ViaAPI, ViaNone), PubVias: v(ViaAPI, ViaIFTTT, ViaManual, ViaNone),
		Intervals: iv(3*time.Hour, 10*time.Minute, 30*time.Minute), HandleHelp: "full handle, e.g. name.bsky.social",
		TextLimit: 300, Unit: Graphemes, MaxImages: 4},
	{Name: "mastodon", Title: "Mastodon", Read: ViaAPI, Publish: ViaAPI,
		ReadVias: v(ViaAPI, ViaNone), PubVias: v(ViaAPI, ViaIFTTT, ViaManual, ViaNone),
		Intervals: iv(3*time.Hour, 10*time.Minute, 30*time.Minute), HandleHelp: "user@instance",
		TextLimit: 500, Unit: Runes, URLWeight: 23, MaxImages: 4},
	{Name: "x", Title: "X", Read: ViaApify, Publish: ViaIFTTT,
		ReadVias: v(ViaApify, ViaNone), PubVias: v(ViaIFTTT, ViaManual, ViaNone),
		TextLimit: 280, Unit: XWeighted, URLWeight: 23, MaxImages: 4},
	{Name: "threads", Title: "Threads", Read: ViaAPI, Publish: ViaAPI,
		ReadVias: v(ViaAPI, ViaNone), PubVias: v(ViaAPI, ViaIFTTT, ViaManual, ViaNone),
		Intervals: iv(3*time.Hour, 15*time.Minute, 0), TextLimit: 500, Unit: Runes, MaxImages: 10},
	{Name: "instagram", Title: "Instagram", Read: ViaAPI, Publish: ViaAPI,
		ReadVias: v(ViaAPI, ViaApify, ViaNone), PubVias: v(ViaAPI, ViaManual, ViaNone),
		Intervals: iv(3*time.Hour, 15*time.Minute, 0), TextLimit: 2200, Unit: Runes, MaxImages: 10},
	{Name: "facebook", Title: "Facebook Page", Read: ViaAPI, Publish: ViaIFTTT,
		ReadVias: v(ViaAPI, ViaNone), PubVias: v(ViaIFTTT, ViaAPI, ViaManual, ViaNone),
		Intervals: iv(3*time.Hour, 15*time.Minute, 0), TextLimit: 63206, Unit: Runes, MaxImages: 10},
	{Name: "youtube", Title: "YouTube", Read: ViaAPI, Publish: ViaManual,
		ReadVias: v(ViaAPI, ViaApify, ViaNone), PubVias: v(ViaManual, ViaAPI, ViaNone),
		Intervals: iv(3*time.Hour, 15*time.Minute, 0), HandleHelp: "channel handle without @",
		TextLimit: 5000, Unit: Runes, TitleMax: 100},
	{Name: "tiktok", Title: "TikTok", Read: ViaApify, Publish: ViaManual,
		ReadVias: v(ViaApify, ViaAPI, ViaNone), PubVias: v(ViaManual, ViaAPI, ViaNone),
		Intervals: iv(6*time.Hour, 0, 0),
		TextLimit: 2200, Unit: Runes},
	{Name: "reddit", Title: "Reddit", Read: ViaApify, Publish: ViaManual,
		ReadVias: v(ViaApify, ViaNone), PubVias: v(ViaManual, ViaNone),
		TextLimit: 40000, Unit: Runes, TitleMax: 300},
	{Name: "linkedin", Title: "LinkedIn", Read: ViaNone, Publish: ViaIFTTT,
		ReadVias: v(ViaNone), PubVias: v(ViaIFTTT, ViaManual, ViaNone),
		TextLimit: 3000, Unit: Runes},
	{Name: "pinterest", Title: "Pinterest", Read: ViaNone, Publish: ViaIFTTT,
		ReadVias: v(ViaApify, ViaNone), PubVias: v(ViaIFTTT, ViaManual, ViaNone),
		TextLimit: 800, Unit: Runes, MaxImages: 1, TitleMax: 100},
	{Name: "telegram", Title: "Telegram channel", Read: ViaAPI, Publish: ViaAPI,
		ReadVias: v(ViaAPI, ViaNone), PubVias: v(ViaAPI, ViaManual, ViaNone),
		Intervals: iv(6*time.Hour, 0, 0), HandleHelp: "public channel username without @",
		TextLimit: 4096, Unit: Runes, MaxImages: 10},
	{Name: "discord", Title: "Discord", Read: ViaAPI, Publish: ViaAPI,
		ReadVias: v(ViaAPI, ViaNone), PubVias: v(ViaAPI, ViaManual, ViaNone),
		Intervals: iv(6*time.Hour, 0, 0), HandleHelp: "server name; external_id is a permanent invite code",
		TextLimit: 2000, Unit: Runes, MaxImages: 10},
	{Name: "hackernews", Title: "Hacker News", Read: ViaAPI, Publish: ViaManual,
		ReadVias: v(ViaAPI, ViaNone), PubVias: v(ViaManual, ViaNone),
		Intervals: iv(6*time.Hour, 0, 30*time.Minute), HandleHelp: "HN username (karma counts as followers)",
		TextLimit: 0, Unit: Runes, TitleMax: 80},
	{Name: "producthunt", Title: "Product Hunt", Read: ViaAPI, Publish: ViaManual,
		ReadVias: v(ViaAPI, ViaNone), PubVias: v(ViaManual, ViaNone),
		Intervals: iv(6*time.Hour, 30*time.Minute, 0), TextLimit: 0, Unit: Runes, TitleMax: 60},
	{Name: "devto", Title: "dev.to", Read: ViaAPI, Publish: ViaAPI,
		ReadVias: v(ViaAPI, ViaNone), PubVias: v(ViaAPI, ViaManual, ViaNone),
		Intervals: iv(6*time.Hour, 30*time.Minute, 0), TextLimit: 0, Unit: Runes, TitleMax: 128},
}

func v(x ...Via) []Via { return x }

// iv builds the polling intervals; 0 means the capability does not exist on the platform.
func iv(profile, inbound, search time.Duration) map[string]time.Duration {
	m := map[string]time.Duration{}
	for k, d := range map[string]time.Duration{CapProfile: profile, CapInbound: inbound, CapSearch: search} {
		if d > 0 {
			m[k] = d
		}
	}
	return m
}

var byName = func() map[string]Spec {
	m := map[string]Spec{}
	for _, s := range all {
		m[s.Name] = s
	}
	return m
}()

// Lookup returns the spec of a platform.
func Lookup(name string) (Spec, bool) {
	s, ok := byName[name]
	return s, ok
}

// All returns every known platform in registry order.
func All() []Spec { return append([]Spec(nil), all...) }

// Allows reports whether via is a valid choice from list.
func Allows(list []Via, via Via) bool {
	for _, x := range list {
		if x == via {
			return true
		}
	}
	return false
}

// Capabilities returns the read capabilities the platform has, in a stable order.
func (s Spec) Capabilities() []string {
	var out []string
	for _, c := range []string{CapProfile, CapInbound, CapSearch} {
		if s.Intervals[c] > 0 {
			out = append(out, c)
		}
	}
	return out
}

// ExternalID derives the canonical external ID of one of my posts from its URL, so that the
// same post seen by an API adapter, an Apify scrape, IFTTT or a pasted URL dedups in
// content_items. Falls back to the trimmed URL.
func ExternalID(platform, raw string) string {
	raw = strings.TrimSpace(raw)
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return raw
	}
	parts := strings.FieldsFunc(u.Path, func(r rune) bool { return r == '/' })
	after := func(seg string) string {
		for i := 0; i+1 < len(parts); i++ {
			if parts[i] == seg {
				return parts[i+1]
			}
		}
		return ""
	}
	last := ""
	if len(parts) > 0 {
		last = parts[len(parts)-1]
	}
	switch platform {
	case "youtube":
		if v := u.Query().Get("v"); v != "" {
			return v
		}
		if strings.Contains(u.Host, "youtu.be") && len(parts) > 0 {
			return parts[0]
		}
		if id := after("shorts"); id != "" {
			return id
		}
	case "bluesky":
		if id := after("post"); id != "" {
			return id // record key; unique within my repo, which is all content_items holds
		}
	case "instagram", "threads":
		for _, seg := range []string{"p", "reel", "tv", "post"} {
			if id := after(seg); id != "" {
				return id // shortcode
			}
		}
	case "mastodon":
		if strings.HasPrefix(last, "@") {
			break
		}
		if last != "" && isDigits(last) {
			return last
		}
	case "x":
		if id := after("status"); id != "" {
			return id
		}
	case "tiktok":
		if id := after("video"); id != "" {
			return id
		}
	case "reddit":
		if id := after("comments"); id != "" {
			return id
		}
	case "hackernews":
		if id := u.Query().Get("id"); id != "" {
			return id
		}
	}
	return raw
}

func isDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return s != ""
}
