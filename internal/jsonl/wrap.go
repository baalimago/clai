package jsonl

import (
	"io"
	"log/slog"
)

// Wrapper turns written text into records: the catch-all "output" record of
// F12, or the "child_output" record of a relayed child process.
type Wrapper interface {
	// Write accepts any bytes. Each completed line ("\n") becomes one record
	// of the Wrapper's type, with its fields and a "text" field holding the
	// line without its terminator. (Write.1, F12, F14)
	//
	// Terminal presentation is removed from the text: ANSI CSI and OSC
	// sequences are dropped, and a "\r" not followed by "\n" discards the
	// text before it on that line, as a terminal redraw would; a "\r" that
	// ends one Write and a "\n" that starts the next are one "\r\n".
	// (Write.2, F12)
	//
	// Write returns len(p) and nil once the completed lines are encoded, and
	// the Encoder's error otherwise. (Write.3)
	//
	// Write, Flush, and Close are safe for concurrent use; the lines of one
	// call are encoded before another call's, and a Flush never splits a
	// line another Write is completing. (Write.4, F12)
	io.Writer

	// Flush encodes the pending unterminated line, if any, as one record,
	// so text written before an input read is visible before the read; it
	// returns that Encode's error. (Flush.1, F13)
	Flush() error

	// Close flushes, then makes every later Write return an error matching
	// ErrWrapperClosed. (Close.1)
	io.Closer
}

// ErrWrapperClosed matches a Write after Close.
var ErrWrapperClosed error

// NewWrapper returns the Wrapper that encodes records of recordType with
// fields, then "text", through enc. (NewWrapper.1)
func NewWrapper(enc Encoder, recordType string, fields ...slog.Attr) Wrapper {
	return newWrapper(enc, recordType, fields)
}
