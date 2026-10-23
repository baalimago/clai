package text

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/baalimago/clai/internal/models"
	"github.com/baalimago/clai/internal/utils"
	pub_models "github.com/baalimago/clai/pkg/text/models"
	"github.com/baalimago/go_away_boilerplate/pkg/dimensions"
)

// TestQuerier_SettleTakesALateLoopPhaseSummary pins that a summarizer that
// answers after the stream still reaches the header: the settle waits under the
// join bound before it freezes the header into the scrollback.
func TestQuerier_SettleTakesALateLoopPhaseSummary(t *testing.T) {
	var printed strings.Builder
	q := rollingLoopQuerier(t, &printed)
	q.summarizer = instantSummarizer()
	q.summarizeConversations = true
	q.shouldSaveReply = true
	q.summaryJoinTimeout = time.Second
	q.SetLoopProgress(LoopProgress{Iteration: 1, MaxIterations: 2})
	q.markLoopPhasePending()
	q.ensureActivityViewport()
	q.activityViewport.AppendText("work")
	if err := q.activityViewport.Render(&printed); err != nil {
		t.Fatalf("Render: %v", err)
	}
	q.summaryRun = &summaryRun{
		result:    make(chan summaryResult, 1),
		cancel:    func() {},
		interrupt: make(chan struct{}),
		release:   func() {},
	}
	q.summaryRun.result <- summaryResult{summary: fakeSummary()}

	if err := q.settleLoopHeader(); err != nil {
		t.Fatalf("settleLoopHeader: %v", err)
	}
	want := []string{"▸ loop 1/2 - S"}
	if rows := q.activityViewport.Rows(); !slices.Equal(rows, want) {
		t.Fatalf("rows = %q, want the late summary in the frozen header %q", rows, want)
	}
	if q.summaryReady == nil {
		t.Fatal("the taken result must stay on summaryReady for the finalizer's join")
	}
}

// TestQuerier_SettleRevealsTheFallbackWhenTheSummaryNeverLands pins the
// bounded wait: a summarizer that does not answer before the bound leaves the
// fallback phase and is abandoned, so the finalizer cannot wait again.
func TestQuerier_SettleRevealsTheFallbackWhenTheSummaryNeverLands(t *testing.T) {
	cases := []struct {
		name      string
		interrupt bool
	}{
		{name: "timeout"},
		{name: "interrupt", interrupt: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var printed strings.Builder
			q := rollingLoopQuerier(t, &printed)
			q.summarizer = blockingSummarizer()
			q.summarizeConversations = true
			q.shouldSaveReply = true
			q.summaryJoinTimeout = time.Millisecond
			q.SetLoopProgress(LoopProgress{Iteration: 2, MaxIterations: 2, Phase: "prior phase"})
			q.markLoopPhasePending()
			q.ensureActivityViewport()
			interrupt := make(chan struct{})
			if tc.interrupt {
				close(interrupt)
			}
			q.summaryRun = &summaryRun{
				result:    make(chan summaryResult),
				cancel:    func() {},
				interrupt: interrupt,
				release:   func() {},
			}

			if err := q.settleLoopHeader(); err != nil {
				t.Fatalf("settleLoopHeader: %v", err)
			}
			want := []string{"▸ loop 2/2 - prior phase"}
			if rows := q.activityViewport.Rows(); !slices.Equal(rows, want) {
				t.Fatalf("rows = %q, want the fallback phase %q", rows, want)
			}
			if q.summaryRun != nil {
				t.Fatal("an unanswered summary must be abandoned before the finalizer's join")
			}
		})
	}
}

// TestQuerier_TextQueryCollapsesLoopWindowAtStreamEnd pins that the stream end
// collapses the window to its header before the finalizer writes anything.
func TestQuerier_TextQueryCollapsesLoopWindowAtStreamEnd(t *testing.T) {
	var printed strings.Builder
	q := rollingLoopQuerier(t, &printed)
	model := q.Model
	model.streamFn = func(context.Context, pub_models.Chat) (chan models.CompletionEvent, error) {
		events := make(chan models.CompletionEvent, 3)
		events <- models.ReasoningEvent{Content: "inspect"}
		events <- "The answer is done."
		events <- models.StopEvent{}
		close(events)
		return events, nil
	}
	q.SetLoopProgress(LoopProgress{Iteration: 1, MaxIterations: 4})

	chat := pub_models.Chat{Messages: []pub_models.Message{{Role: "user", Content: "hello"}}}
	if _, err := q.TextQuery(t.Context(), chat); err != nil {
		t.Fatalf("TextQuery: %v", err)
	}
	want := "▸ loop 1/4"
	if rows := q.activityViewport.Rows(); len(rows) != 1 || rows[0] != want {
		t.Fatalf("the stream end must collapse the window to its header row, rows = %q", rows)
	}

	// The finalizer writes cost and cache lines after the stream ends. The
	// collapse must already have happened, so the continue step adds no cursor
	// movement over those lines.
	printed.WriteString("cache log\n")
	mark := printed.Len()
	if err := q.FinishLoopIteration(LoopOutcomeContinue, pub_models.Message{}); err != nil {
		t.Fatalf("FinishLoopIteration: %v", err)
	}
	if suffix := printed.String()[mark:]; suffix != "" {
		t.Fatalf("the continue step must not move the cursor after unrelated output, got %q", suffix)
	}
	if got := strings.Count(stripANSIEscapes(printed.String()), want); got != 1 {
		t.Fatalf("title rendered %d times, want once; output=%q", got, stripANSIEscapes(printed.String()))
	}
}

