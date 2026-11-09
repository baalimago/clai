package chat

import (
	"context"
	"strings"
	"testing"

	"github.com/baalimago/clai/internal/testenv/procenv"
)

// TestDirscopeDebugLogging pins the DEBUG_DIRSCOPE opt-in layer: binding saves
// and searches emit dirscope debug lines on stderr only when the flag (or
// plain DEBUG) is truthy.
func TestDirscopeDebugLogging(t *testing.T) {
	t.Run("binding save silent without flag", func(t *testing.T) {
		t.Setenv("DEBUG", "")
		t.Setenv("DEBUG_DIRSCOPE", "")
		cq, _ := newTestHandler(t)
		out := captureStderr(t, func() {
			if err := cq.SaveDirScope(procenv.Context(context.Background()), t.TempDir(), "chat1"); err != nil {
				t.Fatalf("SaveDirScope: %v", err)
			}
		})
		if strings.Contains(out, "[dirscope]") {
			t.Fatalf("expected no dirscope debug output without flag, got %q", out)
		}
	})
	t.Run("binding save prints with flag", func(t *testing.T) {
		t.Setenv("DEBUG", "")
		t.Setenv("DEBUG_DIRSCOPE", "1")
		cq, _ := newTestHandler(t)
		out := captureStderr(t, func() {
			if err := cq.SaveDirScope(procenv.Context(context.Background()), t.TempDir(), "chat1"); err != nil {
				t.Fatalf("SaveDirScope: %v", err)
			}
		})
		if !strings.Contains(out, "[dirscope] saving binding") {
			t.Fatalf("expected dirscope debug output with flag, got %q", out)
		}
	})
	t.Run("search prints with flag", func(t *testing.T) {
		t.Setenv("DEBUG", "")
		t.Setenv("DEBUG_DIRSCOPE", "1")
		cq, _ := newTestHandler(t)
		s := NewConversationSearcher(cq.confDir)
		out := captureStderr(t, func() {
			if _, err := s.Search(procenv.Context(context.Background()), SearchRequest{Query: "foo bar", Directory: t.TempDir()}); err != nil {
				t.Fatalf("Search: %v", err)
			}
		})
		if !strings.Contains(out, `[dirscope] search "foo bar"`) {
			t.Fatalf("expected search debug output with flag, got %q", out)
		}
	})
}
