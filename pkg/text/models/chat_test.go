package models

import (
	"encoding/json"
	"math"
	"testing"
	"time"
)

func TestChatQueriesBackwardCompatibleUnmarshal(t *testing.T) {
	raw := `{"created":"2026-01-02T03:04:05Z","id":"chat-1","messages":[{"role":"user","content":"hello"}]}`
	var got Chat
	if err := json.Unmarshal([]byte(raw), &got); err != nil {
		t.Fatalf("unmarshal old chat json: %v", err)
	}
	if got.ID != "chat-1" {
		t.Fatalf("id mismatch: got %q", got.ID)
	}
	if len(got.Queries) != 0 {
		t.Fatalf("expected no queries, got %d", len(got.Queries))
	}
	if got.HasCostEstimates() {
		t.Fatal("expected HasCostEstimates to be false")
	}
}

func TestChatQueriesRoundTrip(t *testing.T) {
	chat := Chat{
		Created: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
		ID:      "chat-1",
		Messages: []Message{
			{Role: "user", Content: "hello"},
		},
		Queries: []QueryCost{
			{
				CreatedAt: time.Date(2026, 1, 2, 3, 5, 5, 0, time.UTC),
				CostUSD:   0.12,
				Model:     "openai/gpt-4.1-mini",
				Usage: Usage{
					PromptTokens:     10,
					CompletionTokens: 2,
					TotalTokens:      12,
				},
			},
		},
	}
	b, err := json.Marshal(chat)
	if err != nil {
		t.Fatalf("marshal chat: %v", err)
	}
	var roundTripped Chat
	if err := json.Unmarshal(b, &roundTripped); err != nil {
		t.Fatalf("unmarshal chat: %v", err)
	}
	if len(roundTripped.Queries) != 1 {
		t.Fatalf("queries length mismatch: got %d", len(roundTripped.Queries))
	}
	if roundTripped.Queries[0].Model != "openai/gpt-4.1-mini" {
		t.Fatalf("query model mismatch: got %q", roundTripped.Queries[0].Model)
	}
	if !roundTripped.Queries[0].CreatedAt.Equal(chat.Queries[0].CreatedAt) {
		t.Fatalf("query created_at mismatch: got %v want %v", roundTripped.Queries[0].CreatedAt, chat.Queries[0].CreatedAt)
	}
}

func TestChatTotalCostUSD(t *testing.T) {
	chat := Chat{
		Queries: []QueryCost{{CostUSD: 0.10}, {CostUSD: 1.25}, {CostUSD: 0.005}},
	}
	got := chat.TotalCostUSD()
	want := 1.355
	if math.Abs(got-want) > 1e-9 {
		t.Fatalf("total cost mismatch: got %v want %v", got, want)
	}
}

func TestChatHasCostEstimates(t *testing.T) {
	if (Chat{}).HasCostEstimates() {
		t.Fatal("expected empty chat to not have cost estimates")
	}
	if !(Chat{Queries: []QueryCost{{CostUSD: 0}}}).HasCostEstimates() {
		t.Fatal("expected chat with queries to have cost estimates")
	}
}

