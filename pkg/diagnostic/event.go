package diagnostic

import (
	"context"
	"log/slog"
	"time"
)

type OutputMode uint8

const (
	Immediate OutputMode = iota
	Buffered
)

// Type registers one kind of diagnostic. Name is unique within a registry.
type Type struct {
	Name           string
	Subsystem      string
	Level          Level
	Mode           OutputMode
	ActionRequired bool
}

type Event struct {
	// Type is the registered name. Subsystem and Level are that type's
	// registered subsystem and level, filled by the Reporter.
	Type      string
	Subsystem string
	Level     Level
	Message   string
	Facts     []slog.Attr
}

// Deliverer is one output destination. The owner supplies one per OutputMode.
type Deliverer interface {
	// Deliver writes one event to its destination and returns a failed
	// delivery. It applies no level policy of its own. Concurrent calls are
	// safe. (Deliver.1, F6)
	Deliver(context.Context, Event) error
}

// SlogDeliverer adapts a slog.Handler as a Deliverer. The event's facts and
// subsystem become the record's attributes, the subsystem is bound before
// Enabled is consulted at the event's Level, and Handle's error is Deliver's
// error. A handler that is not enabled for that level receives nothing.
// (SlogDeliverer.1, F6)
func SlogDeliverer(handler slog.Handler) Deliverer {
	return slogDeliverer{handler: handler}
}

type slogDeliverer struct {
	handler slog.Handler
}

func (d slogDeliverer) Deliver(ctx context.Context, event Event) error {
	handler := d.handler.WithAttrs([]slog.Attr{slog.String("subsystem", event.Subsystem)})
	level := slogLevel(event.Level)
	if !handler.Enabled(ctx, level) {
		return nil
	}
	record := slog.NewRecord(time.Now(), level, event.Message, 0)
	record.AddAttrs(event.Facts...)
	return handler.Handle(ctx, record)
}

func slogLevel(level Level) slog.Level {
	switch level {
	case LevelError:
		return slog.LevelError
	case LevelWarn:
		return slog.LevelWarn
	case LevelInfo:
		return slog.LevelInfo
	case LevelDebug:
		return slog.LevelDebug
	case LevelTrace:
		return slog.LevelDebug - 4
	default:
		return slog.Level(level)
	}
}

type Reporter interface {
	// Report accepts an event of a registered type and delivers it through
	// the owner's Deliverer for the type's registered mode. The routing
	// implementation and its constructor are internal to clai. (Report.1, F4, F10)
	//
	// An event below its subsystem's resolved level is silent and not a
	// failure. An action-required event is delivered at every level.
	// (Report.2, F7)
	//
	// Report returns an error that names an unknown type, and returns a
	// failed delivery. Concurrent calls are safe and share no process-wide
	// state. (Report.3, F6)
	Report(context.Context, Event) error
}
