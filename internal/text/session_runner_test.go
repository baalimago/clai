package text

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/baalimago/clai/internal/models"
	"github.com/baalimago/clai/internal/utils"
	pub_models "github.com/baalimago/clai/pkg/text/models"
	"github.com/baalimago/go_away_boilerplate/pkg/dimensions"
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

func withEmptyClaiConfigDir(t *testing.T) string {
	t.Helper()
	confDir := filepath.Join(t.TempDir(), ".clai")
	t.Setenv("CLAI_CONFIG_DIR", confDir)
	return confDir
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

func (f *countingFinalizer) Finalize(_ context.Context, session *QuerySession) {
	f.count++
	f.last = session
	if session == nil || session.Finalized {
		return
	}
	session.Finalized = true
}

func Test_sessionRunner_Run_OversizedFirstQueryNoTokenPrecheck(t *testing.T) {
	// Sunset contract (worklog 2026-08-04-token-stoploss, Phase 1): an
	// oversized first query must be sent to the model as-is — no
	// token-length pre-check, no interactive y/N prompt, no stdin read.
	model := &MockQuerier{}
	var receivedChat pub_models.Chat
	model.streamFn = func(_ context.Context, chat pub_models.Chat) (chan models.CompletionEvent, error) {
		receivedChat = chat
		out := make(chan models.CompletionEvent, 2)
		out <- "understood"
		close(out)
		return out, nil
	}

	oversizedPrompt := strings.Repeat("word ", 50000)
	q := &Querier[*MockQuerier]{
		out:   &strings.Builder{},
		Model: model,
	}
	session := &QuerySession{Chat: pub_models.Chat{
		ID: "chat-oversized",
		Messages: []pub_models.Message{
			{Role: "user", Content: oversizedPrompt},
		},
	}}
	runner := sessionRunner[*MockQuerier]{
		querier:      q,
		finalizer:    &countingFinalizer{},
		toolExecutor: toolExecutor[*MockQuerier]{querier: q},
	}

	if err := runner.Run(context.Background(), session); err != nil {
		t.Fatalf("Run returned err: %v", err)
	}
	if session.FinalAssistantText != "understood" {
		t.Fatalf("expected final assistant text %q, got %q", "understood", session.FinalAssistantText)
	}
	if len(receivedChat.Messages) != 1 || receivedChat.Messages[0].Content != oversizedPrompt {
		t.Fatal("expected oversized first query to reach the model as-is")
	}
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

func Test_sessionRunner_Run_ToolCallPropagatesReasoningContent(t *testing.T) {
	model := &MockQuerier{}
	callCount := 0
	model.streamFn = func(_ context.Context, chat pub_models.Chat) (chan models.CompletionEvent, error) {
		callCount++
		out := make(chan models.CompletionEvent, 1)
		if callCount == 1 {
			out <- pub_models.Call{ID: "call-1", Name: "pwd", ReasoningContent: "Need cwd."}
			close(out)
			return out, nil
		}
		if len(chat.Messages) < 2 {
			t.Fatalf("expected assistant tool-call message in follow-up chat, got %d messages", len(chat.Messages))
		}
		assistantToolCall := chat.Messages[1]
		if assistantToolCall.Role != "assistant" {
			t.Fatalf("expected assistant tool-call message, got role %q", assistantToolCall.Role)
		}
		if assistantToolCall.ReasoningContent != "Need cwd." {
			t.Fatalf("expected reasoning content passed back, got %q", assistantToolCall.ReasoningContent)
		}
		out <- "done"
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
	if callCount != 2 {
		t.Fatalf("expected 2 calls, got %d", callCount)
	}
}

func Test_sessionRunner_Run_FinalReplyReasoningIsPersistedSeparatelyFromContent(t *testing.T) {
	model := &MockQuerier{}
	model.streamFn = func(_ context.Context, _ pub_models.Chat) (chan models.CompletionEvent, error) {
		out := make(chan models.CompletionEvent, 3)
		out <- models.ReasoningEvent{Content: "Need to inspect."}
		out <- "done"
		close(out)
		return out, nil
	}

	q := &Querier[*MockQuerier]{
		out:   &strings.Builder{},
		Model: model,
	}
	session := &QuerySession{Chat: pub_models.Chat{Messages: []pub_models.Message{{Role: "user", Content: "hello"}}}}
	finalizer := sessionFinalizer[*MockQuerier]{querier: q}
	runner := sessionRunner[*MockQuerier]{
		querier:      q,
		recorder:     &recordingCallUsageRecorder{},
		finalizer:    finalizer,
		toolExecutor: toolExecutor[*MockQuerier]{querier: q},
	}

	err := runner.Run(context.Background(), session)
	if err != nil {
		t.Fatalf("Run returned err: %v", err)
	}
	if got := session.FinalAssistantText; got != "done" {
		t.Fatalf("expected final assistant text without display-only reasoning, got %q", got)
	}
	if len(session.Chat.Messages) != 2 {
		t.Fatalf("expected user + finalized assistant-ish message, got %d messages", len(session.Chat.Messages))
	}
	finalMsg := session.Chat.Messages[1]
	if finalMsg.Role != "assistant" {
		t.Fatalf("expected finalized message role assistant, got %q", finalMsg.Role)
	}
	if finalMsg.ReasoningContent != "Need to inspect." {
		t.Fatalf("expected reasoning content to be preserved separately, got %q", finalMsg.ReasoningContent)
	}
	if strings.Contains(finalMsg.Content, "[thinking]") {
		t.Fatalf("expected persisted assistant content to omit wrapped thinking markup, got %q", finalMsg.Content)
	}
}

func Test_sessionRunner_Run_ActivityViewportCombinesReasoningAndToolActivity(t *testing.T) {
	model := &MockQuerier{}
	callCount := 0
	model.streamFn = func(_ context.Context, _ pub_models.Chat) (chan models.CompletionEvent, error) {
		callCount++
		out := make(chan models.CompletionEvent, 3)
		if callCount == 1 {
			out <- models.ReasoningEvent{Content: "inspect \x1b[31mdirectory\x1b[0m"}
			out <- pub_models.Call{ID: "call-1", Name: "missing_tool"}
		} else {
			out <- "done"
		}
		close(out)
		return out, nil
	}

	var printed strings.Builder
	q := &Querier[*MockQuerier]{out: &printed, dims: dimensions.Dimensions{Width: 80, Height: 24}, Model: model}
	session := &QuerySession{Chat: pub_models.Chat{Messages: []pub_models.Message{{Role: "user", Content: "hello"}}}}
	runner := sessionRunner[*MockQuerier]{
		querier:      q,
		recorder:     &recordingCallUsageRecorder{},
		finalizer:    &countingFinalizer{},
		toolExecutor: toolExecutor[*MockQuerier]{querier: q},
	}

	if err := runner.Run(context.Background(), session); err != nil {
		t.Fatalf("Run returned err: %v", err)
	}
	output := stripANSIEscapes(printed.String())
	thinkingAt := strings.Index(output, "∴ thinking\n  inspect directory")
	toolAt := strings.Index(output, "▸ missing_tool")
	if thinkingAt == -1 || toolAt == -1 || thinkingAt > toolAt {
		t.Fatalf("expected viewport before tool activity, got:\n%s", output)
	}
	if strings.Contains(output, "assistant: [thinking]") || strings.Contains(output, "[and ") {
		t.Fatalf("reasoning must not be re-rendered as shortened assistant text, got:\n%s", output)
	}
	if session.FinalReasoningText != "" {
		t.Fatalf("tool-step reasoning must not leak into final reply reasoning, got %q", session.FinalReasoningText)
	}
	if got := session.Chat.Messages[1].ReasoningContent; got != "inspect \x1b[31mdirectory\x1b[0m" {
		t.Fatalf("expected complete reasoning on persisted tool call, got %q", got)
	}
}

func Test_sessionRunner_Run_DisabledRollingOutputStreamsPlainThinkingAndPrintsNewToolBlock(t *testing.T) {
	confDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(confDir, "theme.json"), []byte(`{"notificationBell":true,"rollingOutput":{"enabled":false}}`), 0o644); err != nil {
		t.Fatalf("WriteFile(theme.json): %v", err)
	}
	if err := utils.LoadTheme(confDir); err != nil {
		t.Fatalf("LoadTheme(): %v", err)
	}
	resetThemeDir := t.TempDir()
	t.Cleanup(func() {
		if err := utils.LoadTheme(resetThemeDir); err != nil {
			t.Errorf("reset theme: %v", err)
		}
	})

	model := &MockQuerier{}
	callCount := 0
	model.streamFn = func(_ context.Context, _ pub_models.Chat) (chan models.CompletionEvent, error) {
		callCount++
		out := make(chan models.CompletionEvent, 3)
		if callCount == 1 {
			out <- models.ReasoningEvent{Content: "inspect"}
			out <- pub_models.Call{ID: "call-1", Name: "missing_tool"}
		} else {
			out <- "done"
		}
		close(out)
		return out, nil
	}

	var printed strings.Builder
	q := &Querier[*MockQuerier]{out: &printed, dims: dimensions.Dimensions{Width: 80, Height: 24}, Model: model}
	session := &QuerySession{Chat: pub_models.Chat{Messages: []pub_models.Message{{Role: "user", Content: "hello"}}}}
	runner := sessionRunner[*MockQuerier]{
		querier:      q,
		recorder:     &recordingCallUsageRecorder{},
		finalizer:    &countingFinalizer{},
		toolExecutor: toolExecutor[*MockQuerier]{querier: q},
	}

	if err := runner.Run(context.Background(), session); err != nil {
		t.Fatalf("Run returned err: %v", err)
	}
	output := stripANSIEscapes(printed.String())
	for _, want := range []string{"[thinking]inspect", "[/thinking]", "▸ missing_tool", "done"} {
		if !strings.Contains(output, want) {
			t.Fatalf("expected %q in non-rolling output:\n%s", want, output)
		}
	}
	for _, forbidden := range []string{"∴ thinking", "assistant: [thinking]", "\x1b[2A"} {
		if strings.Contains(output, forbidden) {
			t.Fatalf("non-rolling output contains %q:\n%s", forbidden, output)
		}
	}
}

func Test_sessionRunner_Run_AssistantProseStaysInsideRollingWindow(t *testing.T) {
	t.Setenv("NO_COLOR", "true")
	confDir := withEmptyClaiConfigDir(t)
	if err := utils.LoadTheme(confDir); err != nil {
		t.Fatalf("LoadTheme(): %v", err)
	}
	model := &MockQuerier{}
	callCount := 0
	model.streamFn = func(_ context.Context, _ pub_models.Chat) (chan models.CompletionEvent, error) {
		callCount++
		out := make(chan models.CompletionEvent, 4)
		if callCount == 1 {
			out <- models.ReasoningEvent{Content: "inspect"}
			out <- "I will run a tool."
			out <- pub_models.Call{ID: "call-1", Name: "missing_tool"}
		} else {
			out <- "done"
		}
		close(out)
		return out, nil
	}

	var printed strings.Builder
	q := &Querier[*MockQuerier]{out: &printed, dims: dimensions.Dimensions{Width: 80, Height: 24}, Model: model}
	session := &QuerySession{Chat: pub_models.Chat{Messages: []pub_models.Message{{Role: "user", Content: "hello"}}}}
	runner := sessionRunner[*MockQuerier]{
		querier:      q,
		recorder:     &recordingCallUsageRecorder{},
		finalizer:    &countingFinalizer{},
		toolExecutor: toolExecutor[*MockQuerier]{querier: q},
	}

	if err := runner.Run(context.Background(), session); err != nil {
		t.Fatalf("Run returned err: %v", err)
	}
	output := stripANSIEscapes(printed.String())
	thinkingAt := strings.Index(output, "∴ thinking\n  inspect")
	proseAt := strings.Index(output, "assistant\n  I will run a tool.")
	toolAt := strings.Index(output, "▸ missing_tool")
	if thinkingAt == -1 || proseAt == -1 || toolAt == -1 {
		t.Fatalf("expected thinking, assistant prose and tool activity inside one window, got:\n%s", output)
	}
	if !(thinkingAt < proseAt && proseAt < toolAt) {
		t.Fatalf("expected order thinking < assistant prose < tool activity, got:\n%s", output)
	}
	if strings.Contains(output, "assistant: I will run a tool.") {
		t.Fatalf("assistant prose must not be re-printed outside the window, got:\n%s", output)
	}
	if session.FinalAssistantText != "done" {
		t.Fatalf("expected final assistant text, got %q", session.FinalAssistantText)
	}
}

func Test_sessionRunner_Run_ProseStreamedBeforeFirstActivityMovesIntoWindow(t *testing.T) {
	t.Setenv("NO_COLOR", "true")
	confDir := withEmptyClaiConfigDir(t)
	if err := utils.LoadTheme(confDir); err != nil {
		t.Fatalf("LoadTheme(): %v", err)
	}
	model := &MockQuerier{}
	callCount := 0
	model.streamFn = func(_ context.Context, _ pub_models.Chat) (chan models.CompletionEvent, error) {
		callCount++
		out := make(chan models.CompletionEvent, 3)
		if callCount == 1 {
			out <- "Let me check."
			out <- pub_models.Call{ID: "call-1", Name: "missing_tool"}
		} else {
			out <- "done"
		}
		close(out)
		return out, nil
	}

	var printed strings.Builder
	q := &Querier[*MockQuerier]{out: &printed, dims: dimensions.Dimensions{Width: 80, Height: 24}, Model: model}
	session := &QuerySession{Chat: pub_models.Chat{Messages: []pub_models.Message{{Role: "user", Content: "hello"}}}}
	runner := sessionRunner[*MockQuerier]{
		querier:      q,
		recorder:     &recordingCallUsageRecorder{},
		finalizer:    &countingFinalizer{},
		toolExecutor: toolExecutor[*MockQuerier]{querier: q},
	}

	if err := runner.Run(context.Background(), session); err != nil {
		t.Fatalf("Run returned err: %v", err)
	}
	output := stripANSIEscapes(printed.String())
	proseAt := strings.Index(output, "assistant\n  Let me check.")
	toolAt := strings.Index(output, "▸ missing_tool")
	if proseAt == -1 || toolAt == -1 || proseAt > toolAt {
		t.Fatalf("expected prose inside the window before tool activity, got:\n%s", output)
	}
	if strings.Contains(output, "assistant: Let me check.") {
		t.Fatalf("prose must not be re-printed outside the window, got:\n%s", output)
	}
}

func Test_sessionRunner_Run_FinalAnswerPrintsBelowRollingWindow(t *testing.T) {
	t.Setenv("NO_COLOR", "true")
	confDir := withEmptyClaiConfigDir(t)
	if err := utils.LoadTheme(confDir); err != nil {
		t.Fatalf("LoadTheme(): %v", err)
	}
	model := &MockQuerier{}
	callCount := 0
	model.streamFn = func(_ context.Context, _ pub_models.Chat) (chan models.CompletionEvent, error) {
		callCount++
		out := make(chan models.CompletionEvent, 3)
		if callCount == 1 {
			out <- models.ReasoningEvent{Content: "inspect"}
			out <- pub_models.Call{ID: "call-1", Name: "missing_tool"}
		} else {
			out <- models.ReasoningEvent{Content: "verify"}
			out <- "The answer is done."
		}
		close(out)
		return out, nil
	}

	var printed strings.Builder
	q := &Querier[*MockQuerier]{out: &printed, dims: dimensions.Dimensions{Width: 80, Height: 24}, Model: model}
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
	raw := printed.String()
	// The answer block is the trailing two rows of the window. The pop is a
	// pure shrink: the unchanged window above stays on screen and the pop
	// clears only the removed rows, immediately followed by the answer print
	// (one terminal transition, Option A).
	popSeq := "\x1b[2A\r\x1b[J"
	popAt := strings.LastIndex(raw, popSeq)
	if popAt == -1 {
		t.Fatalf("expected the pop to clear the two answer rows, got:\n%q", raw)
	}
	if windowAt := strings.Index(stripANSIEscapes(raw[:popAt]), "∴ thinking"); windowAt == -1 {
		t.Fatalf("expected the window to stay on screen above the final answer, got:\n%s", stripANSIEscapes(raw[:popAt]))
	}
	if got := stripANSIEscapes(raw[popAt+len(popSeq):]); got != "assistant: The answer is done.\n" {
		t.Fatalf("expected only the final answer right after the pop redraw, got %q", got)
	}
	if session.FinalAssistantText != "The answer is done." {
		t.Fatalf("expected final assistant text, got %q", session.FinalAssistantText)
	}
}

func Test_sessionRunner_Run_FinalAnswerStaysInWindowUntilFinalizer(t *testing.T) {
	// The atomic pop (Option A) removes the answer block from the viewport
	// state at stream end but defers the redraw to postProcessOutput, which
	// runs right before the answer prints below. A finalizer that never prints
	// therefore leaves the answer inside the window in the raw output.
	t.Setenv("NO_COLOR", "true")
	confDir := withEmptyClaiConfigDir(t)
	if err := utils.LoadTheme(confDir); err != nil {
		t.Fatalf("LoadTheme(): %v", err)
	}
	model := &MockQuerier{}
	model.streamFn = func(_ context.Context, _ pub_models.Chat) (chan models.CompletionEvent, error) {
		out := make(chan models.CompletionEvent, 3)
		out <- models.ReasoningEvent{Content: "inspect"}
		out <- "The answer is done."
		close(out)
		return out, nil
	}

	var printed strings.Builder
	q := &Querier[*MockQuerier]{out: &printed, dims: dimensions.Dimensions{Width: 80, Height: 24}, Model: model}
	session := &QuerySession{Chat: pub_models.Chat{Messages: []pub_models.Message{{Role: "user", Content: "hello"}}}}
	runner := sessionRunner[*MockQuerier]{
		querier:      q,
		recorder:     &recordingCallUsageRecorder{},
		finalizer:    &countingFinalizer{},
		toolExecutor: toolExecutor[*MockQuerier]{querier: q},
	}

	if err := runner.Run(context.Background(), session); err != nil {
		t.Fatalf("Run returned err: %v", err)
	}
	if !q.finalAnswerPopPending {
		t.Fatal("expected the final-answer pop to stay pending until the finalizer prints")
	}
	output := stripANSIEscapes(printed.String())
	if !strings.Contains(output, "assistant\n  The answer is done.") {
		t.Fatalf("expected the answer to remain inside the window until the finalizer, got:\n%s", output)
	}
	if strings.Contains(output, "assistant: The answer is done.") {
		t.Fatalf("answer must not be re-printed outside the window, got:\n%s", output)
	}
}

func Test_sessionRunner_Run_RedirectedOutputUsesRawRepresentation(t *testing.T) {
	model := &MockQuerier{}
	callCount := 0
	model.streamFn = func(_ context.Context, _ pub_models.Chat) (chan models.CompletionEvent, error) {
		callCount++
		out := make(chan models.CompletionEvent, 3)
		if callCount == 1 {
			out <- models.ReasoningEvent{Content: "inspect"}
			out <- pub_models.Call{ID: "call-1", Name: "missing_tool"}
		} else {
			out <- "done"
		}
		close(out)
		return out, nil
	}

	var printed strings.Builder
	q := &Querier[*MockQuerier]{
		out:              &printed,
		outputModeKnown:  true,
		outputIsTerminal: false,
		Model:            model,
	}
	session := &QuerySession{Chat: pub_models.Chat{Messages: []pub_models.Message{{Role: "user", Content: "hello"}}}}
	runner := sessionRunner[*MockQuerier]{
		querier:      q,
		recorder:     &recordingCallUsageRecorder{},
		finalizer:    &countingFinalizer{},
		toolExecutor: toolExecutor[*MockQuerier]{querier: q},
	}

	if err := runner.Run(context.Background(), session); err != nil {
		t.Fatalf("Run returned err: %v", err)
	}
	output := printed.String()
	for _, want := range []string{"[thinking]inspect", "[/thinking]", "Call: 'missing_tool'", "ERROR: unknown tool call: missing_tool", "done"} {
		if !strings.Contains(output, want) {
			t.Fatalf("expected raw output %q in %q", want, output)
		}
	}
	for _, forbidden := range []string{"\x1b", "▸ missing_tool", "\x1b["} {
		if strings.Contains(output, forbidden) {
			t.Fatalf("redirected output contains display-only data %q: %q", forbidden, output)
		}
	}
}

func Test_sessionRunner_Run_SecondToolCallDoesNotReusePreviousReasoningContent(t *testing.T) {
	t.Skip("reasoning reuse is asserted at request-building level for generic stream completers")
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

func Test_sessionRunner_Run_DoesNotDuplicateToolCallEchoBeforeToolActivity(t *testing.T) {
	confDir := withEmptyClaiConfigDir(t)
	if err := utils.LoadTheme(confDir); err != nil {
		t.Fatalf("LoadTheme(): %v", err)
	}

	model := &MockQuerier{}
	callCount := 0
	echoCall := pub_models.Call{
		ID:   "call-1",
		Name: "pwd",
		Inputs: &pub_models.Input{
			"path": ".",
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
		out:   &printed,
		dims:  dimensions.Dimensions{Width: 80, Height: 24},
		Model: model,
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

	normalizedOutput := stripANSIEscapes(printed.String())
	// The echoed tool-call text is streamed once and then cleared; it must
	// never be re-printed as an assistant message.
	if got := strings.Count(normalizedOutput, echoCall.PrettyPrint()); got != 1 {
		t.Fatalf("expected echoed tool call text exactly once, got %d occurrences in output:\n%s", got, normalizedOutput)
	}
	if strings.Contains(normalizedOutput, "assistant: "+echoCall.PrettyPrint()) {
		t.Fatalf("echoed tool call text must not be re-printed as assistant prose, got:\n%s", normalizedOutput)
	}
	if !strings.Contains(normalizedOutput, "▸ pwd  path=.") {
		t.Fatalf("expected paired tool activity in the window, got:\n%s", normalizedOutput)
	}
	if got := strings.Count(normalizedOutput, "assistant: final answer"); got != 1 {
		t.Fatalf("expected the final answer printed exactly once, got %d occurrences:\n%s", got, normalizedOutput)
	}
	if session.FinalAssistantText != "final answer" {
		t.Fatalf("expected final assistant text from follow-up step, got %q", session.FinalAssistantText)
	}
}

func Test_toolExecutor_FinalizeAssistantTextBeforeToolCall_PreservesAssistantProse(t *testing.T) {
	confDir := withEmptyClaiConfigDir(t)
	if err := utils.LoadTheme(confDir); err != nil {
		t.Fatalf("LoadTheme(): %v", err)
	}

	call := pub_models.Call{
		ID:   "call-1",
		Name: "pwd",
	}
	var printed strings.Builder
	q := &Querier[*MockQuerier]{
		out:  &printed,
		dims: dimensions.Dimensions{Width: 80, Height: 24},
	}
	session := &QuerySession{}
	session.AppendPendingText("I will check that for you.")

	err := toolExecutor[*MockQuerier]{querier: q}.finalizeAssistantTextBeforeToolCall(context.Background(), session, call)
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
	// The prose is moved into the rolling window; it must not be re-printed as
	// a standalone assistant message outside the window.
	if !strings.Contains(normalizedOutput, "assistant\n  I will check that for you.") {
		t.Fatalf("expected prose rendered inside the rolling window, got output:\n%s", normalizedOutput)
	}
	if strings.Contains(normalizedOutput, "assistant: I will check that for you.") {
		t.Fatalf("prose must not be re-printed outside the window, got output:\n%s", normalizedOutput)
	}
}

func Test_toolExecutor_FinalizeAssistantTextBeforeToolCall_DropsWhitespaceEquivalentEcho(t *testing.T) {
	confDir := withEmptyClaiConfigDir(t)
	if err := utils.LoadTheme(confDir); err != nil {
		t.Fatalf("LoadTheme(): %v", err)
	}

	call := pub_models.Call{
		ID:   "call-1",
		Name: "pwd",
	}
	echoed := "\n" + call.PrettyPrint() + "\n"
	var printed strings.Builder
	q := &Querier[*MockQuerier]{
		out:  &printed,
		dims: dimensions.Dimensions{Width: 80, Height: 24},
	}
	session := &QuerySession{}
	session.AppendPendingText(echoed)

	err := toolExecutor[*MockQuerier]{querier: q}.finalizeAssistantTextBeforeToolCall(context.Background(), session, call)
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

func Test_sessionRunner_Run_DrainsAndExecutesParallelToolCallsAsOneTurn(t *testing.T) {
	model := &MockQuerier{}
	callCount := 0
	model.streamFn = func(_ context.Context, chat pub_models.Chat) (chan models.CompletionEvent, error) {
		callCount++
		out := make(chan models.CompletionEvent)
		go func() {
			defer close(out)
			if callCount == 1 {
				model.usage = &pub_models.Usage{TotalTokens: 7}
				out <- pub_models.Call{ID: "call-1", Name: "missing_one"}
				out <- pub_models.Call{ID: "call-2", Name: "missing_two"}
				out <- models.StopEvent{}
				return
			}

			if len(chat.Messages) != 4 {
				t.Errorf("expected user + grouped assistant calls + two outputs, got %d messages", len(chat.Messages))
			} else if got := chat.Messages[1].ToolCalls; len(got) != 2 || got[0].ID != "call-1" || got[1].ID != "call-2" {
				t.Errorf("expected both calls on one assistant turn, got %#v", got)
			}
			model.usage = &pub_models.Usage{TotalTokens: 3}
			out <- "done"
			out <- models.StopEvent{}
		}()
		return out, nil
	}

	var printed strings.Builder
	q := &Querier[*MockQuerier]{out: &printed, dims: dimensions.Dimensions{Width: 80, Height: 24}, Model: model}
	session := &QuerySession{Chat: pub_models.Chat{Messages: []pub_models.Message{{Role: "user", Content: "hello"}}}}
	runner := sessionRunner[*MockQuerier]{
		querier:      q,
		recorder:     &recordingCallUsageRecorder{},
		finalizer:    &countingFinalizer{},
		toolExecutor: toolExecutor[*MockQuerier]{querier: q},
	}

	if err := runner.Run(context.Background(), session); err != nil {
		t.Fatalf("Run returned err: %v", err)
	}
	if callCount != 2 {
		t.Fatalf("expected one continuation after the parallel tool turn, got %d model calls", callCount)
	}
	if session.FinalAssistantText != "done" {
		t.Fatalf("expected final reply, got %q", session.FinalAssistantText)
	}
	output := stripANSIEscapes(printed.String())
	firstCall := strings.Index(output, "▸ missing_one")
	secondCall := strings.Index(output, "▸ missing_two")
	if firstCall == -1 || secondCall == -1 {
		t.Fatalf("expected both tool activity blocks, got:\n%s", output)
	}
	firstResult := strings.Index(output[firstCall:], "✗ ERROR:")
	if firstResult == -1 || firstCall+firstResult > secondCall {
		t.Fatalf("expected the first result before the second call, got:\n%s", output)
	}
}
