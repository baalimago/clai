package chat

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/baalimago/clai/internal/utils"
	"github.com/baalimago/clai/internal/vendors"
	pub_models "github.com/baalimago/clai/pkg/text/models"
)

// chdirToTemp switches the working directory to a fresh temp dir for the duration
// of the test, returning the temp dir. dirFilterAction binds against the live CWD,
// so tests that exercise it must control the working directory.
func chdirToTemp(t *testing.T) string {
	t.Helper()
	wd := t.TempDir()
	old, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	if err := os.Chdir(wd); err != nil {
		t.Fatalf("Chdir(%q): %v", wd, err)
	}
	t.Cleanup(func() { _ = os.Chdir(old) })
	return wd
}

// TestDirFilterAction_GatingAndPredicate covers acceptance #13: the [d]ir button
// is only offered when the current directory has recorded history, and its
// predicate keeps exactly the rows bound to the directory (head + history).
func TestDirFilterAction_GatingAndPredicate(t *testing.T) {
	cq, confDir := newTestHandler(t)
	wd := chdirToTemp(t)

	action, ok := cq.dirFilterAction()
	if !ok {
		t.Fatal("expected dir filter action even before any history is recorded")
	}
	if action.Format != "[d]irscoped convs" || action.Short != "d" || action.Filter == nil {
		t.Fatalf("unexpected action wiring before history: %+v", action)
	}
	if !strings.Contains(action.EmptyMessage, wd) {
		t.Fatalf("expected empty message to mention cwd %q, got %q", wd, action.EmptyMessage)
	}

	if err := cq.SaveDirScope(wd, "bound"); err != nil {
		t.Fatalf("SaveDirScope: %v", err)
	}

	action, ok = cq.dirFilterAction()
	if !ok {
		t.Fatal("expected dir filter action once the directory has history")
	}
	if action.Format != "[d]irscoped convs" || action.Short != "d" || action.Filter == nil {
		t.Fatalf("unexpected action wiring: %+v", action)
	}
	if !action.Filter(chatListRow{Kind: chatRowNative, ChatID: "bound"}) {
		t.Fatal("predicate should keep the bound chat")
	}
	if action.Filter(chatListRow{Kind: chatRowNative, ChatID: "unbound"}) {
		t.Fatal("predicate should drop a chat not bound to the directory")
	}
	if !action.Filter(chatListRow{Kind: chatRowForeign, Source: "claude-code", SourceID: "s1"}) {
		t.Fatal("predicate should keep foreign rows")
	}
	if action.Filter("not-a-row") {
		t.Fatal("predicate should drop a non-row value")
	}
	_ = confDir
}

// TestListChats_DirFilterTogglesThroughListChats covers acceptance #13 end-to-end:
// the [d]ir button renders in the listChats table and pressing it activates the
// predicate filter (surfaced as the "dir filter" prompt marker).
func TestListChats_DirFilterTogglesThroughListChats(t *testing.T) {
	cq, confDir := newTestHandler(t)
	convDir := conversationsDir(confDir)
	wd := chdirToTemp(t)

	// Prevent real foreign sessions from polluting the table or causing timeout.
	restoreReaders := useTestSourceReaders(nil)
	t.Cleanup(restoreReaders)

	for _, c := range []pub_models.Chat{
		{ID: "bound", Created: time.Date(2026, 1, 2, 3, 4, 6, 0, time.UTC), Messages: []pub_models.Message{{Role: "user", Content: "bound prompt"}}},
		{ID: "unbound", Created: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC), Messages: []pub_models.Message{{Role: "user", Content: "unbound prompt"}}},
	} {
		if err := Save(convDir, c); err != nil {
			t.Fatalf("Save(%q): %v", c.ID, err)
		}
	}
	if err := cq.SaveDirScope(wd, "bound"); err != nil {
		t.Fatalf("SaveDirScope: %v", err)
	}

	// Script: press "d" to toggle the dir filter on, then abort so listChats returns.
	calls := 0
	restore := utils.UseReadUserInputForTests(func() (string, error) {
		calls++
		if calls == 1 {
			return "d", nil
		}
		return "", errors.New("stop")
	})
	t.Cleanup(restore)

	paginator, err := NewChatIndexPaginator(convDir)
	if err != nil {
		t.Fatalf("NewChatIndexPaginator: %v", err)
	}
	var out strings.Builder
	cq.out = &out
	if err := cq.listChats(context.Background(), paginator, ""); err == nil {
		t.Fatal("expected listChats to surface the scripted stop error")
	}

	got := out.String()
	if !strings.Contains(got, "[d]irscoped convs") {
		t.Fatalf("expected the [d]irscoped convs button rendered in the table, got: %q", got)
	}
	if !strings.Contains(got, "dir filter") {
		t.Fatalf("expected the dir filter to be active after pressing d, got: %q", got)
	}
}

