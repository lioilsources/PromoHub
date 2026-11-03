// Package apifysrc maps dataset items of Apify Store actors onto the collectors model.
//
// Store actors differ in field names (and change them), so every field is read from a list
// of candidate keys. When an actor is swapped, extend the candidates here and add a fixture
// to the tests; nothing else in the hub depends on actor output.
//
// Checked against real runs (2026-11-03): apidojo/tweet-scraper, clockworks/tiktok-scraper,
// harshmaur/reddit-scraper, apify/instagram-profile-scraper, streamers/youtube-scraper.
package apifysrc

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/lioilsources/promohub/internal/collectors"
)

// Kinds of Apify tasks the hub understands (accounts.yaml → apify_tasks[].kind).
const (
	KindXProfile       = "x-profile"
	KindXMentions      = "x-mentions"
	KindTikTokProfile  = "tiktok-profile"
	KindRedditMentions = "reddit-mentions"
	KindYTMentions     = "yt-mentions"
	KindIGProfile      = "ig-profile"
)

var Kinds = map[string]string{ // kind → platform
	KindXProfile: "x", KindXMentions: "x", KindTikTokProfile: "tiktok",
	KindRedditMentions: "reddit", KindYTMentions: "youtube", KindIGProfile: "instagram",
}

// Map converts raw dataset items. owner is my handle on that platform; it tells my posts
// from other people's and keeps my own posts out of the mention stream.
func Map(kind, owner string, items []json.RawMessage) (collectors.Batch, error) {
	platform, ok := Kinds[kind]
	if !ok {
		return collectors.Batch{}, fmt.Errorf("apifysrc: unknown kind %q", kind)
	}
	owner = normHandle(owner)
	var b collectors.Batch
	var followers *int64
	for _, raw := range items {
		var m obj
		if err := json.Unmarshal(raw, &m); err != nil {
			b.Skipped++
			continue
		}
		switch kind {
		case KindXProfile:
			if !mapXPost(&b, m, owner) {
				b.Skipped++
				continue
			}
			followers = maxPtr(followers, m.num("author.followers", "author.followersCount", "user.followers_count", "user.followersCount"))
		case KindTikTokProfile:
			if !mapTikTok(&b, m, owner) {
				b.Skipped++
				continue
			}
			followers = maxPtr(followers, m.num("authorMeta.fans", "authorMeta.followers", "author.followerCount"))
		case KindIGProfile:
			// instagram-profile-scraper: one item per profile with latestPosts; instagram-scraper: one item per post.
			if posts, ok := m.get("latestPosts").([]any); ok {
				followers = maxPtr(followers, m.num("followersCount", "followers"))
				for _, p := range posts {
					if pm, ok := p.(map[string]any); !ok || !mapIGPost(&b, obj(pm), owner) {
						b.Skipped++
					}
				}
				continue
			}
			if !mapIGPost(&b, m, owner) {
				b.Skipped++
				continue
			}
			followers = maxPtr(followers, m.num("ownerFollowersCount", "followersCount"))
		case KindXMentions, KindRedditMentions, KindYTMentions:
			if !mapMention(&b, platform, kind, m, owner) {
				b.Skipped++
			}
		}
	}
	if followers != nil {
		b.Snapshots = append(b.Snapshots, collectors.Snapshot{Platform: platform, Followers: followers})
	}
	return b, nil
}

func mapXPost(b *collectors.Batch, m obj, owner string) bool {
	id := m.str("id", "id_str", "tweetId", "rest_id")
	if id == "" {
		return false
	}
	author := normHandle(m.str("author.userName", "author.username", "author.screen_name", "user.screen_name"))
	if author != "" && owner != "" && author != owner {
		return false // someone else's tweet (retweet in the timeline)
	}
	if m.bool("isRetweet", "retweeted") {
		return false
	}
	url := m.str("url", "twitterUrl", "tweetUrl")
	if url == "" && owner != "" {
		url = "https://x.com/" + owner + "/status/" + id
	}
	extra := map[string]int64{}
	putExtra(extra, "quotes", m.num("quoteCount", "quote_count"))
	putExtra(extra, "bookmarks", m.num("bookmarkCount", "bookmark_count"))
	b.Snapshots = append(b.Snapshots, collectors.Snapshot{
		Platform: "x",
		Content: &collectors.Content{
			ExternalID: id, URL: url, Body: m.str("fullText", "full_text", "text"),
			PublishedAt: m.time("createdAt", "created_at"),
		},
		Likes:    m.num("likeCount", "favorite_count", "favoriteCount", "likes"),
		Views:    m.num("viewCount", "views", "view_count"),
		Reposts:  m.num("retweetCount", "retweet_count", "retweets"),
		Comments: m.num("replyCount", "reply_count", "replies"),
		Extra:    extra,
	})
	return true
}

