package text

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/baalimago/clai/internal/models"
	pub_models "github.com/baalimago/clai/pkg/text/models"
)

func stripANSIEscapes(s string) string {
	var out strings.Builder
	out.Grow(len(s))
	for i := 0; i < len(s); i++ {
		if s[i] != 0x1b {
			out.WriteByte(s[i])
			continue
		}
		i++
		if i >= len(s) {
			break
		}
		if s[i] != '[' {
			continue
		}
		for i+1 < len(s) {
			i++
			c := s[i]
			if c >= '@' && c <= '~' {
				break
			}
		}
	}
	return out.String()
}

type recordingCallUsageRecorder struct {
	calls []CompletedModelCall
	err   error
}

func (r *recordingCallUsageRecorder) Record(_ context.Context, call CompletedModelCall) error {
	r.calls = append(r.calls, call)
	if r.err != nil {
		return r.err
	}
	return nil
}

type countingFinalizer struct {
	count int
	last  *QuerySession
}

func (f *countingFinalizer) Finalize(session *QuerySession) {
	f.count++
	f.last = session
	if session == nil || session.Finalized {
		return
	}
	session.Finalized = true
}

func Test_sessionRunner_Run_SingleReplyRecordsCompletedCall(t *testing.T) {
	model := &MockQuerier{}
	model.streamFn = func(_ context.Context, _ pub_models.Chat) (chan models.CompletionEvent, error) {
		model.usage = &pub_models.Usage{PromptTokens: 3, CompletionTokens: 5, TotalTokens: 8}
		out := make(chan models.CompletionEvent, 2)
		out <- "hello"
		close(out)
		return out, nil
	}

	q := &Querier[*MockQuerier]{
		out:   &strings.Builder{},
		Model: model,
	}
	session := &QuerySession{Chat: pub_models.Chat{ID: "chat-1"}}
	recorder := &recordingCallUsageRecorder{}
	finalizer := &countingFinalizer{}
	runner := sessionRunner[*MockQuerier]{
		querier:      q,
		recorder:     recorder,
		finalizer:    finalizer,
		toolExecutor: toolExecutor[*MockQuerier]{querier: q},
	}

	err := runner.Run(context.Background(), session)
	if err != nil {
		t.Fatalf("Run returned err: %v", err)
	}
	if session.FinalAssistantText != "hello" {
		t.Fatalf("expected final assistant text, got %q", session.FinalAssistantText)
	}
	if session.FinalUsage == nil || session.FinalUsage.TotalTokens != 8 {
		t.Fatalf("expected final usage total 8, got %+v", session.FinalUsage)
	}
	if len(recorder.calls) != 1 {
		t.Fatalf("expected 1 recorded call, got %d", len(recorder.calls))
	}
	if !recorder.calls[0].EndedWithReply {
		t.Fatalf("expected completed call to end with reply, got %+v", recorder.calls[0])
	}
	if finalizer.count != 1 {
		t.Fatalf("expected finalizer once, got %d", finalizer.count)
	}
	if session.StartedAt.IsZero() || session.FinishedAt.IsZero() {
		t.Fatal("expected session timestamps to be populated")
	}
	if !session.FinishedAt.After(session.StartedAt) && !session.FinishedAt.Equal(session.StartedAt) {
		t.Fatalf("expected finished time after started time, got start=%v finish=%v", session.StartedAt, session.FinishedAt)
	}
}

