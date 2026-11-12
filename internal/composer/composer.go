// Package composer turns a campaign into one draft text per account. v0 is deterministic
// (link placement, hashtags, trimming to the platform limit) and always works; the optional
// LLM pass (llm.go) rewrites texts and falls back to v0 for anything it gets wrong.
package composer

import (
	"net/url"
	"strings"

	"github.com/lioilsources/promohub/internal/config"
	"github.com/lioilsources/promohub/internal/platform"
)

// Campaign is what the composer needs from a campaign row.
type Campaign struct {
	Slug     string // campaign slug for utm_campaign
	App      *config.App
	Lang     string
	Title    string
	Body     string
	Link     string
	HasMedia bool
}

// Draft is the composed text for one account.
type Draft struct {
	Text  string
	Extra map[string]string // title, board, link, image_required, …
}

// How a platform shows a link.
type linkMode int

const (
	linkInline   linkMode = iota // URL in the text (Mastodon, X, Threads, Telegram, Discord)
	linkCard                     // URL outside the text, rendered as a card (Bluesky without media)
	linkSeparate                 // URL in its own field (IFTTT Value2, Reddit, HN, Pinterest)
	linkBio                      // links are not clickable: "link in bio" (Instagram, TikTok)
)

func modeFor(acct config.Account, hasMedia bool) linkMode {
	switch acct.Platform {
	case "bluesky":
		if hasMedia {
			return linkInline // an images embed replaces the link card
		}
		return linkCard
	case "instagram", "tiktok", "youtube":
		return linkBio
	case "facebook", "linkedin", "pinterest", "reddit", "hackernews", "producthunt", "devto":
		return linkSeparate
	}
	return linkInline
}

// hashtagged lists the platforms where hashtags help discovery.
var hashtagged = map[string]bool{"mastodon": true, "bluesky": true, "instagram": true, "tiktok": true, "pinterest": true}

// Compose builds the v0 draft for one account.
func Compose(acct config.Account, c Campaign, ownHosts []string) Draft {
	spec, _ := platform.Lookup(acct.Platform)
	d := Draft{Extra: map[string]string{}}
	link := UTM(c.Link, acct.Platform, c.Slug, ownHosts)
	body := strings.TrimSpace(c.Body)

	var tail []string
	switch modeFor(acct, c.HasMedia) {
	case linkInline:
		if link != "" {
			tail = append(tail, link)
		}
	case linkCard, linkSeparate:
		if link != "" {
			d.Extra["link"] = link
		}
	case linkBio:
		if link != "" {
			d.Extra["link"] = link
			if c.Lang == "cs" {
				tail = append(tail, "Odkaz v profilu.")
			} else {
				tail = append(tail, "Link in bio.")
			}
		}
	}
	if hashtagged[acct.Platform] && c.App != nil && len(c.App.Hashtags) > 0 {
		tags := make([]string, 0, len(c.App.Hashtags))
		for _, h := range c.App.Hashtags {
			tags = append(tags, "#"+strings.TrimPrefix(h, "#"))
		}
		tail = append(tail, strings.Join(tags, " "))
	}
	if spec.TitleMax > 0 {
		t := c.Title
		if t == "" {
			t, _, _ = strings.Cut(body, "\n")
		}
		d.Extra["title"] = cut(strings.TrimSpace(t), spec.TitleMax)
	}
	if acct.Platform == "pinterest" && c.App != nil {
		if b := acct.Boards[c.App.Slug]; b != "" {
			d.Extra["board"] = b
		}
		d.Extra["image_required"] = "true"
	}
	d.Text = spec.TrimTo(body, strings.Join(tail, "\n"))
	return d
}

// UTM tags links to my own sites so web analytics can tell platforms apart.
func UTM(link, platformName, campaign string, ownHosts []string) string {
	u, err := url.Parse(link)
	if err != nil || u.Host == "" || !own(u.Hostname(), ownHosts) {
		return link
	}
	q := u.Query()
	if q.Get("utm_source") != "" {
		return link
	}
	q.Set("utm_source", platformName)
	q.Set("utm_medium", "social")
	if campaign != "" {
		q.Set("utm_campaign", campaign)
	}
	u.RawQuery = q.Encode()
	return u.String()
}

func own(host string, hosts []string) bool {
	for _, h := range hosts {
		if host == h || strings.HasSuffix(host, "."+h) {
			return true
		}
	}
	return false
}

// OwnHosts collects the hosts of the links in apps.yaml.
func OwnHosts(apps []config.App) []string {
	seen := map[string]bool{}
	var out []string
	for _, a := range apps {
		for _, l := range a.Links {
			if u, err := url.Parse(l); err == nil && u.Hostname() != "" && !seen[u.Hostname()] {
				seen[u.Hostname()] = true
				out = append(out, u.Hostname())
			}
		}
	}
	return out
}

func cut(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return strings.TrimSpace(string(r[:n-1])) + "…"
}
