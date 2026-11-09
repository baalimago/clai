package mcp

import (
	"bytes"
	"context"
	"errors"
	"io"
	"slices"
	"strings"
	"testing"

	"github.com/baalimago/clai/internal/jsonl"
	"github.com/baalimago/clai/internal/runenv"
	"github.com/baalimago/clai/internal/stdio"
)

// childStderr is what a server may write: an error a "\r" spinner frame
// follows, an SGR sequence, a bell, a secret-shaped value, and a "\r\n".
const childStderr = "ERROR: something happened\rspin \x1b[31mred\x1b[0m bell\a token=supersecretvalue123\r\nlast\n"

func relayedStderr(t *testing.T, format jsonl.Format, written string) string {
	t.Helper()
	var out, errb bytes.Buffer
	env := runenv.New(runenv.Config{
		Streams: stdio.New(stdio.Config{Format: format, Stdin: strings.NewReader(""), Stdout: &out, Stderr: &errb}),
		Format:  format,
	})
	c, _ := newBareStdioConn()
	done := make(chan struct{})
	c.readStderr(runenv.With(context.Background(), env), strings.NewReader(written), nil, done)
	<-done
	if out.Len() != 0 {
		t.Fatalf("stdout = %q, want nothing", out.String())
	}
	return errb.String()
}

// Both formats carry the same lines of a server's stderr: a lone "\r" ends a
// line instead of erasing it, secrets are redacted, and a control character
// is escaped, by the JSON string under -json and visibly in text.
func TestStdioConnRelaysTheSameStderrTextInBothFormats(t *testing.T) {
	tests := []struct {
		name   string
		format jsonl.Format
		want   string
	}{
		{
			name:   "text",
			format: jsonl.FormatText,
			want: "mcp_srv: ERROR: something happened\n" +
				`mcp_srv: spin \x1b[31mred\x1b[0m bell\x07 token=[REDACTED]` + "\n" +
				"mcp_srv: last\n",
		},
		{
			name:   "json",
			format: jsonl.FormatJSON,
			want: `{"type":"child_output","source":"mcp_srv","stream":"stderr","text":"ERROR: something happened"}` + "\n" +
				`{"type":"child_output","source":"mcp_srv","stream":"stderr","text":"spin \u001b[31mred\u001b[0m bell\u0007 token=[REDACTED]"}` + "\n" +
				`{"type":"child_output","source":"mcp_srv","stream":"stderr","text":"last"}` + "\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := relayedStderr(t, tt.format, childStderr); got != tt.want {
				t.Fatalf("stderr =\n%s\nwant\n%s", got, tt.want)
			}
		})
	}
}

// A sink gets the same lines: the text before a lone "\r" is a line of its
// own, so an error a spinner frame follows is still classified and shown.
func TestStdioConnSplitsSinkLinesAtCarriageReturns(t *testing.T) {
	sink := &recordingSink{}
	c, _ := newBareStdioConn()
	done := make(chan struct{})
	c.readStderr(context.Background(), strings.NewReader("ERROR: something happened\rspin\r\n\r\rnext\n\ntail"), sink, done)
	<-done

	lines, _ := sink.snapshot()
	if want := []string{"ERROR: something happened", "spin", "next", "tail"}; !slices.Equal(lines, want) {
		t.Fatalf("sink lines = %q, want %q", lines, want)
	}
}

type refusingWriter struct{ err error }

func (w refusingWriter) Write([]byte) (int, error) { return 0, w.err }

func TestStderrRelayWriteReturnsTheWriteFailure(t *testing.T) {
	closed := errors.New("closed destination")
	for _, format := range []jsonl.Format{jsonl.FormatText, jsonl.FormatJSON} {
		env := runenv.New(runenv.Config{
			Streams: stdio.New(stdio.Config{Format: format, Stdin: strings.NewReader(""), Stdout: io.Discard, Stderr: refusingWriter{closed}}),
			Format:  format,
		})
		if err := NewStderrRelay(env).Write(env.Stderr(), "srv", "last words"); !errors.Is(err, closed) {
			t.Errorf("format %d: Write = %v, want the destination's error", format, err)
		}
	}
}

func TestStderrRelayRedactedKeepsTheControlCharacters(t *testing.T) {
	relay := NewStderrRelay(runenv.Text(strings.NewReader(""), io.Discard, io.Discard))
	if got, want := relay.Redacted("\x1b[31mred\x1b[0m token=abcdef123456"), "\x1b[31mred\x1b[0m token=[REDACTED]"; got != want {
		t.Fatalf("Redacted = %q, want %q", got, want)
	}
}
