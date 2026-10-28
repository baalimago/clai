package testenv

import (
	"os"
	"sync"
	"testing"
)

// Quiet points os.Stdout and os.Stderr at the null device until restore runs,
// or the test ends. The streams are shared, so only a sequential test may call
// it; the testing package's own output never goes through them.
func Quiet(t *testing.T) (restore func()) {
	t.Helper()
	null, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		t.Fatalf("open %s: %v", os.DevNull, err)
	}
	stdout, stderr := os.Stdout, os.Stderr
	os.Stdout, os.Stderr = null, null
	var once sync.Once
	restore = func() {
		once.Do(func() {
			os.Stdout, os.Stderr = stdout, stderr
			_ = null.Close()
		})
	}
	t.Cleanup(restore)
	return restore
}
