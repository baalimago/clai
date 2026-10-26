package text

import (
	"slices"
	"strings"
	"testing"
	"time"

	pub_models "github.com/baalimago/clai/pkg/text/models"
)

func toolCallMessages() []pub_models.Message {
	call := func(id, name string) pub_models.Message {
		return pub_models.Message{Role: "assistant", ToolCalls: []pub_models.Call{{ID: id, Name: name}}}
	}
	result := func(id, content string) pub_models.Message {
		return pub_models.Message{Role: "tool", ToolCallID: id, Content: content}
	}
	return []pub_models.Message{
		{Role: "user", Content: "go"},
		call("a", "go"), result("a", "ERROR: tool \"go\" failed: exit status 1"),
		call("b", "cat"), result("b", "package main"),
		call("c", "cat"), result("c", "package main"),
		call("d", "apply_patch"), result("d", "patched"),
		call("e", "go"), result("e", "ok"),
		{Role: "assistant", Content: "done"},
	}
}

func TestLoopToolCalls(t *testing.T) {
	got := runToolCalls(toolCallMessages())
	want := []RunToolCall{
		{Name: "go", Failed: true},
		{Name: "cat"},
		{Name: "cat"},
		{Name: "apply_patch"},
		{Name: "go"},
	}
	if !slices.Equal(got, want) {
		t.Fatalf("runToolCalls = %+v, want %+v", got, want)
	}
}

func TestLoopToolCalls_unmatchedResultIsNamedUnknown(t *testing.T) {
	got := runToolCalls([]pub_models.Message{{Role: "tool", ToolCallID: "x", Content: "out"}})
	if want := []RunToolCall{{Name: "unknown"}}; !slices.Equal(got, want) {
		t.Fatalf("runToolCalls = %+v, want %+v", got, want)
	}
}

func TestLoopToolsLine(t *testing.T) {
	if got, want := loopToolsLine(runToolCalls(toolCallMessages())), "go ✗ · cat ×2 · apply_patch · go"; got != want {
		t.Fatalf("loopToolsLine = %q, want %q", got, want)
	}
	if got := loopToolsLine(nil); got != "" {
		t.Fatalf("loopToolsLine(nil) = %q, want empty", got)
	}
}

func TestLoopProgress_HeaderShowsToolsOnceSettled(t *testing.T) {
	p := LoopProgress{Iteration: 1, MaxIterations: 3, Phase: "plan", ToolCalls: []RunToolCall{{Name: "go", Failed: true}, {Name: "cat"}}}
	if lines := p.Header(); len(lines) != 1 {
		t.Fatalf("header lines before settle = %d, want 1", len(lines))
	}
	p.ShowTools = true
	lines := p.Header()
	if len(lines) != 2 {
		t.Fatalf("header lines after settle = %d, want 2", len(lines))
	}
	var row strings.Builder
	for _, s := range lines[1].Spans {
		row.WriteString(s.Text)
	}
	if got, want := row.String(), "↳ go ✗ · cat"; got != want {
		t.Fatalf("tools row = %q, want %q", got, want)
	}
}

func TestLoopSummary_Lines(t *testing.T) {
	s := LoopSummary{
		End:           LoopEndAchieved,
		Iterations:    3,
		MaxIterations: 10,
		Duration:      2*time.Minute + 14*time.Second + 300*time.Millisecond,
		RunTally:      sampleTally(),
	}
	want := []string{
		"▸ loop achieved after 3/10 iterations in 2m14s",
		"  tokens  41.2k in (12k cached) · 3.12k out",
		"  cost    ~$0.18",
		"  tools   6 calls, 1 failed: go ×3 · cat ×2 · apply_patch",
	}
	if got := s.Lines(); !slices.Equal(got, want) {
		t.Fatalf("Lines =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestLoopSummary_LinesWithoutCostOrTools(t *testing.T) {
	s := LoopSummary{End: LoopEndStopped, Iterations: 1, Duration: 900 * time.Millisecond}
	want := []string{
		"▸ loop stopped after 1/∞ iterations in 0s",
		"  tokens  0 in · 0 out",
		"  cost    unknown (no price for this model)",
		"  tools   none",
	}
	if got := s.Lines(); !slices.Equal(got, want) {
		t.Fatalf("Lines =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestLoopEnd_String(t *testing.T) {
	for end, want := range map[LoopEnd]string{
		LoopEndAchieved:  "achieved",
		LoopEndBlocked:   "blocked",
		LoopEndExhausted: "exhausted",
		LoopEndStopped:   "stopped",
		LoopEnd(99):      "unknown",
	} {
		if got := end.String(); got != want {
			t.Fatalf("LoopEnd(%d).String() = %q, want %q", end, got, want)
		}
	}
}

func sampleTally() RunTally {
	return RunTally{
		Usage: pub_models.Usage{
			PromptTokens:        41203,
			CompletionTokens:    3118,
			PromptTokensDetails: pub_models.PromptTokensDetails{CachedTokens: 12004},
		},
		CostUSD: 0.1834,
		HasCost: true,
		ToolCalls: []RunToolCall{
			{Name: "go", Failed: true}, {Name: "cat"}, {Name: "cat"}, {Name: "go"}, {Name: "go"}, {Name: "apply_patch"},
		},
	}
}
