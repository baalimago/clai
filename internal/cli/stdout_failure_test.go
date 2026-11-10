package cli

import (
	"errors"
	"slices"
	"strings"
	"testing"
)

var errStdoutFull = errors.New("no space left on device")

type fullWriter struct{}

func (fullWriter) Write([]byte) (int, error) { return 0, errStdoutFull }

// TestFailedReplyWriteFailsTheRun pins that a reply which cannot be written
// to stdout, as with >/dev/full or a closed stdout, is a non-zero exit with
// an error on stderr in text and under -json.
func TestFailedReplyWriteFailsTheRun(t *testing.T) {
	for _, args := range []string{"-r -cm test q hello", "-cm test q hello", "-json -cm test q hello", "-reply-only -cm test q hello"} {
		t.Run(args, func(t *testing.T) {
			useConfigDir(t)
			got := runProcess(t, args, func(p *process, _ *Deps) { p.stdout = fullWriter{} })
			if got.status == 0 || !strings.Contains(got.stderr, errStdoutFull.Error()) || !strings.Contains(got.stderr, "error") {
				t.Fatalf("got %+v, want a non-zero exit and an error naming the failed write to stdout", got)
			}
			if strings.HasPrefix(args, "-json") && !slices.Contains(recordTypes(t, got.stderr), "error") {
				t.Fatalf("stderr %q, want an error record", got.stderr)
			}
		})
	}
}
