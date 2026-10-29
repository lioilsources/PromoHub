package rules

import (
	"testing"

	"github.com/lioilsources/promohub/internal/config"
)

func TestCompileAndEval(t *testing.T) {
	v := Vars{
		Num:  map[string]float64{"rating": 2, "views_delta_24h": 1500, "likes_delta_24h": 10, "followers": 520},
		Str:  map[string]string{"text": "Just tried hajánek, love it", "platform": "x"},
		Prev: map[string]float64{"followers": 480},
	}
	cases := []struct {
		expr string
		want bool
	}{
		{"", true},
		{"rating <= 2", true},
		{"rating < 2", false},
		{"views_delta_24h > 1000 or likes_delta_24h > 100", true},
		{"views_delta_24h > 1000 and likes_delta_24h > 100", false},
		{`text matches /(SwypeKids|Hajánek|OrbitronTactics|olin\.now)/i`, true},
		{`text matches /SwypeKids/i`, false},
		{"followers crosses [100, 500, 1000, 5000]", true},
		{"followers crosses [1000]", false},
		{`platform == "X"`, true},
		{`platform != "x" || rating == 2`, true},
		{"not (rating > 3)", true},
		{"!(rating <= 2) && rating >= 1", false},
		{"missing > 1", false},
		{`text contains "LOVE"`, true},
		{"likes_delta_24h >= 1_000", false},
	}
	for _, c := range cases {
		n, err := Compile(c.expr)
		if err != nil {
			t.Fatalf("%q: %v", c.expr, err)
		}
		if got := n.eval(v); got != c.want {
			t.Errorf("%q = %v, want %v", c.expr, got, c.want)
		}
	}
}

func TestCrossesNeedsPrevious(t *testing.T) {
	n, err := Compile("followers crosses [100]")
	if err != nil {
		t.Fatal(err)
	}
	if n.eval(Vars{Num: map[string]float64{"followers": 150}}) {
		t.Fatal("first snapshot must not fire a milestone")
	}
	if n.eval(Vars{Num: map[string]float64{"followers": 90}, Prev: map[string]float64{"followers": 150}}) {
		t.Fatal("falling below a threshold is not crossing it")
	}
}

func TestCompileErrors(t *testing.T) {
	for _, e := range []string{
		"rating <=", "rating > \"x\"", "(rating > 1", "rating >> 1", "text matches /a/x",
		"followers crosses []", "followers crosses 100", "rating > 1 rating", "text matches /unterminated",
	} {
		if _, err := Compile(e); err == nil {
			t.Errorf("%q: expected error", e)
		}
	}
}

func TestEngineTargets(t *testing.T) {
	cfg := config.Rules{
		Rules: []config.RuleSpec{
			{Name: "new_review", On: OnInboundReview, Then: config.StringSet{"ifttt:promo_review"}},
			{Name: "negative_review", On: OnInboundReview, When: "rating <= 2", Then: config.StringSet{"ifttt:promo_review_urgent", "telegram:urgent"}},
		},
		Sinks: config.Sinks{Telegram: map[string]string{"urgent": "123"}},
	}
	e, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if m := e.Evaluate(OnInboundReview, Vars{Num: map[string]float64{"rating": 1}}); len(m) != 2 {
		t.Fatalf("got %d matches, want 2", len(m))
	}
	if m := e.Evaluate(OnInboundReview, Vars{Num: map[string]float64{"rating": 5}}); len(m) != 1 {
		t.Fatalf("got %d matches, want 1", len(m))
	}

	bad := []config.Rules{
		{Rules: []config.RuleSpec{{Name: "a", On: "inbound.nope", Then: config.StringSet{"ifttt:x"}}}},
		{Rules: []config.RuleSpec{{Name: "a", On: OnInboundReview, Then: config.StringSet{"telegram:missing"}}}},
		{Rules: []config.RuleSpec{{Name: "a", On: OnInboundReview, Then: config.StringSet{"ifttt:bad/event"}}}},
		{Rules: []config.RuleSpec{{Name: "a", On: OnInboundReview}}},
	}
	for i, b := range bad {
		if _, err := New(b); err == nil {
			t.Errorf("bad config %d: expected error", i)
		}
	}
}
