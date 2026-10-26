package text

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/baalimago/clai/internal/models"
	pub_models "github.com/baalimago/clai/pkg/text/models"
)

// TestQuerier_SettledLoopHeaderListsToolCalls pins that a finished iteration
// keeps a row naming its tool calls, so the collapsed header still shows the
// work the iteration did.
func TestQuerier_SettledLoopHeaderListsToolCalls(t *testing.T) {
	var printed strings.Builder
	q := rollingLoopQuerier(t, &printed)
	calls := 0
	q.Model.streamFn = func(context.Context, pub_models.Chat) (chan models.CompletionEvent, error) {
		calls++
		events := make(chan models.CompletionEvent, 2)
		if calls == 1 {
			events <- pub_models.Call{ID: "c1", Name: "missing_tool"}
		} else {
			events <- "done"
			events <- models.StopEvent{}
		}
		close(events)
		return events, nil
	}
	q.SetLoopProgress(LoopProgress{Iteration: 1, MaxIterations: 4})

	chat := pub_models.Chat{Messages: []pub_models.Message{{Role: "user", Content: "hello"}}}
	if _, err := q.TextQuery(t.Context(), chat); err != nil {
		t.Fatalf("TextQuery: %v", err)
	}
	want := []string{"▸ loop 1/4", "↳ missing_tool ✗"}
	if rows := q.activityViewport.Rows(); !slices.Equal(rows, want) {
		t.Fatalf("settled rows = %q, want %q", rows, want)
	}
}

func TestQuerier_ReportLoopSummary(t *testing.T) {
	summary := LoopSummary{End: LoopEndAchieved, Iterations: 1, MaxIterations: 2}

	t.Run("terminal output prints below the loop", func(t *testing.T) {
		var printed, errOut strings.Builder
		q := rollingLoopQuerier(t, &printed)
		q.errOut = &errOut
		if err := q.ReportLoopSummary(summary); err != nil {
			t.Fatalf("ReportLoopSummary: %v", err)
		}
		if got := stripANSIEscapes(printed.String()); !strings.HasPrefix(got, "▸ loop achieved after 1/2 iterations") {
			t.Fatalf("out = %q, want the summary", got)
		}
		if errOut.Len() != 0 {
			t.Fatalf("errOut = %q, want empty", errOut.String())
		}
	})
	t.Run("raw output goes to stderr", func(t *testing.T) {
		var printed, errOut strings.Builder
		q := rollingLoopQuerier(t, &printed)
		q.Raw = true
		q.errOut = &errOut
		if err := q.ReportLoopSummary(summary); err != nil {
			t.Fatalf("ReportLoopSummary: %v", err)
		}
		if printed.Len() != 0 {
			t.Fatalf("stdout = %q, want it left machine-readable", printed.String())
		}
		if want := strings.Join(summary.Lines(), "\n") + "\n"; errOut.String() != want {
			t.Fatalf("errOut = %q, want %q", errOut.String(), want)
		}
	})
}
