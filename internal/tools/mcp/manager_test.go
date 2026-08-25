package mcp

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/baalimago/clai/internal/tools"
	pub_models "github.com/baalimago/clai/pkg/text/models"
)

func TestHandleServerRegistersTool(t *testing.T) {
	ctx := t.Context()

	srv := pub_models.McpServer{Command: "go", Args: []string{"run", "./testserver"}}
	in, out, err := Client(ctx, srv, nil)
	if err != nil {
		t.Fatalf("client: %v", err)
	}

	reg := tools.NewRegistry()

	ev := ControlEvent{ServerName: "echo", Server: srv, InputChan: in, OutputChan: out}
	if serveErr := handleServer(ctx, ev, reg); serveErr != nil {
		t.Fatalf("handleServer: %v", serveErr)
	}

	tool, ok := reg.Get("mcp_echo_echo")
	if !ok {
		t.Fatal("tool not registered")
	}
	if _, leaked := tools.Registry.Get("mcp_echo_echo"); leaked {
		t.Fatal("MCP tool leaked into the process-global registry")
	}
	res, err := tool.Call(pub_models.Input{"text": "hello"})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if res != "hello" {
		t.Errorf("unexpected response %q", res)
	}

	if _, err := tool.Call(pub_models.Input{"text": "error"}); err == nil {
		t.Error("expected error on isError=true")
	}
}

func TestSendRequest_ReturnsErrorOnClosedOutput(t *testing.T) {
	ctx := context.Background()
	in := make(chan any, 1)
	out := make(chan any)
	close(out)

	_, err := sendRequest(ctx, in, out, Request{JSONRPC: "2.0", ID: 1, Method: "initialize"})
	if err == nil {
		t.Fatal("expected an error when the output channel is closed")
	}
	if !strings.Contains(err.Error(), "connection closed") {
		t.Fatalf("expected a connection-closed error, got: %v", err)
	}
}

func TestHandleServer_TimesOutHungServer(t *testing.T) {
	srv := pub_models.McpServer{
		Command: "go",
		Args:    []string{"run", "./testserver"},
		Env:     map[string]string{"TEST_SERVER_AUTH_HANG": "1"},
	}
	in, out, err := Client(t.Context(), srv, nil)
	if err != nil {
		t.Fatalf("client: %v", err)
	}

	reg := tools.NewRegistry()
	ev := ControlEvent{
		ServerName:     "oauth",
		Server:         srv,
		InputChan:      in,
		OutputChan:     out,
		StartupTimeout: 50 * time.Millisecond,
	}

	start := time.Now()
	serveErr := handleServer(t.Context(), ev, reg)
	if serveErr == nil {
		t.Fatal("handleServer returned nil for a hung server, want timeout error")
	}
	if !errors.Is(serveErr, context.DeadlineExceeded) {
		t.Fatalf("expected DeadlineExceeded, got: %v", serveErr)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("timeout did not fire promptly: elapsed %v", elapsed)
	}
	if _, ok := reg.Get("mcp_oauth_echo"); ok {
		t.Fatal("hung server registered tools")
	}
}

func TestManager(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	srv := pub_models.McpServer{Command: "go", Args: []string{"run", "./testserver"}}
	in, out, err := Client(ctx, srv, nil)
	if err != nil {
		t.Fatalf("client: %v", err)
	}

	reg := tools.NewRegistry()

	controlCh := make(chan ControlEvent)
	var wg sync.WaitGroup
	wg.Add(1)
	go Manager(ctx, controlCh, &wg, reg)

	controlCh <- ControlEvent{ServerName: "echo", Server: srv, InputChan: in, OutputChan: out}

	var ok bool
	for range 20 {
		_, ok = reg.Get("mcp_echo_echo")
		if ok {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !ok {
		t.Fatal("tool not registered")
	}

	cancel()
	wg.Wait()
}

// TestManager_SkipsFailingServer pins the non-fatal error contract: a server
// whose handshake fails is skipped while a healthy server still registers.
func TestManager_SkipsFailingServer(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	goodSrv := pub_models.McpServer{Command: "go", Args: []string{"run", "./testserver"}}
	goodIn, goodOut, err := Client(ctx, goodSrv, nil)
	if err != nil {
		t.Fatalf("good client: %v", err)
	}
	brokenSrv := pub_models.McpServer{
		Command: "go",
		Args:    []string{"run", "./testserver"},
		Env:     map[string]string{"TEST_SERVER_EXIT": "1"},
	}
	brokenIn, brokenOut, err := Client(ctx, brokenSrv, nil)
	if err != nil {
		t.Fatalf("broken client: %v", err)
	}

	reg := tools.NewRegistry()
	controlCh := make(chan ControlEvent)
	var wg sync.WaitGroup
	wg.Add(2)
	go Manager(ctx, controlCh, &wg, reg)

	controlCh <- ControlEvent{ServerName: "broken", Server: brokenSrv, InputChan: brokenIn, OutputChan: brokenOut}
	controlCh <- ControlEvent{ServerName: "echo", Server: goodSrv, InputChan: goodIn, OutputChan: goodOut}

	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, ok := reg.Get("mcp_echo_echo"); ok {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("good server's tools never registered")
		}
		time.Sleep(10 * time.Millisecond)
	}
	wg.Wait()

	if _, ok := reg.Get("mcp_broken_echo"); ok {
		t.Error("broken server's tools must not be registered")
	}
}

func TestMcpTool_CallWithContext_CancelBeforeSend(t *testing.T) {
	// Use a live context for Client and handleServer
	srvCtx := context.Background()
	srv := pub_models.McpServer{Command: "go", Args: []string{"run", "./testserver"}}
	in, out, err := Client(srvCtx, srv, nil)
	if err != nil {
		t.Fatalf("client: %v", err)
	}

	reg := tools.NewRegistry()

	ev := ControlEvent{ServerName: "echo", Server: srv, InputChan: in, OutputChan: out}
	_ = handleServer(srvCtx, ev, reg)

	tool, ok := reg.Get("mcp_echo_echo")
	if !ok {
		t.Fatal("tool not registered")
	}

	// Now call with an already-cancelled context — should fail immediately on send
	cancelledCtx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = tool.(*mcpTool).CallWithContext(cancelledCtx, pub_models.Input{"text": "hello"})
	if err == nil {
		t.Fatal("expected error for cancelled context, got nil")
	}
	if !strings.Contains(err.Error(), "cancelled") {
		t.Fatalf("expected cancellation error, got: %v", err)
	}
}
