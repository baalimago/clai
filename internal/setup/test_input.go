package setup

import (
	"io"
)

// useReadUserInputForTests bridges the old table.UseReadUserInputForTests
// pattern with the new Input mechanism for setup-package tests.
func useReadUserInputForTests(fn func() (string, error)) func() {
	old := Input
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
	Input = r
	return func() {
		Input = old
		r.Close()
	}
}
