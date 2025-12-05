package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

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

func TestStartHttpClientStreamable(t *testing.T) {
	// 1. Setup Mock Server (Streamable HTTP: Single Endpoint)
	messageChan := make(chan string, 10)
	postChan := make(chan []byte, 10)

	mux := http.NewServeMux()
	mux.HandleFunc("/mcp", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" {
			// Check header
			if r.Header.Get("MCP-Protocol-Version") != "2025-06-18" {
				// Don't fail hard here, as earlier check might be missing for GET?
				// Spec says "client MUST include ... on all subsequent requests".
			}

			w.Header().Set("Content-Type", "text/event-stream")
			w.Header().Set("Cache-Control", "no-cache")
			w.Header().Set("Connection", "keep-alive")
			w.WriteHeader(http.StatusOK)

			flusher, ok := w.(http.Flusher)
			if !ok {
				http.Error(w, "Streaming unsupported!", http.StatusInternalServerError)
				return
			}
			flusher.Flush()

			// NO "endpoint" event sent!

			// Listen for messages to send to client
			ctx := r.Context()
			for {
				select {
				case msg := <-messageChan:
					fmt.Fprintf(w, "event: message\n")
					fmt.Fprintf(w, "data: %s\n\n", msg)
					flusher.Flush()
				case <-ctx.Done():
					return
				}
			}
		} else if r.Method == "POST" {
			if r.Header.Get("MCP-Protocol-Version") != "2025-06-18" {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			if r.Header.Get("Authorization") != "Bearer test-token" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}

			var body json.RawMessage
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			postChan <- body
			w.WriteHeader(http.StatusOK)
		} else {
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	})

	server := httptest.NewServer(mux)
	defer server.Close()

	// 2. Start Client
	mcpConfig := pub_models.McpServer{
		Name: "test_http",
		Url:  server.URL + "/mcp",
		Env: map[string]string{
			"Authorization": "Bearer test-token",
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	in, out, err := startHttpClient(ctx, mcpConfig)
	if err != nil {
		t.Fatalf("startHttpClient failed: %v", err)
	}

	// 3. Test sending request (POST)
	reqMsg := map[string]any{"method": "test"}
	// Wait a bit for connection to establish (though not strictly needed with Streamable HTTP)
	time.Sleep(100 * time.Millisecond)

	in <- reqMsg

	select {
	case receivedPost := <-postChan:
		var receivedMap map[string]any
		json.Unmarshal(receivedPost, &receivedMap)
		if receivedMap["method"] != "test" {
			t.Errorf("expected method 'test', got %v", receivedMap["method"])
		}
	case <-time.After(2 * time.Second):
		t.Error("timeout waiting for POST")
	}

	// 4. Test receiving response (SSE)
	respMsg := `{"result": "success"}`
	messageChan <- respMsg

	select {
	case receivedOut := <-out:
		raw, ok := receivedOut.(json.RawMessage)
		if !ok {
			t.Fatalf("Expected json.RawMessage, got %T", receivedOut)
		}
		var receivedMap map[string]any
		json.Unmarshal(raw, &receivedMap)
		if receivedMap["result"] != "success" {
			t.Errorf("expected result 'success', got %v", receivedMap["result"])
		}
	case <-time.After(2 * time.Second):
		t.Error("timeout waiting for SSE message")
	}
}

func TestStartHttpClientLegacy(t *testing.T) {
	// 1. Setup Mock Server (Legacy: Endpoint event)
	messageChan := make(chan string, 10)
	postChan := make(chan []byte, 10)

	mux := http.NewServeMux()
	mux.HandleFunc("/sse", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.WriteHeader(http.StatusOK)

		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "Streaming unsupported!", http.StatusInternalServerError)
			return
		}

		// Send endpoint event
		fmt.Fprintf(w, "event: endpoint\n")
		// Relative path
		fmt.Fprintf(w, "data: /message\n\n")
		flusher.Flush()

		ctx := r.Context()
		for {
			select {
			case msg := <-messageChan:
				fmt.Fprintf(w, "event: message\n")
				fmt.Fprintf(w, "data: %s\n\n", msg)
				flusher.Flush()
			case <-ctx.Done():
				return
			}
		}
	})

	mux.HandleFunc("/message", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		postChan <- []byte("{}")
		w.WriteHeader(http.StatusOK)
	})

	server := httptest.NewServer(mux)
	defer server.Close()

	// 2. Start Client
	mcpConfig := pub_models.McpServer{
		Name: "test_http_legacy",
		Url:  server.URL + "/sse",
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	in, _, err := startHttpClient(ctx, mcpConfig)
	if err != nil {
		t.Fatalf("startHttpClient failed: %v", err)
	}

	// 3. Test sending request (POST) - ensuring it uses the endpoint from event
	time.Sleep(100 * time.Millisecond) // Wait for endpoint event
	in <- map[string]any{"method": "test"}

	select {
	case <-postChan:
		// Success
	case <-time.After(2 * time.Second):
		t.Error("timeout waiting for POST to legacy endpoint")
	}
}
