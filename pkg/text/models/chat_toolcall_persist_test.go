package models

import (
	"encoding/json"
	"testing"
)

func TestChatPersistsFullToolCall(t *testing.T) {
	inputs := Input{"path": "/tmp"}
	chat := Chat{
		ID: "c1",
		Messages: []Message{{
			Role: "assistant",
			ToolCalls: []Call{{
				ID:     "call_1",
				Name:   "ls",
				Inputs: &inputs,
				Function: Specification{
					Name:        "ls",
					Description: "list files",
					Inputs:      &InputSchema{Type: "object"},
				},
			}},
		}},
	}
	b, err := json.Marshal(chat)
	if err != nil {
		t.Fatalf("marshal chat: %v", err)
	}
	var got Chat
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("unmarshal chat: %v", err)
	}
	call := got.Messages[0].ToolCalls[0]
	if call.ID != "call_1" || call.Name != "ls" || call.Type != "function" {
		t.Fatalf("call identity lost: %+v", call)
	}
	if call.Inputs == nil || (*call.Inputs)["path"] != "/tmp" {
		t.Fatalf("inputs lost: %+v", call.Inputs)
	}
	if call.Function.Name != "ls" || call.Function.Description != "list files" || call.Function.Inputs == nil {
		t.Fatalf("function spec lost: %+v", call.Function)
	}
}