func TestListChats_DirFilterWithoutBindingsShowsEmptyDirScopedView(t *testing.T) {
	cq, confDir := newTestHandler(t)
	convDir := conversationsDir(confDir)
	_ = chdirToTemp(t)

	// Prevent real foreign sessions (claude-code, pi) on disk from bleeding
	// into the test. The dir filter passes all foreign rows, which would
	// prevent the "empty dir-scoped" message from appearing.
	restoreReaders := useTestSourceReaders(nil)
	t.Cleanup(restoreReaders)

	for _, c := range []pub_models.Chat{
		{ID: "a", Created: time.Date(2026, 1, 2, 3, 4, 6, 0, time.UTC), Messages: []pub_models.Message{{Role: "user", Content: "prompt a"}}},
		{ID: "b", Created: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC), Messages: []pub_models.Message{{Role: "user", Content: "prompt b"}}},
	} {
		if err := Save(convDir, c); err != nil {
			t.Fatalf("Save(%q): %v", c.ID, err)
		}
	}

	calls := 0
	restore := utils.UseReadUserInputForTests(func() (string, error) {
		calls++
		if calls == 1 {
			return "d", nil
		}
		return "", errors.New("stop")
	})
	t.Cleanup(restore)

	paginator, err := NewChatIndexPaginator(convDir)
	if err != nil {
		t.Fatalf("NewChatIndexPaginator: %v", err)
	}
	var out strings.Builder
	cq.out = &out
	if err := cq.listChats(context.Background(), paginator, ""); err == nil {
		t.Fatal("expected listChats to surface the scripted stop error")
	}

	got := out.String()
	if !strings.Contains(got, "[d]irscoped convs") {
		t.Fatalf("expected the [d]irscoped convs button rendered in the table, got: %q", got)
	}
	if !strings.Contains(got, "no dirscoped conversations in") {
		t.Fatalf("expected the empty dir-scoped state after pressing d, got: %q", got)
	}
}

func TestChatListTokensOrNA(t *testing.T) {
	chWith := pub_models.Chat{
		Created: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
		ID:      "a",
		TokenUsage: &pub_models.Usage{
			TotalTokens: 42,
		},
	}
	chWithout := pub_models.Chat{
		Created:    time.Date(2026, 1, 2, 3, 4, 4, 0, time.UTC),
		ID:         "b",
		TokenUsage: nil,
	}

	if got := chatListTokenStr(chWith); got != "0.042K" {
		t.Fatalf("with usage: want %q, got %q", "0.042K", got)
	}
	if got := chatListTokenStr(chWithout); got != "N/A" {
		t.Fatalf("without usage: want %q, got %q", "N/A", got)
	}
}

