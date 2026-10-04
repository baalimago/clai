package text

import (
	"fmt"
	"os"
	"os/exec"
	"testing"
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
	f, err := os.CreateTemp("", "clai-mcp-testserver-*")
	if err != nil {
		testServerBinErr = fmt.Errorf("create temp file: %w", err)
		os.Exit(m.Run())
	}
	f.Close()
	cmd := exec.Command("go", "build", "-o", f.Name(), "../tools/mcp/testserver")
	if out, buildErr := cmd.CombinedOutput(); buildErr != nil {
		testServerBinErr = fmt.Errorf("go build testserver: %w: %s", buildErr, out)
		os.Exit(m.Run())
	}
	testServerBinPath = f.Name()

	code := m.Run()
	os.Remove(testServerBinPath)
	os.Exit(code)
}
