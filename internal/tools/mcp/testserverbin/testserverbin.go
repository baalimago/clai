// Package testserverbin builds the stdio MCP test server once per test binary,
// in TestMain, so the go build never runs on the -timeout clock.
package testserverbin

import (
	"fmt"
	"os"
	"os/exec"
)

const testServerPkg = "github.com/baalimago/clai/internal/tools/mcp/testserver"

// Build compiles the test server into a fresh temp file and returns its path.
// The caller owns the file. On failure nothing is left behind.
func Build() (string, error) { return build(os.TempDir(), testServerPkg) }

func build(dir, pkg string) (string, error) {
	f, err := os.CreateTemp(dir, "clai-mcp-testserver-*")
	if err != nil {
		return "", fmt.Errorf("create temp file: %w", err)
	}
	path := f.Name()
	if err := f.Close(); err != nil {
		_ = os.Remove(path)
		return "", fmt.Errorf("close temp file %q: %w", path, err)
	}
	if out, err := exec.Command("go", "build", "-o", path, pkg).CombinedOutput(); err != nil {
		_ = os.Remove(path)
		return "", fmt.Errorf("go build %s: %w: %s", pkg, err, out)
	}
	return path, nil
}