func TestLoopProgress_Header(t *testing.T) {
	cases := []struct {
		name       string
		progress   LoopProgress
		wantMarker string
		wantPhase  string
	}{
		{
			name:       "bounded with a phase summary",
			progress:   LoopProgress{Iteration: 2, MaxIterations: 10, Phase: "added the flag; tests remain"},
			wantMarker: "▸ loop 2/10",
			wantPhase:  " - added the flag; tests remain",
		},
		{
			name:       "first iteration has no phase summary",
			progress:   LoopProgress{Iteration: 1, MaxIterations: 10},
			wantMarker: "▸ loop 1/10",
		},
		{
			name:       "a pending phase shows the summarizing placeholder",
			progress:   LoopProgress{Iteration: 3, MaxIterations: 10, Phase: "stale fallback", PhasePending: true},
			wantMarker: "▸ loop 3/10",
			wantPhase:  loopPhaseSeparator + loopSummarizingPlaceholder,
		},
		{
			name:       "a pending first iteration shows the summarizing placeholder",
			progress:   LoopProgress{Iteration: 1, MaxIterations: 10, PhasePending: true},
			wantMarker: "▸ loop 1/10",
			wantPhase:  loopPhaseSeparator + loopSummarizingPlaceholder,
		},
		{
			name:       "unbounded",
			progress:   LoopProgress{Iteration: 1},
			wantMarker: "▸ loop 1/∞",
		},
		{
			name:       "collapses whitespace",
			progress:   LoopProgress{Iteration: 2, MaxIterations: 3, Phase: "  added\n\tthe flag  "},
			wantMarker: "▸ loop 2/3",
			wantPhase:  " - added the flag",
		},
		{
			name:       "caps the phase summary",
			progress:   LoopProgress{Iteration: 3, MaxIterations: 3, Phase: strings.Repeat("a", models.LoopPhaseSummaryRunes+10)},
			wantMarker: "▸ loop 3/3",
			wantPhase:  " - " + strings.Repeat("a", models.LoopPhaseSummaryRunes) + "…",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.progress.Header()
			if len(got) != 1 {
				t.Fatalf("Header() = %+v, want one logical line", got)
			}
			want := []utils.HeaderSpan{{Text: tc.wantMarker, Color: utils.LoopTitleColor()}}
			if tc.wantPhase != "" {
				want = append(want, utils.HeaderSpan{Text: tc.wantPhase, Color: utils.LoopPhaseColor()})
			}
			if !slices.Equal(got[0].Spans, want) {
				t.Fatalf("Header() spans = %+v, want %+v", got[0].Spans, want)
			}
		})
	}
}

func TestLoopProgressLines(t *testing.T) {
	t.Run("handover", func(t *testing.T) {
		cases := []struct {
			name  string
			input string
			want  string
		}{
			{name: "empty", input: "", want: ""},
			{name: "single line", input: "All tests pass.", want: "All tests pass."},
			{name: "first and last non-empty lines", input: "Implemented X.\n\nRemaining: docs.\n\n", want: "Implemented X. · Remaining: docs."},
			{name: "collapses whitespace", input: "Ran   the\t\tsuite.", want: "Ran the suite."},
			{name: "caps a long line", input: strings.Repeat("a", models.LoopPhaseSummaryRunes+10), want: strings.Repeat("a", models.LoopPhaseSummaryRunes) + "…"},
			{name: "placeholder passes through", input: loopHandoverPlaceholder, want: loopHandoverPlaceholder},
			{name: "duplicate first and last", input: "done\n\ndone", want: "done"},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				if got := loopHandoverProgress(tc.input); got != tc.want {
					t.Fatalf("loopHandoverProgress(%q) = %q, want %q", tc.input, got, tc.want)
				}
			})
		}
	})

	t.Run("summary", func(t *testing.T) {
		if got, want := loopProgressLine("  added\n\tthe flag "), "added the flag"; got != want {
			t.Fatalf("loopProgressLine = %q, want %q", got, want)
		}
		long := strings.Repeat("b", models.LoopPhaseSummaryRunes+10)
		if got, want := loopProgressLine(long), strings.Repeat("b", models.LoopPhaseSummaryRunes)+"…"; got != want {
			t.Fatalf("loopProgressLine cap = %q, want %q", got, want)
		}
	})
}

func TestLoopRunner_ProgressAndDisplayPerIteration(t *testing.T) {
	recorder := NewCompletionRecorder("")
	fake := &recordingLoopQuerier{}
	fake.handle = func(ctx context.Context, iteration int, chat pub_models.Chat) (pub_models.Chat, error) {
		chat.Messages = append(chat.Messages, pub_models.Message{
			Role:    "assistant",
			Content: fmt.Sprintf("handover %d\n\nremaining: docs %d", iteration, iteration),
		})
		if iteration == 2 {
			if _, err := recorder.CallWithContext(ctx, loopInput("achieved", "done")); err != nil {
				return pub_models.Chat{}, err
			}
		}
		return chat, nil
	}
	goal := "Please finalize this: ## Handover summary\n\nbody of the prompt\n\nremaining: docs"
	runner := NewLoopRunner(fake, recorder, LoopConfig{SeedChat: loopSeedChat(), GoalPrompt: goal, MaxIterations: 5})

	if err := runner.Query(t.Context()); err != nil {
		t.Fatalf("Query: %v", err)
	}

	if len(fake.progress) != 2 {
		t.Fatalf("SetLoopProgress calls = %d, want 2", len(fake.progress))
	}
	first := fake.progress[0]
	if first.Iteration != 1 || first.MaxIterations != 5 || first.Phase != "" {
		t.Fatalf("iteration 1 progress = %+v, want no phase before any iteration finishes", first)
	}
	second := fake.progress[1]
	if second.Iteration != 2 || second.Phase != "handover 1 · remaining: docs 1" {
		t.Fatalf("iteration 2 progress = %+v, want the iteration 1 handover progression", second)
	}

	if len(fake.finished) != 2 {
		t.Fatalf("FinishLoopIteration calls = %d, want 2", len(fake.finished))
	}
	if got := fake.finished[0].outcome; got != LoopOutcomeContinue {
		t.Fatalf("iteration 1 outcome = %d, want continues", got)
	}
	if got := fake.finished[1]; got.outcome != LoopOutcomeAchieved {
		t.Fatalf("iteration 2 outcome = %d, want achieved", got.outcome)
	} else if got.answer.Role != "assistant" || got.answer.Content != "handover 2\n\nremaining: docs 2" {
		t.Fatalf("achieved answer = %+v, want the closing assistant text", got.answer)
	}
}

func TestLoopRunner_AchievedAnswerFallsBackToSubmissionSummary(t *testing.T) {
	recorder := NewCompletionRecorder("")
	fake := &recordingLoopQuerier{}
	fake.handle = func(ctx context.Context, _ int, chat pub_models.Chat) (pub_models.Chat, error) {
		if _, err := recorder.CallWithContext(ctx, loopInput("achieved", "the summary")); err != nil {
			return pub_models.Chat{}, err
		}
		return chat, nil
	}
	runner := NewLoopRunner(fake, recorder, LoopConfig{SeedChat: loopSeedChat(), MaxIterations: 1})

	if err := runner.Query(t.Context()); err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(fake.finished) != 1 || fake.finished[0].answer.Content != "the summary" {
		t.Fatalf("finished = %+v, want the submission summary as the answer", fake.finished)
	}
}

