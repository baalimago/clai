package tools

import (
	"strings"
	"testing"
)

func TestDate_Default(t *testing.T) {
	out, err := Date.Call(map[string]any{})
	if err != nil {
		t.Fatalf("Date.Call default returned error: %v", err)
	}
	if strings.TrimSpace(out) == "" {
		t.Fatalf("expected non-empty output from date")
	}
}

func TestDate_Unix(t *testing.T) {
	out, err := Date.Call(map[string]any{"unix": true})
	if err != nil {
		t.Fatalf("Date.Call unix returned error: %v", err)
	}
	out = strings.TrimSpace(out)
	if len(out) == 0 {
		t.Fatalf("expected unix timestamp, got empty string")
	}
	for _, r := range out {
		if r < '0' || r > '9' {
			t.Fatalf("expected numeric unix timestamp, got %q", out)
		}
	}
}

func TestDate_UTCAndRFC3339(t *testing.T) {
	out, err := Date.Call(map[string]any{"utc": true, "rfc3339": true})
	if err != nil {
		t.Fatalf("Date.Call utc+rfc3339 returned error: %v", err)
	}
	out = strings.TrimSpace(out)
	if !strings.Contains(out, "T") {
		t.Fatalf("expected RFC3339-like output containing 'T', got %q", out)
	}
}
