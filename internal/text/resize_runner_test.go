package text

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/baalimago/clai/internal/models"
	"github.com/baalimago/clai/internal/utils"
	pub_models "github.com/baalimago/clai/pkg/text/models"
	"github.com/baalimago/go_away_boilerplate/pkg/dimensions"
)

// withRollingTheme loads the default theme (rolling output enabled) and
// restores the previous theme after the test. Tests in this package mutate
// the process-global theme, so every phase-5 test pins its own theme.
func withRollingTheme(t *testing.T) {
	t.Helper()
	confDir := withEmptyClaiConfigDir(t)
	if err := utils.LoadTheme(confDir); err != nil {
		t.Fatalf("LoadTheme(): %v", err)
	}
	t.Cleanup(func() {
		if err := utils.LoadTheme(t.TempDir()); err != nil {
			t.Errorf("reset theme: %v", err)
		}
	})
}

// waitFor polls cond until it holds or the timeout expires. The polling
// interval keeps -race runs deterministic without busy-spinning.
func waitFor(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("condition not met within %v", timeout)
}

// syncBuffer is a mutex-guarded strings.Builder. The session loop renders into
// it from its own goroutine while the test observes the streaming output.
type syncBuffer struct {
	mu sync.Mutex
	b  strings.Builder
}

func (w *syncBuffer) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.b.Write(p)
}

func (w *syncBuffer) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.b.String()
}

// wideContent is a 100-column line that wraps to one row at width 80 and to
// three rows at width 40, so tests can tell the wrap width apart.
const wideContent = "012345678901234567890123456789012345678901234567890123456789012345678901234567890123456789"

// wrappedRow is the exact first body row of a wrapped wideContent block at the
// given viewport width, terminated by the row's newline. The terminating
// newline makes the marker unambiguous: at width 50 the row is 48 columns
// long, so a 38-column marker without the newline would match its prefix.
func wrappedRow(width int) string {
	return "  " + wideContent[:width-2] + "\n"
}

// Test_sessionRunner_Run_ResizeDuringStreamingRewrapsRollingOutput proves the
// acceptance criterion "a simulated SIGWINCH changes rolling output dimensions
// during streaming": the reasoning first renders at width 80, a resize event
// rewraps the retained block and redraws the window at width 40, and the
// trailing token renders at the new width.
func Test_sessionRunner_Run_ResizeDuringStreamingRewrapsRollingOutput(t *testing.T) {
	withRollingTheme(t)
	proceed := make(chan struct{})
	model := &MockQuerier{}
	model.streamFn = func(_ context.Context, _ pub_models.Chat) (chan models.CompletionEvent, error) {
		out := make(chan models.CompletionEvent, 4)
		go func() {
			out <- models.ReasoningEvent{Content: wideContent}
			<-proceed
			out <- "done"
			close(out)
		}()
		return out, nil
	}

	var printed syncBuffer
	resizeEvents := make(chan dimensions.Dimensions, 4)
	q := &Querier[*MockQuerier]{
		out:   &printed,
		dims:  dimensions.Dimensions{Width: 80, Height: 10},
		Model: model,
	}
	session := &QuerySession{Chat: pub_models.Chat{Messages: []pub_models.Message{{Role: "user", Content: "hello"}}}}
	runner := sessionRunner[*MockQuerier]{
		querier:      q,
		recorder:     &recordingCallUsageRecorder{},
		finalizer:    &countingFinalizer{},
		toolExecutor: toolExecutor[*MockQuerier]{querier: q},
		resizeEvents: resizeEvents,
	}

	done := make(chan error, 1)
	go func() { done <- runner.Run(context.Background(), session) }()

	row80 := wrappedRow(80)
	row40 := wrappedRow(40)
	waitFor(t, 2*time.Second, func() bool { return strings.Contains(printed.String(), row80) })

	resizeEvents <- dimensions.Dimensions{Width: 40, Height: 10}
	waitFor(t, 2*time.Second, func() bool {
		return strings.Contains(stripANSIEscapes(printed.String()), row40)
	})

	close(proceed)
	if err := <-done; err != nil {
		t.Fatalf("Run returned err: %v", err)
	}
	if q.dims != (dimensions.Dimensions{Width: 40, Height: 10}) {
		t.Fatalf("q.dims = %+v, want {40 10}", q.dims)
	}
	output := stripANSIEscapes(printed.String())
	if !strings.Contains(output, "done") {
		t.Fatalf("expected trailing token rendered after the resize, got:\n%s", output)
	}
}

