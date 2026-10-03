package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/baalimago/clai/pkg/claierr"
	pub_models "github.com/baalimago/clai/pkg/text/models"
)

// TestConnectorLazyDoesNotConnectDuringSetup pins that merely constructing a
// Connector spawns nothing: the marker-writing command never runs until the
// connection is actually resolved.
func TestConnectorLazyDoesNotConnectDuringSetup(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "spawned")
	srv := pub_models.McpServer{Name: "lazy", Command: "sh", Args: []string{"-c", "touch " + marker}}

	_ = NewConnector(t.Context(), srv, nil)

	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("constructing a Connector spawned a process, marker stat err: %v", err)
	}
}

// TestConnectorEagerConnectsDuringSetup pins the eager posture: resolving
// right after construction, as setup does for an eager server, spawns the
// process immediately rather than waiting for a tool call.
func TestConnectorEagerConnectsDuringSetup(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "spawned")
	srv := pub_models.McpServer{Name: "eager", Command: "sh", Args: []string{"-c", "touch " + marker}}
	c := NewConnector(t.Context(), srv, nil)

	_, _ = c.Conn(t.Context())

	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("eager resolution never spawned the process: %v", err)
	}
}

// TestConnectorSingleFlightCreatesOneConnPerRun pins D3's extension to the
// connector: concurrent callers for one server share one resolution and all
// receive the same outcome, driven directly since no production path issues
// concurrent calls today.
func TestConnectorSingleFlightCreatesOneConnPerRun(t *testing.T) {
	var calls int32
	fc := &fakeConn{raw: json.RawMessage(`"ok"`)}
	c := newConnector(t.Context(), func(context.Context) (Conn, error) {
		atomic.AddInt32(&calls, 1)
		time.Sleep(50 * time.Millisecond)
		return fc, nil
	})

	const n = 5
	var wg sync.WaitGroup
	results := make([]Conn, n)
	errs := make([]error, n)
	wg.Add(n)
	for i := range n {
		go func(i int) {
			defer wg.Done()
			results[i], errs[i] = c.Conn(t.Context())
		}(i)
	}
	wg.Wait()

	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("dial called %d times, want 1", got)
	}
	for i, err := range errs {
		if err != nil {
			t.Fatalf("caller %d: %v", i, err)
		}
		if results[i] != fc {
			t.Fatalf("caller %d got a different conn than the others", i)
		}
	}
}

// TestThreeCallsOneServerSpawnOnce pins that a batch of calls to one server
// resolves one connection: today's sequential tool-batch execution reaches
// this through plain memoisation.
func TestThreeCallsOneServerSpawnOnce(t *testing.T) {
	var calls int32
	fc := &fakeConn{}
	c := newConnector(t.Context(), func(context.Context) (Conn, error) {
		atomic.AddInt32(&calls, 1)
		return fc, nil
	})

	for i := range 3 {
		if _, err := c.Conn(t.Context()); err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("dial called %d times across three calls, want 1", got)
	}
}

// TestConnectorConnectFailureIsRunScopedAndNotRetried pins the retry-count
// parameter: a connect failure is memoised and every later call returns it
// unchanged, with no second dial.
func TestConnectorConnectFailureIsRunScopedAndNotRetried(t *testing.T) {
	var calls int32
	wantErr := claierr.NewMcpServerStartup("broken", "spawn", errors.New("boom"))
	c := newConnector(t.Context(), func(context.Context) (Conn, error) {
		atomic.AddInt32(&calls, 1)
		return nil, wantErr
	})

	for i := range 3 {
		_, err := c.Conn(t.Context())
		if !errors.Is(err, claierr.ErrMcpServerStartup) {
			t.Fatalf("call %d: err = %v, want ErrMcpServerStartup", i, err)
		}
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("dial called %d times, want 1 (no retry after a connect failure)", got)
	}
}

// blockedOutsideRunErr is a hand-built error satisfying outsideRunBlocker,
// standing in for the auth-pending signal a later phase introduces.
type blockedOutsideRunErr struct{}

func (blockedOutsideRunErr) Error() string           { return "blocked outside the run" }
func (blockedOutsideRunErr) BlockedOutsideRun() bool { return true }

// TestBlockedResolutionIsNotMemoisedAsFailure pins that a resolution blocked
// on something outside the run is returned to the caller but never
// memoised, so the next call dials again instead of replaying a cached
// failure.
func TestBlockedResolutionIsNotMemoisedAsFailure(t *testing.T) {
	var calls int32
	fc := &fakeConn{}
	c := newConnector(t.Context(), func(context.Context) (Conn, error) {
		if atomic.AddInt32(&calls, 1) == 1 {
			return nil, blockedOutsideRunErr{}
		}
		return fc, nil
	})

	_, err := c.Conn(t.Context())
	var blocked blockedOutsideRunErr
	if !errors.As(err, &blocked) {
		t.Fatalf("first call err = %v, want blockedOutsideRunErr", err)
	}

	conn, err := c.Conn(t.Context())
	if err != nil {
		t.Fatalf("second call after a blocked resolution: %v", err)
	}
	if conn != fc {
		t.Fatal("second call did not resolve a connection")
	}
	if got := atomic.LoadInt32(&calls); got != 2 {
		t.Fatalf("dial called %d times, want 2 (a blocked outcome must not consume the retry budget)", got)
	}
}