func TestLoopRunner_FinishOutcomes(t *testing.T) {
	t.Run("blocked", func(t *testing.T) {
		recorder := NewCompletionRecorder("")
		fake := &recordingLoopQuerier{handle: func(ctx context.Context, _ int, chat pub_models.Chat) (pub_models.Chat, error) {
			if _, err := recorder.CallWithContext(ctx, loopInput("blocked", "Need credentials.")); err != nil {
				return pub_models.Chat{}, err
			}
			return chat, nil
		}}
		runner := NewLoopRunner(fake, recorder, LoopConfig{SeedChat: loopSeedChat(), MaxIterations: 3})

		err := runner.Query(t.Context())
		if !errors.Is(err, ErrLoopBlocked) {
			t.Fatalf("Query error = %v, want ErrLoopBlocked", err)
		}
		if len(fake.finished) != 1 || fake.finished[0].outcome != LoopOutcomeBlocked {
			t.Fatalf("finished = %+v, want one blocked outcome", fake.finished)
		}
	})

	t.Run("exhausted", func(t *testing.T) {
		fake := &recordingLoopQuerier{handle: func(_ context.Context, iteration int, chat pub_models.Chat) (pub_models.Chat, error) {
			chat.Messages = append(chat.Messages, pub_models.Message{Role: "assistant", Content: fmt.Sprintf("handover %d", iteration)})
			return chat, nil
		}}
		runner := NewLoopRunner(fake, NewCompletionRecorder(""), LoopConfig{SeedChat: loopSeedChat(), MaxIterations: 2})

		err := runner.Query(t.Context())
		if !errors.Is(err, ErrMaxIterations) {
			t.Fatalf("Query error = %v, want ErrMaxIterations", err)
		}
		outcomes := make([]LoopOutcome, 0, len(fake.finished))
		for _, call := range fake.finished {
			outcomes = append(outcomes, call.outcome)
		}
		if want := []LoopOutcome{LoopOutcomeContinue, LoopOutcomeExhausted}; len(outcomes) != len(want) || outcomes[0] != want[0] || outcomes[1] != want[1] {
			t.Fatalf("outcomes = %v, want %v", outcomes, want)
		}
	})
}

func TestLoopRunner_DisplayErrorPropagates(t *testing.T) {
	displayErr := errors.New("display failed")

	t.Run("achieved", func(t *testing.T) {
		recorder := NewCompletionRecorder("")
		fake := &recordingLoopQuerier{finishErr: displayErr, handle: func(ctx context.Context, _ int, chat pub_models.Chat) (pub_models.Chat, error) {
			if _, err := recorder.CallWithContext(ctx, loopInput("achieved", "done")); err != nil {
				return pub_models.Chat{}, err
			}
			return chat, nil
		}}
		runner := NewLoopRunner(fake, recorder, LoopConfig{SeedChat: loopSeedChat(), MaxIterations: 1})

		err := runner.Query(t.Context())
		if !errors.Is(err, displayErr) {
			t.Fatalf("Query error = %v, want the display error", err)
		}
	})

	t.Run("blocked keeps both errors", func(t *testing.T) {
		recorder := NewCompletionRecorder("")
		fake := &recordingLoopQuerier{finishErr: displayErr, handle: func(ctx context.Context, _ int, chat pub_models.Chat) (pub_models.Chat, error) {
			if _, err := recorder.CallWithContext(ctx, loopInput("blocked", "Need credentials.")); err != nil {
				return pub_models.Chat{}, err
			}
			return chat, nil
		}}
		runner := NewLoopRunner(fake, recorder, LoopConfig{SeedChat: loopSeedChat(), MaxIterations: 1})

		err := runner.Query(t.Context())
		if !errors.Is(err, ErrLoopBlocked) || !errors.Is(err, displayErr) {
			t.Fatalf("Query error = %v, want both the blocked and the display error", err)
		}
	})

	t.Run("continue aborts the next iteration", func(t *testing.T) {
		fake := &recordingLoopQuerier{finishErr: displayErr, handle: func(_ context.Context, _ int, chat pub_models.Chat) (pub_models.Chat, error) {
			return chat, nil
		}}
		runner := NewLoopRunner(fake, NewCompletionRecorder(""), LoopConfig{SeedChat: loopSeedChat(), MaxIterations: 3})

		err := runner.Query(t.Context())
		if !errors.Is(err, displayErr) {
			t.Fatalf("Query error = %v, want the display error", err)
		}
		if len(fake.chats) != 1 {
			t.Fatalf("TextQuery calls = %d, want the loop to stop after the failed display", len(fake.chats))
		}
	})
}

// rollingLoopQuerier returns a querier whose rolling window is enabled and
// writes to printed.
func rollingLoopQuerier(t *testing.T, printed *strings.Builder) *Querier[*MockQuerier] {
	t.Helper()
	t.Setenv("NO_COLOR", "true")
	withRollingTheme(t)
	return &Querier[*MockQuerier]{
		out:   printed,
		dims:  dimensions.Dimensions{Width: 80, Height: 24},
		Model: &MockQuerier{},
	}
}

func TestFinishLoopIteration_RequiresProgress(t *testing.T) {
	var printed strings.Builder
	q := rollingLoopQuerier(t, &printed)

	err := q.FinishLoopIteration(LoopOutcomeAchieved, pub_models.Message{Role: "assistant", Content: "done"})
	if err == nil || !strings.Contains(err.Error(), "no loop progress") {
		t.Fatalf("FinishLoopIteration error = %v, want a missing-progress error", err)
	}
}

func TestFinishLoopIteration_ContinueCollapsesToHeader(t *testing.T) {
	var printed strings.Builder
	q := rollingLoopQuerier(t, &printed)
	q.SetLoopProgress(LoopProgress{Iteration: 2, MaxIterations: 10, Phase: "tests pass"})
	q.ensureActivityViewport()
	q.activityViewport.AppendReasoning("inspect the repository")
	q.activityViewport.AppendTool(pub_models.Call{Name: "ls"}, "one\ntwo", 4)
	if err := q.activityViewport.Render(&printed); err != nil {
		t.Fatalf("Render: %v", err)
	}

	if err := q.FinishLoopIteration(LoopOutcomeContinue, pub_models.Message{}); err != nil {
		t.Fatalf("FinishLoopIteration: %v", err)
	}
	if q.loopProgress != nil {
		t.Fatal("loop progress must be cleared after the iteration finishes")
	}
	want := []string{"▸ loop 2/10 - tests pass"}
	if rows := q.activityViewport.Rows(); !slices.Equal(rows, want) {
		t.Fatalf("rows after collapse = %q, want %q", rows, want)
	}
}

