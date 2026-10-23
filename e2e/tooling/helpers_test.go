package tooling

import "testing"

// Set once, before m.Run starts the -timeout clock; testServerBinary reads them.
var (
	testServerBinPath string
	testServerBinErr  error
)

// testServerBinary returns the fixture TestMain built: a connect bound must never
// enclose a compile (D41, invariant 15; R2-19/R2-01, phase 8).
func testServerBinary(t *testing.T) string {
	t.Helper()
	if testServerBinErr != nil {
		t.Fatalf("build testserver binary: %v", testServerBinErr)
	}
	return testServerBinPath
}
