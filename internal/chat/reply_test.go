package chat

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	pub_models "github.com/baalimago/clai/pkg/text/models"
)

func TestSaveAsPreviousQuery_PreservesQueries(t *testing.T) {
	confDir := t.TempDir()
	chat := pub_models.Chat{
		ID:      "globalScope",
		Created: time.Now(),
		Messages: []pub_models.Message{
			{Role: "system", Content: "sys"},
			{Role: "user", Content: "hello"},
			{Role: "assistant", Content: "hi"},
		},
		TokenUsage: &pub_models.Usage{TotalTokens: 12},
		Queries: []pub_models.QueryCost{
			{CreatedAt: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC), CostUSD: 0.42, Model: "openai/gpt-4.1-mini"},
		},
	}

	if err := SaveAsPreviousQuery(confDir, chat); err != nil {
		t.Fatalf("SaveAsPreviousQuery: %v", err)
	}

	got, err := LoadPrevQuery(confDir)
	if err != nil {
		t.Fatalf("LoadPrevQuery: %v", err)
	}
	if len(got.Queries) != 1 {
		t.Fatalf("queries length mismatch: got %d", len(got.Queries))
	}
	if got.Queries[0].CostUSD != 0.42 {
		t.Fatalf("query cost mismatch: got %v", got.Queries[0].CostUSD)
	}

	entries, err := os.ReadDir(filepath.Join(confDir, "conversations"))
	if err != nil {
		t.Fatalf("ReadDir(conversations): %v", err)
	}
	foundConversation := false
	for _, entry := range entries {
		if entry.IsDir() || entry.Name() == "globalScope.json" || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		conv, err := FromPath(filepath.Join(confDir, "conversations", entry.Name()))
		if err != nil {
			t.Fatalf("load saved conversation %q: %v", entry.Name(), err)
		}
		if len(conv.Queries) != 1 {
			t.Fatalf("conversation queries length mismatch: got %d", len(conv.Queries))
		}
		foundConversation = true
	}
	if !foundConversation {
		t.Fatalf("expected a persisted conversation besides globalScope")
	}
}
