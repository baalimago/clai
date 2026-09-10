package summary

import (
	"errors"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var errSinceForm = errors.New("since: expected a duration (30m, 12h, 3d, 1w, 1w2d), an RFC 3339 timestamp or a YYYY-MM-DD date")

var extendedDuration = regexp.MustCompile(`^(?:(\d+)w)?(?:(\d+)d)?(.*)$`)

// ParseSince resolves a window value to an instant: an extended Go duration
// (weeks and days before the standard units) subtracted from now, an RFC
// 3339 timestamp, or a date at local midnight (D4).
func ParseSince(s string, now time.Time) (time.Time, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, errSinceForm
	}
	if d, ok := parseExtendedDuration(s); ok {
		if d <= 0 {
			return time.Time{}, errSinceForm
		}
		return now.Add(-d), nil
	}
	if ts, err := time.Parse(time.RFC3339, s); err == nil {
		return ts, nil
	}
	if day, err := time.ParseInLocation("2006-01-02", s, time.Local); err == nil {
		return day, nil
	}
	return time.Time{}, errSinceForm
}

func parseExtendedDuration(s string) (time.Duration, bool) {
	m := extendedDuration.FindStringSubmatch(s)
	if m == nil || (m[1] == "" && m[2] == "" && m[3] == "") {
		return 0, false
	}
	var total time.Duration
	for i, unit := range []time.Duration{7 * 24 * time.Hour, 24 * time.Hour} {
		if m[i+1] == "" {
			continue
		}
		n, err := strconv.Atoi(m[i+1])
		if err != nil {
			return 0, false
		}
		total += time.Duration(n) * unit
	}
	if m[3] != "" {
		if strings.HasPrefix(m[3], "-") {
			return 0, false
		}
		rest, err := time.ParseDuration(m[3])
		if err != nil {
			return 0, false
		}
		total += rest
	}
	return total, true
}
