package tooling

import (
	"os"
	"testing"

	"github.com/baalimago/clai/e2e/e2etest"
	"github.com/baalimago/clai/internal/tools/mcp/testserverbin"
)

// harness holds this binary's CLI collaborators; TestMain refuses the paid ones.
var harness = e2etest.NewHarness()

func TestMain(m *testing.M) {
	harness.Deps.NewSummarizer = e2etest.RefusingSummarizer
	e2etest.DetachTTY()
	testServerBinPath, testServerBinErr = testserverbin.Build()
	code := m.Run()
	if testServerBinPath != "" {
		_ = os.Remove(testServerBinPath)
	}
	os.Exit(code)
}