// TestConnectorConnectFailureSurfacesTypedToolResult pins that a failed
// server's later tool calls return the same typed error as a tool result,
// naming the tool, with no new spawn.
func TestConnectorConnectFailureSurfacesTypedToolResult(t *testing.T) {
	var calls int32
	wantErr := claierr.NewMcpServerStartup("broken", "spawn", errors.New("boom"))
	c := newConnector(t.Context(), func(context.Context) (Conn, error) {
		atomic.AddInt32(&calls, 1)
		return nil, wantErr
	})
	mt := &mcpTool{remoteName: "echo", connector: c}

	for i := range 2 {
		_, err := mt.CallWithContext(t.Context(), pub_models.Input{"text": "hi"})
		if err == nil {
			t.Fatalf("call %d: expected an error, got nil", i)
		}
		if !errors.Is(err, claierr.ErrMcpServerStartup) {
			t.Fatalf("call %d: err = %v, want ErrMcpServerStartup", i, err)
		}
		if !strings.Contains(err.Error(), "echo") {
			t.Fatalf("call %d: err = %v, want it to name the tool", i, err)
		}
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("dial called %d times across two tool calls, want 1", got)
	}
}

// TestConnectTimeoutIsSeparateFromCallTimeout pins that connection
// resolution is excluded from the per-call bound: a resolve slower than the
// tool's own call timeout must still succeed.
func TestConnectTimeoutIsSeparateFromCallTimeout(t *testing.T) {
	fc := &fakeConn{raw: json.RawMessage(`{"content":[{"type":"text","text":"hi"}],"isError":false}`)}
	c := newConnector(t.Context(), func(context.Context) (Conn, error) {
		time.Sleep(150 * time.Millisecond)
		return fc, nil
	})
	mt := &mcpTool{remoteName: "echo", connector: c, timeout: 50 * time.Millisecond}

	res, err := mt.CallWithContext(t.Context(), pub_models.Input{"text": "hi"})
	if err != nil {
		t.Fatalf("call: %v, want the slow resolve excluded from the 50ms call bound", err)
	}
	if res != "hi" {
		t.Errorf("res = %q, want hi", res)
	}
}

// TestConnectBoundExpiryReturnsTypedStartupError pins the connect-bound
// limit: a fake server that completes its spawn but never answers initialize
// expires the outer connect bound rather than the connection's own,
// larger-by-default handshake bound.
func TestConnectBoundExpiryReturnsTypedStartupError(t *testing.T) {
	srv := pub_models.McpServer{
		Name:    "hang",
		Command: "go",
		Args:    []string{"run", "./testserver"},
		Env:     map[string]string{"TEST_SERVER_AUTH_HANG": "1"},
	}
	c := NewConnector(t.Context(), srv, nil, WithConnectBound(100*time.Millisecond))

	start := time.Now()
	_, err := c.Conn(t.Context())
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected a connect-bound expiry error, got nil")
	}
	var startupErr *claierr.McpServerStartupError
	if !errors.As(err, &startupErr) {
		t.Fatalf("err = %v, want *claierr.McpServerStartupError", err)
	}
	if startupErr.Stage != "connect" {
		t.Errorf("Stage = %q, want %q", startupErr.Stage, "connect")
	}
	if elapsed > 3*time.Second {
		t.Fatalf("the connect bound did not fire promptly: elapsed %v", elapsed)
	}
}

