package models

import (
	"testing"
	"time"
)

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
