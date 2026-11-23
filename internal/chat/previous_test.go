package chat

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	pub_models "github.com/baalimago/clai/pkg/text/models"
)

func conversationFixture(t *testing.T, confDir, chatID string, messages ...pub_models.Message) pub_models.Chat {
	t.Helper()
	if err := os.MkdirAll(conversationsDir(confDir), 0o755); err != nil {
		t.Fatalf("MkdirAll(conversations): %v", err)
	}
	chat := pub_models.Chat{Created: time.Unix(1000, 0), ID: chatID, Messages: messages}
	if err := Save(context.Background(), conversationsDir(confDir), chat); err != nil {
		t.Fatalf("Save(%q): %v", chatID, err)
	}
	return chat
}

func writeLegacyMirror(t *testing.T, confDir, name string, messages []pub_models.Message) string {
	t.Helper()
	if err := os.MkdirAll(conversationsDir(confDir), 0o755); err != nil {
		t.Fatalf("MkdirAll(conversations): %v", err)
	}
	mirror := pub_models.Chat{Created: time.Unix(2000, 0), ID: legacyGlobalScopeChatID, Messages: messages}
	if err := Save(context.Background(), conversationsDir(confDir), mirror); err != nil {
		t.Fatalf("Save(legacy mirror): %v", err)
	}
	written := filepath.Join(conversationsDir(confDir), legacyGlobalScopeFile)
	if name != legacyGlobalScopeFile {
		renamed := filepath.Join(conversationsDir(confDir), name)
		if err := os.Rename(written, renamed); err != nil {
			t.Fatalf("Rename(legacy mirror): %v", err)
		}
		return renamed
	}
	return written
}

func readRecord(t *testing.T, confDir string) previousQuery {
	t.Helper()
	record, err := readPreviousQuery(context.Background(), confDir)
	if err != nil {
		t.Fatalf("readPreviousQuery: %v", err)
	}
	return record
}

// savePreviousConversation persists chat and records it as the previous query,
// which is the state every reader expects after a run.
func savePreviousConversation(t *testing.T, confDir string, chat pub_models.Chat) {
	t.Helper()
	if err := os.MkdirAll(conversationsDir(confDir), 0o755); err != nil {
		t.Fatalf("MkdirAll(conversations): %v", err)
	}
	if err := Save(context.Background(), conversationsDir(confDir), chat); err != nil {
		t.Fatalf("Save(%q): %v", chat.ID, err)
	}
	if err := SavePreviousQuery(context.Background(), confDir, chat.ID, time.Unix(4000, 0)); err != nil {
		t.Fatalf("SavePreviousQuery(%q): %v", chat.ID, err)
	}
}

func TestPreviousQuery_RoundTripsTheConversationItNames(t *testing.T) {
	confDir := t.TempDir()
	conversationFixture(t, confDir, "chat-a",
		pub_models.Message{Role: "user", Content: "hello"},
		pub_models.Message{Role: "assistant", Content: "hi"},
	)
	updated := time.Unix(4000, 0)
	if err := SavePreviousQuery(context.Background(), confDir, "chat-a", updated); err != nil {
		t.Fatalf("SavePreviousQuery: %v", err)
	}

	got, err := LoadPreviousQuery(context.Background(), confDir)
	if err != nil {
		t.Fatalf("LoadPreviousQuery: %v", err)
	}
	if got.Chat.ID != "chat-a" {
		t.Fatalf("chat id = %q, want chat-a", got.Chat.ID)
	}
	if len(got.Chat.Messages) != 2 || got.Chat.Messages[0].Content != "hello" {
		t.Fatalf("messages = %+v", got.Chat.Messages)
	}
	if !got.Updated.Equal(updated) {
		t.Fatalf("updated = %v, want %v", got.Updated, updated)
	}

	// The record is machine-local state, not a conversation: it lives outside
	// the conversation namespace and carries no messages of its own.
	if _, err := os.Stat(filepath.Join(conversationsDir(confDir), "previousQuery.json")); err == nil {
		t.Fatal("the record must not be written beside the conversations")
	}
	raw, err := os.ReadFile(PreviousQueryPath(confDir))
	if err != nil {
		t.Fatalf("ReadFile(record): %v", err)
	}
	var fields map[string]any
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatalf("Unmarshal(record): %v", err)
	}
	if _, ok := fields["messages"]; ok {
		t.Fatalf("the record must hold no messages: %s", raw)
	}
}

