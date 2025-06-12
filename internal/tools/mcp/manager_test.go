package mcp

import (
	"context"
	"testing"
	"time"

	"github.com/baalimago/clai/internal/tools"
)

func TestHandleServerRegistersTool(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	srv := tools.McpServer{Command: "go", Args: []string{"run", "./testserver"}}
	in, out, err := Client(ctx, srv)
	if err != nil {
		t.Fatalf("client: %v", err)
	}

	orig := tools.Tools
	tools.Tools = make(map[string]tools.LLMTool)
	defer func() { tools.Tools = orig }()

	ev := ControlEvent{ServerName: "ts", Server: srv, InputChan: in, OutputChan: out}
	if err := handleServer(ctx, ev); err != nil {
		t.Fatalf("handleServer: %v", err)
	}

	tool, ok := tools.Tools["ts_echo"]
	if !ok {
		t.Fatal("tool not registered")
	}
	res, err := tool.Call(tools.Input{"text": "hello"})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if res != "hello" {
		t.Errorf("unexpected response %q", res)
	}

	if _, err := tool.Call(tools.Input{"text": "error"}); err == nil {
		t.Error("expected error on isError=true")
	}
}

func TestManager(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	srv := tools.McpServer{Command: "go", Args: []string{"run", "./testserver"}}
	in, out, err := Client(ctx, srv)
	if err != nil {
		t.Fatalf("client: %v", err)
	}

	orig := tools.Tools
	tools.Tools = make(map[string]tools.LLMTool)
	defer func() { tools.Tools = orig }()

	controlCh := make(chan ControlEvent)
	statusCh := make(chan error, 1)
	go Manager(ctx, controlCh, statusCh)

	controlCh <- ControlEvent{ServerName: "man", Server: srv, InputChan: in, OutputChan: out}

	var ok bool
	for i := 0; i < 20; i++ {
		_, ok = tools.Tools["man_echo"]
		if ok {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !ok {
		t.Fatal("tool not registered")
	}

	cancel()
	if err := <-statusCh; err != nil {
		t.Fatalf("manager error: %v", err)
	}
}
