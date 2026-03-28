package chat

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	pub_models "github.com/baalimago/clai/pkg/text/models"
)

func TestSaveDirScopedAsPrevQueryCopiesDirectoryScopedConversationToGlobalScope(t *testing.T) {
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
		Created: time.Unix(123, 0),
		ID:      "chat-123",
		Profile: "work",
		Messages: []pub_models.Message{
			{Role: "user", Content: "hello"},
			{Role: "assistant", Content: "world"},
		},
	}
	if err := Save(conversationsDir(confDir), sourceChat); err != nil {
		t.Fatalf("Save(sourceChat): %v", err)
	}

	handler := &ChatHandler{confDir: confDir}
	if err := handler.SaveDirScope(dir, sourceChat.ID); err != nil {
		t.Fatalf("SaveDirScope: %v", err)
	}

	if err := SaveDirScopedAsPrevQuery(confDir); err != nil {
		t.Fatalf("SaveDirScopedAsPrevQuery: %v", err)
	}

	globalScope, err := LoadGlobalScope(confDir)
	if err != nil {
		t.Fatalf("LoadGlobalScope: %v", err)
	}
	if globalScope.ID != globalScopeChatID {
		t.Fatalf("expected global scope ID %q, got %q", globalScopeChatID, globalScope.ID)
	}
	if len(globalScope.Messages) != len(sourceChat.Messages) {
		t.Fatalf("expected %d messages, got %d", len(sourceChat.Messages), len(globalScope.Messages))
	}
	for i := range sourceChat.Messages {
		if globalScope.Messages[i].Role != sourceChat.Messages[i].Role {
			t.Fatalf("message %d role mismatch: got %q want %q", i, globalScope.Messages[i].Role, sourceChat.Messages[i].Role)
		}
		if globalScope.Messages[i].Content != sourceChat.Messages[i].Content {
			t.Fatalf("message %d content mismatch: got %q want %q", i, globalScope.Messages[i].Content, sourceChat.Messages[i].Content)
		}
	}
	if globalScope.Profile != sourceChat.Profile {
		t.Fatalf("expected profile %q, got %q", sourceChat.Profile, globalScope.Profile)
	}
}
