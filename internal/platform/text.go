package platform

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/rivo/uniseg"
)

var reURL = regexp.MustCompile(`https?://[^\s<>"]+`)

// URLs returns the links in a text, in order.
func URLs(text string) []string { return reURL.FindAllString(text, -1) }

// Length counts text the way the platform does: graphemes on Bluesky, weighted characters on
// X, code points elsewhere; with URLWeight set every link counts as that many characters.
func (s Spec) Length(text string) int {
	n := 0
	if s.URLWeight > 0 {
		n += s.URLWeight * len(URLs(text))
		text = reURL.ReplaceAllString(text, "")
	}
	switch s.Unit {
	case Graphemes:
		n += uniseg.GraphemeClusterCount(text)
	case XWeighted:
		// twitter-text: these ranges weigh 1, everything else (CJK, emoji) 2.
		for _, r := range text {
			if r <= 0x10FF || r >= 0x2000 && r <= 0x200D || r >= 0x2010 && r <= 0x201F || r >= 0x2032 && r <= 0x2037 {
				n++
			} else {
				n += 2
			}
		}
	default:
		n += utf8.RuneCountInString(text)
	}
	return n
}

// Fits reports whether text is within the platform's limit (0 = no limit).
func (s Spec) Fits(text string) bool { return s.TextLimit == 0 || s.Length(text) <= s.TextLimit }

// Intent is a compose link that opens the platform's own editor prefilled, for the manual
// path. host is the account's instance (Mastodon). "" when the platform has none.
func Intent(name, host, text, link, title string) string {
	q := url.Values{}
	switch name {
	case "reddit":
		q.Set("title", title)
		if link != "" {
			q.Set("url", link)
		} else {
			q.Set("text", text)
			q.Set("type", "TEXT")
		}
		return "https://www.reddit.com/submit?" + q.Encode()
	case "hackernews":
		if link == "" {
			return "https://news.ycombinator.com/submit"
		}
		q.Set("u", link)
		q.Set("t", title)
		return "https://news.ycombinator.com/submitlink?" + q.Encode()
	case "x":
		q.Set("text", text)
		return "https://x.com/intent/post?" + q.Encode()
	case "bluesky":
		q.Set("text", text)
		return "https://bsky.app/intent/compose?" + q.Encode()
	case "threads":
		q.Set("text", text)
		return "https://www.threads.net/intent/post?" + q.Encode()
	case "mastodon":
		if host == "" {
			return ""
		}
		q.Set("text", text)
		return "https://" + host + "/share?" + q.Encode()
	case "linkedin":
		if link == "" {
			return "https://www.linkedin.com/feed/?shareActive=true"
		}
		q.Set("url", link)
		return "https://www.linkedin.com/sharing/share-offsite/?" + q.Encode()
	case "facebook":
		if link == "" {
			return "https://www.facebook.com/"
		}
		q.Set("u", link)
		return "https://www.facebook.com/sharer/sharer.php?" + q.Encode()
	case "pinterest":
		q.Set("url", link)
		q.Set("description", text)
		return "https://www.pinterest.com/pin/create/button/?" + q.Encode()
	case "youtube":
		return "https://studio.youtube.com/"
	case "tiktok":
		return "https://www.tiktok.com/tiktokstudio/upload"
	case "producthunt":
		return "https://www.producthunt.com/posts/new"
	case "devto":
		return "https://dev.to/new"
	}
	return ""
}

// TrimTo shortens text to the platform limit at a word boundary, adding an ellipsis; tail
// (links, hashtags) is kept whole and appended after a blank line.
func (s Spec) TrimTo(body, tail string) string {
	join := func(b string) string {
		switch {
		case tail == "":
			return b
		case b == "":
			return tail
		}
		return b + "\n\n" + tail
	}
	if out := join(body); s.Fits(out) {
		return out
	}
	words := strings.Fields(body)
	for len(words) > 0 {
		words = words[:len(words)-1]
		if out := join(strings.Join(words, " ") + "…"); s.Fits(out) {
			return out
		}
	}
	return tail
}

// video limits by platform (official docs, 2026-11-12; docs/PLATFORMS.md). IFTTT applets only
// take an image URL, so ifttt/manual platforms are absent.
var video = map[string][2]int{ // MB, seconds
	"bluesky":   {100, 180},
	"mastodon":  {99, 0},
	"instagram": {300, 900},
	"threads":   {1000, 300},
	"tiktok":    {300, 600}, // the hub caps uploads at 300 MB
	"youtube":   {300, 0},
	"telegram":  {50, 0},
	"discord":   {10, 0},
}

func init() {
	for i := range all {
		if v, ok := video[all[i].Name]; ok {
			all[i].VideoMB, all[i].VideoSeconds = v[0], v[1]
			byName[all[i].Name] = all[i]
		}
	}
}

// CheckVideo explains why a video cannot go to the platform ("" = fine).
func (s Spec) CheckVideo(bytes int64, durationMs int) string {
	switch {
	case s.VideoMB == 0:
		return s.Title + " video přes hub nebere"
	case bytes > int64(s.VideoMB)<<20:
		return fmt.Sprintf("%s bere video do %d MB (má %d MB)", s.Title, s.VideoMB, bytes>>20)
	case s.VideoSeconds > 0 && durationMs > s.VideoSeconds*1000:
		return fmt.Sprintf("%s bere video do %d s (má %d s)", s.Title, s.VideoSeconds, durationMs/1000)
	}
	return ""
}
