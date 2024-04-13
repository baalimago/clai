package anthropic

import (
	"testing"

	"github.com/baalimago/clai/internal/models"
)

func Test_claudifyMessage(t *testing.T) {
	testCases := []struct {
		desc  string
		given []models.Message
		want  []models.Message
	}{
		{
			desc: "it should remove the first message if it is a system message",
			given: []models.Message{
				{Role: "system", Content: "system message"},
				{Role: "user", Content: "user message"},
			},
			want: []models.Message{
				{Role: "user", Content: "user message"},
			},
		},
		{
			desc: "it should convert system messages to assistant messages",
			given: []models.Message{
				{Role: "user", Content: "user message"},
				{Role: "system", Content: "system message"},
				{Role: "user", Content: "user message"},
			},
			want: []models.Message{
				{Role: "user", Content: "user message"},
				{Role: "assistant", Content: "system message"},
				{Role: "user", Content: "user message"},
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
			want: []models.Message{
				{Role: "user", Content: "user message 1"},
				{Role: "assistant", Content: "assistant message 1"},
				{Role: "user", Content: "user message 2\nuser message 3"},
			},
		},
		{
			desc: "glob message should start with assistant message",
			given: []models.Message{
				{Role: "system", Content: "system message 1"},
				{Role: "system", Content: "system message 2"},
				{Role: "user", Content: "user message 1"},
			},
			want: []models.Message{
				{Role: "assistant", Content: "system message 2"},
				{Role: "user", Content: "user message 1"},
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
			want: []models.Message{
				{Role: "user", Content: "user message 1\nuser message 2"},
				{Role: "assistant", Content: "assistant message 1"},
				{Role: "user", Content: "user message 3\nuser message 4\nuser message 5"},
			},
		},
	}

	for _, tC := range testCases {
		t.Run(tC.desc, func(t *testing.T) {
			got := claudifyMessages(tC.given)
			if len(tC.want) != len(got) {
				t.Fatalf("incorrect length. expected: %q, got: %q", tC.want, got)
			}

			for i := range tC.want {
				if tC.want[i].Role != got[i].Role {
					t.Fatalf("expected: %q, got: %q", tC.want[i].Role, got[i].Role)
				}
				if tC.want[i].Content != got[i].Content {
					t.Fatalf("expected: %q, got: %q", tC.want[i].Content, got[i].Content)
				}
			}
		})
	}
}
