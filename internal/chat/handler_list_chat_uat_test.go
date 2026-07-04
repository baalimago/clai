package chat

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/baalimago/clai/internal/utils"
)

// These tests are "user acceptance" style integration tests.
// They intentionally exercise the real handler path (list -> select -> continue)
// by stubbing only terminal input.

func TestUAT_ListSelectContinue_ForeignClaudeChat_ClonesAndThenDedups(t *testing.T) {
	ctx := context.Background()

	// Ensure a controlled CWD so dirscope binding is deterministic.
	_ = chdirToTemp(t)

	// Create a temp HOME with a Claude project.
	home := t.TempDir()
	t.Setenv("HOME", home)

	projDir := filepath.Join(home, ".claude", "projects", "p1")
	if err := os.MkdirAll(projDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	jsonlPath := filepath.Join(projDir, "sess.jsonl")
	jsonl := strings.Join([]string{
		`{"type":"user","timestamp":"2026-01-01T00:00:00Z","sessionId":"s-uat","cwd":"/work","message":{"content":"hi"}}`,
		`{"type":"assistant","timestamp":"2026-01-01T00:00:01Z","sessionId":"s-uat","cwd":"/work","message":{"content":"hello"}}`,
		"",
	}, "\n")
	if err := os.WriteFile(jsonlPath, []byte(jsonl), 0o644); err != nil {
		t.Fatalf("write jsonl: %v", err)
	}
	// Make ordering stable even if timestamp parse degrades to mtime.
	if err := os.Chtimes(jsonlPath, time.Date(2026, 1, 1, 0, 0, 2, 0, time.UTC), time.Date(2026, 1, 1, 0, 0, 2, 0, time.UTC)); err != nil {
		t.Fatalf("chtimes: %v", err)
	}

	// Stub the interactive terminal: select row 0, then continue clone.
	in := []string{"0", "c"}
	restore := utils.UseReadUserInputForTests(func() (string, error) {
		if len(in) == 0 {
			return "", nil
		}
		next := in[0]
		in = in[1:]
		return next, nil
	})
	t.Cleanup(restore)

	// Capture output to ensure this is running through the real list/act flow.
	var out bytes.Buffer
	cq, confDir := newTestHandler(t)
	cq.out = &out

	// First run: foreign row discovered, selected, and cloned.
	if err := cq.handleListCmd(ctx); err != nil {
		t.Fatalf("handleListCmd: %v", err)
	}

	convDir := conversationsDir(confDir)
	p, err := NewChatIndexPaginator(convDir)
	if err != nil {
		t.Fatalf("NewChatIndexPaginator: %v", err)
	}
	var clonedID string
	for _, r := range p.rows {
		if r.Source == "claude-code" && r.SourceID == "s-uat" {
			clonedID = r.ID
			break
		}
	}
	if clonedID == "" {
		t.Fatalf("expected cloned chat to exist in index")
	}
	if clonedID == "s-uat" {
		t.Fatalf("expected cloned chat to get a new unique clai ID, got %q", clonedID)
	}
	if !strings.Contains(out.String(), "(press [c]ontinue") {
		t.Fatalf("expected foreign chat info continue prompt in output, got:\n%s", out.String())
	}

	// Second run: now the foreign listing should be deduped.
	var out2 bytes.Buffer
	cq2, _ := newTestHandler(t)
	cq2.out = &out2
	// Only list, then quit.
	in2 := []string{"q"}
	restore2 := utils.UseReadUserInputForTests(func() (string, error) {
		if len(in2) == 0 {
			return "q", nil
		}
		next := in2[0]
		in2 = in2[1:]
		return next, nil
	})
	t.Cleanup(restore2)

	_ = cq2.handleListCmd(ctx)
	if strings.Contains(out2.String(), "s-uat") {
		t.Fatalf("expected foreign session id to be suppressed after clone; output:\n%s", out2.String())
	}
}
