package antigravity

import (
	"context"
	"encoding/binary"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/baalimago/clai/internal/sqlitepage"
	"github.com/baalimago/clai/internal/vendors"
)

const (
	secTest   int64 = 1792597430
	nanosTest int64 = 600138127
)

func fixtureReader() SourceReader {
	return SourceReader{Roots: []string{
		filepath.Join("testdata", "store", "antigravity"),
		filepath.Join("testdata", "store", "antigravity-cli"),
	}}
}

func discover(t *testing.T) []vendors.SourceRow {
	t.Helper()
	rows, err := fixtureReader().Discover(context.Background(), nil)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	return rows
}

func mustRow(t *testing.T, rows []vendors.SourceRow, id string) vendors.SourceRow {
	t.Helper()
	for _, row := range rows {
		if row.SourceID == id {
			return row
		}
	}
	t.Fatalf("row %q not discovered: %+v", id, rows)
	return vendors.SourceRow{}
}

func TestSource(t *testing.T) {
	if got := (SourceReader{}).Source(); got != "antigravity" {
		t.Fatalf("Source() = %q", got)
	}
}

func TestDiscover(t *testing.T) {
	rows := discover(t)
	if len(rows) != 7 {
		t.Fatalf("got %d rows, want 7: %+v", len(rows), rows)
	}
	main := mustRow(t, rows, "main")
	if main.FirstUserMessage != "first prompt for antigravity" {
		t.Fatalf("preview = %q", main.FirstUserMessage)
	}
	if main.FullFirstUserMessage != "first prompt for antigravity" {
		t.Fatalf("full prompt = %q", main.FullFirstUserMessage)
	}
	if main.MessageCount != 4 {
		t.Fatalf("message count = %d, want 4", main.MessageCount)
	}
	if main.Cwd != "/work/dir" {
		t.Fatalf("cwd = %q", main.Cwd)
	}
	if want := time.Unix(secTest, nanosTest).UTC(); !main.Created.Equal(want) {
		t.Fatalf("created = %v, want %v", main.Created, want)
	}
	if main.RawPath == "" {
		t.Fatal("raw path is empty")
	}
	if main.Source != "antigravity" {
		t.Fatalf("source = %q", main.Source)
	}

	nometa := mustRow(t, rows, "nometa")
	if nometa.Cwd != "" {
		t.Fatalf("nometa cwd = %q, want empty", nometa.Cwd)
	}
	if nometa.FirstUserMessage != "prompt without metadata" {
		t.Fatalf("nometa preview = %q", nometa.FirstUserMessage)
	}

	nouser := mustRow(t, rows, "nouser")
	if nouser.FirstUserMessage != "" {
		t.Fatalf("nouser preview = %q, want empty", nouser.FirstUserMessage)
	}
	if nouser.MessageCount != 1 {
		t.Fatalf("nouser count = %d, want 1", nouser.MessageCount)
	}

	emptyuser := mustRow(t, rows, "emptyuser")
	if emptyuser.FirstUserMessage != "" {
		t.Fatalf("emptyuser preview = %q, want empty", emptyuser.FirstUserMessage)
	}
	if emptyuser.MessageCount != 1 {
		t.Fatalf("emptyuser count = %d, want 1", emptyuser.MessageCount)
	}
}

func TestDiscoverCountMatchesRead(t *testing.T) {
	reader := fixtureReader()
	for _, row := range discover(t) {
		chat, err := reader.Read(context.Background(), nil, row.SourceID)
		if err != nil {
			t.Fatalf("Read(%q): %v", row.SourceID, err)
		}
		if got := len(chat.Messages) - 1; got != row.MessageCount {
			t.Errorf("%s: Read has %d messages, row says %d", row.SourceID, got, row.MessageCount)
		}
	}
}