// Test_sessionRunner_Run_ResizeBurstConvergesOnLatestDimensions proves resize
// bursts are safe and converge on the latest dimensions: three events arrive
// back to back, each redraw is serialized on the session loop, and the last
// event wins.
func Test_sessionRunner_Run_ResizeBurstConvergesOnLatestDimensions(t *testing.T) {
	withRollingTheme(t)
	proceed := make(chan struct{})
	model := &MockQuerier{}
	model.streamFn = func(_ context.Context, _ pub_models.Chat) (chan models.CompletionEvent, error) {
		out := make(chan models.CompletionEvent, 4)
		go func() {
			out <- models.ReasoningEvent{Content: wideContent}
			<-proceed
			out <- "done"
			close(out)
		}()
		return out, nil
	}

	var printed syncBuffer
	resizeEvents := make(chan dimensions.Dimensions, 4)
	q := &Querier[*MockQuerier]{
		out:   &printed,
		dims:  dimensions.Dimensions{Width: 80, Height: 10},
		Model: model,
	}
	session := &QuerySession{Chat: pub_models.Chat{Messages: []pub_models.Message{{Role: "user", Content: "hello"}}}}
	runner := sessionRunner[*MockQuerier]{
		querier:      q,
		recorder:     &recordingCallUsageRecorder{},
		finalizer:    &countingFinalizer{},
		toolExecutor: toolExecutor[*MockQuerier]{querier: q},
		resizeEvents: resizeEvents,
	}

	done := make(chan error, 1)
	go func() { done <- runner.Run(context.Background(), session) }()

	waitFor(t, 2*time.Second, func() bool { return strings.Contains(printed.String(), wrappedRow(80)) })

	resizeEvents <- dimensions.Dimensions{Width: 60, Height: 10}
	resizeEvents <- dimensions.Dimensions{Width: 50, Height: 10}
	resizeEvents <- dimensions.Dimensions{Width: 40, Height: 10}
	waitFor(t, 2*time.Second, func() bool {
		return strings.Contains(stripANSIEscapes(printed.String()), wrappedRow(40))
	})

	close(proceed)
	if err := <-done; err != nil {
		t.Fatalf("Run returned err: %v", err)
	}
	if q.dims != (dimensions.Dimensions{Width: 40, Height: 10}) {
		t.Fatalf("q.dims = %+v, want last event {40 10}", q.dims)
	}
}

