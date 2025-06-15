package generic

import (
	"fmt"
	"net/http"
	"testing"
	"time"
)

func TestRateLimiter_OpenAIHeaders(t *testing.T) {
	rl := NewRateLimiter("remaining", "reset")
	h := http.Header{}
	h.Set("remaining", "10")
	h.Set("reset", "2s")
	if err := rl.UpdateFromHeaders(h); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rl.remainingTokens != 10 {
		t.Errorf("expected remaining 10, got %v", rl.remainingTokens)
	}
	if d := time.Until(rl.resetTokens); d < time.Second || d > 3*time.Second {
		t.Errorf("expected ~2s reset, got %v", d)
	}
}

func TestRateLimiter_AnthropicHeaders(t *testing.T) {
	rl := NewRateLimiter("remaining", "reset")
	h := http.Header{}
	h.Set("remaining", "5")
	ts := time.Now().Add(3 * time.Second).Unix()
	h.Set("reset", fmt.Sprintf("%d", ts))
	if err := rl.UpdateFromHeaders(h); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rl.remainingTokens != 5 {
		t.Errorf("expected remaining 5, got %v", rl.remainingTokens)
	}
	if d := time.Until(rl.resetTokens); d < 2*time.Second || d > 4*time.Second {
		t.Errorf("expected ~3s reset, got %v", d)
	}
}

func TestRateLimiter_Missing(t *testing.T) {
	rl := NewRateLimiter("remaining", "reset")
	h := http.Header{}
	h.Set("remaining", "1")
	if err := rl.UpdateFromHeaders(h); err == nil {
		t.Fatal("expected error")
	}
}
