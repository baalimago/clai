package generic

import (
	"context"
	"encoding/json"
	"io"
	"reflect"
	"sort"
	"testing"

	pub_models "github.com/baalimago/clai/pkg/text/models"
)

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func fullCall() pub_models.Call {
	inputs := pub_models.Input{"path": "/tmp"}
	return pub_models.Call{
		ID:     "call_1",
		Name:   "ls",
		Type:   "function",
		Inputs: &inputs,
		Function: pub_models.Specification{
			Name:        "ls",
			Description: "list files",
			Inputs:      &pub_models.InputSchema{Type: "object"},
			Arguments:   `{"path":"/tmp"}`,
		},
	}
}

func requestBody(t *testing.T, msgs []pub_models.Message) map[string]any {
	t.Helper()
	s := &StreamCompleter{Model: "m", apiKey: "k", URL: "http://example.invalid"}
	httpReq, err := s.createRequest(context.Background(), pub_models.Chat{Messages: msgs})
	if err != nil {
		t.Fatalf("createRequest: %v", err)
	}
	b, err := io.ReadAll(httpReq.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	var body map[string]any
	if err := json.Unmarshal(b, &body); err != nil {
		t.Fatalf("unmarshal body: %v", err)
	}
	return body
}

func TestCreateRequest_AssistantToolCallWireShape(t *testing.T) {
	msgs := []pub_models.Message{
		{Role: "user", Content: "hi"},
		{Role: "assistant", ToolCalls: []pub_models.Call{fullCall()}},
		{Role: "tool", ToolCallID: "call_1", Content: "out"},
	}
	body := requestBody(t, msgs)

	wireMsgs := body["messages"].([]any)
	tc := wireMsgs[1].(map[string]any)["tool_calls"].([]any)[0].(map[string]any)
	if got, want := sortedKeys(tc), []string{"function", "id", "type"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("tool call keys = %v, want %v", got, want)
	}
	fn := tc["function"].(map[string]any)
	if got, want := sortedKeys(fn), []string{"arguments", "name"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("function keys = %v, want %v", got, want)
	}
	if tc["id"] != "call_1" || tc["type"] != "function" || fn["name"] != "ls" || fn["arguments"] != `{"path":"/tmp"}` {
		t.Fatalf("unexpected tool call values: %v", tc)
	}
	if wireMsgs[2].(map[string]any)["tool_call_id"] != "call_1" {
		t.Fatalf("tool message lost tool_call_id: %v", wireMsgs[2])
	}
}

func TestCreateRequest_ToolCallWireDefaultsAndInputsAsArguments(t *testing.T) {
	inputs := pub_models.Input{"a": "b"}
	msgs := []pub_models.Message{
		{Role: "assistant", ToolCalls: []pub_models.Call{{ID: "c1", Name: "x", Inputs: &inputs}}},
	}
	body := requestBody(t, msgs)
	tc := body["messages"].([]any)[0].(map[string]any)["tool_calls"].([]any)[0].(map[string]any)
	fn := tc["function"].(map[string]any)
	if tc["type"] != "function" || fn["name"] != "x" || fn["arguments"] != `{"a":"b"}` {
		t.Fatalf("unexpected defaults: %v", tc)
	}
}

func TestCreateRequest_ToolCallWireKeepsExtraContentWhenPresent(t *testing.T) {
	call := fullCall()
	call.ExtraContent = map[string]any{"google": map[string]any{"thought_signature": "sig"}}
	body := requestBody(t, []pub_models.Message{{Role: "assistant", ToolCalls: []pub_models.Call{call}}})
	tc := body["messages"].([]any)[0].(map[string]any)["tool_calls"].([]any)[0].(map[string]any)
	if _, ok := tc["extra_content"]; !ok {
		t.Fatalf("expected extra_content to be forwarded, got %v", tc)
	}
}

func TestCreateRequest_WireDoesNotMutateMessages(t *testing.T) {
	msgs := []pub_models.Message{{Role: "assistant", ToolCalls: []pub_models.Call{fullCall()}}}
	_ = requestBody(t, msgs)
	got := msgs[0].ToolCalls[0]
	if got.Name != "ls" || got.Inputs == nil || got.Function.Description != "list files" || got.Function.Inputs == nil {
		t.Fatalf("source call mutated: %+v", got)
	}
}

func TestCreateRequest_WireKeepsMessageContent(t *testing.T) {
	msgs := []pub_models.Message{
		{Role: "user", Content: "plain"},
		{Role: "user", ContentParts: []pub_models.ImageOrTextInput{{Type: "text", Text: "parts"}}},
		{Role: "assistant", ReasoningContent: "why", Content: "ok"},
	}
	body := requestBody(t, msgs)
	wire := body["messages"].([]any)
	if wire[0].(map[string]any)["content"] != "plain" {
		t.Fatalf("plain content lost: %v", wire[0])
	}
	if _, ok := wire[1].(map[string]any)["content"].([]any); !ok {
		t.Fatalf("content parts lost: %v", wire[1])
	}
	if wire[2].(map[string]any)["reasoning_content"] != "why" {
		t.Fatalf("reasoning_content lost: %v", wire[2])
	}
}
