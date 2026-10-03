package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/baalimago/clai/internal/tools"
	pub_models "github.com/baalimago/clai/pkg/text/models"
)

// fakeConn is a minimal Conn double for unit-testing mcpTool in isolation
// from any transport.
type fakeConn struct {
	raw json.RawMessage
	err error
}

func (f *fakeConn) Call(_ context.Context, _ string, _ map[string]any) (json.RawMessage, error) {
	return f.raw, f.err
}

func (f *fakeConn) Notify(context.Context, string, map[string]any) error { return nil }

func (f *fakeConn) Close() error { return nil }

// startTestServerConn boots the testserver process and returns a connected
// Conn. t.Context() tears the process down at test end via its ctx.
func startTestServerConn(t *testing.T) Conn {
	t.Helper()
	srv := pub_models.McpServer{Command: "go", Args: []string{"run", "./testserver"}}
	conn, err := NewStdioConn(t.Context(), srv, nil)
	if err != nil {
		t.Fatalf("NewStdioConn: %v", err)
	}
	return conn
}

// registerTestTools runs handleServer against a fresh registry and returns the
// registered *mcpTool for the named remote tool.
func registerTestTools(t *testing.T, srv pub_models.McpServer, remoteTool string) *mcpTool {
	t.Helper()
	reg := tools.NewRegistry()

	conn, err := NewStdioConn(t.Context(), srv, nil)
	if err != nil {
		t.Fatalf("NewStdioConn: %v", err)
	}
	ev := ControlEvent{ServerName: "echo", Server: srv, Conn: conn}
	if serveErr := handleServer(t.Context(), ev, reg); serveErr != nil {
		t.Fatalf("handleServer: %v", serveErr)
	}
	tool, ok := reg.Get("mcp_echo_" + remoteTool)
	if !ok {
		t.Fatalf("tool mcp_echo_%s not registered", remoteTool)
	}
	return tool.(*mcpTool)
}

// TestMcpTool_CallWithContext_PerCallTimeout pins the core fix: a tool call
// against a server that never answers must fail once the mcpTool's own
// timeout expires, even when the caller's context has a much longer
// deadline.
func TestMcpTool_CallWithContext_PerCallTimeout(t *testing.T) {
	conn := startTestServerConn(t)
	mt := &mcpTool{remoteName: "hang", connector: resolvedConnector{conn}, timeout: 100 * time.Millisecond}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	start := time.Now()
	_, err := mt.CallWithContext(ctx, pub_models.Input{})
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected error from hung server, got nil")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected DeadlineExceeded, got: %v", err)
	}
	if elapsed > 2*time.Second {
		t.Fatalf("per-call timeout did not fire promptly: elapsed %v", elapsed)
	}
}

// TestMcpTool_CallWithContext_ZeroTimeoutHonorsCallerDeadline pins that a zero
// timeout leaves the bound to the caller.
func TestMcpTool_CallWithContext_ZeroTimeoutHonorsCallerDeadline(t *testing.T) {
	conn := startTestServerConn(t)
	mt := &mcpTool{remoteName: "hang", connector: resolvedConnector{conn}}

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err := mt.CallWithContext(ctx, pub_models.Input{})
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected error from hung server, got nil")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected DeadlineExceeded from caller ctx, got: %v", err)
	}
	if elapsed > 2*time.Second {
		t.Fatalf("caller deadline did not interrupt the wait: elapsed %v", elapsed)
	}
}

// TestMcpTool_CallWithContext_CancelWhileWaiting pins cancellation while a
// response is pending.
func TestMcpTool_CallWithContext_CancelWhileWaiting(t *testing.T) {
	conn := startTestServerConn(t)
	mt := &mcpTool{remoteName: "hang", connector: resolvedConnector{conn}}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		time.Sleep(100 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	_, err := mt.CallWithContext(ctx, pub_models.Input{})
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected error after cancellation, got nil")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got: %v", err)
	}
	if elapsed > 2*time.Second {
		t.Fatalf("cancellation did not interrupt the wait: elapsed %v", elapsed)
	}
}

// TestMcpTool_CallWithContext_SuccessWithTimeout pins that an active timeout
// does not disturb a healthy round trip.
func TestMcpTool_CallWithContext_SuccessWithTimeout(t *testing.T) {
	conn := startTestServerConn(t)
	mt := &mcpTool{remoteName: "echo", connector: resolvedConnector{conn}, timeout: time.Second}

	res, err := mt.CallWithContext(t.Context(), pub_models.Input{"text": "hello"})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if res != "hello" {
		t.Errorf("unexpected response %q", res)
	}
}

// Test_McpTool_ConnCallFailure_PropagatesError pins that an error from the
// connection is wrapped and returned, naming the tool.
func Test_McpTool_ConnCallFailure_PropagatesError(t *testing.T) {
	mt := &mcpTool{remoteName: "echo", connector: resolvedConnector{&fakeConn{err: errors.New("boom")}}}
	_, err := mt.CallWithContext(t.Context(), pub_models.Input{"text": "hello"})
	if err == nil {
		t.Fatal("expected an error, got nil")
	}
	if !strings.Contains(err.Error(), "boom") || !strings.Contains(err.Error(), "echo") {
		t.Fatalf("err = %v, want it to name the tool and the cause", err)
	}
}

// Test_McpTool_DecodeResultFailure_ErrorResult pins that a result payload
// that cannot be decoded returns an error naming the tool, rather than an
// empty success.
func Test_McpTool_DecodeResultFailure_ErrorResult(t *testing.T) {
	mt := &mcpTool{remoteName: "echo", connector: resolvedConnector{&fakeConn{raw: json.RawMessage(`not json`)}}}
	_, err := mt.CallWithContext(t.Context(), pub_models.Input{"text": "hello"})
	if err == nil {
		t.Fatal("expected an error result, got nil")
	}
	if !strings.Contains(err.Error(), "decode result") {
		t.Errorf("err = %v, want it to name the decode failure", err)
	}
	if !strings.Contains(err.Error(), "echo") {
		t.Errorf("err = %v, want it to name the tool", err)
	}
}

// TestHandleServer_CarriesMcpServerTimeout pins the wiring: the per-server
// TimeoutSeconds config must reach the registered mcpTool.
func TestHandleServer_CarriesMcpServerTimeout(t *testing.T) {
	srv := pub_models.McpServer{
		Command:        "go",
		Args:           []string{"run", "./testserver"},
		TimeoutSeconds: 7,
	}
	mt := registerTestTools(t, srv, "echo")
	if mt.timeout != 7*time.Second {
		t.Fatalf("timeout = %v, want 7s", mt.timeout)
	}
}
