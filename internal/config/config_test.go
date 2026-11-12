package config

import (
	"strings"
	"testing"

	"github.com/lioilsources/promohub/internal/platform"
)

func TestAccountDefaultsAndValidation(t *testing.T) {
	f := &Files{
		Apps: []App{{Slug: "swypekids", Name: "SwypeKids"}},
		Accounts: Accounts{Accounts: []Account{
			{Platform: "x", Handle: "ol1n"},
			{Platform: "mastodon", Handle: "ol1n@mastodon.social", Lang: "any", Apps: []string{"swypekids"}},
			{Platform: "youtube", Handle: "ol1n", Publish: "api"},
		}},
	}
	if err := f.validate(); err != nil {
		t.Fatal(err)
	}
	x, m, y := f.Accounts.Accounts[0], f.Accounts.Accounts[1], f.Accounts.Accounts[2]
	if x.Read != platform.ViaApify || x.Publish != platform.ViaIFTTT || x.IFTTTEvent != "publish_x" || x.Lang != "en" {
		t.Fatalf("x defaults: %+v", x)
	}
	if m.APIHost() != "mastodon.social" || m.User() != "ol1n" || !m.Speaks("cs") || m.Promotes("hajanek") || !m.Promotes("swypekids") {
		t.Fatalf("mastodon: %+v", m)
	}
	if y.Publish != platform.ViaAPI {
		t.Fatal("explicit publish path must win")
	}
	for _, bad := range []struct {
		acct Account
		want string
	}{
		{Account{Platform: "myspace", Handle: "a"}, "unknown platform"},
		{Account{Platform: "mastodon", Handle: "nohost"}, "user@instance"},
		{Account{Platform: "x", Handle: "a", Publish: "api"}, "publish"},
		{Account{Platform: "bluesky", Handle: "a", Apps: []string{"nope"}}, "unknown app"},
	} {
		f := &Files{Apps: []App{{Slug: "swypekids", Name: "SwypeKids"}}, Accounts: Accounts{Accounts: []Account{bad.acct}}}
		if err := f.validate(); err == nil || !strings.Contains(err.Error(), bad.want) {
			t.Errorf("%+v: err %v, want %q", bad.acct, err, bad.want)
		}
	}
	dup := &Files{Accounts: Accounts{Accounts: []Account{{Platform: "x", Handle: "a"}, {Platform: "x", Handle: "a"}}}}
	if err := dup.validate(); err == nil {
		t.Fatal("duplicate accounts must fail")
	}
}
