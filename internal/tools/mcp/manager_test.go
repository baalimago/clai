package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/baalimago/clai/internal/tools"
	"github.com/baalimago/clai/pkg/claierr"
	pub_models "github.com/baalimago/clai/pkg/text/models"
)

func TestManagerRegistersToolsThroughConn(t *testing.T) {
	ctx := t.Context()

	srv := pub_models.McpServer{Command: "go", Args: []string{"run", "./testserver"}}
	conn, err := NewStdioConn(ctx, srv, nil)
	if err != nil {
		t.Fatalf("NewStdioConn: %v", err)
	}

	reg := tools.NewRegistry()

	ev := ControlEvent{ServerName: "echo", Server: srv, Conn: conn}
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

// TestConnHandshakeTimeoutReturnsTypedError migrates the retired
// ControlEvent.StartupTimeout override: the handshake bound now lives on
// the connection, set here through WithHandshakeBound. It also pins the
// acceptance criterion that expiry surfaces a typed startup error.
func TestConnHandshakeTimeoutReturnsTypedError(t *testing.T) {
	srv := pub_models.McpServer{
		Command: "go",
		Args:    []string{"run", "./testserver"},
		Env:     map[string]string{"TEST_SERVER_AUTH_HANG": "1"},
	}
	conn, err := NewStdioConn(t.Context(), srv, nil, WithHandshakeBound(50*time.Millisecond))
	if err != nil {
		t.Fatalf("NewStdioConn: %v", err)
	}
	t.Cleanup(func() { conn.Close() })

	reg := tools.NewRegistry()
	ev := ControlEvent{ServerName: "oauth", Server: srv, Conn: conn}

	start := time.Now()
	serveErr := handleServer(t.Context(), ev, reg)
	elapsed := time.Since(start)

	if serveErr == nil {
		t.Fatal("handleServer returned nil for a hung server, want a timeout error")
	}
	if !errors.Is(serveErr, context.DeadlineExceeded) {
		t.Fatalf("expected DeadlineExceeded, got: %v", serveErr)
	}
	var startupErr *claierr.McpServerStartupError
	if !errors.As(serveErr, &startupErr) {
		t.Fatalf("err = %v, want *claierr.McpServerStartupError", serveErr)
	}
	if startupErr.Stage != "initialize" {
		t.Errorf("Stage = %q, want %q", startupErr.Stage, "initialize")
	}
	if elapsed > 2*time.Second {
		t.Fatalf("timeout did not fire promptly: elapsed %v", elapsed)
	}
	if _, ok := reg.Get("mcp_oauth_echo"); ok {
		t.Fatal("hung server registered tools")
	}
}

// TestConnInitializeRpcErrorIsTypedStartupError pins that a JSON-RPC error
// answer to initialize surfaces as a typed startup error naming that stage.
func TestConnInitializeRpcErrorIsTypedStartupError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	c, dec, w := newPipeConn(t, ctx, nil)
	enc := json.NewEncoder(w)

	go func() {
		var r Request
		dec.Decode(&r)
		enc.Encode(Response{JSONRPC: "2.0", ID: r.ID, Error: &RPCError{Code: -32000, Message: "boom"}})
	}()

	reg := tools.NewRegistry()
	ev := ControlEvent{ServerName: "svc", Server: pub_models.McpServer{Name: "svc"}, Conn: c}
	serveErr := handleServer(ctx, ev, reg)
	if serveErr == nil {
		t.Fatal("expected an error")
	}
	var startupErr *claierr.McpServerStartupError
	if !errors.As(serveErr, &startupErr) {
		t.Fatalf("err = %v, want *claierr.McpServerStartupError", serveErr)
	}
	if startupErr.Stage != "initialize" {
		t.Errorf("Stage = %q, want %q", startupErr.Stage, "initialize")
	}
}