func TestActOnChat_enter_continues(t *testing.T) {
	confDir := t.TempDir()
	if err := utils.CreateConfigDir(confDir); err != nil {
		t.Fatalf("CreateConfigDir: %v", err)
	}

	convDir := filepath.Join(confDir, "conversations")
	id := "enter-continue-test"
	ch := pub_models.Chat{ID: id, Created: time.Now(), Messages: []pub_models.Message{{Role: "user", Content: "seed"}}}
	if err := Save(convDir, ch); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Create a FIFO to act as TTY and provide a single "\n" (empty line) input so actOnChat treats it as continue.
	fifoPath := filepath.Join(t.TempDir(), "tty-fifo")
	if err := syscall.Mkfifo(fifoPath, 0o600); err != nil {
		t.Fatalf("Mkfifo: %v", err)
	}
	// Writer goroutine: open fifo for writing and write "\n" then close.
	go func() {
		f, err := os.OpenFile(fifoPath, os.O_WRONLY, 0)
		if err != nil {
			return
		}
		defer f.Close()
		_, _ = f.WriteString("\n")
	}()

	// Set TTY env so ReadUserInput will use the FIFO.
	oldTTY := os.Getenv("TTY")
	_ = os.Setenv("TTY", fifoPath)
	defer func() { _ = os.Setenv("TTY", oldTTY) }()

	cq := &ChatHandler{q: nil, confDir: confDir, convDir: convDir, out: io.Discard}
	if err := cq.actOnChat(ch, ""); err != nil {
		if !errors.Is(err, errExitList) {
			t.Fatalf("actOnChat: %v", err)
		}
	}

	// Verify a dirscope binding file was created and references the chat id.
	dirsPath := filepath.Join(convDir, "dirs")
	entries, err := os.ReadDir(dirsPath)
	if err != nil {
		t.Fatalf("ReadDir dirs: %v", err)
	}
	found := false
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dirsPath, e.Name()))
		if err != nil {
			t.Fatalf("ReadFile dirscope: %v", err)
		}
		var ds DirScope
		if err := json.Unmarshal(b, &ds); err != nil {
			t.Fatalf("Unmarshal dirscope: %v", err)
		}
		if ds.ChatID == id {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected dirscope binding for chat id %q not found", id)
	}
}

func TestChatListCostFormattingHelpers(t *testing.T) {
	chatWithCost := pub_models.Chat{Queries: []pub_models.QueryCost{{CostUSD: 1.2}, {CostUSD: 0.034}}}
	chatWithoutCost := pub_models.Chat{}

	if got := chatListCostStr(chatWithCost); got != "$1.234" {
		t.Fatalf("cost string mismatch: got %q", got)
	}
	if got := chatListCostStr(chatWithoutCost); got != "N/A" {
		t.Fatalf("expected N/A, got %q", got)
	}
}

func TestPrintChatInfo_ShowsCost(t *testing.T) {
	confDir := t.TempDir()
	if err := utils.CreateConfigDir(confDir); err != nil {
		t.Fatalf("CreateConfigDir: %v", err)
	}
	oldConf := os.Getenv("CLAI_CONFIG_DIR")
	if err := os.Setenv("CLAI_CONFIG_DIR", confDir); err != nil {
		t.Fatalf("set CLAI_CONFIG_DIR: %v", err)
	}
	defer func() {
		if err := os.Setenv("CLAI_CONFIG_DIR", oldConf); err != nil {
			t.Fatalf("restore CLAI_CONFIG_DIR: %v", err)
		}
	}()

	chatWithCost := pub_models.Chat{
		ID:       "chat-with-cost",
		Created:  time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
		Messages: []pub_models.Message{{Role: "user", Content: "hello"}},
		Queries:  []pub_models.QueryCost{{CostUSD: 14.53}},
	}
	chatWithoutCost := pub_models.Chat{
		ID:       "chat-without-cost",
		Created:  time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
		Messages: []pub_models.Message{{Role: "user", Content: "hello"}},
	}

	var out strings.Builder
	cq := &ChatHandler{out: &out}
	if err := cq.printChatInfo(&out, chatWithCost, ""); err != nil {
		t.Fatalf("printChatInfo with cost: %v", err)
	}
	if !strings.Contains(out.String(), "$14.53") {
		t.Fatalf("expected cost in output, got: %q", out.String())
	}

	out.Reset()
	if err := cq.printChatInfo(&out, chatWithoutCost, ""); err != nil {
		t.Fatalf("printChatInfo without cost: %v", err)
	}
	if !strings.Contains(out.String(), "N/A") {
		t.Fatalf("expected N/A in output, got: %q", out.String())
	}
}

