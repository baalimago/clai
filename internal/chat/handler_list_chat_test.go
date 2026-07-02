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
	if !action.Filter(chatIndexRow{ID: "bound"}) {
		t.Fatal("predicate should keep the bound chat")
	}
	if action.Filter(chatIndexRow{ID: "unbound"}) {
		t.Fatal("predicate should drop a chat not bound to the directory")
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
	if err := cq.listChats(context.Background(), paginator); err == nil {
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
	if err := cq.listChats(context.Background(), paginator); err == nil {
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
	if err := cq.actOnChat(context.Background(), ch); err != nil {
		t.Fatalf("actOnChat: %v", err)
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
	if err := cq.printChatInfo(&out, chatWithCost); err != nil {
		t.Fatalf("printChatInfo with cost: %v", err)
	}
	if !strings.Contains(out.String(), "$14.53") {
		t.Fatalf("expected cost in output, got: %q", out.String())
	}

	out.Reset()
	if err := cq.printChatInfo(&out, chatWithoutCost); err != nil {
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
	err = cq.listChats(context.Background(), paginator)
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
	err = cq.listChats(context.Background(), paginator)
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