func TestFinishLoopIteration_ContinueWithoutActivityPrintsHeader(t *testing.T) {
	var printed strings.Builder
	q := rollingLoopQuerier(t, &printed)
	q.SetLoopProgress(LoopProgress{Iteration: 1, MaxIterations: 4, Phase: "initial prompt"})
	q.ensureActivityViewport()

	if err := q.FinishLoopIteration(LoopOutcomeContinue, pub_models.Message{}); err != nil {
		t.Fatalf("FinishLoopIteration: %v", err)
	}
	out := stripANSIEscapes(printed.String())
	if want := "▸ loop 1/4 - initial prompt"; strings.Count(out, want) != 1 {
		t.Fatalf("header line %q rendered %d times, want once; output=%q", want, strings.Count(out, want), out)
	}
}

func TestFinishLoopIteration_AchievedPrintsAnswerBelowHeader(t *testing.T) {
	var printed strings.Builder
	q := rollingLoopQuerier(t, &printed)
	q.SetLoopProgress(LoopProgress{Iteration: 2, MaxIterations: 10, Phase: "tests pass"})
	q.ensureActivityViewport()
	q.activityViewport.AppendReasoning("inspect the repository")
	if err := q.activityViewport.Render(&printed); err != nil {
		t.Fatalf("Render: %v", err)
	}
	q.activityViewport.CollapseToHeader()
	if err := q.activityViewport.RenderPinned(&printed); err != nil {
		t.Fatalf("RenderPinned: %v", err)
	}

	before := printed.Len()
	answer := pub_models.Message{Role: "assistant", Content: "all done"}
	if err := q.FinishLoopIteration(LoopOutcomeAchieved, answer); err != nil {
		t.Fatalf("FinishLoopIteration: %v", err)
	}
	suffix := printed.String()[before:]
	if !strings.HasSuffix(suffix, "assistant: all done\n") {
		t.Fatalf("answer must print below the header rows, suffix = %q", suffix)
	}
	if strings.Contains(suffix, "\x1b[") {
		t.Fatalf("the answer must not move the cursor over the header rows, suffix = %q", suffix)
	}
	want := []string{"▸ loop 2/10 - tests pass"}
	if rows := q.activityViewport.Rows(); !slices.Equal(rows, want) {
		t.Fatalf("the header rows must stay as the progress record, rows = %q", rows)
	}
}

func TestFinishLoopIteration_BlockedPrintsNothing(t *testing.T) {
	var printed strings.Builder
	q := rollingLoopQuerier(t, &printed)
	q.SetLoopProgress(LoopProgress{Iteration: 1, MaxIterations: 4})
	q.ensureActivityViewport()
	q.activityViewport.AppendReasoning("inspect the repository")
	if err := q.activityViewport.Render(&printed); err != nil {
		t.Fatalf("Render: %v", err)
	}

	before := printed.Len()
	if err := q.FinishLoopIteration(LoopOutcomeBlocked, pub_models.Message{}); err != nil {
		t.Fatalf("FinishLoopIteration: %v", err)
	}
	if stripped := strings.Trim(stripANSIEscapes(printed.String()[before:]), "\r"); stripped != "" {
		t.Fatalf("blocked iteration must not print an answer, got %q", stripped)
	}
}

func TestFinishLoopIteration_NonRollingIsNoop(t *testing.T) {
	var printed strings.Builder
	q := &Querier[*MockQuerier]{out: &printed, Raw: true, dims: dimensions.Dimensions{Width: 80, Height: 24}}
	q.SetLoopProgress(LoopProgress{Iteration: 1, MaxIterations: 2})

	if err := q.FinishLoopIteration(LoopOutcomeAchieved, pub_models.Message{Role: "assistant", Content: "done"}); err != nil {
		t.Fatalf("FinishLoopIteration: %v", err)
	}
	if printed.Len() != 0 {
		t.Fatalf("non-rolling iteration must print nothing, got %q", printed.String())
	}
	if q.loopProgress != nil {
		t.Fatal("loop progress must be cleared after the iteration finishes")
	}
}

// withRollingDisabledTheme loads a theme whose rolling output is off and
// restores the previous theme after the test.
func withRollingDisabledTheme(t *testing.T) {
	t.Helper()
	confDir := withEmptyClaiConfigDir(t)
	if err := os.MkdirAll(confDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(%q): %v", confDir, err)
	}
	themePath := filepath.Join(confDir, "theme.json")
	if err := os.WriteFile(themePath, []byte(`{"rollingOutput":{"enabled":false}}`), 0o644); err != nil {
		t.Fatalf("WriteFile(%q): %v", themePath, err)
	}
	if err := utils.LoadTheme(confDir); err != nil {
		t.Fatalf("LoadTheme(%q): %v", confDir, err)
	}
	t.Cleanup(func() {
		if err := utils.LoadTheme(t.TempDir()); err != nil {
			t.Errorf("reset theme: %v", err)
		}
	})
}

// TestFinishLoopIteration_DrawnWindowOutlivesRollingOutput proves that a drawn
// window, not the current rolling setting, decides whether the loop finishes
// its display: the rows exist on the terminal and the loop owns them.
func TestFinishLoopIteration_DrawnWindowOutlivesRollingOutput(t *testing.T) {
	t.Run("achieved prints the answer once", func(t *testing.T) {
		var printed strings.Builder
		q := rollingLoopQuerier(t, &printed)
		q.SetLoopProgress(LoopProgress{Iteration: 1, MaxIterations: 4})
		q.ensureActivityViewport()
		q.activityViewport.AppendReasoning("inspect the repository")
		if err := q.activityViewport.Render(&printed); err != nil {
			t.Fatalf("Render: %v", err)
		}
		withRollingDisabledTheme(t)

		answer := pub_models.Message{Role: "assistant", Content: "all done"}
		if err := q.FinishLoopIteration(LoopOutcomeAchieved, answer); err != nil {
			t.Fatalf("FinishLoopIteration: %v", err)
		}
		out := stripANSIEscapes(printed.String())
		if got := strings.Count(out, "assistant: all done"); got != 1 {
			t.Fatalf("answer printed %d times, want once; output=%q", got, out)
		}
	})

	t.Run("continue collapses the drawn window", func(t *testing.T) {
		var printed strings.Builder
		q := rollingLoopQuerier(t, &printed)
		q.SetLoopProgress(LoopProgress{Iteration: 2, MaxIterations: 10, Phase: "tests pass"})
		q.ensureActivityViewport()
		q.activityViewport.AppendReasoning("inspect the repository")
		if err := q.activityViewport.Render(&printed); err != nil {
			t.Fatalf("Render: %v", err)
		}
		withRollingDisabledTheme(t)

		if err := q.FinishLoopIteration(LoopOutcomeContinue, pub_models.Message{}); err != nil {
			t.Fatalf("FinishLoopIteration: %v", err)
		}
		want := []string{"▸ loop 2/10 - tests pass"}
		if rows := q.activityViewport.Rows(); !slices.Equal(rows, want) {
			t.Fatalf("rows after collapse = %q, want %q", rows, want)
		}
	})
}

