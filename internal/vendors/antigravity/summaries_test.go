package antigravity

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/baalimago/clai/internal/sqlitepage"
)

func storePath(parts ...string) string {
	return filepath.Join(append([]string{"testdata", "store"}, parts...)...)
}

func TestReadSummariesDesktopRoot(t *testing.T) {
	idx := readSummaries(storePath("antigravity"))
	tests := []struct {
		name   string
		id     string
		title  string
		cwd    string
		parent string
		depth  int64
	}{
		{name: "title", id: "main", title: "Main Conversation Title"},
		{name: "title and workspace", id: "titled", title: "Generated Title", cwd: "/work/titled"},
		{name: "workspace only", id: "nometa", cwd: "/work/from-index"},
		{name: "child", id: "nested", parent: "main", depth: 1},
		{name: "child with a prompt", id: "childprompt", parent: "main", depth: 1},
		{name: "child without a parent", id: "orphan", depth: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sum, ok := idx[tt.id]
			if !ok {
				t.Fatalf("%q is absent from the index", tt.id)
			}
			if sum.title != tt.title || sum.cwd != tt.cwd || sum.parent != tt.parent || sum.depth != tt.depth {
				t.Fatalf("summary = %+v", sum)
			}
		})
	}
	if _, ok := idx["stray"]; ok {
		t.Fatal("a conversation the app never indexed got an entry")
	}
	if got := idx["nested"].child(); !got {
		t.Fatal("a nested row is not a child run")
	}
	if got := idx["main"].child(); got {
		t.Fatal("a top-level row is a child run")
	}
}

func TestReadSummariesOldSchema(t *testing.T) {
	idx := readSummaries(storePath("antigravity-cli"))
	sum, ok := idx["cli"]
	if !ok {
		t.Fatal("the cli row is absent")
	}
	if sum.title != "CLI Conversation Title" {
		t.Fatalf("title = %q", sum.title)
	}
	if sum.cwd != "" {
		t.Fatalf("cwd = %q, want empty", sum.cwd)
	}
	if sum.child() {
		t.Fatal("an index without nesting columns reported a child run")
	}
}

func TestReadSummariesNotADatabase(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, summariesFile), []byte("not an index\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := readSummaries(root); len(got) != 0 {
		t.Fatalf("got %d entries from a corrupt index", len(got))
	}
}

func TestReadSummariesWithoutIdentity(t *testing.T) {
	if got := readSummaries(storePath("antigravity-ide")); len(got) != 0 {
		t.Fatalf("got %d entries from an index without a conversation id", len(got))
	}
}

func TestReadSummariesMissingFile(t *testing.T) {
	if got := readSummaries(t.TempDir()); len(got) != 0 {
		t.Fatalf("got %d entries from a missing index", len(got))
	}
	if got := readSummaries(""); len(got) != 0 {
		t.Fatalf("got %d entries from an empty root", len(got))
	}
}

func TestReadSummariesMissingTable(t *testing.T) {
	root := t.TempDir()
	data, err := os.ReadFile(storePath("antigravity", "conversations", "foreign.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, summariesFile), data, 0o600); err != nil {
		t.Fatal(err)
	}
	if got := readSummaries(root); len(got) != 0 {
		t.Fatalf("got %d entries from an index without the summaries table", len(got))
	}
}

func TestColumnPositions(t *testing.T) {
	pos := columnPositions([]string{"conversation_id", "title"})
	if pos["conversation_id"] != 0 || pos["title"] != 1 {
		t.Fatalf("positions = %+v", pos)
	}
	if got := positionOf(pos, "nesting_depth"); got != -1 {
		t.Fatalf("position of an absent column = %d, want -1", got)
	}
}

func TestWorkspacePath(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{name: "empty", raw: "", want: ""},
		{name: "one", raw: `["file:///work/titled"]`, want: "/work/titled"},
		{name: "second file uri", raw: `["https://example.com","file:///work/b"]`, want: "/work/b"},
		{name: "no file uri", raw: `["https://example.com"]`, want: ""},
		{name: "not json", raw: "file:///work/titled", want: ""},
		{name: "empty list", raw: "[]", want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := workspacePath(tt.raw); got != tt.want {
				t.Fatalf("workspacePath(%q) = %q, want %q", tt.raw, got, tt.want)
			}
		})
	}
}

func TestTextAt(t *testing.T) {
	vals := []sqlitepage.Value{
		{Kind: sqlitepage.Text, Text: "text"},
		{Kind: sqlitepage.Integer, Int: 1},
	}
	if got := textAt(vals, 0); got != "text" {
		t.Fatalf("textAt = %q", got)
	}
	if got := textAt(vals, 1); got != "" {
		t.Fatalf("textAt over an integer = %q, want empty", got)
	}
	if got := textAt(vals, -1); got != "" {
		t.Fatalf("textAt over an absent column = %q, want empty", got)
	}
	if got := textAt(vals, 2); got != "" {
		t.Fatalf("textAt past the row = %q, want empty", got)
	}
}
