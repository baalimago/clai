//go:build !(linux || darwin || freebsd || netbsd || openbsd || dragonfly)

package term

import (
	"runtime"

	"github.com/baalimago/clai/internal/diag"
	"github.com/baalimago/go_away_boilerplate/pkg/dimensions"
)

// openResizeSource reports that this platform has no size-change signal.
func openResizeSource(string, *dimensions.Viewer) (ResizeSource, error) {
	return ResizeSource{}, diag.Errorf("%w on %s", ErrResizeUnsupported, runtime.GOOS)
}
