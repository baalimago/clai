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
		TokenUsage:       &pub_models.Usage{TotalTokens: 12},
		RecentTokenUsage: &pub_models.Usage{TotalTokens: 7},
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
	if got.RecentTokenUsage == nil || got.RecentTokenUsage.TotalTokens != 7 {
		t.Fatalf("recent token usage mismatch: got %+v", got.RecentTokenUsage)
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
		if conv.RecentTokenUsage == nil || conv.RecentTokenUsage.TotalTokens != 7 {
			t.Fatalf("conversation recent token usage mismatch: got %+v", conv.RecentTokenUsage)
		}
		foundConversation = true
	}
	if !foundConversation {
		t.Fatalf("expected a persisted conversation besides globalScope")
	}
}

func TestSaveAsPreviousQuery_DoesNotCreateDuplicateConversationForExistingChat(t *testing.T) {
	confDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(confDir, "conversations"), 0o755); err != nil {
		t.Fatalf("MkdirAll(conversations): %v", err)
	}
	chat := pub_models.Chat{
		ID:      "existing-chat",
		Created: time.Now(),
		Messages: []pub_models.Message{
			{Role: "system", Content: "sys"},
			{Role: "user", Content: "hello"},
			{Role: "assistant", Content: "hi"},
		},
	}

	if err := SaveAsPreviousQuery(confDir, chat); err != nil {
		t.Fatalf("SaveAsPreviousQuery: %v", err)
	}

	entries, err := os.ReadDir(filepath.Join(confDir, "conversations"))
	if err != nil {
		t.Fatalf("ReadDir(conversations): %v", err)
	}

	var conversationFiles []string
	for _, entry := range entries {
		if entry.IsDir() || entry.Name() == "globalScope.json" || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		conversationFiles = append(conversationFiles, entry.Name())
	}

	if len(conversationFiles) != 1 {
		t.Fatalf("expected exactly 1 persisted conversation, got %d: %v", len(conversationFiles), conversationFiles)
	}
	if conversationFiles[0] != "existing-chat.json" {
		t.Fatalf("expected persisted conversation %q, got %q", "existing-chat.json", conversationFiles[0])
	}
}

func labelledChat(id string, msgs ...pub_models.Message) pub_models.Chat {
	return pub_models.Chat{
		ID:        id,
		Created:   time.Date(2026, 9, 9, 8, 0, 0, 0, time.UTC),
		Title:     "T",
		Summary:   "S",
		SummaryAt: time.Date(2026, 9, 9, 9, 0, 0, 0, time.UTC),
		Messages:  msgs,
	}
}

func assertLabelled(t *testing.T, got pub_models.Chat, want pub_models.Chat) {
	t.Helper()
	if got.Title != want.Title || got.Summary != want.Summary || !got.SummaryAt.Equal(want.SummaryAt) {
		t.Fatalf("label fields lost: got title=%q summary=%q at=%v, want title=%q summary=%q at=%v",
			got.Title, got.Summary, got.SummaryAt, want.Title, want.Summary, want.SummaryAt)
	}
}

func TestSave_roundTripsTitleSummary(t *testing.T) {
	convDir := t.TempDir()
	want := labelledChat("labelled", pub_models.Message{Role: "user", Content: "hi"})
	if err := Save(convDir, want); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := FromPath(filepath.Join(convDir, "labelled.json"))
	if err != nil {
		t.Fatalf("FromPath: %v", err)
	}
	assertLabelled(t, got, want)
}

func TestSaveAsPreviousQuery_mirrorCarriesFields(t *testing.T) {
	confDir := t.TempDir()
	want := labelledChat("source", pub_models.Message{Role: "user", Content: "hi"}, pub_models.Message{Role: "assistant", Content: "yo"})
	if err := SaveAsPreviousQuery(confDir, want); err != nil {
		t.Fatalf("SaveAsPreviousQuery: %v", err)
	}
	mirror, err := LoadPrevQuery(confDir)
	if err != nil {
		t.Fatalf("LoadPrevQuery: %v", err)
	}
	if mirror.ID != globalScopeChatID {
		t.Fatalf("mirror id = %q", mirror.ID)
	}
	assertLabelled(t, mirror, want)
}

func TestSaveAsPreviousQuery_promotionCarriesFields(t *testing.T) {
	confDir := t.TempDir()
	want := labelledChat("",
		pub_models.Message{Role: "system", Content: "sys"},
		pub_models.Message{Role: "user", Content: "hi"},
		pub_models.Message{Role: "assistant", Content: "yo"},
	)
	if err := SaveAsPreviousQuery(confDir, want); err != nil {
		t.Fatalf("SaveAsPreviousQuery: %v", err)
	}
	entries, err := os.ReadDir(filepath.Join(confDir, "conversations"))
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	promoted := 0
	for _, entry := range entries {
		if entry.IsDir() || entry.Name() == "globalScope.json" || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		got, err := FromPath(filepath.Join(confDir, "conversations", entry.Name()))
		if err != nil {
			t.Fatalf("FromPath(%q): %v", entry.Name(), err)
		}
		assertLabelled(t, got, want)
		promoted++
	}
	if promoted != 1 {
		t.Fatalf("expected exactly one promoted conversation, got %d", promoted)
	}
}
