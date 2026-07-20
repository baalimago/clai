package agent

import (
	"context"
	"fmt"
	"testing"

	"github.com/baalimago/clai/pkg/text/models"
)

func TestExtractJSONCandidates(t *testing.T) {
	tests := []struct {
		name     string
		content  string
		wantLens []int
	}{
		{
			name:     "no braces",
			content:  "no json",
			wantLens: nil,
		},
		{
			name:     "single object",
			content:  `{"a":1}`,
			wantLens: []int{7},
		},
		{
			name:     "nested objects",
			content:  `{"outer":{"inner":1}}`,
			wantLens: []int{21, 11},
		},
		{
			name:     "thinking with brace tokens",
			content:  `Let me use {YYYY} and {MM} tokens. {"real":"json"}`,
			wantLens: []int{15, 6, 4},
		},
		{
			name:     "multiple top-level objects",
			content:  `{"first":1} {"second":2}`,
			wantLens: []int{12, 11},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			candidates := extractJSONCandidates(tt.content)
			if len(candidates) != len(tt.wantLens) {
				t.Fatalf("expected %d candidates, got %d: %v", len(tt.wantLens), len(candidates), candidates)
			}
			for i, want := range tt.wantLens {
				if len(candidates[i]) != want {
					t.Errorf("candidate[%d]: expected len %d, got %d (%q)", i, want, len(candidates[i]), candidates[i])
				}
			}
		})
	}
}

func TestParseTyped(t *testing.T) {
	type simple struct {
		Name  string `json:"name"`
		Value int    `json:"value"`
	}

	t.Run("valid json", func(t *testing.T) {
		result, err := parseTyped[simple](`{"name":"test","value":42}`)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.Name != "test" || result.Value != 42 {
			t.Fatalf("unexpected result: %+v", result)
		}
	})

	t.Run("json with surrounding text", func(t *testing.T) {
		result, err := parseTyped[simple](`Here is the result: {"name":"test","value":42} and some trail`)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.Name != "test" {
			t.Fatalf("unexpected result: %+v", result)
		}
	})

	t.Run("thinking text with brace tokens", func(t *testing.T) {
		result, err := parseTyped[simple](`Using {YYYY} tokens. {"name":"test","value":42}`)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.Name != "test" {
			t.Fatalf("unexpected result: %+v", result)
		}
	})

	t.Run("no json", func(t *testing.T) {
		_, err := parseTyped[simple]("no json here")
		if err == nil {
			t.Fatal("expected error")
		}
	})

	t.Run("invalid json in all candidates", func(t *testing.T) {
		_, err := parseTyped[simple](`{not valid} {also not}`)
		if err == nil {
			t.Fatal("expected error")
		}
	})

	t.Run("multiple candidates picks longest first", func(t *testing.T) {
		// Longest candidate {"name":"b","value":2} tried first, wins.
		result, err := parseTyped[simple](`{"name":"a"} {"name":"b","value":2}`)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.Name != "b" {
			t.Fatalf("expected longest candidate to win, got: %+v", result)
		}
	})
}

