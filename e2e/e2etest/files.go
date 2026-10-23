package e2etest

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/baalimago/clai/internal/chat"
	pub_models "github.com/baalimago/clai/pkg/text/models"
)

func WriteJSONFileAny(t *testing.T, path string, value any) {
	t.Helper()
	b, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatalf("Marshal(%q): %v", path, err)
	}
	b = append(b, '\n')
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatalf("WriteFile(%q): %v", path, err)
	}
}

func ReadStringFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%q): %v", path, err)
	}
	return string(b)
}

func FindSavedConversationFile(t *testing.T, confDir string) string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(confDir, "conversations"))
	if err != nil {
		t.Fatalf("ReadDir(conversations): %v", err)
	}
	for _, entry := range entries {
		if entry.IsDir() || entry.Name() == "globalScope.json" || entry.Name() == "seed.json" {
			continue
		}
		return filepath.Join(confDir, "conversations", entry.Name())
	}
	t.Fatalf("expected a saved conversation file")
	return ""
}

func WriteSkillFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll(%q): %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile(%q): %v", path, err)
	}
}

func WriteSkillsConfigJSON(t *testing.T, confDir string, cfg map[string]any) {
	t.Helper()
	WriteJSONFileAny(t, filepath.Join(confDir, "skills.json"), cfg)
}

func WriteAudioFile(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "f.wav")
	if err := os.WriteFile(path, []byte("RIFF-fake-wav"), 0o644); err != nil {
		t.Fatalf("WriteFile(audio): %v", err)
	}
	return path
}

func SeedConv(t *testing.T, convDir string, c pub_models.Chat) {
	t.Helper()
	if err := chat.Save(convDir, c); err != nil {
		t.Fatalf("Save(%q): %v", c.ID, err)
	}
}

func MakeConv(id string, created time.Time, msgs ...pub_models.Message) pub_models.Chat {
	return pub_models.Chat{
		ID:       id,
		Created:  created,
		Messages: msgs,
	}
}

func MsgUser(content string) pub_models.Message {
	return pub_models.Message{Role: "user", Content: content}
}

func MsgAsst(content string) pub_models.Message {
	return pub_models.Message{Role: "assistant", Content: content}
}
