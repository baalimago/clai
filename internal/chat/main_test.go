package chat

import (
	"os"
	"testing"
)

// The table reads TTY, defaulting to /dev/tty, so a test reaching an
// interactive list blocks when run from a terminal. A test wanting real input
// sets TTY to its own FIFO.
func TestMain(m *testing.M) {
	_ = os.Setenv("TTY", "/dev/null")
	os.Exit(m.Run())
}
