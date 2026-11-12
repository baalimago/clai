package e2etest

import (
	"os"
	"testing"

	"github.com/baalimago/clai/internal/testenv"
	"github.com/baalimago/clai/internal/tools/mcp/testserverbin"
)

// Set once, before m.Run starts the -timeout clock; TestServerBinary reads them.
var (
	testServerBinPath string
	testServerBinErr  error
)

// TestServerSetup builds the stdio MCP test server before m.Run starts the
// -timeout clock and removes it when the binary ends, so a connect bound never
// encloses a compile (D41, invariant 15; R2-19/R2-01, phase 8).
func TestServerSetup() testenv.Setup {
	return testenv.Setup{
		Before: func(testenv.Home) error {
			testServerBinPath, testServerBinErr = testserverbin.Build()
			return nil
		},
		After: func() {
			if testServerBinPath != "" {
				_ = os.Remove(testServerBinPath)
			}
		},
	}
}

// TestServerBinary returns the fixture TestServerSetup built.
func TestServerBinary(t *testing.T) string {
	t.Helper()
	if testServerBinErr != nil {
		t.Fatalf("build testserver binary: %v", testServerBinErr)
	}
	return testServerBinPath
}