// Test_sessionRunner_Run_ResizeBeforeViewportCreationUsesNewDimensions proves
// the R2-03 ordering: a resize arriving before the first reasoning event
// updates the session snapshot, and the lazily created viewport renders at the
// new width immediately — the 80-column row never appears.
func Test_sessionRunner_Run_ResizeBeforeViewportCreationUsesNewDimensions(t *testing.T) {
	withRollingTheme(t)
	start := make(chan struct{})
	proceed := make(chan struct{})
	model := &MockQuerier{}
	model.streamFn = func(_ context.Context, _ pub_models.Chat) (chan models.CompletionEvent, error) {
		out := make(chan models.CompletionEvent, 4)
		go func() {
			<-start
			out <- models.ReasoningEvent{Content: wideContent}
			<-proceed
			out <- "done"
			close(out)
		}()
		return out, nil
	}

	var printed syncBuffer
	resizeEvents := make(chan dimensions.Dimensions, 4)
	q := &Querier[*MockQuerier]{
		out:   &printed,
		dims:  dimensions.Dimensions{Width: 80, Height: 10},
		Model: model,
	}
	session := &QuerySession{Chat: pub_models.Chat{Messages: []pub_models.Message{{Role: "user", Content: "hello"}}}}
	runner := sessionRunner[*MockQuerier]{
		querier:      q,
		recorder:     &recordingCallUsageRecorder{},
		finalizer:    &countingFinalizer{},
		toolExecutor: toolExecutor[*MockQuerier]{querier: q},
		resizeEvents: resizeEvents,
	}

	done := make(chan error, 1)
	go func() { done <- runner.Run(context.Background(), session) }()

	// The stream emits nothing until the resize is consumed: the select's only
	// ready case is the resize event, so the ordering is deterministic.
	resizeEvents <- dimensions.Dimensions{Width: 40, Height: 10}
	close(start)
	waitFor(t, 2*time.Second, func() bool {
		return strings.Contains(stripANSIEscapes(printed.String()), wrappedRow(40))
	})

	close(proceed)
	if err := <-done; err != nil {
		t.Fatalf("Run returned err: %v", err)
	}
	if q.dims != (dimensions.Dimensions{Width: 40, Height: 10}) {
		t.Fatalf("q.dims = %+v, want {40 10}", q.dims)
	}
	output := stripANSIEscapes(printed.String())
	if strings.Contains(output, wrappedRow(80)) {
		t.Fatalf("first render must use the new width, got an 80-column row:\n%s", output)
	}
	if !strings.Contains(output, "done") {
		t.Fatalf("expected final answer after the resize, got:\n%s", output)
	}
}

// Test_sessionRunner_Run_ResizeKeepsToolTransitionAndFinalAnswerValid proves
// the final answer and tool transitions remain valid after a resize: the
// thinking/prose/tool order survives, the tool block renders, and the final
// answer streams at the new width.
func Test_sessionRunner_Run_ResizeKeepsToolTransitionAndFinalAnswerValid(t *testing.T) {
	withRollingTheme(t)
	step2Started := make(chan struct{})
	releaseStep2 := make(chan struct{})
	callCount := 0
	model := &MockQuerier{}
	model.streamFn = func(_ context.Context, _ pub_models.Chat) (chan models.CompletionEvent, error) {
		callCount++
		out := make(chan models.CompletionEvent, 8)
		if callCount == 1 {
			out <- models.ReasoningEvent{Content: wideContent}
			out <- "I will run a tool."
			out <- pub_models.Call{ID: "call-1", Name: "missing_tool"}
			close(out)
			return out, nil
		}
		close(step2Started)
		go func() {
			<-releaseStep2
			out <- "done"
			close(out)
		}()
		return out, nil
	}

	var printed syncBuffer
	resizeEvents := make(chan dimensions.Dimensions, 4)
	q := &Querier[*MockQuerier]{
		out:   &printed,
		dims:  dimensions.Dimensions{Width: 80, Height: 10},
		Model: model,
	}
	session := &QuerySession{Chat: pub_models.Chat{Messages: []pub_models.Message{{Role: "user", Content: "hello"}}}}
	runner := sessionRunner[*MockQuerier]{
		querier:      q,
		recorder:     &recordingCallUsageRecorder{},
		finalizer:    &countingFinalizer{},
		toolExecutor: toolExecutor[*MockQuerier]{querier: q},
		resizeEvents: resizeEvents,
	}

	done := make(chan error, 1)
	go func() { done <- runner.Run(context.Background(), session) }()

	waitFor(t, 2*time.Second, func() bool {
		select {
		case <-step2Started:
			return true
		default:
			return false
		}
	})
	resizeEvents <- dimensions.Dimensions{Width: 40, Height: 10}
	waitFor(t, 2*time.Second, func() bool {
		return strings.Contains(stripANSIEscapes(printed.String()), wrappedRow(40))
	})
	close(releaseStep2)

	if err := <-done; err != nil {
		t.Fatalf("Run returned err: %v", err)
	}
	if q.dims != (dimensions.Dimensions{Width: 40, Height: 10}) {
		t.Fatalf("q.dims = %+v, want {40 10}", q.dims)
	}
	if session.FinalAssistantText != "done" {
		t.Fatalf("final answer = %q, want %q", session.FinalAssistantText, "done")
	}
	output := stripANSIEscapes(printed.String())
	thinkingAt := strings.Index(output, "∴ thinking")
	proseAt := strings.Index(output, "assistant\n  I will run a tool.")
	toolAt := strings.Index(output, "▸ missing_tool")
	if thinkingAt == -1 || proseAt == -1 || toolAt == -1 || !(thinkingAt < proseAt && proseAt < toolAt) {
		t.Fatalf("expected thinking < assistant prose < tool after the resize, got:\n%s", output)
	}
	if !strings.Contains(output, "done") {
		t.Fatalf("expected final answer rendered, got:\n%s", output)
	}
}

