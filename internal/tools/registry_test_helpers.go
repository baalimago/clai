package tools

import (
	"sync"
	"testing"
)

var testRegistryMu sync.Mutex

// WithTestRegistry replaces the global Registry for the duration of the test callback.
func WithTestRegistry(t *testing.T, fn func()) {
	t.Helper()
	testRegistryMu.Lock()
	t.Cleanup(testRegistryMu.Unlock)

	orig := Registry
	Registry = NewRegistry()
	t.Cleanup(func() { Registry = orig })
	fn()
}
