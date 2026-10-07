package chat

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/baalimago/clai/internal/utils"
	"github.com/baalimago/clai/internal/vendors"
	pub_models "github.com/baalimago/clai/pkg/text/models"
)

type failingSourceReader struct {
	name string
	err  error
}

func (f failingSourceReader) Source() string { return f.name }
func (f failingSourceReader) Discover(context.Context, vendors.SourceCache) ([]vendors.SourceRow, error) {
	return nil, f.err
}

func (f failingSourceReader) Read(context.Context, vendors.SourceCache, string) (pub_models.Chat, error) {
	return pub_models.Chat{}, f.err
}

func TestChatTotalTokens_FallsBackToPersistedUsage(t *testing.T) {
	withLastUsage := pub_models.Chat{TokenUsage: &pub_models.Usage{TotalTokens: 5}}
	if got := chatTotalTokens(withLastUsage); got != 5 {
		t.Fatalf("chatTotalTokens = %d, want the persisted TokenUsage", got)
	}
	withRecentUsage := pub_models.Chat{RecentTokenUsage: &pub_models.Usage{TotalTokens: 7}}
	if got := chatTotalTokens(withRecentUsage); got != 7 {
		t.Fatalf("chatTotalTokens = %d, want the recent usage", got)
	}
	if got := chatTotalTokens(pub_models.Chat{}); got != 0 {
		t.Fatalf("chatTotalTokens = %d, want 0 without any usage", got)
	}
}

func TestChatRecentTokens_NoUsage(t *testing.T) {
	if got := chatRecentTokens(pub_models.Chat{}); got != 0 {
		t.Fatalf("chatRecentTokens = %d, want 0", got)
	}
}

func TestVisibleWidth_NonBracketEscapeIsNotCounted(t *testing.T) {
	// ESC followed by a non-'[' byte aborts the escape state; both bytes are
	// hidden, the trailing runes count.
	if got := visibleWidth("\x1bXabc"); got != 3 {
		t.Fatalf("visibleWidth = %d, want 3", got)
	}
	if got := visibleWidth("\x1b[31mred\x1b[0m"); got != 3 {
		t.Fatalf("visibleWidth = %d, want 3", got)
	}
}

func TestSourceReaderByName_RejectsInvalidReaders(t *testing.T) {
	if _, err := sourceReaderByName([]vendors.SourceReader{stubSourceReader{name: ""}}); err == nil {
		t.Fatal("expected an error for a reader with an empty Source()")
	}
	dup := []vendors.SourceReader{stubSourceReader{name: "same"}, stubSourceReader{name: "same"}}
	if _, err := sourceReaderByName(dup); err == nil {
		t.Fatal("expected an error for duplicate reader names")
	}
	if _, err := sourceReaderByName([]vendors.SourceReader{stubSourceReader{name: "a"}}); err != nil {
		t.Fatalf("sourceReaderByName: %v", err)
	}
}

func TestForeignChatRows_SkipsFailingSourceAndEmptyIDs(t *testing.T) {
	t.Setenv("DEBUG", "1")
	cq, _ := newTestHandler(t)
	readers := []vendors.SourceReader{
		failingSourceReader{name: "broken", err: errors.New("nope")},
		stubSourceReader{name: "ok", rows: []vendors.SourceRow{
			{Source: "ok", SourceID: ""},
			{Source: "ok", SourceID: "keep"},
		}},
	}
	rows, err := cq.foreignChatRows(context.Background(), readers, map[string]struct{}{})
	if err != nil {
		t.Fatalf("foreignChatRows: %v", err)
	}
	if len(rows) != 1 || rows[0].SourceID != "keep" {
		t.Fatalf("expected only the identified row, got %#v", rows)
	}
}

func TestBuildChatListRows_RejectsBadReaderSet(t *testing.T) {
	t.Cleanup(useTestSourceReaders([]vendors.SourceReader{stubSourceReader{name: ""}}))
	cq, _ := newTestHandler(t)
	if _, _, err := cq.buildChatListRows(context.Background(), &ChatIndexPaginator{}); err == nil {
		t.Fatal("expected an error when a source reader has an empty name")
	}
}

