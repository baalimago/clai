package chat

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	pub_models "github.com/baalimago/clai/pkg/text/models"
)

func TestCloneForeignChat_UpsertsIndexAndDedupsOnNextListBuild(t *testing.T) {
	cq, confDir := newTestHandler(t)
	convDir := conversationsDir(confDir)

	foreign := pub_models.Chat{
		Created:  time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		Source:   "claude-code",
		SourceID: "s1",
		Messages: []pub_models.Message{{Role: "system", Content: "seed"}, {Role: "user", Content: "hi"}},
	}
	if foreign.Source == "" || foreign.SourceID == "" {
		t.Fatal("test setup invalid")
	}

	oldTTY := os.Getenv("TTY")
	if err := os.Setenv("TTY", "/dev/null"); err != nil {
		t.Fatalf("set TTY: %v", err)
	}
	t.Cleanup(func() { _ = os.Setenv("TTY", oldTTY) })

	cloned, err := cq.cloneForeignChat(context.Background(), foreign)
	if err != nil {
		t.Fatalf("cloneForeignChat: %v", err)
	}
	if cloned.ID == "" {
		t.Fatal("expected cloned chat to have non-empty ID")
	}

	p, err := NewChatIndexPaginator(convDir)
	if err != nil {
		t.Fatalf("NewChatIndexPaginator: %v", err)
	}
	found := false
	for _, r := range p.rows {
		if r.ID == cloned.ID {
			found = true
			if r.Source != foreign.Source || r.SourceID != foreign.SourceID {
				t.Fatalf("index row source mismatch: got (%q,%q)", r.Source, r.SourceID)
			}
		}
	}
	if !found {
		t.Fatalf("expected cloned chat %q to be present in index", cloned.ID)
	}

	// Create a Claude jsonl on disk with same session so discovery would find it.
	home := t.TempDir()
	t.Setenv("HOME", home)
	projDir := filepath.Join(home, ".claude", "projects", "p1")
	if err := os.MkdirAll(projDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	jsonlPath := filepath.Join(projDir, "sess.jsonl")
	jsonl := `{"type":"user","timestamp":"2026-01-01T00:00:00Z","sessionId":"s1","cwd":"/work","message":{"content":"hi"}}` + "\n"
	if err := os.WriteFile(jsonlPath, []byte(jsonl), 0o644); err != nil {
		t.Fatalf("write jsonl: %v", err)
	}

	p2, err := NewChatIndexPaginator(convDir)
	if err != nil {
		t.Fatalf("NewChatIndexPaginator: %v", err)
	}
	rows, _, err := cq.buildChatListRows(context.Background(), p2)
	if err != nil {
		t.Fatalf("buildChatListRows: %v", err)
	}
	for _, r := range rows {
		if r.Kind == chatRowForeign && r.Source == foreign.Source && r.SourceID == foreign.SourceID {
			t.Fatalf("expected foreign row to be deduped after clone; still present")
		}
	}
}