func TestTypedQuerier_Query_ReadsFromAssistantRole(t *testing.T) {
	type platform struct {
		PlatformType string `json:"platform_type"`
	}

	// Simulate a chat with:
	// - system prompt containing JSON-like formatting examples (trap)
	// - assistant response containing the actual LLM output JSON
	chat := models.Chat{
		Messages: []models.Message{
			{
				Role: "system",
				// This system prompt contains valid JSON that matches platform fields —
				// if Query incorrectly reads from system role, it will parse this silently.
				Content: `OUTPUT FORMAT: {"platform_type": "ciceron"}`,
			},
			{
				Role:    "user",
				Content: "Classify this page.",
			},
			{
				Role:    "assistant",
				Content: `{"platform_type": "unknown"}`,
			},
		},
	}

	// Mock agent whose querier returns our crafted chat.
	a := New()
	a.querier = &stubChatQuerier{chat: chat}
	tq := NewTyped[platform]()
	tq.agent = &a

	result, err := tq.Query(context.Background(), models.Chat{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.PlatformType != "unknown" {
		t.Fatalf("expected PlatformType='unknown' (from assistant), got '%s' (likely from system prompt trap)", result.PlatformType)
	}
}

func TestTypedMetadataQuerier_Query(t *testing.T) {
	type simple struct {
		Name string `json:"name"`
	}

	t.Run("returns typed result with metadata", func(t *testing.T) {
		chat := models.Chat{
			ID: "chat-abc123",
			TokenUsage: &models.Usage{
				PromptTokens:     100,
				CompletionTokens: 50,
				TotalTokens:      150,
			},
			Messages: []models.Message{
				{
					Role:    "assistant",
					Content: `{"name":"meta-test"}`,
				},
			},
		}

		a := New(WithConfigDir("/tmp/clai"))
		a.querier = &stubChatQuerier{chat: chat}
		tmq := NewTypedMetadata[simple]()
		tmq.agent = &a

		result, meta, err := tmq.Query(context.Background(), models.Chat{})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.Name != "meta-test" {
			t.Fatalf("expected Name='meta-test', got '%s'", result.Name)
		}
		if meta.ChatID != "chat-abc123" {
			t.Fatalf("expected ChatID='chat-abc123', got '%s'", meta.ChatID)
		}
		if meta.TokenUsage == nil {
			t.Fatal("expected non-nil TokenUsage")
		}
		if meta.TokenUsage.TotalTokens != 150 {
			t.Fatalf("expected TotalTokens=150, got %d", meta.TokenUsage.TotalTokens)
		}
		expectedPath := "/tmp/clai/conversations/chat-abc123.json"
		if meta.ConversationPath != expectedPath {
			t.Fatalf("expected ConversationPath='%s', got '%s'", expectedPath, meta.ConversationPath)
		}
	})

	t.Run("nil token usage", func(t *testing.T) {
		chat := models.Chat{
			ID:         "chat-no-usage",
			TokenUsage: nil,
			Messages: []models.Message{
				{
					Role:    "assistant",
					Content: `{"name":"no-usage"}`,
				},
			},
		}

		a := New()
		a.querier = &stubChatQuerier{chat: chat}
		tmq := NewTypedMetadata[simple]()
		tmq.agent = &a

		_, meta, err := tmq.Query(context.Background(), models.Chat{})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if meta.TokenUsage != nil {
			t.Fatalf("expected nil TokenUsage, got %+v", meta.TokenUsage)
		}
	})

	t.Run("agent query error", func(t *testing.T) {
		a := New()
		a.querier = &stubChatQuerier{err: fmt.Errorf("boom")}
		tmq := NewTypedMetadata[simple]()
		tmq.agent = &a

		_, _, err := tmq.Query(context.Background(), models.Chat{})
		if err == nil {
			t.Fatal("expected error when agent.Query fails")
		}
	})

	t.Run("no assistant message", func(t *testing.T) {
		chat := models.Chat{
			ID: "chat-no-assist",
			Messages: []models.Message{
				{
					Role:    "user",
					Content: "hello",
				},
			},
		}

		a := New()
		a.querier = &stubChatQuerier{chat: chat}
		tmq := NewTypedMetadata[simple]()
		tmq.agent = &a

		_, _, err := tmq.Query(context.Background(), models.Chat{})
		if err == nil {
			t.Fatal("expected error when no assistant message exists")
		}
	})

	t.Run("no valid json in assistant message", func(t *testing.T) {
		chat := models.Chat{
			ID: "chat-bad-json",
			Messages: []models.Message{
				{
					Role:    "assistant",
					Content: "sorry, no json here",
				},
			},
		}

		a := New()
		a.querier = &stubChatQuerier{chat: chat}
		tmq := NewTypedMetadata[simple]()
		tmq.agent = &a

		_, _, err := tmq.Query(context.Background(), models.Chat{})
		if err == nil {
			t.Fatal("expected error when assistant message has no valid JSON")
		}
	})
}

// stubChatQuerier returns a predefined chat, or an error if err is set.
type stubChatQuerier struct {
	chat models.Chat
	err  error
}

func (s *stubChatQuerier) Query(ctx context.Context) error { return nil }
func (s *stubChatQuerier) TextQuery(ctx context.Context, chat models.Chat) (models.Chat, error) {
	if s.err != nil {
		return models.Chat{}, s.err
	}
	return s.chat, nil
}
