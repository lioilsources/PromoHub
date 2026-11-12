package apifysrc

import "testing"

// Fixtures below are trimmed items from real actor runs on 2026-11-03 (only the fields the
// mapping reads, plus a few look-alikes that must be ignored).

func TestRealXSearch(t *testing.T) {
	b, err := Map(KindXMentions, "timmyisagod", items(t, `[
		{"type":"tweet","id":"2117421134232621467","url":"https://x.com/0xdevshm/status/2117421134232621467",
		 "twitterUrl":"https://twitter.com/0xdevshm/status/2117421134232621467","createdAt":"Tue Nov 03 00:58:32 +0000 2026",
		 "fullText":"Spot the bug: #golang","text":"Spot the bug","isRetweet":false,"isReply":false,"likeCount":0,"viewCount":3,
		 "author":{"userName":"0xdevshm","followers":128}},
		{"type":"tweet","id":"2117414447627002115","url":"https://x.com/Princemave123/status/2117414447627002115",
		 "createdAt":"Tue Nov 03 00:31:58 +0000 2026","fullText":"@timmyisagod Boss, I'm a Golang dev","isReply":true,
		 "inReplyToUsername":"timmyisagod","author":{"userName":"Princemave123","followers":1254}},
		{"noResults":true}
	]`))
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Inbound) != 2 || b.Skipped != 1 {
		t.Fatalf("inbound=%d skipped=%d", len(b.Inbound), b.Skipped)
	}
	in := b.Inbound[0]
	if in.Text != "Spot the bug: #golang" || in.URL != "https://x.com/0xdevshm/status/2117421134232621467" ||
		in.CreatedAt == nil || in.CreatedAt.Hour() != 0 || in.CreatedAt.Minute() != 58 {
		t.Fatalf("%+v", in)
	}
	if b.Inbound[1].Kind != "reply" {
		t.Fatalf("reply: %+v", b.Inbound[1])
	}
}

func TestRealRedditHarshmaur(t *testing.T) {
	b, err := Map(KindRedditMentions, "", items(t, `[
		{"dataType":"comment","id":"19s1w9r376ow5",
		 "url":"https://www.reddit.com/r/Artificials/comments/1xmb2bw/pythons_readability/19s1w9r376ow5/",
		 "authorName":"YoghiThorn","postTitle":"Python's readability may not matter","body":"Golang",
		 "commentCreatedAt":"2026-11-02T23:55:29.000Z"},
		{"dataType":"post","id":"t3_1xmf82s","parsedId":"1xmf82s","url":"https://jobboardsearch.com/out",
		 "postUrl":"https://www.reddit.com/r/jobboardsearch/comments/1xmf82s/hiring/","authorName":"rrmdp",
		 "title":"[Hiring] Go engineer","body":"details","createdAt":"2026-11-03T01:04:05.000Z"},
		{"dataType":"community","id":"t5_2rc7j","title":"r/golang","url":"https://www.reddit.com/r/golang/"}
	]`))
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Inbound) != 2 || b.Skipped != 1 {
		t.Fatalf("inbound=%d skipped=%d", len(b.Inbound), b.Skipped)
	}
	c, p := b.Inbound[0], b.Inbound[1]
	if c.ExternalID != "19s1w9r376ow5" || c.Author != "YoghiThorn" || c.Text != "Golang" || c.CreatedAt == nil {
		t.Fatalf("comment: %+v", c)
	}
	if p.URL != "https://www.reddit.com/r/jobboardsearch/comments/1xmf82s/hiring/" || p.Text != "[Hiring] Go engineer\n\ndetails" {
		t.Fatalf("post: %+v", p)
	}
}

