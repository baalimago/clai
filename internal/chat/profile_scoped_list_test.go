package chat

import (
	"testing"
	"time"

	pub_models "github.com/baalimago/clai/pkg/text/models"
)

func TestReadChatIndex_DoesNotDropDirScopedChatsWhenGlobalScopeHasSameProfile(t *testing.T) {
	convDir := t.TempDir()

	dirScoped := pub_models.Chat{
		ID:      "dir-chat",
		Created: time.Date(2026, 4, 9, 10, 0, 0, 0, time.UTC),
		Profile: "gopher",
		Messages: []pub_models.Message{
			{Role: "user", Content: "Hello B"},
		},
	}
	if err := Save(convDir, dirScoped); err != nil {
		t.Fatalf("Save(dirScoped): %v", err)
	}

	global := pub_models.Chat{
		ID:      globalScopeChatID,
		Created: time.Date(2026, 4, 9, 11, 0, 0, 0, time.UTC),
		Profile: "gopher",
		Messages: []pub_models.Message{
			{Role: "user", Content: "Hello C"},
		},
	}
	if err := Save(convDir, global); err != nil {
		t.Fatalf("Save(global): %v", err)
	}

	rows, err := readChatIndex(convDir)
	if err != nil {
		t.Fatalf("readChatIndex: %v", err)
	}

	if len(rows) != 2 {
		t.Fatalf("expected 2 rows, got %d: %#v", len(rows), rows)
	}

	seen := map[string]bool{}
	for _, row := range rows {
		seen[row.ID] = true
	}

	if !seen[dirScoped.ID] {
		t.Fatalf("expected dir-scoped chat id %q in index rows: %#v", dirScoped.ID, rows)
	}
	if !seen[global.ID] {
		t.Fatalf("expected global scope chat id %q in index rows: %#v", global.ID, rows)
	}
}