// TestConnToolsListUndecodableResultIsTypedStartupError pins that a
// tools/list result that cannot be decoded surfaces as a typed startup
// error naming that stage.
func TestConnToolsListUndecodableResultIsTypedStartupError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	c, dec, w := newPipeConn(t, ctx, nil)
	enc := json.NewEncoder(w)

	go func() {
		var initReq Request
		dec.Decode(&initReq)
		enc.Encode(Response{JSONRPC: "2.0", ID: initReq.ID, Result: json.RawMessage(`{}`)})

		var notif Request
		dec.Decode(&notif) // notifications/initialized: no id, consumed and ignored

		var listReq Request
		dec.Decode(&listReq)
		enc.Encode(Response{JSONRPC: "2.0", ID: listReq.ID, Result: json.RawMessage(`"not-an-object"`)})
	}()

	reg := tools.NewRegistry()
	ev := ControlEvent{ServerName: "svc", Server: pub_models.McpServer{Name: "svc"}, Conn: c}
	serveErr := handleServer(ctx, ev, reg)
	if serveErr == nil {
		t.Fatal("expected an error")
	}
	var startupErr *claierr.McpServerStartupError
	if !errors.As(serveErr, &startupErr) {
		t.Fatalf("err = %v, want *claierr.McpServerStartupError", serveErr)
	}
	if startupErr.Stage != "tools/list" {
		t.Errorf("Stage = %q, want %q", startupErr.Stage, "tools/list")
	}
}

// TestConnAdvertisesProtocolVersion pins that handleServer advertises the
// current protocol-version parameter rather than the retired live value: the
// fake server rejects any other version, so a successful handshake proves
// the right one was sent.
func TestConnAdvertisesProtocolVersion(t *testing.T) {
	srv := pub_models.McpServer{Name: "echo", Command: "go", Args: []string{"run", "./testserver"}}
	conn, err := NewStdioConn(t.Context(), srv, nil)
	if err != nil {
		t.Fatalf("NewStdioConn: %v", err)
	}
	t.Cleanup(func() { conn.Close() })

	reg := tools.NewRegistry()
	ev := ControlEvent{ServerName: "echo", Server: srv, Conn: conn}
	if serveErr := handleServer(t.Context(), ev, reg); serveErr != nil {
		t.Fatalf("handleServer with the current protocol version: %v", serveErr)
	}

	conn2, err := NewStdioConn(t.Context(), srv, nil)
	if err != nil {
		t.Fatalf("NewStdioConn: %v", err)
	}
	t.Cleanup(func() { conn2.Close() })
	if _, err := conn2.Call(t.Context(), "initialize", map[string]any{"protocolVersion": "2025-03-26"}); err == nil {
		t.Fatal("expected the fake server to reject the retired protocol version")
	}
}

func TestManager(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	srv := pub_models.McpServer{Command: "go", Args: []string{"run", "./testserver"}}
	conn, err := NewStdioConn(ctx, srv, nil)
	if err != nil {
		t.Fatalf("NewStdioConn: %v", err)
	}

	reg := tools.NewRegistry()

	controlCh := make(chan ControlEvent)
	var wg sync.WaitGroup
	wg.Add(1)
	go Manager(ctx, controlCh, &wg, reg, nil)

	controlCh <- ControlEvent{ServerName: "echo", Server: srv, Conn: conn}

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
	ctx := t.Context()

	goodSrv := pub_models.McpServer{Command: "go", Args: []string{"run", "./testserver"}}
	goodConn, err := NewStdioConn(ctx, goodSrv, nil)
	if err != nil {
		t.Fatalf("good conn: %v", err)
	}
	brokenSrv := pub_models.McpServer{
		Name:    "broken",
		Command: "go",
		Args:    []string{"run", "./testserver"},
		Env:     map[string]string{"TEST_SERVER_EXIT": "1"},
	}
	brokenConn, err := NewStdioConn(ctx, brokenSrv, nil)
	if err != nil {
		t.Fatalf("broken conn: %v", err)
	}

	reg := tools.NewRegistry()
	controlCh := make(chan ControlEvent)
	var wg sync.WaitGroup
	wg.Add(2)
	go Manager(ctx, controlCh, &wg, reg, nil)

	controlCh <- ControlEvent{ServerName: "broken", Server: brokenSrv, Conn: brokenConn}
	controlCh <- ControlEvent{ServerName: "echo", Server: goodSrv, Conn: goodConn}

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
