package chat

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	pub_models "github.com/baalimago/clai/pkg/text/models"
)

func TestSaveAsPreviousQuery_ExistingConversationDoesNotCreatePromotedCopy(t *testing.T) {
	confDir := t.TempDir()
	if err := os.MkdirAll(conversationsDir(confDir), 0o755); err != nil {
		t.Fatalf("MkdirAll(conversations): %v", err)
	}

	chat := pub_models.Chat{
		ID:      "existing-chat",
		Created: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
		Messages: []pub_models.Message{
			{Role: "system", Content: "sys"},
			{Role: "user", Content: "hello"},
			{Role: "assistant", Content: "world"},
		},
	}

	if err := Save(conversationsDir(confDir), chat); err != nil {
		t.Fatalf("Save(existing chat): %v", err)
	}

	if err := SaveAsPreviousQuery(confDir, chat); err != nil {
		t.Fatalf("SaveAsPreviousQuery: %v", err)
	}

	entries, err := os.ReadDir(conversationsDir(confDir))
	if err != nil {
		t.Fatalf("ReadDir(conversations): %v", err)
	}

	var chatFiles []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if filepath.Ext(name) != ".json" {
			continue
		}
		chatFiles = append(chatFiles, name)
	}

	if len(chatFiles) != 2 {
		t.Fatalf("expected exactly existing chat and global scope to be persisted, got %d files: %v", len(chatFiles), chatFiles)
	}
	if _, err := os.Stat(filepath.Join(conversationsDir(confDir), globalScopeChatID+".json")); err != nil {
		t.Fatalf("Stat(global scope): %v", err)
	}
	if _, err := os.Stat(filepath.Join(conversationsDir(confDir), chat.ID+".json")); err != nil {
		t.Fatalf("Stat(existing chat): %v", err)
	}
}