func TestLoadPreviousQuery_NoRecord(t *testing.T) {
	_, err := LoadPreviousQuery(context.Background(), t.TempDir())
	if !errors.Is(err, ErrNoPreviousQuery) {
		t.Fatalf("err = %v, want ErrNoPreviousQuery", err)
	}
}

func TestLoadPreviousQuery_RecordNamesMissingConversation(t *testing.T) {
	confDir := t.TempDir()
	if err := SavePreviousQuery(context.Background(), confDir, "chat-gone", time.Unix(4000, 0)); err != nil {
		t.Fatalf("SavePreviousQuery: %v", err)
	}
	_, err := LoadPreviousQuery(context.Background(), confDir)
	if !errors.Is(err, ErrPreviousQueryGone) {
		t.Fatalf("err = %v, want ErrPreviousQueryGone", err)
	}
}

func TestSavePreviousQuery_RejectsIdsWhichCannotNameAConversation(t *testing.T) {
	for _, chatID := range []string{"", legacyGlobalScopeChatID, "../escape", "a/b"} {
		t.Run(chatID, func(t *testing.T) {
			confDir := t.TempDir()
			err := SavePreviousQuery(context.Background(), confDir, chatID, time.Unix(4000, 0))
			if err == nil {
				t.Fatalf("expected an error for chat id %q", chatID)
			}
			if _, statErr := os.Stat(PreviousQueryPath(confDir)); statErr == nil {
				t.Fatal("a rejected id must not write a record")
			}
		})
	}
}

func TestReadPreviousQuery_RefusesForeignVersions(t *testing.T) {
	confDir := t.TempDir()
	if err := os.MkdirAll(previousQueryStateDir(confDir), 0o755); err != nil {
		t.Fatalf("MkdirAll(state): %v", err)
	}
	body := `{"version":99,"chat_id":"chat-a"}`
	if err := os.WriteFile(PreviousQueryPath(confDir), []byte(body), 0o644); err != nil {
		t.Fatalf("WriteFile(record): %v", err)
	}
	if _, err := readPreviousQuery(context.Background(), confDir); err == nil {
		t.Fatal("expected a version mismatch error")
	}
}

func TestMigratePreviousQuery_LegacyMirrorBecomesARecord(t *testing.T) {
	confDir := t.TempDir()
	conversationFixture(t, confDir, "chat-a",
		pub_models.Message{Role: "system", Content: "sys"},
		pub_models.Message{Role: "user", Content: "hello"},
		pub_models.Message{Role: "assistant", Content: "hi"},
	)
	legacyPath := writeLegacyMirror(t, confDir, legacyGlobalScopeFile, []pub_models.Message{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "hello"},
		{Role: "assistant", Content: "hi"},
	})

	if err := MigratePreviousQuery(context.Background(), confDir); err != nil {
		t.Fatalf("MigratePreviousQuery: %v", err)
	}

	if got := readRecord(t, confDir).ChatID; got != "chat-a" {
		t.Fatalf("record chat id = %q, want chat-a", got)
	}
	if _, err := os.Stat(legacyPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the migrated mirror must be removed, stat err = %v", err)
	}
}

func TestMigratePreviousQuery_IsIdempotentAndKeepsAnExistingRecord(t *testing.T) {
	confDir := t.TempDir()
	conversationFixture(t, confDir, "chat-a", pub_models.Message{Role: "user", Content: "a"})
	conversationFixture(t, confDir, "chat-b", pub_models.Message{Role: "user", Content: "b"})
	writeLegacyMirror(t, confDir, legacyGlobalScopeFile, []pub_models.Message{{Role: "user", Content: "a"}})
	if err := SavePreviousQuery(context.Background(), confDir, "chat-b", time.Unix(4000, 0)); err != nil {
		t.Fatalf("SavePreviousQuery: %v", err)
	}

	if err := MigratePreviousQuery(context.Background(), confDir); err != nil {
		t.Fatalf("MigratePreviousQuery: %v", err)
	}
	if got := readRecord(t, confDir).ChatID; got != "chat-b" {
		t.Fatalf("record chat id = %q, want the pre-existing chat-b", got)
	}
}

func TestMigratePreviousQuery_NoLegacyMirrorIsANoOp(t *testing.T) {
	confDir := t.TempDir()
	if err := MigratePreviousQuery(context.Background(), confDir); err != nil {
		t.Fatalf("MigratePreviousQuery: %v", err)
	}
	if _, err := readPreviousQuery(context.Background(), confDir); !errors.Is(err, ErrNoPreviousQuery) {
		t.Fatalf("err = %v, want ErrNoPreviousQuery", err)
	}
}

