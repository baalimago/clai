//go:build linux || darwin || freebsd || netbsd || openbsd || dragonfly

package term

import (
	"errors"
	"io/fs"
	"os"
	"syscall"
	"time"
	"unsafe"

	"github.com/baalimago/clai/internal/diag"
	"github.com/baalimago/go_away_boilerplate/pkg/dimensions"
)

// resizeSettle lets a dragged pane border come to rest before the redraw.
const resizeSettle = 200 * time.Millisecond

// openResizeSource opens the terminal at path for reads that the size
// changes size reports can end.
func openResizeSource(path string, size *dimensions.Viewer) (ResizeSource, error) {
	tty, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOCTTY, 0)
	// ENXIO is /dev/tty without a controlling terminal.
	if errors.Is(err, syscall.ENXIO) || errors.Is(err, fs.ErrNotExist) {
		return ResizeSource{}, diag.Errorf("%w: open %s: %w", ErrResizeUnsupported, path, err)
	}
	if err != nil {
		return ResizeSource{}, diag.Errorf("open %s: %w", path, err)
	}
	// Fd would switch the file back to blocking reads, which no deadline ends.
	conn, err := tty.SyscallConn()
	if err == nil {
		err = tty.SetReadDeadline(time.Time{})
	}
	if err != nil {
		return ResizeSource{}, errors.Join(
			diag.Errorf("%w: %s takes no read deadline: %w", ErrResizeUnsupported, path, err),
			tty.Close(),
		)
	}
	return ResizeSource{
		TTY:     tty,
		Resized: size.Events(),
		Stop:    size.Stop,
		Flush:   func() error { return flushInput(conn) },
		Measure: viewedSize(size),
		Settle:  resizeSettle,
	}, nil
}

// flushInput sets the terminal's own settings back with the variant that
// first discards the input not yet read.
func flushInput(conn syscall.RawConn) error {
	var errno syscall.Errno
	ctrlErr := conn.Control(func(fd uintptr) {
		var t syscall.Termios
		if _, _, errno = syscall.Syscall(syscall.SYS_IOCTL, fd, ioctlGetTermios, uintptr(unsafe.Pointer(&t))); errno != 0 {
			return
		}
		_, _, errno = syscall.Syscall(syscall.SYS_IOCTL, fd, ioctlSetTermiosFlush, uintptr(unsafe.Pointer(&t)))
	})
	if ctrlErr != nil {
		return diag.Errorf("reach the terminal: %w", ctrlErr)
	}
	if errno != 0 {
		return diag.Errorf("discard the typed input: %w", errno)
	}
	return nil
}