func TestListChats_IncludesModelColumnAndValue(t *testing.T) {
	confDir := t.TempDir()
	if err := utils.CreateConfigDir(confDir); err != nil {
		t.Fatalf("CreateConfigDir: %v", err)
	}

	// Prevent real foreign sessions from polluting the table.
	restoreReaders := useTestSourceReaders(nil)
	t.Cleanup(restoreReaders)

	convDir := filepath.Join(confDir, "conversations")
	chat := pub_models.Chat{
		ID:       "chat-with-model",
		Created:  time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
		Profile:  "default",
		Messages: []pub_models.Message{{Role: "user", Content: "hello from a fairly descriptive prompt"}},
		Queries:  []pub_models.QueryCost{{CostUSD: 0.42, Model: "openai/gpt-4.1-mini"}},
	}
	if err := Save(convDir, chat); err != nil {
		t.Fatalf("Save: %v", err)
	}

	oldTTY := os.Getenv("TTY")
	if err := os.Setenv("TTY", "/dev/null"); err != nil {
		t.Fatalf("set TTY: %v", err)
	}
	defer func() {
		if err := os.Setenv("TTY", oldTTY); err != nil {
			t.Fatalf("restore TTY: %v", err)
		}
	}()

	paginator, err := NewChatIndexPaginator(convDir)
	if err != nil {
		t.Fatalf("NewChatIndexPaginator: %v", err)
	}

	var out strings.Builder
	cq := &ChatHandler{confDir: confDir, convDir: convDir, out: &out}
	err = cq.listChats(context.Background(), paginator, "")
	if err == nil {
		t.Fatal("listChats() error = nil, want error from empty selection input")
	}
	if !strings.Contains(err.Error(), "failed to select chat") {
		t.Fatalf("listChats() error = %q, want selection context", err.Error())
	}

	got := out.String()
	if !strings.Contains(got, "Model") {
		t.Fatalf("expected table header to include Model, got: %q", got)
	}
	if !strings.Contains(got, "openai/gpt-4.1-mini") {
		t.Fatalf("expected table row to include model value, got: %q", got)
	}
}

func TestListChats_NarrowWidthShowsCostAndPrompt(t *testing.T) {
	confDir := t.TempDir()
	if err := utils.CreateConfigDir(confDir); err != nil {
		t.Fatalf("CreateConfigDir: %v", err)
	}

	// Prevent real foreign sessions from polluting the table or causing timeout.
	restoreReaders := useTestSourceReaders(nil)
	t.Cleanup(restoreReaders)

	convDir := filepath.Join(confDir, "conversations")
	chat := pub_models.Chat{
		ID:       "chat-narrow-width",
		Created:  time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
		Messages: []pub_models.Message{{Role: "user", Content: "hello from a fairly descriptive prompt"}},
		Queries:  []pub_models.QueryCost{{CostUSD: 0.42, Model: "gpt-5.4"}},
	}
	if err := Save(convDir, chat); err != nil {
		t.Fatalf("Save: %v", err)
	}

	oldTTY := os.Getenv("TTY")
	if err := os.Setenv("TTY", "/dev/null"); err != nil {
		t.Fatalf("set TTY: %v", err)
	}
	t.Cleanup(func() {
		if err := os.Setenv("TTY", oldTTY); err != nil {
			t.Fatalf("restore TTY: %v", err)
		}
	})
	oldColumns := os.Getenv("COLUMNS")
	if err := os.Setenv("COLUMNS", "100"); err != nil {
		t.Fatalf("set COLUMNS: %v", err)
	}
	t.Cleanup(func() {
		if err := os.Setenv("COLUMNS", oldColumns); err != nil {
			t.Fatalf("restore COLUMNS: %v", err)
		}
	})

	paginator, err := NewChatIndexPaginator(convDir)
	if err != nil {
		t.Fatalf("NewChatIndexPaginator: %v", err)
	}

	var out strings.Builder
	cq := &ChatHandler{confDir: confDir, convDir: convDir, out: &out}
	err = cq.listChats(context.Background(), paginator, "")
	if err == nil {
		t.Fatal("listChats() error = nil, want error from empty selection input")
	}
	if !strings.Contains(err.Error(), "failed to select chat") {
		t.Fatalf("listChats() error = %q, want selection context", err.Error())
	}

	got := out.String()
	if strings.Contains(got, "%!(EXTRA") {
		t.Fatalf("expected no fmt extra marker, got: %q", got)
	}
	if !strings.Contains(got, "Cost") {
		t.Fatalf("expected narrow table to include Cost, got: %q", got)
	}
	if !strings.Contains(got, "Prompt") {
		t.Fatalf("expected narrow table to include Prompt, got: %q", got)
	}
}

