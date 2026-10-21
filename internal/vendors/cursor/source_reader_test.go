package cursor

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/baalimago/clai/internal/sqlitepage"
	"github.com/baalimago/clai/internal/vendors"
)

func testReader(t *testing.T) SourceReader {
	t.Helper()
	return SourceReader{
		DBPath:  filepath.Join("testdata", "state.vscdb"),
		CLIRoot: filepath.Join(t.TempDir(), "chats"),
	}
}

func TestSource(t *testing.T) {
	if got := (SourceReader{}).Source(); got != "cursor" {
		t.Fatalf("Source() = %q, want cursor", got)
	}
}

func TestDiscover(t *testing.T) {
	rows, err := testReader(t).Discover(context.Background(), nil)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	byID := map[string]vendors.SourceRow{}
	for _, r := range rows {
		byID[r.SourceID] = r
	}
	if _, ok := byID["composer-empty-headers"]; ok {
		t.Fatal("a composer without headers must not be listed")
	}
	if _, ok := byID["composer-missing-bubble"]; ok {
		t.Fatal("a composer whose bubbles produce no messages must not be listed")
	}
	if len(rows) != 3 {
		t.Fatalf("want 3 rows, got %d: %+v", len(rows), rows)
	}
	got := byID["composer-1"]
	if got.Source != "cursor" {
		t.Fatalf("Source = %q", got.Source)
	}
	if got.FirstUserMessage != "Fix the parser please" {
		t.Fatalf("preview = %q", got.FirstUserMessage)
	}
	if got.FullFirstUserMessage != "Fix the parser please" {
		t.Fatalf("full preview = %q", got.FullFirstUserMessage)
	}
	if got.MessageCount != 4 {
		t.Fatalf("message count = %d", got.MessageCount)
	}
	if got.Model != "claude-4-sonnet" {
		t.Fatalf("model = %q", got.Model)
	}
	if got.Cwd != "/home/dev/project" {
		t.Fatalf("cwd = %q", got.Cwd)
	}
	if want := time.UnixMilli(1750000000000); !got.Created.Equal(want) {
		t.Fatalf("created = %v, want %v", got.Created, want)
	}
	if got.RawPath == "" {
		t.Fatal("RawPath must name the database")
	}
	if got := byID["composer-no-preview"]; got.FirstUserMessage != "Fallback preview" {
		t.Fatalf("fallback preview = %q", got.FirstUserMessage)
	}
	if got := byID["composer-2"]; got.Model != "" || got.Cwd != "/home/dev/flaky" {
		t.Fatalf("missing model and bubble cwd: %+v", got)
	}
	for _, row := range rows {
		chat, err := testReader(t).Read(context.Background(), nil, row.SourceID)
		if err != nil {
			t.Fatalf("Read(%q): %v", row.SourceID, err)
		}
		if got := len(chat.Messages) - 1; got != row.MessageCount {
			t.Fatalf("%s: Read has %d messages, row says %d", row.SourceID, got, row.MessageCount)
		}
	}
}

func TestRead(t *testing.T) {
	chat, err := testReader(t).Read(context.Background(), nil, "composer-1")
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if chat.Source != "cursor" || chat.SourceID != "composer-1" || chat.ID != "" {
		t.Fatalf("chat identity: %+v", chat)
	}
	if len(chat.Messages) != 5 {
		t.Fatalf("want 5 messages, got %d: %+v", len(chat.Messages), chat.Messages)
	}
	if chat.Messages[0].Role != "system" || !strings.Contains(chat.Messages[0].Content, "composer-1") {
		t.Fatalf("system message = %+v", chat.Messages[0])
	}
	if chat.Messages[1].Role != "user" || chat.Messages[1].Content != "Fix the parser please" {
		t.Fatalf("first user = %+v", chat.Messages[1])
	}
	assistant := chat.Messages[2]
	if assistant.Role != "assistant" {
		t.Fatalf("assistant role = %q", assistant.Role)
	}
	if assistant.Content != "I will fix it.\n[tool call: read_file({\"path\":\"parser.go\"})]" {
		t.Fatalf("assistant content = %q", assistant.Content)
	}
	if assistant.ReasoningContent != "Need to inspect the parser." {
		t.Fatalf("reasoning = %q", assistant.ReasoningContent)
	}
	if chat.Messages[3].Content != "Thanks" || chat.Messages[4].Content != "Done" {
		t.Fatalf("tail messages: %+v", chat.Messages)
	}
	if want := time.UnixMilli(1750000000000); !chat.Created.Equal(want) {
		t.Fatalf("created = %v, want %v", chat.Created, want)
	}
}

