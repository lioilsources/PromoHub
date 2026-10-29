package apifysrc

import (
	"encoding/json"
	"testing"
)

func items(t *testing.T, js string) []json.RawMessage {
	t.Helper()
	var out []json.RawMessage
	if err := json.Unmarshal([]byte(js), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestXProfile(t *testing.T) {
	b, err := Map(KindXProfile, "@Ol1n", items(t, `[
		{"id":"1","url":"https://x.com/ol1n/status/1","text":"hello","likeCount":12,"retweetCount":3,"replyCount":2,
		 "viewCount":"1.2K","quoteCount":1,"createdAt":"Tue Oct 28 10:00:00 +0000 2026",
		 "author":{"userName":"ol1n","followers":480}},
		{"id":"2","text":"RT someone","author":{"userName":"someone","followers":99999}},
		{"text":"no id"}
	]`))
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Snapshots) != 2 || b.Skipped != 2 {
		t.Fatalf("snapshots=%d skipped=%d", len(b.Snapshots), b.Skipped)
	}
	p := b.Snapshots[0]
	if p.Content == nil || p.Content.ExternalID != "1" || *p.Likes != 12 || *p.Views != 1200 || p.Extra["quotes"] != 1 {
		t.Fatalf("post: %+v", p)
	}
	if p.Content.PublishedAt == nil || p.Content.PublishedAt.Day() != 28 {
		t.Fatalf("published: %v", p.Content.PublishedAt)
	}
	// Followers come from my own tweets only, not from the retweeted account.
	acct := b.Snapshots[1]
	if acct.Content != nil || *acct.Followers != 480 {
		t.Fatalf("account: %+v", acct)
	}
}

func TestMentionsSkipOwnPosts(t *testing.T) {
	b, err := Map(KindXMentions, "ol1n", items(t, `[
		{"id":"10","text":"SwypeKids is great","author":{"userName":"fan"},"url":"https://x.com/fan/status/10"},
		{"id":"11","text":"my own SwypeKids post","author":{"userName":"Ol1n"}},
		{"id":"12","text":"@ol1n thanks","author":{"userName":"fan2"},"inReplyToUsername":"ol1n"}
	]`))
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Inbound) != 2 || b.Skipped != 1 {
		t.Fatalf("inbound=%d skipped=%d", len(b.Inbound), b.Skipped)
	}
	if b.Inbound[0].Kind != "mention" || b.Inbound[1].Kind != "reply" || b.Inbound[0].Author != "fan" {
		t.Fatalf("%+v", b.Inbound)
	}
}

func TestReddit(t *testing.T) {
	b, err := Map(KindRedditMentions, "", items(t, `[
		{"id":"t3_abc","parsedId":"abc","url":"/r/kids/comments/abc/x/","title":"Hajánek app?","body":"anyone tried it","username":"u1","createdAt":"2026-10-28T09:00:00.000Z","dataType":"post"}
	]`))
	if err != nil {
		t.Fatal(err)
	}
	in := b.Inbound[0]
	if in.ExternalID != "t3_abc" || in.URL != "https://www.reddit.com/r/kids/comments/abc/x/" || in.Text != "Hajánek app?\n\nanyone tried it" || in.CreatedAt == nil {
		t.Fatalf("%+v", in)
	}
}

func TestTikTokAndInstagram(t *testing.T) {
	b, err := Map(KindTikTokProfile, "ol1n", items(t, `[
		{"id":"7","text":"clip","webVideoUrl":"https://tiktok.com/@ol1n/video/7","diggCount":5,"playCount":100,"shareCount":1,"commentCount":2,"createTime":1761645600,"authorMeta":{"name":"ol1n","fans":50}}
	]`))
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Snapshots) != 2 || *b.Snapshots[0].Views != 100 || b.Snapshots[0].Content.PublishedAt == nil || *b.Snapshots[1].Followers != 50 {
		t.Fatalf("tiktok: %+v", b)
	}

	b, err = Map(KindIGProfile, "ol1n", items(t, `[
		{"username":"ol1n","followersCount":321,"latestPosts":[
			{"id":"a","shortCode":"Ab","caption":"post","likesCount":9,"commentsCount":1,"timestamp":"2026-10-27T08:00:00.000Z","ownerUsername":"ol1n"}
		]}
	]`))
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Snapshots) != 2 || b.Snapshots[0].Content.URL != "https://www.instagram.com/p/Ab/" || *b.Snapshots[1].Followers != 321 {
		t.Fatalf("ig: %+v", b)
	}
}

func TestUnknownKind(t *testing.T) {
	if _, err := Map("nope", "", nil); err == nil {
		t.Fatal("expected error")
	}
}