// TestCollapseGroupRows_BasicGrouping verifies that conversations with the same
// first-user-message GroupKey are collapsed into a single [group:N] row, while
// conversations with unique GroupKeys remain ungrouped.
func TestCollapseGroupRows_BasicGrouping(t *testing.T) {
	gk := ComputeGroupKeyFromText("fix the auth bug")
	rows := []chatListRow{
		{
			Kind: chatRowNative, Created: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
			ChatID: "a", FirstUserMessage: "fix the auth bug", GroupKey: gk,
			Profile: "default", Model: "gpt-5", MessageCount: 5, TotalTokens: 100, TotalCostUSD: 1.23,
		},
		{
			Kind: chatRowNative, Created: time.Date(2026, 1, 2, 3, 4, 4, 0, time.UTC),
			ChatID: "b", FirstUserMessage: "fix the auth bug", GroupKey: gk,
			Profile: "default", Model: "gpt-5", MessageCount: 3, TotalTokens: 60, TotalCostUSD: 0.50,
		},
		{
			Kind: chatRowNative, Created: time.Date(2026, 1, 2, 3, 4, 3, 0, time.UTC),
			ChatID: "c", FirstUserMessage: "refactor database", GroupKey: ComputeGroupKeyFromText("refactor database"),
			Profile: "default", Model: "gpt-4o", MessageCount: 4, TotalTokens: 80, TotalCostUSD: 0.80,
		},
	}
	got := collapseGroupRows(rows)
	if len(got) != 2 {
		t.Fatalf("collapseGroupRows: expected 2 rows (1 group + 1 ungrouped), got %d", len(got))
	}
	if got[0].Kind != chatRowGroup {
		t.Fatalf("collapseGroupRows[0]: expected chatRowGroup, got %v", got[0].Kind)
	}
	if got[0].GroupMemberCount != 2 {
		t.Fatalf("collapseGroupRows[0].GroupMemberCount = %d, want 2", got[0].GroupMemberCount)
	}
	if got[0].GroupKey != gk {
		t.Fatalf("collapseGroupRows[0].GroupKey = %q, want %q", got[0].GroupKey, gk)
	}
	// Aggregate totals: 100+60 = 160 tokens, 1.23+0.50 = 1.73 cost
	if got[0].TotalTokens != 160 {
		t.Fatalf("collapseGroupRows[0].TotalTokens = %d, want 160", got[0].TotalTokens)
	}
	if got[0].TotalCostUSD != 1.73 {
		t.Fatalf("collapseGroupRows[0].TotalCostUSD = %.2f, want 1.73", got[0].TotalCostUSD)
	}
	// Second row should be the refactor-database conversation (ungrouped)
	if got[1].Kind != chatRowNative {
		t.Fatalf("collapseGroupRows[1]: expected chatRowNative, got %v", got[1].Kind)
	}
	if got[1].ChatID != "c" {
		t.Fatalf("collapseGroupRows[1].ChatID = %q, want %q", got[1].ChatID, "c")
	}
}

// TestCollapseGroupRows_EmptyGroupKeyNeverGrouped verifies that rows with empty
// GroupKey never form groups, even when their FirstUserMessage text matches.
func TestCollapseGroupRows_EmptyGroupKeyNeverGrouped(t *testing.T) {
	rows := []chatListRow{
		{Kind: chatRowNative, Created: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC), ChatID: "a", FirstUserMessage: "same prompt", GroupKey: ""},
		{Kind: chatRowNative, Created: time.Date(2026, 1, 2, 3, 4, 4, 0, time.UTC), ChatID: "b", FirstUserMessage: "same prompt", GroupKey: ""},
	}
	got := collapseGroupRows(rows)
	if len(got) != 2 {
		t.Fatalf("collapseGroupRows: expected 2 ungrouped rows, got %d", len(got))
	}
	for i, r := range got {
		if r.Kind == chatRowGroup {
			t.Fatalf("collapseGroupRows[%d]: unexpected group row with empty GroupKey: %+v", i, r)
		}
	}
}

