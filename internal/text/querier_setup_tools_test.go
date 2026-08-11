package text

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	pub_models "github.com/baalimago/clai/pkg/text/models"
	"github.com/baalimago/go_away_boilerplate/pkg/ancli"
)

type setupToolsTestTool struct{ name string }

func (t setupToolsTestTool) Call(pub_models.Input) (string, error) { return "", nil }

func (t setupToolsTestTool) Specification() pub_models.Specification {
	return pub_models.Specification{Name: t.name}
}

func Test_filterMcpServersByProfile(t *testing.T) {
	tests := []struct {
		name     string
		files    []string
		userConf Configurations
		want     []string
	}{
		{
			name:  "No specific tools configured, return all files",
			files: []string{"server1.json", "server2.json"},
			userConf: Configurations{
				RequestedToolGlobs: []string{},
			},
			want: []string{"server1.json", "server2.json"},
		},
		{
			name:  "Specific tool matches one server",
			files: []string{"server1.json", "server2.json"},
			userConf: Configurations{
				RequestedToolGlobs: []string{"mcp_server1"},
			},
			want: []string{"server1.json"},
		},
		{
			name:  "Wildcard matches all mcp",
			files: []string{"server1.json", "server2.json"},
			userConf: Configurations{
				RequestedToolGlobs: []string{"mcp_*"},
			},
			want: []string{"server1.json", "server2.json"},
		},
		{
			name:  "Wildcard match on some servers",
			files: []string{"server1.json", "server2.json"},
			userConf: Configurations{
				RequestedToolGlobs: []string{"mcp_server1*"},
			},
			want: []string{"server1.json"},
		},
		{
			name:  "Match on server tool",
			files: []string{"server1.json", "server2.json"},
			userConf: Configurations{
				RequestedToolGlobs: []string{"mcp_server1_tool0"},
			},
			want: []string{"server1.json"},
		},
		{
			name:  "No match for any servers",
			files: []string{"server1.json", "server2.json"},
			userConf: Configurations{
				RequestedToolGlobs: []string{"mcp_server3"},
			},
			want: []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ancli.Noticef("== test: %v\n", tt.name)
			got := filterMcpServersByProfile(tt.files, tt.userConf)
			if !slices.Equal(got, tt.want) {
				t.Errorf("want %v, got: %v", tt.want, got)
			}
		})
	}
}

func Test_uniqueToolsDeduplicatesAliasesBySpecificationName(t *testing.T) {
	canonical := setupToolsTestTool{name: "async_cmd"}
	alias := setupToolsTestTool{name: "async_cmd"}
	other := setupToolsTestTool{name: "cat"}

	got := uniqueTools([]pub_models.LLMTool{canonical, alias, other, canonical})
	if len(got) != 2 {
		t.Fatalf("expected two unique tool specifications, got %d", len(got))
	}
	if got[0].Specification().Name != "async_cmd" || got[1].Specification().Name != "cat" {
		t.Fatalf("unexpected tools after deduplication: %q, %q", got[0].Specification().Name, got[1].Specification().Name)
	}
}

// captureStderr redirects os.Stderr for the duration of fn and returns
// everything fn wrote to it. The package runs its tests sequentially, so the
// global swap cannot race another test.
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("stderr pipe: %v", err)
	}
	os.Stderr = w
	defer func() { os.Stderr = old }()
	fn()
	w.Close()
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read stderr: %v", err)
	}
	return string(out)
}

func Test_findConfiguredMcpServers_ParsesTimeoutSeconds(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "playwright.json")
	if err := os.WriteFile(path, []byte(`{
		"command": "npm",
		"args": ["exec", "@playwright/mcp"],
		"timeout_seconds": 300
	}`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	servers, err := findConfiguredMcpServers([]string{path})
	if err != nil {
		t.Fatalf("findConfiguredMcpServers: %v", err)
	}
	if len(servers) != 1 {
		t.Fatalf("expected 1 server, got %d", len(servers))
	}
	if servers[0].TimeoutSeconds != 300 {
		t.Errorf("TimeoutSeconds = %d, want 300", servers[0].TimeoutSeconds)
	}
	if servers[0].Name != "playwright" {
		t.Errorf("Name = %q, want playwright (derived from filename)", servers[0].Name)
	}
}

func Test_flushMcpSetupErrors_PrintsOnlyErrorsAndExitTails(t *testing.T) {
	sink := newMcpLogSink(mcpLogRolling)
	// Eleven normal lines: the first four are evicted from the retained tail
	// (cap 10), so they stay queued as plain entries and must be skipped.
	for i := range 11 {
		sink.AppendServerLog("fs", fmt.Sprintf("n%d", i))
	}
	sink.AppendServerLog("fs", "fatal: boom")
	sink.AppendServerLog("fs", "tail one")
	sink.AppendServerLog("fs", "tail two")
	sink.ServerExited("fs")

	got := captureStderr(t, func() { flushMcpSetupErrors(sink) })

	for _, want := range []string{"mcp_fs: fatal: boom", "mcp_fs: n4\n", "mcp_fs: tail one", "mcp_fs: tail two"} {
		if !strings.Contains(got, want) {
			t.Errorf("flushMcpSetupErrors output missing %q; got: %q", want, got)
		}
	}
	for _, absent := range []string{"mcp_fs: n0\n", "mcp_fs: n1\n", "mcp_fs: n2\n", "mcp_fs: n3\n"} {
		if strings.Contains(got, absent) {
			t.Errorf("plain queued line was flushed as an error: %q in %q", absent, got)
		}
	}
	if rest := sink.Drain(); rest != nil {
		t.Errorf("flushMcpSetupErrors left buffered entries behind: %v", rest)
	}
}

func Test_flushMcpSetupErrors_NonDrainingSinkIsANoop(t *testing.T) {
	// A nil sink fails the drainer type assertion: the flush must stay silent
	// instead of panicking.
	flushMcpSetupErrors(nil)
}
