package vendors

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWalkJSONLFiles_RootNotAUsableDirectory(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "root.jsonl")
	if err := os.WriteFile(root, []byte("{}\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	// A regular file root yields no visits and no error.
	if err := WalkJSONLFiles(context.Background(), root, nil, func(string) bool { return false }); err != nil {
		t.Fatalf("WalkJSONLFiles(file root): %v", err)
	}

	// A path whose parent is a file cannot be stat'ed at all.
	err := WalkJSONLFiles(context.Background(), filepath.Join(root, "child"), nil, func(string) bool { return false })
	if err == nil {
		t.Fatal("expected a stat error for a path below a file")
	}
}

func TestWalkJSONLFiles_SkipsNonJSONLAndSkipDirs(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "notes.txt"), []byte("x"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(root, "subagents"), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "subagents", "hidden.jsonl"), []byte("{}\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "keep.jsonl"), []byte("{}\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	var seen []string
	if err := WalkJSONLFiles(context.Background(), root, []string{"subagents"}, func(p string) bool {
		seen = append(seen, filepath.Base(p))
		return false
	}); err != nil {
		t.Fatalf("WalkJSONLFiles: %v", err)
	}
	if len(seen) != 1 || seen[0] != "keep.jsonl" {
		t.Fatalf("visited %v, want only keep.jsonl", seen)
	}
}

func TestScanJSONLLines_SkipsUnparsableLines(t *testing.T) {
	var seen []map[string]any
	err := ScanJSONLLines(strings.NewReader("{}\nnot json\n{\"a\":1}\n"), 1024, func(env map[string]any) bool {
		seen = append(seen, env)
		return true
	})
	if err != nil {
		t.Fatalf("ScanJSONLLines: %v", err)
	}
	if len(seen) != 2 {
		t.Fatalf("expected the two object lines, got %d", len(seen))
	}
}

func TestHomeRelativeRoot_NoHome(t *testing.T) {
	t.Setenv("HOME", "")
	if got := HomeRelativeRoot(""); got != "" {
		t.Fatalf("HomeRelativeRoot = %q, want empty without HOME", got)
	}
}

func TestTextBlocksContent_NonTextShapes(t *testing.T) {
	if got := TextBlocksContent(5); got != "" {
		t.Fatalf("TextBlocksContent(int) = %q, want empty", got)
	}
	if got := TextBlocksContent([]any{"not-a-map", map[string]any{"type": "text", "text": "hi"}}); got != "hi" {
		t.Fatalf("TextBlocksContent = %q, want the text block only", got)
	}
}

func TestRawTextBlocksContent_TolerantDecoding(t *testing.T) {
	if got := RawTextBlocksContent([]byte("  ")); got != "" {
		t.Fatalf("RawTextBlocksContent(blank) = %q", got)
	}
	if got := RawTextBlocksContent([]byte(`"unterminated`)); got != "" {
		t.Fatalf("RawTextBlocksContent(bad string) = %q", got)
	}
	if got := RawTextBlocksContent([]byte(`5`)); got != "" {
		t.Fatalf("RawTextBlocksContent(scalar) = %q", got)
	}
	got := RawTextBlocksContent([]byte(`[5,{"type":"text","text":"hi"}]`))
	if got != "hi" {
		t.Fatalf("RawTextBlocksContent = %q, want the readable block only", got)
	}
}

func TestMapAssistantBlocks_EmptyAndReasoningOnly(t *testing.T) {
	if got := MapAssistantBlocks("", ToolCallBlockKeys{}); got != nil {
		t.Fatalf("empty string content = %#v, want nil", got)
	}
	if got := MapAssistantBlocks([]any{"not-a-map"}, ToolCallBlockKeys{}); got != nil {
		t.Fatalf("only non-map blocks = %#v, want nil", got)
	}
	got := MapAssistantBlocks([]any{map[string]any{"type": "thinking", "thinking": "deep"}}, ToolCallBlockKeys{})
	if len(got) != 1 || got[0].Content != "[thinking] deep" {
		t.Fatalf("reasoning-only message = %#v", got)
	}
}
