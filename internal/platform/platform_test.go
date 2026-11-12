package platform

import "testing"

func TestExternalID(t *testing.T) {
	cases := []struct{ platform, url, want string }{
		{"youtube", "https://www.youtube.com/watch?v=abc123&t=1", "abc123"},
		{"youtube", "https://youtu.be/abc123", "abc123"},
		{"youtube", "https://www.youtube.com/shorts/xyz", "xyz"},
		{"bluesky", "https://bsky.app/profile/ol1n.bsky.social/post/3kabc", "3kabc"},
		{"instagram", "https://www.instagram.com/reel/Cx1/", "Cx1"},
		{"instagram", "https://www.instagram.com/p/DeTUk4sDSp-/", "DeTUk4sDSp-"},
		{"threads", "https://www.threads.net/@ol1n/post/C9xyz", "C9xyz"},
		{"mastodon", "https://mastodon.social/@Gargron/117598326059120853", "117598326059120853"},
		{"mastodon", "https://mastodon.social/@Gargron", "https://mastodon.social/@Gargron"},
		{"x", "https://x.com/ol1n/status/2117421134232621467", "2117421134232621467"},
		{"tiktok", "https://www.tiktok.com/@nasa/video/7665075736742530317", "7665075736742530317"},
		{"reddit", "https://www.reddit.com/r/golang/comments/1xmf82s/hiring/", "1xmf82s"},
		{"hackernews", "https://news.ycombinator.com/item?id=42", "42"},
		{"youtube", "not a url", "not a url"},
	}
	for _, c := range cases {
		if got := ExternalID(c.platform, c.url); got != c.want {
			t.Errorf("%s %s = %q, want %q", c.platform, c.url, got, c.want)
		}
	}
}

func TestRegistryIsConsistent(t *testing.T) {
	for _, s := range All() {
		if !Allows(s.ReadVias, s.Read) || !Allows(s.PubVias, s.Publish) {
			t.Errorf("%s: default path not allowed", s.Name)
		}
		if s.Read == ViaAPI && len(s.Capabilities()) == 0 {
			t.Errorf("%s: read via api without intervals", s.Name)
		}
	}
}

func TestLength(t *testing.T) {
	bsky, _ := Lookup("bluesky")
	x, _ := Lookup("x")
	masto, _ := Lookup("mastodon")
	// "👨‍👩‍👧" is one grapheme of five code points.
	if n := bsky.Length("Hajánek 👨‍👩‍👧"); n != 9 {
		t.Errorf("bluesky graphemes = %d", n)
	}
	if n := masto.Length("see https://olin.now/pass?a=very-long-query-string-indeed"); n != 4+23 {
		t.Errorf("mastodon url weight = %d", n)
	}
	if n := x.Length("ahoj 日本 https://x.co"); n != 5+4+1+23 { // "ahoj ", 日本 at weight 2, the space before the URL
		t.Errorf("x weighted = %d", n)
	}
	long := ""
	for i := 0; i < 80; i++ {
		long += "word "
	}
	out := bsky.TrimTo(long, "#swypekids")
	if !bsky.Fits(out) || !hasSuffix(out, "…\n\n#swypekids") {
		t.Errorf("trim: %d %q", bsky.Length(out), out)
	}
}

func hasSuffix(s, suf string) bool { return len(s) >= len(suf) && s[len(s)-len(suf):] == suf }

func TestIntent(t *testing.T) {
	if got := Intent("hackernews", "", "", "https://olin.now", "Show HN: SwypeKids"); got != "https://news.ycombinator.com/submitlink?t=Show+HN%3A+SwypeKids&u=https%3A%2F%2Folin.now" {
		t.Error(got)
	}
	if got := Intent("mastodon", "mastodon.social", "ahoj", "", ""); got != "https://mastodon.social/share?text=ahoj" {
		t.Error(got)
	}
	if Intent("instagram", "", "a", "", "") != "" {
		t.Error("instagram has no web compose")
	}
}
