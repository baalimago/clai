package e2etest

import (
	"io"
	"os"
	"sync"
	"testing"

	"github.com/baalimago/go_away_boilerplate/pkg/ancli"
)

var captureStdoutStderrMu sync.Mutex

func CaptureStdoutStderr(t *testing.T, fn func()) (string, string) {
	t.Helper()

	// Serialize capture across all tests that use this helper.
	captureStdoutStderrMu.Lock()
	defer captureStdoutStderrMu.Unlock()

	// Hold ancli mutexes while swapping os.Stdout/os.Stderr to avoid
	// racing with background goroutines that write via ancli.PrintErr etc.
	ancli.OutMu.Lock()
	ancli.ErrMut.Lock()
	oldOut, oldErr := os.Stdout, os.Stderr
	outR, outW, err := os.Pipe()
	if err != nil {
		ancli.ErrMut.Unlock()
		ancli.OutMu.Unlock()
		t.Fatalf("Pipe(stdout): %v", err)
	}
	errR, errW, err := os.Pipe()
	if err != nil {
		ancli.ErrMut.Unlock()
		ancli.OutMu.Unlock()
		t.Fatalf("Pipe(stderr): %v", err)
	}
	os.Stdout = outW
	os.Stderr = errW
	ancli.ErrMut.Unlock()
	ancli.OutMu.Unlock()

	doneOut := make(chan string, 1)
	doneErr := make(chan string, 1)
	go func() {
		b, _ := io.ReadAll(outR)
		doneOut <- string(b)
	}()
	go func() {
		b, _ := io.ReadAll(errR)
		doneErr <- string(b)
	}()
	fn()
	_ = outW.Close()
	_ = errW.Close()

	// Restore under the ancli mutexes.
	ancli.OutMu.Lock()
	ancli.ErrMut.Lock()
	os.Stdout = oldOut
	os.Stderr = oldErr
	ancli.ErrMut.Unlock()
	ancli.OutMu.Unlock()

	return <-doneOut, <-doneErr
}
