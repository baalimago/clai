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

// lastStartupFrame returns the content of the most recent full-region redraw:
// everything after the last clear sequence, or the whole output when no
// clear happened yet.
func lastStartupFrame(s string) string {
	if i := strings.LastIndex(s, "\x1b[J"); i >= 0 {
		return s[i+len("\x1b[J"):]
	}
	return s
}

func TestMcpLogSink_StartupWindowShowsAllServerLines(t *testing.T) {
	var errOut bytes.Buffer
	sink := newMcpLogSink(mcpLogRolling)
	sink.errOut = &errOut
	sink.termWidth = func() int { return 200 }
	sink.termHeight = func() int { return 40 }

	sink.AppendServerLog("notion", "started")
	sink.AppendServerLog("notion", "Please authorize this client by visiting:")
	sink.AppendServerLog("notion", "https://mcp.notion.com/authorize?response_type=code&client_id=abc")
	sink.AppendServerLog("notion", "fatal: boom")

	frame := lastStartupFrame(errOut.String())
	for _, want := range []string{
		"▸ mcp.notion log",
		"started",
		"» Please authorize this client by visiting:",
		"https://mcp.notion.com/authorize?response_type=code&client_id=abc",
		"✗ fatal: boom",
	} {
		if !strings.Contains(frame, want) {
			t.Errorf("startup window missing %q; frame: %q", want, frame)
		}
	}
	if count := strings.Count(frame, "▸ mcp.notion log"); count != 1 {
		t.Errorf("server header appears %d times in one frame, want 1", count)
	}
	if entries := sink.Drain(); entries != nil {
		t.Errorf("startup window lines also queued: %+v", entries)
	}
}

func TestMcpLogSink_StartupWindowGroupsPerServer(t *testing.T) {
	var errOut bytes.Buffer
	sink := newMcpLogSink(mcpLogRolling)
	sink.errOut = &errOut
	sink.termWidth = func() int { return 120 }
	sink.termHeight = func() int { return 40 }

	sink.AppendServerLog("notion", "notion line one")
	sink.AppendServerLog("linear", "linear line one")
	sink.AppendServerLog("notion", "notion line two")

	frame := lastStartupFrame(errOut.String())
	if count := strings.Count(frame, "▸ mcp.notion log"); count != 1 {
		t.Errorf("notion header appears %d times, want 1; frame: %q", count, frame)
	}
	if count := strings.Count(frame, "▸ mcp.linear log"); count != 1 {
		t.Errorf("linear header appears %d times, want 1; frame: %q", count, frame)
	}
	// A late notion line lands inside the notion window, above the linear
	// window: per-server grouping survives interleaved output.
	if strings.Index(frame, "notion line two") > strings.Index(frame, "▸ mcp.linear log") {
		t.Errorf("late notion line not grouped under its server window; frame: %q", frame)
	}
}

// TestMcpLogSink_StartupWindowPinsAuthPrompt reproduces the OAuth flow where
// the prompt and URL print first and the auth machinery then emits enough
// chatter to fill the tail: the prompt must stay pinned in the window while
// the chatter rolls.
func TestMcpLogSink_StartupWindowPinsAuthPrompt(t *testing.T) {
	var errOut bytes.Buffer
	sink := newMcpLogSink(mcpLogRolling)
	sink.errOut = &errOut
	sink.termWidth = func() int { return 200 }
	sink.termHeight = func() int { return 40 }

	sink.AppendServerLog("notion", "Please authorize this client by visiting:")
	sink.AppendServerLog("notion", "https://mcp.notion.com/authorize?client_id=abc")
	for i := range 8 {
		sink.AppendServerLog("notion", fmt.Sprintf("[109] chatter %d", i))
	}

	frame := lastStartupFrame(errOut.String())
	for _, want := range []string{
		"» Please authorize this client by visiting:",
		"» https://mcp.notion.com/authorize?client_id=abc",
		"chatter 7",
	} {
		if !strings.Contains(frame, want) {
			t.Errorf("startup window missing %q; frame: %q", want, frame)
		}
	}
	if strings.Contains(frame, "chatter 0") {
		t.Errorf("oldest chatter not evicted from the tail; frame: %q", frame)
	}
	if strings.Index(frame, "» Please authorize") > strings.Index(frame, "chatter 7") {
		t.Errorf("pinned auth lines not above the rolling tail; frame: %q", frame)
	}
}

