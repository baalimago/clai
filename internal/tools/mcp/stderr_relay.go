package mcp

import (
	"bytes"
	"fmt"
	"io"

	"github.com/baalimago/clai/internal/diag"
	"github.com/baalimago/clai/internal/jsonl"
	"github.com/baalimago/clai/internal/runenv"
	"github.com/baalimago/clai/internal/stdio"
)

// StderrRelay writes MCP server stderr lines to a run's stderr in the run's
// format: one child_output record per line under the JSON format (F14),
// "mcp_<server>: <line>" otherwise. Both carry the same text, redacted as a
// diagnostic is; the JSON string escapes its control characters and the text
// form shows them as visible escapes.
type StderrRelay struct {
	// encoder is nil under the text format.
	encoder   jsonl.Encoder
	sanitizer diag.Sanitizer
}

func NewStderrRelay(env runenv.Env) StderrRelay {
	relay := StderrRelay{sanitizer: diag.NewSanitizer()}
	if !env.JSON() {
		return relay
	}
	if encoder, err := env.Streams().Encoder(stdio.Stderr); err == nil {
		relay.encoder = encoder
	}
	return relay
}

// Redacted is line as both formats carry it, for a display that handles the
// control characters itself.
func (r StderrRelay) Redacted(line string) string {
	return r.sanitizer.Text(line)
}

// Write relays one line of server's stderr: a record on the run's stderr
// Encoder under the JSON format, a text line on textOut otherwise.
func (r StderrRelay) Write(textOut io.Writer, server, line string) error {
	if r.encoder != nil {
		if err := r.encoder.Encode(jsonl.ChildOutputRecord("mcp_"+server, "stderr", r.sanitizer.Text(line))); err != nil {
			return diag.Errorf("relay mcp server %q stderr: %w", server, err)
		}
		return nil
	}
	if _, err := fmt.Fprintf(textOut, "mcp_%v: %v\n", server, r.sanitizer.Line(line)); err != nil {
		return diag.Errorf("relay mcp server %q stderr: %w", server, err)
	}
	return nil
}

// scanStderrLines is a bufio.SplitFunc for a child's stderr: a "\n" and a
// "\r" each end a line, so text a child redraws over with "\r" is a line of
// its own instead of being erased. A "\r\n" leaves an empty line between.
func scanStderrLines(data []byte, atEOF bool) (advance int, token []byte, err error) {
	if i := bytes.IndexAny(data, "\r\n"); i >= 0 {
		return i + 1, data[:i], nil
	}
	if atEOF && len(data) > 0 {
		return len(data), data, nil
	}
	return 0, nil, nil
}
