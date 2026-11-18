package term

import (
	"errors"
	"io"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/baalimago/go_away_boilerplate/pkg/dimensions"
)

// pipeTTY is a pipe standing in for a terminal: both take read deadlines.
// It reports every deadline that would end a read, and can refuse them.
type pipeTTY struct {
	*os.File
	interrupts   chan struct{}
	refuseCancel error
}

func (p *pipeTTY) SetReadDeadline(t time.Time) error {
	if !t.IsZero() {
		if p.refuseCancel != nil {
			defer func() { p.interrupts <- struct{}{} }()
			return p.refuseCancel
		}
		defer func() { p.interrupts <- struct{}{} }()
	}
	return p.File.SetReadDeadline(t)
}

type resizeHarness struct {
	reader  *ResizeReader
	typed   *os.File
	resized chan dimensions.Dimensions
	tty     *pipeTTY
	width   atomic.Int64
	// measured receives once per measurement after construction.
	measured chan struct{}
	flushes  atomic.Int64
	flushErr error
	stops    atomic.Int64
}

func newResizeHarness(t *testing.T, configure func(*resizeHarness)) *resizeHarness {
	t.Helper()
	rd, wr, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { wr.Close() })
	h := &resizeHarness{
		typed:    wr,
		resized:  make(chan dimensions.Dimensions, 1),
		tty:      &pipeTTY{File: rd, interrupts: make(chan struct{}, 16)},
		measured: make(chan struct{}, 16),
	}
	h.width.Store(120)
	if configure != nil {
		configure(h)
	}
	constructed := false
	reader, err := NewResizeReader(ResizeSource{
		TTY:     h.tty,
		Resized: h.resized,
		Stop:    func() { h.stops.Add(1) },
		Flush: func() error {
			h.flushes.Add(1)
			return h.flushErr
		},
		Measure: func() dimensions.Dimensions {
			if constructed {
				h.measured <- struct{}{}
			}
			return dimensions.Dimensions{Width: int(h.width.Load()), Height: 40}
		},
	})
	if err != nil {
		t.Fatalf("NewResizeReader: %v", err)
	}
	constructed = true
	h.reader = reader
	t.Cleanup(func() { reader.Close() })
	return h
}

// readLine reads as the table does: one byte at a time up to the newline.
func readLine(r io.Reader) (string, error) {
	var line []byte
	b := make([]byte, 1)
	for {
		if _, err := r.Read(b); err != nil {
			return string(line), err
		}
		line = append(line, b[0])
		if b[0] == '\n' {
			return string(line), nil
		}
	}
}

type readResult struct {
	line string
	err  error
}

func readAsync(r io.Reader) <-chan readResult {
	done := make(chan readResult, 1)
	go func() {
		line, err := readLine(r)
		done <- readResult{line, err}
	}()
	return done
}

func TestResizeReader_passesLinesThrough(t *testing.T) {
	h := newResizeHarness(t, nil)
	if _, err := h.typed.WriteString("12\nq\n"); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"12\n", "q\n"} {
		got, err := readLine(h.reader)
		if err != nil || got != want {
			t.Fatalf("read %q, %v; want %q", got, err, want)
		}
	}
}

func TestResizeReader_widthChangeEndsTheRead(t *testing.T) {
	h := newResizeHarness(t, nil)
	pending := readAsync(h.reader)
	<-h.measured
	h.width.Store(80)
	h.resized <- dimensions.Dimensions{}
	got := <-pending
	if !errors.Is(got.err, ErrResized) {
		t.Fatalf("read ended with %q, %v; want ErrResized", got.line, got.err)
	}
	if n := h.flushes.Load(); n != 1 {
		t.Fatalf("typed input was flushed %d times, want 1", n)
	}
	if d := h.reader.Observe(); d.Width != 80 {
		t.Fatalf("Observe() = %+v, want width 80", d)
	}
	if _, err := h.typed.WriteString("3\n"); err != nil {
		t.Fatal(err)
	}
	if line, err := readLine(h.reader); err != nil || line != "3\n" {
		t.Fatalf("after the redraw read %q, %v; want \"3\\n\"", line, err)
	}
}