func Test_sessionRunner_Run_ToolThenReplyRecordsEachCompletedCall(t *testing.T) {
	model := &MockQuerier{}
	callCount := 0
	model.streamFn = func(_ context.Context, _ pub_models.Chat) (chan models.CompletionEvent, error) {
		callCount++
		out := make(chan models.CompletionEvent, 2)
		if callCount == 1 {
			model.usage = &pub_models.Usage{PromptTokens: 2, CompletionTokens: 4, TotalTokens: 6}
			out <- pub_models.Call{ID: "call-1", Name: "pwd"}
			close(out)
			return out, nil
		}
		model.usage = &pub_models.Usage{PromptTokens: 3, CompletionTokens: 5, TotalTokens: 8}
		out <- "done"
		close(out)
		return out, nil
	}

	q := &Querier[*MockQuerier]{
		out:   &strings.Builder{},
		Model: model,
	}
	session := &QuerySession{Chat: pub_models.Chat{Messages: []pub_models.Message{{Role: "user", Content: "hello"}}}}
	recorder := &recordingCallUsageRecorder{}
	finalizer := &countingFinalizer{}
	runner := sessionRunner[*MockQuerier]{
		querier:      q,
		recorder:     recorder,
		finalizer:    finalizer,
		toolExecutor: toolExecutor[*MockQuerier]{querier: q},
	}

	err := runner.Run(context.Background(), session)
	if err != nil {
		t.Fatalf("Run returned err: %v", err)
	}
	if callCount != 2 {
		t.Fatalf("expected 2 model calls, got %d", callCount)
	}
	if len(recorder.calls) != 2 {
		t.Fatalf("expected 2 recorded calls, got %d", len(recorder.calls))
	}
	if !recorder.calls[0].EndedWithTool {
		t.Fatalf("expected first call to end with tool, got %+v", recorder.calls[0])
	}
	if !recorder.calls[1].EndedWithReply {
		t.Fatalf("expected second call to end with reply, got %+v", recorder.calls[1])
	}
	if session.FinalUsage == nil || session.FinalUsage.TotalTokens != 8 {
		t.Fatalf("expected final usage from final step, got %+v", session.FinalUsage)
	}
	if finalizer.count != 1 {
		t.Fatalf("expected finalizer once, got %d", finalizer.count)
	}
}

func Test_sessionRunner_Run_RecorderFailureDoesNotFailQuery(t *testing.T) {
	model := &MockQuerier{}
	model.streamFn = func(_ context.Context, _ pub_models.Chat) (chan models.CompletionEvent, error) {
		model.usage = &pub_models.Usage{TotalTokens: 1}
		out := make(chan models.CompletionEvent, 1)
		out <- "ok"
		close(out)
		return out, nil
	}

	q := &Querier[*MockQuerier]{out: &strings.Builder{}, Model: model}
	session := &QuerySession{}
	recorder := &recordingCallUsageRecorder{err: errors.New("record failed")}
	finalizer := &countingFinalizer{}
	runner := sessionRunner[*MockQuerier]{
		querier:      q,
		recorder:     recorder,
		finalizer:    finalizer,
		toolExecutor: toolExecutor[*MockQuerier]{querier: q},
	}

	err := runner.Run(context.Background(), session)
	if err != nil {
		t.Fatalf("expected recorder failure to be soft, got err: %v", err)
	}
	if len(recorder.calls) != 1 {
		t.Fatalf("expected recorder to be called once, got %d", len(recorder.calls))
	}
	if finalizer.count != 1 {
		t.Fatalf("expected finalizer once, got %d", finalizer.count)
	}
}

func Test_sessionRunner_Run_PartialStreamFailureFinalizesOnce(t *testing.T) {
	model := &MockQuerier{}
	model.streamFn = func(_ context.Context, _ pub_models.Chat) (chan models.CompletionEvent, error) {
		out := make(chan models.CompletionEvent, 2)
		out <- "partial"
		out <- errors.New("boom")
		close(out)
		return out, nil
	}

	q := &Querier[*MockQuerier]{out: &strings.Builder{}, Model: model}
	session := &QuerySession{}
	finalizer := &countingFinalizer{}
	runner := sessionRunner[*MockQuerier]{
		querier:      q,
		recorder:     &recordingCallUsageRecorder{},
		finalizer:    finalizer,
		toolExecutor: toolExecutor[*MockQuerier]{querier: q},
	}

	err := runner.Run(context.Background(), session)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "completion stream error: boom") {
		t.Fatalf("expected completion stream error, got %v", err)
	}
	if session.FinalAssistantText != "partial" {
		t.Fatalf("expected partial assistant text to be finalized, got %q", session.FinalAssistantText)
	}
	if finalizer.count != 1 {
		t.Fatalf("expected finalizer once, got %d", finalizer.count)
	}
}