// Test_sessionRunner_Run_InitialHeightBindsTerminalHeight proves R5-01 at the
// session boundary: the lazily created viewport's first render is bounded by
// min(cap, terminal height) even before any SIGWINCH. A 5-row terminal with
// the default 30-row cap renders a 5-row window, not a 30-row window, so the
// phase-4 acceptance criterion "effective height never exceeds terminal height
// or configured cap" holds from the very first frame. The dropped middle rows
// ("  line four" and earlier body rows) prove the window was compacted to the
// terminal height, exactly the scenario that failed before the fix.
func Test_sessionRunner_Run_InitialHeightBindsTerminalHeight(t *testing.T) {
	withRollingTheme(t)
	model := &MockQuerier{}
	model.streamFn = func(_ context.Context, _ pub_models.Chat) (chan models.CompletionEvent, error) {
		out := make(chan models.CompletionEvent, 4)
		out <- models.ReasoningEvent{Content: "line one\nline two\nline three\nline four\nline five\nline six\nline seven\nline eight"}
		out <- "done"
		close(out)
		return out, nil
	}

	var printed strings.Builder
	q := &Querier[*MockQuerier]{
		out:   &printed,
		dims:  dimensions.Dimensions{Width: 80, Height: 5},
		Model: model,
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
	output := stripANSIEscapes(printed.String())
	if !strings.Contains(output, "∴ thinking") {
		t.Fatalf("expected the reasoning window, got:\n%s", output)
	}
	if !strings.Contains(output, "  line eight") {
		t.Fatalf("expected the trailing reasoning rows in the window, got:\n%s", output)
	}
	if strings.Contains(output, "  line four") {
		t.Fatalf("first render must be bounded by the terminal height 5, got a taller window:\n%s", output)
	}
}

// Test_sessionRunner_Run_NoWatcherFallsBackToOneShotRead proves the watcher
// startup rejection path: a nil resizeEvents channel (non-rolling,
// non-terminal, or non-file session) keeps today's one-shot q.dims read and
// renders exactly as before, without starting partial cleanup.
func Test_sessionRunner_Run_NoWatcherFallsBackToOneShotRead(t *testing.T) {
	withRollingTheme(t)
	model := &MockQuerier{}
	model.streamFn = func(_ context.Context, _ pub_models.Chat) (chan models.CompletionEvent, error) {
		out := make(chan models.CompletionEvent, 4)
		out <- models.ReasoningEvent{Content: wideContent}
		out <- "done"
		close(out)
		return out, nil
	}

	var printed strings.Builder
	q := &Querier[*MockQuerier]{
		out:   &printed,
		dims:  dimensions.Dimensions{Width: 80, Height: 10},
		Model: model,
	}
	session := &QuerySession{Chat: pub_models.Chat{Messages: []pub_models.Message{{Role: "user", Content: "hello"}}}}
	runner := sessionRunner[*MockQuerier]{
		querier:      q,
		recorder:     &recordingCallUsageRecorder{},
		finalizer:    &countingFinalizer{},
		toolExecutor: toolExecutor[*MockQuerier]{querier: q},
		// resizeEvents intentionally nil: the session must fall back to the
		// one-shot snapshot.
	}

	if err := runner.Run(context.Background(), session); err != nil {
		t.Fatalf("Run returned err: %v", err)
	}
	if q.dims != (dimensions.Dimensions{Width: 80, Height: 10}) {
		t.Fatalf("q.dims = %+v, want unchanged {80 10}", q.dims)
	}
	output := stripANSIEscapes(printed.String())
	if !strings.Contains(output, wrappedRow(80)) {
		t.Fatalf("expected one-shot width-80 render, got:\n%s", output)
	}
	if !strings.Contains(output, "done") {
		t.Fatalf("expected final answer, got:\n%s", output)
	}
}

// Test_sessionRunner_Run_ResizeChannelClosedMidStreamKeepsStreaming proves the
// termination path where the watcher stops while the stream still runs: the
// closed channel nils out the resize case and the session finishes normally.
func Test_sessionRunner_Run_ResizeChannelClosedMidStreamKeepsStreaming(t *testing.T) {
	withRollingTheme(t)
	proceed := make(chan struct{})
	model := &MockQuerier{}
	model.streamFn = func(_ context.Context, _ pub_models.Chat) (chan models.CompletionEvent, error) {
		out := make(chan models.CompletionEvent, 4)
		go func() {
			out <- models.ReasoningEvent{Content: "inspect"}
			<-proceed
			out <- "done"
			close(out)
		}()
		return out, nil
	}

	var printed syncBuffer
	resizeEvents := make(chan dimensions.Dimensions, 1)
	q := &Querier[*MockQuerier]{
		out:   &printed,
		dims:  dimensions.Dimensions{Width: 80, Height: 10},
		Model: model,
	}
	session := &QuerySession{Chat: pub_models.Chat{Messages: []pub_models.Message{{Role: "user", Content: "hello"}}}}
	runner := sessionRunner[*MockQuerier]{
		querier:      q,
		recorder:     &recordingCallUsageRecorder{},
		finalizer:    &countingFinalizer{},
		toolExecutor: toolExecutor[*MockQuerier]{querier: q},
		resizeEvents: resizeEvents,
	}

	done := make(chan error, 1)
	go func() { done <- runner.Run(context.Background(), session) }()

	waitFor(t, 2*time.Second, func() bool { return strings.Contains(printed.String(), "∴ thinking") })
	close(resizeEvents)
	close(proceed)

	if err := <-done; err != nil {
		t.Fatalf("Run returned err: %v", err)
	}
	if q.dims != (dimensions.Dimensions{Width: 80, Height: 10}) {
		t.Fatalf("q.dims = %+v, want unchanged {80 10}", q.dims)
	}
	if !strings.Contains(stripANSIEscapes(printed.String()), "done") {
		t.Fatalf("expected the stream to finish after the watcher closed, got:\n%s", printed.String())
	}
}

// Test_sessionRunner_Run_StreamEndsWhileResizePendingNoLateRedraw proves the
// termination race: a notification that arrives after the stream ended is
// never applied and never redraws. The output after teardown stays unchanged.
func Test_sessionRunner_Run_StreamEndsWhileResizePendingNoLateRedraw(t *testing.T) {
	withRollingTheme(t)
	model := &MockQuerier{}
	model.streamFn = func(_ context.Context, _ pub_models.Chat) (chan models.CompletionEvent, error) {
		out := make(chan models.CompletionEvent, 4)
		out <- models.ReasoningEvent{Content: wideContent}
		out <- "done"
		close(out)
		return out, nil
	}

	var printed strings.Builder
	resizeEvents := make(chan dimensions.Dimensions, 4)
	q := &Querier[*MockQuerier]{
		out:   &printed,
		dims:  dimensions.Dimensions{Width: 80, Height: 10},
		Model: model,
	}
	session := &QuerySession{Chat: pub_models.Chat{Messages: []pub_models.Message{{Role: "user", Content: "hello"}}}}
	runner := sessionRunner[*MockQuerier]{
		querier:      q,
		recorder:     &recordingCallUsageRecorder{},
		finalizer:    &countingFinalizer{},
		toolExecutor: toolExecutor[*MockQuerier]{querier: q},
		resizeEvents: resizeEvents,
	}

	if err := runner.Run(context.Background(), session); err != nil {
		t.Fatalf("Run returned err: %v", err)
	}
	outputBefore := printed.String()

	// The resize arrives after teardown: nobody consumes it, and the viewport
	// must not redraw.
	resizeEvents <- dimensions.Dimensions{Width: 40, Height: 10}
	time.Sleep(50 * time.Millisecond)
	if got := printed.String(); got != outputBefore {
		t.Fatalf("late notification caused output after teardown:\nbefore:\n%s\nafter:\n%s", outputBefore, got)
	}
	if q.dims != (dimensions.Dimensions{Width: 80, Height: 10}) {
		t.Fatalf("q.dims = %+v, want unchanged {80 10}", q.dims)
	}
}

// Test_sessionRunner_Run_ContextCancellationStopsCleanly proves the
// cancellation path: the session loop returns promptly when the context is
// done, with the watcher channel open but idle.
func Test_sessionRunner_Run_ContextCancellationStopsCleanly(t *testing.T) {
	withRollingTheme(t)
	model := &MockQuerier{}
	model.streamFn = func(_ context.Context, _ pub_models.Chat) (chan models.CompletionEvent, error) {
		// A stream that never delivers: only context cancellation can end it.
		return make(chan models.CompletionEvent), nil
	}

	var printed syncBuffer
	resizeEvents := make(chan dimensions.Dimensions, 1)
	q := &Querier[*MockQuerier]{
		out:   &printed,
		dims:  dimensions.Dimensions{Width: 80, Height: 10},
		Model: model,
	}
	session := &QuerySession{Chat: pub_models.Chat{Messages: []pub_models.Message{{Role: "user", Content: "hello"}}}}
	runner := sessionRunner[*MockQuerier]{
		querier:      q,
		recorder:     &recordingCallUsageRecorder{},
		finalizer:    &countingFinalizer{},
		toolExecutor: toolExecutor[*MockQuerier]{querier: q},
		resizeEvents: resizeEvents,
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runner.Run(ctx, session) }()
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run returned err after cancellation: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after context cancellation")
	}
}