func TestFinishLoopIteration_UnknownOutcomeReturnsError(t *testing.T) {
	var printed strings.Builder
	q := rollingLoopQuerier(t, &printed)
	q.SetLoopProgress(LoopProgress{Iteration: 1, MaxIterations: 2})

	err := q.FinishLoopIteration(LoopOutcome(200), pub_models.Message{})
	if err == nil || !strings.Contains(err.Error(), "unknown outcome") {
		t.Fatalf("FinishLoopIteration error = %v, want an unknown-outcome error", err)
	}
	if q.loopProgress == nil {
		t.Fatal("a rejected outcome must not clear the loop progress")
	}
}

func TestFinishLoopIteration_ContinueWithoutViewportIsNoop(t *testing.T) {
	var printed strings.Builder
	q := rollingLoopQuerier(t, &printed)
	q.SetLoopProgress(LoopProgress{Iteration: 1, MaxIterations: 2})

	if err := q.FinishLoopIteration(LoopOutcomeContinue, pub_models.Message{}); err != nil {
		t.Fatalf("FinishLoopIteration: %v", err)
	}
	if printed.Len() != 0 {
		t.Fatalf("a window that was never drawn must not print, got %q", printed.String())
	}
	if q.loopProgress != nil {
		t.Fatal("loop progress must be cleared after the iteration finishes")
	}
}

func TestFinishLoopIteration_AchievedPrintFailureReturnsError(t *testing.T) {
	var printed strings.Builder
	q := rollingLoopQuerier(t, &printed)
	q.SetLoopProgress(LoopProgress{Iteration: 1, MaxIterations: 2})
	q.ensureActivityViewport()
	q.out = failingWriter{}

	err := q.FinishLoopIteration(LoopOutcomeAchieved, pub_models.Message{Role: "assistant", Content: "done"})
	if err == nil || !strings.Contains(err.Error(), "print achieved loop answer") {
		t.Fatalf("FinishLoopIteration error = %v, want the answer print failure", err)
	}
}

func TestClipRunes_NonPositiveLimitIsEmpty(t *testing.T) {
	if got := clipRunes("some text", 0); got != "" {
		t.Fatalf("clipRunes with limit 0 = %q, want empty", got)
	}
}

func TestQuerier_TextQueryDefersLoopAnswerUntilFinish(t *testing.T) {
	var printed strings.Builder
	q := rollingLoopQuerier(t, &printed)
	model := q.Model
	model.streamFn = func(context.Context, pub_models.Chat) (chan models.CompletionEvent, error) {
		events := make(chan models.CompletionEvent, 2)
		events <- models.ReasoningEvent{Content: "inspect"}
		events <- "The answer is done."
		close(events)
		return events, nil
	}
	q.SetLoopProgress(LoopProgress{Iteration: 1, MaxIterations: 4})

	chat := pub_models.Chat{Messages: []pub_models.Message{{Role: "user", Content: "hello"}}}
	if _, err := q.TextQuery(t.Context(), chat); err != nil {
		t.Fatalf("TextQuery: %v", err)
	}

	output := stripANSIEscapes(printed.String())
	wantHeader := "▸ loop 1/4"
	if !strings.Contains(output, wantHeader) {
		t.Fatalf("missing pinned loop header %q in output:\n%s", wantHeader, output)
	}
	if strings.Contains(output, "assistant: The answer is done.") {
		t.Fatalf("a loop iteration must defer its answer print, got:\n%s", output)
	}
	if !strings.Contains(output, "assistant\n  The answer is done.") {
		t.Fatalf("the answer must still stream inside the window, got:\n%s", output)
	}

	before := printed.Len()
	answer := pub_models.Message{Role: "assistant", Content: "The answer is done."}
	if err := q.FinishLoopIteration(LoopOutcomeAchieved, answer); err != nil {
		t.Fatalf("FinishLoopIteration: %v", err)
	}
	if stripped := stripANSIEscapes(printed.String()[before:]); !strings.Contains(stripped, "assistant: The answer is done.") {
		t.Fatalf("the achieved answer must print where the window was, got %q", stripped)
	}
}

// TestLoopRunner_SettlesBeforeFinalizerChatter pins that an iteration whose
// stream ends with an EOF event still collapses its window before the
// finalizer's chat-index progress reaches the terminal. The progress line moves
// the cursor below the frame; a collapse after it would move up over that line
// and strand the frame's first body row in the scrollback.
func TestLoopRunner_SettlesBeforeFinalizerChatter(t *testing.T) {
	t.Setenv("NO_COLOR", "true")
	withRollingTheme(t)
	confDir := withEmptyClaiConfigDir(t)
	terminal := filepath.Join(t.TempDir(), "terminal.log")
	f, err := os.Create(terminal)
	if err != nil {
		t.Fatalf("create terminal log: %v", err)
	}
	t.Cleanup(func() { _ = f.Close() })
	origStderr := os.Stderr
	os.Stderr = f
	t.Cleanup(func() { os.Stderr = origStderr })

	q := &Querier[*MockQuerier]{
		out:             f,
		dims:            dimensions.Dimensions{Width: 80, Height: 24},
		configDir:       confDir,
		Model:           &MockQuerier{},
		shouldSaveReply: true,
	}
	q.Model.streamFn = func(context.Context, pub_models.Chat) (chan models.CompletionEvent, error) {
		events := make(chan models.CompletionEvent, 3)
		events <- models.ReasoningEvent{Content: "inspect the repository"}
		events <- "iteration answer"
		events <- io.EOF
		close(events)
		return events, nil
	}
	runner := NewLoopRunner(q, NewCompletionRecorder(""), LoopConfig{
		SeedChat:      loopSeedChat(),
		GoalPrompt:    "refactor auth",
		MaxIterations: 3,
	})

	if err := runner.Query(t.Context()); !errors.Is(err, ErrMaxIterations) {
		t.Fatalf("Query error = %v, want ErrMaxIterations", err)
	}
	if err := f.Sync(); err != nil {
		t.Fatalf("sync terminal log: %v", err)
	}
	printed, err := os.ReadFile(terminal)
	if err != nil {
		t.Fatalf("read terminal log: %v", err)
	}
	rows := nonEmptyScreenRows(string(printed))
	var headers []string
	for _, row := range rows {
		switch {
		case strings.HasPrefix(row, "▸ loop"):
			headers = append(headers, row)
		case strings.HasPrefix(row, "Building cache index"), strings.Contains(row, "est. left:"):
			// The finalizer's chat-index progress is legitimate output below the
			// collapsed header, not a stranded frame row.
		default:
			t.Fatalf("screen row %q is a stranded frame row; screen = %q", row, rows)
		}
	}
	want := []string{
		"▸ loop 1/3",
		"▸ loop 2/3 - iteration answer",
		"▸ loop 3/3 - iteration answer",
	}
	if !slices.Equal(headers, want) {
		t.Fatalf("progress headers = %q, want one per iteration %q", headers, want)
	}
}

