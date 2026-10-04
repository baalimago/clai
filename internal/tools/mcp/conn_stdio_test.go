package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/baalimago/clai/pkg/claierr"
	pub_models "github.com/baalimago/clai/pkg/text/models"
)

// testServerBinary compiles the shared stdio fixture exactly once per test
// binary run and returns its path. A timing-sensitive test must never
// enclose a go run compile, or its bound would measure the Go build cache
// rather than the code under test (D41, invariant 15, R2-01): on a cold
// cache, "go run ./testserver" inside a 200ms connect bound fails every
// time, deterministically, because the bound expires before the compile
// even finishes.
func testServerBinary(t *testing.T) string {
	t.Helper()
	testServerBinOnce.Do(func() {
		f, err := os.CreateTemp("", "clai-mcp-testserver-*")
		if err != nil {
			testServerBinErr = fmt.Errorf("create temp file: %w", err)
			return
		}
		f.Close()
		cmd := exec.Command("go", "build", "-o", f.Name(), "./testserver")
		if out, buildErr := cmd.CombinedOutput(); buildErr != nil {
			testServerBinErr = fmt.Errorf("go build testserver: %w: %s", buildErr, out)
			return
		}
		testServerBinPath = f.Name()
	})
	if testServerBinErr != nil {
		t.Fatalf("build testserver binary: %v", testServerBinErr)
	}
	return testServerBinPath
}

var (
	testServerBinOnce sync.Once
	testServerBinPath string
	testServerBinErr  error
)

// recordingSink collects stderr lines and exit notifications for assertions.
// It also implements AuthPendingSink (phase 6), recording every AuthPending
// call so a test can assert the signal fired without a real log sink.
type recordingSink struct {
	mu          sync.Mutex
	lines       []string
	exited      []string
	authPending []string
	authDone    int
}

func (r *recordingSink) AppendServerLog(_ string, line string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.lines = append(r.lines, line)
}

func (r *recordingSink) ServerExited(server string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.exited = append(r.exited, server)
}

func (r *recordingSink) AuthPending(server string) (done func()) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.authPending = append(r.authPending, server)
	return func() {
		r.mu.Lock()
		defer r.mu.Unlock()
		r.authDone++
	}
}

func (r *recordingSink) snapshot() (lines []string, exited []string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.lines...), append([]string(nil), r.exited...)
}

func (r *recordingSink) authSnapshot() (pending []string, done int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.authPending...), r.authDone
}

// newPipeConn wires a StdioConn over a pair of in-process pipes instead of a
// spawned process, so a test can script the server side with full control
// over exactly what bytes it sends. serverReads decodes what the connection
// writes (requests and notifications); serverWrites is the raw stream the
// test's fake server writes responses onto.
func newPipeConn(t *testing.T, ctx context.Context, sink ServerLogSink, opts ...StdioConnOption) (c *StdioConn, serverReads *json.Decoder, serverWrites io.Writer) {
	t.Helper()
	reqR, reqW := io.Pipe()
	respR, respW := io.Pipe()
	t.Cleanup(func() {
		reqW.Close()
		respW.Close()
	})

	transport := stdioTransport{
		stdin:  reqW,
		stdout: respR,
		stderr: strings.NewReader(""),
		wait:   func() {},
	}
	conn := newStdioConn(ctx, "fake", transport, sink, opts...)
	return conn, json.NewDecoder(reqR), respW
}

