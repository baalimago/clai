package text

import (
	"bytes"
	"fmt"
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

// Test_setupPhase_LogsRenderLiveInStartupWindow pins the pre-session
// contract: the startup window shows each server's trailing log lines live,
// so a setup failure (or a setup blocked on an auth flow) never hides the
// reason.
func Test_setupPhase_LogsRenderLiveInStartupWindow(t *testing.T) {
	var errOut bytes.Buffer
	sink := newMcpLogSink(mcpLogRolling)
	sink.errOut = &errOut
	sink.termWidth = func() int { return 80 }
	sink.termHeight = func() int { return 40 }
	for i := range 11 {
		sink.AppendServerLog("fs", fmt.Sprintf("n%d", i))
	}
	sink.AppendServerLog("fs", "fatal: boom")
	sink.AppendServerLog("fs", "tail one")
	sink.AppendServerLog("fs", "tail two")
	sink.ServerExited("fs")

	frame := errOut.String()
	if i := strings.LastIndex(frame, "\x1b[J"); i >= 0 {
		frame = frame[i+len("\x1b[J"):]
	}
	for _, want := range []string{"▸ mcp.fs log", "✗ fatal: boom", "tail one", "tail two"} {
		if !strings.Contains(frame, want) {
			t.Errorf("startup window missing %q; frame: %q", want, frame)
		}
	}
	for _, absent := range []string{"n0\n", "n1\n", "n2\n", "n3\n"} {
		if strings.Contains(frame, absent) {
			t.Errorf("line past the window tail bound shown: %q in %q", absent, frame)
		}
	}
	if entries := sink.Drain(); entries != nil {
		t.Errorf("startup window lines also queued: %+v", entries)
	}
}

// recordingSuccessSink observes the setup-success notification without any
// rendering machinery.
type recordingSuccessSink struct{ succeeded bool }

func (r *recordingSuccessSink) AppendServerLog(string, string) {}
func (r *recordingSuccessSink) ServerExited(string)            {}
func (r *recordingSuccessSink) setupSucceeded()                { r.succeeded = true }

// Test_setupMcpManager_RegistersToolsAndNotifiesSuccess drives the full
// startup path against the real testserver subprocess: tools register under
// the mcp_<server>_<tool> prefix and the sink learns that setup succeeded.
func Test_setupMcpManager_RegistersToolsAndNotifiesSuccess(t *testing.T) {
	dir := t.TempDir()
	conf := []byte(`{"command":"go","args":["run","../tools/mcp/testserver"]}`)
	if err := os.WriteFile(filepath.Join(dir, "echo.json"), conf, 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	sink := &recordingSuccessSink{}

	got, err := setupMcpManager(t.Context(), dir, Configurations{}, sink)
	if err != nil {
		t.Fatalf("setupMcpManager: %v", err)
	}
	for _, want := range []string{"mcp_echo_echo", "mcp_echo_hang"} {
		if _, ok := got[want]; !ok {
			t.Errorf("registered tools missing %q; got: %v", want, got)
		}
	}
	if !sink.succeeded {
		t.Error("sink never notified of setup success")
	}
}

func Test_setupMcpManager_SuccessClearsStartupWindows(t *testing.T) {
	dir := t.TempDir()
	conf := []byte(`{"command":"go","args":["run","../tools/mcp/testserver"],"env":{"TEST_SERVER_STDERR":"1"}}`)
	if err := os.WriteFile(filepath.Join(dir, "echo.json"), conf, 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	var errOut bytes.Buffer
	sink := newMcpLogSink(mcpLogRolling)
	sink.errOut = &errOut
	sink.termWidth = func() int { return 120 }
	sink.termHeight = func() int { return 40 }

	if _, err := setupMcpManager(t.Context(), dir, Configurations{}, sink); err != nil {
		t.Fatalf("setupMcpManager: %v", err)
	}
	if !sink.startup.cleared {
		t.Error("startup windows not cleared after successful setup")
	}
}

func Test_setupMcpManager_MissingDirErrors(t *testing.T) {
	_, err := setupMcpManager(t.Context(), "/nonexistent/mcp/servers/dir", Configurations{}, &recordingSuccessSink{})
	if err == nil || !strings.Contains(err.Error(), "MCP servers directory not found") {
		t.Fatalf("err = %v, want missing-directory error", err)
	}
}

func Test_setupMcpManager_EmptyDirIsANoop(t *testing.T) {
	sink := &recordingSuccessSink{}
	got, err := setupMcpManager(t.Context(), t.TempDir(), Configurations{}, sink)
	if err != nil {
		t.Fatalf("setupMcpManager: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("empty dir registered tools: %v", got)
	}
	if sink.succeeded {
		t.Error("sink notified of success although no manager ran")
	}
}

func Test_setupMcpManager_SpawnFailureSkipsServer(t *testing.T) {
	dir := t.TempDir()
	conf := []byte(`{"command":"/nonexistent-binary-xyz","args":[]}`)
	if err := os.WriteFile(filepath.Join(dir, "broken.json"), conf, 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	sink := &recordingSuccessSink{}

	got, err := setupMcpManager(t.Context(), dir, Configurations{}, sink)
	if err != nil {
		t.Fatalf("setupMcpManager: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("broken server registered tools: %v", got)
	}
	if !sink.succeeded {
		t.Error("setup with only skipped servers must still report success")
	}
}

// Test_setupMcpManager_HandshakeFailureKeepsOtherTools pins the core fix: one
// MCP server that starts but fails its initialize/tools-list handshake must be
// skipped without dropping the tools of every other server.
func Test_setupMcpManager_HandshakeFailureKeepsOtherTools(t *testing.T) {
	dir := t.TempDir()
	good := []byte(`{"command":"go","args":["run","../tools/mcp/testserver"]}`)
	if err := os.WriteFile(filepath.Join(dir, "echo.json"), good, 0o644); err != nil {
		t.Fatalf("write good config: %v", err)
	}
	broken := []byte(`{"command":"go","args":["run","../tools/mcp/testserver"],"env":{"TEST_SERVER_EXIT":"1"}}`)
	if err := os.WriteFile(filepath.Join(dir, "broken.json"), broken, 0o644); err != nil {
		t.Fatalf("write broken config: %v", err)
	}
	sink := &recordingSuccessSink{}

	got, err := setupMcpManager(t.Context(), dir, Configurations{}, sink)
	if err != nil {
		t.Fatalf("setupMcpManager: %v", err)
	}
	if _, ok := got["mcp_echo_echo"]; !ok {
		t.Errorf("good server's tools not registered; got: %v", got)
	}
	if _, ok := got["mcp_broken_echo"]; ok {
		t.Error("broken server's tools must not be registered")
	}
}

func Test_matchingTools(t *testing.T) {
	available := map[string]pub_models.LLMTool{
		"mcp_notion_search": setupToolsTestTool{name: "mcp_notion_search"},
		"mcp_notion_fetch":  setupToolsTestTool{name: "mcp_notion_fetch"},
		"cat":               setupToolsTestTool{name: "cat"},
	}
	if got := matchingTools(available, "mcp_notion_*"); len(got) != 2 {
		t.Errorf("wildcard matched %d tools, want 2", len(got))
	}
	if got := matchingTools(available, "cat"); len(got) != 1 {
		t.Errorf("exact match found %d tools, want 1", len(got))
	}
	if got := matchingTools(available, "mcp_linear_*"); got != nil {
		t.Errorf("non-matching pattern returned %v, want none", got)
	}
}

// recordingToolBox counts tool registrations.
type recordingToolBox struct{ registered []string }

func (r *recordingToolBox) RegisterTool(tool pub_models.LLMTool) {
	r.registered = append(r.registered, tool.Specification().Name)
}

func Test_registerTool_DeduplicatesByName(t *testing.T) {
	box := &recordingToolBox{}
	conf := &Configurations{}
	tool := setupToolsTestTool{name: "cat"}

	registerTool(box, conf, tool)
	registerTool(box, conf, tool)
	registerTool(box, conf, setupToolsTestTool{name: "ls"})

	if !slices.Equal(box.registered, []string{"cat", "ls"}) {
		t.Errorf("registered = %v, want [cat ls]", box.registered)
	}
	if _, ok := conf.RegisteredTools["cat"]; !ok {
		t.Error("registration not tracked in RegisteredTools")
	}
}

func Test_findConfiguredMcpServers_EnvFileHomeResolution(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	confDir := t.TempDir()

	tests := []struct {
		name    string
		envfile string
		want    string
	}{
		{name: "tilde resolves to home", envfile: "~/.envfile", want: filepath.Join(home, ".envfile")},
		{name: "HOME var resolves to home", envfile: "$HOME/.envfile", want: filepath.Join(home, ".envfile")},
		{name: "relative joins config dir", envfile: ".envfile", want: filepath.Join(confDir, ".envfile")},
		{name: "absolute untouched", envfile: "/etc/clai/.envfile", want: "/etc/clai/.envfile"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(confDir, "srv.json")
			cfg := fmt.Sprintf(`{"command":"echo","envfile":%q}`, tt.envfile)
			if err := os.WriteFile(path, []byte(cfg), 0o644); err != nil {
				t.Fatalf("write config: %v", err)
			}
			servers, err := findConfiguredMcpServers([]string{path})
			if err != nil {
				t.Fatalf("findConfiguredMcpServers: %v", err)
			}
			if len(servers) != 1 {
				t.Fatalf("expected 1 server, got %d", len(servers))
			}
			if servers[0].EnvFile != tt.want {
				t.Fatalf("EnvFile = %q, want %q", servers[0].EnvFile, tt.want)
			}
		})
	}
}
