// Package metrics is a minimal Prometheus text-format counter registry (no client library needed
// for a handful of counters).
package metrics

import (
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
)

type Registry struct {
	mu     sync.Mutex
	help   map[string]string
	gauges map[string]bool
	values map[string]map[string]float64 // name → rendered labels → value
}

func New() *Registry {
	return &Registry{help: map[string]string{}, gauges: map[string]bool{}, values: map[string]map[string]float64{}}
}

// Help registers a counter description so it shows up even before the first increment.
func (r *Registry) Help(name, text string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.help[name] = text
	if r.values[name] == nil {
		r.values[name] = map[string]float64{}
	}
}

// Inc adds 1. labels are key, value pairs.
func (r *Registry) Inc(name string, labels ...string) { r.Add(name, 1, labels...) }

func (r *Registry) Add(name string, v float64, labels ...string) {
	k := renderLabels(labels)
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.values[name] == nil {
		r.values[name] = map[string]float64{}
	}
	r.values[name][k] += v
}

// Set stores a gauge value (replacing the previous one).
func (r *Registry) Set(name string, v float64, labels ...string) {
	r.mu.Lock()
	r.gauges[name] = true
	if r.values[name] == nil {
		r.values[name] = map[string]float64{}
	}
	r.values[name][renderLabels(labels)] = v
	r.mu.Unlock()
}

func renderLabels(labels []string) string {
	var b strings.Builder
	for i := 0; i+1 < len(labels); i += 2 {
		if b.Len() > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, "%s=%q", labels[i], labels[i+1])
	}
	return b.String()
}

func (r *Registry) Write(w io.Writer) {
	r.mu.Lock()
	defer r.mu.Unlock()
	names := make([]string, 0, len(r.values))
	for n := range r.values {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		if h := r.help[n]; h != "" {
			fmt.Fprintf(w, "# HELP %s %s\n", n, h)
		}
		typ := "counter"
		if r.gauges[n] {
			typ = "gauge"
		}
		fmt.Fprintf(w, "# TYPE %s %s\n", n, typ)
		keys := make([]string, 0, len(r.values[n]))
		for k := range r.values[n] {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if k == "" {
				fmt.Fprintf(w, "%s %g\n", n, r.values[n][k])
			} else {
				fmt.Fprintf(w, "%s{%s} %g\n", n, k, r.values[n][k])
			}
		}
	}
}

func (r *Registry) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		r.Write(w)
	})
}
