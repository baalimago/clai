package gemini

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/baalimago/clai/internal/vendors"
	"github.com/baalimago/clai/internal/vendors/jsonltest"
)

const geminiTestMeta = `{"sessionId":"s1","projectHash":"h","startTime":"2026-01-02T03:04:05Z","summary":"Fix the build","kind":"main"}`

func writeGeminiSession(t *testing.T, home, slug, name string, lines ...string) string {
	t.Helper()
	dir := filepath.Join(home, "tmp", slug, "chats")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	return path
}

func writeGeminiProjectRoot(t *testing.T, home, slug, root string) {
	t.Helper()
	dir := filepath.Join(home, "tmp", slug)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".project_root"), []byte(root+"\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
}

func TestSourceReader_Source(t *testing.T) {
	if got := (SourceReader{}).Source(); got != "gemini" {
		t.Fatalf("Source() = %q, want gemini", got)
	}
}

func TestSourceReader_DiscoverAndRead(t *testing.T) {
	home := t.TempDir()
	writeGeminiProjectRoot(t, home, "slug", "/work/project")
	writeGeminiSession(t, home, "slug", "session-a.jsonl",
		geminiTestMeta,
		`{"id":"u1","timestamp":"2026-01-02T03:04:06Z","type":"user","content":"hello"}`,
		`{"id":"g1","timestamp":"2026-01-02T03:04:07Z","type":"gemini","content":[{"text":"hi"}],"model":"gemini-2.5-pro","toolCalls":[{"id":"c1","name":"read_file","args":{"path":"a.go"},"status":"success","result":"package a"}]}`,
	)
	r := SourceReader{Root: home}
	rows, err := r.Discover(context.Background(), nil)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(rows))
	}
	row := rows[0]
	if row.Source != "gemini" || row.SourceID != "s1" {
		t.Fatalf("row identity = %q/%q", row.Source, row.SourceID)
	}
	if row.MessageCount != 3 {
		t.Fatalf("MessageCount = %d, want 3", row.MessageCount)
	}
	if row.FirstUserMessage != "Fix the build" {
		t.Fatalf("FirstUserMessage = %q", row.FirstUserMessage)
	}
	if row.FullFirstUserMessage != "hello" {
		t.Fatalf("FullFirstUserMessage = %q", row.FullFirstUserMessage)
	}
	if row.Cwd != "/work/project" {
		t.Fatalf("Cwd = %q", row.Cwd)
	}
	if row.Model != "gemini-2.5-pro" {
		t.Fatalf("Model = %q", row.Model)
	}
	if row.Created.IsZero() {
		t.Fatal("Created is zero")
	}

	chat, err := r.Read(context.Background(), nil, "s1")
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if chat.Source != "gemini" || chat.SourceID != "s1" {
		t.Fatalf("chat identity = %q/%q", chat.Source, chat.SourceID)
	}
	if len(chat.Messages) != row.MessageCount+1 {
		t.Fatalf("messages = %d, want count+1 = %d", len(chat.Messages), row.MessageCount+1)
	}
	if chat.Messages[0].Role != "system" || !strings.Contains(chat.Messages[0].Content, "s1") {
		t.Fatalf("preamble = %#v", chat.Messages[0])
	}
	if chat.Messages[1].Role != "user" || chat.Messages[1].Content != "hello" {
		t.Fatalf("user = %#v", chat.Messages[1])
	}
	if len(chat.Messages[2].ToolCalls) != 1 || chat.Messages[2].ToolCalls[0].ID != "c1" {
		t.Fatalf("assistant calls = %#v", chat.Messages[2].ToolCalls)
	}
	if chat.Messages[3].Role != "tool" || chat.Messages[3].ToolCallID != "c1" || chat.Messages[3].Content != "package a" {
		t.Fatalf("tool = %#v", chat.Messages[3])
	}
}

