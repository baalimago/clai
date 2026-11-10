package diag

import (
	"github.com/baalimago/clai/internal/errctx"
	"github.com/baalimago/clai/pkg/diagnostic"
)

// Errorf attaches inspectable context to the error wrapped by a %w directive.
// Formats without a non-nil wrapped error retain fmt.Errorf behavior.
func Errorf(format string, arguments ...any) error {
	return errctx.ErrorfAt(1, errctx.LocationOnly, format, arguments...)
}

// ErrorfWithStack is Errorf with a bounded stack captured at the attachment site.
func ErrorfWithStack(format string, arguments ...any) error {
	return errctx.ErrorfAt(1, errctx.WithStack, format, arguments...)
}

func sourceLocation(function, file string, line int) diagnostic.SourceLocation {
	return errctx.SourceLocation(function, file, line)
}
