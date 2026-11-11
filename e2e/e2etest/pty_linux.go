//go:build linux

package e2etest

import (
	"fmt"
	"os"
	"syscall"
	"testing"
	"unsafe"
)

// OpenPTY allocates a pseudo-terminal pair and returns the master and slave
// ends. The parent keeps the master; the slave becomes the child's controlling
// terminal via SysProcAttr.
func OpenPTY(t *testing.T) (master, slave *os.File) {
	t.Helper()
	master, err := os.OpenFile("/dev/ptmx", os.O_RDWR, 0)
	if err != nil {
		t.Fatalf("open /dev/ptmx: %v", err)
	}
	// Unlock the slave and learn its number.
	var unlock int32
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, master.Fd(), syscall.TIOCSPTLCK, uintptr(unsafe.Pointer(&unlock))); errno != 0 {
		master.Close()
		t.Fatalf("TIOCSPTLCK: %v", errno)
	}
	var n int32
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, master.Fd(), syscall.TIOCGPTN, uintptr(unsafe.Pointer(&n))); errno != 0 {
		master.Close()
		t.Fatalf("TIOCGPTN: %v", errno)
	}
	slave, err = os.OpenFile(fmt.Sprintf("/dev/pts/%d", n), os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		master.Close()
		t.Fatalf("open pty slave: %v", err)
	}
	return master, slave
}
