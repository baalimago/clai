// Package errctx attaches inspectable context to wrapped errors. It imports
// nothing under internal/, so every internal package may use it.
package errctx

import (
	"errors"
	"fmt"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/baalimago/clai/pkg/diagnostic"
)

const stackLimit = 32

// Capture selects what an attachment records besides its location.
type Capture int

const (
	// LocationOnly records the attachment site.
	LocationOnly Capture = iota
	// WithStack also records a bounded stack starting at the attachment site.
	WithStack
)

type contextError struct {
	cause     error
	text      string
	operation string
	location  diagnostic.SourceLocation
	stack     []diagnostic.SourceLocation
}

func (e *contextError) Error() string                       { return e.text }
func (e *contextError) Unwrap() error                       { return e.cause }
func (e *contextError) Operation() string                   { return e.operation }
func (e *contextError) Location() diagnostic.SourceLocation { return e.location }
func (e *contextError) Stack() []diagnostic.SourceLocation {
	return append([]diagnostic.SourceLocation(nil), e.stack...)
}

// Errorf attaches inspectable context to the error wrapped by a %w directive.
// Formats without a non-nil wrapped error retain fmt.Errorf behavior.
func Errorf(format string, arguments ...any) error {
	return errorf(0, LocationOnly, format, arguments...)
}

// ErrorfWithStack is Errorf with a bounded stack captured at the attachment site.
func ErrorfWithStack(format string, arguments ...any) error {
	return errorf(0, WithStack, format, arguments...)
}

// ErrorfAt is Errorf for wrappers: the attachment site is skip frames above
// the caller of ErrorfAt.
func ErrorfAt(skip int, capture Capture, format string, arguments ...any) error {
	return errorf(skip, capture, format, arguments...)
}

func errorf(skip int, capture Capture, format string, arguments ...any) error {
	formatted := fmt.Errorf(format, arguments...)
	cause := wrappedCause(formatted)
	if cause == nil {
		return formatted
	}

	location := callerLocation(3 + skip)
	var stack []diagnostic.SourceLocation
	if capture == WithStack {
		stack = captureLocations(4 + skip)
	}
	return &contextError{
		cause:     cause,
		text:      formatted.Error(),
		operation: operation(formatted.Error(), cause),
		location:  location,
		stack:     stack,
	}
}

func wrappedCause(err error) error {
	if single, ok := err.(interface{ Unwrap() error }); ok {
		return single.Unwrap()
	}
	if multiple, ok := err.(interface{ Unwrap() []error }); ok {
		causes := multiple.Unwrap()
		if len(causes) > 0 {
			return errors.Join(causes...)
		}
	}
	return nil
}

func operation(text string, cause error) string {
	if prefix, found := strings.CutSuffix(text, ": "+cause.Error()); found {
		return prefix
	}
	if prefix, found := strings.CutSuffix(text, cause.Error()); found {
		return strings.TrimRight(prefix, ":,; ")
	}
	return text
}

func captureLocations(skip int) []diagnostic.SourceLocation {
	pcs := make([]uintptr, stackLimit)
	count := runtime.Callers(skip, pcs)
	frames := runtime.CallersFrames(pcs[:count])
	locations := make([]diagnostic.SourceLocation, 0, count)
	for len(locations) < stackLimit {
		frame, more := frames.Next()
		location := SourceLocation(frame.Function, frame.File, frame.Line)
		if location != (diagnostic.SourceLocation{}) {
			locations = append(locations, location)
		}
		if !more {
			break
		}
	}
	return locations
}

func callerLocation(skip int) diagnostic.SourceLocation {
	pc, file, line, ok := runtime.Caller(skip)
	if !ok {
		return diagnostic.SourceLocation{}
	}
	function := ""
	if details := runtime.FuncForPC(pc); details != nil {
		function = details.Name()
	}
	return SourceLocation(function, file, line)
}

// SourceLocation reports a frame as a module-relative location, or the zero
// location when the frame is unavailable or outside the module.
func SourceLocation(function, file string, line int) diagnostic.SourceLocation {
	if function == "" || file == "" || line <= 0 {
		return diagnostic.SourceLocation{}
	}
	if !filepath.IsAbs(file) && file != "." && !strings.HasPrefix(file, ".."+string(filepath.Separator)) {
		return diagnostic.SourceLocation{Function: function, File: filepath.ToSlash(file), Line: line}
	}

	const module = "github.com/baalimago/clai"
	if !strings.HasPrefix(function, module) {
		return diagnostic.SourceLocation{}
	}
	packagePath, _, _ := strings.Cut(strings.TrimPrefix(function, module), ".")
	packagePath = strings.TrimSuffix(packagePath, "_test")
	relative := filepath.Join(filepath.FromSlash(strings.TrimPrefix(packagePath, "/")), filepath.Base(file))
	return diagnostic.SourceLocation{Function: function, File: filepath.ToSlash(relative), Line: line}
}
