package mcp

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	pub_models "github.com/baalimago/clai/pkg/text/models"
)

// recordingSink collects stderr lines and exit notifications for assertions.
type recordingSink struct {
	mu     sync.Mutex
	lines  []string
	exited []string
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

func (r *recordingSink) snapshot() (lines []string, exited []string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.lines...), append([]string(nil), r.exited...)
}

func TestClient(t *testing.T) {
	ctx := t.Context()

	srv := pub_models.McpServer{Command: "go", Args: []string{"run", "./testserver"}}
	in, out, err := Client(ctx, srv, nil)
	if err != nil {
		t.Fatalf("client: %v", err)
	}

	req := Request{JSONRPC: "2.0", ID: 1, Method: "initialize"}
	in <- req
	msg := <-out
	raw, ok := msg.(json.RawMessage)
	if !ok {
		t.Fatalf("unexpected type %T", msg)
	}
	var resp Response
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.ID != 1 || resp.Error != nil {
		t.Errorf("unexpected response: %+v", resp)
	}
}

func TestClientBadCommand(t *testing.T) {
	ctx := t.Context()
	_, _, err := Client(ctx, pub_models.McpServer{Command: "does-not-exist"}, nil)
	if err == nil {
		t.Fatal("expected error for bad command")
	}
}

func TestClient_StderrLinesReachSink(t *testing.T) {
	ctx := t.Context()
	sink := &recordingSink{}
	srv := pub_models.McpServer{
		Command: "go",
		Args:    []string{"run", "./testserver"},
		Env:     map[string]string{"TEST_SERVER_STDERR": "1"},
	}
	in, out, err := Client(ctx, srv, sink)
	if err != nil {
		t.Fatalf("client: %v", err)
	}

	// One initialize round trip gives the server time to write its stderr lines.
	req := Request{JSONRPC: "2.0", ID: 1, Method: "initialize"}
	in <- req
	if msg := <-out; msg == nil {
		t.Fatal("no response from test server")
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

func TestClient_ServerExitNotifiesSink(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	sink := &recordingSink{}
	srv := pub_models.McpServer{
		Command: "go",
		Args:    []string{"run", "./testserver"},
		Env:     map[string]string{"TEST_SERVER_STDERR": "1"},
	}
	in, _, err := Client(ctx, srv, sink)
	if err != nil {
		t.Fatalf("client: %v", err)
	}
	req := Request{JSONRPC: "2.0", ID: 1, Method: "initialize"}
	in <- req
	// The test server exits when stdin closes. Cancel the context, which closes
	// stdin; the stderr pipe then reaches EOF. Because the context is already
	// cancelled, the exit notification must NOT fire (normal teardown).
	cancel()
	time.Sleep(100 * time.Millisecond)
	_, exited := sink.snapshot()
	if len(exited) != 0 {
		t.Errorf("ServerExited fired on normal teardown: %v", exited)
	}
}

func TestClient_ServerCrashNotifiesSink(t *testing.T) {
	ctx := t.Context()
	sink := &recordingSink{}
	srv := pub_models.McpServer{
		Command: "go",
		Args:    []string{"run", "./testserver"},
		Env:     map[string]string{"TEST_SERVER_STDERR": "1", "TEST_SERVER_EXIT": "1"},
	}
	_, _, err := Client(ctx, srv, sink)
	if err != nil {
		t.Fatalf("client: %v", err)
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

func TestClient_StderrCloseWithoutExitNoServerExited(t *testing.T) {
	ctx := t.Context()
	sink := &recordingSink{}
	srv := pub_models.McpServer{
		Command: "go",
		Args:    []string{"run", "./testserver"},
		Env:     map[string]string{"TEST_SERVER_CLOSE_STDERR": "1"},
	}
	in, out, err := Client(ctx, srv, sink)
	if err != nil {
		t.Fatalf("client: %v", err)
	}

	// The server closes stderr and keeps serving. One round trip proves the
	// JSON-RPC channel is still alive after the stderr pipe hit EOF.
	req := Request{JSONRPC: "2.0", ID: 1, Method: "initialize"}
	in <- req
	if msg := <-out; msg == nil {
		t.Fatal("no response after stderr was closed")
	}

	// Wait until the stderr line reached the sink, then give the EOF path
	// time to finish so a (buggy) exit report would already have fired.
	deadline := time.Now().Add(5 * time.Second)
	for {
		lines, _ := sink.snapshot()
		if len(lines) >= 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("stderr line never reached sink: %v", lines)
		}
		time.Sleep(10 * time.Millisecond)
	}
	time.Sleep(200 * time.Millisecond)

	// A second round trip confirms the process is still alive and serving.
	listReq := Request{JSONRPC: "2.0", ID: 2, Method: "tools/list"}
	in <- listReq
	if msg := <-out; msg == nil {
		t.Fatal("no response to second request after stderr close")
	}

	_, exited := sink.snapshot()
	if len(exited) != 0 {
		t.Errorf("ServerExited fired although the server is still alive: %v", exited)
	}
}

// TestClient_ServerExitedWithUnconsumedStdout pins that exit detection does
// not depend on the stdout reader: the server writes a valid JSON line to
// stdout that nobody consumes, then a keyword-free crash tail to stderr, then
// exits. The stdout reader blocks delivering the unconsumed line, so the
// crash tail must be flushed via cmd.Wait, not via stdout EOF.
func TestClient_ServerExitedWithUnconsumedStdout(t *testing.T) {
	ctx := t.Context()
	sink := &recordingSink{}
	srv := pub_models.McpServer{
		Command: "go",
		Args:    []string{"run", "./testserver"},
		Env:     map[string]string{"TEST_SERVER_UNCONSUMED_STDOUT": "1"},
	}
	if _, _, err := Client(ctx, srv, sink); err != nil {
		t.Fatalf("client: %v", err)
	}

	deadline := time.Now().Add(5 * time.Second)
	for {
		lines, exited := sink.snapshot()
		if len(exited) > 0 {
			if len(lines) != 1 || lines[0] != "worker stopped" {
				t.Fatalf("crash tail not complete when exit fired: %v", lines)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("ServerExited never fired; lines: %v", lines)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
