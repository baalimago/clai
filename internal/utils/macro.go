package utils

import (
	"io"
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
// transparently switches to the live input so the user continues interacting;
// NewMacroReader has no live input and ends there.
//
// When Live is false (--non-interactive), 10 trailing "q\n" lines are
// appended for graceful termination through nested table loops.
//
// Returns nil when inputs is empty or nil (caller should skip injection).
func NewMacroReader(inputs []string) io.Reader {
	return NewMacroReaderFrom(inputs, strings.NewReader(""))
}

// NewMacroReaderFrom is NewMacroReader whose live mode continues on live,
// the run's input.
func NewMacroReaderFrom(inputs []string, live io.Reader) io.Reader {
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
	return newLiveReader(inputs, live)
}

// liveReader drains a macro-input buffer then falls through to the live
// input. The transition is transparent: when the buffer returns io.EOF the
// next Read call goes directly to the live input.
type liveReader struct {
	buf     *strings.Reader
	live    io.Reader
	drained bool
}

func newLiveReader(inputs []string, live io.Reader) *liveReader {
	var sb strings.Builder
	for _, inp := range inputs {
		sb.WriteString(inp)
		sb.WriteByte('\n')
	}
	return &liveReader{buf: strings.NewReader(sb.String()), live: live}
}

func (r *liveReader) Read(p []byte) (int, error) {
	if r.drained {
		return r.live.Read(p)
	}
	n, err := r.buf.Read(p)
	if err == io.EOF {
		r.drained = true
		if n > 0 {
			return n, nil
		}
		return r.live.Read(p)
	}
	return n, err
}
