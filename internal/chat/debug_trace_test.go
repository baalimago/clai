package chat

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	pub_models "github.com/baalimago/clai/pkg/text/models"
)

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	origStdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("create stdout pipe: %v", err)
	}
	os.Stdout = w
	defer func() {
		os.Stdout = origStdout
	}()

	readDone := make(chan string, 1)
	go func() {
		var buf bytes.Buffer
		_, copyErr := io.Copy(&buf, r)
		if copyErr != nil {
			readDone <- "COPYERR: " + copyErr.Error()
			return
		}
		readDone <- buf.String()
	}()

	fn()
	if err := w.Close(); err != nil {
		t.Fatalf("close stdout writer: %v", err)
	}
	got := <-readDone
	if strings.HasPrefix(got, "COPYERR: ") {
		t.Fatalf("capture stdout: %v", got)
	}
	if err := r.Close(); err != nil {
		t.Fatalf("close stdout reader: %v", err)
	}
	return got
}

func TestLoadPrevQuery_TraceShowsLoadedConversationIDWhenDebugEnabled(t *testing.T) {
	confDir := t.TempDir()
	convDir := filepath.Join(confDir, "conversations")
	if err := os.MkdirAll(convDir, 0o755); err != nil {
		t.Fatalf("mkdir conversations: %v", err)
	}
	chat := pub_models.Chat{
		ID:       "globalScope",
		Messages: []pub_models.Message{{Role: "user", Content: "hello"}},
	}
	if err := Save(convDir, chat); err != nil {
		t.Fatalf("save global scope chat: %v", err)
	}

	t.Setenv("DEBUG", "false")
	t.Setenv("DEBUG_CHAT", "false")
	withoutDebug := captureStdout(t, func() {
		if _, err := LoadPrevQuery(confDir); err != nil {
			t.Fatalf("LoadPrevQuery without debug: %v", err)
		}
	})
	if strings.Contains(withoutDebug, "chat_id=") {
		t.Fatalf("expected no trace output when debug is disabled, got %q", withoutDebug)
	}

	t.Setenv("DEBUG", "true")
	t.Setenv("DEBUG_CHAT", "false")
	withDebug := captureStdout(t, func() {
		if _, err := LoadPrevQuery(confDir); err != nil {
			t.Fatalf("LoadPrevQuery with debug: %v", err)
		}
	})
	if !strings.Contains(withDebug, `chat_id="globalScope"`) {
		t.Fatalf("expected trace output to mention loaded conversation id, got %q", withDebug)
	}
}