func TestDiscoverDuplicateIdentityFirstRootWins(t *testing.T) {
	rows := discover(t)
	count := 0
	var row vendors.SourceRow
	for _, r := range rows {
		if r.SourceID == "dup" {
			count++
			row = r
		}
	}
	if count != 1 {
		t.Fatalf("got %d dup rows, want 1", count)
	}
	if row.FirstUserMessage != "duplicate identity" {
		t.Fatalf("preview = %q, want the first root's", row.FirstUserMessage)
	}
	if row.Cwd != "/work/first" {
		t.Fatalf("cwd = %q, want the first root's", row.Cwd)
	}
	chat, err := fixtureReader().Read(context.Background(), nil, "dup")
	if err != nil {
		t.Fatal(err)
	}
	if got := chat.Messages[1].Content; got != "duplicate identity" {
		t.Fatalf("Read used the wrong root: %q", got)
	}
	if got := chat.Messages[2].Content; got != "first root answer" {
		t.Fatalf("Read used the wrong root answer: %q", got)
	}
}

func TestDiscoverMissingRoot(t *testing.T) {
	rows, err := (SourceReader{Roots: []string{t.TempDir()}}).Discover(context.Background(), nil)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("got %d rows, want 0", len(rows))
	}
}

func TestDiscoverRootNotDirectory(t *testing.T) {
	root := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(root, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := (SourceReader{Roots: []string{root}}).Discover(context.Background(), nil)
	if err == nil {
		t.Fatal("expected an error for a root that is not a directory")
	}
}

func TestDiscoverContextCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := fixtureReader().Discover(ctx, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

type testCache struct {
	rows    map[string]vendors.SourceRow
	infos   map[string]fs.FileInfo
	lookups int
	stores  int
}

func newTestCache() *testCache {
	return &testCache{
		rows:  map[string]vendors.SourceRow{},
		infos: map[string]fs.FileInfo{},
	}
}

func (c *testCache) Lookup(absPath string, info fs.FileInfo) (vendors.SourceRow, bool) {
	c.lookups++
	row, ok := c.rows[absPath]
	if !ok {
		return vendors.SourceRow{}, false
	}
	stored := c.infos[absPath]
	if stored == nil || stored.Size() != info.Size() || !stored.ModTime().Equal(info.ModTime()) {
		return vendors.SourceRow{}, false
	}
	return row, true
}

func (c *testCache) Store(absPath string, info fs.FileInfo, row vendors.SourceRow) {
	c.stores++
	c.rows[absPath] = row
	c.infos[absPath] = info
}

func (c *testCache) Locate(source, sourceID string) (string, bool) {
	for p, row := range c.rows {
		if row.Source == source && row.SourceID == sourceID {
			return p, true
		}
	}
	return "", false
}

func TestDiscoverUsesWarmCache(t *testing.T) {
	mainPath := filepath.Join("testdata", "store", "antigravity", "conversations", "main.db")
	info, err := stateInfoOf(mainPath)
	if err != nil {
		t.Fatal(err)
	}
	cache := newTestCache()
	cache.Store(mainPath, info, vendors.SourceRow{
		Source:               "antigravity",
		SourceID:             "main",
		FirstUserMessage:     "sentinel",
		FullFirstUserMessage: "sentinel",
		MessageCount:         1,
	})
	rows, err := fixtureReader().Discover(context.Background(), cache)
	if err != nil {
		t.Fatal(err)
	}
	if got := mustRow(t, rows, "main").FirstUserMessage; got != "sentinel" {
		t.Fatalf("Discover ignored the warm cache: %q", got)
	}
	if cache.lookups == 0 {
		t.Fatal("Discover did not consult the cache")
	}
}

func TestDiscoverStoresZeroRowOnlyForEmptyConversation(t *testing.T) {
	cache := newTestCache()
	if _, err := fixtureReader().Discover(context.Background(), cache); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join("testdata", "store", "antigravity", "conversations")
	emptyPath := filepath.Join(root, "empty.db")
	row, ok := cache.rows[emptyPath]
	if !ok {
		t.Fatal("empty conversation was not cached")
	}
	if row.SourceID != "" {
		t.Fatalf("empty conversation row = %+v, want a zero row", row)
	}
	for _, name := range []string{"malformed.db", "drift.db", "foreign.db", "junk.db"} {
		if _, ok := cache.rows[filepath.Join(root, name)]; ok {
			t.Errorf("%s was cached after a failed read", name)
		}
	}
}

func TestReadUsesCachedLocation(t *testing.T) {
	temp := t.TempDir()
	convs := filepath.Join(temp, "conversations")
	if err := os.MkdirAll(convs, 0o755); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(convs, "located.db")
	copyFile(t, filepath.Join("testdata", "store", "antigravity", "conversations", "fallback.db"), dst)
	info, err := stateInfoOf(dst)
	if err != nil {
		t.Fatal(err)
	}
	cache := newTestCache()
	cache.Store(dst, info, vendors.SourceRow{Source: "antigravity", SourceID: "cli", MessageCount: 2})
	chat, err := (SourceReader{Roots: []string{t.TempDir()}}).Read(context.Background(), cache, "cli")
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if got := chat.Messages[1].Content; got != "part one part two" {
		t.Fatalf("Read did not use the located database: %q", got)
	}
}

func TestReadMain(t *testing.T) {
	chat, err := fixtureReader().Read(context.Background(), nil, "main")
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if chat.Source != "antigravity" || chat.SourceID != "main" {
		t.Fatalf("identity = %q/%q", chat.Source, chat.SourceID)
	}
	if !strings.Contains(chat.Messages[0].Content, "Continued from Antigravity session main") {
		t.Fatalf("preamble = %q", chat.Messages[0].Content)
	}
	if !strings.Contains(chat.Messages[0].Content, "/work/dir") {
		t.Fatalf("preamble does not name the workspace: %q", chat.Messages[0].Content)
	}
	if len(chat.Messages) != 5 {
		t.Fatalf("got %d messages, want 5: %+v", len(chat.Messages), chat.Messages)
	}
	user := chat.Messages[1]
	if user.Role != "user" || user.Content != "first prompt for antigravity" {
		t.Fatalf("user = %+v", user)
	}
	assistant := chat.Messages[2]
	if assistant.Role != "assistant" || assistant.ReasoningContent != "think one" {
		t.Fatalf("assistant = %+v", assistant)
	}
	if len(assistant.ToolCalls) != 1 {
		t.Fatalf("settled calls = %d, want 1: %+v", len(assistant.ToolCalls), assistant.ToolCalls)
	}
	call := assistant.ToolCalls[0]
	if call.ID != "call_1" || call.Name != "read_file" || call.Function.Arguments != `{"Path":"/tmp/a"}` {
		t.Fatalf("call = %+v", call)
	}
	if !strings.Contains(assistant.Content, "[tool call: run_command(") {
		t.Fatalf("unpaired call was not rendered as text: %q", assistant.Content)
	}
	tool := chat.Messages[3]
	if tool.Role != "tool" || tool.ToolCallID != "call_1" || tool.Content != "file body" {
		t.Fatalf("tool = %+v", tool)
	}
	final := chat.Messages[4]
	if final.Role != "assistant" || final.Content != "final answer" || final.ReasoningContent != "think two" {
		t.Fatalf("final = %+v", final)
	}
}

func TestReadUserPartsFallback(t *testing.T) {
	chat, err := fixtureReader().Read(context.Background(), nil, "fallback")
	if err != nil {
		t.Fatal(err)
	}
	if got := chat.Messages[1].Content; got != "part one part two" {
		t.Fatalf("user = %q", got)
	}
}

func TestReadErrors(t *testing.T) {
	reader := fixtureReader()
	tests := []struct {
		name string
		id   string
		want string
	}{
		{name: "empty id", id: "", want: "empty session id"},
		{name: "unknown", id: "absent", want: "not found"},
		{name: "no messages", id: "empty", want: "has no messages"},
		{name: "malformed", id: "malformed", want: "parse antigravity step"},
		{name: "drift", id: "drift", want: "column"},
		{name: "foreign", id: "foreign"},
		{name: "junk", id: "junk"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := reader.Read(context.Background(), nil, tt.id)
			if err == nil {
				t.Fatal("expected an error")
			}
			if tt.want != "" && !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v, want it to contain %q", err, tt.want)
			}
		})
	}
}

func TestReadContextCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := fixtureReader().Read(ctx, nil, "main")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

func TestStateInfoIncludesWAL(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.db")
	if err := os.WriteFile(path, []byte("1234567890"), 0o600); err != nil {
		t.Fatal(err)
	}
	older := time.Unix(1000, 0)
	newer := time.Unix(2000, 0)
	if err := os.Chtimes(path, older, older); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path+"-wal", []byte("12345"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path+"-wal", newer, newer); err != nil {
		t.Fatal(err)
	}
	info, err := stateInfoOf(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() != 15 {
		t.Fatalf("size = %d, want 15", info.Size())
	}
	if !info.ModTime().Equal(newer) {
		t.Fatalf("mod time = %v, want %v", info.ModTime(), newer)
	}
}

func TestRootsDefaultHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dst := filepath.Join(home, ".gemini", "antigravity-cli", "conversations", "cli.db")
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		t.Fatal(err)
	}
	copyFile(t, filepath.Join("testdata", "store", "antigravity", "conversations", "main.db"), dst)
	rows, err := (SourceReader{}).Discover(context.Background(), nil)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(rows) != 1 || rows[0].SourceID != "cli" {
		t.Fatalf("rows = %+v, want the CLI root's one row", rows)
	}
}

func TestBlobOf(t *testing.T) {
	vals := []sqlitepage.Value{
		{Kind: sqlitepage.Blob, Blob: []byte("blob")},
		{Kind: sqlitepage.Text, Text: "text"},
		{Kind: sqlitepage.Integer, Int: 1},
	}
	if got, ok := blobOf(vals, 0); !ok || string(got) != "blob" {
		t.Fatalf("blob = %q/%v", got, ok)
	}
	if got, ok := blobOf(vals, 1); !ok || string(got) != "text" {
		t.Fatalf("text = %q/%v", got, ok)
	}
	if _, ok := blobOf(vals, 2); ok {
		t.Fatal("an integer column became a blob")
	}
	if _, ok := blobOf(vals, 3); ok {
		t.Fatal("an absent column became a blob")
	}
}

