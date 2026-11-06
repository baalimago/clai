package text

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

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

// TestLazyCacheMissInstallStageNamesTheDownload pins the reported failure end
// to end: a server whose command line clai holds no evidence for is waited on
// as an install, announces "installing <package>" while it holds, and reports
// the install stage when that wait runs out instead of a connect timeout.
func TestLazyCacheMissInstallStageNamesTheDownload(t *testing.T) {
	fakePackageRunner(t, "uvx", testServerBinary(t))

	ctx, cancel := httpSetupTestContext()
	defer cancel()

	mcpDir := t.TempDir()
	conf := `{"command":"uvx","args":["duckduckgo-mcp-server==0.7.0"],"env":{"TEST_SERVER_AUTH_HANG":"1"},"startup":"lazy","connect_timeout_seconds":1}`
	if err := os.WriteFile(filepath.Join(mcpDir, "ddg.json"), []byte(conf), 0o644); err != nil {
		t.Fatalf("write server config: %v", err)
	}
	sink := &installStatusSink{}

	start := time.Now()
	stdout := testboil.CaptureStdout(t, func(t *testing.T) {
		if _, err := setupMcpManager(ctx, mcpDir, Configurations{}, sink, testSchemaCache(t), nil); err != nil {
			t.Fatalf("an ambient connect failure must degrade, not fail setup: %v", err)
		}
	})
	elapsed := time.Since(start)

	if elapsed > 15*time.Second {
		t.Fatalf("elapsed %v, want the 1s connect_timeout_seconds to fire promptly", elapsed)
	}
	wantStage := "'ddg' failed to start at stage 'install'"
	if !strings.Contains(stdout, wantStage) {
		t.Errorf("stdout = %q, want %q", stdout, wantStage)
	}
	for _, want := range []string{"first-run install", "within 1s", "connect_timeout_seconds"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout = %q, want it to mention %q", stdout, want)
		}
	}
	sink.mu.Lock()
	labels := append([]string(nil), sink.labels...)
	sink.mu.Unlock()
	if len(labels) != 1 || labels[0] != "installing duckduckgo-mcp-server" {
		t.Errorf("announced labels = %v, want [installing duckduckgo-mcp-server]", labels)
	}
}

// TestEagerInstallStageAndStatusNamesTheDownload pins the same contract on the
// eager path: the bound an eager server is handshaked under is the connect
// bound resolver, so a first-run install there is bounded, announced and named
// exactly as a lazy one.
func TestEagerInstallStageAndStatusNamesTheDownload(t *testing.T) {
	fakePackageRunner(t, "uvx", testServerBinary(t))

	ctx, cancel := httpSetupTestContext()
	defer cancel()

	mcpDir := t.TempDir()
	conf := `{"command":"uvx","args":["duckduckgo-mcp-server==0.7.0"],"env":{"TEST_SERVER_AUTH_HANG":"1"},"startup":"eager","connect_timeout_seconds":1}`
	if err := os.WriteFile(filepath.Join(mcpDir, "ddg.json"), []byte(conf), 0o644); err != nil {
		t.Fatalf("write server config: %v", err)
	}
	sink := &installStatusSink{}

	stdout := testboil.CaptureStdout(t, func(t *testing.T) {
		if _, err := setupMcpManager(ctx, mcpDir, Configurations{}, sink, testSchemaCache(t), nil); err != nil {
			t.Fatalf("an ambient connect failure must degrade, not fail setup: %v", err)
		}
	})

	if want := "'ddg' failed to start at stage 'install'"; !strings.Contains(stdout, want) {
		t.Errorf("stdout = %q, want %q", stdout, want)
	}
	sink.mu.Lock()
	labels := append([]string(nil), sink.labels...)
	sink.mu.Unlock()
	if len(labels) != 1 || labels[0] != "installing duckduckgo-mcp-server" {
		t.Errorf("announced labels = %v, want [installing duckduckgo-mcp-server]", labels)
	}
}
