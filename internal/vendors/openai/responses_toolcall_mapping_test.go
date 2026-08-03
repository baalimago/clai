package openai

import (
	"strings"
	"testing"

	pub_models "github.com/baalimago/clai/pkg/text/models"
)

func TestMapChatToResponsesInput_UsesFunctionNameForImportedToolCall(t *testing.T) {
	t.Parallel()

	chat := pub_models.Chat{Messages: []pub_models.Message{
		{Role: "assistant", ToolCalls: []pub_models.Call{{
			ID:       "call_1",
			Function: pub_models.Specification{Name: "read", Arguments: `{"path":"main.go"}`},
		}}},
	}}

	items, err := mapChatToResponsesInput(chat, false)
	if err != nil {
		t.Fatalf("mapChatToResponsesInput: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	if items[0].Name != "read" {
		t.Fatalf("expected function name %q, got %q", "read", items[0].Name)
	}
}

func TestMapChatToResponsesInput_ShortensLongToolCallIDsConsistently(t *testing.T) {
	t.Parallel()

	longID := "call_" + strings.Repeat("x", 78)
	chat := pub_models.Chat{Messages: []pub_models.Message{
		{Role: "assistant", ToolCalls: []pub_models.Call{{
			ID:       longID,
			Name:     "read",
			Function: pub_models.Specification{Arguments: `{}`},
		}}},
		{Role: "tool", ToolCallID: longID, Content: "result"},
	}}

	items, err := mapChatToResponsesInput(chat, false)
	if err != nil {
		t.Fatalf("mapChatToResponsesInput: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(items))
	}
	if len(items[0].CallID) > 64 {
		t.Fatalf("function call id has length %d, want at most 64", len(items[0].CallID))
	}
	if items[0].CallID == longID {
		t.Fatalf("function call id was not shortened: %q", items[0].CallID)
	}
	if items[1].CallID != items[0].CallID {
		t.Fatalf("tool output call id %q does not match function call id %q", items[1].CallID, items[0].CallID)
	}
}

func TestMapChatToResponsesInput_IncludesAssistantToolCallsBeforeToolOutputs(t *testing.T) {
	t.Parallel()

	chat := pub_models.Chat{Messages: []pub_models.Message{
		{Role: "user", Content: "hi"},
		{Role: "assistant", Content: "", ToolCalls: []pub_models.Call{{ID: "call_1", Name: "tool_x", Type: "function"}}},
		{Role: "tool", ToolCallID: "call_1", Content: "result"},
	}}

	items, err := mapChatToResponsesInput(chat, false)
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
