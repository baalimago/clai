package text

import (
	"context"
	"io"
	"os"
	"strings"
	"testing"

	pub_models "github.com/baalimago/clai/pkg/text/models"
)

// captureStdout redirects stdout for the duration of fn and returns what was
// written, so opt-in debug output can be asserted.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	orig := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("create stdout pipe: %v", err)
	}
	os.Stdout = w
	readDone := make(chan string, 1)
	go func() {
		b, _ := io.ReadAll(r)
		readDone <- string(b)
	}()
	fn()
	if err := w.Close(); err != nil {
		t.Fatalf("close stdout writer: %v", err)
	}
	os.Stdout = orig
	return <-readDone
}

// TestStoplossDebugLogging pins the DEBUG_STOPLOSS opt-in layer: budget checks
// emit [DEBUG_STOPLOSS] lines only when the flag (or plain DEBUG) is truthy.
func TestStoplossDebugLogging(t *testing.T) {
	cross := func(t *testing.T) string {
		t.Helper()
		s := &stoploss{maxTokens: 10, maxTokensHandoverMsg: "wrap up"}
		session := &QuerySession{}
		usage := &pub_models.Usage{PromptTokens: 10, CompletionTokens: 10}
		return captureStdout(t, func() {
			injected, err := s.CheckContextBudget(context.Background(), nil, session, usage)
			if err != nil {
				t.Fatalf("CheckContextBudget: %v", err)
			}
			if !injected {
				t.Fatal("expected handover injection at footprint 20 >= max-tokens 10")
			}
		})
	}
	t.Run("silent without flag", func(t *testing.T) {
		t.Setenv("DEBUG", "")
		t.Setenv("DEBUG_STOPLOSS", "")
		if out := cross(t); strings.Contains(out, "[DEBUG_STOPLOSS]") {
			t.Fatalf("expected no stoploss debug output without flag, got %q", out)
		}
	})
	t.Run("prints with flag", func(t *testing.T) {
		t.Setenv("DEBUG", "")
		t.Setenv("DEBUG_STOPLOSS", "1")
		if out := cross(t); !strings.Contains(out, "[DEBUG_STOPLOSS] footprint 20 reached max-tokens 10") {
			t.Fatalf("expected stoploss debug output with flag, got %q", out)
		}
	})
}