func TestMigratePreviousQuery_UnresolvableMirrorIsKept(t *testing.T) {
	confDir := t.TempDir()
	conversationFixture(t, confDir, "chat-a", pub_models.Message{Role: "user", Content: "a"})
	legacyPath := writeLegacyMirror(t, confDir, legacyGlobalScopeFile, []pub_models.Message{
		{Role: "user", Content: "the conversation this mirrored was deleted"},
	})

	if err := MigratePreviousQuery(context.Background(), confDir); err != nil {
		t.Fatalf("MigratePreviousQuery: %v", err)
	}
	if _, err := readPreviousQuery(context.Background(), confDir); !errors.Is(err, ErrNoPreviousQuery) {
		t.Fatalf("err = %v, want no record", err)
	}
	if _, err := os.Stat(legacyPath); err != nil {
		t.Fatalf("an unresolvable mirror must be left in place: %v", err)
	}
}

func TestMigratePreviousQuery_PrefersPrevQueryOverGlobalScope(t *testing.T) {
	confDir := t.TempDir()
	conversationFixture(t, confDir, "chat-a", pub_models.Message{Role: "user", Content: "a"})
	conversationFixture(t, confDir, "chat-b", pub_models.Message{Role: "user", Content: "b"})
	writeLegacyMirror(t, confDir, legacyGlobalScopeFile, []pub_models.Message{{Role: "user", Content: "a"}})
	writeLegacyMirror(t, confDir, legacyPrevQueryFile, []pub_models.Message{{Role: "user", Content: "b"}})

	if err := MigratePreviousQuery(context.Background(), confDir); err != nil {
		t.Fatalf("MigratePreviousQuery: %v", err)
	}
	if got := readRecord(t, confDir).ChatID; got != "chat-b" {
		t.Fatalf("record chat id = %q, want chat-b (the pre-globalScope mirror)", got)
	}
}

func TestMigratePreviousQuery_PicksTheNewestIdenticalConversation(t *testing.T) {
	confDir := t.TempDir()
	messages := []pub_models.Message{
		{Role: "user", Content: "same"},
		{Role: "assistant", Content: "same"},
	}
	conversationFixture(t, confDir, "chat-old", messages...)
	conversationFixture(t, confDir, "chat-new", messages...)
	writeLegacyMirror(t, confDir, legacyGlobalScopeFile, messages)

	oldT := time.Unix(3000, 0)
	newT := time.Unix(5000, 0)
	if err := os.Chtimes(conversationPath(confDir, "chat-old"), oldT, oldT); err != nil {
		t.Fatalf("Chtimes(old): %v", err)
	}
	if err := os.Chtimes(conversationPath(confDir, "chat-new"), newT, newT); err != nil {
		t.Fatalf("Chtimes(new): %v", err)
	}

	if err := MigratePreviousQuery(context.Background(), confDir); err != nil {
		t.Fatalf("MigratePreviousQuery: %v", err)
	}
	if got := readRecord(t, confDir).ChatID; got != "chat-new" {
		t.Fatalf("record chat id = %q, want the newest identical conversation", got)
	}
}

func TestClearPreviousQueryRecord_OnlyRemovesTheNamedChat(t *testing.T) {
	confDir := t.TempDir()
	if err := SavePreviousQuery(context.Background(), confDir, "chat-a", time.Unix(4000, 0)); err != nil {
		t.Fatalf("SavePreviousQuery: %v", err)
	}
	if err := clearPreviousQueryRecord(context.Background(), confDir, "chat-b"); err != nil {
		t.Fatalf("clearPreviousQueryRecord(other): %v", err)
	}
	if got := readRecord(t, confDir).ChatID; got != "chat-a" {
		t.Fatalf("record chat id = %q, want chat-a to survive", got)
	}
	if err := clearPreviousQueryRecord(context.Background(), confDir, "chat-a"); err != nil {
		t.Fatalf("clearPreviousQueryRecord(named): %v", err)
	}
	if _, err := readPreviousQuery(context.Background(), confDir); !errors.Is(err, ErrNoPreviousQuery) {
		t.Fatalf("err = %v, want the record removed", err)
	}
	if err := clearPreviousQueryRecord(context.Background(), confDir, "chat-a"); err != nil {
		t.Fatalf("clearing an absent record must be a no-op: %v", err)
	}
}
