package term

import (
	"context"
	"errors"
	"net"
	"os"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/baalimago/clai/internal/testenv"
	"github.com/baalimago/go_away_boilerplate/pkg/dimensions"
)

func openPty(t *testing.T) (*os.File, string) {
	t.Helper()
	master, slave := testenv.OpenPTY(t)
	t.Cleanup(func() {
		master.Close()
		slave.Close()
	})
	return master, slave.Name()
}

// sizeViewer reports width and counts its reads on measured.
func sizeViewer(width *atomic.Int64, measured chan<- struct{}) (*dimensions.Viewer, chan<- os.Signal) {
	signals := make(chan os.Signal, 1)
	viewer := dimensions.New(context.Background(), 0,
		dimensions.WithReader(func() (dimensions.Dimensions, error) {
			select {
			case measured <- struct{}{}:
			default:
			}
			return dimensions.Dimensions{Width: int(width.Load()), Height: 30}, nil
		}),
		dimensions.WithSignalSource(signals))
	return viewer, signals
}

// stopped reports whether the viewer's events ended.
func stopped(viewer *dimensions.Viewer) bool {
	select {
	case _, ok := <-viewer.Events():
		return !ok
	default:
		return false
	}
}

// typeInto writes keys to the terminal and waits for their echo, so the line
// discipline holds them before the test goes on.
func typeInto(t *testing.T, master *os.File, keys string) {
	t.Helper()
	if _, err := master.WriteString(keys); err != nil {
		t.Fatal(err)
	}
	var echo strings.Builder
	buf := make([]byte, 64)
	for !strings.Contains(echo.String(), strings.TrimSuffix(keys, "\n")) {
		n, err := master.Read(buf)
		if err != nil {
			t.Fatalf("read the echo: %v", err)
		}
		echo.Write(buf[:n])
	}
}

func TestOpenResizeReader_widthChangeDiscardsTheTypedLine(t *testing.T) {
	master, path := openPty(t)
	var width atomic.Int64
	width.Store(100)
	measured := make(chan struct{}, 64)
	viewer, signals := sizeViewer(&width, measured)
	reader, err := openResizeReader(path, viewer)
	if err != nil {
		t.Fatalf("openResizeReader: %v", err)
	}
	t.Cleanup(func() { reader.Close() })

	pending := readAsync(reader)
	// The viewer's first read, the reader's, and the pending read's check;
	// then the read blocks on the terminal.
	for range 3 {
		<-measured
	}
	typeInto(t, master, "12")
	width.Store(70)
	resizedAt := time.Now()
	signals <- syscall.SIGWINCH
	if got := <-pending; !errors.Is(got.err, ErrResized) {
		t.Fatalf("read ended with %q, %v; want ErrResized", got.line, got.err)
	}
	if elapsed := time.Since(resizedAt); elapsed < resizeSettle {
		t.Fatalf("the read ended %v after the signal, before the %v settle", elapsed, resizeSettle)
	}
	if d := reader.Observe(); d.Width != 70 {
		t.Fatalf("Observe() = %+v, want width 70", d)
	}
	typeInto(t, master, "3\n")
	line, err := readLine(reader)
	if err != nil || line != "3\n" {
		t.Fatalf("read %q, %v; want \"3\\n\": the line typed before the redraw must be gone", line, err)
	}
	if err := reader.Close(); err != nil {
		t.Fatalf("Close() = %v", err)
	}
	if !stopped(viewer) {
		t.Fatal("Close left the size viewer running")
	}
}

func TestOpenResizeReader_openFailures(t *testing.T) {
	dir := t.TempDir()
	regular := dir + "/regular"
	if err := os.WriteFile(regular, []byte("x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	socket := dir + "/socket"
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	for _, tc := range []struct {
		name        string
		path        string
		unsupported bool
	}{
		{"missing terminal", dir + "/no-tty", true},
		// open(2) answers ENXIO for a socket, as for /dev/tty without a
		// controlling terminal.
		{"no controlling terminal", socket, true},
		{"file without deadlines", regular, true},
		{"other open failure", regular + "/tty", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var width atomic.Int64
			viewer, _ := sizeViewer(&width, make(chan struct{}))
			_, err := openResizeReader(tc.path, viewer)
			if err == nil {
				t.Fatal("openResizeReader() succeeded")
			}
			if got := errors.Is(err, ErrResizeUnsupported); got != tc.unsupported {
				t.Fatalf("openResizeReader() = %v; unsupported %v, want %v", err, got, tc.unsupported)
			}
			if !stopped(viewer) {
				t.Fatal("a failed open left the size viewer running")
			}
		})
	}
}

func TestFlushInput_refusedOnANonTerminal(t *testing.T) {
	rd, wr, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer rd.Close()
	defer wr.Close()
	conn, err := rd.SyscallConn()
	if err != nil {
		t.Fatal(err)
	}
	if err := flushInput(conn); !errors.Is(err, syscall.ENOTTY) {
		t.Fatalf("flushInput(pipe) = %v, want ENOTTY", err)
	}
}
