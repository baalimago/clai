package text

import (
	"context"
	"strings"
	"testing"

	"github.com/baalimago/clai/internal/models"
	pub_models "github.com/baalimago/clai/pkg/text/models"
	"github.com/baalimago/go_away_boilerplate/pkg/dimensions"
)

// Test_sessionRunner_Run_EmptyReasoningPrintsNothing pins that a model which
// streams thinking blocks with no visible text (Anthropic's omitted thinking
// display) prints no empty [thinking] / [/thinking] wrapper.
func Test_sessionRunner_Run_EmptyReasoningPrintsNothing(t *testing.T) {
	model := &MockQuerier{}
	model.streamFn = func(_ context.Context, _ pub_models.Chat) (chan models.CompletionEvent, error) {
		out := make(chan models.CompletionEvent, 4)
		out <- models.ReasoningEvent{Content: ""}
		out <- models.ReasoningEvent{Content: ""}
		out <- "answer"
		close(out)
		return out, nil
	}

	var printed strings.Builder
	q := &Querier[*MockQuerier]{Raw: true, out: &printed, dims: dimensions.Dimensions{Width: 80, Height: 24}, Model: model}
	session := &QuerySession{Chat: pub_models.Chat{Messages: []pub_models.Message{{Role: "user", Content: "hello"}}}}
	runner := sessionRunner[*MockQuerier]{
		querier:      q,
		recorder:     &recordingCallUsageRecorder{},
		finalizer:    sessionFinalizer[*MockQuerier]{querier: q},
		toolExecutor: toolExecutor[*MockQuerier]{querier: q},
	}

	if err := runner.Run(context.Background(), session); err != nil {
		t.Fatalf("Run returned err: %v", err)
	}
	if got := printed.String(); strings.Contains(got, "thinking]") {
		t.Fatalf("raw output = %q, want no thinking wrapper for empty reasoning", got)
	}
	if got := session.FinalAssistantText; got != "answer" {
		t.Fatalf("final assistant text = %q, want %q", got, "answer")
	}
}
