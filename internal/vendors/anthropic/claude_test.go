package anthropic

import (
	"testing"

	"github.com/baalimago/clai/internal/models"
)

func Test_claudifyMessage(t *testing.T) {
	testCases := []struct {
		desc  string
		given []models.Message
		want  []claudeReqMessage
	}{
		{
			desc: "it should remove the first message if it is a system message",
			given: []models.Message{
				{Role: "system", Content: "system message"},
				{Role: "user", Content: "user message"},
			},
			want: []claudeReqMessage{
				{Role: "user", Content: []ClaudeMessage{{Type: "text", Text: "user message"}}},
			},
		},
		{
			desc: "it should convert system messages to assistant messages",
			given: []models.Message{
				{Role: "user", Content: "user message"},
				{Role: "system", Content: "system message"},
				{Role: "user", Content: "user message"},
			},
			want: []claudeReqMessage{
				{Role: "user", Content: []ClaudeMessage{{Type: "text", Text: "user message"}}},
				{Role: "assistant", Content: []ClaudeMessage{{Type: "text", Text: "system message"}}},
				{Role: "user", Content: []ClaudeMessage{{Type: "text", Text: "user message"}}},
			},
		},
		{
			desc: "it should merge user messages into the upcoming message",
			given: []models.Message{
				{Role: "user", Content: "user message 1"},
				{Role: "assistant", Content: "assistant message 1"},
				{Role: "user", Content: "user message 2"},
				{Role: "user", Content: "user message 3"},
			},
			want: []claudeReqMessage{
				{Role: "user", Content: []ClaudeMessage{{Type: "text", Text: "user message 1"}}},
				{Role: "assistant", Content: []ClaudeMessage{{Type: "text", Text: "assistant message 1"}}},
				{Role: "user", Content: []ClaudeMessage{
					{Type: "text", Text: "user message 2"},
					{Type: "text", Text: "user message 3"},
				}},
			},
		},
		{
			desc: "glob message should start with assistant message",
			given: []models.Message{
				{Role: "system", Content: "system message 1"},
				{Role: "system", Content: "system message 2"},
				{Role: "user", Content: "user message 1"},
			},
			want: []claudeReqMessage{
				{Role: "assistant", Content: []ClaudeMessage{{Type: "text", Text: "system message 2"}}},
				{Role: "user", Content: []ClaudeMessage{{Type: "text", Text: "user message 1"}}},
			},
		},
		{
			desc: "tricky example 1",
			given: []models.Message{
				{Role: "user", Content: "user message 1"},
				{Role: "user", Content: "user message 2"},
				{Role: "assistant", Content: "assistant message 1"},
				{Role: "user", Content: "user message 3"},
				{Role: "user", Content: "user message 4"},
				{Role: "user", Content: "user message 5"},
			},
			want: []claudeReqMessage{
				{Role: "user", Content: []ClaudeMessage{
					{Type: "text", Text: "user message 1"},
					{Type: "text", Text: "user message 2"},
				}},
				{Role: "assistant", Content: []ClaudeMessage{{Type: "text", Text: "assistant message 1"}}},
				{Role: "user", Content: []ClaudeMessage{
					{Type: "text", Text: "user message 3"},
					{Type: "text", Text: "user message 4"},
					{Type: "text", Text: "user message 5"},
				}},
			},
		},
	}

	for _, tC := range testCases {
		t.Run(tC.desc, func(t *testing.T) {
			got := claudifyMessages(tC.given)
			if len(tC.want) != len(got) {
				t.Fatalf("incorrect length. expected: %v, got: %v", tC.want, got)
			}

			for i := range tC.want {
				if tC.want[i].Role != got[i].Role {
					t.Fatalf("expected: %q, got: %q", tC.want[i].Role, got[i].Role)
				}

				if len(tC.want[i].Content) != len(got[i].Content) {
					t.Fatalf("content length mismatch. expected %v, got %v", len(tC.want[i].Content), len(got[i].Content))
				}

				for j := range tC.want[i].Content {
					wantText := tC.want[i].Content[j].Text
					gotText := got[i].Content[j].Text
					if wantText != gotText {
						t.Fatalf("expected: %q, got: %q", wantText, gotText)
					}
				}
			}
		})
	}
}
