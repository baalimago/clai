package reply

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/baalimago/clai/internal/chat"
	pub_models "github.com/baalimago/clai/pkg/text/models"
)

func TestSaveAsPreviousQuery(t *testing.T) {
	tmp := t.TempDir()
	os.MkdirAll(filepath.Join(tmp, "conversations"), 0o755)
	msgs := []pub_models.Message{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "what is life"},
		{Role: "assistant", Content: "42"},
	}
	if err := SaveAsPreviousQuery(tmp, msgs); err != nil {
		t.Fatalf("SaveAsPreviousQuery() err = %v", err)
	}
	prev := filepath.Join(tmp, "conversations", "prevQuery.json")
	if _, err := os.Stat(prev); err != nil {
		t.Fatalf("expected prevQuery file: %v", err)
	}
	convID := chat.IDFromPrompt("what is life")
	conv := filepath.Join(tmp, "conversations", convID+".json")
	if _, err := os.Stat(conv); err != nil {
		t.Fatalf("expected conversation file: %v", err)
	}
}

func TestLoad(t *testing.T) {
	tmp := t.TempDir()
	os.MkdirAll(filepath.Join(tmp, "conversations"), 0o755)
	ch := pub_models.Chat{ID: "prevQuery", Messages: []pub_models.Message{{Role: "user", Content: "hi"}}}
	if err := chat.Save(filepath.Join(tmp, "conversations"), ch); err != nil {
		t.Fatalf("setup save failed: %v", err)
	}
	got, err := Load(tmp)
	if err != nil {
		t.Fatalf("load failed: %v", err)
	}
	if len(got.Messages) != 1 || got.Messages[0].Content != "hi" {
		t.Errorf("unexpected load result: %+v", got)
	}
	// Missing file should not error
	os.Remove(filepath.Join(tmp, "conversations", "prevQuery.json"))
	if _, err := Load(tmp); err != nil {
		t.Errorf("expected nil error when file missing, got %v", err)
	}
}
