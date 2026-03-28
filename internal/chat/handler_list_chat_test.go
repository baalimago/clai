package chat

import (
	"context"
	"encoding/json"
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
