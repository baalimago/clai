package text

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/baalimago/clai/internal/models"
	"github.com/baalimago/clai/internal/tools/mcp"
	pub_models "github.com/baalimago/clai/pkg/text/models"
	"github.com/baalimago/go_away_boilerplate/pkg/dimensions"
)

func TestMcpLogModeFor(t *testing.T) {
	tests := []struct {
		name             string
		debug            bool
		outputIsTerminal bool
		raw              bool
		structured       bool
		rollingEnabled   bool
		want             mcpLogMode
	}{
		{name: "rolling terminal session", debug: false, outputIsTerminal: true, rollingEnabled: true, want: mcpLogRolling},
		{name: "rolling disabled falls back to direct print", debug: false, outputIsTerminal: true, rollingEnabled: false, want: mcpLogPlainDirect},
		{name: "debug keeps direct print", debug: true, outputIsTerminal: true, rollingEnabled: true, want: mcpLogPlainDirect},
		{name: "raw keeps only errors", debug: false, outputIsTerminal: true, raw: true, rollingEnabled: true, want: mcpLogRawStructured},
		{name: "structured keeps only errors", debug: false, outputIsTerminal: true, structured: true, rollingEnabled: true, want: mcpLogRawStructured},
		{name: "redirected output keeps only errors", debug: false, outputIsTerminal: false, rollingEnabled: true, want: mcpLogRawStructured},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := mcpLogModeFor(tt.debug, tt.outputIsTerminal, tt.raw, tt.structured, tt.rollingEnabled)
			if got != tt.want {
				t.Errorf("mcpLogModeFor(%v, %v, %v, %v, %v) = %v, want %v", tt.debug, tt.outputIsTerminal, tt.raw, tt.structured, tt.rollingEnabled, got, tt.want)
			}
		})
	}
}

func TestMcpLogSink_RollingModeQueuesLines(t *testing.T) {
	sink := newMcpLogSink(mcpLogRolling)
	sink.AppendServerLog("fs", "started")
	sink.AppendServerLog("fs", "error: boom")

	entries := sink.Drain()
	if len(entries) != 2 {
		t.Fatalf("Drain() = %d entries, want 2", len(entries))
	}
	if entries[0].isError {
		t.Errorf("entry %q classified as error, want normal", entries[0].line)
	}
	if !entries[1].isError {
		t.Errorf("entry %q classified as normal, want error", entries[1].line)
	}
	if rest := sink.Drain(); rest != nil {
		t.Errorf("second Drain() = %v, want nil", rest)
	}
}

func TestMcpLogSink_PlainDirectPrintsImmediately(t *testing.T) {
	var noticed []string
	sink := newMcpLogSink(mcpLogPlainDirect)
	sink.notice = func(server, line string) { noticed = append(noticed, server+":"+line) }

	sink.AppendServerLog("fs", "started")
	sink.AppendServerLog("fs", "error: boom")

	if len(noticed) != 2 {
		t.Fatalf("printed %d lines, want 2: %v", len(noticed), noticed)
	}
	if entries := sink.Drain(); entries != nil {
		t.Errorf("plain direct mode queued %d entries, want none", len(entries))
	}
}

func TestMcpLogSink_RawStructuredKeepsOnlyErrors(t *testing.T) {
	var errOut bytes.Buffer
	sink := newMcpLogSink(mcpLogRawStructured)
	sink.errOut = &errOut

	sink.AppendServerLog("fs", "started")
	sink.AppendServerLog("fs", "error: boom")

	if strings.Contains(errOut.String(), "started") {
		t.Errorf("raw/structured mode printed normal line: %q", errOut.String())
	}
	if !strings.Contains(errOut.String(), "error: boom") {
		t.Errorf("raw/structured mode dropped error line: %q", errOut.String())
	}
	if entries := sink.Drain(); entries != nil {
		t.Errorf("raw/structured mode queued %d entries, want none", len(entries))
	}
}

func TestMcpLogSink_OverflowDropsOldestNonErrorFirst(t *testing.T) {
	sink := newMcpLogSink(mcpLogRolling)
	sink.AppendServerLog("fs", "error: keep me")
	for range mcpLogQueueCap + 10 {
		sink.AppendServerLog("fs", "noise")
	}

	entries := sink.Drain()
	if len(entries) > mcpLogQueueCap {
		t.Fatalf("queue grew past cap: %d entries", len(entries))
	}
	if !entries[0].isError {
		t.Errorf("oldest error entry was dropped; first entry = %q", entries[0].line)
	}
}

func TestMcpLogSink_ServerExitedFlushesTail(t *testing.T) {
	sink := newMcpLogSink(mcpLogRolling)
	for i := range 5 {
		sink.AppendServerLog("fs", fmt.Sprintf("line %d", i))
	}
	sink.ServerExited("fs")

	entries := sink.Drain()
	last := entries[len(entries)-1]
	if !last.exit {
		t.Fatal("last entry is not the exit marker")
	}
	if !last.isError {
		t.Fatal("exit marker must classify as error for elevation")
	}
	if len(last.lines) != 5 {
		t.Fatalf("exit marker carries %d lines, want 5", len(last.lines))
	}
}

