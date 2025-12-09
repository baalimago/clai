package generic

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/baalimago/clai/internal/models"
	"github.com/baalimago/clai/internal/tools"
	pub_models "github.com/baalimago/clai/pkg/text/models"
)

func isNoop(ev any) bool {
	_, ok := ev.(models.NoopEvent)
	return ok
}

// roundTripFunc allows injecting errors in http.Client
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestStreamCompletions_DoError(t *testing.T) {
	s := &StreamCompleter{client: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return nil, errors.New("boom")
	})}, apiKey: "k", URL: "http://example.invalid"}

	ch, err := s.StreamCompletions(context.Background(), pub_models.Chat{Messages: []pub_models.Message{{Role: "user", Content: "x"}}})
	if err == nil || !strings.Contains(err.Error(), "failed to execute request") {
		t.Fatalf("expected execute request error, got: %v, ch=%v", err, ch)
	}
}

func TestStreamCompletions_Non200_And_CleanDoesNotMutateOriginal(t *testing.T) {
	invoked := false
	orig := pub_models.Chat{Messages: []pub_models.Message{{Role: "user", Content: "orig"}}}
	s := &StreamCompleter{apiKey: "k"}
	s.Clean = func(in []pub_models.Message) []pub_models.Message {
		invoked = true
		if len(in) > 0 {
			in[0].Content = "mutated"
		}
		return append(in, pub_models.Message{Role: "system", Content: "added"})
	}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
		_, _ = w.Write([]byte("bad"))
	}))
	defer ts.Close()
	s.client = ts.Client()
	s.URL = ts.URL

	ch, err := s.StreamCompletions(context.Background(), orig)
	if err == nil || !strings.Contains(err.Error(), "unexpected status code") {
		t.Fatalf("expected non-200 error, got: %v, ch=%v", err, ch)
	}
	if !invoked {
		t.Fatalf("expected Clean to be invoked")
	}
	if got := orig.Messages[0].Content; got != "orig" {
		t.Fatalf("original chat mutated, got: %q", got)
	}
}

func TestStreamCompletions_HappyPath_FirstEventOnly(t *testing.T) {
	// SSE-like server emitting a single content chunk and staying open
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		fl, _ := w.(http.Flusher)
		fmt.Fprintf(w, "data: %s\n", `{"choices":[{"delta":{"content":"Hello"}}]}`)
		if fl != nil {
			fl.Flush()
		}
		// Keep connection open to avoid EOF behavior in the reader goroutine
		time.Sleep(50 * time.Millisecond)
	}))
	defer ts.Close()

	s := &StreamCompleter{client: ts.Client(), apiKey: "k", URL: ts.URL}
	ctx := context.Background()
	out, err := s.StreamCompletions(ctx, pub_models.Chat{Messages: []pub_models.Message{{Role: "user", Content: "hi"}}})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}

	select {
	case ev := <-out:
		str, ok := ev.(string)
		if !ok || str != "Hello" {
			t.Fatalf("expected 'Hello' string event, got: %T %v", ev, ev)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for first event")
	}
}

func TestCreateRequest_BodyAndHeaders(t *testing.T) {
	fpen, ppen, temp, top, max := 0.25, 0.75, 0.5, 0.9, 123
	choice := "auto"
	s := &StreamCompleter{
		Model:            "m",
		FrequencyPenalty: &fpen,
		PresencePenalty:  &ppen,
		Temperature:      &temp,
		TopP:             &top,
		MaxTokens:        &max,
		ToolChoice:       &choice,
		apiKey:           "sekret",
		URL:              "http://example.invalid",
		tools:            []ToolSuper{{Type: "function", Function: Tool{Name: "x", Description: "d", Inputs: pub_models.InputSchema{Type: "object"}}}},
	}
	chat := pub_models.Chat{Messages: []pub_models.Message{{Role: "user", Content: "c"}}}
	httpReq, err := s.createRequest(context.Background(), chat)
	if err != nil {
		t.Fatalf("createRequest err: %v", err)
	}

	if got := httpReq.Header.Get("Content-Type"); got != "application/json" {
		t.Fatalf("bad content-type: %q", got)
	}
	if got := httpReq.Header.Get("Authorization"); !strings.HasPrefix(got, "Bearer ") {
		t.Fatalf("bad auth header: %q", got)
	}
	if got := httpReq.Header.Get("Accept"); got != "text/event-stream" {
		t.Fatalf("bad accept: %q", got)
	}
	if got := httpReq.Header.Get("Connection"); got != "keep-alive" {
		t.Fatalf("bad connection: %q", got)
	}

	b, _ := io.ReadAll(httpReq.Body)
	var body map[string]any
	if err := jsonUnmarshal(b, &body); err != nil {
		t.Fatalf("unmarshal body: %v\nbody=%s", err, string(b))
	}
	if v, ok := body["stream"].(bool); !ok || !v {
		t.Fatalf("expected stream=true, got: %T %v", body["stream"], body["stream"])
	}
	if v, ok := body["model"].(string); !ok || v != s.Model {
		t.Fatalf("model mismatch: %v", body["model"])
	}
	if v, ok := body["frequency_penalty"].(float64); !ok || v != fpen {
		t.Fatalf("freq pen mismatch: %v", body["frequency_penalty"])
	}
	if v, ok := body["presence_penalty"].(float64); !ok || v != ppen {
		t.Fatalf("presence pen mismatch: %v", body["presence_penalty"])
	}
	if v, ok := body["temperature"].(float64); !ok || v != temp {
		t.Fatalf("temp mismatch: %v", body["temperature"])
	}
	if v, ok := body["top_p"].(float64); !ok || v != top {
		t.Fatalf("topP mismatch: %v", body["top_p"])
	}
	if v, ok := body["max_tokens"].(float64); !ok || int(v) != max {
		t.Fatalf("max mismatch: %v", body["max_tokens"])
	}
	if v, ok := body["tool_choice"].(string); !ok || v != choice {
		t.Fatalf("tool choice mismatch: %v", body["tool_choice"])
	}
	toolsV, ok := body["tools"].([]any)
	if !ok || len(toolsV) != 1 {
		t.Fatalf("tools missing in body: %T %v", body["tools"], body["tools"])
	}
	tool0, _ := toolsV[0].(map[string]any)
	fn, _ := tool0["function"].(map[string]any)
	if name, _ := fn["name"].(string); name != "x" {
		t.Fatalf("tool name mismatch: %v", name)
	}
}