func Test_sessionRunner_Run_DoesNotDuplicateToolCallEchoBeforeStructuredCall(t *testing.T) {
	model := &MockQuerier{}
	callCount := 0
	echoCall := pub_models.Call{
		ID:   "call-1",
		Name: "mcp_postgres_execute_sql",
		Inputs: &pub_models.Input{
			"sql": "SELECT 1",
		},
	}
	model.streamFn = func(_ context.Context, _ pub_models.Chat) (chan models.CompletionEvent, error) {
		callCount++
		out := make(chan models.CompletionEvent, 3)
		if callCount == 1 {
			out <- echoCall.PrettyPrint()
			out <- echoCall
			close(out)
			return out, nil
		}
		out <- "final answer"
		close(out)
		return out, nil
	}

	var printed strings.Builder
	q := &Querier[*MockQuerier]{
		out:       &printed,
		termWidth: 80,
		Model:     model,
	}
	session := &QuerySession{Chat: pub_models.Chat{Messages: []pub_models.Message{{Role: "user", Content: "hello"}}}}
	runner := sessionRunner[*MockQuerier]{
		querier:      q,
		recorder:     &recordingCallUsageRecorder{},
		finalizer:    sessionFinalizer[*MockQuerier]{querier: q},
		toolExecutor: toolExecutor[*MockQuerier]{querier: q},
	}

	err := runner.Run(context.Background(), session)
	if err != nil {
		t.Fatalf("Run returned err: %v", err)
	}

	printedOutput := printed.String()
	normalizedOutput := stripANSIEscapes(printedOutput)
	if got := strings.Count(normalizedOutput, "assistant:\n  "+echoCall.PrettyPrint()); got != 1 {
		t.Fatalf("expected exactly one structured assistant tool-call render, got %d occurrences in output:\n%s", got, normalizedOutput)
	}
	if !strings.Contains(printedOutput, "\r") {
		t.Fatalf("expected terminal clear/control output to be emitted when removing streamed echo, got output:\n%s", printedOutput)
	}
	if session.FinalAssistantText != "final answer" {
		t.Fatalf("expected final assistant text from follow-up step, got %q", session.FinalAssistantText)
	}
}

func Test_toolExecutor_FinalizeAssistantTextBeforeToolCall_PreservesAssistantProse(t *testing.T) {
	call := pub_models.Call{
		ID:   "call-1",
		Name: "pwd",
	}
	var printed strings.Builder
	q := &Querier[*MockQuerier]{
		out:       &printed,
		termWidth: 80,
	}
	session := &QuerySession{}
	session.AppendPendingText("I will check that for you.")

	err := toolExecutor[*MockQuerier]{querier: q}.finalizeAssistantTextBeforeToolCall(session, call)
	if err != nil {
		t.Fatalf("finalizeAssistantTextBeforeToolCall returned err: %v", err)
	}

	if got := session.PendingTextString(); got != "" {
		t.Fatalf("expected pending text to be cleared, got %q", got)
	}
	if session.FinalAssistantText != "I will check that for you." {
		t.Fatalf("expected prose to be preserved as final assistant text, got %q", session.FinalAssistantText)
	}

	normalizedOutput := stripANSIEscapes(printed.String())
	if !strings.Contains(normalizedOutput, "I will check that for you.") {
		t.Fatalf("expected prose to be rendered during finalization, got output:\n%s", normalizedOutput)
	}
}

func Test_toolExecutor_FinalizeAssistantTextBeforeToolCall_DropsWhitespaceEquivalentEcho(t *testing.T) {
	call := pub_models.Call{
		ID:   "call-1",
		Name: "pwd",
	}
	echoed := "\n" + call.PrettyPrint() + "\n"
	var printed strings.Builder
	q := &Querier[*MockQuerier]{
		out:       &printed,
		termWidth: 80,
	}
	session := &QuerySession{}
	session.AppendPendingText(echoed)

	err := toolExecutor[*MockQuerier]{querier: q}.finalizeAssistantTextBeforeToolCall(session, call)
	if err != nil {
		t.Fatalf("finalizeAssistantTextBeforeToolCall returned err: %v", err)
	}

	if got := session.PendingTextString(); got != "" {
		t.Fatalf("expected pending text to be cleared, got %q", got)
	}
	if session.FinalAssistantText != "" {
		t.Fatalf("expected echoed tool-call text not to be finalized, got %q", session.FinalAssistantText)
	}
	if got := stripANSIEscapes(printed.String()); strings.Contains(got, call.PrettyPrint()) {
		t.Fatalf("expected echoed tool-call text not to be post-processed, got output:\n%s", got)
	}
}

