package runenv

import (
	"bytes"
	"io"
	"strings"
	"testing"

	"github.com/baalimago/clai/internal/jsonl"
	"github.com/baalimago/clai/internal/stdio"
)

// TestTablePrompter pins F13 for table selections: under the JSON format the
// prompt line is a select prompt record on its stream; under the text format
// there is no prompter, so the table writes its own line.
func TestTablePrompter(t *testing.T) {
	if Text(strings.NewReader(""), io.Discard, io.Discard).TablePrompter(stdio.Stdout) != nil {
		t.Fatal("the text format has a table prompter; the table must write its own line")
	}

	var stdout, stderr bytes.Buffer
	env := New(Config{
		Streams: stdio.New(stdio.Config{Format: jsonl.FormatJSON, Stdout: &stdout, Stderr: &stderr}),
		Format:  jsonl.FormatJSON,
	})
	show := env.TablePrompter(stdio.Stdout)
	if show == nil {
		t.Fatal("the JSON format has no table prompter")
	}
	if err := show("(select, [p]rev, [q]uit, [/] filter): "); err != nil {
		t.Fatalf("show: %v", err)
	}
	want := `{"type":"prompt","kind":"select","text":"(select, [p]rev, [q]uit, [/] filter):"}` + "\n"
	if stdout.String() != want || stderr.Len() != 0 {
		t.Fatalf("stdout %q stderr %q, want %q on stdout only", stdout.String(), stderr.String(), want)
	}
}
