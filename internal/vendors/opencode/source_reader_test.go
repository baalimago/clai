package opencode

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/baalimago/clai/internal/sqlitepage"
	"github.com/baalimago/clai/internal/vendors"
)

func reader(t *testing.T, name string) SourceReader {
	t.Helper()
	p, err := filepath.Abs(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return SourceReader{DBPath: p}
}

func fixture(t *testing.T) SourceReader { return reader(t, "opencode.db") }

func discover(t *testing.T) []vendors.SourceRow {
	t.Helper()
	rows, err := fixture(t).Discover(context.Background(), nil)
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
	if got := (SourceReader{}).Source(); got != "opencode" {
		t.Fatalf("Source() = %q", got)
	}
}

func TestDiscover(t *testing.T) {
	rows := discover(t)
	if len(rows) != 4 {
		t.Fatalf("got %d rows, want 4: %+v", len(rows), rows)
	}
	main := mustRow(t, rows, "ses_main")

	if main.FirstUserMessage != "Ship the slurp" {
		t.Fatalf("preview = %q, want the title", main.FirstUserMessage)
	}
	if main.FullFirstUserMessage != "hello opencode" {
		t.Fatalf("full prompt = %q, want the prompt, because grouping hashes it", main.FullFirstUserMessage)
	}
	if main.MessageCount != 5 {
		t.Fatalf("ses_main count = %d, want 5 (user, assistant, two tool results, tail)", main.MessageCount)
	}
	if main.Cwd != "/work/clai" {
		t.Fatalf("cwd = %q", main.Cwd)
	}
	if main.Model != "prov/m1" {
		t.Fatalf("model = %q, want prov/m1", main.Model)
	}
	if main.Created.IsZero() {
		t.Fatal("created is zero")
	}
	if main.RawPath == "" {
		t.Fatal("raw path is empty")
	}

	untitled := mustRow(t, rows, "ses_untitled")
	if untitled.FirstUserMessage != "first prompt wins" || untitled.FullFirstUserMessage != "first prompt wins" {
		t.Fatalf("untitled = %+v, want the first prompt in both fields", untitled)
	}
	if untitled.MessageCount != 1 {
		t.Fatalf("count = %d, want 1", untitled.MessageCount)
	}
}

// TestDiscoverFollowsSeq proves the preview follows seq, not the rowid scan
// order: the fixture stores the later prompt first.
func TestDiscoverFollowsSeq(t *testing.T) {
	row := mustRow(t, discover(t), "ses_outoforder")
	if row.FullFirstUserMessage != "earlier prompt" {
		t.Fatalf("prompt = %q, want the lower seq prompt", row.FullFirstUserMessage)
	}
	if row.MessageCount != 2 {
		t.Fatalf("count = %d, want 2", row.MessageCount)
	}
}

// TestDiscoverSkipsNonConversations covers the four reasons a stored session is
// not a user conversation: a subagent parent, no messages, only bookkeeping
// messages, and a body that does not decode.
func TestDiscoverSkipsNonConversations(t *testing.T) {
	rows := discover(t)
	for _, row := range rows {
		switch row.SourceID {
		case "ses_child", "ses_empty", "ses_noise", "ses_broken":
			t.Fatalf("%s must not be listed: %+v", row.SourceID, row)
		}
	}
}

func TestDiscoverMissingDatabase(t *testing.T) {
	r := SourceReader{DBPath: filepath.Join(t.TempDir(), "absent.db")}
	rows, err := r.Discover(context.Background(), nil)
	if err != nil {
		t.Fatalf("a missing database must be absence, got %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("got %d rows", len(rows))
	}
}

func TestDiscoverNotADatabase(t *testing.T) {
	p := filepath.Join(t.TempDir(), "junk.db")
	if err := os.WriteFile(p, []byte("not sqlite"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := (SourceReader{DBPath: p}).Discover(context.Background(), nil); err == nil {
		t.Fatal("want an error for a file that is not a database")
	}
}

func TestRead(t *testing.T) {
	chat, err := fixture(t).Read(context.Background(), nil, "ses_main")
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if chat.Source != "opencode" || chat.SourceID != "ses_main" {
		t.Fatalf("chat identity = %q %q", chat.Source, chat.SourceID)
	}
	if len(chat.Messages) != 6 {
		t.Fatalf("got %d messages, want 6: %+v", len(chat.Messages), chat.Messages)
	}
	if chat.Messages[0].Role != "system" || !strings.Contains(chat.Messages[0].Content, "/work/clai") {
		t.Fatalf("system preamble = %+v", chat.Messages[0])
	}

	user := chat.Messages[1]
	if user.Role != "user" || user.Content != "hello opencode" {
		t.Fatalf("user = %+v", user)
	}

	step := chat.Messages[2]
	if step.Role != "assistant" || step.Content != "looking around" {
		t.Fatalf("assistant = %+v", step)
	}
	if step.ReasoningContent != "the repo is small" {
		t.Fatalf("reasoning = %q", step.ReasoningContent)
	}
	if len(step.ToolCalls) != 2 {
		t.Fatalf("got %d tool calls, want 2", len(step.ToolCalls))
	}
	if got := step.ToolCalls[0]; got.ID != "call_1" || got.Function.Name != "shell" || got.Function.Arguments != `{"command":"ls"}` {
		t.Fatalf("call 0 = %+v", got)
	}

	result := chat.Messages[3]
	if result.Role != "tool" || result.ToolCallID != "call_1" || result.Content != "out" {
		t.Fatalf("result = %+v", result)
	}
	failed := chat.Messages[4]
	if failed.Role != "tool" || failed.ToolCallID != "call_2" || failed.Content != "tool error: dismissed" {
		t.Fatalf("failed result = %+v", failed)
	}
	if tail := chat.Messages[5]; tail.Role != "assistant" || tail.Content != "done" {
		t.Fatalf("tail = %+v", tail)
	}
}

// TestRowGuards covers the row shapes a rebuilt or foreign record can take: a
// short record and a column of the wrong storage class contribute nothing.
func TestRowGuards(t *testing.T) {
	text := func(s string) sqlitepage.Value { return sqlitepage.Value{Kind: sqlitepage.Text, Text: s} }
	blob := sqlitepage.Value{Kind: sqlitepage.Blob, Blob: []byte("x")}

	message := make([]sqlitepage.Value, colMessageData+1)
	message[colMessageSession] = text("ses_main")
	message[colMessageKind] = text("user")
	message[colMessageSeq] = sqlitepage.Value{Kind: sqlitepage.Integer, Int: 4}
	message[colMessageData] = text("{\"text\":\"hi\"}")
	row, ok := messageRowOf(message)
	if !ok || row.session != "ses_main" || row.kind != "user" || row.seq != 4 || string(row.data) != `{"text":"hi"}` {
		t.Fatalf("messageRowOf = %+v, %v", row, ok)
	}

	badMessages := map[string][]sqlitepage.Value{
		"short record":     message[:colMessageData],
		"session not text": {blob, blob, text("user"), {}, {}, {}, text("{}")},
		"session empty":    {text(""), text(""), text("user"), {}, {}, {}, text("{}")},
		"kind not text":    {text(""), text("s"), blob, {}, {}, {}, text("{}")},
		"data not text":    {text(""), text("s"), text("user"), {}, {}, {}, blob},
	}
	for name, vals := range badMessages {
		t.Run("message "+name, func(t *testing.T) {
			if _, ok := messageRowOf(vals); ok {
				t.Fatalf("messageRowOf accepted %s", name)
			}
		})
	}

	session := make([]sqlitepage.Value, colSessionBorn+1)
	session[colSessionID] = text("ses_main")
	session[colSessionParent] = text("ses_parent")
	session[colSessionTitle] = text("title")
	session[colSessionCwd] = text("/work")
	session[colSessionModel] = text(`{"id":"m1","providerID":"prov"}`)
	session[colSessionBorn] = sqlitepage.Value{Kind: sqlitepage.Integer, Int: 5}
	s, ok := sessionOf(session)
	if !ok || s.id != "ses_main" || s.parent != "ses_parent" || s.title != "title" || s.cwd != "/work" || s.modelName() != "prov/m1" || s.born != 5 {
		t.Fatalf("sessionOf = %+v, %v", s, ok)
	}

	sessionWithID := func(id sqlitepage.Value) []sqlitepage.Value {
		vals := make([]sqlitepage.Value, colSessionBorn+1)
		vals[colSessionID] = id
		return vals
	}
	badSessions := map[string][]sqlitepage.Value{
		"short record": session[:colSessionBorn],
		"id not text":  sessionWithID(blob),
		"id empty":     sessionWithID(text("")),
	}
	for name, vals := range badSessions {
		t.Run("session "+name, func(t *testing.T) {
			if _, ok := sessionOf(vals); ok {
				t.Fatalf("sessionOf accepted %s", name)
			}
		})
	}
}

// TestReadParts covers the parts that must fold, skip, or render as text: an
// empty text part and an unknown type contribute nothing, and a tool that has
// not settled or has no id becomes a text line instead of an unpaired call.
func TestReadParts(t *testing.T) {
	chat, err := fixture(t).Read(context.Background(), nil, "ses_parts")
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(chat.Messages) != 2 {
		t.Fatalf("got %d messages, want the system preamble and one step: %+v", len(chat.Messages), chat.Messages)
	}
	step := chat.Messages[1]
	want := "kept\n[tool call: bash({\"command\":\"pwd\"})]\n[tool call: grep({\"pattern\":\"x\"})]\n[tool call: read({})]"
	if step.Role != "assistant" || step.Content != want {
		t.Fatalf("assistant = %+v, want content %q", step, want)
	}
	if len(step.ToolCalls) != 0 {
		t.Fatalf("got %d tool calls, want none for unsettled or id-less parts", len(step.ToolCalls))
	}
}

// TestCountMatchesRead is the invariant the list depends on: the number shown
// is the number Read returns, plus the system preamble.
func TestCountMatchesRead(t *testing.T) {
	r := fixture(t)
	rows, err := r.Discover(context.Background(), nil)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	for _, row := range rows {
		chat, err := r.Read(context.Background(), nil, row.SourceID)
		if err != nil {
			t.Fatalf("Read(%s): %v", row.SourceID, err)
		}
		if got := len(chat.Messages) - 1; got != row.MessageCount {
			t.Fatalf("%s: Read returned %d messages, list showed %d", row.SourceID, got, row.MessageCount)
		}
	}
}

func TestReadErrors(t *testing.T) {
	r := fixture(t)
	tests := []struct {
		name string
		id   string
		want string
	}{
		{"empty id", "", "empty session id"},
		{"unknown id", "ses_absent", "not found"},
		{"subagent", "ses_child", "subagent"},
		{"no messages", "ses_empty", "no messages"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := r.Read(context.Background(), nil, tc.id)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestReadNotADatabase(t *testing.T) {
	p := filepath.Join(t.TempDir(), "junk.db")
	if err := os.WriteFile(p, []byte("not sqlite"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := (SourceReader{DBPath: p}).Read(context.Background(), nil, "ses_main"); err == nil {
		t.Fatal("want an error for a file that is not a database")
	}
}

// TestSchemaRefused covers the positional guard: a table rebuild that reorders
// or drops a column must fail, because a width-only check would misread it.
func TestSchemaRefused(t *testing.T) {
	tests := []struct {
		name string
		file string
		run  func(SourceReader) error
	}{
		{
			"reordered columns",
			"drift.db",
			func(r SourceReader) error { _, err := r.Discover(context.Background(), nil); return err },
		},
		{
			"missing column",
			"drift.db",
			func(r SourceReader) error { _, err := r.Read(context.Background(), nil, "ses_main"); return err },
		},
		{
			"missing tables",
			"foreign.db",
			func(r SourceReader) error { _, err := r.Discover(context.Background(), nil); return err },
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.run(reader(t, tc.file)); !errors.Is(err, sqlitepage.ErrUnsupported) {
				t.Fatalf("err = %v, want ErrUnsupported", err)
			}
		})
	}
}

// TestWALReplay covers the live store shape: a session committed only to the
// write-ahead log must be listed and read, beside a session in the main file.
func TestWALReplay(t *testing.T) {
	r := reader(t, "opencode-wal.db")
	rows, err := r.Discover(context.Background(), nil)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	mustRow(t, rows, "ses_checkpointed")
	wal := mustRow(t, rows, "ses_wal")
	if wal.MessageCount != 1 || wal.FullFirstUserMessage != "from the log" {
		t.Fatalf("wal row = %+v, want the log-only prompt", wal)
	}

	chat, err := r.Read(context.Background(), nil, "ses_wal")
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(chat.Messages) != 2 || chat.Messages[1].Content != "from the log" {
		t.Fatalf("chat = %+v, want the log-only message", chat.Messages)
	}
}

func TestReadMissingDatabase(t *testing.T) {
	r := SourceReader{DBPath: filepath.Join(t.TempDir(), "absent.db")}
	_, err := r.Read(context.Background(), nil, "ses_main")
	if !errors.Is(err, errNoDatabase) {
		t.Fatalf("want errNoDatabase, got %v", err)
	}
}

func TestDatabasePath(t *testing.T) {
	t.Run("xdg data home", func(t *testing.T) {
		t.Setenv("XDG_DATA_HOME", "/xdg")
		if got := (SourceReader{}).databasePath(); got != filepath.Join("/xdg", "opencode", "opencode.db") {
			t.Fatalf("path = %q", got)
		}
	})
	t.Run("home fallback", func(t *testing.T) {
		t.Setenv("XDG_DATA_HOME", "")
		t.Setenv("HOME", "/home/tester")
		want := filepath.Join("/home/tester", ".local", "share", "opencode", "opencode.db")
		if got := (SourceReader{}).databasePath(); got != want {
			t.Fatalf("path = %q, want %q", got, want)
		}
	})
}

func TestDiscoverCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := fixture(t).Discover(ctx, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("want context.Canceled, got %v", err)
	}
}

func TestReadCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := fixture(t).Read(ctx, nil, "ses_main"); !errors.Is(err, context.Canceled) {
		t.Fatalf("want context.Canceled, got %v", err)
	}
}