// TestQuerier_WarnfKeepsTheDrawnFrameIntact pins that a warning raised while a
// frame is drawn prints above it instead of moving the cursor behind the
// frame's bookkeeping, which would strand a row on the next collapse.
func TestQuerier_WarnfKeepsTheDrawnFrameIntact(t *testing.T) {
	var printed strings.Builder
	q := rollingLoopQuerier(t, &printed)
	q.SetLoopProgress(LoopProgress{Iteration: 1, MaxIterations: 2})
	q.ensureActivityViewport()
	q.activityViewport.AppendReasoning("inspect the repository")
	if err := q.activityViewport.Render(&printed); err != nil {
		t.Fatalf("Render: %v", err)
	}

	if err := q.warnf("stoploss: context usage ~40000 tokens reached max-tokens (40000)"); err != nil {
		t.Fatalf("warnf: %v", err)
	}
	if err := q.settleLoopHeader(); err != nil {
		t.Fatalf("settleLoopHeader: %v", err)
	}

	screen := nonEmptyScreenRows(printed.String())
	want := []string{
		"warning: stoploss: context usage ~40000 tokens reached max-tokens (40000)",
		"▸ loop 1/2",
	}
	if !slices.Equal(screen, want) {
		t.Fatalf("screen rows = %q, want the warning above the collapsed header %q", screen, want)
	}
}

// TestStoploss_LoopIterationLeavesTheHeaderClean pins that a token stoploss
// crossing inside a loop iteration injects the handover but prints no notice:
// the iteration header is the progress record, and reaching the budget is the
// loop's expected operation.
func TestStoploss_LoopIterationLeavesTheHeaderClean(t *testing.T) {
	var printed strings.Builder
	q := rollingLoopQuerier(t, &printed)
	q.SetLoopProgress(LoopProgress{Iteration: 2, MaxIterations: 4})
	q.ensureActivityViewport()
	q.activityViewport.AppendTool(pub_models.Call{Name: "ls"}, "one", 4)
	if err := q.activityViewport.Render(&printed); err != nil {
		t.Fatalf("Render: %v", err)
	}
	ctrl := q.newStoploss()
	ctrl.maxTokens = 10
	session := &QuerySession{Chat: pub_models.Chat{}}

	injected, err := ctrl.CheckContextBudget(t.Context(), nil, session, &pub_models.Usage{PromptTokens: 100})
	if err != nil {
		t.Fatalf("CheckContextBudget: %v", err)
	}
	if !injected {
		t.Fatal("CheckContextBudget must inject the handover on the first crossing")
	}
	if err := q.settleLoopHeader(); err != nil {
		t.Fatalf("settleLoopHeader: %v", err)
	}
	screen := nonEmptyScreenRows(printed.String())
	want := []string{"▸ loop 2/4"}
	if !slices.Equal(screen, want) {
		t.Fatalf("screen rows = %q, want the clean collapsed header %q", screen, want)
	}
}

// TestStoploss_WarnsOutsideLoopIterations pins that a plain query still reports
// the crossing above the drawn frame.
func TestStoploss_WarnsOutsideLoopIterations(t *testing.T) {
	var printed strings.Builder
	q := rollingLoopQuerier(t, &printed)
	q.ensureActivityViewport()
	q.activityViewport.AppendTool(pub_models.Call{Name: "ls"}, "one", 4)
	if err := q.activityViewport.Render(&printed); err != nil {
		t.Fatalf("Render: %v", err)
	}
	ctrl := q.newStoploss()
	ctrl.maxTokens = 10
	session := &QuerySession{Chat: pub_models.Chat{}}

	injected, err := ctrl.CheckContextBudget(t.Context(), nil, session, &pub_models.Usage{PromptTokens: 100})
	if err != nil {
		t.Fatalf("CheckContextBudget: %v", err)
	}
	if !injected {
		t.Fatal("CheckContextBudget must inject the handover on the first crossing")
	}
	screen := nonEmptyScreenRows(printed.String())
	if len(screen) == 0 || !strings.HasPrefix(screen[0], "warning: stoploss: context usage") {
		t.Fatalf("screen rows = %q, want the stoploss notice above the frame", screen)
	}
}

// interpretScreen replays the cursor movements the rolling window emits and
// returns the visible screen as rows. It understands newline, carriage return,
// cursor-up, and erase-below; colour and other escapes are ignored.
func interpretScreen(output string) []string {
	rows := []string{""}
	row, col := 0, 0
	writeText := func(text string) {
		cells := []rune(rows[row])
		for len(cells) < col {
			cells = append(cells, ' ')
		}
		for _, r := range text {
			if col < len(cells) {
				cells[col] = r
			} else {
				cells = append(cells, r)
			}
			col++
		}
		rows[row] = string(cells)
	}
	for i := 0; i < len(output); {
		switch {
		case output[i] == '\n':
			row++
			if row == len(rows) {
				rows = append(rows, "")
			}
			col = 0
			i++
		case output[i] == '\r':
			col = 0
			i++
		case output[i] == 0x1b && i+1 < len(output) && output[i+1] == '[':
			j := i + 2
			for j < len(output) && (output[j] == ';' || (output[j] >= '0' && output[j] <= '9')) {
				j++
			}
			if j >= len(output) {
				i = len(output)
				continue
			}
			n := 0
			if digits := output[i+2 : j]; digits != "" {
				n, _ = strconv.Atoi(strings.SplitN(digits, ";", 2)[0])
			}
			switch output[j] {
			case 'A':
				row = max(row-n, 0)
			case 'J':
				cells := []rune(rows[row])
				if col < len(cells) {
					cells = cells[:col]
				}
				rows = append(rows[:row+1], "")
				rows[row] = string(cells)
			}
			i = j + 1
		default:
			end := i
			for end < len(output) && output[end] != '\n' && output[end] != '\r' && output[end] != 0x1b {
				end++
			}
			writeText(output[i:end])
			i = end
		}
	}
	return rows
}

