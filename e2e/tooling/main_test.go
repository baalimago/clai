package tooling

import (
	"os"
	"testing"

	"github.com/baalimago/clai/e2e/e2etest"
	"github.com/baalimago/clai/internal/testenv"
	"github.com/baalimago/clai/internal/tools/mcp/testserverbin"
)

// harness holds this binary's CLI collaborators; TestMain refuses the paid ones.
var harness = e2etest.NewHarness()

func TestMain(m *testing.M) {
	harness.Deps.NewSummarizer = e2etest.RefusingSummarizer
	harness.Deps.NewImageGenerator = e2etest.RefusingImageGenerator
	e2etest.Main(m, testenv.Setup{
		Before: func(testenv.Home) error {
			testServerBinPath, testServerBinErr = testserverbin.Build()
			return nil
		},
		After: func() {
			if testServerBinPath != "" {
				_ = os.Remove(testServerBinPath)
			}
		},
	})
}
