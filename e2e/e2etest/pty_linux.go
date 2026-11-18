//go:build linux

package e2etest

import (
	"os"
	"testing"

	"github.com/baalimago/clai/internal/testenv"
)

// OpenPTY allocates a pseudo-terminal pair and returns the master and slave
// ends. The parent keeps the master; the slave becomes the child's controlling
// terminal via SysProcAttr.
func OpenPTY(t *testing.T) (master, slave *os.File) {
	t.Helper()
	return testenv.OpenPTY(t)
}