// TestCollapseGroupRows_GlobalScopeMirrorNeverGrouped verifies the globalScope
// mirror (which shares the newest conversation's GroupKey) is excluded from
// grouping, so aggregates are not double-counted and no phantom group forms.
func TestCollapseGroupRows_GlobalScopeMirrorNeverGrouped(t *testing.T) {
	gk := ComputeGroupKeyFromText("latest prompt")
	rows := []chatListRow{
		{Kind: chatRowNative, Created: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC), ChatID: globalScopeChatID, FirstUserMessage: "latest prompt", GroupKey: gk, MessageCount: 4},
		{Kind: chatRowNative, Created: time.Date(2026, 1, 2, 3, 4, 4, 0, time.UTC), ChatID: "real", FirstUserMessage: "latest prompt", GroupKey: gk, MessageCount: 4},
	}
	got := collapseGroupRows(rows)
	if len(got) != 2 {
		t.Fatalf("expected 2 ungrouped rows (mirror excluded), got %d", len(got))
	}
	for i, r := range got {
		if r.Kind == chatRowGroup {
			t.Fatalf("row %d: globalScope mirror formed a group: %+v", i, r)
		}
	}
	// Entering a group must never surface the mirror either.
	members := filterRowsByGroupKey(rows, gk)
	if len(members) != 1 || members[0].ChatID != "real" {
		t.Fatalf("expected only the real conversation as group member, got %+v", members)
	}
}

// TestCollapseGroupRows_SingleMemberSuppressed verifies that a GroupKey with only
// one conversation produces no group row.
func TestCollapseGroupRows_SingleMemberSuppressed(t *testing.T) {
	gk := ComputeGroupKeyFromText("unique prompt")
	rows := []chatListRow{
		{Kind: chatRowNative, Created: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC), ChatID: "a", FirstUserMessage: "unique prompt", GroupKey: gk},
	}
	got := collapseGroupRows(rows)
	if len(got) != 1 {
		t.Fatalf("collapseGroupRows: expected 1 ungrouped row, got %d", len(got))
	}
	if got[0].Kind == chatRowGroup {
		t.Fatal("collapseGroupRows: single member should not produce a group row")
	}
}

// TestSave_StampsGroupKeyOnFirstPersist verifies that Save() computes and
// persists GroupKey when a chat with messages is first saved.
func TestSave_StampsGroupKeyOnFirstPersist(t *testing.T) {
	tmp := t.TempDir()
	ch := pub_models.Chat{
		ID:       "test-chat",
		Messages: []pub_models.Message{{Role: "system", Content: "sys"}, {Role: "user", Content: "fix the auth bug"}},
	}
	if ch.GroupKey != "" {
		t.Fatal("expected empty GroupKey before save")
	}
	if err := Save(tmp, ch); err != nil {
		t.Fatalf("Save: %v", err)
	}
	// Reload from disk
	loaded, err := FromPath(filepath.Join(tmp, "test-chat.json"))
	if err != nil {
		t.Fatalf("FromPath: %v", err)
	}
	wantGK := ComputeGroupKeyFromText("fix the auth bug")
	if loaded.GroupKey != wantGK {
		t.Fatalf("loaded.GroupKey = %q, want %q", loaded.GroupKey, wantGK)
	}
	if loaded.GroupKey == "" {
		t.Fatal("expected non-empty GroupKey after save")
	}
}

// stubSourceReader implements vendors.SourceReader for tests.
type stubSourceReader struct {
	name string
	rows []vendors.SourceRow
}

func (s stubSourceReader) Source() string { return s.name }
func (s stubSourceReader) Discover(ctx context.Context) ([]vendors.SourceRow, error) {
	return s.rows, nil
}

func (s stubSourceReader) Read(ctx context.Context, sourceID string) (pub_models.Chat, error) {
	return pub_models.Chat{}, nil
}

