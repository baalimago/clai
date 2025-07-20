package mcp

import (
	"context"
	"encoding/json"
	"testing"

	pub_models "github.com/baalimago/clai/pkg/text/models"
)

func TestClient(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	srv := pub_models.McpServer{Command: "go", Args: []string{"run", "./testserver"}}
	in, out, err := Client(ctx, srv)
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
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_, _, err := Client(ctx, pub_models.McpServer{Command: "does-not-exist"})
	if err == nil {
		t.Fatal("expected error for bad command")
	}
}
