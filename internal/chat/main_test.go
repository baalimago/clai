package chat

import (
	"fmt"
	"os"
	"testing"

	"github.com/baalimago/clai/internal/testenv"
)

// The table reads TTY, defaulting to /dev/tty, so a test reaching an
// interactive list blocks when run from a terminal. A test wanting real input
// sets TTY to its own FIFO.
func TestMain(m *testing.M) {
	testenv.LimitProcs()
	_ = os.Setenv("TTY", "/dev/null")
	home, err := testenv.IsolateHome()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	code := m.Run()
	_ = os.RemoveAll(string(home))
	os.Exit(code)
}