func TestResizeReader_widthChangeWaitsForTheOpenLine(t *testing.T) {
	h := newResizeHarness(t, nil)
	// Ctrl-D hands the reader a line without its newline.
	if _, err := h.typed.WriteString("2"); err != nil {
		t.Fatal(err)
	}
	if n, err := h.reader.Read(make([]byte, 1)); n != 1 || err != nil {
		t.Fatalf("read %d, %v; want the open line's byte", n, err)
	}
	h.width.Store(80)
	pending := readAsync(h.reader)
	h.resized <- dimensions.Dimensions{}
	<-h.tty.interrupts
	if _, err := h.typed.WriteString("\n"); err != nil {
		t.Fatal(err)
	}
	if got := <-pending; got.err != nil || got.line != "\n" {
		t.Fatalf("read %q, %v; want the open line ended before the redraw", got.line, got.err)
	}
	if _, err := h.reader.Read(make([]byte, 1)); !errors.Is(err, ErrResized) {
		t.Fatalf("read = %v, want ErrResized once the line ended", err)
	}
}

func TestResizeReader_closedEventsKeepReading(t *testing.T) {
	h := newResizeHarness(t, nil)
	close(h.resized)
	if _, err := h.typed.WriteString("4\n"); err != nil {
		t.Fatal(err)
	}
	if line, err := readLine(h.reader); err != nil || line != "4\n" {
		t.Fatalf("read %q, %v; want \"4\\n\" after the size events ended", line, err)
	}
	if n := len(h.tty.interrupts); n != 0 {
		t.Fatalf("closed size events interrupted the read %d times", n)
	}
	if err := h.reader.Close(); err != nil {
		t.Fatalf("Close() = %v", err)
	}
}

func TestResizeReader_unobservedWidthEndsEveryRead(t *testing.T) {
	h := newResizeHarness(t, nil)
	h.width.Store(80)
	for range 2 {
		if _, err := h.reader.Read(make([]byte, 1)); !errors.Is(err, ErrResized) {
			t.Fatalf("read = %v, want ErrResized until Observe", err)
		}
	}
}

func TestResizeReader_sameWidthKeepsReading(t *testing.T) {
	h := newResizeHarness(t, nil)
	pending := readAsync(h.reader)
	<-h.measured
	h.resized <- dimensions.Dimensions{}
	<-h.tty.interrupts
	// The read measures again before it blocks once more.
	<-h.measured
	if _, err := h.typed.WriteString("7\n"); err != nil {
		t.Fatal(err)
	}
	got := <-pending
	if got.err != nil || got.line != "7\n" {
		t.Fatalf("read %q, %v; want \"7\\n\" after a height-only change", got.line, got.err)
	}
	if n := h.flushes.Load(); n != 0 {
		t.Fatalf("typed input was flushed %d times, want 0", n)
	}
}

func TestResizeReader_resizeBetweenReadsIsMetByObserve(t *testing.T) {
	h := newResizeHarness(t, nil)
	h.width.Store(90)
	h.resized <- dimensions.Dimensions{}
	<-h.tty.interrupts
	if d := h.reader.Observe(); d.Width != 90 {
		t.Fatalf("Observe() = %+v, want width 90", d)
	}
	if _, err := h.typed.WriteString("5\n"); err != nil {
		t.Fatal(err)
	}
	if line, err := readLine(h.reader); err != nil || line != "5\n" {
		t.Fatalf("read %q, %v; want \"5\\n\": the redraw already met the new width", line, err)
	}
}

func TestResizeReader_flushFailureEndsTheRead(t *testing.T) {
	flushErr := errors.New("flush refused")
	h := newResizeHarness(t, func(h *resizeHarness) { h.flushErr = flushErr })
	h.width.Store(80)
	if _, err := h.reader.Read(make([]byte, 1)); !errors.Is(err, flushErr) || errors.Is(err, ErrResized) {
		t.Fatalf("read = %v, want the flush failure", err)
	}
}

func TestResizeReader_readFailureIsReturned(t *testing.T) {
	h := newResizeHarness(t, nil)
	if err := h.typed.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := h.reader.Read(make([]byte, 1)); !errors.Is(err, io.EOF) {
		t.Fatalf("read = %v, want io.EOF", err)
	}
}

func TestResizeReader_refusedInterruptIsReturnedByClose(t *testing.T) {
	refused := errors.New("deadline refused")
	h := newResizeHarness(t, func(h *resizeHarness) { h.tty.refuseCancel = refused })
	h.resized <- dimensions.Dimensions{}
	<-h.tty.interrupts
	if err := h.reader.Close(); !errors.Is(err, refused) {
		t.Fatalf("Close() = %v, want the refused interrupt", err)
	}
}

