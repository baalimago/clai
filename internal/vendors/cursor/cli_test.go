package cursor

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/baalimago/clai/internal/sqlitepage"
)

func cliReader(t *testing.T) SourceReader {
	t.Helper()
	return SourceReader{
		DBPath:  filepath.Join(t.TempDir(), "absent.vscdb"),
		CLIRoot: filepath.Join("testdata", "cli"),
	}
}

func copyCLIFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	src := filepath.Join("testdata", "cli", "workspace-a", "chat-cli-1")
	dst := filepath.Join(root, "workspace-a", "chat-cli-1")
	if err := os.MkdirAll(dst, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"store.db", "meta.json"} {
		b, err := os.ReadFile(filepath.Join(src, name))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dst, name), b, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestDiscoverCLI(t *testing.T) {
	rows, err := cliReader(t).Discover(context.Background(), nil)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("want 1 row, got %d: %+v", len(rows), rows)
	}
	got := rows[0]
	if got.Source != "cursor" || got.SourceID != "cli:chat-cli-1" {
		t.Fatalf("identity: %+v", got)
	}
	if got.FirstUserMessage != "CLI review" {
		t.Fatalf("preview = %q", got.FirstUserMessage)
	}
	if got.FullFirstUserMessage != "Review the CLI reader" {
		t.Fatalf("full preview = %q", got.FullFirstUserMessage)
	}
	if got.MessageCount != 6 {
		t.Fatalf("message count = %d", got.MessageCount)
	}
	if got.Model != "" {
		t.Fatalf("model = %q", got.Model)
	}
	if got.Cwd != "/home/dev/cli" {
		t.Fatalf("cwd = %q", got.Cwd)
	}
	if want := time.UnixMilli(1755000000000); !got.Created.Equal(want) {
		t.Fatalf("created = %v, want %v", got.Created, want)
	}
	if !strings.HasSuffix(got.RawPath, "store.db") {
		t.Fatalf("raw path = %q", got.RawPath)
	}
}

func TestDiscoverAggregatesGUIAndCLI(t *testing.T) {
	r := testReader(t)
	r.CLIRoot = filepath.Join("testdata", "cli")
	rows, err := r.Discover(context.Background(), nil)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(rows) != 5 {
		t.Fatalf("want 5 rows (4 IDE + 1 CLI), got %d", len(rows))
	}
	ids := map[string]bool{}
	for _, row := range rows {
		ids[row.SourceID] = true
	}
	if !ids["composer-1"] || !ids["cli:chat-cli-1"] {
		t.Fatalf("missing rows: %+v", ids)
	}
}

func TestReadCLI(t *testing.T) {
	chat, err := cliReader(t).Read(context.Background(), nil, "cli:chat-cli-1")
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if chat.Source != "cursor" || chat.SourceID != "cli:chat-cli-1" || chat.ID != "" {
		t.Fatalf("chat identity: %+v", chat)
	}
	if len(chat.Messages) != 4 {
		t.Fatalf("want 4 messages, got %d: %+v", len(chat.Messages), chat.Messages)
	}
	if chat.Messages[0].Role != "system" || !strings.Contains(chat.Messages[0].Content, "chat-cli-1") {
		t.Fatalf("system message = %+v", chat.Messages[0])
	}
	if chat.Messages[1].Role != "user" || chat.Messages[1].Content != "Review the CLI reader" {
		t.Fatalf("first user = %+v", chat.Messages[1])
	}
	assistant := chat.Messages[2]
	if assistant.Role != "assistant" {
		t.Fatalf("assistant role = %q", assistant.Role)
	}
	if assistant.Content != "Reading files.\n[tool call: Read({\"path\":\"cli.go\"})]" {
		t.Fatalf("assistant content = %q", assistant.Content)
	}
	if assistant.ReasoningContent != "Need to read." {
		t.Fatalf("reasoning = %q", assistant.ReasoningContent)
	}
	if chat.Messages[3].Content != "Done." {
		t.Fatalf("tail = %+v", chat.Messages)
	}
	if want := time.UnixMilli(1755000000000); !chat.Created.Equal(want) {
		t.Fatalf("created = %v, want %v", chat.Created, want)
	}
}

func TestCLIReadUnknownChat(t *testing.T) {
	if _, err := cliReader(t).Read(context.Background(), nil, "cli:absent"); err == nil {
		t.Fatal("want an error for an unknown CLI chat")
	}
	if _, err := cliReader(t).Read(context.Background(), nil, "cli:"); err == nil {
		t.Fatal("want an error for an empty CLI chat id")
	}
}