func Test_sessionRunner_Run_RateLimitRetryIsIterative(t *testing.T) {
	model := &MockQuerier{}
	callCount := 0
	rateLimitReset := time.Now().Add(-11 * time.Second)
	model.streamFn = func(_ context.Context, _ pub_models.Chat) (chan models.CompletionEvent, error) {
		callCount++
		if callCount == 1 {
			return nil, &models.ErrRateLimit{ResetAt: rateLimitReset}
		}
		model.usage = &pub_models.Usage{TotalTokens: 9}
		out := make(chan models.CompletionEvent, 1)
		out <- "after retry"
		close(out)
		return out, nil
	}

	q := &Querier[*MockQuerier]{out: &strings.Builder{}, Model: model}
	session := &QuerySession{Chat: pub_models.Chat{Messages: []pub_models.Message{{Role: "user", Content: "hi"}}}}
	recorder := &recordingCallUsageRecorder{}
	finalizer := &countingFinalizer{}
	runner := sessionRunner[*MockQuerier]{
		querier:      q,
		recorder:     recorder,
		finalizer:    finalizer,
		toolExecutor: toolExecutor[*MockQuerier]{querier: q},
	}

	err := runner.Run(context.Background(), session)
	if err != nil {
		t.Fatalf("Run returned err: %v", err)
	}
	if callCount != 2 {
		t.Fatalf("expected exactly 2 stream attempts, got %d", callCount)
	}
	if len(recorder.calls) != 1 {
		t.Fatalf("expected only completed retry step to be recorded, got %d", len(recorder.calls))
	}
	if session.FinalAssistantText != "after retry" {
		t.Fatalf("expected final assistant text after retry, got %q", session.FinalAssistantText)
	}
	if finalizer.count != 1 {
		t.Fatalf("expected finalizer once, got %d", finalizer.count)
	}
}

func Test_sessionRunner_Run_MultipleToolCallsDoNotReusePreviousPendingText(t *testing.T) {
	model := &MockQuerier{}
	callCount := 0
	model.streamFn = func(_ context.Context, _ pub_models.Chat) (chan models.CompletionEvent, error) {
		callCount++
		out := make(chan models.CompletionEvent, 2)
		switch callCount {
		case 1:
			model.usage = &pub_models.Usage{TotalTokens: 1}
			out <- "prefix "
			out <- pub_models.Call{ID: "call-1", Name: "pwd"}
		case 2:
			model.usage = &pub_models.Usage{TotalTokens: 2}
			out <- pub_models.Call{ID: "call-2", Name: "pwd"}
		default:
			model.usage = &pub_models.Usage{TotalTokens: 3}
			out <- "final"
		}
		close(out)
		return out, nil
	}

	q := &Querier[*MockQuerier]{
		out:   &strings.Builder{},
		Model: model,
	}
	session := &QuerySession{Chat: pub_models.Chat{Messages: []pub_models.Message{{Role: "user", Content: "hello"}}}}
	runner := sessionRunner[*MockQuerier]{
		querier:      q,
		recorder:     &recordingCallUsageRecorder{},
		finalizer:    &countingFinalizer{},
		toolExecutor: toolExecutor[*MockQuerier]{querier: q},
	}

	err := runner.Run(context.Background(), session)
	if err != nil {
		t.Fatalf("Run returned err: %v", err)
	}
	if session.FinalAssistantText != "final" {
		t.Fatalf("expected only final step text to remain, got %q", session.FinalAssistantText)
	}
	if len(session.Chat.Messages) != 5 {
		t.Fatalf("expected user + 2 tool call pairs before finalization appends final reply, got %d messages", len(session.Chat.Messages))
	}
}
