package text

import (
	"errors"
	"io"
	"strings"
	"testing"
)

// Test_applyToolCallBudget pins the -max-tool-calls contract shared by the
// generic tool path and the lookback tools: within budget the output is
// prefixed with the remaining count, over budget the output is replaced with
// an escalating warning, and persisting past the final warning hard-stops the
// run with io.EOF.
func Test_applyToolCallBudget(t *testing.T) {
	t.Run("no budget passes output through untouched", func(t *testing.T) {
		q := &Querier[*MockQuerier]{}
		e := toolExecutor[*MockQuerier]{querier: q}
		session := &QuerySession{}

		out, err := e.applyToolCallBudget(session, "result")
		if err != nil {
			t.Fatalf("applyToolCallBudget: %v", err)
		}
		if out != "result" {
			t.Fatalf("expected untouched output, got %q", out)
		}
		if session.ToolCallsUsed != 0 {
			t.Fatalf("expected no increment without budget, got %d", session.ToolCallsUsed)
		}
	})

	t.Run("within budget prefixes remaining count and increments", func(t *testing.T) {
		maxCalls := 3
		q := &Querier[*MockQuerier]{maxToolCalls: &maxCalls}
		e := toolExecutor[*MockQuerier]{querier: q}
		session := &QuerySession{}

		out, err := e.applyToolCallBudget(session, "result")
		if err != nil {
			t.Fatalf("applyToolCallBudget: %v", err)
		}
		if !strings.Contains(out, "Tool calls remaining: 3") || !strings.Contains(out, "result") {
			t.Fatalf("expected remaining-count prefix around output, got %q", out)
		}
		if session.ToolCallsUsed != 1 {
			t.Fatalf("expected ToolCallsUsed 1, got %d", session.ToolCallsUsed)
		}
	})

	t.Run("over budget escalates and hard stops with io.EOF", func(t *testing.T) {
		maxCalls := 1
		q := &Querier[*MockQuerier]{maxToolCalls: &maxCalls}
		e := toolExecutor[*MockQuerier]{querier: q}
		session := &QuerySession{ToolCallsUsed: 1}

		out, err := e.applyToolCallBudget(session, "result")
		if err != nil {
			t.Fatalf("first over-budget call should warn, not error: %v", err)
		}
		if !strings.Contains(out, "No more tool calls allowed") {
			t.Fatalf("expected over-budget warning, got %q", out)
		}
		if strings.Contains(out, "result") {
			t.Fatalf("expected tool output to be replaced when over budget, got %q", out)
		}

		var lastErr error
		for range 4 {
			_, lastErr = e.applyToolCallBudget(session, "again")
		}
		if !errors.Is(lastErr, io.EOF) {
			t.Fatalf("expected io.EOF after persisting over budget, got %v", lastErr)
		}
	})
}