func TestReadRichTextAndReasoningOnly(t *testing.T) {
	chat, err := testReader(t).Read(context.Background(), nil, "composer-2")
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(chat.Messages) != 3 {
		t.Fatalf("want 3 messages, got %d: %+v", len(chat.Messages), chat.Messages)
	}
	if chat.Messages[1].Content != "Investigate the flaky test" {
		t.Fatalf("rich text = %q", chat.Messages[1].Content)
	}
	if chat.Messages[2].Content != "[thinking] Check the logs." {
		t.Fatalf("reasoning-only content = %q", chat.Messages[2].Content)
	}
	if chat.Messages[2].ReasoningContent != "Check the logs." {
		t.Fatalf("reasoning = %q", chat.Messages[2].ReasoningContent)
	}
}

func TestReadMissingBubbleStillReturnsSystemMessage(t *testing.T) {
	chat, err := testReader(t).Read(context.Background(), nil, "composer-missing-bubble")
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(chat.Messages) != 1 || chat.Messages[0].Role != "system" {
		t.Fatalf("messages: %+v", chat.Messages)
	}
}

func TestReadUnknownChat(t *testing.T) {
	_, err := testReader(t).Read(context.Background(), nil, "absent")
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("want a not-found error, got %v", err)
	}
	_, err = testReader(t).Read(context.Background(), nil, "")
	if err == nil {
		t.Fatal("want an error for an empty session id")
	}
}

func TestDiscoverMissingDatabase(t *testing.T) {
	r := SourceReader{
		DBPath:  filepath.Join(t.TempDir(), "absent.vscdb"),
		CLIRoot: filepath.Join(t.TempDir(), "chats"),
	}
	rows, err := r.Discover(context.Background(), nil)
	if err != nil {
		t.Fatalf("a missing database must be absence, not an error: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("want no rows, got %d", len(rows))
	}
	if _, err := r.Read(context.Background(), nil, "x"); err == nil {
		t.Fatal("Read must report a missing database")
	}
}

func TestDiscoverNoUserConfigDir(t *testing.T) {
	t.Setenv("HOME", "")
	t.Setenv("XDG_CONFIG_HOME", "")
	rows, err := (SourceReader{}).Discover(context.Background(), nil)
	if err != nil {
		t.Fatalf("no config dir must be absence: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("want no rows, got %d", len(rows))
	}
	if _, err := (SourceReader{}).Read(context.Background(), nil, "x"); err == nil {
		t.Fatal("Read must report a missing database")
	}
}

func TestWALRefused(t *testing.T) {
	dir := t.TempDir()
	src, err := os.ReadFile(filepath.Join("testdata", "state.vscdb"))
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, "state.vscdb")
	if err := os.WriteFile(p, src, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p+"-wal", []byte("ANONWAL"), 0o600); err != nil {
		t.Fatal(err)
	}
	r := SourceReader{DBPath: p, CLIRoot: filepath.Join(t.TempDir(), "chats")}
	if _, err := r.Discover(context.Background(), nil); !errors.Is(err, sqlitepage.ErrWALPresent) {
		t.Fatalf("Discover: want ErrWALPresent, got %v", err)
	}
	if _, err := r.Read(context.Background(), nil, "composer-1"); !errors.Is(err, sqlitepage.ErrWALPresent) {
		t.Fatalf("Read: want ErrWALPresent, got %v", err)
	}
}

func TestCorruptDatabase(t *testing.T) {
	p := filepath.Join(t.TempDir(), "state.vscdb")
	if err := os.WriteFile(p, []byte("not a database"), 0o600); err != nil {
		t.Fatal(err)
	}
	r := SourceReader{DBPath: p, CLIRoot: filepath.Join(t.TempDir(), "chats")}
	if _, err := r.Discover(context.Background(), nil); !errors.Is(err, sqlitepage.ErrNotSQLite) {
		t.Fatalf("want ErrNotSQLite, got %v", err)
	}
}

func TestContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := testReader(t).Discover(ctx, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("Discover: want context.Canceled, got %v", err)
	}
	if _, err := testReader(t).Read(ctx, nil, "composer-1"); !errors.Is(err, context.Canceled) {
		t.Fatalf("Read: want context.Canceled, got %v", err)
	}
}

