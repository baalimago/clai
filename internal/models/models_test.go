// This package contains test intended to be used by the implementations of the
// Querier, ChatQuerier and StreamCompleter interfaces
package models

import (
	"context"
	"testing"
	"time"

	"github.com/baalimago/go_away_boilerplate/pkg/testboil"
)

func TestQuerier_Context(t *testing.T, q Querier) {
	testboil.ReturnsOnContextCancel(t, func(ctx context.Context) {
		q.Query(ctx)
	}, time.Second)
}

func TestChatQuerier(t *testing.T, q ChatQuerier) {
	testboil.ReturnsOnContextCancel(t, func(ctx context.Context) {
		q.TextQuery(ctx, Chat{})
	}, time.Second)
}

func TestStreamCompleter(t *testing.T, s StreamCompleter) {
	testboil.ReturnsOnContextCancel(t, func(ctx context.Context) {
		s.StreamCompletions(ctx, Chat{})
	}, time.Second)
}

func TestLastOfRole(t *testing.T) {
	chat := Chat{Messages: []Message{
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
