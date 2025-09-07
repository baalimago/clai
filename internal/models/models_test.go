package models

import (
	"testing"

	pub_models "github.com/baalimago/clai/pkg/text/models"
)

func TestLastOfRole(t *testing.T) {
	chat := pub_models.Chat{Messages: []pub_models.Message{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "first"},
		{Role: "admin", Content: "admin-msg"},
		{Role: "user", Content: "last"},
	}}

	msg, i, err := chat.LastOfRole("admin")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if msg.Content != "admin-msg" {
		t.Errorf("expected 'admin-msg', got %q", msg.Content)
	}
	if i != 2 {
		t.Errorf("expected '2', got %v", i)
	}

	msg, i, err = chat.LastOfRole("user")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if msg.Content != "last" {
		t.Errorf("expected 'last', got %q", msg.Content)
	}
	if i != 3 {
		t.Errorf("expected '3', got %v", i)
	}

	_, _, err = chat.LastOfRole("nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent role")
	}
}

func TestFirstSystemMessage(t *testing.T) {
	chat := pub_models.Chat{Messages: []pub_models.Message{
		{Role: "user", Content: "hi"},
		{Role: "system", Content: "rules"},
	}}
	msg, err := chat.FirstSystemMessage()
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if msg.Content != "rules" {
		t.Errorf("expected 'rules', got %q", msg.Content)
	}
	chat.Messages = []pub_models.Message{{Role: "user", Content: "hi"}}
	if _, err := chat.FirstSystemMessage(); err == nil {
		t.Error("expected error when no system message")
	}
}

func TestFirstUserMessage(t *testing.T) {
	chat := pub_models.Chat{Messages: []pub_models.Message{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "ok"},
	}}
	msg, err := chat.FirstUserMessage()
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if msg.Content != "ok" {
		t.Errorf("expected 'ok', got %q", msg.Content)
	}
	chat.Messages = []pub_models.Message{{Role: "system", Content: "sys"}}
	if _, err := chat.FirstUserMessage(); err == nil {
		t.Error("expected error when no user message")
	}
}
