package mcp

import (
	"os"
	"testing"

	"github.com/baalimago/clai/internal/tools/mcp/testserverbin"
)

// Set once, before m.Run starts the -timeout clock; testServerBinary reads them.
var (
	testServerBinPath string
	testServerBinErr  error
)

func TestMain(m *testing.M) {
	testServerBinPath, testServerBinErr = testserverbin.Build()
	code := m.Run()
	if testServerBinPath != "" {
		_ = os.Remove(testServerBinPath)
	}
	os.Exit(code)
}
