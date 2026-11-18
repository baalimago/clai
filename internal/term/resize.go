package term

import (
	"errors"
	"io"
	"os"
	"sync"
	"time"

	"github.com/baalimago/clai/internal/diag"
	"github.com/baalimago/go_away_boilerplate/pkg/dimensions"
)

// ErrResized ends a ResizeReader read while the terminal width differs from
// the last Observe.
var ErrResized = errors.New("terminal width changed")

// ErrResizeUnsupported marks a terminal whose size changes cannot end a read.
var ErrResizeUnsupported = errors.New("terminal reads cannot follow size changes")

// ErrIncompleteResizeSource marks a ResizeSource with a field left unset.
var ErrIncompleteResizeSource = errors.New("incomplete resize source")

// DeadlineReader is terminal input whose blocked read a deadline ends.
type DeadlineReader interface {
	io.ReadCloser
	SetReadDeadline(t time.Time) error
}

// ResizeSource is the terminal a ResizeReader reads and watches. Every field
// but Settle is required.
type ResizeSource struct {
	TTY DeadlineReader
	// Resized receives a value per size change; it may close.
	Resized <-chan dimensions.Dimensions
	// Stop ends the deliveries to Resized.
	Stop func()
	// Flush discards input typed but not yet read.
	Flush   func() error
	Measure func() dimensions.Dimensions
	// Settle is how long size changes must pause before a read is ended.
	Settle time.Duration
}

// ResizeReader reads lines from a terminal. A read at a line boundary ends
// with ErrResized, after the typed input is discarded, while the width differs
// from the last Observe, so the caller can redraw at the new width before
// reading on.
type ResizeReader struct {
	src     ResizeSource
	width   int
	buf     []byte
	pending []byte
	// lineOpen holds while the bytes handed out so far end inside a line.
	lineOpen bool

	errs      chan error
	done      chan struct{}
	stopped   chan struct{}
	closeOnce sync.Once
	closeErr  error
}

// NewResizeReader starts watching src; Close stops it and closes src.TTY.
func NewResizeReader(src ResizeSource) (*ResizeReader, error) {
	for name, missing := range map[string]bool{
		"TTY":     src.TTY == nil,
		"Resized": src.Resized == nil,
		"Stop":    src.Stop == nil,
		"Flush":   src.Flush == nil,
		"Measure": src.Measure == nil,
	} {
		if missing {
			return nil, diag.Errorf("%w: no %s", ErrIncompleteResizeSource, name)
		}
	}
	r := &ResizeReader{
		src:     src,
		width:   src.Measure().Width,
		buf:     make([]byte, 4096),
		errs:    make(chan error, 1),
		done:    make(chan struct{}),
		stopped: make(chan struct{}),
	}
	go r.watch()
	return r, nil
}

const controllingTerminal = "/dev/tty"

// OpenResizeReader reads the controlling terminal and takes over size, the
// viewer of the terminal the caller draws on: Close, or a failed open, stops
// it. The error wraps ErrResizeUnsupported when there is no controlling
// terminal or its reads take no deadline.
func OpenResizeReader(size *dimensions.Viewer) (*ResizeReader, error) {
	return openResizeReader(controllingTerminal, size)
}

func openResizeReader(path string, size *dimensions.Viewer) (*ResizeReader, error) {
	src, err := openResizeSource(path, size)
	if err != nil {
		size.Stop()
		return nil, err
	}
	return startResizeReader(src)
}

// viewedSize measures with size; a failed read keeps the last size, as
// utils.SessionDimensions falls back.
func viewedSize(size *dimensions.Viewer) func() dimensions.Dimensions {
	return func() dimensions.Dimensions {
		d, _ := size.Snapshot()
		return d
	}
}

// startResizeReader releases what an incomplete source holds.
func startResizeReader(src ResizeSource) (*ResizeReader, error) {
	reader, err := NewResizeReader(src)
	if err == nil {
		return reader, nil
	}
	if src.Stop != nil {
		src.Stop()
	}
	if src.TTY != nil {
		err = errors.Join(err, src.TTY.Close())
	}
	return nil, err
}

// Observe measures the terminal the caller is about to draw on.
func (r *ResizeReader) Observe() dimensions.Dimensions {
	d := r.src.Measure()
	r.width = d.Width
	return d
}

func (r *ResizeReader) Read(p []byte) (int, error) {
	if len(r.pending) == 0 {
		if err := r.fill(); err != nil {
			return 0, err
		}
	}
	n := copy(p, r.pending)
	r.pending = r.pending[n:]
	if n > 0 {
		r.lineOpen = p[n-1] != '\n'
	}
	return n, nil
}

func (r *ResizeReader) fill() error {
	for {
		if err := r.watchErr(); err != nil {
			return err
		}
		// A read ending inside a line would hand the line on as typed.
		if !r.lineOpen && r.src.Measure().Width != r.width {
			if err := r.src.Flush(); err != nil {
				return diag.Errorf("discard the input typed before the resize: %w", err)
			}
			return ErrResized
		}
		n, err := r.src.TTY.Read(r.buf)
		if n > 0 {
			r.pending = r.buf[:n]
			return nil
		}
		if !errors.Is(err, os.ErrDeadlineExceeded) {
			return diag.Errorf("read the terminal: %w", err)
		}
		if err := r.src.TTY.SetReadDeadline(time.Time{}); err != nil {
			return diag.Errorf("resume the terminal read: %w", err)
		}
	}
}

// watch ends the pending read once size changes pause; fill decides whether
// the width changed.
func (r *ResizeReader) watch() {
	defer close(r.stopped)
	var settled <-chan time.Time
	resized := r.src.Resized
	for {
		select {
		case <-r.done:
			return
		case _, ok := <-resized:
			if !ok {
				resized = nil
				continue
			}
			settled = time.After(r.src.Settle)
		case <-settled:
			settled = nil
			if err := r.src.TTY.SetReadDeadline(time.Now()); err != nil {
				r.errs <- diag.Errorf("interrupt the terminal read: %w", err)
				return
			}
		}
	}
}

func (r *ResizeReader) watchErr() error {
	select {
	case err := <-r.errs:
		return err
	default:
		return nil
	}
}

// Close stops the watch and closes the terminal; a watch failure no read
// returned is reported here.
func (r *ResizeReader) Close() error {
	r.closeOnce.Do(func() {
		r.src.Stop()
		close(r.done)
		<-r.stopped
		errs := []error{r.watchErr()}
		if err := r.src.TTY.Close(); err != nil {
			errs = append(errs, diag.Errorf("close the terminal: %w", err))
		}
		r.closeErr = errors.Join(errs...)
	})
	return r.closeErr
}
