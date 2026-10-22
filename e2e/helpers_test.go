package e2e

import (
	"io"

	"github.com/baalimago/clai/internal/cli"
	"github.com/baalimago/clai/internal/setup"
)

const cpuProfileFileName = cli.CPUProfileFileName

// useReadUserInputForTests bridges the old table.UseReadUserInputForTests
// pattern with the new setup.Input mechanism for the e2e tests.
func useReadUserInputForTests(fn func() (string, error)) func() {
	old := setup.Input
	r, w := io.Pipe()
	go func() {
		defer w.Close()
		for {
			s, err := fn()
			if err != nil {
				w.CloseWithError(err)
				return
			}
			if _, writeErr := io.WriteString(w, s+"\n"); writeErr != nil {
				return
			}
		}
	}()
	setup.Input = r
	return func() {
		setup.Input = old
		r.Close()
	}
}
