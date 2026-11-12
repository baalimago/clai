package runenv

import (
	"bytes"
	"errors"
	"testing"

	"github.com/baalimago/clai/internal/jsonl"
	"github.com/baalimago/clai/internal/stdio"
)

// failingWriter fails every write with err.
type failingWriter struct{ err error }

func (w failingWriter) Write([]byte) (int, error) { return 0, w.err }

// TestWriteJSONDocument pins the one escape from the record stream: the
// document is written compactly and unwrapped, so a script parses it without
// reading a record's text field.
func TestWriteJSONDocument(t *testing.T) {
	t.Run("writes the document unwrapped", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		env := New(Config{
			Streams: stdio.New(stdio.Config{Format: jsonl.FormatJSON, Stdout: &stdout, Stderr: &stderr}),
			Format:  jsonl.FormatJSON,
		})
		if err := env.WriteJSONDocument([]map[string]string{{"name": "cat"}}); err != nil {
			t.Fatalf("WriteJSONDocument: %v", err)
		}
		want := `[{"name":"cat"}]` + "\n"
		if stdout.String() != want || stderr.Len() != 0 {
			t.Fatalf("stdout %q stderr %q, want %q on stdout only", stdout.String(), stderr.String(), want)
		}
	})

	t.Run("an unencodable value is an error", func(t *testing.T) {
		var stdout bytes.Buffer
		env := New(Config{Streams: stdio.New(stdio.Config{Format: jsonl.FormatJSON, Stdout: &stdout})})
		if err := env.WriteJSONDocument(make(chan int)); err == nil {
			t.Fatal("want an encode error")
		}
	})

	t.Run("a missing stdout is an error", func(t *testing.T) {
		env := New(Config{Streams: stdio.New(stdio.Config{Format: jsonl.FormatJSON})})
		if err := env.WriteJSONDocument(map[string]string{"name": "cat"}); !errors.Is(err, ErrNoStdout) {
			t.Fatalf("error = %v, want ErrNoStdout", err)
		}
	})

	t.Run("a failed write is an error", func(t *testing.T) {
		want := errors.New("no space")
		env := New(Config{
			Streams: stdio.New(stdio.Config{Format: jsonl.FormatJSON, Stdout: failingWriter{err: want}}),
			Format:  jsonl.FormatJSON,
		})
		if err := env.WriteJSONDocument(map[string]string{"name": "cat"}); !errors.Is(err, want) {
			t.Fatalf("error = %v, want %v", err, want)
		}
	})
}
