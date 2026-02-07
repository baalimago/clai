package chat

import (
	"context"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/baalimago/clai/internal/utils"
	pub_models "github.com/baalimago/clai/pkg/text/models"
)

func TestChatContinue_notFound_printsError_and_revertsToList(t *testing.T) {
	// Setup config dir and a single conversation so the list is non-empty.
	confDir := t.TempDir()
	if err := utils.CreateConfigDir(confDir); err != nil {
		t.Fatalf("CreateConfigDir: %v", err)
	}
	convDir := filepath.Join(confDir, "conversations")
	conv := pub_models.Chat{Created: time.Now(), ID: HashIDFromPrompt("hello"), Messages: []pub_models.Message{{Role: "user", Content: "hello"}}}
	if err := Save(convDir, conv); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Create a FIFO to act as TTY and provide a single "q" input so the selection quickly exits.
	fifoPath := filepath.Join(t.TempDir(), "tty-fifo")
	if err := syscall.Mkfifo(fifoPath, 0o600); err != nil {
		t.Fatalf("Mkfifo: %v", err)
	}
	// Writer goroutine: open fifo for writing and write "q\n" then close.
	go func() {
		f, err := os.OpenFile(fifoPath, os.O_WRONLY, 0)
		if err != nil {
			return
		}
		defer f.Close()
		_, _ = f.WriteString("q\n")
	}()

	// Set TTY env so ReadUserInput will use the FIFO.
	oldTTY := os.Getenv("TTY")
	_ = os.Setenv("TTY", fifoPath)
	defer func() { _ = os.Setenv("TTY", oldTTY) }()

	// Capture stderr to check printed error message. We only need the exit behavior from cont().
	oldStderr := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	os.Stderr = w
	defer func() {
		_ = w.Close()
		os.Stderr = oldStderr
	}()

	cq := &ChatHandler{q: nil, subCmd: "continue", prompt: "missing-id", confDir: confDir, convDir: convDir}
	// call cont which should print the error and then call handleListCmd which will read from our FIFO and exit.
	err = cq.cont(context.Background())
	// Wait for writers to finish and read stderr
	_ = w.Close()
	buf := make([]byte, 4096)
	n, _ := r.Read(buf)
	stderrOut := string(buf[:n])

	if err == nil {
		t.Fatalf("expected cont to return an error (user-initiated exit), got nil")
	}

	if !contains(stderrOut, "could not find chat with id: \"missing-id\"") {
		t.Fatalf("expected stderr to contain not-found message, got: %q", stderrOut)
	}
}

func contains(s, sub string) bool { return len(s) >= len(sub) && (s == sub || (len(s) > len(sub) && (""+s) != "" && (string(s[0:len(sub)]) == sub || string(s[len(s)-len(sub):]) == sub || (len(s) > len(sub) && (string(s[1:len(sub)+1]) == sub || (len(s) > len(sub)+1 && string(s[2:len(sub)+2]) == sub)))))) }
