package text

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/baalimago/clai/internal/models"
	"github.com/baalimago/clai/internal/runtally"
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

func TestQuerySummary_LinesWithSubagents(t *testing.T) {
	tally := sampleTally()
	tally.Subagents = runtally.Totals{Runs: 4, PromptTokens: 210000, CompletionTokens: 9000, CostUSD: 0.41}
	s := QuerySummary{Duration: 4 * time.Second, RunTally: tally}
	want := []string{
		"▸ query done in 4s",
		"  tokens  41.2k in (12k cached) · 3.12k out",
		"  cost    ~$0.18",
		"  tools   6 calls, 1 failed: go ×3 · cat ×2 · apply_patch",
		"  subagents 4 runs · 210k in · 9k out · ~$0.41",
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

func TestQuerier_FinishQueryPropagatesWriteErrors(t *testing.T) {
	q := rollingLoopQuerier(t, &strings.Builder{})
	q.Raw = true
	q.querySummary = true
	q.errOut = failingWriter{}
	err := q.finishQuery(time.Now(), pub_models.Chat{}, pub_models.Chat{}, nil)
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

// TestQuerier_QuerySummaryCountsTheRunsCostAndTools drives the real Query
// path: the finalizer stores the result chat before the summary is built,
// so the summary must compare against the chat the run started from.
func TestQuerier_QuerySummaryCountsTheRunsCostAndTools(t *testing.T) {
	var printed, errOut strings.Builder
	q := rollingLoopQuerier(t, &printed)
	q.Raw = true
	q.errOut = &errOut
	q.Model.usage = &pub_models.Usage{PromptTokens: 1200, CompletionTokens: 30, TotalTokens: 1230}
	q.costEnricher = costEnricher{
		manager: &fakeCostManager{enrichFn: appendCostRow},
		ready:   closedReady(),
		waitFor: time.Millisecond,
		warnf:   func(string, ...any) {},
	}
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
	q.EnableQuerySummary()

	chat := pub_models.Chat{Messages: []pub_models.Message{{Role: "user", Content: "hello"}}}
	if _, err := q.TextQuery(t.Context(), chat); err != nil {
		t.Fatalf("TextQuery: %v", err)
	}
	got := errOut.String()
	for _, want := range []string{"  cost    ~$0.42", "  tools   1 call, 1 failed: missing_tool"} {
		if !strings.Contains(got, want) {
			t.Fatalf("summary = %q, want it to contain %q", got, want)
		}
	}
}

// TestQuerier_TextQueryErrorCarriesThePartialRun pins that a run which fails
// or is cancelled after a completed tool step still hands back the finalized
// chat, so callers can account for the work it did.
func TestQuerier_TextQueryErrorCarriesThePartialRun(t *testing.T) {
	for name, cancelSecond := range map[string]bool{"failure": false, "cancellation": true} {
		t.Run(name, func(t *testing.T) {
			q := rollingLoopQuerier(t, &strings.Builder{})
			q.Raw = true
			q.errOut = &strings.Builder{}
			q.Model.usage = &pub_models.Usage{PromptTokens: 1200, CompletionTokens: 30, TotalTokens: 1230}
			q.costEnricher = costEnricher{
				manager: &fakeCostManager{enrichFn: appendCostRow},
				ready:   closedReady(),
				waitFor: time.Millisecond,
				warnf:   func(string, ...any) {},
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			calls := 0
			q.Model.streamFn = func(context.Context, pub_models.Chat) (chan models.CompletionEvent, error) {
				calls++
				if calls == 1 {
					events := make(chan models.CompletionEvent, 1)
					events <- pub_models.Call{ID: "c1", Name: "missing_tool"}
					close(events)
					return events, nil
				}
				if cancelSecond {
					cancel()
					return nil, ctx.Err()
				}
				return nil, errors.New("vendor down")
			}
			chat := pub_models.Chat{Messages: []pub_models.Message{{Role: "user", Content: "hello"}}}
			_, err := q.TextQuery(ctx, chat)
			var partial *PartialRunError
			if !errors.As(err, &partial) {
				t.Fatalf("err = %v, want a *PartialRunError", err)
			}
			var tally RunTally
			tally.addTurn(chat, partial.Chat)
			if tally.Usage.PromptTokens == 0 || !tally.HasCost {
				t.Fatalf("tally = %+v, want the completed step's usage and cost", tally)
			}
			if want := []RunToolCall{{Name: "missing_tool", Failed: true}}; !slices.Equal(tally.ToolCalls, want) {
				t.Fatalf("tool calls = %+v, want %+v", tally.ToolCalls, want)
			}
		})
	}
}

func TestQuerier_FinishQueryNamesAFailedParentReport(t *testing.T) {
	parent := filepath.Join(t.TempDir(), "a-file")
	if err := os.WriteFile(parent, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	scope, err := runtally.Open(fakeRunTallyEnv{runtally.EnvDir: parent}, t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = scope.Close() })
	q := rollingLoopQuerier(t, &strings.Builder{})
	q.SetRunTally(scope)
	err = q.finishQuery(time.Now(), pub_models.Chat{}, pub_models.Chat{}, nil)
	if err == nil || !strings.Contains(err.Error(), "report to parent run") {
		t.Fatalf("err = %v, want the failed report", err)
	}
}

func TestWithRunTally_refusesANonTextQuerier(t *testing.T) {
	deps := QueryCommandDeps{RunTallyEnv: fakeRunTallyEnv{}, RunTallyTempRoot: t.TempDir()}
	var q models.Querier = noopQuerier{}
	if _, err := withRunTally(q, q, deps, true); err == nil {
		t.Fatal("withRunTally accepted a querier that cannot count subagents")
	}
}

type noopQuerier struct{}

func (noopQuerier) Query(context.Context) error { return nil }
