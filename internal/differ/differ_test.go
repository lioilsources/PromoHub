package differ

import (
	"testing"
	"time"
)

func p(n int64) *int64 { return &n }

func TestVars(t *testing.T) {
	now := time.Now()
	cur := Metrics{TakenAt: now, Views: p(3000), Likes: p(50), Followers: p(510), Extra: map[string]int64{"quotes": 2}}
	prev := &Metrics{TakenAt: now.Add(-12 * time.Hour), Views: p(2500), Likes: p(50), Followers: p(490)}
	base := &Metrics{TakenAt: now.Add(-25 * time.Hour), Views: p(1000), Likes: p(20)}

	v := Vars(cur, prev, base)
	want := map[string]float64{
		"views": 3000, "views_delta": 500, "views_delta_24h": 2000,
		"likes_delta": 0, "likes_delta_24h": 30, "followers_delta": 20, "quotes": 2, "hours_since_prev": 12,
	}
	for k, w := range want {
		if v.Num[k] != w {
			t.Errorf("%s = %v, want %v", k, v.Num[k], w)
		}
	}
	if _, ok := v.Num["followers_delta_24h"]; ok {
		t.Error("followers_delta_24h must be absent when the base has no followers")
	}
	if v.Prev["followers"] != 490 {
		t.Errorf("prev followers = %v", v.Prev["followers"])
	}
	d := Deltas(v)
	if d["views_delta_24h"] != 2000 || d["followers_delta"] != 20 {
		t.Errorf("deltas: %v", d)
	}
	if _, ok := d["likes_delta"]; ok {
		t.Error("zero deltas are dropped")
	}
}

func TestVarsFirstSnapshot(t *testing.T) {
	v := Vars(Metrics{TakenAt: time.Now(), Views: p(10)}, nil, nil)
	if len(v.Prev) != 0 || v.Num["views"] != 10 {
		t.Fatalf("%+v", v)
	}
	if _, ok := v.Num["views_delta"]; ok {
		t.Fatal("no delta without a predecessor")
	}
}