// TestConnectorCloseReleasesProcessOnRunEnd pins that cancelling the run
// context tears the resolved connection down: a later call surfaces the
// typed closed error instead of hanging.
func TestConnectorCloseReleasesProcessOnRunEnd(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	srv := pub_models.McpServer{Name: "echo", Command: "go", Args: []string{"run", "./testserver"}}
	c := NewConnector(ctx, srv, nil)

	conn, err := c.Conn(ctx)
	if err != nil {
		t.Fatalf("Conn: %v", err)
	}
	if _, err := conn.Call(ctx, "tools/list", nil); err != nil {
		t.Fatalf("tools/list before cancel: %v", err)
	}

	cancel()

	deadline := time.Now().Add(5 * time.Second)
	for {
		_, err := conn.Call(context.Background(), "tools/list", nil)
		if errors.Is(err, claierr.ErrMcpConnClosed) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("connection never closed after the run ended, last err: %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestConnectorCallAfterRunEndIsClosedError pins the error-coverage row: a
// tool call arriving after run end returns the typed closed error as its
// tool result, with no new spawn (the connector stays memoised).
func TestConnectorCallAfterRunEndIsClosedError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	srv := pub_models.McpServer{Name: "echo", Command: "go", Args: []string{"run", "./testserver"}}
	c := NewConnector(ctx, srv, nil)
	if _, err := c.Conn(ctx); err != nil {
		t.Fatalf("Conn: %v", err)
	}
	mt := &mcpTool{remoteName: "echo", connector: c}

	cancel()

	deadline := time.Now().Add(5 * time.Second)
	for {
		_, err := mt.CallWithContext(context.Background(), pub_models.Input{"text": "hi"})
		if errors.Is(err, claierr.ErrMcpConnClosed) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("tool call after run end never returned the closed error, last err: %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestAbandonedWaitDoesNotCancelResolution pins that resolution runs under
// the run context, never the triggering call's: a caller whose own ctx
// expires abandons its wait without cancelling the resolution, and a later
// call in the same run reuses its outcome with no second spawn.
func TestAbandonedWaitDoesNotCancelResolution(t *testing.T) {
	var calls int32
	fc := &fakeConn{raw: json.RawMessage(`"ok"`)}
	release := make(chan struct{})
	c := newConnector(t.Context(), func(context.Context) (Conn, error) {
		atomic.AddInt32(&calls, 1)
		<-release
		return fc, nil
	})

	abandonCtx, abandonCancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer abandonCancel()
	if _, err := c.Conn(abandonCtx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("abandoned wait err = %v, want DeadlineExceeded", err)
	}

	close(release)

	conn, err := c.Conn(context.Background())
	if err != nil {
		t.Fatalf("later call after the resolution completed: %v", err)
	}
	if conn != fc {
		t.Fatal("later call did not reuse the completed resolution")
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("dial called %d times, want 1 (the abandoned wait must not trigger a second spawn)", got)
	}
}

// TestConnectorCancelDuringResolutionReapsProcess pins that cancelling the
// run context while a resolution is in flight returns a cancellation error
// promptly, and that the teardown is recognised as normal (no crash report)
// rather than leaving the attempt hanging.
func TestConnectorCancelDuringResolutionReapsProcess(t *testing.T) {
	runCtx, cancel := context.WithCancel(context.Background())
	sink := &recordingSink{}
	srv := pub_models.McpServer{
		Name:    "hang",
		Command: "go",
		Args:    []string{"run", "./testserver"},
		Env:     map[string]string{"TEST_SERVER_AUTH_HANG": "1"},
	}
	c := NewConnector(runCtx, srv, sink, WithConnectBound(10*time.Second))

	done := make(chan struct{})
	var resolveErr error
	go func() {
		_, resolveErr = c.Conn(runCtx)
		close(done)
	}()

	deadline := time.Now().Add(5 * time.Second)
	for {
		lines, _ := sink.snapshot()
		if len(lines) > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the fake server never printed its auth prompt before the deadline")
		}
		time.Sleep(10 * time.Millisecond)
	}

	cancel()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("resolution never returned after the run context was cancelled")
	}
	if resolveErr == nil {
		t.Fatal("expected a cancellation error, got nil")
	}
	if !errors.Is(resolveErr, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", resolveErr)
	}
	_, exited := sink.snapshot()
	if len(exited) != 0 {
		t.Errorf("ServerExited fired on a run-context cancellation: %v", exited)
	}
}

// TestAuthChallengeIsNotMemoisedAsRunFailure pins the generic half of D20: a
// resolution failing with claierr.AuthChallengeError is the mirror of
// TestBlockedResolutionIsNotMemoisedAsFailure using the real production
// type rather than a hand-built fake, proving BlockedOutsideRun's addition
// to AuthChallengeError (pkg/claierr) is what the connector's existing
// single-flight mechanism (shipped in phase 2, unmodified by phase 6) reads.
// A later call is therefore free to retry instead of replaying a cached
// failure.
func TestAuthChallengeIsNotMemoisedAsRunFailure(t *testing.T) {
	var calls int32
	fc := &fakeConn{raw: json.RawMessage(`"ok"`)}
	c := newConnector(t.Context(), func(context.Context) (Conn, error) {
		if atomic.AddInt32(&calls, 1) == 1 {
			return nil, claierr.NewAuthChallenge("fx", `Bearer realm="OAuth"`, "")
		}
		return fc, nil
	})

	_, err := c.Conn(t.Context())
	var challenge *claierr.AuthChallengeError
	if !errors.As(err, &challenge) {
		t.Fatalf("first call err = %v, want *claierr.AuthChallengeError", err)
	}

	conn, err := c.Conn(t.Context())
	if err != nil {
		t.Fatalf("second call after a challenged resolution: %v", err)
	}
	if conn != fc {
		t.Fatal("second call did not resolve a connection")
	}
	if got := atomic.LoadInt32(&calls); got != 2 {
		t.Fatalf("dial called %d times, want 2 (a challenge must not consume the retry budget)", got)
	}
}
