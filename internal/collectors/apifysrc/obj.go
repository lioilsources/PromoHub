package apifysrc

import (
	"math"
	"strconv"
	"strings"
	"time"
)

// obj is a decoded dataset item with lookups by dotted path ("author.userName").
type obj map[string]any

func (m obj) get(path string) any {
	var cur any = map[string]any(m)
	for _, p := range strings.Split(path, ".") {
		mm, ok := cur.(map[string]any)
		if !ok {
			return nil
		}
		if cur, ok = mm[p]; !ok {
			return nil
		}
	}
	return cur
}

// str returns the first non-empty candidate as a string; numbers are formatted without exponent.
func (m obj) str(paths ...string) string {
	for _, p := range paths {
		switch v := m.get(p).(type) {
		case string:
			if s := strings.TrimSpace(v); s != "" {
				return s
			}
		case float64:
			return strconv.FormatFloat(v, 'f', -1, 64)
		}
	}
	return ""
}

// num returns the first candidate that is a number or a numeric string ("1,234", "1.2K").
func (m obj) num(paths ...string) *int64 {
	for _, p := range paths {
		switch v := m.get(p).(type) {
		case float64:
			n := int64(math.Round(v))
			return &n
		case string:
			if n, ok := parseCount(v); ok {
				return &n
			}
		}
	}
	return nil
}

func (m obj) bool(paths ...string) bool {
	for _, p := range paths {
		if v, ok := m.get(p).(bool); ok && v {
			return true
		}
	}
	return false
}

var timeLayouts = []string{
	time.RFC3339Nano,
	time.RFC3339,
	"Mon Jan 02 15:04:05 -0700 2006", // classic Twitter
	"2006-01-02T15:04:05.000Z",
	"2006-01-02 15:04:05",
	"2006-01-02",
}

// time accepts RFC 3339-ish strings and unix timestamps in seconds or milliseconds.
func (m obj) time(paths ...string) *time.Time {
	for _, p := range paths {
		switch v := m.get(p).(type) {
		case string:
			for _, l := range timeLayouts {
				if t, err := time.Parse(l, strings.TrimSpace(v)); err == nil {
					t = t.UTC()
					return &t
				}
			}
			if f, err := strconv.ParseFloat(v, 64); err == nil {
				if t, ok := unix(f); ok {
					return &t
				}
			}
		case float64:
			if t, ok := unix(v); ok {
				return &t
			}
		}
	}
	return nil
}

func unix(f float64) (time.Time, bool) {
	switch {
	case f > 1e12:
		return time.UnixMilli(int64(f)).UTC(), true
	case f > 1e9:
		return time.Unix(int64(f), 0).UTC(), true
	}
	return time.Time{}, false
}

func parseCount(s string) (int64, bool) {
	s = strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(s), ",", ""))
	if s == "" {
		return 0, false
	}
	mult := 1.0
	switch s[len(s)-1] {
	case 'K':
		mult, s = 1e3, s[:len(s)-1]
	case 'M':
		mult, s = 1e6, s[:len(s)-1]
	case 'B':
		mult, s = 1e9, s[:len(s)-1]
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, false
	}
	return int64(math.Round(f * mult)), true
}
