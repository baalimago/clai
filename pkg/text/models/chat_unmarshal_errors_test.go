package models

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestChatTotalTokens(t *testing.T) {
	if got := (Chat{}).TotalTokens(); got != "N/A" {
		t.Fatalf("TotalTokens() = %q, want N/A without a usage record", got)
	}
	withUsage := Chat{TokenUsage: &Usage{TotalTokens: 42}}
	if got := withUsage.TotalTokens(); got != "42" {
		t.Fatalf("TotalTokens() = %q, want 42", got)
	}
}

// TestMessageUnmarshalJSONErrors pins that a wrongly typed field fails the
// unmarshal instead of being silently dropped.
func TestMessageUnmarshalJSONErrors(t *testing.T) {
	tests := []struct {
		name    string
		payload string
	}{
		{"role is not a string", `{"role":1}`},
		{"tool_calls is not a list", `{"tool_calls":"x"}`},
		{"tool_call_id is not a string", `{"tool_call_id":1}`},
		{"reasoning_content is not a string", `{"reasoning_content":1}`},
		{"content part is malformed", `{"content":[{"text":1}]}`},
		{"content is neither string nor list", `{"content":1}`},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var msg Message
			if err := json.Unmarshal([]byte(tc.payload), &msg); err == nil {
				t.Fatalf("expected an error unmarshalling %s", tc.payload)
			}
		})
	}
}

func TestStartupModeUnmarshalError(t *testing.T) {
	var mode StartupMode
	if err := json.Unmarshal([]byte(`123`), &mode); err == nil {
		t.Fatal("expected an error unmarshalling a non-string startup mode")
	}
}

func TestParameterObjectUnmarshalErrors(t *testing.T) {
	t.Run("document is not an object", func(t *testing.T) {
		var p ParameterObject
		if err := json.Unmarshal([]byte(`[1]`), &p); err == nil {
			t.Fatal("expected an error unmarshalling an array into a parameter object")
		}
	})

	t.Run("type is neither string nor string list", func(t *testing.T) {
		var p ParameterObject
		err := json.Unmarshal([]byte(`{"type":123}`), &p)
		if err == nil || !strings.Contains(err.Error(), "type must be a string or array of strings") {
			t.Fatalf("err = %v, want the type error", err)
		}
	})
}

func TestCallJSONMarshalError(t *testing.T) {
	c := Call{ExtraContent: map[string]any{"bad": make(chan int)}}
	if got := c.JSON(); !strings.HasPrefix(got, "ERROR: Failed to unmarshal") {
		t.Fatalf("JSON() = %q, want an error string", got)
	}
}