// nonEmptyScreenRows drops blank rows so a test can assert on the visible text
// only.
func nonEmptyScreenRows(output string) []string {
	var rows []string
	for _, row := range interpretScreen(output) {
		if trimmed := strings.TrimSpace(row); trimmed != "" {
			rows = append(rows, trimmed)
		}
	}
	return rows
}

// labelWriter records output written from another goroutine and closes marked
// once the recorded text contains label. A test uses it to release a blocked
// stream exactly when the label landed, instead of sleeping.
type labelWriter struct {
	mu     sync.Mutex
	buf    strings.Builder
	label  string
	marked chan struct{}
	once   sync.Once
}

func newLabelWriter(label string) *labelWriter {
	return &labelWriter{label: label, marked: make(chan struct{})}
}

func (w *labelWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.buf.Write(p)
	if strings.Contains(stripANSIEscapes(w.buf.String()), w.label) {
		w.once.Do(func() { close(w.marked) })
	}
	return len(p), nil
}

func (w *labelWriter) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.String()
}

func TestLoopRunner_LeavesOneHeaderPerIterationInScrollback(t *testing.T) {
	var printed strings.Builder
	q := rollingLoopQuerier(t, &printed)
	q.Model.streamFn = func(context.Context, pub_models.Chat) (chan models.CompletionEvent, error) {
		events := make(chan models.CompletionEvent, 3)
		events <- models.ReasoningEvent{Content: "inspect the repository"}
		events <- "still working on it"
		events <- models.StopEvent{}
		close(events)
		return events, nil
	}
	runner := NewLoopRunner(q, NewCompletionRecorder(""), LoopConfig{
		SeedChat:      loopSeedChat(),
		GoalPrompt:    "refactor auth",
		MaxIterations: 3,
	})

	if err := runner.Query(t.Context()); !errors.Is(err, ErrMaxIterations) {
		t.Fatalf("Query error = %v, want ErrMaxIterations", err)
	}
	rows := nonEmptyScreenRows(printed.String())
	want := []string{
		"▸ loop 1/3",
		"▸ loop 2/3 - still working on it",
		"▸ loop 3/3 - still working on it",
	}
	if !slices.Equal(rows, want) {
		t.Fatalf("screen rows = %q, want one progress header per iteration %q", rows, want)
	}
}

// TestQuerier_LoopPhaseSummaryRewritesTheHeaderMidStream pins that the landed
// summary replaces the placeholder while the iteration still streams.
func TestQuerier_LoopPhaseSummaryRewritesTheHeaderMidStream(t *testing.T) {
	out := newLabelWriter(" - S")
	t.Setenv("NO_COLOR", "true")
	withRollingTheme(t)
	confDir := t.TempDir()
	seedEmptyChatIndex(t, confDir)
	q := &Querier[*MockQuerier]{
		out:                    out,
		dims:                   dimensions.Dimensions{Width: 80, Height: 24},
		configDir:              confDir,
		Model:                  &MockQuerier{},
		summarizer:             instantSummarizer(),
		summarizeConversations: true,
		shouldSaveReply:        true,
		summaryJoinTimeout:     5 * time.Second,
		summaryInterrupt:       make(chan struct{}),
		runModel:               "run-model",
	}
	q.SetLoopProgress(LoopProgress{Iteration: 2, MaxIterations: 2, Phase: "prior phase"})
	q.Model.streamFn = func(ctx context.Context, _ pub_models.Chat) (chan models.CompletionEvent, error) {
		events := make(chan models.CompletionEvent, 2)
		events <- models.ReasoningEvent{Content: "inspect the repository"}
		go func() {
			// The stream ends only after the session loop rendered the landed
			// summary, so the rewrite provably lands mid-stream.
			select {
			case <-out.marked:
			case <-ctx.Done():
			case <-time.After(5 * time.Second):
			}
			events <- "still working on it"
			close(events)
		}()
		return events, nil
	}

	chat := pub_models.Chat{Messages: []pub_models.Message{{Role: "user", Content: "refactor auth"}}}
	if _, err := q.TextQuery(t.Context(), chat); err != nil {
		t.Fatalf("TextQuery: %v", err)
	}
	rows := nonEmptyScreenRows(out.String())
	want := []string{"▸ loop 2/2 - S"}
	if !slices.Equal(rows, want) {
		t.Fatalf("screen rows = %q, want the landed phase on the header %q", rows, want)
	}
}

// TestQuerier_QueryDecidesThePendingPhaseFromTheIterationChat pins the ordering
// the loop runner relies on: SetLoopProgress runs before the iteration chat
// exists, so Query decides the placeholder from that chat. A fresh iteration
// shows it; a resumed seed that already carries a summary launches no
// summarizer and shows none.
func TestQuerier_QueryDecidesThePendingPhaseFromTheIterationChat(t *testing.T) {
	cases := []struct {
		name        string
		summary     string
		wantPending bool
	}{
		{name: "fresh iteration", wantPending: true},
		{name: "resumed seed with a summary", summary: "labelled before resuming"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("NO_COLOR", "true")
			withRollingTheme(t)
			confDir := t.TempDir()
			seedEmptyChatIndex(t, confDir)
			var printed strings.Builder
			q := &Querier[*MockQuerier]{
				out:                    &printed,
				dims:                   dimensions.Dimensions{Width: 80, Height: 24},
				configDir:              confDir,
				Model:                  &MockQuerier{streamFn: echoStream("answer")},
				summarizer:             instantSummarizer(),
				summarizeConversations: true,
				shouldSaveReply:        true,
				summaryJoinTimeout:     5 * time.Second,
				summaryInterrupt:       make(chan struct{}),
				runModel:               "run-model",
			}
			q.SetLoopProgress(LoopProgress{Iteration: 1, MaxIterations: 2})
			chat := newChat("iteration-one")
			chat.Summary = tc.summary
			if _, err := q.TextQuery(t.Context(), chat); err != nil {
				t.Fatalf("TextQuery: %v", err)
			}
			if got := strings.Contains(printed.String(), loopSummarizingPlaceholder); got != tc.wantPending {
				t.Fatalf("placeholder rendered = %v, want %v; output %q", got, tc.wantPending, printed.String())
			}
		})
	}
}

