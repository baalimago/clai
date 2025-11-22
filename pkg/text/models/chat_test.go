package models

import (
	"encoding/json"
	"testing"
	"time"
)

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