func TestIntAt(t *testing.T) {
	vals := []sqlitepage.Value{{Kind: sqlitepage.Integer, Int: 7}, {Kind: sqlitepage.Text, Text: "x"}}
	if got := intAt(vals, 0); got != 7 {
		t.Fatalf("intAt = %d, want 7", got)
	}
	if got := intAt(vals, 1); got != 0 {
		t.Fatalf("intAt over text = %d, want 0", got)
	}
	if got := intAt(vals, 2); got != 0 {
		t.Fatalf("intAt over an absent column = %d, want 0", got)
	}
}

func TestWorkspaceURI(t *testing.T) {
	blob := pbMsg(1, pbMsg(1, []byte("file:///work/x")))
	if got := workspaceURI(blob); got != "/work/x" {
		t.Fatalf("workspaceURI = %q", got)
	}
	if got := workspaceURI(pbMsg(1, pbMsg(1, []byte("https://example.com")))); got != "" {
		t.Fatalf("workspaceURI = %q, want empty", got)
	}
	if got := workspaceURI([]byte{0xff}); got != "" {
		t.Fatalf("workspaceURI = %q, want empty", got)
	}
}

func TestParseWireRejectsMalformedInput(t *testing.T) {
	tests := [][]byte{
		{0x08},
		{0x0a, 0x05, 'a'},
		{0x00},
		{0x0b},
		{0x09, 1, 2, 3, 4, 5, 6, 7},
		{0x0d, 1, 2, 3},
	}
	for _, input := range tests {
		if _, err := parseWire(input); err == nil {
			t.Errorf("parseWire(%x) did not fail", input)
		}
	}
}

func TestParseTimestamp(t *testing.T) {
	stamp := append(pbVarint(1, 123), pbVarint(2, 456)...)
	got := parseTimestamp(pbMsg(1, stamp))
	want := time.Unix(123, 456).UTC()
	if !got.Equal(want) {
		t.Fatalf("parseTimestamp = %v, want %v", got, want)
	}
	if got := parseTimestamp([]byte{0xff}); !got.IsZero() {
		t.Fatalf("parseTimestamp = %v, want zero", got)
	}
}