func TestList_DebugPrintAndMalformedChat(t *testing.T) {
	t.Setenv("DEBUG", "1")
	cq, confDir := newTestHandler(t)
	convDir := conversationsDir(confDir)
	if err := os.WriteFile(filepath.Join(convDir, "broken.json"), []byte("{not json"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	_, err := cq.list()
	if err == nil || !strings.Contains(err.Error(), "failed to get chat") {
		t.Fatalf("err = %v, want the per-chat parse failure", err)
	}
}

func TestHandleListCmd_IndexReadFailure(t *testing.T) {
	blocker := filepath.Join(t.TempDir(), "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	cq := &ChatHandler{convDir: filepath.Join(blocker, "conversations")}
	err := cq.handleListCmd(context.Background())
	if err == nil || !strings.Contains(err.Error(), "failed to create chat index paginator") {
		t.Fatalf("err = %v", err)
	}
}

func TestActOnChat_UnknownChoice(t *testing.T) {
	cq := &ChatHandler{out: io.Discard, input: utils.NewMacroReader([]string{"zzz"})}
	err := cq.actOnChat(pub_models.Chat{ID: "x"}, "")
	if err == nil || !strings.Contains(err.Error(), "unknown choice") {
		t.Fatalf("err = %v, want the unknown-choice error", err)
	}
}

func TestActOnChat_PrintFailure(t *testing.T) {
	cq := &ChatHandler{out: &failWriteAt{remaining: 0}, input: utils.NewMacroReader([]string{"b"})}
	err := cq.actOnChat(pub_models.Chat{ID: "x"}, "")
	if err == nil || !strings.Contains(err.Error(), "failed to printChatInfo") {
		t.Fatalf("err = %v, want the print failure", err)
	}
}

func TestActOnChat_BackAndPreviousQuery(t *testing.T) {
	cq, confDir := newTestHandler(t)
	cq.out = io.Discard
	cq.input = utils.NewMacroReader([]string{"b"})
	if err := cq.actOnChat(pub_models.Chat{ID: "x"}, ""); err != nil {
		t.Fatalf("actOnChat(back): %v", err)
	}

	cq.input = utils.NewMacroReader([]string{"P"})
	chat := pub_models.Chat{ID: "prev-chat", Messages: []pub_models.Message{{Role: "user", Content: "hi"}}}
	if err := cq.actOnChat(chat, ""); err != nil {
		t.Fatalf("actOnChat(save as previous): %v", err)
	}
	if _, err := os.Stat(filepath.Join(confDir, "conversations", "prev-chat.json")); err != nil {
		t.Fatalf("expected the previous query saved: %v", err)
	}
}

func TestDirScopeRowPredicate_GroupRowsNeverMatch(t *testing.T) {
	cq, _ := newTestHandler(t)
	predicate, _, ok := cq.dirScopeRowPredicate()
	if !ok {
		t.Fatal("expected a usable dir-scope predicate")
	}
	if predicate(chatListRow{Kind: chatRowGroup}) {
		t.Fatal("group rows must never belong to the directory scope")
	}
}

func TestCloneForeignChat_SaveFailure(t *testing.T) {
	blocker := filepath.Join(t.TempDir(), "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	cq := &ChatHandler{convDir: filepath.Join(blocker, "conversations")}
	_, err := cq.cloneForeignChat(pub_models.Chat{Source: "anthropic", SourceID: "s"})
	if err == nil || !strings.Contains(err.Error(), "failed to save cloned chat") {
		t.Fatalf("err = %v", err)
	}
}

func TestDeleteAndEditMessageInChat_BackExitsCleanly(t *testing.T) {
	cq, _ := newTestHandler(t)
	cq.out = io.Discard
	chat := pub_models.Chat{
		ID:       "pick",
		Messages: []pub_models.Message{{Role: "user", Content: "hi"}},
	}

	cq.input = utils.NewMacroReader([]string{"b"})
	if err := cq.deleteMessageInChat(chat); err != nil {
		t.Fatalf("deleteMessageInChat(back): %v", err)
	}

	cq.input = utils.NewMacroReader([]string{"b"})
	if err := cq.editMessageInChat(chat); err != nil {
		t.Fatalf("editMessageInChat(back): %v", err)
	}
}

func TestHandleEditAndDeleteMessages(t *testing.T) {
	cq, _ := newTestHandler(t)
	cq.out = io.Discard
	chat := pub_models.Chat{
		ID:       "pick",
		Messages: []pub_models.Message{{Role: "user", Content: "hi"}},
	}

	cq.input = utils.NewMacroReader([]string{"b"})
	if err := cq.handleDeleteMessages(chat); err != nil {
		t.Fatalf("handleDeleteMessages: %v", err)
	}

	cq.input = utils.NewMacroReader([]string{"b"})
	if err := cq.handleEditMessages(chat); err != nil {
		t.Fatalf("handleEditMessages: %v", err)
	}
}
