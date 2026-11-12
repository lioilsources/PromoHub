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
