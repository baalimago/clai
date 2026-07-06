package pi

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/baalimago/clai/internal/vendors"
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

func TestSourceReader_Read_MappingToolCallAndToolResult(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	sessDir := filepath.Join(home, ".pi", "agent", "sessions", "--test--")
	if err := os.MkdirAll(sessDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	p := filepath.Join(sessDir, "2026-01-01T00-00-00Z_s1.jsonl")
	jsonl := "" +
		`{"type":"session","version":3,"id":"s1","timestamp":"2026-01-01T00:00:00Z","cwd":"/work"}` + "\n" +
		`{"type":"message","message":{"role":"user","content":[{"type":"text","text":"hi"}],"timestamp":1}}` + "\n" +
		`{"type":"message","message":{"role":"assistant","content":[{"type":"thinking","thinking":"hmm"},{"type":"text","text":"ok"},{"type":"toolCall","id":"tu1","name":"rg","arguments":{"pattern":"x"}}],"model":"deepseek-v4","api":"openai-completions"}}` + "\n" +
		`{"type":"message","message":{"role":"toolResult","toolCallId":"tu1","toolName":"rg","content":[{"type":"text","text":"out"}],"isError":false}}` + "\n"
	if err := os.WriteFile(p, []byte(jsonl), 0o644); err != nil {
		t.Fatalf("write jsonl: %v", err)
	}

	r := SourceReader{FS: os.DirFS("/")}
	chat, err := r.Read(context.Background(), "s1")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if chat.Source != "pi" {
		t.Fatalf("expected source pi, got %q", chat.Source)
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
	foundToolResult := false
	for _, m := range chat.Messages {
		if m.Role == "assistant" && len(m.ToolCalls) > 0 {
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
			if m.Content != "out" {
				t.Fatalf("expected tool content 'out', got %q", m.Content)
			}
			foundToolResult = true
		}
	}
	if !foundToolCall {
		t.Fatalf("expected to find assistant tool call message")
	}
	if !foundToolResult {
		t.Fatalf("expected to find tool result message")
	}
}

func TestSourceReader_Discover_DeduceMetadata(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	sessDir := filepath.Join(home, ".pi", "agent", "sessions", "--test--")
	if err := os.MkdirAll(sessDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	p := filepath.Join(sessDir, "2026-01-01T00-00-00Z_s1.jsonl")
	jsonl := `{"type":"session","version":3,"id":"s1","timestamp":"2026-01-01T00:00:00Z","cwd":"/work"}` + "\n" +
		`{"type":"message","message":{"role":"user","content":[{"type":"text","text":"first user msg"}],"timestamp":1}}` + "\n" +
		`{"type":"message","message":{"role":"assistant","content":[{"type":"text","text":"hello"}],"model":"deepseek-v4","api":"openai-completions"}}` + "\n"
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
	if rows[0].Source != "pi" {
		t.Fatalf("expected source pi, got %q", rows[0].Source)
	}
	if rows[0].SourceID != "s1" {
		t.Fatalf("expected sourceID s1, got %q", rows[0].SourceID)
	}
	if rows[0].FirstUserMessage == "" {
		t.Fatalf("expected preview")
	}
	if rows[0].MessageCount != 2 {
		t.Fatalf("expected message_count 2, got %d", rows[0].MessageCount)
	}
	if rows[0].Model != "deepseek-v4" {
		t.Fatalf("expected model deepseek-v4, got %q", rows[0].Model)
	}
	if rows[0].Created.IsZero() {
		t.Fatalf("expected created")
	}
	if rows[0].Created.Before(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("expected created to be parsed from timestamp, got %v", rows[0].Created)
	}
}

func TestSourceReader_Discover_FullFirstUserMessage_Populated(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	sessDir := filepath.Join(home, ".pi", "agent", "sessions", "--test--")
	if err := os.MkdirAll(sessDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	var longMsg strings.Builder
	for range 150 {
		longMsg.WriteString("x")
	}
	p := filepath.Join(sessDir, "2026-01-01T00-00-00Z_s1.jsonl")
	jsonl := `{"type":"session","version":3,"id":"s1","timestamp":"2026-01-01T00:00:00Z","cwd":"/work"}` + "\n" +
		`{"type":"message","message":{"role":"user","content":[{"type":"text","text":"` + longMsg.String() + `"}],"timestamp":1}}` + "\n"
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
	if len(rows[0].FirstUserMessage) > 100 {
		t.Fatalf("FirstUserMessage should be truncated to ≤100 chars, got %d: %q", len(rows[0].FirstUserMessage), rows[0].FirstUserMessage)
	}
	if rows[0].FullFirstUserMessage != longMsg.String() {
		t.Fatalf("FullFirstUserMessage mismatch: len=%d, want len=%d", len(rows[0].FullFirstUserMessage), len(longMsg.String()))
	}
}

func TestSourceReader_Read_LongLine(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	sessDir := filepath.Join(home, ".pi", "agent", "sessions", "--test--")
	if err := os.MkdirAll(sessDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	largePayload := make([]byte, 128_000)
	for i := range largePayload {
		largePayload[i] = 'x'
	}

	p := filepath.Join(sessDir, "2026-01-01T00-00-00Z_big.jsonl")
	jsonl := `{"type":"session","version":3,"id":"big","timestamp":"2026-01-01T00:00:00Z","cwd":"/work"}` + "\n" +
		`{"type":"message","message":{"role":"user","content":[{"type":"text","text":"hi"}],"timestamp":1}}` + "\n" +
		`{"type":"message","message":{"role":"assistant","content":[{"type":"text","text":"` + string(largePayload) + `"}],"model":"deepseek-v4","api":"openai-completions"}}` + "\n"
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

func TestSourceReader_Discover_LongLine(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	sessDir := filepath.Join(home, ".pi", "agent", "sessions", "--test--")
	if err := os.MkdirAll(sessDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	largePayload := make([]byte, 128_000)
	for i := range largePayload {
		largePayload[i] = 'y'
	}

	p := filepath.Join(sessDir, "2026-01-01T00-00-00Z_s1.jsonl")
	jsonl := `{"type":"session","version":3,"id":"s1","timestamp":"2026-01-01T00:00:00Z","cwd":"/work"}` + "\n" +
		`{"type":"message","message":{"role":"user","content":[{"type":"text","text":"` + string(largePayload) + `"}],"timestamp":1}}` + "\n" +
		`{"type":"message","message":{"role":"assistant","content":[{"type":"text","text":"ok"}],"model":"deepseek-v4","api":"openai-completions"}}` + "\n"
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

func TestSourceReader_MessageCount_IncludesToolResults(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	sessDir := filepath.Join(home, ".pi", "agent", "sessions", "--test--")
	if err := os.MkdirAll(sessDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	p := filepath.Join(sessDir, "2026-01-01T00-00-00Z_s1.jsonl")
	jsonl := `{"type":"session","version":3,"id":"s1","timestamp":"2026-01-01T00:00:00Z","cwd":"/work"}` + "\n" +
		`{"type":"message","message":{"role":"user","content":[{"type":"text","text":"hi"}],"timestamp":1}}` + "\n" +
		`{"type":"message","message":{"role":"assistant","content":[{"type":"toolCall","id":"tc1","name":"read","arguments":{}}],"model":"gpt-5","api":"openai-completions"}}` + "\n" +
		`{"type":"message","message":{"role":"toolResult","toolCallId":"tc1","toolName":"read","content":[{"type":"text","text":"file content"}],"isError":false}}` + "\n" +
		`{"type":"message","message":{"role":"assistant","content":[{"type":"text","text":"done"}],"model":"gpt-5","api":"openai-completions"}}` + "\n"
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
	// user + assistant(toolCall) + toolResult + assistant(text) = 4
	if rows[0].MessageCount != 4 {
		t.Fatalf("expected message_count 4, got %d", rows[0].MessageCount)
	}
}

func TestSourceReader_Source(t *testing.T) {
	r := SourceReader{}
	if r.Source() != "pi" {
		t.Fatalf("expected 'pi', got %q", r.Source())
	}
}

func TestSourceReader_Read_SystemMessageIncludesCwd(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	sessDir := filepath.Join(home, ".pi", "agent", "sessions", "--test--")
	if err := os.MkdirAll(sessDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	p := filepath.Join(sessDir, "2026-01-01T00-00-00Z_s1.jsonl")
	jsonl := `{"type":"session","version":3,"id":"s1","timestamp":"2026-01-01T00:00:00Z","cwd":"/home/user/project"}` + "\n" +
		`{"type":"message","message":{"role":"user","content":[{"type":"text","text":"hello"}],"timestamp":1}}` + "\n"
	if err := os.WriteFile(p, []byte(jsonl), 0o644); err != nil {
		t.Fatalf("write jsonl: %v", err)
	}

	r := SourceReader{FS: os.DirFS("/")}
	chat, err := r.Read(context.Background(), "s1")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.Contains(chat.Messages[0].Content, "/home/user/project") {
		t.Fatalf("expected system message to include cwd, got: %q", chat.Messages[0].Content)
	}
}

func TestSourceReader_Read_MissingCwd_NoCrash(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	sessDir := filepath.Join(home, ".pi", "agent", "sessions", "--test--")
	if err := os.MkdirAll(sessDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	p := filepath.Join(sessDir, "2026-01-01T00-00-00Z_s1.jsonl")
	// session line without cwd field
	jsonl := `{"type":"session","version":3,"id":"s1","timestamp":"2026-01-01T00:00:00Z"}` + "\n" +
		`{"type":"message","message":{"role":"user","content":[{"type":"text","text":"hello"}],"timestamp":1}}` + "\n"
	if err := os.WriteFile(p, []byte(jsonl), 0o644); err != nil {
		t.Fatalf("write jsonl: %v", err)
	}

	r := SourceReader{FS: os.DirFS("/")}
	chat, err := r.Read(context.Background(), "s1")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.Contains(chat.Messages[0].Content, "Pi session s1") {
		t.Fatalf("expected system message to mention session ID, got: %q", chat.Messages[0].Content)
	}
}

func TestSourceReader_Read_SkipsLinesBeforeSession(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	sessDir := filepath.Join(home, ".pi", "agent", "sessions", "--test--")
	if err := os.MkdirAll(sessDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	p := filepath.Join(sessDir, "2026-01-01T00-00-00Z_s1.jsonl")
	// model_change before session line — should be skipped entirely
	jsonl := `{"type":"model_change","id":"x","provider":"deepseek"}` + "\n" +
		`{"type":"session","version":3,"id":"s1","timestamp":"2026-01-01T00:00:00Z","cwd":"/work"}` + "\n" +
		`{"type":"message","message":{"role":"user","content":[{"type":"text","text":"hello"}],"timestamp":1}}` + "\n"
	if err := os.WriteFile(p, []byte(jsonl), 0o644); err != nil {
		t.Fatalf("write jsonl: %v", err)
	}

	r := SourceReader{FS: os.DirFS("/")}
	chat, err := r.Read(context.Background(), "s1")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	// Should have system + 1 user message = 2
	if len(chat.Messages) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(chat.Messages))
	}
}

func TestSourceReader_Discover_SkipsFileWithoutSessionID(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	sessDir := filepath.Join(home, ".pi", "agent", "sessions", "--test--")
	if err := os.MkdirAll(sessDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	p := filepath.Join(sessDir, "2026-01-01T00-00-00Z_noID.jsonl")
	// No session line with id
	jsonl := `{"type":"message","message":{"role":"user","content":[{"type":"text","text":"hi"}],"timestamp":1}}` + "\n"
	if err := os.WriteFile(p, []byte(jsonl), 0o644); err != nil {
		t.Fatalf("write jsonl: %v", err)
	}

	r := SourceReader{FS: os.DirFS("/")}
	rows, err := r.Discover(context.Background())
	if err != nil {
		t.Fatalf("discover: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("expected 0 rows (no session id), got %d", len(rows))
	}
}

func TestSourceReader_Discover_MultipleSessions(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	sessDir := filepath.Join(home, ".pi", "agent", "sessions", "--test--")
	if err := os.MkdirAll(sessDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	writeSession := func(id, timestamp, msg string) {
		p := filepath.Join(sessDir, timestamp+"_"+id+".jsonl")
		jsonl := `{"type":"session","version":3,"id":"` + id + `","timestamp":"` + timestamp + `","cwd":"/work"}` + "\n" +
			`{"type":"message","message":{"role":"user","content":[{"type":"text","text":"` + msg + `"}],"timestamp":1}}` + "\n"
		if err := os.WriteFile(p, []byte(jsonl), 0o644); err != nil {
			t.Fatalf("write jsonl: %v", err)
		}
	}

	writeSession("s1", "2026-01-01T00:00:00Z", "first")
	writeSession("s2", "2026-01-02T00:00:00Z", "second")

	r := SourceReader{FS: os.DirFS("/")}
	rows, err := r.Discover(context.Background())
	if err != nil {
		t.Fatalf("discover: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(rows))
	}
}

func TestSourceReader_Discover_TimestampWithNanos(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	sessDir := filepath.Join(home, ".pi", "agent", "sessions", "--test--")
	if err := os.MkdirAll(sessDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	p := filepath.Join(sessDir, "2026-01-01T00-00-00.123456789Z_s1.jsonl")
	jsonl := `{"type":"session","version":3,"id":"s1","timestamp":"2026-01-01T00:00:00.123456789Z","cwd":"/work"}` + "\n" +
		`{"type":"message","message":{"role":"user","content":[{"type":"text","text":"hi"}],"timestamp":1}}` + "\n"
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
	if rows[0].Created.IsZero() {
		t.Fatalf("expected created to be parsed from nano timestamp")
	}
}

func TestPiUserContent_MultipleTextBlocks(t *testing.T) {
	msg := map[string]any{
		"content": []any{
			map[string]any{"type": "text", "text": "hello"},
			map[string]any{"type": "text", "text": "world"},
		},
	}
	full := vendors.TextBlocksContent(msg["content"])
	preview := vendors.TruncateOneLine(full, 100)
	if preview != "hello world" {
		t.Fatalf("expected 'hello world', got %q", preview)
	}
	if full != "hello\nworld" {
		t.Fatalf("expected 'hello\\nworld', got %q", full)
	}
}

func TestPiUserContent_NoTextBlocks(t *testing.T) {
	msg := map[string]any{
		"content": []any{
			map[string]any{"type": "image", "url": "http://example.com"},
		},
	}
	if got := vendors.TextBlocksContent(msg["content"]); got != "" {
		t.Fatalf("expected empty content, got %q", got)
	}
	if got := mapPiUserMessage(msg); got != nil {
		t.Fatalf("expected no user message for text-free content, got %+v", got)
	}
}

func TestMapPiAssistantMessage_ToolCallArgumentsMarshal(t *testing.T) {
	msg := map[string]any{
		"content": []any{
			map[string]any{"type": "toolCall", "id": "tc1", "name": "read", "arguments": map[string]any{"path": "/etc/hosts"}},
		},
	}
	msgs := vendors.MapAssistantBlocks(msg["content"], toolCallKeys)
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	if len(msgs[0].ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(msgs[0].ToolCalls))
	}
	if msgs[0].ToolCalls[0].Function.Arguments != `{"path":"/etc/hosts"}` {
		t.Fatalf("expected marshalled args, got %q", msgs[0].ToolCalls[0].Function.Arguments)
	}
}
