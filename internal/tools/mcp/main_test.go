package mcp

import (
	"os"
	"testing"

	"github.com/baalimago/clai/internal/testenv"
	"github.com/baalimago/clai/internal/tools/mcp/testserverbin"
)

// Set once, before m.Run starts the -timeout clock; testServerBinary reads them.
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
