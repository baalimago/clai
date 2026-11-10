// Package stubllm is a scripted OpenAI-shaped chat-completions provider for
// tests that need a model to answer with a tool call of the test's choosing.
package stubllm

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// Answer is one assistant turn: a call of Tool with Arguments (a JSON object)
// when Tool is set, the reply Text otherwise.
type Answer struct {
	Tool      string
	Arguments string
	Text      string
}

// Script chooses the answer to one request body.
type Script func(body string) Answer

// Provider is a running stub and the request bodies it has received.
type Provider struct {
	url string

	mu       sync.Mutex
	requests []string
}

// Start serves script until the test ends.
func Start(t testing.TB, script Script) *Provider {
	t.Helper()
	p := &Provider{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		body := string(raw)
		p.mu.Lock()
		p.requests = append(p.requests, body)
		p.mu.Unlock()
		answer := script(body)
		w.Header().Set("Content-Type", "text/event-stream")
		if answer.Tool == "" {
			fmt.Fprintf(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":%q}}]}\n\ndata: [DONE]\n\n", answer.Text)
			return
		}
		args, _ := json.Marshal(answer.Arguments)
		fmt.Fprintf(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_1\",\"type\":\"function\",\"function\":{\"name\":%q,\"arguments\":%s}}]}}]}\n\n", answer.Tool, args)
		fmt.Fprint(w, "data: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"tool_calls\"}]}\n\ndata: [DONE]\n\n")
	}))
	t.Cleanup(server.Close)
	p.url = server.URL
	return p
}

// WriteModel points model, whose name must start with "gpt-", at the stub
// for runs using confDir, and sets the dummy key the vendor requires.
func (p *Provider) WriteModel(t testing.TB, confDir, model string) {
	t.Helper()
	if setter, ok := t.(interface{ Setenv(key, value string) }); ok {
		setter.Setenv("OPENAI_API_KEY", "stub-key")
	}
	conf, err := json.Marshal(map[string]any{"model": model, "url": p.url + "/v1/chat/completions"})
	if err != nil {
		t.Fatalf("marshal model config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(confDir, "openai_gpt_"+model+".json"), conf, 0o644); err != nil {
		t.Fatalf("write model config: %v", err)
	}
}

// Requests returns the request bodies received so far, oldest first.
func (p *Provider) Requests() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.requests...)
}

// OfferedTools returns the tool names request body offers the model.
func OfferedTools(t testing.TB, body string) []string {
	t.Helper()
	var req struct {
		Tools []struct {
			Function struct {
				Name string `json:"name"`
			} `json:"function"`
		} `json:"tools"`
	}
	if err := json.Unmarshal([]byte(body), &req); err != nil {
		t.Fatalf("request body is not JSON: %v: %q", err, body)
	}
	names := make([]string, 0, len(req.Tools))
	for _, tool := range req.Tools {
		names = append(names, tool.Function.Name)
	}
	return names
}

// HasToolResult reports whether body carries a tool result, which is how a
// script tells a first request from the one after its tool call.
func HasToolResult(body string) bool {
	return strings.Contains(body, `"role":"tool"`)
}

// TouchCall is a call of tool whose command creates marker, the side effect
// a test asserts on.
func TouchCall(tool, marker string) Answer {
	args, _ := json.Marshal(map[string]string{"command": "touch " + marker})
	return Answer{Tool: tool, Arguments: string(args)}
}
