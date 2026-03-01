package openai

import (
	"testing"

	pub_models "github.com/baalimago/clai/pkg/text/models"
)

func TestMapChatToResponsesInput_IncludesAssistantToolCallsBeforeToolOutputs(t *testing.T) {
	t.Parallel()

	chat := pub_models.Chat{Messages: []pub_models.Message{
		{Role: "user", Content: "hi"},
		{Role: "assistant", Content: "", ToolCalls: []pub_models.Call{{ID: "call_1", Name: "tool_x", Type: "function"}}},
		{Role: "tool", ToolCallID: "call_1", Content: "result"},
	}}

	items, err := mapChatToResponsesInput(chat)
	if err != nil {
		t.Fatalf("mapChatToResponsesInput: %v", err)
	}

	if len(items) != 3 {
		t.Fatalf("expected 3 items, got %d", len(items))
	}

	if items[1].Type != "function_call" {
		t.Fatalf("expected second item to be function_call, got %#v", items[1])
	}
	if items[1].CallID != "call_1" {
		t.Fatalf("expected function_call call_id=call_1, got %#v", items[1])
	}

	if items[2].Type != "function_call_output" {
		t.Fatalf("expected third item to be function_call_output, got %#v", items[2])
	}
	if items[2].CallID != "call_1" {
		t.Fatalf("expected output call_id=call_1, got %#v", items[2])
	}
}
