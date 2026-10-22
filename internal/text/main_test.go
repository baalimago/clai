package text

import (
	"os"
	"testing"

	"github.com/baalimago/clai/internal/tools/mcp/testserverbin"
)

// testServerBinPath and testServerBinErr are set once, here, before m.Run()
// starts the race gate's -timeout clock (see TestMain). testServerBinary
// (querier_setup_tools_test.go) only ever reads them.
var (
	testServerBinPath string
	testServerBinErr  error
)

// TestMain builds the shared stdio test-server fixture once, before
// m.Run() starts the race gate's -timeout clock, so this package's ~4s "go
// build" is never charged against that budget (worklog
// 2026-10-02-mcp-connection-cost, sign-off review, 2026-10-03: the review
// measured this package's cold-cache run at 25.3s of a 30s budget with the
// build behind a sync.Once inside a test, and found the worklog's own
// "needs a package split" conclusion wrong — building here instead raises
// headroom from ~4.7s to ~8.7s at no structural cost).
func TestMain(m *testing.M) {
	// A developer's key would make every querier setup fetch the live OpenRouter catalog.
	_ = os.Unsetenv("OPENROUTER_API_KEY")
	testServerBinPath, testServerBinErr = testserverbin.Build()
	code := m.Run()
	if testServerBinPath != "" {
		_ = os.Remove(testServerBinPath)
	}
	os.Exit(code)
}
