package vendors_test

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"strconv"
	"strings"
	"testing"

	"github.com/baalimago/clai/internal/vendors"
)

// scanBufferBytes is the scanner's starting buffer, which is also the smallest
// token bound that can be reached without allocating more: a line only fails
// the bound once the buffer it has to grow into is already at the bound.
const scanBufferBytes = 64 << 10

// TestScanJSONLLines_tokenBoundErrorNamesCauseAndBound covers D29. A file
// holding a line above the token bound is discovered as a truncated row and
// cached as one (D28), so the row is listable forever; selecting it runs Read,
// which ends on the same sentinel. Read must not tolerate the truncation —
// handing a silently shortened conversation to a model is worse than failing —
// so what is owed is a message naming the cause and the bound.
//
// The wrap must keep the sentinel reachable through errors.Is: discovery's D28
// gate is that exact call, so an opaque replacement would quietly revert the
// oversized-line row to uncacheable with no test failing.
func TestScanJSONLLines_tokenBoundErrorNamesCauseAndBound(t *testing.T) {
	oversized := `{"text":"` + strings.Repeat("x", scanBufferBytes) + `"}`
	seen := 0
	err := vendors.ScanJSONLLines(strings.NewReader(oversized+"\n"), scanBufferBytes, func(map[string]any) bool {
		seen++
		return true
	})
	if err == nil {
		t.Fatal("a scan ended by the token bound returned no error")
	}
	if seen != 0 {
		t.Fatalf("the oversized line reached the callback %d times; it was never tokenised", seen)
	}
	if !errors.Is(err, bufio.ErrTooLong) {
		t.Fatalf("error %q is no longer the sentinel; D28's discovery gate is errors.Is(err, bufio.ErrTooLong)", err)
	}
	for _, want := range []string{"line", "exceeds", strconv.Itoa(scanBufferBytes)} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not name %q: the dead end stays cryptic", err, want)
		}
	}
	if err.Error() == bufio.ErrTooLong.Error() {
		t.Fatalf("error %q is the raw scanner sentinel, which explains nothing to a user", err)
	}

	t.Run("the bound it was given is the bound it names", func(t *testing.T) {
		wider := scanBufferBytes * 2
		err := vendors.ScanJSONLLines(strings.NewReader(strings.Repeat("y", wider+1)+"\n"), wider, func(map[string]any) bool { return true })
		if err == nil {
			t.Fatal("expected the token bound to end the scan")
		}
		if !strings.Contains(err.Error(), strconv.Itoa(wider)) {
			t.Fatalf("error %q names a bound other than the one in force", err)
		}
	})

	t.Run("any other scanner error is returned as it is", func(t *testing.T) {
		wire := errors.New("read: transport endpoint is not connected")
		r := io.MultiReader(strings.NewReader(`{"a":1}`+"\n"), errReader{wire})
		err := vendors.ScanJSONLLines(r, vendors.ReadMaxToken, func(map[string]any) bool { return true })
		if !errors.Is(err, wire) {
			t.Fatalf("error %q lost the transport failure", err)
		}
		if err.Error() != wire.Error() {
			t.Fatalf("a transport failure was rewritten as %q; only the token bound is wrapped", err)
		}
	})

	t.Run("a scan that reaches the end returns nothing", func(t *testing.T) {
		err := vendors.ScanJSONLLines(bytes.NewReader([]byte(`{"a":1}`+"\n")), vendors.ReadMaxToken, func(map[string]any) bool { return true })
		if err != nil {
			t.Fatalf("a complete scan returned %v", err)
		}
	})
}

// errReader fails every read, so a scanner error that is not the token bound
// is reachable without a filesystem.
type errReader struct{ err error }

func (e errReader) Read([]byte) (int, error) { return 0, e.err }