func TestRealYouTubeSearch(t *testing.T) {
	b, err := Map(KindYTMentions, "TechWorldwithNana", items(t, `[
		{"type":"video","id":"yyUHQIec83I","url":"https://www.youtube.com/watch?v=yyUHQIec83I","title":"Golang Tutorial",
		 "text":"Full Golang Tutorial","date":"2021-12-20T15:05:11.000Z","channelName":"TechWorld with Nana",
		 "channelUsername":"TechWorldwithNana"},
		{"type":"video","id":"abc","url":"https://www.youtube.com/watch?v=abc","title":"Go in 100s","text":null,
		 "date":"2026-10-01T10:00:00.000Z","channelName":"Fireship","channelUsername":"Fireship"}
	]`))
	if err != nil {
		t.Fatal(err)
	}
	// The first video is mine (handle matches channelUsername, not the display name).
	if len(b.Inbound) != 1 || b.Skipped != 1 {
		t.Fatalf("inbound=%d skipped=%d", len(b.Inbound), b.Skipped)
	}
	if in := b.Inbound[0]; in.Text != "Go in 100s" || in.Author != "Fireship" || in.CreatedAt == nil {
		t.Fatalf("%+v", in)
	}
}

func TestRealTikTokProfile(t *testing.T) {
	b, err := Map(KindTikTokProfile, "nasa", items(t, `[
		{"id":"7665075736742530317","webVideoUrl":"https://www.tiktok.com/@nasa/video/7665075736742530317",
		 "text":"Something big just landed on TikTok.","createTime":1784664542,"createTimeISO":"2026-07-21T20:09:02.000Z",
		 "diggCount":109500,"playCount":1700000,"shareCount":3333,"repostCount":0,"commentCount":3255,"collectCount":7923,
		 "isPinned":true,"authorMeta":{"name":"nasa","fans":2200000}},
		{"error":"Profile not found","errorCode":"NOT_FOUND","url":"https://www.tiktok.com/@nobody","input":"nobody"}
	]`))
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Snapshots) != 2 || b.Skipped != 1 {
		t.Fatalf("snapshots=%d skipped=%d", len(b.Snapshots), b.Skipped)
	}
	v := b.Snapshots[0]
	if *v.Likes != 109500 || *v.Views != 1700000 || v.Extra["saves"] != 7923 || v.Content.PublishedAt.Month() != 7 {
		t.Fatalf("%+v", v)
	}
	if *b.Snapshots[1].Followers != 2200000 {
		t.Fatalf("followers: %+v", b.Snapshots[1])
	}
}

func TestRealInstagramProfile(t *testing.T) {
	b, err := Map(KindIGProfile, "nasa", items(t, `[
		{"username":"nasa","followersCount":104425748,"postsCount":4987,"latestPosts":[
			{"id":"4004634989362489982","type":"Video","shortCode":"DeTUk4sDSp-","caption":"POV: Earth set",
			 "url":"https://www.instagram.com/p/DeTUk4sDSp-/","commentsCount":7723,"likesCount":770225,
			 "videoViewCount":3468670,"timestamp":"2026-10-10T15:15:02.000Z","ownerUsername":"nasa"},
			{"id":"4001971394119634476","type":"Image","shortCode":"DeJ28eTxo4s","caption":"collab",
			 "likesCount":299176,"timestamp":"2026-10-06T13:10:11.000Z","ownerUsername":"nasawebb"}
		]}
	]`))
	if err != nil {
		t.Fatal(err)
	}
	// Collab posts owned by another account show up in latestPosts and must not count as mine.
	if len(b.Snapshots) != 2 || b.Skipped != 1 {
		t.Fatalf("snapshots=%d skipped=%d", len(b.Snapshots), b.Skipped)
	}
	if p := b.Snapshots[0]; *p.Views != 3468670 || p.Content.URL != "https://www.instagram.com/p/DeTUk4sDSp-/" || p.Content.ExternalID != "DeTUk4sDSp-" {
		t.Fatalf("%+v", p)
	}
	if *b.Snapshots[1].Followers != 104425748 {
		t.Fatalf("followers: %+v", b.Snapshots[1])
	}
}