func TestParseAssistantFallsBackToAltText(t *testing.T) {
	body := append(pbMsg(8, []byte("alt text")), pbMsg(3, []byte("thought"))...)
	text, thought, calls, err := parseAssistant(body)
	if err != nil {
		t.Fatal(err)
	}
	if text != "alt text" || thought != "thought" || len(calls) != 0 {
		t.Fatalf("got text=%q thought=%q calls=%d", text, thought, len(calls))
	}
}

func TestPayloadParsersTolerateMalformedInput(t *testing.T) {
	if got := userPart([]byte{0xff}); got != "\xff" {
		t.Fatalf("userPart fallback = %q, want the raw bytes", got)
	}
	if got := parseUser([]byte{0xff}); got != "" {
		t.Fatalf("parseUser = %q, want empty", got)
	}
	if got := parseToolResult(pbMsg(2, []byte{0xff})); got != "" {
		t.Fatalf("parseToolResult = %q, want empty", got)
	}
	if got := parseToolResult(pbMsg(1, []byte("x"))); got != "" {
		t.Fatalf("parseToolResult without a body = %q, want empty", got)
	}
	if got := parseCallID(pbMsg(4, []byte{0xff})); got != "" {
		t.Fatalf("parseCallID = %q, want empty", got)
	}
	if got := parseCallID(pbMsg(1, []byte("x"))); got != "" {
		t.Fatalf("parseCallID without a call = %q, want empty", got)
	}
}

func TestParseStepRejectsTypeMismatch(t *testing.T) {
	payload := pbVarint(1, 15)
	if _, err := parseStep(rawStep{idx: 0, typ: 14, payload: payload}); err == nil {
		t.Fatal("expected a type mismatch error")
	}
}

func TestAssistantMessagesReasoningOnly(t *testing.T) {
	step := parsedStep{typ: stepAssistant, thought: "deep thought"}
	msg, toolMsgs := assistantMessages(step, map[string]string{})
	if msg == nil {
		t.Fatal("reasoning-only assistant message was dropped")
	}
	if msg.Content != "[thinking] deep thought" {
		t.Fatalf("content = %q, want [thinking] deep thought", msg.Content)
	}
	if msg.ReasoningContent != "deep thought" {
		t.Fatalf("reasoning = %q, want deep thought", msg.ReasoningContent)
	}
	if len(toolMsgs) != 0 {
		t.Fatalf("tool messages = %d, want 0", len(toolMsgs))
	}
}

func TestAssistantMessagesUnpairedCallOnly(t *testing.T) {
	step := parsedStep{typ: stepAssistant, calls: []toolCall{{id: "c", name: "tool", args: `{"a":1}`}}}
	msg, toolMsgs := assistantMessages(step, map[string]string{})
	if msg == nil {
		t.Fatal("assistant message was dropped")
	}
	if len(msg.ToolCalls) != 0 {
		t.Fatalf("unpaired call became structured: %+v", msg.ToolCalls)
	}
	if msg.Content != "[tool call: tool({\"a\":1})]" {
		t.Fatalf("content = %q", msg.Content)
	}
	if len(toolMsgs) != 0 {
		t.Fatalf("tool messages = %d, want 0", len(toolMsgs))
	}
}

func TestMapStepsIgnoresOperationalSteps(t *testing.T) {
	conv := mapSteps([]parsedStep{
		{typ: stepError},
		{typ: stepTaskNotice},
		{typ: 999},
	})
	if len(conv.messages) != 0 {
		t.Fatalf("messages = %+v, want none", conv.messages)
	}
}

func copyFile(t *testing.T, src, dst string) {
	t.Helper()
	data, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func pbVarint(num int, value uint64) []byte {
	out := binary.AppendUvarint(nil, uint64(num)<<3)
	return binary.AppendUvarint(out, value)
}

func pbMsg(num int, payload []byte) []byte {
	out := binary.AppendUvarint(nil, uint64(num)<<3|2)
	out = binary.AppendUvarint(out, uint64(len(payload)))
	return append(out, payload...)
}