// failAfterWriter fails every write at or after the configured call number,
// so a test can force a mid-frame write error deterministically.
type failAfterWriter struct {
	mu     sync.Mutex
	writes int
	failOn int
	b      strings.Builder
}

func (w *failAfterWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.writes++
	if w.writes >= w.failOn {
		return 0, errors.New("boom")
	}
	return w.b.Write(p)
}

func (w *failAfterWriter) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.b.String()
}

// Test_sessionRunner_Run_ResizeRenderWriterFailureReturnsError proves the
// writer-failure path: a failed redraw after a resize returns the render error
// and the run stops without a goroutine leak.
func Test_sessionRunner_Run_ResizeRenderWriterFailureReturnsError(t *testing.T) {
	withRollingTheme(t)
	proceed := make(chan struct{})
	model := &MockQuerier{}
	model.streamFn = func(_ context.Context, _ pub_models.Chat) (chan models.CompletionEvent, error) {
		out := make(chan models.CompletionEvent, 4)
		go func() {
			out <- models.ReasoningEvent{Content: "inspect"}
			<-proceed
			out <- "done"
			close(out)
		}()
		return out, nil
	}

	// Write 1 is the reasoning render; write 2 is the resize redraw and fails.
	writer := &failAfterWriter{failOn: 2}
	resizeEvents := make(chan dimensions.Dimensions, 4)
	q := &Querier[*MockQuerier]{
		out:   writer,
		dims:  dimensions.Dimensions{Width: 80, Height: 10},
		Model: model,
	}
	session := &QuerySession{Chat: pub_models.Chat{Messages: []pub_models.Message{{Role: "user", Content: "hello"}}}}
	runner := sessionRunner[*MockQuerier]{
		querier:      q,
		recorder:     &recordingCallUsageRecorder{},
		finalizer:    &countingFinalizer{},
		toolExecutor: toolExecutor[*MockQuerier]{querier: q},
		resizeEvents: resizeEvents,
	}

	done := make(chan error, 1)
	go func() { done <- runner.Run(context.Background(), session) }()

	waitFor(t, 2*time.Second, func() bool { return strings.Contains(writer.String(), "∴ thinking") })
	resizeEvents <- dimensions.Dimensions{Width: 40, Height: 10}

	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "render activity viewport after resize") {
			t.Fatalf("Run error = %v, want render-after-resize error", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after the writer failure")
	}
}