// TestQuerier_MarkLoopPhasePendingMatchesTheSummaryLaunch pins that the pending
// placeholder follows the summarizer's own launch decision. SetLoopProgress runs
// before the iteration's chat exists, so it cannot decide.
func TestQuerier_MarkLoopPhasePendingMatchesTheSummaryLaunch(t *testing.T) {
	launching := launchQuerier(t, instantSummarizer(), newChat("loop"))
	launching.SetLoopProgress(LoopProgress{Iteration: 1, MaxIterations: 2})
	if launching.loopProgress.PhasePending {
		t.Fatal("SetLoopProgress must not decide the phase before the iteration chat exists")
	}
	launching.markLoopPhasePending()
	if !launching.loopProgress.PhasePending {
		t.Fatal("a loop run that launches a summarizer must start with a pending phase")
	}

	bare := launchQuerier(t, nil, newChat("bare"))
	bare.SetLoopProgress(LoopProgress{Iteration: 1, MaxIterations: 2})
	bare.markLoopPhasePending()
	if bare.loopProgress.PhasePending {
		t.Fatal("a loop run without a summarizer must not wait for a phase")
	}

	resumed := newChat("resumed")
	resumed.Summary = "labelled before the loop resumed"
	labelled := launchQuerier(t, instantSummarizer(), resumed)
	labelled.SetLoopProgress(LoopProgress{Iteration: 1, MaxIterations: 2})
	labelled.markLoopPhasePending()
	if labelled.loopProgress.PhasePending {
		t.Fatal("a chat that already carries a summary launches no summarizer, so nothing is pending")
	}

	fresh := launchQuerier(t, instantSummarizer(), newChat("iteration-two"))
	fresh.SetLoopProgress(LoopProgress{Iteration: 2, MaxIterations: 2, Phase: "prior phase"})
	fresh.markLoopPhasePending()
	if !fresh.loopProgress.PhasePending {
		t.Fatal("a fresh later iteration launches a summarizer, so its phase starts pending")
	}
}

// TestQuerier_LoopHeaderShowsTheSummarizingPlaceholderUntilThePhaseLands pins
// the header timing: the first frame names the pending summary, then the landed
// summary replaces it in place.
func TestQuerier_LoopHeaderShowsTheSummarizingPlaceholderUntilThePhaseLands(t *testing.T) {
	var printed strings.Builder
	q := rollingLoopQuerier(t, &printed)
	q.summarizer = instantSummarizer()
	q.summarizeConversations = true
	q.shouldSaveReply = true
	q.SetLoopProgress(LoopProgress{Iteration: 1, MaxIterations: 2})
	q.markLoopPhasePending()
	q.ensureActivityViewport()
	if err := q.activityViewport.RenderPinned(&printed); err != nil {
		t.Fatalf("RenderPinned: %v", err)
	}
	wantPending := []string{"▸ loop 1/2 - " + loopSummarizingPlaceholder}
	if rows := q.activityViewport.Rows(); !slices.Equal(rows, wantPending) {
		t.Fatalf("first frame rows = %q, want the pending summary %q", rows, wantPending)
	}

	if err := q.awaitSummaryResult(summaryResult{summary: fakeSummary()}); err != nil {
		t.Fatalf("awaitSummaryResult: %v", err)
	}
	wantLanded := []string{"▸ loop 1/2 - S"}
	if rows := q.activityViewport.Rows(); !slices.Equal(rows, wantLanded) {
		t.Fatalf("rows after the summary landed = %q, want %q", rows, wantLanded)
	}
}

// TestQuerier_TextQueryDrawsTheLoopHeaderBeforeAnyActivity pins that a silent
// first model step still shows which iteration runs.
func TestQuerier_TextQueryDrawsTheLoopHeaderBeforeAnyActivity(t *testing.T) {
	var printed strings.Builder
	q := rollingLoopQuerier(t, &printed)
	q.Model.streamFn = func(context.Context, pub_models.Chat) (chan models.CompletionEvent, error) {
		events := make(chan models.CompletionEvent, 1)
		events <- models.StopEvent{}
		close(events)
		return events, nil
	}
	q.SetLoopProgress(LoopProgress{Iteration: 1, MaxIterations: 2})

	chat := pub_models.Chat{Messages: []pub_models.Message{{Role: "user", Content: "hello"}}}
	if _, err := q.TextQuery(t.Context(), chat); err != nil {
		t.Fatalf("TextQuery: %v", err)
	}
	rows := nonEmptyScreenRows(printed.String())
	if want := []string{"▸ loop 1/2"}; !slices.Equal(rows, want) {
		t.Fatalf("screen rows = %q, want the iteration header before any activity %q", rows, want)
	}
}

// TestQuerier_LoopIterationLeavesItsHeaderInScrollback pins that each iteration
// keeps its header on screen: the finalizer's cost and cache lines land between
// iterations, so a frame that rewrites the previous header erases the progress
// record and strands a bare marker.
func TestQuerier_LoopIterationLeavesItsHeaderInScrollback(t *testing.T) {
	var printed strings.Builder
	q := rollingLoopQuerier(t, &printed)
	q.Model.streamFn = func(_ context.Context, chat pub_models.Chat) (chan models.CompletionEvent, error) {
		events := make(chan models.CompletionEvent, 2)
		events <- fmt.Sprintf("iteration %d answer", len(chat.Messages))
		events <- models.StopEvent{}
		close(events)
		return events, nil
	}
	chat := pub_models.Chat{Messages: []pub_models.Message{{Role: "user", Content: "hello"}}}

	q.SetLoopProgress(LoopProgress{Iteration: 1, MaxIterations: 2})
	if _, err := q.TextQuery(t.Context(), chat); err != nil {
		t.Fatalf("TextQuery iteration 1: %v", err)
	}
	if err := q.FinishLoopIteration(LoopOutcomeContinue, pub_models.Message{}); err != nil {
		t.Fatalf("FinishLoopIteration 1: %v", err)
	}
	printed.WriteString("cache log\n")

	q.SetLoopProgress(LoopProgress{Iteration: 2, MaxIterations: 2, Phase: "iteration two phase"})
	if _, err := q.TextQuery(t.Context(), chat); err != nil {
		t.Fatalf("TextQuery iteration 2: %v", err)
	}
	if err := q.FinishLoopIteration(LoopOutcomeContinue, pub_models.Message{}); err != nil {
		t.Fatalf("FinishLoopIteration 2: %v", err)
	}

	rows := nonEmptyScreenRows(printed.String())
	want := []string{"▸ loop 1/2", "cache log", "▸ loop 2/2 - iteration two phase"}
	if !slices.Equal(rows, want) {
		t.Fatalf("screen rows = %q, want every iteration header kept in scrollback %q", rows, want)
	}
}
