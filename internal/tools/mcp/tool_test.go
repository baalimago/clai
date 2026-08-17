package mcp

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/baalimago/clai/internal/tools"
	pub_models "github.com/baalimago/clai/pkg/text/models"
)

// startTestServer boots the testserver process and returns its request and
// response channels. t.Context() tears the process down at test end.
func startTestServer(t *testing.T) (chan<- any, <-chan any) {
	t.Helper()
	srv := pub_models.McpServer{Command: "go", Args: []string{"run", "./testserver"}}
	in, out, err := Client(t.Context(), srv, nil)
	if err != nil {
		t.Fatalf("client: %v", err)
	}
	return in, out
}

// registerTestTools runs handleServer against a fresh registry and returns the
// registered *mcpTool for the named remote tool.
func registerTestTools(t *testing.T, srv pub_models.McpServer, remoteTool string) *mcpTool {
	t.Helper()
	reg := tools.NewRegistry()

	in, out, err := Client(t.Context(), srv, nil)
	if err != nil {
		t.Fatalf("client: %v", err)
	}
	ev := ControlEvent{ServerName: "echo", Server: srv, InputChan: in, OutputChan: out}
	readyChan := make(chan struct{}, 1)
	if serveErr := handleServer(t.Context(), ev, readyChan, reg); serveErr != nil {
		t.Fatalf("handleServer: %v", serveErr)
	}
	tool, ok := reg.Get("mcp_echo_" + remoteTool)
	if !ok {
		t.Fatalf("tool mcp_echo_%s not registered", remoteTool)
	}
	return tool.(*mcpTool)
}

// TestMcpTool_CallWithContext_PerCallTimeout pins the core fix: a tool call
// against a server that never answers must fail once the mcpTool's own timeout
// expires, even when the caller's context has a much longer deadline. Before
// the timeout existed, the call waited for the caller deadline (5s here) and
// the elapsed assertion failed.
func TestMcpTool_CallWithContext_PerCallTimeout(t *testing.T) {
	in, out := startTestServer(t)
	mt := &mcpTool{
		remoteName: "hang",
		inputChan:  in,
		outputChan: out,
		timeout:    100 * time.Millisecond,
	}

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
	if !strings.Contains(err.Error(), "cancelled while waiting") {
		t.Fatalf("expected receive-loop cancellation error, got: %v", err)
	}
}

// TestMcpTool_CallWithContext_ZeroTimeoutHonorsCallerDeadline pins that a zero
// timeout leaves the bound to the caller: the caller's own deadline still
// interrupts the wait through the receive loop's ctx.Done case.
func TestMcpTool_CallWithContext_ZeroTimeoutHonorsCallerDeadline(t *testing.T) {
	in, out := startTestServer(t)
	mt := &mcpTool{
		remoteName: "hang",
		inputChan:  in,
		outputChan: out,
	}

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

// TestMcpTool_CallWithContext_CancelWhileWaiting pins the receive-loop
// ctx.Done case: a caller cancellation while a response is pending must abort
// the call instead of waiting forever.
func TestMcpTool_CallWithContext_CancelWhileWaiting(t *testing.T) {
	in, out := startTestServer(t)
	mt := &mcpTool{
		remoteName: "hang",
		inputChan:  in,
		outputChan: out,
	}

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
	in, out := startTestServer(t)
	mt := &mcpTool{
		remoteName: "echo",
		inputChan:  in,
		outputChan: out,
		timeout:    time.Second,
	}

	res, err := mt.CallWithContext(t.Context(), pub_models.Input{"text": "hello"})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if res != "hello" {
		t.Errorf("unexpected response %q", res)
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
