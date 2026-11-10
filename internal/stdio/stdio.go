package stdio

import (
	"io"

	"github.com/baalimago/clai/internal/jsonl"
)

// Stream names an output destination.
type Stream uint8

const (
	Stdout Stream = iota
	Stderr
)

// Config is what the owner reads from the process once and injects.
type Config struct {
	Format         jsonl.Format
	Stdin          io.Reader
	Stdout         io.Writer
	Stderr         io.Writer
	StdinTerminal  bool
	StdoutTerminal bool
	StderrTerminal bool
}

type OutputProvider interface {
	// Writer returns the writer for s. Under FormatText it is the configured
	// writer; under FormatJSON it is the "output" Wrapper of s, with field
	// stream "stdout" or "stderr", over Encoder(s). (Writer.1, F11, F12)
	Writer(s Stream) io.Writer

	// Encoder returns the one Encoder of s for the run, shared by every
	// record writer of that destination; the same call returns the same
	// Encoder. Under FormatText it returns nil and an error matching
	// ErrTextFormat. (Encoder.1, F12)
	Encoder(s Stream) (jsonl.Encoder, error)

	// IsTerminal reports the configured terminal state of s under
	// FormatText, and false under FormatJSON, so producers choose their
	// pipe form. (IsTerminal.1, F12)
	IsTerminal(s Stream) bool
}

type InputProvider interface {
	// Input returns the configured stdin. Under FormatJSON each Read first
	// flushes the Wrappers of both outputs; when a flush fails, the Read
	// returns that error and reads nothing. A question written before a read
	// is a record before the read starts. (Input.1, F13)
	Input() io.Reader

	// InputIsTerminal reports the configured stdin terminal state in every
	// format. (InputIsTerminal.1, F11, F13)
	InputIsTerminal() bool
}

// StreamProvider is the run's streams. Close closes both Wrappers under
// FormatJSON and returns their errors joined; under FormatText it does
// nothing. (Close.1)
type StreamProvider interface {
	OutputProvider
	InputProvider
	io.Closer
}

// ErrTextFormat matches Encoder under FormatText.
var ErrTextFormat error

// New returns the StreamProvider for cfg. (New.1)
func New(cfg Config) StreamProvider {
	return newProvider(cfg)
}
