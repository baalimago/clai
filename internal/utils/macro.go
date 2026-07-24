package utils

import (
	"io"
	"os"
	"strings"
)

// Live controls whether macro mode switches to interactive stdin after
// consuming all macro inputs (true, default) or appends trailing "q\n"
// terminators for graceful auto-exit (false). Set by Setup() based on
// the --non-interactive flag.
var Live bool

// NewMacroReader creates a reader that yields each input string as a line.
//
// By default (Live=true), after all macro inputs are consumed the reader
// transparently switches to os.Stdin so the user continues interacting.
//
// When Live is false (--non-interactive), 10 trailing "q\n" lines are
// appended for graceful termination through nested table loops.
//
// Returns nil when inputs is empty or nil (caller should skip injection).
func NewMacroReader(inputs []string) io.Reader {
	if len(inputs) == 0 {
		return nil
	}
	if !Live {
		var sb strings.Builder
		for _, inp := range inputs {
			sb.WriteString(inp)
			sb.WriteByte('\n')
		}
		for range 10 {
			sb.WriteString("q\n")
		}
		return strings.NewReader(sb.String())
	}
	return newLiveReader(inputs)
}

// liveReader drains a macro-input buffer then falls through to os.Stdin.
// The transition is transparent: when the buffer returns io.EOF the next
// Read call goes directly to os.Stdin.
type liveReader struct {
	buf     *strings.Reader
	drained bool
}

func newLiveReader(inputs []string) *liveReader {
	var sb strings.Builder
	for _, inp := range inputs {
		sb.WriteString(inp)
		sb.WriteByte('\n')
	}
	return &liveReader{buf: strings.NewReader(sb.String())}
}

func (r *liveReader) Read(p []byte) (int, error) {
	if r.drained {
		return os.Stdin.Read(p)
	}
	n, err := r.buf.Read(p)
	if err == io.EOF {
		r.drained = true
		if n > 0 {
			return n, nil
		}
		return os.Stdin.Read(p)
	}
	return n, err
}