func TestMcpLogSink_StartupWindowBoundsServerTail(t *testing.T) {
	var errOut bytes.Buffer
	sink := newMcpLogSink(mcpLogRolling)
	sink.errOut = &errOut
	sink.termWidth = func() int { return 120 }
	sink.termHeight = func() int { return 40 }

	for i := range 10 {
		sink.AppendServerLog("fs", fmt.Sprintf("line %d", i))
	}

	frame := lastStartupFrame(errOut.String())
	if strings.Contains(frame, "line 0\n") || strings.Contains(frame, "line 3\n") {
		t.Errorf("startup window shows lines past the tail bound; frame: %q", frame)
	}
	if !strings.Contains(frame, "line 9") {
		t.Errorf("newest line missing from startup window; frame: %q", frame)
	}
}

func TestMcpLogSink_StartupWindowCapsRegionToTerminalHeight(t *testing.T) {
	var errOut bytes.Buffer
	sink := newMcpLogSink(mcpLogRolling)
	sink.errOut = &errOut
	sink.termWidth = func() int { return 120 }
	sink.termHeight = func() int { return 8 }

	for _, server := range []string{"one", "two", "three", "four"} {
		for i := range 6 {
			sink.AppendServerLog(server, fmt.Sprintf("%s line %d", server, i))
		}
	}

	frame := lastStartupFrame(errOut.String())
	if rows := strings.Count(frame, "\n"); rows > 7 {
		t.Errorf("startup region %d rows exceeds terminal height budget 7; frame: %q", rows, frame)
	}
}

func TestMcpLogSink_StartupWindowShowsExitTail(t *testing.T) {
	var errOut bytes.Buffer
	sink := newMcpLogSink(mcpLogRolling)
	sink.errOut = &errOut
	sink.termWidth = func() int { return 120 }
	sink.termHeight = func() int { return 40 }
	sink.AppendServerLog("fs", "line one")
	sink.ServerExited("fs")

	frame := lastStartupFrame(errOut.String())
	for _, want := range []string{"▸ mcp.fs log", "line one"} {
		if !strings.Contains(frame, want) {
			t.Errorf("startup window missing %q after exit; frame: %q", want, frame)
		}
	}
	if entries := sink.Drain(); entries != nil {
		t.Errorf("pre-attach exit queued entries: %+v", entries)
	}
}

// TestMcpLogSink_SetupSucceededClearsStartupWindows pins the handoff
// contract: once MCP setup completes, the startup log windows are cleared in
// place, and later pre-attach lines queue for the session loop instead of
// rendering.
func TestMcpLogSink_SetupSucceededClearsStartupWindows(t *testing.T) {
	var errOut bytes.Buffer
	sink := newMcpLogSink(mcpLogRolling)
	sink.errOut = &errOut
	sink.termWidth = func() int { return 120 }
	sink.termHeight = func() int { return 40 }

	sink.AppendServerLog("notion", "Please authorize this client by visiting:")
	sink.AppendServerLog("notion", "https://example.com/authorize?x=1")
	if !strings.Contains(errOut.String(), "» Please authorize this client by visiting:") {
		t.Fatalf("startup window never rendered: %q", errOut.String())
	}
	errOut.Reset()

	sink.setupSucceeded()
	got := errOut.String()
	if !strings.Contains(got, "\x1b[") {
		t.Errorf("expected ANSI clear of the startup windows; got: %q", got)
	}
	if strings.Contains(got, "authorize") {
		t.Errorf("startup content re-rendered after clear: %q", got)
	}
	errOut.Reset()

	sink.AppendServerLog("notion", "please sign in again")
	if errOut.Len() != 0 {
		t.Errorf("post-setup line rendered into cleared region: %q", errOut.String())
	}
	entries := sink.Drain()
	if len(entries) != 1 || !entries[0].isError {
		t.Fatalf("post-setup auth line not queued for session elevation: %+v", entries)
	}
}

