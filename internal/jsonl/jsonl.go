package jsonl

import (
	"io"
	"log/slog"

	"github.com/baalimago/clai/pkg/diagnostic"
)

// Format is the output encoding the owner selects for one run. (F11)
type Format uint8

const (
	// FormatText is clai's terminal or plain text output.
	FormatText Format = iota
	// FormatJSON is one JSON object per line. (F12)
	FormatJSON
)

// Record is one line of JSON output: a record type from the Record types
// table (README F14) and its fields, in order.
type Record struct {
	Type   string
	Fields []slog.Attr
}

type Encoder interface {
	// Encode writes r as one JSON object followed by "\n": the key "type"
	// first with r.Type, then each field in order. Values encode as JSON:
	// a string as a string; a bool as a bool; an integer, float, or duration
	// (in seconds) as a number; a time as an RFC 3339 string; a non-nil
	// error as its Error text; a group attribute or a map[string]any as an
	// object with its keys, an empty one as "{}"; a slice as an array, its
	// elements by the same rules; nil as null. Every field in r is written; a
	// caller leaves an absent optional field out of r. (Encode.1, F11, F12)
	//
	// Every string is valid JSON: control characters, including ESC and
	// "\r", are escaped, never written raw, and invalid UTF-8 is replaced
	// with U+FFFD. (Encode.2, F12)
	//
	// Encode holds the Encoder's lock across its one Write to the
	// destination, so concurrent calls never interleave within a line and
	// the destination needs no lock of its own. (Encode.3, F12)
	//
	// An empty r.Type, or a field named "type", writes nothing and returns
	// an error matching ErrInvalidRecord. A failed or short write returns the
	// writer's error or io.ErrShortWrite. (Encode.4)
	Encode(r Record) error
}

// ErrInvalidRecord matches a record Encode refuses.
var ErrInvalidRecord error

// NewEncoder returns the Encoder that writes to w. (NewEncoder.1)
func NewEncoder(w io.Writer) Encoder {
	return &encoder{destination: w}
}

// EventRecord is the "diagnostic" record of e: the fields name, subsystem,
// level (the F7 level name), message, and facts, an object of e.Facts that
// is "{}" when e has none. (EventRecord.1, F14)
func EventRecord(e diagnostic.Event) Record {
	return Record{Type: "diagnostic", Fields: []slog.Attr{
		slog.String("name", e.Type),
		slog.String("subsystem", e.Subsystem),
		slog.String("level", levelName(e.Level)),
		slog.String("message", e.Message),
		slog.Any("facts", object(e.Facts)),
	}}
}
