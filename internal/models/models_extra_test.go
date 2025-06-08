package models

import "testing"

func TestFirstSystemMessage(t *testing.T) {
	chat := Chat{Messages: []Message{
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
	chat.Messages = []Message{{Role: "user", Content: "hi"}}
	if _, err := chat.FirstSystemMessage(); err == nil {
		t.Error("expected error when no system message")
	}
}

func TestFirstUserMessage(t *testing.T) {
	chat := Chat{Messages: []Message{
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
	chat.Messages = []Message{{Role: "system", Content: "sys"}}
	if _, err := chat.FirstUserMessage(); err == nil {
		t.Error("expected error when no user message")
	}
}