func TestResizeReader_refusedInterruptEndsTheNextRead(t *testing.T) {
	refused := errors.New("deadline refused")
	h := newResizeHarness(t, func(h *resizeHarness) { h.tty.refuseCancel = refused })
	h.resized <- dimensions.Dimensions{}
	<-h.tty.interrupts
	// The watcher sends the failure right after the refused call returns.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if len(h.reader.errs) > 0 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if _, err := h.reader.Read(make([]byte, 1)); !errors.Is(err, refused) {
		t.Fatalf("read = %v, want the refused interrupt", err)
	}
}

func TestResizeReader_closeStopsWatchingOnce(t *testing.T) {
	h := newResizeHarness(t, nil)
	first := h.reader.Close()
	second := h.reader.Close()
	if first != nil || second != nil {
		t.Fatalf("Close() = %v, then %v; want nil twice", first, second)
	}
	if n := h.stops.Load(); n != 1 {
		t.Fatalf("Stop ran %d times, want 1", n)
	}
	if _, err := h.reader.Read(make([]byte, 1)); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("read after Close = %v, want os.ErrClosed", err)
	}
}

func TestResizeReader_closeReportsTheTerminalCloseFailure(t *testing.T) {
	h := newResizeHarness(t, nil)
	if err := h.tty.File.Close(); err != nil {
		t.Fatal(err)
	}
	if err := h.reader.Close(); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("Close() = %v, want the terminal close failure", err)
	}
}

type nopTTY struct{}

func (nopTTY) Read([]byte) (int, error)        { return 0, io.EOF }
func (nopTTY) Close() error                    { return nil }
func (nopTTY) SetReadDeadline(time.Time) error { return nil }

func TestNewResizeReader_rejectsAnIncompleteSource(t *testing.T) {
	complete := func() ResizeSource {
		return ResizeSource{
			TTY:     nopTTY{},
			Resized: make(chan dimensions.Dimensions),
			Stop:    func() {},
			Flush:   func() error { return nil },
			Measure: func() dimensions.Dimensions { return dimensions.Dimensions{Width: 80, Height: 24} },
		}
	}
	for name, strip := range map[string]func(*ResizeSource){
		"tty":     func(s *ResizeSource) { s.TTY = nil },
		"resized": func(s *ResizeSource) { s.Resized = nil },
		"stop":    func(s *ResizeSource) { s.Stop = nil },
		"flush":   func(s *ResizeSource) { s.Flush = nil },
		"measure": func(s *ResizeSource) { s.Measure = nil },
	} {
		t.Run(name, func(t *testing.T) {
			src := complete()
			strip(&src)
			if _, err := NewResizeReader(src); !errors.Is(err, ErrIncompleteResizeSource) {
				t.Fatalf("NewResizeReader() = %v, want ErrIncompleteResizeSource", err)
			}
		})
	}
	reader, err := NewResizeReader(complete())
	if err != nil {
		t.Fatalf("a complete source was refused: %v", err)
	}
	if err := reader.Close(); err != nil {
		t.Fatalf("Close() = %v", err)
	}
}

type closeCountingTTY struct {
	nopTTY
	closes int
}

func (c *closeCountingTTY) Close() error {
	c.closes++
	return nil
}

func TestStartResizeReader(t *testing.T) {
	t.Run("incomplete source is released", func(t *testing.T) {
		tty := &closeCountingTTY{}
		stops := 0
		src := ResizeSource{TTY: tty, Resized: make(chan dimensions.Dimensions), Stop: func() { stops++ }}
		if _, err := startResizeReader(src); !errors.Is(err, ErrIncompleteResizeSource) {
			t.Fatalf("startResizeReader() = %v, want ErrIncompleteResizeSource", err)
		}
		if tty.closes != 1 || stops != 1 {
			t.Fatalf("closed %d and stopped %d times, want once each", tty.closes, stops)
		}
	})
	t.Run("source without resources", func(t *testing.T) {
		if _, err := startResizeReader(ResizeSource{}); !errors.Is(err, ErrIncompleteResizeSource) {
			t.Fatalf("startResizeReader() = %v, want ErrIncompleteResizeSource", err)
		}
	})
	t.Run("complete source", func(t *testing.T) {
		reader, err := startResizeReader(ResizeSource{
			TTY: nopTTY{}, Resized: make(chan dimensions.Dimensions), Stop: func() {},
			Flush:   func() error { return nil },
			Measure: func() dimensions.Dimensions { return dimensions.Fallback },
		})
		if err != nil {
			t.Fatalf("startResizeReader() = %v", err)
		}
		if err := reader.Close(); err != nil {
			t.Fatalf("Close() = %v", err)
		}
	})
}