// TestConnCallAssignsUniqueIDsAcrossTools pins D3: one id source, owned by
// the connection, serves the handshake and every tool call. No per-tool
// counter exists any more to collide.
func TestConnCallAssignsUniqueIDsAcrossTools(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	c, dec, _ := newPipeConn(t, ctx, nil)

	reqs := make(chan Request, 3)
	go func() {
		for range 3 {
			var r Request
			if err := dec.Decode(&r); err != nil {
				return
			}
			reqs <- r
		}
	}()

	callCtx, callCancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer callCancel()
	// Nobody answers; each call returns once its own short deadline fires,
	// but the request it wrote is what this test inspects.
	_, _ = c.Call(callCtx, "initialize", nil)
	_, _ = c.Call(callCtx, "tools/list", nil)
	_, _ = c.Call(callCtx, "tools/call", map[string]any{"name": "echo"})

	wantMethods := []string{"initialize", "tools/list", "tools/call"}
	for i, want := range wantMethods {
		select {
		case r := <-reqs:
			if r.ID != i+1 {
				t.Fatalf("request %d (%s): id = %d, want %d", i, want, r.ID, i+1)
			}
			if r.Method != want {
				t.Fatalf("request %d: method = %q, want %q", i, r.Method, want)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("request %d (%s) never reached the fake server", i, want)
		}
	}
}

// TestConnCallDeliversResponseToMatchingWaiter pins the core demux fix: a
// response reaches the waiter registered for its own id, never another.
func TestConnCallDeliversResponseToMatchingWaiter(t *testing.T) {
	c := &StdioConn{serverName: "fake", pending: map[int]chan pendingResult{}}
	ch1 := make(chan pendingResult, 1)
	ch2 := make(chan pendingResult, 1)
	c.pending[1] = ch1
	c.pending[2] = ch2

	c.deliver(2, Response{JSONRPC: "2.0", ID: 2, Result: json.RawMessage(`"beta"`)})

	select {
	case res := <-ch2:
		if string(res.raw) != `"beta"` {
			t.Fatalf("waiter 2 got %q, want beta", res.raw)
		}
	default:
		t.Fatal("waiter 2 received nothing")
	}
	select {
	case <-ch1:
		t.Fatal("waiter 1 was delivered a response meant for waiter 2")
	default:
	}
	if _, ok := c.pending[2]; ok {
		t.Fatal("waiter 2 still pending after delivery")
	}
	if _, ok := c.pending[1]; !ok {
		t.Fatal("waiter 1 removed although it never received anything")
	}
}

// TestConnConcurrentCallsDoNotStealResponses is the defensive contract: the
// seam is package-public, and this pins that two simultaneous callers on one
// connection never steal each other's response, even though nothing in this
// repository issues two calls concurrently today.
func TestConnConcurrentCallsDoNotStealResponses(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	c, dec, w := newPipeConn(t, ctx, nil)
	enc := json.NewEncoder(w)

	go func() {
		var r1, r2 Request
		dec.Decode(&r1)
		dec.Decode(&r2)
		// Answer the second-received request first.
		enc.Encode(Response{JSONRPC: "2.0", ID: r2.ID, Result: json.RawMessage(`"second"`)})
		enc.Encode(Response{JSONRPC: "2.0", ID: r1.ID, Result: json.RawMessage(`"first"`)})
	}()

	var wg sync.WaitGroup
	results := make([]string, 2)
	errs := make([]error, 2)
	wg.Add(2)
	for i := range 2 {
		go func(i int) {
			defer wg.Done()
			raw, err := c.Call(ctx, "tools/call", nil)
			results[i] = string(raw)
			errs[i] = err
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
	}
	got := map[string]bool{results[0]: true, results[1]: true}
	if !got[`"first"`] || !got[`"second"`] {
		t.Fatalf("results = %v, want exactly {\"first\", \"second\"}", results)
	}
}

// TestConnUnknownIDFrameIsDropped pins that a frame with no matching waiter
// is dropped, not treated as a connection error.
func TestConnUnknownIDFrameIsDropped(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	c, dec, w := newPipeConn(t, ctx, nil)
	enc := json.NewEncoder(w)

	go func() {
		// Nobody is waiting for id 999.
		enc.Encode(Response{JSONRPC: "2.0", ID: 999, Result: json.RawMessage(`{}`)})
		var r Request
		dec.Decode(&r)
		enc.Encode(Response{JSONRPC: "2.0", ID: r.ID, Result: json.RawMessage(`"ok"`)})
	}()

	raw, err := c.Call(ctx, "tools/call", nil)
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if string(raw) != `"ok"` {
		t.Fatalf("raw = %s, want \"ok\"", raw)
	}
}

// TestStdioConnPublishesServerInitiatedNotifications pins R2-16's stdio
// half: StdioConn implements NotificationWatcher exactly as HttpConn does,
// so a server notification with no id (e.g.
// notifications/tools/list_changed) reaches the same channel the endpoint
// schema cache's watcher already consumes, instead of being silently
// dropped.
func TestStdioConnPublishesServerInitiatedNotifications(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	c, _, w := newPipeConn(t, ctx, nil)

	watcher, ok := Conn(c).(NotificationWatcher)
	if !ok {
		t.Fatal("StdioConn does not implement NotificationWatcher")
	}

	enc := json.NewEncoder(w)
	if err := enc.Encode(map[string]any{"jsonrpc": "2.0", "method": "notifications/tools/list_changed"}); err != nil {
		t.Fatalf("write notification: %v", err)
	}

	select {
	case method := <-watcher.Notifications():
		if method != "notifications/tools/list_changed" {
			t.Errorf("method = %q, want notifications/tools/list_changed", method)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("notification never arrived on the watcher channel")
	}
}

// TestStdioConnCloseClosesNotificationChannel mirrors
// TestHttpConnCloseClosesNotificationChannel (R1-27's class): Close closes
// notifyCh so a tools-list-changed watcher stops promptly instead of
// running against a dead connection for the rest of the run.
func TestStdioConnCloseClosesNotificationChannel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	c, _, _ := newPipeConn(t, ctx, nil)

	watcher := Conn(c).(NotificationWatcher)
	if err := c.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	select {
	case _, ok := <-watcher.Notifications():
		if ok {
			t.Fatal("expected the notification channel to be closed, got a value instead")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("notification channel was never closed")
	}
}

// TestStdioConnDecodeFailureFailsAllPendingWaiters pins that an id-less
// frame fails every call pending on the connection with the same typed
// error, since attributing it to one arbitrary caller would silently
// corrupt the others.
func TestStdioConnDecodeFailureFailsAllPendingWaiters(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	c, dec, w := newPipeConn(t, ctx, nil)

	go func() {
		var r1, r2 Request
		dec.Decode(&r1)
		dec.Decode(&r2)
		fmt.Fprintln(w, "not valid json at all {{{")
	}()

	var wg sync.WaitGroup
	errs := make([]error, 2)
	wg.Add(2)
	for i := range 2 {
		go func(i int) {
			defer wg.Done()
			_, errs[i] = c.Call(ctx, "tools/call", nil)
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		if err == nil {
			t.Fatalf("call %d: expected an error, got nil", i)
		}
		if !errors.Is(err, claierr.ErrMcpFrameUndecodable) {
			t.Fatalf("call %d: err = %v, want ErrMcpFrameUndecodable", i, err)
		}
	}
}

// TestStdioConnSurfacesDecodeFailureAsCallError pins the single-waiter case:
// the frame fails that one call rather than being logged and dropped, which
// would otherwise leave it waiting until its context expires.
func TestStdioConnSurfacesDecodeFailureAsCallError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	c, dec, w := newPipeConn(t, ctx, nil)

	go func() {
		var r Request
		dec.Decode(&r)
		fmt.Fprintln(w, "not valid json at all {{{")
	}()

	_, err := c.Call(ctx, "tools/call", nil)
	if err == nil {
		t.Fatal("expected an error, got nil")
	}
	if !errors.Is(err, claierr.ErrMcpFrameUndecodable) {
		t.Fatalf("err = %v, want ErrMcpFrameUndecodable", err)
	}
}

// TestStdioConnOversizedFrameIsBoundedError pins the read-bound limit: the
// oversized frame fails its call with a bounded error, and the connection
// survives to serve the next call.
func TestStdioConnOversizedFrameIsBoundedError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	c, dec, w := newPipeConn(t, ctx, nil, WithReadBound(64))

	go func() {
		var r1 Request
		dec.Decode(&r1)
		fmt.Fprintf(w, "{\"jsonrpc\":\"2.0\",\"id\":%d,\"result\":\"%s\"}\n", r1.ID, strings.Repeat("A", 200))
		var r2 Request
		dec.Decode(&r2)
		fmt.Fprintf(w, "{\"jsonrpc\":\"2.0\",\"id\":%d,\"result\":\"ok\"}\n", r2.ID)
	}()

	_, err := c.Call(ctx, "tools/call", nil)
	if err == nil {
		t.Fatal("expected a bounded error for the oversized frame")
	}
	if !errors.Is(err, claierr.ErrMcpFrameUndecodable) {
		t.Fatalf("err = %v, want ErrMcpFrameUndecodable", err)
	}

	raw, err := c.Call(ctx, "tools/call", nil)
	if err != nil {
		t.Fatalf("second call after the oversized frame: %v", err)
	}
	if string(raw) != `"ok"` {
		t.Fatalf("raw = %s, want \"ok\"", raw)
	}
}

// TestConnServerToClientRequestIsRejectedWithMethodNotFound pins that a
// server-initiated request receives a definite JSON-RPC error rather than
// being ignored: clai advertises no roots/sampling/elicitation capability,
// so a well-behaved server never asks, but a server that does must not
// stall.
func TestConnServerToClientRequestIsRejectedWithMethodNotFound(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	_, dec, w := newPipeConn(t, ctx, nil)
	enc := json.NewEncoder(w)
	enc.Encode(map[string]any{"jsonrpc": "2.0", "id": 7, "method": "roots/list"})

	var resp Response
	if err := dec.Decode(&resp); err != nil {
		t.Fatalf("decode the connection's answer: %v", err)
	}
	if resp.ID != 7 {
		t.Fatalf("id = %d, want 7", resp.ID)
	}
	if resp.Error == nil || resp.Error.Code != -32601 {
		t.Fatalf("error = %+v, want method-not-found (-32601)", resp.Error)
	}
}

// TestConnNotifyCarriesNoID pins that Notify writes a notification with no
// id and registers no waiter.
func TestConnNotifyCarriesNoID(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	c, dec, _ := newPipeConn(t, ctx, nil)

	// The pipe is unbuffered: the fake server's read must already be in
	// flight before Notify's write can complete.
	type decoded struct {
		raw map[string]any
		err error
	}
	done := make(chan decoded, 1)
	go func() {
		var raw map[string]any
		err := dec.Decode(&raw)
		done <- decoded{raw: raw, err: err}
	}()

	if err := c.Notify(ctx, "notifications/initialized", map[string]any{}); err != nil {
		t.Fatalf("notify: %v", err)
	}

	select {
	case d := <-done:
		if d.err != nil {
			t.Fatalf("decode: %v", d.err)
		}
		if _, ok := d.raw["id"]; ok {
			t.Fatalf("notification carried an id: %v", d.raw)
		}
		if d.raw["method"] != "notifications/initialized" {
			t.Fatalf("method = %v, want notifications/initialized", d.raw["method"])
		}
	case <-time.After(2 * time.Second):
		t.Fatal("notification never reached the fake server")
	}
}

// TestConnCloseUnblocksPendingCalls pins that Close fails a pending call
// with a typed error instead of leaving it to its context's deadline.
func TestConnCloseUnblocksPendingCalls(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	c, dec, _ := newPipeConn(t, ctx, nil)

	go func() {
		var r Request
		dec.Decode(&r) // read it, never answer
	}()

	done := make(chan error, 1)
	go func() {
		_, err := c.Call(context.Background(), "tools/call", nil)
		done <- err
	}()

	deadline := time.Now().Add(2 * time.Second)
	for {
		c.mu.Lock()
		n := len(c.pending)
		c.mu.Unlock()
		if n > 0 || time.Now().After(deadline) {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	if err := c.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	select {
	case err := <-done:
		if !errors.Is(err, claierr.ErrMcpConnClosed) {
			t.Fatalf("err = %v, want ErrMcpConnClosed", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the pending call never unblocked after Close")
	}
}

// TestConnCloseIsIdempotent pins that a second Close is a no-op and returns
// no error.
func TestConnCloseIsIdempotent(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	c, _, _ := newPipeConn(t, ctx, nil)

	if err := c.Close(); err != nil {
		t.Fatalf("first close: %v", err)
	}
	if err := c.Close(); err != nil {
		t.Fatalf("second close: %v", err)
	}
}

// TestStdioConnSpawnFailureReturnsTypedStartupError pins that a process that
// cannot be started returns a typed startup error naming the server and the
// spawn stage.
func TestStdioConnSpawnFailureReturnsTypedStartupError(t *testing.T) {
	_, err := NewStdioConn(t.Context(), pub_models.McpServer{Name: "bad", Command: "does-not-exist-anywhere"}, nil)
	if err == nil {
		t.Fatal("expected an error for a bad command")
	}
	var startupErr *claierr.McpServerStartupError
	if !errors.As(err, &startupErr) {
		t.Fatalf("err = %v, want *claierr.McpServerStartupError", err)
	}
	if startupErr.ServerName != "bad" {
		t.Errorf("ServerName = %q, want %q", startupErr.ServerName, "bad")
	}
	if startupErr.Stage != "spawn" {
		t.Errorf("Stage = %q, want %q", startupErr.Stage, "spawn")
	}
}

// TestStdioConnEncodesRequestsToProcessStdin pins that a request issued
// through Call actually reaches the spawned process's stdin and that its
// response comes back over stdout, end to end.
func TestStdioConnEncodesRequestsToProcessStdin(t *testing.T) {
	srv := pub_models.McpServer{Name: "echo", Command: "go", Args: []string{"run", "./testserver"}}
	conn, err := NewStdioConn(t.Context(), srv, nil)
	if err != nil {
		t.Fatalf("NewStdioConn: %v", err)
	}
	t.Cleanup(func() { conn.Close() })

	raw, err := conn.Call(t.Context(), "initialize", nil)
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if string(raw) != "{}" {
		t.Fatalf("result = %s, want {}", raw)
	}
}

// TestStdioConnSingleCallBoundExpires pins the pre-existing per-server
// timeout_seconds field: a call against a server that answers the handshake
// but stalls a specific call expires once the caller's own bounded context
// does, rather than waiting forever.
func TestStdioConnSingleCallBoundExpires(t *testing.T) {
	srv := pub_models.McpServer{Name: "echo", Command: "go", Args: []string{"run", "./testserver"}}
	conn, err := NewStdioConn(t.Context(), srv, nil)
	if err != nil {
		t.Fatalf("NewStdioConn: %v", err)
	}
	t.Cleanup(func() { conn.Close() })

	ctx, cancel := context.WithTimeout(t.Context(), 300*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err = conn.Call(ctx, "tools/call", map[string]any{"name": "hang", "arguments": map[string]any{}})
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected an error from the stalled call, got nil")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want DeadlineExceeded", err)
	}
	if elapsed > 2*time.Second {
		t.Fatalf("the single-call bound did not fire promptly: elapsed %v", elapsed)
	}
}

// TestStdioConnStderrStillFeedsSink pins C3-01: the stderr reader is a
// separate goroutine from the frame reader and keeps feeding ServerLogSink
// unchanged after the transport was replaced.
func TestStdioConnStderrStillFeedsSink(t *testing.T) {
	sink := &recordingSink{}
	srv := pub_models.McpServer{
		Name:    "echo",
		Command: "go",
		Args:    []string{"run", "./testserver"},
		Env:     map[string]string{"TEST_SERVER_STDERR": "1"},
	}
	conn, err := NewStdioConn(t.Context(), srv, sink)
	if err != nil {
		t.Fatalf("NewStdioConn: %v", err)
	}
	t.Cleanup(func() { conn.Close() })

	// One round trip gives the server time to write its stderr lines.
	if _, err := conn.Call(t.Context(), "initialize", nil); err != nil {
		t.Fatalf("call: %v", err)
	}

	deadline := time.Now().Add(5 * time.Second)
	for {
		lines, _ := sink.snapshot()
		if len(lines) >= 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("sink received %d lines, want 2: %v", len(lines), lines)
		}
		time.Sleep(10 * time.Millisecond)
	}
	lines, _ := sink.snapshot()
	if lines[0] != "stderr line one" {
		t.Errorf("first line = %q, want %q", lines[0], "stderr line one")
	}
	if lines[1] != "stderr line two: an error occurred" {
		t.Errorf("second line = %q, want %q", lines[1], "stderr line two: an error occurred")
	}
}

// TestStdioConn_ServerExitNotifiesSinkOnCrash pins that an unexpected exit
// while the run context is alive is reported to the sink.
func TestStdioConn_ServerExitNotifiesSinkOnCrash(t *testing.T) {
	sink := &recordingSink{}
	srv := pub_models.McpServer{
		Name:    "echo",
		Command: "go",
		Args:    []string{"run", "./testserver"},
		Env:     map[string]string{"TEST_SERVER_STDERR": "1", "TEST_SERVER_EXIT": "1"},
	}
	if _, err := NewStdioConn(t.Context(), srv, sink); err != nil {
		t.Fatalf("NewStdioConn: %v", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		_, exited := sink.snapshot()
		if len(exited) > 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("ServerExited never fired after the server died")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestStdioConn_ServerExitNotSignaledOnNormalTeardown pins that cancelling
// the run context is not reported as a crash.
func TestStdioConn_ServerExitNotSignaledOnNormalTeardown(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	sink := &recordingSink{}
	srv := pub_models.McpServer{
		Name:    "echo",
		Command: "go",
		Args:    []string{"run", "./testserver"},
		Env:     map[string]string{"TEST_SERVER_STDERR": "1"},
	}
	conn, err := NewStdioConn(ctx, srv, sink)
	if err != nil {
		t.Fatalf("NewStdioConn: %v", err)
	}
	if _, err := conn.Call(ctx, "initialize", nil); err != nil {
		t.Fatalf("call: %v", err)
	}
	cancel()
	time.Sleep(200 * time.Millisecond)
	_, exited := sink.snapshot()
	if len(exited) != 0 {
		t.Errorf("ServerExited fired on normal teardown: %v", exited)
	}
}

// TestStdioConnectReclassifiesAuthPromptAsChallenge is a supplementary test
// beyond the phase's declared names (phase 5's own executor established the
// convention of adding a cheap one where the declared set left an easy
// gap): it proves the real production wiring phase 6 adds to conn_stdio.go
// and connector.go, end to end, rather than only through a fake. A lazy
// connect against a server whose stderr shows an authorization prompt
// before its connect bound expires is reclassified from a plain
// connect-stage timeout into a typed AuthChallengeError, the sink is told a
// wait is pending, and the wait resolves once the process is reaped.
func TestStdioConnectReclassifiesAuthPromptAsChallenge(t *testing.T) {
	bin := testServerBinary(t)
	sink := &recordingSink{}
	srv := pub_models.McpServer{
		Name:    "hang",
		Command: bin,
		Env:     map[string]string{"TEST_SERVER_AUTH_HANG": "1"},
	}
	c := NewConnector(t.Context(), srv, sink, WithConnectBound(200*time.Millisecond))

	_, err := c.Conn(t.Context())
	var challenge *claierr.AuthChallengeError
	if !errors.As(err, &challenge) {
		t.Fatalf("err = %v, want *claierr.AuthChallengeError", err)
	}
	if challenge.ServerName != "hang" {
		t.Errorf("ServerName = %q, want %q", challenge.ServerName, "hang")
	}

	pending, _ := sink.authSnapshot()
	if len(pending) != 1 || pending[0] != "hang" {
		t.Fatalf("authPending = %v, want exactly one call naming %q", pending, "hang")
	}

	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, done := sink.authSnapshot(); done == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the auth-pending wait never resolved after the process was reaped")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
