package utils

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/baalimago/go_away_boilerplate/pkg/dimensions"
)

// TestSessionDimensions_NonFileWriterFallsBack proves that a writer which is
// not an *os.File (buffers, builders, io.Discard) has no file descriptor to
// query, so the deterministic fallback applies. This is the common shape of
// captured output in tests and pipes in production.
func TestSessionDimensions_NonFileWriterFallsBack(t *testing.T) {
	if got := SessionDimensions(&strings.Builder{}); got != dimensions.Fallback {
		t.Fatalf("SessionDimensions(builder) = %+v, want fallback %+v", got, dimensions.Fallback)
	}
	if got := SessionDimensions(nil); got != dimensions.Fallback {
		t.Fatalf("SessionDimensions(nil) = %+v, want fallback %+v", got, dimensions.Fallback)
	}
}

// TestSessionDimensions_RegularFileFallsBack proves that a real file
// descriptor which is not a terminal (a regular file) yields the
// deterministic fallback: the ioctl fails, and no environment lookup or
// fabrication happens at this boundary.
func TestSessionDimensions_RegularFileFallsBack(t *testing.T) {
	path := filepath.Join(t.TempDir(), "out.txt")
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create file: %v", err)
	}
	defer f.Close()

	got := SessionDimensions(f)
	if got != dimensions.Fallback {
		t.Fatalf("SessionDimensions(regular file) = %+v, want fallback %+v", got, dimensions.Fallback)
	}
}

// TestSessionDimensions_UsableSize proves that SessionDimensions always
// returns a usable size: a live terminal yields its real size, and every
// failure path yields the fallback. Width and height are therefore always
// positive, so width-aware render paths never divide by zero or emit
// malformed output.
func TestSessionDimensions_UsableSize(t *testing.T) {
	got := SessionDimensions(os.Stdout)
	if got.Width <= 0 || got.Height <= 0 {
		t.Fatalf("SessionDimensions(os.Stdout) = %+v, want positive width and height", got)
	}
}
