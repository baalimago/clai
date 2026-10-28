package text

import (
	"os"
	"testing"

	"github.com/baalimago/clai/internal/testenv"
	"github.com/baalimago/clai/internal/tools/mcp/testserverbin"
)

// The stdio test-server fixture is built once, before m.Run starts the race
// gate's -timeout clock; testServerBinary (querier_setup_tools_test.go) reads it.
var (
	testServerBinPath string
	testServerBinErr  error
)

func TestMain(m *testing.M) {
	testenv.MainWith(m, testenv.Setup{
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
