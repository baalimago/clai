package anthropic

import (
	"reflect"
	"testing"

	"github.com/baalimago/clai/internal/models"
	"github.com/baalimago/clai/internal/tools"
)

func Test_claudifyMessages(t *testing.T) {
	tests := []struct {
		name string
		msgs []models.Message
		want []ClaudeConvMessage
	}{
		{
			name: "Single text message",
			msgs: []models.Message{
				{Role: "user", Content: "Hello"},
			},
			want: []ClaudeConvMessage{
				{Role: "user", Content: []any{TextContentBlock{Type: "text", Text: "Hello"}}},
			},
		},
		{
			name: "Multiple text messages same role",
			msgs: []models.Message{
				{Role: "user", Content: "Hello"},
				{Role: "user", Content: "World"},
			},
			want: []ClaudeConvMessage{
				{Role: "user", Content: []any{
					TextContentBlock{Type: "text", Text: "Hello"},
					TextContentBlock{Type: "text", Text: "World"},
				}},
			},
		},
		{
			name: "Tool call and result",
			msgs: []models.Message{
				{Role: "user", ToolCalls: []tools.Call{
					{Name: "exampleTool", ID: "tool1", Inputs: &tools.Input{"test": 0}},
				}},
				{Role: "tool", ToolCallID: "tool1", Content: "tool result"},
			},
			want: []ClaudeConvMessage{
				{Role: "user", Content: []any{
					ToolUseContentBlock{Type: "tool_use", ID: "tool1", Name: "exampleTool", Input: &map[string]interface{}{"test": 0}},
					ToolResultContentBlock{Type: "tool_result", ToolUseID: "tool1", Content: "tool result"},
				}},
			},
		},
		{
			name: "System message ignored",
			msgs: []models.Message{
				{Role: "system", Content: "system message"},
				{Role: "user", Content: "Hello"},
			},
			want: []ClaudeConvMessage{
				{Role: "user", Content: []any{TextContentBlock{Type: "text", Text: "Hello"}}},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := claudifyMessages(tt.msgs); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("claudifyMessages() = %v, want %v", got, tt.want)
			}
		})
	}
}
