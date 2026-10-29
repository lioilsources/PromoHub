// Package differ turns a new metric snapshot plus its stored predecessors into rule variables.
package differ

import (
	"time"

	"github.com/lioilsources/promohub/internal/rules"
)

// Metrics is one point of a metric series.
type Metrics struct {
	TakenAt   time.Time
	Likes     *int64
	Views     *int64
	Reposts   *int64
	Comments  *int64
	Followers *int64
	Extra     map[string]int64
}

func (m Metrics) fields() map[string]*int64 {
	return map[string]*int64{
		"likes": m.Likes, "views": m.Views, "reposts": m.Reposts,
		"comments": m.Comments, "followers": m.Followers,
	}
}

// Vars builds the variables for metric.* rules:
//
//	likes, views, …          current values (plus Extra keys, e.g. quotes)
//	likes_delta, …           change since the previous snapshot
//	likes_delta_24h, …       change since the last snapshot at least 24 h old, or since the
//	                         oldest one when the series is younger than that
//	hours_since_prev         hours between prev and cur
//
// prev and base may be nil. Prev values feed `crosses`.
func Vars(cur Metrics, prev, base *Metrics) rules.Vars {
	v := rules.Vars{Num: map[string]float64{}, Str: map[string]string{}, Prev: map[string]float64{}}
	for k, x := range cur.Extra {
		v.Num[k] = float64(x)
	}
	for k, x := range cur.fields() {
		if x == nil {
			continue
		}
		v.Num[k] = float64(*x)
		if prev != nil {
			if p := prev.fields()[k]; p != nil {
				v.Prev[k] = float64(*p)
				v.Num[k+"_delta"] = float64(*x - *p)
			}
		}
		if base != nil {
			if b := base.fields()[k]; b != nil {
				v.Num[k+"_delta_24h"] = float64(*x - *b)
			}
		}
	}
	if prev != nil {
		v.Num["hours_since_prev"] = cur.TakenAt.Sub(prev.TakenAt).Hours()
	}
	return v
}

// Deltas returns the non-zero *_delta and *_delta_24h values for event payloads.
func Deltas(v rules.Vars) map[string]int64 {
	out := map[string]int64{}
	for k, x := range v.Num {
		if len(k) > 6 && (k[len(k)-6:] == "_delta" || (len(k) > 10 && k[len(k)-10:] == "_delta_24h")) && x != 0 {
			out[k] = int64(x)
		}
	}
	return out
}
