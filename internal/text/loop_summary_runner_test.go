package text

import (
	"context"
	"errors"
	"slices"
	"testing"

	pub_models "github.com/baalimago/clai/pkg/text/models"
)

// iterationWithWork appends one tool call, its result and a reply, plus usage
// and a cost row, the way a real TextQuery session does.
func iterationWithWork(chat pub_models.Chat, tool, out string) pub_models.Chat {
	chat.Messages = append(chat.Messages,
		pub_models.Message{Role: "assistant", ToolCalls: []pub_models.Call{{ID: tool, Name: tool}}},
		pub_models.Message{Role: "tool", ToolCallID: tool, Content: out},
		pub_models.Message{Role: "assistant", Content: "handover"},
	)
	chat.TokenUsage = &pub_models.Usage{PromptTokens: 100, CompletionTokens: 10, TotalTokens: 110}
	chat.Queries = append(chat.Queries, pub_models.QueryCost{CostUSD: 0.5})
	return chat
}

func TestLoopRunner_ReportsSummary(t *testing.T) {
	cases := []struct {
		name       string
		lastStatus string
		maxIter    int
		wantEnd    LoopEnd
		wantIter   int
	}{
		{name: "achieved", lastStatus: "achieved", maxIter: 5, wantEnd: LoopEndAchieved, wantIter: 2},
		{name: "blocked", lastStatus: "blocked", maxIter: 5, wantEnd: LoopEndBlocked, wantIter: 2},
		{name: "exhausted", maxIter: 2, wantEnd: LoopEndExhausted, wantIter: 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			recorder := NewCompletionRecorder("")
			fake := &recordingLoopQuerier{handle: func(ctx context.Context, iteration int, chat pub_models.Chat) (pub_models.Chat, error) {
				if iteration == 1 {
					return iterationWithWork(chat, "go", "ERROR: exit 1"), nil
				}
				if tc.lastStatus != "" {
					if _, err := recorder.CallWithContext(ctx, loopInput(tc.lastStatus, "summary")); err != nil {
						return pub_models.Chat{}, err
					}
				}
				return iterationWithWork(chat, "cat", "ok"), nil
			}}
			seed := loopSeedChat()
			seed.Queries = []pub_models.QueryCost{{CostUSD: 9}}
			runner := NewLoopRunner(fake, recorder, LoopConfig{SeedChat: seed, GoalPrompt: "g", MaxIterations: tc.maxIter})

			err := runner.Query(t.Context())
			if tc.wantEnd == LoopEndAchieved && err != nil {
				t.Fatalf("Query: %v", err)
			}
			if len(fake.summaries) != 1 {
				t.Fatalf("summaries = %d, want 1", len(fake.summaries))
			}
			got := fake.summaries[0]
			if got.End != tc.wantEnd || got.Iterations != tc.wantIter || got.MaxIterations != tc.maxIter {
				t.Fatalf("summary = %+v, want end %v after %d/%d", got, tc.wantEnd, tc.wantIter, tc.maxIter)
			}
			if got.Usage.PromptTokens != 200 || got.Usage.CompletionTokens != 20 {
				t.Fatalf("usage = %+v, want both iterations summed", got.Usage)
			}
			if !got.HasCost || got.CostUSD != 1.0 {
				t.Fatalf("cost = %v (has=%v), want 1.0 without the seed's cost rows", got.CostUSD, got.HasCost)
			}
			if want := []RunToolCall{{Name: "go", Failed: true}, {Name: "cat"}}; !slices.Equal(got.ToolCalls, want) {
				t.Fatalf("tool calls = %+v, want %+v", got.ToolCalls, want)
			}
		})
	}
}

func TestLoopRunner_ReportsSummaryWhenStopped(t *testing.T) {
	recorder := NewCompletionRecorder("")
	boom := errors.New("boom")
	fake := &recordingLoopQuerier{handle: func(context.Context, int, pub_models.Chat) (pub_models.Chat, error) {
		return pub_models.Chat{}, boom
	}}
	runner := NewLoopRunner(fake, recorder, LoopConfig{SeedChat: loopSeedChat(), MaxIterations: 3})
	if err := runner.Query(t.Context()); !errors.Is(err, boom) {
		t.Fatalf("Query err = %v, want boom", err)
	}
	if len(fake.summaries) != 1 || fake.summaries[0].End != LoopEndStopped || fake.summaries[0].Iterations != 1 {
		t.Fatalf("summaries = %+v, want one stopped summary after 1 iteration", fake.summaries)
	}
}

func TestLoopRunner_NoSummaryBeforeAnyIteration(t *testing.T) {
	fake := &recordingLoopQuerier{}
	runner := NewLoopRunner(fake, NewCompletionRecorder(""), LoopConfig{SeedChat: loopSeedChat(), MaxIterations: -1})
	if err := runner.Query(t.Context()); err == nil {
		t.Fatal("Query accepted a negative -max-iterations")
	}
	if len(fake.summaries) != 0 {
		t.Fatalf("summaries = %d, want none when no iteration ran", len(fake.summaries))
	}
}

func TestLoopRunner_SummaryReportErrorIsJoined(t *testing.T) {
	recorder := NewCompletionRecorder("")
	reportErr := errors.New("write failed")
	fake := &recordingLoopQuerier{reportErr: reportErr, handle: func(ctx context.Context, _ int, chat pub_models.Chat) (pub_models.Chat, error) {
		if _, err := recorder.CallWithContext(ctx, loopInput("blocked", "stuck")); err != nil {
			return pub_models.Chat{}, err
		}
		return chat, nil
	}}
	runner := NewLoopRunner(fake, recorder, LoopConfig{SeedChat: loopSeedChat(), MaxIterations: 3})
	err := runner.Query(t.Context())
	var blocked *LoopBlockedError
	if !errors.As(err, &blocked) || !errors.Is(err, reportErr) {
		t.Fatalf("Query err = %v, want the blocked error joined with the report failure", err)
	}
}

// TestLoopRunner_TalliesAFailedIterationsCompletedWork pins that work a
// failed or cancelled iteration finished still counts toward the run.
func TestLoopRunner_TalliesAFailedIterationsCompletedWork(t *testing.T) {
	boom := errors.New("boom")
	for name, cancelFirst := range map[string]bool{"failure": false, "cancellation": true} {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			fake := &recordingLoopQuerier{handle: func(_ context.Context, _ int, chat pub_models.Chat) (pub_models.Chat, error) {
				if cancelFirst {
					cancel()
				}
				return pub_models.Chat{}, &PartialRunError{Chat: iterationWithWork(chat, "go", "ok"), Err: boom}
			}}
			runner := NewLoopRunner(fake, NewCompletionRecorder(""), LoopConfig{SeedChat: loopSeedChat(), MaxIterations: 3})
			if err := runner.Query(ctx); err == nil {
				t.Fatal("Query returned no error")
			}
			if len(fake.summaries) != 1 {
				t.Fatalf("summaries = %d, want 1", len(fake.summaries))
			}
			got := fake.summaries[0]
			if got.Usage.PromptTokens != 100 || !got.HasCost || got.CostUSD != 0.5 {
				t.Fatalf("summary = %+v, want the iteration's usage and cost", got)
			}
			if want := []RunToolCall{{Name: "go"}}; !slices.Equal(got.ToolCalls, want) {
				t.Fatalf("tool calls = %+v, want %+v", got.ToolCalls, want)
			}
		})
	}
}
