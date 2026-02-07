package chat

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
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
