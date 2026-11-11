package text

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/baalimago/clai/internal/tools/mcp/httptestserver"
	"github.com/baalimago/go_away_boilerplate/pkg/testboil"
)

// installStatusSink records the connect labels the setup pipeline announces, so
// a test can see the status row a terminal would draw.
type installStatusSink struct {
	mu      sync.Mutex
	labels  []string
	succeed bool
}

func (s *installStatusSink) AppendServerLog(string, string) {}
func (s *installStatusSink) ServerExited(string)            {}
func (s *installStatusSink) setupSucceeded()                { s.succeed = true }

func (s *installStatusSink) ConnectStarted(_, label string) func() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.labels = append(s.labels, label)
	return func() {}
}

// fakePackageRunner installs a shell script named bin on PATH. The script runs
// the real test server, so the classification sees an unmodelled runner while
// the process behaves like any other MCP server.
func fakePackageRunner(t *testing.T, bin, target string) {
	t.Helper()
	dir := t.TempDir()
	script := fmt.Sprintf("#!/bin/sh\nexec %q\n", target)
	if err := os.WriteFile(filepath.Join(dir, bin), []byte(script), 0o755); err != nil {
		t.Fatalf("write fake %q: %v", bin, err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// TestCacheMissStagesAndConnectBoundNamesTheWait pins R1-14 and the reported
// failure end to end on every cache-miss path at once: a lazy and an eager
// first-run install, a hanging stdio handshake and a hanging HTTP handshake,
// each bounded at 1s. A first-run install announces "installing <package>"
// while it holds and reports the install stage when that wait runs out; a
// handshake reports the connect stage. All four resolve concurrently, so one
// setup proves every path is bounded inside one 1s window instead of the 30s
// handshake default.
func TestCacheMissStagesAndConnectBoundNamesTheWait(t *testing.T) {
	fakePackageRunner(t, "uvx", testServerBinary(t))
	hs := httptestserver.New()
	defer hs.Close()
	hs.Configure(func(c *httptestserver.Config) { c.HangForever = true })

	ctx, cancel := httpSetupTestContext()
	defer cancel()

	mcpDir := t.TempDir()
	for name, conf := range map[string]string{
		"lazy-install":  `{"command":"uvx","args":["duckduckgo-mcp-server==0.7.0"],"env":{"TEST_SERVER_AUTH_HANG":"1"},"startup":"lazy","connect_timeout_seconds":1}`,
		"eager-install": `{"command":"uvx","args":["other-mcp-server==2.1.0"],"env":{"TEST_SERVER_AUTH_HANG":"1"},"startup":"eager","connect_timeout_seconds":1}`,
		"hangs-stdio":   fmt.Sprintf(`{"command":%q,"env":{"TEST_SERVER_AUTH_HANG":"1"},"startup":"lazy","connect_timeout_seconds":1}`, testServerBinary(t)),
		"hangs-http":    fmt.Sprintf(`{"url":%q,"connect_timeout_seconds":1}`, hs.URL),
	} {
		if err := os.WriteFile(filepath.Join(mcpDir, name+".json"), []byte(conf), 0o644); err != nil {
			t.Fatalf("write server config %q: %v", name, err)
		}
	}
	sink := &installStatusSink{}

	start := time.Now()
	stdout := testboil.CaptureStderr(t, func(t *testing.T) {
		if _, err := setupMcpManager(ctx, mcpDir, Configurations{}, sink, testSchemaCache(t), nil); err != nil {
			t.Fatalf("an ambient connect failure must degrade, not fail setup: %v", err)
		}
	})
	elapsed := time.Since(start)

	// Generous margin for host load, far short of the 30s default.
	if elapsed > 15*time.Second {
		t.Fatalf("elapsed %v, want the 1s connect_timeout_seconds to fire well before the 30s handshake-bound default", elapsed)
	}
	for _, want := range []string{
		"'lazy-install' failed to start at stage 'install'",
		"'eager-install' failed to start at stage 'install'",
		"'hangs-stdio' failed to start at stage 'connect'",
		"'hangs-http' failed to start at stage 'connect'",
		"first-run install", "within 1s", "connect_timeout_seconds",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout = %q, want it to mention %q", stdout, want)
		}
	}
	sink.mu.Lock()
	labels := append([]string(nil), sink.labels...)
	sink.mu.Unlock()
	for _, want := range []string{"installing duckduckgo-mcp-server", "installing other-mcp-server"} {
		if got := slices.Contains(labels, want); !got {
			t.Errorf("announced labels = %v, want %v among them", labels, want)
		}
	}
}
