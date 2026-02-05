package chat

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	pub_models "github.com/baalimago/clai/pkg/text/models"
)

func TestSaveAndFromPath(t *testing.T) {
	tmp := t.TempDir()
	ch := pub_models.Chat{
		ID:       "my_chat",
		Messages: []pub_models.Message{{Role: "user", Content: "hello"}},
	}
	if err := Save(tmp, ch); err != nil {
		t.Fatalf("save failed: %v", err)
	}
	file := filepath.Join(tmp, "my_chat.json")
	if _, err := os.Stat(file); err != nil {
		t.Fatalf("expected file %v to exist: %v", file, err)
	}
	loaded, err := FromPath(file)
	if err != nil {
		t.Fatalf("frompath failed: %v", err)
	}
	if !reflect.DeepEqual(ch, loaded) {
		t.Errorf("loaded chat mismatch: %+v vs %+v", loaded, ch)
	}
}

func TestFromPathError(t *testing.T) {
	if _, err := FromPath("nonexistent.json"); err == nil {
		t.Error("expected error for missing file")
	}
}

func TestList_WhenConversationsContainDirs_DoesNotError(t *testing.T) {
	confDir := t.TempDir()
	convDir := filepath.Join(confDir, "conversations")
	if err := os.MkdirAll(filepath.Join(convDir, "dirs"), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	cq := &ChatHandler{convDir: convDir}
	_, err := cq.list()
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
}