func TestMcpLogSink_SetupSucceededWithoutWindowIsNoop(t *testing.T) {
	var errOut bytes.Buffer
	sink := newMcpLogSink(mcpLogRolling)
	sink.errOut = &errOut
	sink.setupSucceeded()
	if errOut.Len() != 0 {
		t.Errorf("no-op clear wrote output: %q", errOut.String())
	}
}

func Test_notifyMcpSetupSucceeded_NilSinkIsANoop(t *testing.T) {
	notifyMcpSetupSucceeded(nil)
}

func TestMcpLogSink_AttachRestoresQueueing(t *testing.T) {
	var errOut bytes.Buffer
	sink := newMcpLogSink(mcpLogRolling)
	sink.errOut = &errOut
	sink.attach()

	sink.AppendServerLog("github", "please sign in")
	if errOut.Len() != 0 {
		t.Errorf("post-attach line printed directly: %q", errOut.String())
	}
	entries := sink.Drain()
	if len(entries) != 1 || !entries[0].isError {
		t.Fatalf("post-attach auth entry not queued for elevation: %+v", entries)
	}
}

// Test_McpSink_AuthPromptSurfacesDuringBlockedStartup pins the OAuth startup
// case end to end: the server prints an auth prompt to stderr and then hangs
// waiting for an authorization that never comes. No session loop exists to
// drain the sink, yet the prompt must reach stderr while the server is alive.
func Test_McpSink_AuthPromptSurfacesDuringBlockedStartup(t *testing.T) {
	var errOut lockedBuffer
	sink := newMcpLogSink(mcpLogRolling)
	sink.errOut = &errOut
	sink.termWidth = func() int { return 200 }
	srv := pub_models.McpServer{
		Name:    "oauth",
		Command: "go",
		Args:    []string{"run", "../tools/mcp/testserver"},
		Env:     map[string]string{"TEST_SERVER_AUTH_HANG": "1"},
	}
	_, _, err := mcp.Client(t.Context(), srv, sink)
	if err != nil {
		t.Fatalf("client: %v", err)
	}

	deadline := time.Now().Add(10 * time.Second)
	for {
		got := errOut.String()
		if strings.Contains(got, "▸ mcp.oauth log") &&
			strings.Contains(got, "» Please authorize this client by visiting:") &&
			strings.Contains(got, "https://example.com/authorize?client_id=test") {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("auth prompt never surfaced while startup blocked; got:\n%q", got)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestMcpLogSink_RollingModeQueuesLines(t *testing.T) {
	sink := newMcpLogSink(mcpLogRolling)
	sink.attach()
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

func TestMcpLogSink_RollingModeElevatesAuthLines(t *testing.T) {
	sink := newMcpLogSink(mcpLogRolling)
	sink.attach()
	sink.AppendServerLog("github", "started")
	sink.AppendServerLog("github", "Please visit https://github.com/login/device")

	entries := sink.Drain()
	if len(entries) != 2 {
		t.Fatalf("Drain() = %d entries, want 2", len(entries))
	}
	if entries[0].isError {
		t.Errorf("entry %q classified for elevation, want normal", entries[0].line)
	}
	if !entries[1].isError {
		t.Errorf("auth entry %q not classified for elevation", entries[1].line)
	}
}

func TestMcpLogSink_AuthFollowWindowElevatesPayloadLines(t *testing.T) {
	sink := newMcpLogSink(mcpLogRolling)
	sink.attach()
	sink.AppendServerLog("github", "To authenticate, visit:")
	sink.AppendServerLog("github", "https://example.com/device-flow")
	sink.AppendServerLog("github", "and enter your one-time code")
	sink.AppendServerLog("github", "ABCD-1234")
	sink.AppendServerLog("github", "plain line past the follow window")

	entries := sink.Drain()
	if len(entries) != 5 {
		t.Fatalf("Drain() = %d entries, want 5", len(entries))
	}
	for i := range 4 {
		if !entries[i].isError {
			t.Errorf("entry %d %q not elevated, want elevated (auth prompt or payload)", i, entries[i].line)
		}
	}
	if entries[4].isError {
		t.Errorf("entry %q elevated past the follow window, want normal", entries[4].line)
	}
}

func TestMcpLogSink_AuthFollowWindowSkipsChatter(t *testing.T) {
	sink := newMcpLogSink(mcpLogRolling)
	sink.attach()
	sink.AppendServerLog("notion", "Please authorize this client by visiting:")
	sink.AppendServerLog("notion", "Browser opened automatically.")
	sink.AppendServerLog("notion", "Creating lockfile for server cb42 with process 115 on port 9553")

	entries := sink.Drain()
	if len(entries) != 3 {
		t.Fatalf("Drain() = %d entries, want 3", len(entries))
	}
	if !entries[0].isError {
		t.Errorf("auth prompt %q not elevated", entries[0].line)
	}
	for _, entry := range entries[1:] {
		if entry.isError {
			t.Errorf("chatter %q elevated by follow window, want normal", entry.line)
		}
	}
}

func TestMcpLogSink_AuthFollowWindowIsPerServer(t *testing.T) {
	sink := newMcpLogSink(mcpLogRolling)
	sink.attach()
	sink.AppendServerLog("github", "please sign in")
	sink.AppendServerLog("fs", "plain line from another server")

	entries := sink.Drain()
	if len(entries) != 2 {
		t.Fatalf("Drain() = %d entries, want 2", len(entries))
	}
	if !entries[0].isError {
		t.Errorf("auth entry %q not elevated", entries[0].line)
	}
	if entries[1].isError {
		t.Errorf("entry %q from unrelated server elevated by follow window", entries[1].line)
	}
}

func TestMcpLogSink_RawStructuredPrintsAuthLines(t *testing.T) {
	var errOut bytes.Buffer
	sink := newMcpLogSink(mcpLogRawStructured)
	sink.errOut = &errOut

	sink.AppendServerLog("github", "started")
	sink.AppendServerLog("github", "Not authenticated. Run 'gh auth login'")
	sink.AppendServerLog("github", "https://example.com/device-flow")
	sink.AppendServerLog("github", "Browser opened automatically.")
	sink.AppendServerLog("github", "plain line past the follow window")

	got := errOut.String()
	if strings.Contains(got, "started") {
		t.Errorf("raw/structured mode printed normal line: %q", got)
	}
	for _, want := range []string{"Not authenticated", "https://example.com/device-flow"} {
		if !strings.Contains(got, want) {
			t.Errorf("raw/structured mode dropped auth line %q; got: %q", want, got)
		}
	}
	for _, absent := range []string{"Browser opened", "past the follow window"} {
		if strings.Contains(got, absent) {
			t.Errorf("raw/structured mode printed chatter %q: %q", absent, got)
		}
	}
}

func TestMcpLogSink_NotifySignalsOnAuth(t *testing.T) {
	sink := newMcpLogSink(mcpLogRolling)
	sink.attach()
	sink.AppendServerLog("github", "please sign in at https://example.com")
	select {
	case <-sink.Notify():
	default:
		t.Fatal("auth line must signal the session loop")
	}
}

func TestMcpLogSink_OverflowDropsOldestNonErrorFirst(t *testing.T) {
	sink := newMcpLogSink(mcpLogRolling)
	sink.attach()
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
	sink.attach()
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
	sink.attach()
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
	sink.attach()
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
	q.mcpSink.attach()
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
	q.mcpSink.attach()
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

func Test_drainMcpLogs_AuthPromptElevates(t *testing.T) {
	var out strings.Builder
	q := &Querier[*MockQuerier]{
		out:  &out,
		dims: dimensions.Dimensions{Width: 80, Height: 24},
	}
	q.mcpSink = newMcpLogSink(mcpLogRolling)
	q.mcpSink.attach()
	q.mcpSink.AppendServerLog("github", "To authenticate, visit:")
	q.mcpSink.AppendServerLog("github", "https://example.com/device")

	if err := q.drainMcpLogs(); err != nil {
		t.Fatalf("drainMcpLogs: %v", err)
	}
	got := out.String()
	for _, want := range []string{"» To authenticate, visit:", "https://example.com/device"} {
		if !strings.Contains(got, want) {
			t.Errorf("elevated auth block missing %q; got:\n%s", want, got)
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
	q.mcpSink.attach()
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
	q.mcpSink.attach()
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
