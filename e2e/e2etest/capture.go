package e2etest

import (
	"io"
	"os"
	"sync"
	"testing"
)

var captureStdoutStderrMu sync.Mutex

func CaptureStdoutStderr(t *testing.T, fn func()) (string, string) {
	t.Helper()

	// Serialize capture across all tests that use this helper.
	captureStdoutStderrMu.Lock()
	defer captureStdoutStderrMu.Unlock()

	oldOut, oldErr := os.Stdout, os.Stderr
	outR, outW, err := os.Pipe()
	if err != nil {
		t.Fatalf("Pipe(stdout): %v", err)
	}
	errR, errW, err := os.Pipe()
	if err != nil {
		_ = outR.Close()
		_ = outW.Close()
		t.Fatalf("Pipe(stderr): %v", err)
	}
	os.Stdout = outW
	os.Stderr = errW
	defer func() {
		os.Stdout = oldOut
		os.Stderr = oldErr
	}()

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

	return <-doneOut, <-doneErr
}