// Test_Querier_startResizeWatcher covers the watcher gate and lifecycle at the
// query boundary: the watcher starts exactly when usesActivityViewport holds
// and the writer is an *os.File, and its stop function releases the
// registration and closes the event channel.
func Test_Querier_startResizeWatcher(t *testing.T) {
	withRollingTheme(t)

	t.Run("rejected for raw sessions", func(t *testing.T) {
		q := &Querier[*MockQuerier]{Raw: true, out: &strings.Builder{}}
		ch, stop := q.startResizeWatcher(context.Background())
		if ch != nil {
			t.Fatal("raw session must not start a watcher")
		}
		stop() // no-op stop must be callable
	})

	t.Run("rejected for structured output", func(t *testing.T) {
		q := &Querier[*MockQuerier]{structuredOutput: true, out: &strings.Builder{}}
		ch, stop := q.startResizeWatcher(context.Background())
		if ch != nil {
			t.Fatal("structured session must not start a watcher")
		}
		stop()
	})

	t.Run("rejected for non-file writers", func(t *testing.T) {
		q := &Querier[*MockQuerier]{out: &strings.Builder{}}
		ch, stop := q.startResizeWatcher(context.Background())
		if ch != nil {
			t.Fatal("non-file writer must not start a watcher")
		}
		stop()
	})

	t.Run("starts for a file writer and stops cleanly", func(t *testing.T) {
		file, err := os.CreateTemp(t.TempDir(), "session-out")
		if err != nil {
			t.Fatalf("CreateTemp: %v", err)
		}
		defer file.Close()
		q := &Querier[*MockQuerier]{out: file}
		ch, stop := q.startResizeWatcher(context.Background())
		if ch == nil {
			t.Fatal("terminal rolling session must start a watcher")
		}
		stop()
		select {
		case d, ok := <-ch:
			if ok {
				t.Fatalf("event channel still open after stop, got %v", d)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("event channel was not closed after stop")
		}
	})

	t.Run("stops cleanly after context cancellation", func(t *testing.T) {
		file, err := os.CreateTemp(t.TempDir(), "session-out")
		if err != nil {
			t.Fatalf("CreateTemp: %v", err)
		}
		defer file.Close()
		ctx, cancel := context.WithCancel(context.Background())
		q := &Querier[*MockQuerier]{out: file}
		ch, stop := q.startResizeWatcher(ctx)
		if ch == nil {
			t.Fatal("terminal rolling session must start a watcher")
		}
		cancel()
		stop() // must return promptly and release the registration
		select {
		case d, ok := <-ch:
			if ok {
				t.Fatalf("event channel still open after cancellation, got %v", d)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("event channel was not closed after cancellation")
		}
	})
}
