package summary

import (
	"strings"
	"testing"
	"time"
)

func TestParseSince(t *testing.T) {
	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	day := 24 * time.Hour
	tests := []struct {
		in   string
		want time.Time
	}{
		{"30s", now.Add(-30 * time.Second)},
		{"90m", now.Add(-90 * time.Minute)},
		{"1h30m", now.Add(-90 * time.Minute)},
		{"3d", now.Add(-3 * day)},
		{"2w", now.Add(-14 * day)},
		{"1w2d", now.Add(-9 * day)},
		{"1w2d12h", now.Add(-9*day - 12*time.Hour)},
		{"2026-09-01T08:30:00Z", time.Date(2026, 9, 1, 8, 30, 0, 0, time.UTC)},
		{"2026-09-01T08:30:00+02:00", time.Date(2026, 9, 1, 6, 30, 0, 0, time.UTC)},
		{"2026-09-01", time.Date(2026, 9, 1, 0, 0, 0, 0, time.Local)},
	}
	for _, tc := range tests {
		t.Run(tc.in, func(t *testing.T) {
			got, err := ParseSince(tc.in, now)
			if err != nil {
				t.Fatalf("ParseSince(%q): %v", tc.in, err)
			}
			if !got.Equal(tc.want) {
				t.Fatalf("ParseSince(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}

	for _, bad := range []string{"", "  ", "-1h", "0", "0s", "0d", "yesterday", "2d1w", "1.5d", "2026-13-40", "12h-"} {
		t.Run("rejects "+bad, func(t *testing.T) {
			_, err := ParseSince(bad, now)
			if err == nil {
				t.Fatalf("ParseSince(%q) must fail", bad)
			}
			for _, form := range []string{"duration", "RFC 3339", "YYYY-MM-DD"} {
				if !strings.Contains(err.Error(), form) {
					t.Fatalf("error %q must name the accepted form %q", err.Error(), form)
				}
			}
		})
	}
}
