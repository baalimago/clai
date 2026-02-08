package chat

import (
	"os"
	"path/filepath"
	"testing"

	pub_models "github.com/baalimago/clai/pkg/text/models"
)

func TestGlobalScopeMigration_PrevQueryToGlobalScope_AndPreferGlobalScopeForDirInfo(t *testing.T) {
	tmp := t.TempDir()
	convDir := filepath.Join(tmp, "conversations")
	if err := os.MkdirAll(filepath.Join(convDir, "dirs"), 0o755); err != nil {
		t.Fatalf("mkdir conversations/dirs dir: %v", err)
	}

	oldPath := filepath.Join(convDir, "prevQuery.json")
	newPath := filepath.Join(convDir, "globalScope.json")

	// Create an old global chat file.
	old := pub_models.Chat{ID: "prevQuery"}
	if err := Save(convDir, old); err != nil {
		t.Fatalf("save old prevQuery chat: %v", err)
	}
	if _, err := os.Stat(oldPath); err != nil {
		t.Fatalf("stat old prevQuery.json: %v", err)
	}

	// Also create a new global chat file, which must be replaced by migration.
	if err := os.WriteFile(newPath, []byte(`{"id":"globalScope"}`), 0o644); err != nil {
		t.Fatalf("write pre-existing globalScope.json: %v", err)
	}

	// Load should migrate and prefer the old content.
	got, err := LoadGlobalScope(tmp)
	if err != nil {
		t.Fatalf("LoadGlobalScope: %v", err)
	}
	if got.ID != "globalScope" {
		t.Fatalf("expected migrated chat id to be globalScope, got %q", got.ID)
	}

	// Ensure old is removed and new exists.
	if _, err := os.Stat(oldPath); err == nil {
		t.Fatalf("expected prevQuery.json to be removed after migration")
	}
	if _, err := os.Stat(newPath); err != nil {
		t.Fatalf("expected globalScope.json to exist after migration: %v", err)
	}

	// For `chat dir`, when no dir-scoped chat exists, global scope should be used.
	cq := &ChatHandler{confDir: tmp, convDir: convDir}
	info, err := cq.resolveChatDirInfo()
	if err != nil {
		t.Fatalf("resolveChatDirInfo: %v", err)
	}
	if info.Scope != "global" {
		t.Fatalf("expected global scope, got %q", info.Scope)
	}
	if info.ChatID != "globalScope" {
		t.Fatalf("expected globalScope chat id, got %q", info.ChatID)
	}
}
