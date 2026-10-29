// Package rules compiles rules.yaml and decides which targets an observation fires.
package rules

import (
	"fmt"
	"strings"

	"github.com/lioilsources/promohub/internal/config"
)

// Triggers a rule can listen on.
const (
	OnInboundComment = "inbound.comment"
	OnInboundReview  = "inbound.review"
	OnInboundMention = "inbound.mention"
	OnInboundReply   = "inbound.reply"
	OnMetricSnapshot = "metric.snapshot" // every stored snapshot
	OnMetricDelta    = "metric.delta"    // a snapshot that has a predecessor to diff against
	OnContentNew     = "content.new"     // first sighting of one of my posts
)

var validOn = map[string]bool{
	OnInboundComment: true, OnInboundReview: true, OnInboundMention: true, OnInboundReply: true,
	OnMetricSnapshot: true, OnMetricDelta: true, OnContentNew: true,
}

// Target kinds. ifttt:<event> goes to the IFTTT Webhooks service, the others to a named sink.
const (
	TargetIFTTT    = "ifttt"
	TargetTelegram = "telegram"
	TargetDiscord  = "discord"
)

type Rule struct {
	Name    string
	On      string
	When    string
	Targets []string
	cond    node
}

type Engine struct {
	rules []Rule
}

// Match is one rule that fired.
type Match struct {
	Rule    string
	Targets []string
}

func New(cfg config.Rules) (*Engine, error) {
	e := &Engine{}
	seen := map[string]bool{}
	for i, s := range cfg.Rules {
		name := s.Name
		if name == "" {
			name = fmt.Sprintf("rule#%d", i+1)
		}
		if seen[name] {
			return nil, fmt.Errorf("rules.yaml: duplicate rule %q", name)
		}
		seen[name] = true
		if !validOn[s.On] {
			return nil, fmt.Errorf("rules.yaml: rule %q: unknown trigger %q", name, s.On)
		}
		cond, err := Compile(s.When)
		if err != nil {
			return nil, fmt.Errorf("rules.yaml: rule %q: when: %w", name, err)
		}
		if len(s.Then) == 0 {
			return nil, fmt.Errorf("rules.yaml: rule %q: empty then", name)
		}
		for _, t := range s.Then {
			if err := checkTarget(t, cfg.Sinks); err != nil {
				return nil, fmt.Errorf("rules.yaml: rule %q: %w", name, err)
			}
		}
		e.rules = append(e.rules, Rule{Name: name, On: s.On, When: s.When, Targets: s.Then, cond: cond})
	}
	return e, nil
}

func checkTarget(t string, sinks config.Sinks) error {
	kind, name, ok := strings.Cut(t, ":")
	if !ok || name == "" {
		return fmt.Errorf("target %q must look like kind:name", t)
	}
	switch kind {
	case TargetIFTTT:
		for _, c := range name {
			// IFTTT event names end up in the URL path.
			if !(c == '_' || c == '-' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9') {
				return fmt.Errorf("target %q: IFTTT event names may only contain letters, digits, _ and -", t)
			}
		}
	case TargetTelegram:
		if sinks.Telegram[name] == "" {
			return fmt.Errorf("target %q: sinks.telegram.%s is not configured", t, name)
		}
	case TargetDiscord:
		if sinks.Discord[name] == "" {
			return fmt.Errorf("target %q: sinks.discord.%s is not configured", t, name)
		}
	default:
		return fmt.Errorf("target %q: unknown kind %q", t, kind)
	}
	return nil
}

// Evaluate returns every rule listening on `on` whose condition holds.
func (e *Engine) Evaluate(on string, v Vars) []Match {
	var out []Match
	for _, r := range e.rules {
		if r.On == on && r.cond.eval(v) {
			out = append(out, Match{Rule: r.Name, Targets: r.Targets})
		}
	}
	return out
}

func (e *Engine) Rules() []Rule { return e.rules }
