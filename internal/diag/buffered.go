package diag

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/baalimago/clai/pkg/diagnostic"
)

const truncationMarker = "[truncated]"

type bufferedEvent struct {
	event diagnostic.Event
	line  string
}

type bufferedDeliverer struct {
	dir       string
	size      ByteSize
	immediate diagnostic.Deliverer

	mu     sync.Mutex
	buffer []bufferedEvent
	bytes  ByteSize
	paths  []string
}

func (b *bufferedDeliverer) Deliver(ctx context.Context, event diagnostic.Event) error {
	entry := b.bounded(event)

	b.mu.Lock()
	defer b.mu.Unlock()
	if b.size <= 0 {
		b.buffer = append(b.buffer, entry)
		return b.flush(ctx)
	}
	var overflowErr error
	if len(b.buffer) > 0 && b.bytes+ByteSize(len(entry.line)) > b.size {
		overflowErr = b.flush(ctx)
	}
	b.buffer = append(b.buffer, entry)
	b.bytes += ByteSize(len(entry.line))
	if b.bytes >= b.size {
		return errors.Join(overflowErr, b.flush(ctx))
	}
	return overflowErr
}

func (b *bufferedDeliverer) Finish(ctx context.Context) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	var pending []diagnostic.Event
	if len(b.paths) > 0 {
		pending = append(pending, diagnostic.Event{
			Type:      "diagnostic_spill_files",
			Subsystem: "diagnostics",
			Level:     diagnostic.LevelWarn,
			Message:   "buffered diagnostics were written to spill files",
			Facts:     []slog.Attr{slog.Any("paths", append([]string(nil), b.paths...))},
		})
	}
	for _, entry := range b.buffer {
		pending = append(pending, entry.event)
	}
	b.reset()
	b.paths = nil

	delivered, err := b.deliverAll(ctx, pending)
	if err == nil {
		return nil
	}
	if delivered == 0 {
		return Errorf("finish diagnostic buffer: %w: %w", ErrUndeliverable, err)
	}
	return Errorf("finish diagnostic buffer: %w", err)
}

// flush spills the buffer, or delivers it through immediate when the spill
// fails, and resets it either way. The caller holds b.mu.
func (b *bufferedDeliverer) flush(ctx context.Context) error {
	events := b.buffer
	path, spillErr := b.spill(events)
	b.reset()
	if spillErr == nil {
		b.paths = append(b.paths, path)
		return nil
	}
	pending := make([]diagnostic.Event, 0, len(events))
	for _, entry := range events {
		pending = append(pending, entry.event)
	}
	if _, deliverErr := b.deliverAll(ctx, pending); deliverErr != nil {
		return Errorf("flush diagnostic buffer: %w: spill: %w: immediate: %w", ErrUndeliverable, spillErr, deliverErr)
	}
	return nil
}

func (b *bufferedDeliverer) reset() {
	b.buffer = nil
	b.bytes = 0
}

func (b *bufferedDeliverer) deliverAll(ctx context.Context, events []diagnostic.Event) (int, error) {
	var failures []error
	delivered := 0
	for _, event := range events {
		if err := b.immediate.Deliver(ctx, event); err != nil {
			failures = append(failures, err)
			continue
		}
		delivered++
	}
	return delivered, errors.Join(failures...)
}

func (b *bufferedDeliverer) spill(events []bufferedEvent) (string, error) {
	if b.dir == "" {
		return "", errors.New("spill diagnostic buffer: no spill-file directory")
	}
	if err := os.MkdirAll(b.dir, 0o700); err != nil {
		return "", Errorf("create spill-file directory: %w", err)
	}
	stamp := time.Now().UTC().Format("20060102T150405.000000000Z")
	file, err := os.CreateTemp(b.dir, "diagnostics-"+stamp+"-*.log")
	if err != nil {
		return "", Errorf("create spill file: %w", err)
	}
	var content strings.Builder
	for _, entry := range events {
		content.WriteString(entry.line)
	}
	_, writeErr := file.WriteString(content.String())
	closeErr := file.Close()
	if err := errors.Join(writeErr, closeErr); err != nil {
		return "", Errorf("write spill file %s: %w", file.Name(), errors.Join(err, os.Remove(file.Name())))
	}
	return file.Name(), nil
}

// bounded returns the event with its spill line, truncating the message and
// dropping the facts of an event whose line exceeds the buffer size.
func (b *bufferedDeliverer) bounded(event diagnostic.Event) bufferedEvent {
	line := encodeLine(event)
	if b.size <= 0 || ByteSize(len(line)) <= b.size {
		return bufferedEvent{event: event, line: line}
	}
	original := event.Message
	event.Facts = nil
	keep := len(original)
	for {
		for keep > 0 && keep < len(original) && !utf8.RuneStart(original[keep]) {
			keep--
		}
		event.Message = truncationMarker
		if keep > 0 {
			event.Message = original[:keep] + " " + truncationMarker
		}
		line = encodeLine(event)
		over := ByteSize(len(line)) - b.size
		if over <= 0 || keep == 0 {
			return bufferedEvent{event: event, line: line}
		}
		keep = max(0, keep-int(over))
	}
}

func encodeLine(event diagnostic.Event) string {
	var line strings.Builder
	line.WriteString("level=")
	line.WriteString(levelLabel(event.Level))
	line.WriteString(" subsystem=")
	line.WriteString(strconv.Quote(event.Subsystem))
	line.WriteString(" type=")
	line.WriteString(strconv.Quote(event.Type))
	line.WriteString(" message=")
	line.WriteString(strconv.Quote(event.Message))
	for _, fact := range event.Facts {
		line.WriteByte(' ')
		line.WriteString(fact.Key)
		line.WriteByte('=')
		line.WriteString(strconv.Quote(fact.Value.String()))
	}
	line.WriteByte('\n')
	return line.String()
}

func levelLabel(level diagnostic.Level) string {
	switch level {
	case diagnostic.LevelError:
		return "error"
	case diagnostic.LevelWarn:
		return "warn"
	case diagnostic.LevelInfo:
		return "info"
	case diagnostic.LevelDebug:
		return "debug"
	case diagnostic.LevelTrace:
		return "trace"
	default:
		return strconv.Itoa(int(level))
	}
}