func TestMcpLogSink_ServerExitedTailCapped(t *testing.T) {
	sink := newMcpLogSink(mcpLogRolling)
	for i := range 15 {
		sink.AppendServerLog("fs", fmt.Sprintf("line %d", i))
	}
	sink.ServerExited("fs")

	entries := sink.Drain()
	last := entries[len(entries)-1]
	if len(last.lines) != mcpLogExitTailLines {
		t.Fatalf("exit marker carries %d lines, want %d", len(last.lines), mcpLogExitTailLines)
	}
	if last.lines[0] != "line 5" {
		t.Errorf("exit tail starts at %q, want %q", last.lines[0], "line 5")
	}
}

func TestMcpLogSink_ServerExitedFlushesTailRawStructured(t *testing.T) {
	var errOut bytes.Buffer
	sink := newMcpLogSink(mcpLogRawStructured)
	sink.errOut = &errOut

	sink.AppendServerLog("fs", "started")
	sink.AppendServerLog("fs", "worker stopped")
	if got := errOut.String(); strings.Contains(got, "started") || strings.Contains(got, "worker") {
		t.Fatalf("normal lines printed while the server runs: %q", got)
	}

	sink.ServerExited("fs")
	got := errOut.String()
	for _, want := range []string{"mcp_fs: started", "mcp_fs: worker stopped"} {
		if !strings.Contains(got, want) {
			t.Errorf("termination tail missing %q; got:\n%q", want, got)
		}
	}
	if entries := sink.Drain(); entries != nil {
		t.Errorf("raw/structured mode queued %d entries, want none", len(entries))
	}
}

func TestMcpLogSink_ServerExitedFlushesTailRawStructuredCapped(t *testing.T) {
	var errOut bytes.Buffer
	sink := newMcpLogSink(mcpLogRawStructured)
	sink.errOut = &errOut

	for i := range 15 {
		sink.AppendServerLog("fs", fmt.Sprintf("line %d", i))
	}
	sink.ServerExited("fs")

	got := errOut.String()
	if strings.Contains(got, "line 0") {
		t.Errorf("tail not capped; oldest line still present: %q", got)
	}
	if !strings.Contains(got, "line 14") {
		t.Errorf("newest tail line missing: %q", got)
	}
}

func TestMcpLogSink_NotifySignalsOnError(t *testing.T) {
	sink := newMcpLogSink(mcpLogRolling)
	notify := sink.Notify()

	// Normal lines stay quiet: they coalesce into the window at the next
	// session event instead of waking the loop.
	sink.AppendServerLog("fs", "started")
	select {
	case <-notify:
		t.Fatal("normal line must not signal the session loop")
	default:
	}

	// Error lines wake the loop immediately so they render live.
	sink.AppendServerLog("fs", "fatal: boom")
	select {
	case <-notify:
	default:
		t.Fatal("error line must signal the session loop")
	}

	// Exit reports wake the loop too: the elevated crash block is an error
	// diagnostic that must not wait for the next session event.
	sink.ServerExited("fs")
	select {
	case <-notify:
	default:
		t.Fatal("server exit must signal the session loop")
	}
}

// lockedBuffer is a concurrency-safe writer used when the session loop renders
// while the test goroutine polls the output.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// Test_sessionRunner_McpErrorRendersLiveWhileModelStreams pins R1-02: an MCP
// error written while the model stream is blocked must wake the serialized
// session loop and render elevated before the stream ends.
func Test_sessionRunner_McpErrorRendersLiveWhileModelStreams(t *testing.T) {
	model := &MockQuerier{}
	stream := make(chan models.CompletionEvent)
	streamReady := make(chan struct{})
	model.streamFn = func(_ context.Context, _ pub_models.Chat) (chan models.CompletionEvent, error) {
		close(streamReady)
		return stream, nil
	}

	var out lockedBuffer
	q := &Querier[*MockQuerier]{
		out:   &out,
		Model: model,
		dims:  dimensions.Dimensions{Width: 80, Height: 24},
	}
	q.mcpSink = newMcpLogSink(mcpLogRolling)
	session := &QuerySession{Chat: pub_models.Chat{ID: "chat-live-mcp"}}
	runner := sessionRunner[*MockQuerier]{
		querier:      q,
		finalizer:    &countingFinalizer{},
		toolExecutor: toolExecutor[*MockQuerier]{querier: q},
	}
	done := make(chan error, 1)
	go func() { done <- runner.Run(context.Background(), session) }()

	// The loop is now blocked waiting for stream events. Inject the MCP error
	// and expect the elevated block while the stream is still open.
	<-streamReady
	q.mcpSink.AppendServerLog("fs", "fatal: boom")

	deadline := time.Now().Add(5 * time.Second)
	for {
		if strings.Contains(out.String(), "✗ fatal: boom") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("elevated mcp error never rendered while stream blocked; got:\n%s", out.String())
		}
		time.Sleep(5 * time.Millisecond)
	}

	// End the stream: the error already rendered, so Run must finish cleanly.
	close(stream)
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run returned err: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not finish after the stream closed")
	}
}

