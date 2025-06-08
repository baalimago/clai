package vendorstest

import (
	"testing"

	"github.com/baalimago/clai/internal/models"
)

// RunSetupTests runs common Setup tests for vendors.
func RunSetupTests(t *testing.T, envVar string, requiresEnv bool, newVendor func() models.StreamCompleter) {
	t.Helper()

	t.Run("with_env", func(t *testing.T) {
		v := newVendor()
		t.Setenv(envVar, "some-key")
		if err := v.Setup(); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	if requiresEnv {
		t.Run("no_env", func(t *testing.T) {
			v := newVendor()
			t.Setenv(envVar, "")
			if err := v.Setup(); err == nil {
				t.Fatalf("expected error when %s unset", envVar)
			}
		})
	}
}
