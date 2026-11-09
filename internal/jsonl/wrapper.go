package jsonl

import (
	"errors"
	"log/slog"
	"slices"
	"sync"

	"github.com/baalimago/clai/internal/errctx"
)

func init() {
	ErrWrapperClosed = errors.New("jsonl: wrapper closed")
}

const (
	escape = 0x1b
	bell   = 0x07
)

// scanState is where the terminal-sequence scanner stands between bytes,
// kept across Writes so a sequence split between them is still dropped.
type scanState uint8

const (
	scanText scanState = iota
	scanEscape
	// scanEscapeIntermediate: an ESC and intermediate bytes (0x20–0x2f)
	// wait for the final byte (0x30–0x7e) that ends the sequence.
	scanEscapeIntermediate
	scanCSI
	scanOSC
	scanOSCEscape
)

// lineState records what the pending line has already produced.
type lineState uint8

const (
	lineFresh lineState = iota
	// lineFlushed: Flush emitted the line's text so far; an empty rest
	// completes it without a further record.
	lineFlushed
)

type wrapper struct {
	mu         sync.Mutex
	encoder    Encoder
	recordType string
	fields     []slog.Attr

	pending        []byte
	scan           scanState
	line           lineState
	carriageReturn bool
	closed         bool
}

func newWrapper(enc Encoder, recordType string, fields []slog.Attr) *wrapper {
	return &wrapper{encoder: enc, recordType: recordType, fields: slices.Clone(fields)}
}

func (w *wrapper) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return 0, errctx.Errorf("jsonl: write %d bytes: %w", len(p), ErrWrapperClosed)
	}
	for i, b := range p {
		if err := w.scanByte(b); err != nil {
			return i + 1, err
		}
	}
	return len(p), nil
}

func (w *wrapper) scanByte(b byte) error {
	switch w.scan {
	case scanEscape, scanEscapeIntermediate:
		switch {
		case w.scan == scanEscape && b == '[':
			w.scan = scanCSI
			return nil
		case w.scan == scanEscape && b == ']':
			w.scan = scanOSC
			return nil
		case b >= 0x20 && b <= 0x2f:
			w.scan = scanEscapeIntermediate
			return nil
		case b >= 0x30 && b <= 0x7e:
			w.scan = scanText
			return nil
		}
		w.scan = scanText
	case scanCSI:
		switch {
		case b >= 0x20 && b <= 0x3f:
			return nil
		case b >= 0x40 && b <= 0x7e:
			w.scan = scanText
			return nil
		}
		w.scan = scanText
	case scanOSC, scanOSCEscape:
		switch {
		case b == bell, w.scan == scanOSCEscape && b == '\\':
			w.scan = scanText
			return nil
		case b == escape:
			w.scan = scanOSCEscape
			return nil
		case b != '\n':
			w.scan = scanOSC
			return nil
		}
		w.scan = scanText
	}
	return w.textByte(b)
}

func (w *wrapper) textByte(b byte) error {
	if w.carriageReturn {
		w.carriageReturn = false
		if b == '\n' {
			return w.completeLine()
		}
		w.pending = w.pending[:0]
		w.line = lineFresh
	}
	switch b {
	case '\n':
		return w.completeLine()
	case '\r':
		w.carriageReturn = true
	case escape:
		w.scan = scanEscape
	default:
		w.pending = append(w.pending, b)
	}
	return nil
}

func (w *wrapper) completeLine() error {
	if len(w.pending) == 0 && w.line == lineFlushed {
		w.line = lineFresh
		return nil
	}
	w.line = lineFresh
	return w.emit()
}

func (w *wrapper) emit() error {
	text := string(w.pending)
	w.pending = w.pending[:0]
	fields := make([]slog.Attr, 0, len(w.fields)+1)
	fields = append(fields, w.fields...)
	fields = append(fields, slog.String("text", text))
	if err := w.encoder.Encode(Record{Type: w.recordType, Fields: fields}); err != nil {
		return errctx.Errorf("jsonl: wrap a %q line: %w", w.recordType, err)
	}
	return nil
}

func (w *wrapper) Flush() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.flush()
}

func (w *wrapper) flush() error {
	if len(w.pending) == 0 {
		return nil
	}
	w.line = lineFlushed
	return w.emit()
}

func (w *wrapper) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return nil
	}
	w.closed = true
	return w.flush()
}
