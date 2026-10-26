package text

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/baalimago/clai/internal/models"
	pub_models "github.com/baalimago/clai/pkg/text/models"
)

func TestQuerySummary_Lines(t *testing.T) {
	s := QuerySummary{Duration: 4*time.Second + 600*time.Millisecond, RunTally: sampleTally()}
	want := []string{
		"▸ query done in 4s",
		"  tokens  41.2k in (12k cached) · 3.12k out",
		"  cost    ~$0.18",
		"  tools   6 calls, 1 failed: go ×3 · cat ×2 · apply_patch",
	}
	if got := s.Lines(); !slices.Equal(got, want) {
		t.Fatalf("Lines =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestQuerySummary_LinesWhenStopped(t *testing.T) {
	s := QuerySummary{Outcome: QueryStopped, Duration: 900 * time.Millisecond}
	want := []string{
		"▸ query stopped in 0s",
		"  tokens  0 in · 0 out",
		"  cost    unknown (no price for this model)",
		"  tools   none",
	}
	if got := s.Lines(); !slices.Equal(got, want) {
		t.Fatalf("Lines =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestQueryOutcome_String(t *testing.T) {
	for outcome, want := range map[QueryOutcome]string{
		QueryDone:       "done",
		QueryStopped:    "stopped",
		QueryOutcome(9): "unknown",
	} {
		if got := outcome.String(); got != want {
			t.Fatalf("QueryOutcome(%d).String() = %q, want %q", outcome, got, want)
		}
	}
}

func TestRunTally_AddTurnCountsOnlyWhatTheTurnAdded(t *testing.T) {
	seed := pub_models.Chat{
		Messages: []pub_models.Message{{Role: "user", Content: "old"}},
		Queries:  []pub_models.QueryCost{{CostUSD: 5}},
	}
	result := seed
	result.Messages = append(slices.Clone(seed.Messages),
		pub_models.Message{Role: "assistant", ToolCalls: []pub_models.Call{{ID: "1", Name: "ls"}}},
		pub_models.Message{Role: "tool", ToolCallID: "1", Content: "ERROR: nope"},
	)
	result.Queries = append(slices.Clone(seed.Queries), pub_models.QueryCost{CostUSD: 0.25})
	result.TokenUsage = &pub_models.Usage{PromptTokens: 1500, CompletionTokens: 20}

	var tally RunTally
	tally.addTurn(seed, result)
	if !tally.HasCost || tally.CostUSD != 0.25 {
		t.Fatalf("cost = %v (has %v), want only the new row 0.25", tally.CostUSD, tally.HasCost)
	}
	if tally.Usage.PromptTokens != 1500 || tally.Usage.CompletionTokens != 20 {
		t.Fatalf("usage = %+v", tally.Usage)
	}
	if want := []RunToolCall{{Name: "ls", Failed: true}}; !slices.Equal(tally.ToolCalls, want) {
		t.Fatalf("tool calls = %+v, want %+v", tally.ToolCalls, want)
	}
}

func TestQuerier_ReportQuerySummaryPropagatesWriteErrors(t *testing.T) {
	q := rollingLoopQuerier(t, &strings.Builder{})
	q.Raw = true
	q.errOut = failingWriter{}
	err := q.reportQuerySummary(time.Now(), pub_models.Chat{}, pub_models.Chat{}, nil)
	if err == nil || !strings.Contains(err.Error(), "print query summary: boom") {
		t.Fatalf("err = %v, want the write failure", err)
	}
}

func answeringQuerier(t *testing.T, printed *strings.Builder) *Querier[*MockQuerier] {
	t.Helper()
	q := rollingLoopQuerier(t, printed)
	q.Model.streamFn = func(context.Context, pub_models.Chat) (chan models.CompletionEvent, error) {
		events := make(chan models.CompletionEvent, 2)
		events <- "The answer."
		events <- models.StopEvent{}
		close(events)
		return events, nil
	}
	return q
}

func TestQuerier_QuerySummary(t *testing.T) {
	chat := pub_models.Chat{Messages: []pub_models.Message{{Role: "user", Content: "hello"}}}

	t.Run("off by default", func(t *testing.T) {
		var printed, errOut strings.Builder
		q := answeringQuerier(t, &printed)
		q.errOut = &errOut
		if _, err := q.TextQuery(t.Context(), chat); err != nil {
			t.Fatalf("TextQuery: %v", err)
		}
		if strings.Contains(printed.String()+errOut.String(), "▸ query") {
			t.Fatalf("summary printed without opt-in: out=%q err=%q", printed.String(), errOut.String())
		}
	})
	t.Run("prints below the answer once enabled", func(t *testing.T) {
		var printed strings.Builder
		q := answeringQuerier(t, &printed)
		q.EnableQuerySummary()
		if _, err := q.TextQuery(t.Context(), chat); err != nil {
			t.Fatalf("TextQuery: %v", err)
		}
		out := stripANSIEscapes(printed.String())
		answer, summary := strings.Index(out, "The answer."), strings.Index(out, "▸ query done in")
		if answer < 0 || summary < answer {
			t.Fatalf("want the answer, then the summary; out=%q", out)
		}
	})
	t.Run("raw output sends it to stderr", func(t *testing.T) {
		var printed, errOut strings.Builder
		q := answeringQuerier(t, &printed)
		q.Raw = true
		q.errOut = &errOut
		q.EnableQuerySummary()
		if _, err := q.TextQuery(t.Context(), chat); err != nil {
			t.Fatalf("TextQuery: %v", err)
		}
		if strings.Contains(printed.String(), "▸ query") {
			t.Fatalf("stdout = %q, want only the answer", printed.String())
		}
		if !strings.HasPrefix(errOut.String(), "▸ query done in") {
			t.Fatalf("errOut = %q, want the summary", errOut.String())
		}
	})
	t.Run("loop iterations leave it to the loop", func(t *testing.T) {
		var printed strings.Builder
		q := answeringQuerier(t, &printed)
		q.EnableQuerySummary()
		q.SetLoopProgress(LoopProgress{Iteration: 1, MaxIterations: 2})
		if _, err := q.TextQuery(t.Context(), chat); err != nil {
			t.Fatalf("TextQuery: %v", err)
		}
		if strings.Contains(printed.String(), "▸ query") {
			t.Fatalf("loop iteration printed a query summary: %q", stripANSIEscapes(printed.String()))
		}
	})
	t.Run("a failed query still reports, and joins a print failure", func(t *testing.T) {
		q := rollingLoopQuerier(t, &strings.Builder{})
		q.Raw = true
		q.errOut = failingWriter{}
		q.Model.streamFn = func(context.Context, pub_models.Chat) (chan models.CompletionEvent, error) {
			return nil, errors.New("vendor down")
		}
		q.EnableQuerySummary()
		_, err := q.TextQuery(t.Context(), chat)
		if err == nil || !strings.Contains(err.Error(), "vendor down") || !strings.Contains(err.Error(), "print query summary") {
			t.Fatalf("err = %v, want the run error joined with the print failure", err)
		}
	})
}
