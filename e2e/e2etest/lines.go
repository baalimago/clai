package e2etest

import (
	"strings"
	"testing"
)

// ListingLine returns the line of a listing that names path, failing when no
// line does.
func ListingLine(t *testing.T, output, path string) string {
	t.Helper()
	for line := range strings.SplitSeq(output, "\n") {
		if strings.Contains(line, path) {
			return line
		}
	}
	t.Fatalf("no line names %q in %q", path, output)
	return ""
}
