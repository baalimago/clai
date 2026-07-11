package chat

import (
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
	if err := Save(conversationsDir(confDir), sourceChat); err != nil {
		t.Fatalf("Save(sourceChat): %v", err)
	}

	handler := &ChatHandler{confDir: confDir}
	if err := handler.SaveDirScope(dir, sourceChat.ID); err != nil {
		t.Fatalf("SaveDirScope: %v", err)
	}

	// Pre-populate globalScope.json with stale content to prove it is NOT read.
	staleChat := pub_models.Chat{
		ID: globalScopeChatID,
		Messages: []pub_models.Message{
			{Role: "user", Content: "stale"},
		},
	}
	if err := Save(conversationsDir(confDir), staleChat); err != nil {
		t.Fatalf("Save(stale): %v", err)
	}

	got, err := LoadDirScopedContext(confDir)
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

	// Verify globalScope was NOT modified (the stale content is still there).
	gs, err := LoadGlobalScope(confDir)
	if err != nil {
		t.Fatalf("LoadGlobalScope: %v", err)
	}
	if len(gs.Messages) != 1 || gs.Messages[0].Content != "stale" {
		t.Fatalf("globalScope was modified unexpectedly: %+v", gs.Messages)
	}
}
