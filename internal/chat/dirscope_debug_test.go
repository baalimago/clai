package chat

import (
	"strings"
	"testing"
)

// TestDirscopeDebugLogging pins the DEBUG_DIRSCOPE opt-in layer: binding saves
// and searches emit [DEBUG_DIRSCOPE] lines only when the flag (or plain DEBUG)
// is truthy.
func TestDirscopeDebugLogging(t *testing.T) {
	t.Run("binding save silent without flag", func(t *testing.T) {
		t.Setenv("DEBUG", "")
		t.Setenv("DEBUG_DIRSCOPE", "")
		cq, _ := newTestHandler(t)
		out := captureStdout(t, func() {
			if err := cq.SaveDirScope(t.TempDir(), "chat1"); err != nil {
				t.Fatalf("SaveDirScope: %v", err)
			}
		})
		if strings.Contains(out, "[DEBUG_DIRSCOPE]") {
			t.Fatalf("expected no dirscope debug output without flag, got %q", out)
		}
	})
	t.Run("binding save prints with flag", func(t *testing.T) {
		t.Setenv("DEBUG", "")
		t.Setenv("DEBUG_DIRSCOPE", "1")
		cq, _ := newTestHandler(t)
		out := captureStdout(t, func() {
			if err := cq.SaveDirScope(t.TempDir(), "chat1"); err != nil {
				t.Fatalf("SaveDirScope: %v", err)
			}
		})
		if !strings.Contains(out, "[DEBUG_DIRSCOPE] saving binding") {
			t.Fatalf("expected dirscope debug output with flag, got %q", out)
		}
	})
	t.Run("search prints with flag", func(t *testing.T) {
		t.Setenv("DEBUG", "")
		t.Setenv("DEBUG_DIRSCOPE", "1")
		cq, _ := newTestHandler(t)
		s := NewConversationSearcher(cq.confDir)
		out := captureStdout(t, func() {
			if _, err := s.Search(SearchRequest{Query: "foo bar", Directory: t.TempDir()}); err != nil {
				t.Fatalf("Search: %v", err)
			}
		})
		if !strings.Contains(out, `[DEBUG_DIRSCOPE] search "foo bar"`) {
			t.Fatalf("expected search debug output with flag, got %q", out)
		}
	})
}