// TestForeignChatRows_GroupKeyFromFullFirstUserMessage verifies that
// foreignChatRows computes GroupKey from FullFirstUserMessage, not the
// truncated FirstUserMessage. This prevents two foreign conversations whose
// first 100 chars match but full text differs from colliding into the same group.
func TestForeignChatRows_GroupKeyFromFullFirstUserMessage(t *testing.T) {
	cq, _ := newTestHandler(t)

	// Two conversations whose first 100 chars are identical but full text differs.
	var prefix strings.Builder
	for range 100 {
		prefix.WriteString("x")
	}
	suffixA := "-This-is-the-unique-suffix-for-A"
	suffixB := "-This-is-the-unique-suffix-for-B"

	reader := stubSourceReader{
		name: "test-source",
		rows: []vendors.SourceRow{
			{
				Source:               "test-source",
				SourceID:             "a",
				FirstUserMessage:     prefix.String(), // truncated (same for both)
				FullFirstUserMessage: prefix.String() + suffixA,
				Created:              time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
			},
			{
				Source:               "test-source",
				SourceID:             "b",
				FirstUserMessage:     prefix.String(), // truncated (same for both)
				FullFirstUserMessage: prefix.String() + suffixB,
				Created:              time.Date(2026, 1, 2, 3, 4, 4, 0, time.UTC),
			},
		},
	}

	rows, err := cq.foreignChatRows(context.Background(), []vendors.SourceReader{reader}, map[string]struct{}{})
	if err != nil {
		t.Fatalf("foreignChatRows: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(rows))
	}

	// Verify GroupKeys are different (because FullFirstUserMessage differs)
	if rows[0].GroupKey == rows[1].GroupKey {
		t.Fatalf("expected different GroupKeys for different FullFirstUserMessage, got same: %q", rows[0].GroupKey)
	}

	// Verify GroupKeys match what we'd compute from full text
	want0 := ComputeGroupKeyFromText(prefix.String() + suffixA)
	want1 := ComputeGroupKeyFromText(prefix.String() + suffixB)
	if rows[0].GroupKey != want0 {
		t.Fatalf("row 0 GroupKey: got %q, want %q", rows[0].GroupKey, want0)
	}
	if rows[1].GroupKey != want1 {
		t.Fatalf("row 1 GroupKey: got %q, want %q", rows[1].GroupKey, want1)
	}

	// Sanity check: if we had used truncated FirstUserMessage, keys would be equal
	collidedKey := ComputeGroupKeyFromText(prefix.String())
	if collidedKey == want0 || collidedKey == want1 {
		t.Fatal("test setup error: truncated key should not match full-text keys")
	}
}

// TestListChats_GroupKeyZeroMembers_RendersGroupIndicator covers issue #3:
// when a group view expands to zero members (groupKey != "" but no rows
// match), the prompt bar must still indicate the group context with the
// truncated hash and a [b]ack to list button.
func TestListChats_GroupKeyZeroMembers_RendersGroupIndicator(t *testing.T) {
	cq, confDir := newTestHandler(t)
	convDir := conversationsDir(confDir)

	// Prevent real foreign sessions from polluting the table and causing timeout.
	restoreReaders := useTestSourceReaders(nil)
	t.Cleanup(restoreReaders)

	// Save chats whose GroupKey does NOT match the queried groupKey,
	// ensuring the group view will be empty.
	for _, c := range []pub_models.Chat{
		{
			ID:       "chat-a",
			Created:  time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
			Messages: []pub_models.Message{{Role: "user", Content: "unrelated prompt"}},
			GroupKey: ComputeGroupKeyFromText("unrelated prompt"),
		},
		{
			ID:       "chat-b",
			Created:  time.Date(2026, 1, 2, 3, 4, 4, 0, time.UTC),
			Messages: []pub_models.Message{{Role: "user", Content: "another unrelated"}},
			GroupKey: ComputeGroupKeyFromText("another unrelated"),
		},
	} {
		if err := Save(convDir, c); err != nil {
			t.Fatalf("Save(%q): %v", c.ID, err)
		}
	}

	// A hex groupKey that won't match any saved chat.
	nonMatchingGroupKey := "deadbeefcafebabedeadbeefcafebabedeadbeefcafebabedeadbeefcafebabe"

	calls := 0
	restore := utils.UseReadUserInputForTests(func() (string, error) {
		calls++
		return "b", nil // back to list
	})
	t.Cleanup(restore)

	paginator, err := NewChatIndexPaginator(convDir)
	if err != nil {
		t.Fatalf("NewChatIndexPaginator: %v", err)
	}
	var out strings.Builder
	cq.out = &out
	if err := cq.listChats(context.Background(), paginator, nonMatchingGroupKey); err != nil {
		t.Fatalf("listChats: %v", err)
	}

	got := out.String()
	if !strings.Contains(got, "group:") {
		t.Fatalf("expected group indicator in output, got: %q", got)
	}
	if !strings.Contains(got, "deadbeef...") {
		t.Fatalf("expected truncated hash in output, got: %q", got)
	}
	if !strings.Contains(got, "[b]ack to list") {
		t.Fatalf("expected [b]ack to list in output, got: %q", got)
	}
}

// useTestSourceReaders replaces allSourceReaders for the duration of a test.
func useTestSourceReaders(readers []vendors.SourceReader) func() {
	orig := allSourceReaders
	allSourceReaders = func() []vendors.SourceReader { return readers }
	return func() { allSourceReaders = orig }
}