func mapTikTok(b *collectors.Batch, m obj, owner string) bool {
	id := m.str("id", "videoId", "aweme_id")
	if id == "" {
		return false
	}
	author := normHandle(m.str("authorMeta.name", "author.uniqueId", "authorMeta.uniqueId"))
	if author != "" && owner != "" && author != owner {
		return false
	}
	extra := map[string]int64{}
	putExtra(extra, "saves", m.num("collectCount", "bookmarkCount"))
	putExtra(extra, "reposts", m.num("repostCount"))
	b.Snapshots = append(b.Snapshots, collectors.Snapshot{
		Platform: "tiktok",
		Content: &collectors.Content{
			ExternalID: id, URL: m.str("webVideoUrl", "url", "videoUrl"), Body: m.str("text", "desc", "description"),
			PublishedAt: m.time("createTimeISO", "createTime", "createdAt"),
		},
		Likes:    m.num("diggCount", "likes", "likeCount"),
		Views:    m.num("playCount", "views", "viewCount"),
		Reposts:  m.num("shareCount", "shares"),
		Comments: m.num("commentCount", "comments"),
		Extra:    extra,
	})
	return true
}

func mapIGPost(b *collectors.Batch, m obj, owner string) bool {
	id := m.str("id", "shortCode", "shortcode")
	if id == "" {
		return false
	}
	author := normHandle(m.str("ownerUsername", "owner.username"))
	if author != "" && owner != "" && author != owner {
		return false
	}
	url := m.str("url")
	if url == "" {
		if sc := m.str("shortCode", "shortcode"); sc != "" {
			url = "https://www.instagram.com/p/" + sc + "/"
		}
	}
	b.Snapshots = append(b.Snapshots, collectors.Snapshot{
		Platform: "instagram",
		Content: &collectors.Content{
			ExternalID: id, URL: url, Body: m.str("caption", "text"),
			PublishedAt: m.time("timestamp", "takenAt", "createdAt"),
		},
		Likes:    m.num("likesCount", "likeCount", "likes"),
		Views:    m.num("videoViewCount", "videoPlayCount", "viewCount"),
		Comments: m.num("commentsCount", "commentCount", "comments"),
	})
	return true
}

func mapMention(b *collectors.Batch, platform, kind string, m obj, owner string) bool {
	if dt := m.str("dataType"); dt != "" && dt != "post" && dt != "comment" {
		return false // Reddit scrapers also emit community/user items
	}
	id := m.str("id", "id_str", "tweetId", "parsedId", "cid", "commentId", "videoId")
	// postUrl first: on a Reddit link post, url is the outbound link, not the thread.
	url := m.str("postUrl", "url", "twitterUrl", "link", "permalink")
	if id == "" {
		id = url
	}
	if id == "" {
		return false
	}
	author := m.str("author.userName", "author.username", "user.screen_name", "username", "author",
		"authorName", "channelName", "channel.name", "authorText")
	if owner != "" && (normHandle(author) == owner || normHandle(m.str("channelUsername")) == owner) {
		return false // my own post found by the keyword search
	}
	text := m.str("fullText", "full_text", "text", "body", "comment", "content", "description")
	if title := m.str("title"); title != "" {
		if text == "" {
			text = title
		} else if !strings.HasPrefix(text, title) {
			text = title + "\n\n" + text
		}
	}
	if kind == KindRedditMentions && url != "" && strings.HasPrefix(url, "/") {
		url = "https://www.reddit.com" + url
	}
	k := "mention"
	if owner != "" && normHandle(m.str("inReplyToUsername", "in_reply_to_screen_name")) == owner {
		k = "reply"
	}
	b.Inbound = append(b.Inbound, collectors.Inbound{
		Platform: platform, Kind: k, ExternalID: id, Author: author, Text: text, URL: url,
		CreatedAt: m.time("commentCreatedAt", "createdAt", "created_at", "date", "publishedAt", "publishedTimeText", "timestamp"),
	})
	return true
}

func normHandle(h string) string {
	return strings.ToLower(strings.TrimPrefix(strings.TrimSpace(h), "@"))
}

func putExtra(m map[string]int64, k string, v *int64) {
	if v != nil {
		m[k] = *v
	}
}

func maxPtr(a, b *int64) *int64 {
	if a == nil || (b != nil && *b > *a) {
		return b
	}
	return a
}
