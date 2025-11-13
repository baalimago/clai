package models

import (
	"strings"
	"testing"
	"time"
)

func TestRateLimitError(t *testing.T) {
	reset := time.Now().Add(time.Minute).Round(time.Second)
	err := NewRateLimitError(reset, 1234, 567)
	rl, ok := err.(*ErrRateLimit)
	if !ok {
		t.Fatalf("expected *ErrRateLimit, got %T", err)
	}
	if rl.MaxInputTokens != 1234 || rl.TokensRemaining != 567 || !rl.ResetAt.Equal(reset) {
		t.Fatalf("unexpected values: %#v", rl)
	}
	msg := rl.Error()
	if !strings.Contains(msg, "reset at:") || !strings.Contains(msg, "input tokens used") {
		t.Fatalf("unexpected error message: %q", msg)
	}
}