// Test_McpSink_RawStructuredFlushesCrashTailFromServer pins R1-01 end to end:
// the test server writes a keyword-free tail and exits while raw/structured
// output is active; the termination tail must appear on stderr.
func Test_McpSink_RawStructuredFlushesCrashTailFromServer(t *testing.T) {
	var errOut lockedBuffer
	sink := newMcpLogSink(mcpLogRawStructured)
	sink.errOut = &errOut
	srv := pub_models.McpServer{
		Command: "go",
		Args:    []string{"run", "../tools/mcp/testserver"},
		Env:     map[string]string{"TEST_SERVER_CRASH_TAIL": "1"},
	}
	in, out, err := mcp.Client(t.Context(), srv, sink)
	if err != nil {
		t.Fatalf("client: %v", err)
	}
	_ = in
	_ = out

	deadline := time.Now().Add(5 * time.Second)
	for {
		if got := errOut.String(); strings.Contains(got, "worker stopped") && strings.Contains(got, "signal received") {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("crash tail never flushed to stderr; got:\n%q", errOut.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestMcpLogSink_ConcurrentAppendAndDrain(t *testing.T) {
	sink := newMcpLogSink(mcpLogRolling)
	var wg sync.WaitGroup
	for i := range 4 {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			for j := range 50 {
				sink.AppendServerLog(fmt.Sprintf("s%d", n), fmt.Sprintf("line %d", j))
			}
		}(i)
	}
	wg.Go(func() {
		for range 50 {
			sink.Drain()
		}
	})
	wg.Wait()
	// The remaining entries must drain without loss or corruption.
	_ = sink.Drain()
}

func Test_drainMcpLogs_WindowAndElevation(t *testing.T) {
	var out strings.Builder
	q := &Querier[*MockQuerier]{
		out:  &out,
		dims: dimensions.Dimensions{Width: 80, Height: 24},
	}
	q.mcpSink = newMcpLogSink(mcpLogRolling)
	q.mcpSink.AppendServerLog("filesystem", "server started")
	q.mcpSink.AppendServerLog("filesystem", "error: boom")

	if err := q.drainMcpLogs(); err != nil {
		t.Fatalf("drainMcpLogs: %v", err)
	}
	got := out.String()
	for _, want := range []string{"▸ mcp.filesystem log", "server started", "✗ error: boom"} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q; got:\n%s", want, got)
		}
	}
}

func Test_drainMcpLogs_ElevationClearsWindowRegion(t *testing.T) {
	var out strings.Builder
	q := &Querier[*MockQuerier]{
		out:  &out,
		dims: dimensions.Dimensions{Width: 80, Height: 24},
	}
	q.mcpSink = newMcpLogSink(mcpLogRolling)
	q.mcpSink.AppendServerLog("fs", "started")
	if err := q.drainMcpLogs(); err != nil {
		t.Fatalf("first drain: %v", err)
	}
	out.Reset()

	q.mcpSink.AppendServerLog("fs", "fatal: boom")
	if err := q.drainMcpLogs(); err != nil {
		t.Fatalf("second drain: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "\x1b[") {
		t.Errorf("elevation must clear the window region with ANSI sequences, got:\n%q", got)
	}
	if !strings.Contains(got, "✗ fatal: boom") {
		t.Errorf("elevated error block missing; got:\n%s", got)
	}
}

func Test_drainMcpLogs_ExitFlushElevates(t *testing.T) {
	var out strings.Builder
	q := &Querier[*MockQuerier]{
		out:  &out,
		dims: dimensions.Dimensions{Width: 80, Height: 24},
	}
	q.mcpSink = newMcpLogSink(mcpLogRolling)
	q.mcpSink.AppendServerLog("fs", "line one")
	q.mcpSink.AppendServerLog("fs", "line two")
	q.mcpSink.ServerExited("fs")

	if err := q.drainMcpLogs(); err != nil {
		t.Fatalf("drainMcpLogs: %v", err)
	}
	got := out.String()
	// The tail lines appear twice: once in the rolling window block and once in
	// the elevated block below it.
	if count := strings.Count(got, "line one"); count != 2 {
		t.Errorf("line one appears %d times, want 2 (window + elevated); got:\n%s", count, got)
	}
	if count := strings.Count(got, "line two"); count != 2 {
		t.Errorf("line two appears %d times, want 2 (window + elevated); got:\n%s", count, got)
	}
}
