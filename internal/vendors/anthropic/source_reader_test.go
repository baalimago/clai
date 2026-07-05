package anthropic

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSourceReader_Discover_NoDirs(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	r := SourceReader{FS: os.DirFS("/")}
	rows, err := r.Discover(context.Background())
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("expected 0 rows, got %d", len(rows))
	}
}

func TestSourceReader_Read_MappingToolUseAndToolResult(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	// Arrange: create one Claude-style jsonl file.
	projDir := filepath.Join(os.Getenv("HOME"), ".claude", "projects", "p1")
	if err := os.MkdirAll(projDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	p := filepath.Join(projDir, "sess.jsonl")
	jsonl := "" +
		`{"type":"user","timestamp":"2026-01-01T00:00:00Z","sessionId":"s1","cwd":"/work","message":{"content":"hi"}}` + "\n" +
		`{"type":"assistant","timestamp":"2026-01-01T00:00:01Z","sessionId":"s1","message":{"content":[{"type":"thinking","thinking":"hmm"},{"type":"text","text":"ok"},{"type":"tool_use","id":"tu1","name":"rg","input":{"pattern":"x"}}]}}` + "\n" +
		`{"type":"user","timestamp":"2026-01-01T00:00:02Z","sessionId":"s1","message":{"content":[{"type":"tool_result","tool_use_id":"tu1","content":"out"}]}}` + "\n"
	if err := os.WriteFile(p, []byte(jsonl), 0o644); err != nil {
		t.Fatalf("write jsonl: %v", err)
	}

	r := SourceReader{FS: os.DirFS("/")}
	chat, err := r.Read(context.Background(), "s1")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if chat.Source != "claude-code" {
		t.Fatalf("expected source claude-code, got %q", chat.Source)
	}
	if chat.SourceID != "s1" {
		t.Fatalf("expected sourceID s1, got %q", chat.SourceID)
	}
	if chat.ID != "" {
		t.Fatalf("expected foreign read chat to have empty ID, got %q", chat.ID)
	}
	if len(chat.Messages) < 3 {
		t.Fatalf("expected at least 3 messages including injected system, got %d", len(chat.Messages))
	}
	if chat.Messages[0].Role != "system" {
		t.Fatalf("expected first message system, got %q", chat.Messages[0].Role)
	}
	// Verify assistant message includes tool call and reasoning.
	foundToolCall := false
	for _, m := range chat.Messages {
		if m.Role == "assistant" {
			if m.ReasoningContent == "" {
				t.Fatalf("expected reasoning_content to be set")
			}
			if len(m.ToolCalls) != 1 {
				t.Fatalf("expected 1 tool call, got %d", len(m.ToolCalls))
			}
			if m.ToolCalls[0].ID != "tu1" {
				t.Fatalf("expected tool call id tu1, got %q", m.ToolCalls[0].ID)
			}
			if m.ToolCalls[0].Type != "function" {
				t.Fatalf("expected tool call type function, got %q", m.ToolCalls[0].Type)
			}
			foundToolCall = true
		}
		if m.Role == "tool" {
			if m.ToolCallID != "tu1" {
				t.Fatalf("expected tool_call_id tu1, got %q", m.ToolCallID)
			}
			if m.String() != "out" {
				t.Fatalf("expected tool content 'out', got %q", m.String())
			}
		}
	}
	if !foundToolCall {
		t.Fatalf("expected to find assistant tool call message")
	}
}

func TestSourceReader_Discover_DeduceMetadata(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	projDir := filepath.Join(home, ".claude", "projects", "p1")
	if err := os.MkdirAll(projDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	p := filepath.Join(projDir, "sess.jsonl")
	jsonl := `{"type":"user","timestamp":"2026-01-01T00:00:00Z","sessionId":"s1","cwd":"/work","message":{"content":"first user msg"}}` + "\n" +
		`{"type":"assistant","timestamp":"2026-01-01T00:00:01Z","sessionId":"s1","message":{"model":"claude-fable-5","content":[{"type":"text","text":"hello"}]}}` + "\n"
	if err := os.WriteFile(p, []byte(jsonl), 0o644); err != nil {
		t.Fatalf("write jsonl: %v", err)
	}

	r := SourceReader{FS: os.DirFS("/")}
	rows, err := r.Discover(context.Background())
	if err != nil {
		t.Fatalf("discover: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	if rows[0].Source != "claude-code" {
		t.Fatalf("expected source claude-code, got %q", rows[0].Source)
	}
	if rows[0].SourceID != "s1" {
		t.Fatalf("expected sourceID s1, got %q", rows[0].SourceID)
	}
	if rows[0].FirstUserMessage == "" {
		t.Fatalf("expected preview")
	}
	if rows[0].MessageCount != 2 {
		t.Fatalf("expected message_count 2 (both lines scanned without early break), got %d", rows[0].MessageCount)
	}
	if rows[0].Model != "claude-fable-5" {
		t.Fatalf("expected model claude-fable-5, got %q", rows[0].Model)
	}
	if rows[0].Created.IsZero() {
		t.Fatalf("expected created")
	}
	// Timestamp in file is 2026; allow small delta when parsing layout.
	if rows[0].Created.Before(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("expected created to be parsed from timestamp, got %v", rows[0].Created)
	}
}

// TestSourceReader_Read_LongLine verifies the scanner can handle JSONL lines
// that exceed the default bufio.MaxScanTokenSize (64KB). Regression for the
// "bufio.Scanner: token too long" error.
func TestSourceReader_Read_LongLine(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	projDir := filepath.Join(os.Getenv("HOME"), ".claude", "projects", "p1")
	if err := os.MkdirAll(projDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	// Build a single assistant line whose raw JSON size exceeds 64KB.
	largePayload := make([]byte, 128_000)
	for i := range largePayload {
		largePayload[i] = 'x'
	}

	p := filepath.Join(projDir, "long.jsonl")
	jsonl := `{"type":"user","timestamp":"2026-01-01T00:00:00Z","sessionId":"big","cwd":"/work","message":{"content":"hi"}}` + "\n" +
		`{"type":"assistant","timestamp":"2026-01-01T00:00:01Z","sessionId":"big","message":{"content":[{"type":"text","text":"` + string(largePayload) + `"}]}}` + "\n"
	if err := os.WriteFile(p, []byte(jsonl), 0o644); err != nil {
		t.Fatalf("write jsonl: %v", err)
	}

	r := SourceReader{FS: os.DirFS("/")}
	chat, err := r.Read(context.Background(), "big")
	if err != nil {
		t.Fatalf("Read with long line: %v", err)
	}
	if len(chat.Messages) < 2 {
		t.Fatalf("expected at least 2 messages, got %d", len(chat.Messages))
	}
}

// TestSourceReader_Discover_LongLine verifies Discover survives a JSONL file where
// the first few lines are long (exceeding default scanner token).
func TestSourceReader_Discover_LongLine(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	projDir := filepath.Join(os.Getenv("HOME"), ".claude", "projects", "p1")
	if err := os.MkdirAll(projDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	// First line is long (>64KB), followed by normal metadata.
	largePayload := make([]byte, 128_000)
	for i := range largePayload {
		largePayload[i] = 'y'
	}

	p := filepath.Join(projDir, "long.jsonl")
	jsonl := `{"type":"user","timestamp":"2026-01-01T00:00:00Z","sessionId":"s1","message":{"content":"` + string(largePayload) + `"}}` + "\n" +
		`{"type":"assistant","timestamp":"2026-01-01T00:00:01Z","sessionId":"s1","message":{"content":[{"type":"text","text":"ok"}]}}` + "\n"
	if err := os.WriteFile(p, []byte(jsonl), 0o644); err != nil {
		t.Fatalf("write jsonl: %v", err)
	}

	r := SourceReader{FS: os.DirFS("/")}
	rows, err := r.Discover(context.Background())
	if err != nil {
		t.Fatalf("Discover with long line: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	if rows[0].SourceID != "s1" {
		t.Fatalf("expected sourceID s1, got %q", rows[0].SourceID)
	}
}

// TestDiscover_FullFirstUserMessage_Populated verifies that discoverOne populates
// both FirstUserMessage (truncated) and FullFirstUserMessage (complete).
func TestDiscover_FullFirstUserMessage_Populated(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	projDir := filepath.Join(home, ".claude", "projects", "p1")
	if err := os.MkdirAll(projDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// Message longer than 100 chars → truncated preview differs from full text.
	var longMsg strings.Builder
	for range 150 {
		longMsg.WriteString("x")
	}
	p := filepath.Join(projDir, "sess.jsonl")
	jsonl := `{"type":"user","timestamp":"2026-01-01T00:00:00Z","sessionId":"s1","message":{"content":"` + longMsg.String() + `"}}` + "\n"
	if err := os.WriteFile(p, []byte(jsonl), 0o644); err != nil {
		t.Fatalf("write jsonl: %v", err)
	}

	r := SourceReader{FS: os.DirFS("/")}
	rows, err := r.Discover(context.Background())
	if err != nil {
		t.Fatalf("discover: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	// FirstUserMessage should be truncated to 100 chars.
	if len(rows[0].FirstUserMessage) > 100 {
		t.Fatalf("FirstUserMessage should be truncated to ≤100 chars, got %d: %q", len(rows[0].FirstUserMessage), rows[0].FirstUserMessage)
	}
	// FullFirstUserMessage should contain the complete text.
	if rows[0].FullFirstUserMessage != longMsg.String() {
		t.Fatalf("FullFirstUserMessage mismatch: len=%d, want len=%d", len(rows[0].FullFirstUserMessage), len(longMsg.String()))
	}
}
