package utils

import (
	"io"
	"os"

	"github.com/baalimago/go_away_boilerplate/pkg/dimensions"
)

// SessionDimensions resolves the one terminal-size snapshot for w. The
// snapshot is bound to the writer's file descriptor, so the observed size
// matches the file the output is actually written to (R2-02): a terminal
// yields its live size, and a non-terminal writer, a failed read, or a
// reported zero size deterministically yields dimensions.Fallback (80x24).
// No error propagates, matching the legacy silent fallback. A nil writer
// resolves to os.Stdout.
//
// This is the single width-discovery point of clai. Every width-aware render
// path either stores the result of this helper for its session (the querier's
// snapshot) or calls it once at the start of a standalone render operation.
func SessionDimensions(w io.Writer) dimensions.Dimensions {
	if w == nil {
		w = os.Stdout
	}
	if f, ok := w.(*os.File); ok {
		if d, err := dimensions.DefaultReader(f.Fd())(); err == nil {
			return d
		}
	}
	return dimensions.Fallback
}
