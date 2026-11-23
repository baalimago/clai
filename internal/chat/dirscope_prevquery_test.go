package chat

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	pub_models "github.com/baalimago/clai/pkg/text/models"
)

func TestLoadDirScopedContext_ReturnsFullConversation(t *testing.T) {
	confDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(confDir, "conversations", "dirs"), 0o755); err != nil {
		t.Fatalf("MkdirAll(conversations/dirs): %v", err)
	}

	dir := t.TempDir()
	oldWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	t.Cleanup(func() {
		if chdirErr := os.Chdir(oldWD); chdirErr != nil {
			t.Fatalf("Chdir(oldWD): %v", chdirErr)
		}
	})
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("Chdir(dir): %v", err)
	}

	sourceChat := pub_models.Chat{
		Created:   time.Unix(500, 0),
		ID:        "chat-dir-head",
		Profile:   "work",
		OriginDir: dir,
		Messages: []pub_models.Message{
			{Role: "system", Content: "sys"},
			{Role: "user", Content: "hello from dir"},
			{Role: "assistant", Content: "response from dir"},
		},
	}
	if err := Save(context.Background(), conversationsDir(confDir), sourceChat); err != nil {
		t.Fatalf("Save(sourceChat): %v", err)
	}

	handler := &ChatHandler{confDir: confDir}
	if err := handler.SaveDirScope(context.Background(), dir, sourceChat.ID); err != nil {
		t.Fatalf("SaveDirScope: %v", err)
	}

	// Point the previous query at another conversation to prove the directory
	// reply takes its context from the binding, not from the previous query.
	otherChat := pub_models.Chat{
		ID: "other-chat",
		Messages: []pub_models.Message{
			{Role: "user", Content: "stale"},
		},
	}
	if err := Save(context.Background(), conversationsDir(confDir), otherChat); err != nil {
		t.Fatalf("Save(otherChat): %v", err)
	}
	if err := SavePreviousQuery(context.Background(), confDir, otherChat.ID, time.Unix(600, 0)); err != nil {
		t.Fatalf("SavePreviousQuery: %v", err)
	}

	got, err := LoadDirScopedContext(context.Background(), confDir)
	if err != nil {
		t.Fatalf("LoadDirScopedContext: %v", err)
	}

	if got.ID != sourceChat.ID {
		t.Fatalf("expected ID %q, got %q", sourceChat.ID, got.ID)
	}
	if len(got.Messages) != len(sourceChat.Messages) {
		t.Fatalf("expected %d messages, got %d", len(sourceChat.Messages), len(got.Messages))
	}
	if got.Messages[1].Content != "hello from dir" {
		t.Fatalf("expected 'hello from dir', got %q", got.Messages[1].Content)
	}
	if got.Profile != sourceChat.Profile {
		t.Fatalf("expected profile %q, got %q", sourceChat.Profile, got.Profile)
	}

	// Verify the previous query was NOT modified.
	prev, err := LoadPreviousQuery(context.Background(), confDir)
	if err != nil {
		t.Fatalf("LoadPreviousQuery: %v", err)
	}
	if prev.Chat.ID != otherChat.ID {
		t.Fatalf("previous query was modified unexpectedly: %+v", prev.Chat)
	}
}
