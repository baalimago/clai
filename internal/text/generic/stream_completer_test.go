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
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		fl, _ := w.(http.Flusher)
		fmt.Fprintf(w, "data: %s\n", `{"choices":[{"delta":{"content":"Hello"}}]}`)
		if fl != nil {
			fl.Flush()
		}
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
	if v, ok := body["parallel_tool_calls"].(bool); !ok || !v {
		t.Fatalf("parallel tool calls mismatch: %T %v", body["parallel_tool_calls"], body["parallel_tool_calls"])
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

	go func() {
		bw := bufio.NewWriter(pw)
		fmt.Fprintf(bw, "data: %s\n", `{"choices":[{"delta":{"content":"first"}}]}`)
		_ = bw.Flush()
		fmt.Fprintf(bw, "data: %s\n", `{"choices":[{"delta":{"content":"second"}}]}`)
		_ = bw.Flush()
		_ = pw.Close()
	}()

	for i := range 2 {
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

func TestHandleChoice_ToolCallsIncremental(t *testing.T) {
	tools.Init()
	s := &StreamCompleter{}
	first := Choice{Delta: Delta{ToolCalls: []ToolsCall{{ID: "id1", Index: 0, Type: "function", Function: Func{Name: "ls", Arguments: "{\"a\":1"}}}}}
	if ev := s.handleChoice(first); !isNoop(ev) {
		t.Fatalf("expected Noop for first partial args, got: %T %v", ev, ev)
	}
	assembly := s.toolCalls[0]
	if assembly == nil || assembly.Name != "ls" || assembly.ID != "id1" {
		t.Fatalf("expected name/id captured, got assembly=%+v", assembly)
	}
	second := Choice{Delta: Delta{ToolCalls: []ToolsCall{{Index: 0, Function: Func{Arguments: ",\"b\":2"}}}}}
	if ev := s.handleChoice(second); !isNoop(ev) {
		t.Fatalf("expected Noop for second partial args, got: %T %v", ev, ev)
	}
	third := Choice{Delta: Delta{ToolCalls: []ToolsCall{{Index: 0, Function: Func{Arguments: "}"}}}}, FinishReason: "tool_calls"}
	ev := s.handleChoice(third)
	callBatch, ok := ev.(models.ToolCallsEvent)
	if !ok {
		t.Fatalf("expected ToolCallsEvent on completed args, got: %T %v", ev, ev)
	}
	if len(callBatch.Calls) != 1 {
		t.Fatalf("expected one call, got: %d", len(callBatch.Calls))
	}
	call := callBatch.Calls[0]
	if call.Name != "ls" || call.Type != "function" || call.ID != "id1" || call.Inputs == nil {
		t.Fatalf("bad call: %+v", call)
	}
	if len(s.toolCalls) != 0 {
		t.Fatalf("expected state to be reset after flush, got: %+v", s.toolCalls)
	}
}

func TestHandleChoice_ToolCallsParallelBatch(t *testing.T) {
	tools.Init()
	s := &StreamCompleter{}

	first := Choice{Delta: Delta{ToolCalls: []ToolsCall{
		{ID: "id1", Index: 0, Type: "function", Function: Func{Name: "ls", Arguments: `{"path":".`}},
		{ID: "id2", Index: 1, Type: "function", Function: Func{Name: "pwd", Arguments: `{`}},
	}}}
	if ev := s.handleChoice(first); !isNoop(ev) {
		t.Fatalf("expected Noop for first parallel partial args, got: %T %v", ev, ev)
	}

	second := Choice{Delta: Delta{ToolCalls: []ToolsCall{
		{Index: 1, Function: Func{Arguments: `}`}},
		{Index: 0, Function: Func{Arguments: `"}`}},
	}}}
	ev := s.handleChoice(second)
	toolCallsEvent, ok := ev.(models.ToolCallsEvent)
	if !ok {
		t.Fatalf("expected ToolCallsEvent on completed parallel batch, got: %T %v", ev, ev)
	}
	if len(toolCallsEvent.Calls) != 2 {
		t.Fatalf("expected 2 calls, got: %d", len(toolCallsEvent.Calls))
	}
	if toolCallsEvent.Calls[0].ID != "id1" || toolCallsEvent.Calls[0].Name != "ls" {
		t.Fatalf("unexpected first call: %+v", toolCallsEvent.Calls[0])
	}
	if toolCallsEvent.Calls[1].ID != "id2" || toolCallsEvent.Calls[1].Name != "pwd" {
		t.Fatalf("unexpected second call: %+v", toolCallsEvent.Calls[1])
	}
	if got := (*toolCallsEvent.Calls[0].Inputs)["path"]; got != "." {
		t.Fatalf("unexpected first call input path: %v", got)
	}
	if len(s.toolCalls) != 0 {
		t.Fatalf("expected tool call state reset, got: %+v", s.toolCalls)
	}

	ev = s.handleChoice(Choice{FinishReason: "tool_calls"})
	if _, ok := ev.(models.StopEvent); !ok {
		t.Fatalf("expected StopEvent after batch has been flushed, got: %T %v", ev, ev)
	}
}

func TestHandleChoice_ContentOnly(t *testing.T) {
	s := &StreamCompleter{}
	c := Choice{Delta: Delta{Content: "hello"}, FinishReason: ""}
	if ev := s.handleChoice(c); ev.(string) != "hello" {
		t.Fatalf("expected content string, got: %T %v", ev, ev)
	}
}

func TestAssembleToolCall_InvalidJSON(t *testing.T) {
	s := &StreamCompleter{}
	_, err := s.assembleToolCall(toolCallAssembly{Name: "ls", Arguments: "not-json"})
	if err == nil {
		t.Fatal("expected error for invalid json")
	}
}

func TestAssembleToolCall_Valid(t *testing.T) {
	tools.Init()
	s := &StreamCompleter{}
	call, err := s.assembleToolCall(toolCallAssembly{ID: "IDX", Name: "ls", Arguments: "{\"x\":1}"})
	if err != nil {
		t.Fatalf("assembleToolCall err: %v", err)
	}
	if call.Name != "ls" || call.ID != "IDX" || call.Type != "function" || call.Inputs == nil {
		t.Fatalf("bad call: %+v", call)
	}
	if call.Function.Arguments != "{\"x\":1}" {
		t.Fatalf("expected arguments to be preserved, got: %q", call.Function.Arguments)
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