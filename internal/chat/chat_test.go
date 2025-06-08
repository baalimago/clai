package chat

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/baalimago/clai/internal/models"
)

func TestSaveAndFromPath(t *testing.T) {
	tmp := t.TempDir()
	ch := models.Chat{
		ID:       "my_chat",
		Messages: []models.Message{{Role: "user", Content: "hello"}},
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

func TestIDFromPrompt(t *testing.T) {
	prompt := "hello world some/test path\\dir other extra"
	got := IDFromPrompt(prompt)
	want := "hello_world_some.test_path.dir_other"
	if got != want {
		t.Errorf("IDFromPrompt() = %q, want %q", got, want)
	}
}