type goldenTable struct {
	Rows   int    `json:"rows"`
	SHA256 string `json:"sha256"`
}

func TestGoldenParity(t *testing.T) {
	checkGolden(t, filepath.Join("testdata", "state.vscdb"), filepath.Join("testdata", "golden.json"))
}

func TestGoldenParityCLI(t *testing.T) {
	checkGolden(t,
		filepath.Join("testdata", "cli", "workspace-a", "chat-cli-1", "store.db"),
		filepath.Join("testdata", "cli-golden.json"),
	)
}

func checkGolden(t *testing.T, dbPath, goldenPath string) {
	t.Helper()
	b, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatal(err)
	}
	var want map[string]goldenTable
	if err := json.Unmarshal(b, &want); err != nil {
		t.Fatal(err)
	}
	db, err := sqlitepage.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for name, w := range want {
		tbl, err := db.Table(name)
		if err != nil {
			t.Fatalf("table %s: %v", name, err)
		}
		sum := sha256.New()
		rows := 0
		err = tbl.Rows(func(_ int64, vals []sqlitepage.Value) error {
			if len(vals) != 2 || vals[0].Kind != sqlitepage.Text {
				return errors.New("unexpected row shape")
			}
			sum.Write([]byte(vals[0].Text))
			sum.Write([]byte{0})
			switch vals[1].Kind {
			case sqlitepage.Null:
				sum.Write([]byte{0xff})
			case sqlitepage.Blob:
				sum.Write(vals[1].Blob)
			case sqlitepage.Text:
				sum.Write([]byte(vals[1].Text))
			default:
				return errors.New("unexpected value kind")
			}
			sum.Write([]byte{0})
			rows++
			return nil
		})
		if err != nil {
			t.Fatalf("rows %s: %v", name, err)
		}
		got := hex.EncodeToString(sum.Sum(nil))
		if rows != w.Rows || got != w.SHA256 {
			t.Fatalf("%s: got rows=%d sha=%s, want rows=%d sha=%s", name, rows, got, w.Rows, w.SHA256)
		}
	}
}

func TestRichText(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"empty", "", ""},
		{"invalid", "{", ""},
		{"text leaf", `{"root":{"children":[{"children":[{"text":"a"},{"text":"b"}]}]}}`, "ab"},
		{"mention skipped", `{"root":{"children":[{"children":[{"type":"mention"},{"text":"c"}]}]}}`, "c"},
		{"text wins over children", `{"text":"root","children":[{"text":"child"}]}`, "root"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := richText([]byte(tc.in)); got != tc.want {
				t.Fatalf("richText = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestThinkingText(t *testing.T) {
	cases := map[string]string{
		``:                           "",
		`"plain"`:                    "plain",
		`{"text":"object"}`:          "object",
		`{"other":true}`:             "",
		`{"text":5}`:                 "",
		`[{"text":"not-an-object"}]`: "",
	}
	for in, want := range cases {
		if got := thinkingText([]byte(in)); got != want {
			t.Fatalf("thinkingText(%s) = %q, want %q", in, got, want)
		}
	}
}

func TestRawText(t *testing.T) {
	if got := rawText([]byte(`"plain"`)); got != "plain" {
		t.Fatalf("string = %q", got)
	}
	if got := rawText([]byte(`{"path":"a.go"}`)); got != `{"path":"a.go"}` {
		t.Fatalf("object = %q", got)
	}
	if got := rawText(nil); got != "" {
		t.Fatalf("nil = %q", got)
	}
}

func TestKeySuffix(t *testing.T) {
	if got := keySuffix("bubbleId:device:b1"); got != "b1" {
		t.Fatalf("suffix = %q", got)
	}
	if got := keySuffix("no-colon"); got != "no-colon" {
		t.Fatalf("suffix = %q", got)
	}
}

func TestKeyValueRejectsUnexpectedShape(t *testing.T) {
	if _, _, ok := keyValue(nil); ok {
		t.Fatal("nil values must be rejected")
	}
	vals := []sqlitepage.Value{{Kind: sqlitepage.Text, Text: "k"}, {Kind: sqlitepage.Integer, Int: 1}}
	key, value, ok := keyValue(vals)
	if !ok || key != "k" || value != nil {
		t.Fatalf("keyValue = %q, %v, %v", key, value, ok)
	}
}
