// Package process is the composition roots' one view of the process's own
// input: the CLI's, and pkg/agent's when its caller supplies no run
// environment. No other package reads the process streams (phase 9,
// criterion 17 of worklog 2026-11-02-diagnostics).
package process

import (
	"io"
	"os"

	"github.com/baalimago/clai/internal/term"
)

// Stdin is the process's stdin and whether it is a terminal.
func Stdin() (io.Reader, bool) {
	return os.Stdin, term.IsTerminal(os.Stdin)
}