// helper to avoid external json pkg alias confusion in tests
func jsonUnmarshal(b []byte, v any) error { return json.Unmarshal(b, v) }

func TestHandleStreamResponse_EmitsEventsAndErrorOnEOF(t *testing.T) {
	pr, pw := io.Pipe()
	res := &http.Response{StatusCode: 200, Body: pr}
	s := &StreamCompleter{}
	ctx := context.Background()
	out, err := s.handleStreamResponse(ctx, res)
	if err != nil {
		t.Fatalf("handleStreamResponse err: %v", err)
	}

	// writer goroutine
	go func() {
		bw := bufio.NewWriter(pw)
		fmt.Fprintf(bw, "data: %s\n", `{"choices":[{"delta":{"content":"first"}}]}`)
		bw.Flush()
		fmt.Fprintf(bw, "data: %s\n", `{"choices":[{"delta":{"content":"second"}}]}`)
		bw.Flush()
		pw.Close() // trigger EOF
	}()

	// Expect first two string events then an error
	for i := 0; i < 2; i++ {
		select {
		case ev := <-out:
			if s, ok := ev.(string); !ok || (i == 0 && s != "first") || (i == 1 && s != "second") {
				t.Fatalf("unexpected ev %d: %T %v", i, ev, ev)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("timeout waiting for events")
		}
	}
	select {
	case ev, open := <-out:
		if !open {
			return
		}
		if _, ok := ev.(error); !ok {
			t.Fatalf("expected error event after EOF, got: %T %v", ev, ev)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for error event")
	}
}

func TestHandleStreamChunk_Table(t *testing.T) {
	s := &StreamCompleter{}

	// DONE -> Noop
	maybeStopEv := s.handleStreamChunk([]byte("data: [DONE]\n"))
	_, isStopEvent := maybeStopEv.(models.StopEvent)
	if !isStopEvent {
		t.Fatalf("expected STOP for DONE, got: %T %v", maybeStopEv, maybeStopEv)
	}

	// Invalid JSON with DEBUG=false -> Noop
	os.Unsetenv("DEBUG")
	if ev := s.handleStreamChunk([]byte("data: garbage\n")); !isNoop(ev) {
		t.Fatalf("expected Noop for invalid JSON, got: %T %v", ev, ev)
	}

	// Invalid JSON with DEBUG=true -> still Noop but alternate branch
	t.Setenv("DEBUG", "1")
	if ev := s.handleStreamChunk([]byte("data: garbage\n")); !isNoop(ev) {
		t.Fatalf("expected Noop for invalid JSON DEBUG=1, got: %T %v", ev, ev)
	}
	t.Setenv("DEBUG", "")

	// Empty choices -> Noop
	if ev := s.handleStreamChunk([]byte("data: {\"choices\":[]}\n")); !isNoop(ev) {
		t.Fatalf("expected Noop for empty choices, got: %T %v", ev, ev)
	}

	// Plain content
	{
		ev := s.handleStreamChunk([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n"))
		str, ok := ev.(string)
		if !ok || str != "hi" {
			t.Fatalf("expected 'hi', got: %T %v", ev, ev)
		}
	}

	// Prefer Call over string
	tools.Init()
	payload := `{"choices":[{"delta":{"content":"text"}},{"delta":{"tool_calls":[{"id":"1","type":"function","index":0,"function":{"name":"ls","arguments":"{}"}}]}}]}`
	maybeStopEv = s.handleStreamChunk([]byte("data: " + payload + "\n"))
	if _, ok := maybeStopEv.(pub_models.Call); !ok {
		t.Fatalf("expected Call to be preferred, got: %T %v", maybeStopEv, maybeStopEv)
	}
}

func TestHandleChoice_ToolCallsIncremental(t *testing.T) {
	tools.Init()
	s := &StreamCompleter{}
	first := Choice{Delta: Delta{ToolCalls: []ToolsCall{{ID: "id1", Index: 0, Type: "function", Function: Func{Name: "ls", Arguments: "{\"a\":1"}}}}}
	if ev := s.handleChoice(first); !isNoop(ev) {
		t.Fatalf("expected Noop for first partial args, got: %T %v", ev, ev)
	}
	if s.toolsCallName != "ls" || s.toolsCallID != "id1" {
		t.Fatalf("expected name/id captured, got name=%q id=%q", s.toolsCallName, s.toolsCallID)
	}
	second := Choice{Delta: Delta{ToolCalls: []ToolsCall{{Function: Func{Arguments: ",\"b\":2"}}}}}
	if ev := s.handleChoice(second); !isNoop(ev) {
		t.Fatalf("expected Noop for second partial args, got: %T %v", ev, ev)
	}
	third := Choice{Delta: Delta{ToolCalls: []ToolsCall{{Function: Func{Arguments: "}"}}}}}
	ev := s.handleChoice(third)
	call, ok := ev.(pub_models.Call)
	if !ok {
		t.Fatalf("expected Call on completed args, got: %T %v", ev, ev)
	}
	if call.Name != "ls" || call.Type != "function" || call.ID != "id1" || call.Inputs == nil {
		t.Fatalf("bad call: %+v", call)
	}
	if s.toolsCallName != "" || s.toolsCallArgsString != "" {
		t.Fatalf("expected state to be reset after doToolsCall, got name=%q args=%q", s.toolsCallName, s.toolsCallArgsString)
	}
}

func TestHandleChoice_ContentOnly(t *testing.T) {
	s := &StreamCompleter{}
	c := Choice{Delta: Delta{Content: "hello"}, FinishReason: ""}
	if ev := s.handleChoice(c); ev.(string) != "hello" {
		t.Fatalf("expected content string, got: %T %v", ev, ev)
	}
}

func TestDoToolsCall_InvalidJSONAndReset(t *testing.T) {
	s := &StreamCompleter{toolsCallName: "ls", toolsCallArgsString: "not-json"}
	ev := s.doToolsCall()
	if _, ok := ev.(error); !ok {
		t.Fatalf("expected error event for invalid json, got: %T %v", ev, ev)
	}
	if s.toolsCallName != "" || s.toolsCallArgsString != "" {
		t.Fatalf("expected reset after doToolsCall, got name=%q args=%q", s.toolsCallName, s.toolsCallArgsString)
	}
}

func TestDoToolsCall_Valid(t *testing.T) {
	tools.Init()
	s := &StreamCompleter{toolsCallName: "ls", toolsCallID: "IDX", toolsCallArgsString: "{\"x\":1}"}
	ev := s.doToolsCall()
	call, ok := ev.(pub_models.Call)
	if !ok {
		t.Fatalf("expected Call, got: %T %v", ev, ev)
	}
	if call.Name != "ls" || call.ID != "IDX" || call.Type != "function" || call.Inputs == nil {
		t.Fatalf("bad call: %+v", call)
	}
	if call.Function.Arguments != "{\"x\":1}" {
		t.Fatalf("expected arguments to be preserved, got: %q", call.Function.Arguments)
	}
	if s.toolsCallName != "" || s.toolsCallArgsString != "" {
		t.Fatalf("expected reset after doToolsCall, got name=%q args=%q", s.toolsCallName, s.toolsCallArgsString)
	}
}

func TestCountInputTokens(t *testing.T) {
	s := &StreamCompleter{}
	c := pub_models.Chat{Messages: []pub_models.Message{{Content: "a b c"}, {Content: "d"}}}
	n, err := s.CountInputTokens(context.Background(), c)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	f := heuristicTokenCountFactor
	exp := int(float64(4) * f)
	if n != exp {
		t.Fatalf("unexpected token count: got %d want %d", n, exp)
	}
	c = pub_models.Chat{}
	n, _ = s.CountInputTokens(context.Background(), c)
	if n != 0 {
		t.Fatalf("expected 0, got %d", n)
	}
}