func TestSourceReader_Replay(t *testing.T) {
	tests := []struct {
		name  string
		lines []string
		want  []string
	}{
		{
			name:  "rewind drops the rewound record and later",
			lines: []string{geminiTestMeta, `{"id":"u1","type":"user","content":"one"}`, `{"id":"g1","type":"gemini","content":"a"}`, `{"id":"u2","type":"user","content":"two"}`, `{"id":"g2","type":"gemini","content":"b"}`, `{"$rewindTo":"u2"}`},
			want:  []string{"u1", "g1"},
		},
		{
			name:  "rewind to an unknown id clears",
			lines: []string{geminiTestMeta, `{"id":"u1","type":"user","content":"one"}`, `{"$rewindTo":"missing"}`},
			want:  nil,
		},
		{
			name:  "set messages replaces history",
			lines: []string{geminiTestMeta, `{"id":"u1","type":"user","content":"one"}`, `{"id":"g1","type":"gemini","content":"a"}`, `{"$set":{"messages":[{"id":"x1","type":"user","content":"reset"}]}}`},
			want:  []string{"x1"},
		},
		{
			name:  "removeIds removes one message",
			lines: []string{geminiTestMeta, `{"id":"u1","type":"user","content":"one"}`, `{"id":"g1","type":"gemini","content":"a"}`, `{"id":"u2","type":"user","content":"two"}`, `{"$patch":{"removeIds":["u1"]}}`},
			want:  []string{"g1", "u2"},
		},
		{
			name:  "orderIds moves named ids to the end",
			lines: []string{geminiTestMeta, `{"id":"u1","type":"user","content":"one"}`, `{"id":"g1","type":"gemini","content":"a"}`, `{"id":"u2","type":"user","content":"two"}`, `{"$patch":{"orderIds":["u2","u1"]}}`},
			want:  []string{"g1", "u2", "u1"},
		},
		{
			name:  "duplicate id replaces in place",
			lines: []string{geminiTestMeta, `{"id":"u1","type":"user","content":"one"}`, `{"id":"g1","type":"gemini","content":"a"}`, `{"id":"u1","type":"user","content":"replaced"}`},
			want:  []string{"u1", "g1"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			home := t.TempDir()
			path := writeGeminiSession(t, home, "slug", "session.jsonl", tt.lines...)
			conv, ok, err := (SourceReader{Root: home}).readConversation(path)
			if err != nil || !ok {
				t.Fatalf("readConversation = %v, %v", ok, err)
			}
			got := make([]string, 0, len(conv.messages))
			for _, msg := range conv.messages {
				got = append(got, msg.ID)
			}
			if strings.Join(got, ",") != strings.Join(tt.want, ",") {
				t.Fatalf("ids = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestSourceReader_PatchResult(t *testing.T) {
	home := t.TempDir()
	path := writeGeminiSession(t, home, "slug", "session.jsonl",
		geminiTestMeta,
		`{"id":"u1","type":"user","content":"read it"}`,
		`{"id":"g1","type":"gemini","content":[{"functionCall":{"id":"c1","name":"read_file","args":{"path":"a.go"}}}]}`,
		`{"$patch":{"id":"g1","toolCalls":[{"id":"c1","result":"file body"}]}}`,
	)
	conv, ok, err := (SourceReader{Root: home}).readConversation(path)
	if err != nil || !ok {
		t.Fatalf("readConversation = %v, %v", ok, err)
	}
	msgs := conv.mapped()
	if len(msgs) != 3 {
		t.Fatalf("messages = %d, want 3: %#v", len(msgs), msgs)
	}
	if len(msgs[1].ToolCalls) != 1 || msgs[1].ToolCalls[0].ID != "c1" {
		t.Fatalf("call = %#v", msgs[1].ToolCalls)
	}
	if msgs[2].Role != "tool" || msgs[2].Content != "file body" {
		t.Fatalf("tool = %#v", msgs[2])
	}
}

func TestSourceReader_IgnoredUserContent(t *testing.T) {
	home := t.TempDir()
	writeGeminiSession(t, home, "slug", "session.jsonl",
		geminiTestMeta,
		`{"id":"u1","type":"user","content":"/help"}`,
		`{"id":"u2","type":"user","content":"?why"}`,
		`{"id":"u3","type":"user","content":"<session_context>x"}`,
		`{"id":"u4","type":"user","content":"<hook_context>x"}`,
		`{"id":"u5","type":"user","content":"   "}`,
		`{"id":"u6","type":"user","content":"real prompt"}`,
		`{"id":"g1","type":"gemini","content":"answer"}`,
	)
	rows, err := (SourceReader{Root: home}).Discover(context.Background(), nil)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(rows) != 1 || rows[0].MessageCount != 2 {
		t.Fatalf("rows = %#v", rows)
	}
	if rows[0].FullFirstUserMessage != "real prompt" {
		t.Fatalf("full = %q", rows[0].FullFirstUserMessage)
	}
}

func TestSourceReader_Subagent(t *testing.T) {
	home := t.TempDir()
	writeGeminiSession(t, home, "slug", "session.jsonl",
		`{"sessionId":"sub","kind":"subagent","startTime":"2026-01-02T03:04:05Z"}`,
		`{"id":"u1","type":"user","content":"child task"}`,
		`{"id":"g1","type":"gemini","content":"child answer"}`,
	)
	r := SourceReader{Root: home}
	rows, err := r.Discover(context.Background(), nil)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("subagent produced rows: %#v", rows)
	}
	_, err = r.Read(context.Background(), nil, "sub")
	if err == nil || !strings.Contains(err.Error(), "subagent") {
		t.Fatalf("Read error = %v, want subagent", err)
	}
}

func TestSourceReader_LegacyJSON(t *testing.T) {
	home := t.TempDir()
	writeGeminiSession(t, home, "slug", "session-old.json",
		`{"sessionId":"legacy","startTime":"2025-05-06T07:08:09Z","summary":"old session","messages":[{"id":"u1","type":"user","content":"old question"},{"id":"g1","type":"gemini","content":"old answer"}]}`,
	)
	r := SourceReader{Root: home}
	rows, err := r.Discover(context.Background(), nil)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(rows) != 1 || rows[0].SourceID != "legacy" || rows[0].MessageCount != 2 {
		t.Fatalf("rows = %#v", rows)
	}
	if rows[0].FirstUserMessage != "old session" {
		t.Fatalf("preview = %q", rows[0].FirstUserMessage)
	}
	chat, err := r.Read(context.Background(), nil, "legacy")
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(chat.Messages) != 3 || chat.Messages[1].Content != "old question" || chat.Messages[2].Content != "old answer" {
		t.Fatalf("messages = %#v", chat.Messages)
	}
}

func TestSourceReader_JunkFiles(t *testing.T) {
	home := t.TempDir()
	writeGeminiSession(t, home, "slug", "junk.json", `{not json`)
	writeGeminiSession(t, home, "slug", "junk.jsonl", `{not json`, `{"sessionId":`)
	r := SourceReader{Root: home}
	rows, err := r.Discover(context.Background(), nil)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("junk produced rows: %#v", rows)
	}
}

func TestSourceReader_ReasoningOnlyAssistant(t *testing.T) {
	home := t.TempDir()
	path := writeGeminiSession(t, home, "slug", "session.jsonl",
		geminiTestMeta,
		`{"id":"u1","type":"user","content":"prompt"}`,
		`{"id":"g1","type":"gemini","content":[{"text":"deep thought","thought":true}]}`,
	)
	conv, ok, err := (SourceReader{Root: home}).readConversation(path)
	if err != nil || !ok {
		t.Fatalf("readConversation = %v, %v", ok, err)
	}
	msgs := conv.mapped()
	if len(msgs) != 2 {
		t.Fatalf("messages = %d, want 2: %#v", len(msgs), msgs)
	}
	if msgs[1].Content != "[thinking] deep thought" || msgs[1].ReasoningContent != "deep thought" {
		t.Fatalf("reasoning-only assistant = %#v", msgs[1])
	}
}

func TestSourceReader_ToolPairing(t *testing.T) {
	home := t.TempDir()
	writeGeminiSession(t, home, "slug", "session.jsonl",
		geminiTestMeta,
		`{"id":"u1","type":"user","content":"do it"}`,
		`{"id":"g1","type":"gemini","content":[{"text":"plan","thought":true},{"text":"answer"},{"functionCall":{"id":"c1","name":"read_file","args":{"path":"a"}}}],"toolCalls":[{"id":"c1","name":"read_file","args":{"path":"a"},"status":"executing","result":"body"}]}`,
		`{"id":"g2","type":"gemini","content":[{"functionCall":{"id":"c2","name":"write_file","args":{}}}]}`,
		`{"id":"g3","type":"gemini","content":[{"functionCall":{"name":"no_id","args":{}}}]}`,
		`{"id":"g4","type":"gemini","content":[{"functionCall":{"id":"c4","name":"array","args":{}}}],"toolCalls":[{"id":"c4","name":"array","result":[{"text":"one"},{"text":"two"}]}]}`,
		`{"id":"g5","type":"gemini","content":[{"functionCall":{"id":"c5","name":"call5","args":{}}}],"toolCalls":[{"id":"c5","name":"call5","result":{"functionResponse":{"response":{"output":"output string"}}}}]}`,
		`{"id":"g6","type":"gemini","content":[{"functionCall":{"id":"c6","name":"call6","args":{}}}],"toolCalls":[{"id":"c6","name":"call6","result":[{"functionResponse":{"response":{"output":{"status":"ok"}}}}]}]}`,
	)
	conv, ok, err := (SourceReader{Root: home}).readConversation(filepath.Join(home, "tmp", "slug", "chats", "session.jsonl"))
	if err != nil || !ok {
		t.Fatalf("readConversation = %v, %v", ok, err)
	}
	msgs := conv.mapped()
	if len(msgs) != 11 {
		t.Fatalf("messages = %d, want 11: %#v", len(msgs), msgs)
	}
	if msgs[1].Content != "answer" || msgs[1].ReasoningContent != "plan" {
		t.Fatalf("assistant = %#v", msgs[1])
	}
	if len(msgs[1].ToolCalls) != 1 || msgs[1].ToolCalls[0].ID != "c1" || msgs[2].Content != "body" {
		t.Fatalf("paired = %#v / %#v", msgs[1], msgs[2])
	}
	if msgs[3].Content != `[tool call: write_file({})]` || len(msgs[3].ToolCalls) != 0 {
		t.Fatalf("unsettled = %#v", msgs[3])
	}
	if msgs[4].Content != `[tool call: no_id({})]` {
		t.Fatalf("missing id = %#v", msgs[4])
	}
	if msgs[5].Content != "" || len(msgs[5].ToolCalls) != 1 {
		t.Fatalf("array call = %#v", msgs[5])
	}
	if msgs[6].Content != "one\ntwo" {
		t.Fatalf("array result = %#v", msgs[6])
	}
	if msgs[7].Content != "" || len(msgs[7].ToolCalls) != 1 || msgs[8].Content != "output string" {
		t.Fatalf("functionResponse string result = %#v / %#v", msgs[7], msgs[8])
	}
	if msgs[9].Content != "" || len(msgs[9].ToolCalls) != 1 || msgs[10].Content != `{"status":"ok"}` {
		t.Fatalf("functionResponse object result = %#v / %#v", msgs[9], msgs[10])
	}
}

func TestSourceReader_Errors(t *testing.T) {
	home := t.TempDir()
	writeGeminiSession(t, home, "slug", "empty.jsonl",
		geminiTestMeta,
		`{"id":"i1","type":"info","content":"notice"}`,
	)
	r := SourceReader{Root: home}
	if _, err := r.Read(context.Background(), nil, ""); err == nil {
		t.Fatal("Read with empty id returned nil")
	}
	if _, err := r.Read(context.Background(), nil, "missing"); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("Read missing = %v", err)
	}
	if _, err := r.Read(context.Background(), nil, "s1"); err == nil || !strings.Contains(err.Error(), "no messages") {
		t.Fatalf("Read empty = %v", err)
	}
	missing := SourceReader{Root: filepath.Join(home, "does-not-exist")}
	rows, err := missing.Discover(context.Background(), nil)
	if err != nil || len(rows) != 0 {
		t.Fatalf("missing root = %#v, %v", rows, err)
	}
}

func TestSourceReader_ContextCanceled(t *testing.T) {
	home := t.TempDir()
	writeGeminiSession(t, home, "slug", "session.jsonl", geminiTestMeta, `{"id":"u1","type":"user","content":"x"}`, `{"id":"g1","type":"gemini","content":"y"}`)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := (SourceReader{Root: home}).Discover(ctx, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("Discover error = %v, want canceled", err)
	}
}

func TestSourceReader_Cache(t *testing.T) {
	home := t.TempDir()
	session := writeGeminiSession(t, home, "slug", "session.jsonl",
		geminiTestMeta,
		`{"id":"u1","type":"user","content":"hello"}`,
		`{"id":"g1","type":"gemini","content":"hi"}`,
	)
	cache := newGeminiTestCache()

	fs1, counts1 := jsonltest.CountingFS("/")
	r1 := SourceReader{Root: home, FS: fs1}
	rows, err := r1.Discover(context.Background(), cache)
	if err != nil || len(rows) != 1 {
		t.Fatalf("first Discover = %#v, %v", rows, err)
	}
	if counts1.TotalOpens() == 0 {
		t.Fatal("first Discover opened nothing")
	}

	fs2, counts2 := jsonltest.CountingFS("/")
	r2 := SourceReader{Root: home, FS: fs2}
	rows, err = r2.Discover(context.Background(), cache)
	if err != nil || len(rows) != 1 {
		t.Fatalf("second Discover = %#v, %v", rows, err)
	}
	if got := counts2.Opens(strings.TrimPrefix(session, "/")); got != 0 {
		t.Fatalf("cache hit opened the session %d times", got)
	}
	if counts2.TotalOpens() != 0 {
		t.Fatalf("cache hit opened %d files", counts2.TotalOpens())
	}

	chat, err := r2.Read(context.Background(), cache, "s1")
	if err != nil {
		t.Fatalf("Read through cache: %v", err)
	}
	if len(chat.Messages) != 3 {
		t.Fatalf("messages = %d", len(chat.Messages))
	}
}

func TestSourceReader_ZeroRowCached(t *testing.T) {
	home := t.TempDir()
	writeGeminiSession(t, home, "slug", "sub.jsonl",
		`{"sessionId":"sub","kind":"subagent"}`,
		`{"id":"u1","type":"user","content":"child"}`,
	)
	cache := newGeminiTestCache()
	fs1, counts1 := jsonltest.CountingFS("/")
	r1 := SourceReader{Root: home, FS: fs1}
	if rows, err := r1.Discover(context.Background(), cache); err != nil || len(rows) != 0 {
		t.Fatalf("first Discover = %#v, %v", rows, err)
	}
	if counts1.TotalOpens() == 0 {
		t.Fatal("first Discover opened nothing")
	}
	fs2, counts2 := jsonltest.CountingFS("/")
	r2 := SourceReader{Root: home, FS: fs2}
	if rows, err := r2.Discover(context.Background(), cache); err != nil || len(rows) != 0 {
		t.Fatalf("second Discover = %#v, %v", rows, err)
	}
	if counts2.TotalOpens() != 0 {
		t.Fatalf("zero row was not cached: %d opens", counts2.TotalOpens())
	}
}

func TestSourceReader_ProjectRootMissing(t *testing.T) {
	home := t.TempDir()
	writeGeminiSession(t, home, "slug", "session.jsonl",
		geminiTestMeta,
		`{"id":"u1","type":"user","content":"hello"}`,
		`{"id":"g1","type":"gemini","content":"hi"}`,
	)
	rows, err := (SourceReader{Root: home}).Discover(context.Background(), nil)
	if err != nil || len(rows) != 1 {
		t.Fatalf("Discover = %#v, %v", rows, err)
	}
	if rows[0].Cwd != "" {
		t.Fatalf("Cwd = %q, want empty", rows[0].Cwd)
	}
}

func TestSourceReader_PartialMetadataAndPreviewFallback(t *testing.T) {
	home := t.TempDir()
	writeGeminiSession(t, home, "slug", "session.jsonl",
		`{"sessionId":"s1","projectHash":"h","startTime":"2026-01-02T03:04:05Z"}`,
		`{"id":"g1","type":"gemini","content":"only an answer","model":"m1"}`,
		`{"id":"u1","type":"user","content":"later prompt"}`,
	)
	rows, err := (SourceReader{Root: home}).Discover(context.Background(), nil)
	if err != nil || len(rows) != 1 {
		t.Fatalf("Discover = %#v, %v", rows, err)
	}
	if rows[0].FirstUserMessage != "later prompt" || rows[0].FullFirstUserMessage != "later prompt" {
		t.Fatalf("preview = %q / %q", rows[0].FirstUserMessage, rows[0].FullFirstUserMessage)
	}
	if rows[0].Model != "m1" {
		t.Fatalf("Model = %q", rows[0].Model)
	}
}

func TestSourceReader_PartialMetadataMessagesAppend(t *testing.T) {
	home := t.TempDir()
	path := writeGeminiSession(t, home, "slug", "session.jsonl",
		`{"sessionId":"s1","projectHash":"h","startTime":"2026-01-02T03:04:05Z"}`,
		`{"sessionId":"s1","messages":[{"id":"u1","type":"user","content":"one"}]}`,
		`{"id":"u2","type":"user","content":"two"}`,
	)
	conv, ok, err := (SourceReader{Root: home}).readConversation(path)
	if err != nil || !ok {
		t.Fatalf("read = %v, %v", ok, err)
	}
	if len(conv.messages) != 2 || conv.messages[0].ID != "u1" || conv.messages[1].ID != "u2" {
		t.Fatalf("messages = %#v", conv.messages)
	}
}

type geminiTestCache struct {
	mu      sync.Mutex
	rows    map[string]vendors.SourceRow
	infos   map[string]fs.FileInfo
	located map[string]string
}

func newGeminiTestCache() *geminiTestCache {
	return &geminiTestCache{
		rows:    map[string]vendors.SourceRow{},
		infos:   map[string]fs.FileInfo{},
		located: map[string]string{},
	}
}

func (c *geminiTestCache) Lookup(absPath string, info fs.FileInfo) (vendors.SourceRow, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	row, ok := c.rows[absPath]
	stored, okInfo := c.infos[absPath]
	if !ok || !okInfo || stored.Size() != info.Size() || !stored.ModTime().Equal(info.ModTime()) {
		return vendors.SourceRow{}, false
	}
	return row, true
}

func (c *geminiTestCache) Store(absPath string, info fs.FileInfo, row vendors.SourceRow) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.rows[absPath] = row
	c.infos[absPath] = info
	if row.SourceID != "" {
		c.located[row.Source+"\x00"+row.SourceID] = absPath
	}
}

func (c *geminiTestCache) Locate(source, sourceID string) (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	path, ok := c.located[source+"\x00"+sourceID]
	return path, ok
}

func TestSourceReader_CreatedFallsBackToModTime(t *testing.T) {
	home := t.TempDir()
	path := writeGeminiSession(t, home, "slug", "session.jsonl",
		`{"sessionId":"s1","projectHash":"h"}`,
		`{"id":"u1","type":"user","content":"hello"}`,
		`{"id":"g1","type":"gemini","content":"hi"}`,
	)
	mod := time.Date(2026, 2, 3, 4, 5, 6, 0, time.UTC)
	if err := os.Chtimes(path, mod, mod); err != nil {
		t.Fatalf("chtimes: %v", err)
	}
	rows, err := (SourceReader{Root: home}).Discover(context.Background(), nil)
	if err != nil || len(rows) != 1 {
		t.Fatalf("Discover = %#v, %v", rows, err)
	}
	if !rows[0].Created.Equal(mod) {
		t.Fatalf("Created = %v, want %v", rows[0].Created, mod)
	}
}

func TestSourceReader_Home(t *testing.T) {
	t.Run("explicit root", func(t *testing.T) {
		r := SourceReader{Root: "/custom/root"}
		if got := r.home(); got != "/custom/root" {
			t.Fatalf("home() = %q, want /custom/root", got)
		}
	})

	t.Run("sandbox-exec", func(t *testing.T) {
		t.Setenv("SANDBOX", "sandbox-exec")
		r := SourceReader{}
		if got := r.home(); !strings.Contains(got, filepath.Join(".cache", ".gemini")) {
			t.Fatalf("home() = %q, want containing .cache/.gemini", got)
		}
	})

	t.Run("default", func(t *testing.T) {
		t.Setenv("SANDBOX", "")
		r := SourceReader{}
		if got := r.home(); !strings.Contains(got, ".gemini") || strings.Contains(got, ".cache") {
			t.Fatalf("home() = %q, want containing .gemini without .cache", got)
		}
	})
}