func TestCLIMissingRootIsAbsence(t *testing.T) {
	r := SourceReader{
		DBPath:  filepath.Join(t.TempDir(), "absent.vscdb"),
		CLIRoot: filepath.Join(t.TempDir(), "absent"),
	}
	rows, err := r.Discover(context.Background(), nil)
	if err != nil {
		t.Fatalf("missing root must be absence: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("want no rows, got %d", len(rows))
	}
	if _, err := r.Read(context.Background(), nil, "cli:x"); err == nil {
		t.Fatal("Read must report a missing root")
	}
}

func TestCLIDefaultRoot(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	src := filepath.Join("testdata", "cli", "workspace-a", "chat-cli-1")
	dst := filepath.Join(home, ".cursor", "chats", "workspace-a", "chat-cli-1")
	if err := os.MkdirAll(dst, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"store.db", "meta.json"} {
		b, err := os.ReadFile(filepath.Join(src, name))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dst, name), b, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	r := SourceReader{DBPath: filepath.Join(t.TempDir(), "absent.vscdb")}
	rows, err := r.Discover(context.Background(), nil)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(rows) != 1 || rows[0].SourceID != "cli:chat-cli-1" {
		t.Fatalf("rows: %+v", rows)
	}
}

func TestCLIWALRefused(t *testing.T) {
	root := copyCLIFixture(t)
	dbPath := filepath.Join(root, "workspace-a", "chat-cli-1", "store.db")
	if err := os.WriteFile(dbPath+"-wal", []byte("ANONWAL"), 0o600); err != nil {
		t.Fatal(err)
	}
	r := SourceReader{DBPath: filepath.Join(t.TempDir(), "absent.vscdb"), CLIRoot: root}
	if _, err := r.Discover(context.Background(), nil); !errors.Is(err, sqlitepage.ErrWALPresent) {
		t.Fatalf("Discover: want ErrWALPresent, got %v", err)
	}
	if _, err := r.Read(context.Background(), nil, "cli:chat-cli-1"); !errors.Is(err, sqlitepage.ErrWALPresent) {
		t.Fatalf("Read: want ErrWALPresent, got %v", err)
	}
}

func TestDiscoverCLIErrorDoesNotHideGUI(t *testing.T) {
	root := copyCLIFixture(t)
	dbPath := filepath.Join(root, "workspace-a", "chat-cli-1", "store.db")
	if err := os.WriteFile(dbPath+"-wal", []byte("ANONWAL"), 0o600); err != nil {
		t.Fatal(err)
	}
	r := testReader(t)
	r.CLIRoot = root
	rows, err := r.Discover(context.Background(), nil)
	if err != nil {
		t.Fatalf("a failed CLI storage must not hide the IDE rows: %v", err)
	}
	if len(rows) != 4 {
		t.Fatalf("want 4 IDE rows, got %d", len(rows))
	}
}

func TestDiscoverGUIErrorDoesNotHideCLI(t *testing.T) {
	gui := filepath.Join(t.TempDir(), "state.vscdb")
	if err := os.WriteFile(gui, []byte("not a database"), 0o600); err != nil {
		t.Fatal(err)
	}
	r := SourceReader{DBPath: gui, CLIRoot: filepath.Join("testdata", "cli")}
	rows, err := r.Discover(context.Background(), nil)
	if err != nil {
		t.Fatalf("a failed IDE storage must not hide the CLI rows: %v", err)
	}
	if len(rows) != 1 || rows[0].SourceID != "cli:chat-cli-1" {
		t.Fatalf("rows: %+v", rows)
	}
}

func TestCLIEmptyAndSubagentChatsAreSkipped(t *testing.T) {
	r := SourceReader{DBPath: filepath.Join(t.TempDir(), "absent.vscdb"), CLIRoot: filepath.Join("testdata", "cli")}
	rows, err := r.Discover(context.Background(), nil)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	for _, row := range rows {
		if row.SourceID == "cli:chat-empty" || row.SourceID == "cli:chat-subagent" {
			t.Fatalf("an empty or subagent chat must not be listed: %+v", row)
		}
	}
	if _, err := r.Read(context.Background(), nil, "cli:chat-empty"); err == nil {
		t.Fatal("Read must report an empty chat")
	}
}

func TestReadRootBlobIDs(t *testing.T) {
	valid := []byte{
		0x0a, 0x20,
		0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15,
		16, 17, 18, 19, 20, 21, 22, 23, 24, 25, 26, 27, 28, 29, 30, 31,
	}
	ids, err := readRootBlobIDs(valid)
	if err != nil {
		t.Fatalf("readRootBlobIDs: %v", err)
	}
	if len(ids) != 1 || ids[0] != "000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f" {
		t.Fatalf("ids = %v", ids)
	}
	if _, err := readRootBlobIDs(nil); err == nil {
		t.Fatal("want an error for a missing root")
	}
	if _, err := readRootBlobIDs([]byte{0x0a}); err == nil {
		t.Fatal("want an error for a truncated key")
	}
	if _, err := readRootBlobIDs([]byte{0x0a, 0x20, 0x00}); err == nil {
		t.Fatal("want an error for a length past the end")
	}
	if _, err := readRootBlobIDs([]byte{0x08, 0x01}); err != nil {
		t.Fatalf("a non-id field must be skipped: %v", err)
	}
	if _, err := readRootBlobIDs([]byte{0x0b}); err == nil {
		t.Fatal("want an error for an unsupported wire type")
	}
	if _, err := readRootBlobIDs([]byte{0x0d, 0, 0, 0, 0}); err != nil {
		t.Fatalf("fixed32 must be skipped: %v", err)
	}
	if _, err := readRootBlobIDs([]byte{0x0d, 0, 0}); err == nil {
		t.Fatal("want an error for a short fixed32")
	}
	if _, err := readRootBlobIDs([]byte{0x09, 0, 0, 0, 0, 0, 0, 0, 0}); err != nil {
		t.Fatalf("fixed64 must be skipped: %v", err)
	}
}

func TestCleanCLIUserText(t *testing.T) {
	cases := map[string]string{
		"<user_query>\nhello\n</user_query>":                      "hello",
		"<timestamp>x</timestamp>\n<user_query>hi</user_query>":   "hi",
		"<user_info>\ncontext\n</user_info>":                      "",
		"<git_status>\nstatus\n</git_status>":                     "",
		"<system_notification>\nfinished\n</system_notification>": "",
		"plain message":            "plain message",
		"<user_query>unterminated": "unterminated",
	}
	for in, want := range cases {
		if got := cleanCLIUserText(in); got != want {
			t.Fatalf("cleanCLIUserText(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCLIBlobMessage(t *testing.T) {
	b := parseCLIBlob([]byte(`{"role":"assistant","content":[
		{"type":"reasoning","text":"why"},
		{"type":"tool-call","toolName":"Shell","args":{"command":"ls"}}
	]}`))
	msg, ok := b.message()
	if !ok {
		t.Fatal("expected a message")
	}
	if msg.ReasoningContent != "why" {
		t.Fatalf("reasoning = %q", msg.ReasoningContent)
	}
	if msg.Content != "[tool call: Shell({\"command\":\"ls\"})]" {
		t.Fatalf("content = %q", msg.Content)
	}

	reasoningOnly := parseCLIBlob([]byte(`{"role":"assistant","content":[{"type":"reasoning","text":"why"}]}`))
	msg, ok = reasoningOnly.message()
	if !ok || msg.Content != "[thinking] why" {
		t.Fatalf("reasoning-only message = %+v, %v", msg, ok)
	}

	if _, ok := parseCLIBlob([]byte(`{"role":"assistant","content":[]}`)).message(); ok {
		t.Fatal("empty content must not produce a message")
	}
	if got := parseCLIBlob([]byte(`{"role":"user","content":"plain"}`)).text(); got != "plain" {
		t.Fatalf("text = %q", got)
	}
	if got := parseCLIBlob([]byte(`{"role":"user","content":[{"type":"text","text":"a"},{"type":"reasoning","text":"b"}]}`)).text(); got != "a" {
		t.Fatalf("text parts = %q", got)
	}
}

func TestCLIReadContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := cliReader(t).Read(ctx, nil, "cli:chat-cli-1"); !errors.Is(err, context.Canceled) {
		t.Fatalf("want context.Canceled, got %v", err)
	}
}

func TestCLIBrokenRootBlob(t *testing.T) {
	r := SourceReader{
		DBPath:  filepath.Join(t.TempDir(), "absent.vscdb"),
		CLIRoot: filepath.Join("testdata", "cli-broken"),
	}
	if _, err := r.Discover(context.Background(), nil); err == nil {
		t.Fatal("want an error for a missing root blob")
	}
	if _, err := r.Read(context.Background(), nil, "cli:chat-broken-root"); err == nil {
		t.Fatal("want an error for a missing root blob")
	}
}

func TestCLIBadMetadata(t *testing.T) {
	root := copyCLIFixture(t)
	metaPath := filepath.Join(root, "workspace-a", "chat-cli-1", "meta.json")
	if err := os.WriteFile(metaPath, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	r := SourceReader{DBPath: filepath.Join(t.TempDir(), "absent.vscdb"), CLIRoot: root}
	if _, err := r.Discover(context.Background(), nil); err == nil {
		t.Fatal("want an error for malformed metadata")
	}
	if _, err := r.Read(context.Background(), nil, "cli:chat-cli-1"); err == nil {
		t.Fatal("want an error for malformed metadata")
	}
}

func TestCLIModelAndFallbacks(t *testing.T) {
	meta := cliStoreMeta{LastUsedModel: "default"}
	if got := meta.model(); got != "" {
		t.Fatalf("default model = %q", got)
	}
	meta.LastUsedModel = "grok-4.5"
	if got := meta.model(); got != "grok-4.5" {
		t.Fatalf("model = %q", got)
	}
	if !(cliFileMeta{}).created().IsZero() {
		t.Fatal("zero file time must be zero")
	}
	if !(cliStoreMeta{}).created().IsZero() {
		t.Fatal("zero store time must be zero")
	}
}
