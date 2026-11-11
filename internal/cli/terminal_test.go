package cli

import (
	"os"
	"strings"
	"testing"

	"github.com/baalimago/clai/internal/jsonl"
)

// TestOSProcessTerminalStates pins one terminal rule for the three process
// streams: the terminal driver decides, so /dev/null, a character device
// but no terminal, is not one for stdout and stderr either.
func TestOSProcessTerminalStates(t *testing.T) {
	devNull, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err != nil {
		t.Fatalf("open %s: %v", os.DevNull, err)
	}
	stdin, stdout, stderr := os.Stdin, os.Stdout, os.Stderr
	t.Cleanup(func() {
		os.Stdin, os.Stdout, os.Stderr = stdin, stdout, stderr
		_ = devNull.Close()
	})
	os.Stdin, os.Stdout, os.Stderr = devNull, devNull, devNull
	p := osProcess()
	if p.stdinTerminal || p.stdoutTerminal || p.stderrTerminal {
		t.Fatalf("terminal states stdin %v stdout %v stderr %v on %s, want none", p.stdinTerminal, p.stdoutTerminal, p.stderrTerminal, os.DevNull)
	}
}

// TestComposeColorFollowsTruthyNoColor pins one NO_COLOR rule for the
// diagnostic presenter and the text producers: any value misc.Truthy
// accepts turns colour off (phase 6 Deliver.3, the Legacy controls table).
func TestComposeColorFollowsTruthyNoColor(t *testing.T) {
	useConfigDir(t)
	for _, tc := range []struct {
		noColor string
		want    bool
	}{{"", true}, {"true", false}, {"1", false}, {"TRUE", false}, {"yes", true}, {"false", true}} {
		var stderr strings.Builder
		p := process{
			stdin:          strings.NewReader(""),
			stdout:         &strings.Builder{},
			stderr:         &stderr,
			stdoutTerminal: true,
			stderrTerminal: true,
			getenv: func(key string) string {
				if key == "NO_COLOR" {
					return tc.noColor
				}
				return ""
			},
		}
		c, err := compose(p, jsonl.FormatText, sharedBufferSize)
		if err != nil {
			t.Fatalf("compose: %v", err)
		}
		if got := c.env.Color(); got != tc.want {
			t.Fatalf("NO_COLOR=%q: producers' colour %v, want %v", tc.noColor, got, tc.want)
		}
		if err := c.presenter.ReportError(t.Context(), os.ErrNotExist); err != nil {
			t.Fatalf("report: %v", err)
		}
		if colored := strings.Contains(stderr.String(), "\x1b["); colored != tc.want {
			t.Fatalf("NO_COLOR=%q: presenter colour %v, want %v: %q", tc.noColor, colored, tc.want, stderr.String())
		}
	}
}
