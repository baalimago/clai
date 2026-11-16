package chat

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	pub_models "github.com/baalimago/clai/pkg/text/models"
)

// TestLoadConversation_readsTheNamedChatWhateverTheDirectoryBinds pins that a
// conversation named by id is read by that id alone: the binding of the
// working directory names another one and is not consulted.
func TestLoadConversation_readsTheNamedChatWhateverTheDirectoryBinds(t *testing.T) {
	confDir := t.TempDir()
	convDir := filepath.Join(confDir, "conversations")
	if err := os.MkdirAll(convDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	first := labelledChat("first", pub_models.Message{Role: "user", Content: "first meeting"})
	second := labelledChat("second", pub_models.Message{Role: "user", Content: "second meeting"})
	for _, c := range []pub_models.Chat{first, second} {
		if err := Save(context.Background(), convDir, c); err != nil {
			t.Fatalf("Save(%s): %v", c.ID, err)
		}
	}
	wd := t.TempDir()
	if err := saveDirScope(context.Background(), confDir, wd, second.ID); err != nil {
		t.Fatalf("saveDirScope: %v", err)
	}
	oldWd, _ := os.Getwd()
	t.Cleanup(func() { _ = os.Chdir(oldWd) })
	if err := os.Chdir(wd); err != nil {
		t.Fatalf("Chdir: %v", err)
	}

	got, err := LoadConversation(context.Background(), confDir, first.ID)
	if err != nil {
		t.Fatalf("LoadConversation: %v", err)
	}
	if got.ID != first.ID || got.Messages[0].Content != "first meeting" {
		t.Fatalf("got %q %v, want the first conversation", got.ID, got.Messages)
	}
	assertLabelled(t, got, first)
}

func TestLoadConversation_failsForAnUnknownOrUnsafeID(t *testing.T) {
	confDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(confDir, "conversations"), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := Save(context.Background(), filepath.Join(confDir, "conversations"), labelledChat("kept")); err != nil {
		t.Fatalf("Save: %v", err)
	}
	for _, id := range []string{"missing", "", "..", "../kept", `a\b`} {
		t.Run(id, func(t *testing.T) {
			_, err := LoadConversation(context.Background(), confDir, id)
			if !errors.Is(err, ErrChatNotFound) {
				t.Fatalf("LoadConversation(%q) = %v, want ErrChatNotFound", id, err)
			}
		})
	}
}

func TestLoadConversation_reportsAnUnreadableConversation(t *testing.T) {
	confDir := t.TempDir()
	convDir := filepath.Join(confDir, "conversations")
	if err := os.MkdirAll(convDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(convDir, "broken.json"), []byte("{"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	_, err := LoadConversation(context.Background(), confDir, "broken")
	if err == nil || errors.Is(err, ErrChatNotFound) {
		t.Fatalf("LoadConversation(broken) = %v, want a decode error that is not ErrChatNotFound", err)
	}
}
