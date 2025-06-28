package mcp

import (
	"context"
	"sync"
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

	orig := tools.Registry
	tools.Registry = tools.NewRegistry()
	defer func() { tools.Registry = orig }()

	ev := ControlEvent{ServerName: "echo", Server: srv, InputChan: in, OutputChan: out}
	readyChan := make(chan struct{}, 1)
	if serveErr := handleServer(ctx, ev, readyChan); serveErr != nil {
		t.Fatalf("handleServer: %v", serveErr)
	}

	tool, ok := tools.Registry.Get("mcp_echo_echo")
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

	orig := tools.Registry
	tools.Registry = tools.NewRegistry()
	defer func() { tools.Registry = orig }()

	controlCh := make(chan ControlEvent)
	statusCh := make(chan error, 1)
	var wg sync.WaitGroup
	wg.Add(1)
	go Manager(ctx, controlCh, statusCh, &wg)

	controlCh <- ControlEvent{ServerName: "echo", Server: srv, InputChan: in, OutputChan: out}

	var ok bool
	for i := 0; i < 20; i++ {
		_, ok = tools.Registry.Get("mcp_echo_echo")
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
	if err := <-statusCh; err != nil {
		t.Fatalf("manager error: %v", err)
	}
}
