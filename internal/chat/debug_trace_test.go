package chat

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/baalimago/clai/internal/testenv/procenv"

	pub_models "github.com/baalimago/clai/pkg/text/models"
)

// captureStderr captures the process stderr, where the test binary's
// fallback Env delivers diagnostics.
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	origStderr := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("create stderr pipe: %v", err)
	}
	os.Stderr = w
	defer func() {
		os.Stderr = origStderr
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
		t.Fatalf("close stderr writer: %v", err)
	}
	got := <-readDone
	if strings.HasPrefix(got, "COPYERR: ") {
		t.Fatalf("capture stderr: %v", got)
	}
	if err := r.Close(); err != nil {
		t.Fatalf("close stderr reader: %v", err)
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
	if err := Save(procenv.Context(context.Background()), convDir, chat); err != nil {
		t.Fatalf("save global scope chat: %v", err)
	}

	t.Setenv("DEBUG", "false")
	t.Setenv("DEBUG_CHAT", "false")
	withoutDebug := captureStderr(t, func() {
		if _, err := LoadPrevQuery(procenv.Context(context.Background()), confDir); err != nil {
			t.Fatalf("LoadPrevQuery without debug: %v", err)
		}
	})
	if strings.Contains(withoutDebug, "chat_id=") {
		t.Fatalf("expected no trace output when debug is disabled, got %q", withoutDebug)
	}

	t.Setenv("DEBUG", "true")
	t.Setenv("DEBUG_CHAT", "false")
	withDebug := captureStderr(t, func() {
		if _, err := LoadPrevQuery(procenv.Context(context.Background()), confDir); err != nil {
			t.Fatalf("LoadPrevQuery with debug: %v", err)
		}
	})
	if !strings.Contains(withDebug, `chat_id="globalScope"`) {
		t.Fatalf("expected trace output to mention loaded conversation id, got %q", withDebug)
	}
}