func TestMessageJSON(t *testing.T) {
	// Test round-trip with simple string content
	simpleMsg := Message{Role: "user", Content: "hello world"}
	data, err := json.Marshal(simpleMsg)
	if err != nil {
		t.Fatalf("failed to marshal simple message: %v", err)
	}
	var decodedSimple Message
	if err := json.Unmarshal(data, &decodedSimple); err != nil {
		t.Fatalf("failed to unmarshal simple message: %v", err)
	}
	if decodedSimple.Role != simpleMsg.Role || decodedSimple.Content != simpleMsg.Content {
		t.Errorf("simple message roundtrip mismatch. got: %+v, want: %+v", decodedSimple, simpleMsg)
	}
	if len(decodedSimple.ContentParts) != 0 {
		t.Errorf("expected nil/empty ContentParts, got %v", decodedSimple.ContentParts)
	}

	// Test round-trip with ContentParts
	partsMsg := Message{
		Role: "user",
		ContentParts: []ImageOrTextInput{
			{Type: "text", Text: "describe this image"},
			{Type: "image_url", ImageB64: &ImageURL{URL: "http://example.com/img.png", Detail: "high"}},
		},
	}
	data, err = json.Marshal(partsMsg)
	if err != nil {
		t.Fatalf("failed to marshal parts message: %v", err)
	}
	var decodedParts Message
	if err := json.Unmarshal(data, &decodedParts); err != nil {
		t.Fatalf("failed to unmarshal parts message: %v", err)
	}
	if decodedParts.Role != partsMsg.Role {
		t.Errorf("parts message role mismatch. got: %v, want: %v", decodedParts.Role, partsMsg.Role)
	}
	if decodedParts.Content != "" {
		t.Errorf("expected empty Content, got %q", decodedParts.Content)
	}
	if len(decodedParts.ContentParts) != 2 {
		t.Fatalf("expected 2 content parts, got %d", len(decodedParts.ContentParts))
	}
	if decodedParts.ContentParts[0].Text != "describe this image" {
		t.Errorf("expected text part match, got %v", decodedParts.ContentParts[0])
	}
	if decodedParts.ContentParts[1].ImageB64.URL != "http://example.com/img.png" {
		t.Errorf("expected image url match, got %v", decodedParts.ContentParts[1].ImageB64)
	}
}

func TestChatHelpers(t *testing.T) {
	c := Chat{
		Created: time.Now(),
		ID:      "id1",
		Messages: []Message{
			{Role: "system", Content: "sys"},
			{Role: "user", Content: "u1"},
			{Role: "assistant", Content: "a"},
			{Role: "user", Content: "u2"},
		},
	}

	// First system
	if m, err := c.FirstSystemMessage(); err != nil || m.Content != "sys" {
		t.Fatalf("FirstSystemMessage unexpected: %v, %v", m, err)
	}
	// First user
	if m, err := c.FirstUserMessage(); err != nil || m.Content != "u1" {
		t.Fatalf("FirstUserMessage unexpected: %v, %v", m, err)
	}
	// Last of role
	m, idx, err := c.LastOfRole("user")
	if err != nil || m.Content != "u2" || idx != 3 {
		t.Fatalf("LastOfRole unexpected: %v, %v, %d", m, err, idx)
	}
	// Missing role
	if _, _, err := c.LastOfRole("none"); err == nil {
		t.Fatalf("expected error for missing role")
	}
}

func TestMessageString(t *testing.T) {
	// Test with Content field set
	msg := Message{Role: "user", Content: "hello"}
	if msg.String() != "hello" {
		t.Errorf("expected 'hello', got %q",
			msg.String())
	}

	// Test with ContentParts text
	msg = Message{
		Role: "user",
		ContentParts: []ImageOrTextInput{
			{Type: "text", Text: "from parts"},
		},
	}
	if msg.String() != "from parts" {
		t.Errorf("expected 'from parts', got %q",
			msg.String())
	}

	// Test with mixed ContentParts (text first)
	msg = Message{
		Role: "user",
		ContentParts: []ImageOrTextInput{
			{Type: "image_url", ImageB64: &ImageURL{
				URL: "http://example.com/img.png",
			}},
			{Type: "text", Text: "second text"},
		},
	}
	if msg.String() != "second text" {
		t.Errorf("expected 'second text', got %q",
			msg.String())
	}

	// Test with image only in ContentParts
	msg = Message{
		Role: "user",
		ContentParts: []ImageOrTextInput{
			{Type: "image_url", ImageB64: &ImageURL{
				URL: "http://example.com/img.png",
			}},
		},
	}
	if msg.String() != "" {
		t.Errorf("expected empty string, got %q",
			msg.String())
	}

	// Test empty message
	msg = Message{Role: "user"}
	if msg.String() != "" {
		t.Errorf("expected empty string, got %q",
			msg.String())
	}

	// Test Content takes precedence over ContentParts
	msg = Message{
		Role:    "user",
		Content: "content text",
		ContentParts: []ImageOrTextInput{
			{Type: "text", Text: "parts text"},
		},
	}
	if msg.String() != "content text" {
		t.Errorf("expected 'content text', got %q",
			msg.String())
	}
}
