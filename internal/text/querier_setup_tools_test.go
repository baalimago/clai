package text

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/baalimago/clai/internal/tools/mcp"
	"github.com/baalimago/clai/internal/tools/mcp/schemacache"
	"github.com/baalimago/clai/pkg/claierr"
	pub_models "github.com/baalimago/clai/pkg/text/models"
	"github.com/baalimago/go_away_boilerplate/pkg/ancli"
	"github.com/baalimago/go_away_boilerplate/pkg/testboil"
)

// testSchemaCache builds a schema cache rooted at a fresh temporary
// directory, so no test touches the developer's real cache directory
// (README parameters table, Phase 3 row).
func testSchemaCache(t *testing.T) *schemacache.Cache {
	t.Helper()
	c, err := schemacache.New(t.TempDir())
	if err != nil {
		t.Fatalf("schemacache.New: %v", err)
	}
	return c
}

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

// Parsing timeout_seconds and deriving a server's name from its config
// file's base name are pinned directly against the relocated parser:
// serverconfig's own TestFindConfiguredServers_ParsesTimeoutSeconds (phase
// 7, relocated from this file to avoid a verbatim duplicate per the dupl
// gate). findConfiguredMcpServers itself stays exercised indirectly by
// every setupMcpManager test in this file.

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
	// Pinned eager (readiness checklist item nine): this file's spawning
	// tests stay on the pre-existing eager path, uncoupled from the schema
	// cache's own miss-connects behaviour; the lazy path gets its coverage
	// from TestLazyStartupE2EUnderRace instead.
	conf := fmt.Appendf(nil, `{"command":%q,"startup":"eager"}`, testServerBinary(t))
	if err := os.WriteFile(filepath.Join(dir, "echo.json"), conf, 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	sink := &recordingSuccessSink{}

	got, err := setupMcpManager(t.Context(), dir, Configurations{}, sink, testSchemaCache(t), nil)
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
	conf := fmt.Appendf(nil, `{"command":%q,"env":{"TEST_SERVER_STDERR":"1"},"startup":"eager"}`, testServerBinary(t))
	if err := os.WriteFile(filepath.Join(dir, "echo.json"), conf, 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	var errOut bytes.Buffer
	sink := newMcpLogSink(mcpLogRolling)
	sink.errOut = &errOut
	sink.termWidth = func() int { return 120 }
	sink.termHeight = func() int { return 40 }

	if _, err := setupMcpManager(t.Context(), dir, Configurations{}, sink, testSchemaCache(t), nil); err != nil {
		t.Fatalf("setupMcpManager: %v", err)
	}
	if !sink.startup.cleared {
		t.Error("startup windows not cleared after successful setup")
	}
}

func Test_setupMcpManager_MissingDirErrors(t *testing.T) {
	_, err := setupMcpManager(t.Context(), "/nonexistent/mcp/servers/dir", Configurations{}, &recordingSuccessSink{}, testSchemaCache(t), nil)
	if err == nil || !strings.Contains(err.Error(), "MCP servers directory not found") {
		t.Fatalf("err = %v, want missing-directory error", err)
	}
}

func Test_setupMcpManager_EmptyDirIsANoop(t *testing.T) {
	sink := &recordingSuccessSink{}
	got, err := setupMcpManager(t.Context(), t.TempDir(), Configurations{}, sink, testSchemaCache(t), nil)
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

// writeTwoServerConfigDir writes one good, eager, spawnable testserver
// config and one config carrying a per-file validation error (an invalid
// startup value), so a scan of the directory returns one server alongside
// a non-nil joined error from findConfiguredMcpServers (R2-06's precondition).
func writeTwoServerConfigDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	good := fmt.Appendf(nil, `{"command":%q,"startup":"eager"}`, testServerBinary(t))
	if err := os.WriteFile(filepath.Join(dir, "good.json"), good, 0o644); err != nil {
		t.Fatalf("write good config: %v", err)
	}
	bad := []byte(`{"command":"node","args":["s.js"],"startup":"LAZY"}`)
	if err := os.WriteFile(filepath.Join(dir, "bad.json"), bad, 0o644); err != nil {
		t.Fatalf("write bad config: %v", err)
	}
	return dir
}

// Test_setupMcpManager_PerFileConfigErrorWarnsAndStillRegistersOthers pins
// D44/R2-06: a per-file parse or validation error must never be dropped
// just because another file in the same directory parsed. Before the fix,
// findConfiguredMcpServers's joined error was read only when zero servers
// parsed at all, so "bad.json"'s invalid startup value vanished silently
// the moment "good.json" parsed. This drives the real composition root
// (setupMcpManager against two real files on disk), not a hand-built
// struct, so it would have caught the swallow the original tests could not
// (invariant 10).
func Test_setupMcpManager_PerFileConfigErrorWarnsAndStillRegistersOthers(t *testing.T) {
	dir := writeTwoServerConfigDir(t)
	sink := &recordingSuccessSink{}

	var got map[string]pub_models.LLMTool
	var err error
	stdout := testboil.CaptureStdout(t, func(t *testing.T) {
		got, err = setupMcpManager(t.Context(), dir, Configurations{}, sink, testSchemaCache(t), nil)
	})
	if err != nil {
		t.Fatalf("an ambient per-file config error must degrade, not fail setup: %v", err)
	}
	if _, ok := got["mcp_good_echo"]; !ok {
		t.Errorf("the server from the valid file was not registered; got: %v", got)
	}
	if !strings.Contains(stdout, "bad.json") {
		t.Errorf("stdout = %q, want a warning naming the invalid file", stdout)
	}
}

// Test_setupMcpManager_PerFileConfigErrorFailsStrictRun pins the other half
// of D44: when the run is strict (AgentSettings.StrictMcpStartup), the same
// per-file error is also returned to the caller, typed so it carries the
// strict/degrade fork's sentinel (invariant 14), rather than only ever being
// a warning nobody automated can see.
func Test_setupMcpManager_PerFileConfigErrorFailsStrictRun(t *testing.T) {
	dir := writeTwoServerConfigDir(t)
	conf := Configurations{AgentSettings: &AgentSettings{StrictMcpStartup: true}}
	sink := &recordingSuccessSink{}

	var got map[string]pub_models.LLMTool
	var err error
	testboil.CaptureStdout(t, func(t *testing.T) {
		got, err = setupMcpManager(t.Context(), dir, conf, sink, testSchemaCache(t), nil)
	})
	if !errors.Is(err, claierr.ErrMcpServerStartup) {
		t.Fatalf("err = %v, want errors.Is(err, claierr.ErrMcpServerStartup)", err)
	}
	if _, ok := got["mcp_good_echo"]; !ok {
		t.Errorf("the valid server's tools were dropped too; got: %v", got)
	}
}

func Test_setupMcpManager_SpawnFailureSkipsServer(t *testing.T) {
	dir := t.TempDir()
	// Pinned eager (readiness checklist item nine, R2-13): this test's own
	// name and assertions are about the Manager spawn-failure path, not the
	// cache-aware lazy path, which would also reach this same outcome but
	// for a different reason.
	conf := []byte(`{"command":"/nonexistent-binary-xyz","args":[],"startup":"eager"}`)
	if err := os.WriteFile(filepath.Join(dir, "broken.json"), conf, 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	sink := &recordingSuccessSink{}

	got, err := setupMcpManager(t.Context(), dir, Configurations{}, sink, testSchemaCache(t), nil)
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
	bin := testServerBinary(t)
	good := fmt.Appendf(nil, `{"command":%q,"startup":"eager"}`, bin)
	if err := os.WriteFile(filepath.Join(dir, "echo.json"), good, 0o644); err != nil {
		t.Fatalf("write good config: %v", err)
	}
	broken := fmt.Appendf(nil, `{"command":%q,"env":{"TEST_SERVER_EXIT":"1"},"startup":"eager"}`, bin)
	if err := os.WriteFile(filepath.Join(dir, "broken.json"), broken, 0o644); err != nil {
		t.Fatalf("write broken config: %v", err)
	}
	sink := &recordingSuccessSink{}

	got, err := setupMcpManager(t.Context(), dir, Configurations{}, sink, testSchemaCache(t), nil)
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

// Envfile path normalisation (tilde, $HOME, relative-to-config-dir,
// absolute-untouched) is pinned directly against the relocated parser:
// serverconfig's own TestFindConfiguredServers_EnvFileHomeResolution (phase
// 7, relocated from this file to avoid a verbatim duplicate per the dupl
// gate).

// startupErrorNames collects the server name of every
// *claierr.McpServerStartupError in err's chain. errors.Join exposes no
// iteration API, so the walk descends the stdlib unwrap contract directly.
func startupErrorNames(err error) []string {
	var names []string
	var visit func(error)
	visit = func(e error) {
		if e == nil {
			return
		}
		if startup, ok := e.(*claierr.McpServerStartupError); ok {
			names = append(names, startup.ServerName)
			return
		}
		if multi, ok := e.(interface{ Unwrap() []error }); ok {
			for _, sub := range multi.Unwrap() {
				visit(sub)
			}
			return
		}
		visit(errors.Unwrap(e))
	}
	visit(err)
	return names
}

// Test_setupMcpManager_ExplicitSpawnFailuresJoinedTyped pins the D13 split:
// with AgentSettings.StrictMcpStartup an explicitly requested server whose
// process cannot spawn fails setup with a typed, joined error naming every
// failed server — never a warn-and-degrade (worklog
// 2026-09-05-error-propagation, D13).
func Test_setupMcpManager_ExplicitSpawnFailuresJoinedTyped(t *testing.T) {
	conf := Configurations{
		AgentSettings: &AgentSettings{StrictMcpStartup: true},
		McpServers: []pub_models.McpServer{
			{Name: "explicit-a", Command: "/nonexistent-clai-test-binary-a"},
			{Name: "explicit-b", Command: "/nonexistent-clai-test-binary-b"},
		},
	}

	got, err := setupMcpManager(t.Context(), t.TempDir(), conf, &recordingSuccessSink{}, testSchemaCache(t), nil)
	if err == nil {
		t.Fatal("expected a typed error for the failed explicit servers, got nil")
	}
	if !errors.Is(err, claierr.ErrMcpServerStartup) {
		t.Errorf("err = %v, want errors.Is(err, claierr.ErrMcpServerStartup)", err)
	}
	if names := startupErrorNames(err); !slices.Equal(names, []string{"explicit-a", "explicit-b"}) {
		t.Errorf("startup errors name %v, want [explicit-a explicit-b]", names)
	}
	if len(got) != 0 {
		t.Errorf("failed servers registered tools: %v", got)
	}
}

// Test_setupMcpManager_ExplicitHandshakeFailureTyped pins the Manager report
// channel: in strict mode an explicit server that spawns but fails its
// initialize handshake surfaces as a typed error naming the server, not a
// warn-and-skip (worklog 2026-09-05-error-propagation, D13).
func Test_setupMcpManager_ExplicitHandshakeFailureTyped(t *testing.T) {
	conf := Configurations{
		AgentSettings: &AgentSettings{StrictMcpStartup: true},
		McpServers: []pub_models.McpServer{{
			Name:    "explicit-handshake",
			Command: testServerBinary(t),
			Env:     map[string]string{"TEST_SERVER_EXIT": "1"},
			Startup: pub_models.StartupEager,
		}},
	}

	_, err := setupMcpManager(t.Context(), t.TempDir(), conf, &recordingSuccessSink{}, testSchemaCache(t), nil)
	if err == nil {
		t.Fatal("expected a typed error for the failed explicit handshake, got nil")
	}
	if !errors.Is(err, claierr.ErrMcpServerStartup) {
		t.Fatalf("err = %v, want errors.Is(err, claierr.ErrMcpServerStartup)", err)
	}
	var startup *claierr.McpServerStartupError
	if !errors.As(err, &startup) {
		t.Fatalf("err = %v, want errors.As to yield *claierr.McpServerStartupError", err)
	}
	if startup.ServerName != "explicit-handshake" {
		t.Errorf("server name = %q, want explicit-handshake", startup.ServerName)
	}
	if startup.Stage == "" {
		t.Error("startup error must carry the failing handshake stage")
	}
}

// Test_setupMcpManager_StrictModeKeepsAmbientDegrade pins D13's asymmetry:
// StrictMcpStartup governs only the servers named in userConf.McpServers. A
// broken config-dir server still warn-degrades in a strict run, so a stale
// json beside an agent never bricks its Setup (worklog
// 2026-09-05-error-propagation, D13).
func Test_setupMcpManager_StrictModeKeepsAmbientDegrade(t *testing.T) {
	dir := t.TempDir()
	// Pinned eager (readiness checklist item nine, R2-13): this ambient
	// server's posture is incidental to what the test is about (the
	// explicit/ambient split), so it is named explicitly rather than left
	// to whatever the cache-aware default resolves to.
	if err := os.WriteFile(filepath.Join(dir, "broken.json"), []byte(`{"command":"/nonexistent-clai-test-binary","args":[],"startup":"eager"}`), 0o644); err != nil {
		t.Fatalf("write broken config: %v", err)
	}
	conf := Configurations{
		AgentSettings: &AgentSettings{StrictMcpStartup: true},
	}

	got, err := setupMcpManager(t.Context(), dir, conf, &recordingSuccessSink{}, testSchemaCache(t), nil)
	if err != nil {
		t.Fatalf("a broken ambient server must not fail a strict setup: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("broken ambient server registered tools: %v", got)
	}
}

// toolBoxCompleter is a StreamCompleter that records tool registrations, so
// setupTooling can be driven without a vendor.
type toolBoxCompleter struct {
	mockCompleter
	registered []string
}

func (c *toolBoxCompleter) RegisterTool(tool pub_models.LLMTool) {
	c.registered = append(c.registered, tool.Specification().Name)
}

// writeAmbientMarkerServer writes an mcpServers config whose process creates
// marker when it spawns, so an ambient start is observable on disk.
func writeAmbientMarkerServer(t *testing.T, mcpDir string) string {
	t.Helper()
	if err := os.MkdirAll(mcpDir, 0o755); err != nil {
		t.Fatalf("mkdir mcpServers: %v", err)
	}
	marker := filepath.Join(t.TempDir(), "ambient-spawned")
	conf := fmt.Sprintf(`{"command":"sh","args":["-c","touch %s"],"startup":"eager"}`, marker)
	if err := os.WriteFile(filepath.Join(mcpDir, "ambient.json"), []byte(conf), 0o644); err != nil {
		t.Fatalf("write ambient config: %v", err)
	}
	return marker
}

func assertMarkerAbsent(t *testing.T, marker string) {
	t.Helper()
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("expected no spawn, marker stat: %v", err)
	}
}

// TestSetupMcpManager_skipAmbient_startsNoConfigDirServer pins D18: with the
// flag set, no server from <configDir>/mcpServers is spawned and no warning
// is emitted; without it the same directory spawns the server (control).
func TestSetupMcpManager_skipAmbient_startsNoConfigDirServer(t *testing.T) {
	mcpDir := filepath.Join(t.TempDir(), "mcpServers")
	marker := writeAmbientMarkerServer(t, mcpDir)
	sink := &recordingSuccessSink{}

	var got map[string]pub_models.LLMTool
	var err error
	stderr := testboil.CaptureStderr(t, func(t *testing.T) {
		got, err = setupMcpManager(t.Context(), mcpDir, Configurations{SkipAmbientMcpServers: true}, sink, testSchemaCache(t), nil)
	})
	if err != nil {
		t.Fatalf("setupMcpManager: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("ambient server registered tools: %v", got)
	}
	assertMarkerAbsent(t, marker)
	if sink.succeeded {
		t.Error("manager ran although no server was requested")
	}
	if strings.Contains(stderr, "failed to setup") {
		t.Errorf("unexpected setup warning on stderr: %q", stderr)
	}

	t.Run("control: flag off spawns the ambient server", func(t *testing.T) {
		testboil.CaptureStderr(t, func(t *testing.T) {
			if _, err := setupMcpManager(t.Context(), mcpDir, Configurations{}, &recordingSuccessSink{}, testSchemaCache(t), nil); err != nil {
				t.Fatalf("setupMcpManager: %v", err)
			}
		})
		if _, err := os.Stat(marker); err != nil {
			t.Fatalf("ambient server must spawn without the flag: %v", err)
		}
	})
}

// TestSetupMcpManager_skipAmbient_keepsExplicitServers pins the second half
// of D18: userConf.McpServers still start under the flag and keep their
// posture, so a strict explicit spawn failure is still the typed error.
func TestSetupMcpManager_skipAmbient_keepsExplicitServers(t *testing.T) {
	mcpDir := filepath.Join(t.TempDir(), "mcpServers")
	marker := writeAmbientMarkerServer(t, mcpDir)

	t.Run("explicit server starts", func(t *testing.T) {
		conf := Configurations{
			SkipAmbientMcpServers: true,
			McpServers:            []pub_models.McpServer{{Name: "echo", Command: testServerBinary(t), Startup: pub_models.StartupEager}},
		}
		sink := &recordingSuccessSink{}
		got, err := setupMcpManager(t.Context(), mcpDir, conf, sink, testSchemaCache(t), nil)
		if err != nil {
			t.Fatalf("setupMcpManager: %v", err)
		}
		if _, ok := got["mcp_echo_echo"]; !ok {
			t.Errorf("explicit server's tools missing; got: %v", got)
		}
		if !sink.succeeded {
			t.Error("sink never notified of setup success")
		}
		assertMarkerAbsent(t, marker)
	})

	t.Run("strict explicit spawn failure stays typed", func(t *testing.T) {
		// Startup left unset: readiness checklist item nine (R2-13) now
		// requires every spawning test to name its posture explicitly.
		// This one deliberately does not, since it is the assertion that
		// D35's strict-explicit exception resolves eager even when nothing
		// configures it.
		conf := Configurations{
			SkipAmbientMcpServers: true,
			McpServers:            []pub_models.McpServer{{Name: "broken", Command: "/nonexistent-binary-xyz"}},
			AgentSettings:         &AgentSettings{StrictMcpStartup: true},
		}
		_, err := setupMcpManager(t.Context(), mcpDir, conf, &recordingSuccessSink{}, testSchemaCache(t), nil)
		if !errors.Is(err, claierr.ErrMcpServerStartup) {
			t.Fatalf("err = %v, want ErrMcpServerStartup", err)
		}
		if names := startupErrorNames(err); !slices.Equal(names, []string{"broken"}) {
			t.Errorf("failed servers = %v, want [broken]", names)
		}
		assertMarkerAbsent(t, marker)
	})
}

// TestSetupMcpManager_skipAmbient_toleratesMissingDir pins that a one-off
// querier must not depend on the mcpServers directory existing.
func TestSetupMcpManager_skipAmbient_toleratesMissingDir(t *testing.T) {
	sink := &recordingSuccessSink{}
	got, err := setupMcpManager(t.Context(), "/nonexistent/mcp/servers/dir", Configurations{SkipAmbientMcpServers: true}, sink, testSchemaCache(t), nil)
	if err != nil {
		t.Fatalf("setupMcpManager: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("missing dir registered tools: %v", got)
	}
	if sink.succeeded {
		t.Error("sink notified of success although no manager ran")
	}
}

// TestLazyConnectDegradesForAmbientServers pinned, under phase 2, that an
// explicitly lazy config-dir server was deferred past setup unconditionally.
// Phase 3 supersedes that: D18 makes a cache miss always connect during
// setup, cold or warm, so a lazy ambient server now registers its tools on
// a cold cache too; only a warm cache (TestSchemaCacheHitRegistersToolsWithoutTransport)
// gets the zero-process outcome. This test now pins that corrected
// cold-cache behaviour instead.
func TestLazyConnectDegradesForAmbientServers(t *testing.T) {
	mcpDir := t.TempDir()
	conf := fmt.Appendf(nil, `{"command":%q,"startup":"lazy"}`, testServerBinary(t))
	if err := os.WriteFile(filepath.Join(mcpDir, "lazy.json"), conf, 0o644); err != nil {
		t.Fatalf("write lazy config: %v", err)
	}
	sink := &recordingSuccessSink{}

	got, err := setupMcpManager(t.Context(), mcpDir, Configurations{}, sink, testSchemaCache(t), nil)
	if err != nil {
		t.Fatalf("setupMcpManager: %v", err)
	}
	if _, ok := got["mcp_lazy_echo"]; !ok {
		t.Errorf("cold-cache lazy ambient server did not register tools; got: %v", got)
	}
	if !sink.succeeded {
		t.Error("setup with a connected lazy server must still report success")
	}
}

// TestLazyConnectHonoursStrictMcpStartupForExplicitServers pins that an
// explicit server under strict startup still resolves during setup.
// R1-25 found the original single-case form pinned Startup: StartupEager in
// its own config literal, so it bypassed effectiveStartupMode's strict
// branch entirely and would have passed unchanged had that branch returned
// StartupLazy. The two subtests below instead drive the two ways a
// strict-explicit server can reach this path: startup mode left unset (the
// default), and startup mode configured lazy with a warm cache, which is
// exactly the shape D35/R1-02 found broken — without the fix, a cache hit
// never connects, so no strict failure exists to report.
func TestLazyConnectHonoursStrictMcpStartupForExplicitServers(t *testing.T) {
	t.Run("startup mode unset resolves eagerly", func(t *testing.T) {
		conf := Configurations{
			AgentSettings: &AgentSettings{StrictMcpStartup: true},
			McpServers: []pub_models.McpServer{
				{Name: "explicit-strict", Command: "/nonexistent-clai-test-binary-strict"},
			},
		}

		got, err := setupMcpManager(t.Context(), t.TempDir(), conf, &recordingSuccessSink{}, testSchemaCache(t), nil)
		if err == nil {
			t.Fatal("expected a typed error for the failed explicit server, got nil")
		}
		if !errors.Is(err, claierr.ErrMcpServerStartup) {
			t.Errorf("err = %v, want errors.Is(err, claierr.ErrMcpServerStartup)", err)
		}
		if len(got) != 0 {
			t.Errorf("failed explicit server registered tools: %v", got)
		}
	})

	t.Run("configured lazy under strict startup still resolves eagerly even with a warm cache", func(t *testing.T) {
		server := pub_models.McpServer{
			Name:    "explicit-strict-lazy",
			Command: "/nonexistent-clai-test-binary-strict-lazy",
			Startup: pub_models.StartupLazy,
		}
		cache := testSchemaCache(t)
		identity := schemacache.BuildIdentity(server)
		if err := cache.Capture(identity, mcp.ProtocolVersion, json.RawMessage(`{}`), json.RawMessage(`[{"name":"sometool"}]`)); err != nil {
			t.Fatalf("seed warm cache: %v", err)
		}

		conf := Configurations{
			AgentSettings: &AgentSettings{StrictMcpStartup: true},
			McpServers:    []pub_models.McpServer{server},
		}
		got, err := setupMcpManager(t.Context(), t.TempDir(), conf, &recordingSuccessSink{}, cache, nil)
		if err == nil {
			t.Fatal("D35: a configured lazy explicit server under strict startup must still surface a typed setup error even with a warm cache, got nil")
		}
		if !errors.Is(err, claierr.ErrMcpServerStartup) {
			t.Errorf("err = %v, want errors.Is(err, claierr.ErrMcpServerStartup)", err)
		}
		if len(got) != 0 {
			t.Errorf("failed explicit server registered tools: %v", got)
		}
	})
}

// TestExplicitServerBothCommandAndUrlFailsValidation pins D43/R2-07: a
// server passed through agent.WithMcpServers never ran
// serverconfig.ValidateTransport, so a struct with both command and url
// silently preferred url. Under strict startup the typed error must come
// back from Setup instead.
func TestExplicitServerBothCommandAndUrlFailsValidation(t *testing.T) {
	conf := Configurations{
		AgentSettings: &AgentSettings{StrictMcpStartup: true},
		McpServers: []pub_models.McpServer{
			// A reachable url matters: without validation this resolves as
			// an HTTP server and a real connect attempt follows, which also
			// fails and would make this test pass for the wrong reason
			// (R1-25's class). Asserting the "config" stage below is what
			// actually discriminates validation from a failed connect.
			{Name: "both-set", Command: "node", Url: "https://mcp.example.com/mcp"},
		},
	}
	got, err := setupMcpManager(t.Context(), t.TempDir(), conf, &recordingSuccessSink{}, testSchemaCache(t), nil)
	var startupErr *claierr.McpServerStartupError
	if !errors.As(err, &startupErr) {
		t.Fatalf("err = %v, want errors.As to yield *claierr.McpServerStartupError", err)
	}
	if startupErr.Stage != "config" {
		t.Errorf("Stage = %q, want %q (rejected by validation, not a failed connect attempt)", startupErr.Stage, "config")
	}
	if startupErr.ServerName != "both-set" {
		t.Errorf("ServerName = %q, want %q", startupErr.ServerName, "both-set")
	}
	if len(got) != 0 {
		t.Errorf("invalid explicit server registered tools: %v", got)
	}
}

// TestExplicitServerNeitherCommandNorUrlFailsValidation pins the other half
// of D43/R2-07: without validation, a server with neither command nor url
// reached exec.CommandContext(ctx, "") instead of failing cleanly. An
// ambient-posture run (no AgentSettings) degrades instead of failing, so
// the run continues with no tools from this server and no panic or hang.
func TestExplicitServerNeitherCommandNorUrlFailsValidation(t *testing.T) {
	conf := Configurations{
		McpServers: []pub_models.McpServer{{Name: "neither-set"}},
	}
	var got map[string]pub_models.LLMTool
	var err error
	// ancli.Warnf prints to stdout, not stderr, despite the name.
	stdout := testboil.CaptureStdout(t, func(t *testing.T) {
		got, err = setupMcpManager(t.Context(), t.TempDir(), conf, &recordingSuccessSink{}, testSchemaCache(t), nil)
	})
	if err != nil {
		t.Fatalf("an ambient-posture invalid server must degrade, not fail setup: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("invalid explicit server registered tools: %v", got)
	}
	// Without validation this still fails, but at exec.CommandContext(ctx,
	// "") instead: asserting the validation message, not just a nil error,
	// is what discriminates the two (R1-25's class). The message names the
	// neither-set branch specifically (R2-18), not a shared XOR string.
	if !strings.Contains(stdout, `sets neither "command" nor "url"`) {
		t.Errorf("stdout = %q, want the transport-validation message, not a bare exec failure", stdout)
	}
}

// TestSetupTooling_injectedToolsRegisterWithoutWarning pins that
// Configurations.Tools registers exactly the injected tools and, unlike an
// unknown name in RequestedToolGlobs, never warns on stderr.
func TestSetupTooling_injectedToolsRegisterWithoutWarning(t *testing.T) {
	confDir := t.TempDir()
	if err := os.Mkdir(filepath.Join(confDir, "mcpServers"), 0o755); err != nil {
		t.Fatalf("mkdir mcpServers: %v", err)
	}
	// setupTooling resolves the production schema cache through
	// utils.GetClaiCacheDir, unlike setupMcpManager's own tests which inject
	// testSchemaCache(t) directly; without this, a run through setupTooling
	// resolves the developer's real cache directory (R2-20).
	t.Setenv("CLAI_CACHE_DIR", filepath.Join(t.TempDir(), "cache"))
	completer := &toolBoxCompleter{}
	conf := &Configurations{
		UseTools:  true,
		ConfigDir: confDir,
		Tools:     []pub_models.LLMTool{setupToolsTestTool{name: "injected"}},
	}

	var err error
	stderr := testboil.CaptureStderr(t, func(t *testing.T) {
		err = setupTooling(t.Context(), completer, conf, &recordingSuccessSink{})
	})
	if err != nil {
		t.Fatalf("setupTooling: %v", err)
	}
	if !slices.Equal(completer.registered, []string{"injected"}) {
		t.Errorf("registered = %v, want [injected]", completer.registered)
	}
	if _, ok := conf.BaseTools["injected"]; !ok || len(conf.BaseTools) != 1 {
		t.Errorf("BaseTools = %v, want only the injected tool", conf.BaseTools)
	}
	if strings.TrimSpace(stderr) != "" {
		t.Errorf("injected tools must register silently, stderr: %q", stderr)
	}
}

// testServerBinary returns the shared stdio fixture TestMain already built
// before m.Run() started, so no test pays its ~4s "go build" against the
// race gate's -timeout budget (sign-off review, 2026-10-03: the review
// measured this package's cold-cache run at 25.3s of a 30s budget with the
// build behind a sync.Once inside a test; TestMain moves that cost outside
// the timed window entirely). A timing-sensitive test
// (TestLazySetupResolvesServersConcurrently) must never enclose a go run or
// go build, or its bound would measure the Go build cache rather than the
// code under test (D41, invariant 15); TestMain already building the
// binary once, up front, is what keeps that true.
func testServerBinary(t *testing.T) string {
	t.Helper()
	if testServerBinErr != nil {
		t.Fatalf("build testserver binary: %v", testServerBinErr)
	}
	return testServerBinPath
}

// spawnTimestamps parses the unix-nanosecond timestamps TEST_SERVER_SPAWN_LOG
// accumulates (one per real process birth), in file order.
func spawnTimestamps(t *testing.T, path string) []time.Time {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatalf("read spawn log: %v", err)
	}
	var out []time.Time
	for line := range strings.SplitSeq(strings.TrimSpace(string(data)), "\n") {
		if line == "" {
			continue
		}
		ns, err := strconv.ParseInt(line, 10, 64)
		if err != nil {
			t.Fatalf("parse spawn timestamp %q: %v", line, err)
		}
		out = append(out, time.Unix(0, ns))
	}
	return out
}

// TestLazySetupResolvesServersConcurrently pins D39/R2-02: the lazy
// branch's cache-miss connect used to run inline in setupMcpManager's
// per-server loop, so a cold run resolved servers one at a time where the
// eager path it replaced (mcp.Manager, one goroutine per ControlEvent)
// resolved them concurrently. Four servers with distinct identities (a
// different TEST_SERVER_SPAWN_LOG path each) and a shared pre-handshake
// delay: resolved one at a time, the spawn timestamps spread out by
// multiples of the delay; resolved concurrently, they cluster together
// regardless of it. Using a pre-built binary instead of "go run" keeps a
// cold Go build cache from affecting either case (D41).
func TestLazySetupResolvesServersConcurrently(t *testing.T) {
	bin := testServerBinary(t)
	const (
		n     = 4
		delay = 600 * time.Millisecond
		// Resolved one at a time, four servers delayed 600ms each spread
		// to at least 1.8s; resolved concurrently, launching four
		// processes takes nowhere near that regardless of host load.
		concurrentBound = 1500 * time.Millisecond
	)
	mcpDir := t.TempDir()
	spawnLogs := make([]string, n)
	for i := range n {
		spawnLogs[i] = filepath.Join(t.TempDir(), "spawns.log")
		conf := fmt.Sprintf(`{"command":%q,"env":{"TEST_SERVER_SPAWN_LOG":%q,"TEST_SERVER_PRE_HANDSHAKE_DELAY_MS":%q},"startup":"lazy"}`,
			bin, spawnLogs[i], strconv.Itoa(int(delay/time.Millisecond)))
		if err := os.WriteFile(filepath.Join(mcpDir, fmt.Sprintf("srv%d.json", i)), []byte(conf), 0o644); err != nil {
			t.Fatalf("write config %d: %v", i, err)
		}
	}

	got, err := setupMcpManager(t.Context(), mcpDir, Configurations{}, &recordingSuccessSink{}, testSchemaCache(t), nil)
	if err != nil {
		t.Fatalf("setupMcpManager: %v", err)
	}
	if len(got) != 2*n {
		t.Fatalf("registered %d tools, want %d (two per server); got: %v", len(got), 2*n, got)
	}

	var first, last time.Time
	for i, log := range spawnLogs {
		times := spawnTimestamps(t, log)
		if len(times) != 1 {
			t.Fatalf("server %d: spawn log has %d entries, want 1", i, len(times))
		}
		if first.IsZero() || times[0].Before(first) {
			first = times[0]
		}
		if times[0].After(last) {
			last = times[0]
		}
	}
	if spread := last.Sub(first); spread > concurrentBound {
		t.Errorf("spawn timestamps spread across %v, want under %v (D39: the lazy branch must resolve servers concurrently, not one at a time)", spread, concurrentBound)
	}
}

// TestConnectorConnectFailureSpawnsOnceAcrossRepeatedToolCallsEndToEnd pins
// the retry-count and batch rows through the production composition root
// (invariant 10, R1-25): the existing connector-level test for this
// contract counts dials against a hand-built fake closure, which the
// limits table's own trigger ("call the tool more than once and count
// spawns") says should instead be real process births, counted through the
// real tool-call dispatch (toolExecutor.invokeToolCall), not a call to the
// function that would have produced them.
func TestConnectorConnectFailureSpawnsOnceAcrossRepeatedToolCallsEndToEnd(t *testing.T) {
	spawnLog := filepath.Join(t.TempDir(), "spawns.log")
	srv := pub_models.McpServer{
		Name:    "broken",
		Command: testServerBinary(t),
		Env:     map[string]string{"TEST_SERVER_SPAWN_LOG": spawnLog, "TEST_SERVER_EXIT": "1"},
	}
	connector := mcp.NewConnector(t.Context(), srv, nil)
	spec := pub_models.Specification{Name: "mcp_broken_echo"}
	tool := mcp.NewTool(connector, "echo", spec, 0, "broken", nil, 0)

	q := &Querier[*MockQuerier]{Raw: true, out: &strings.Builder{}, tooling: tooling{run: map[string]pub_models.LLMTool{"mcp_broken_echo": tool}}}
	e := toolExecutor[*MockQuerier]{querier: q}

	for i := range 3 {
		out := e.invokeToolCall(t.Context(), pub_models.Call{Name: "mcp_broken_echo", Inputs: &pub_models.Input{"text": "hi"}})
		if !strings.Contains(out, "ERROR") {
			t.Fatalf("call %d: out = %q, want a typed error result", i, out)
		}
	}
	if spawns := countSpawnLines(t, spawnLog); spawns != 1 {
		t.Errorf("spawn log has %d lines across three tool calls, want 1 (no retry after a connect failure)", spawns)
	}
}

// TestAuthChallengeStdioSpawnBoundedAcrossRepeatedToolCallsEndToEnd pins the
// R1-01 fix through the real production path (invariant 10): a stdio
// server whose stderr trips the auth classifier but whose handshake never
// completes used to cost two real process births and a full auth-timeout
// block on every single tool call, because the challenge is deliberately
// exempt from the connector's memoisation (D20) and nothing capped the
// re-dials (D36). Driven through the real toolExecutor.invokeToolCall
// three times and counted via the real spawn-log fixture (not a hand-built
// dial closure): D36's cap bounds the whole run to exactly one dial plus
// one re-dial, so the third call spawns nothing and replays the memoised
// challenge.
func TestAuthChallengeStdioSpawnBoundedAcrossRepeatedToolCallsEndToEnd(t *testing.T) {
	bin := testServerBinary(t)
	spawnLog := filepath.Join(t.TempDir(), "spawns.log")
	srv := pub_models.McpServer{
		Name:    "hang",
		Command: bin,
		Env:     map[string]string{"TEST_SERVER_SPAWN_LOG": spawnLog, "TEST_SERVER_AUTH_HANG": "1"},
	}
	// A real mcpLogSink, not a hand-rolled AuthPendingSink fake: it is what
	// makes StdioConn's stderr-driven raiseAuthPending actually run
	// (conn_stdio.go's nil-sink guard) rather than silently declining to
	// reclassify anything.
	sink := newMcpLogSinkTo(mcpLogRolling, &strings.Builder{})
	connector := mcp.NewConnector(t.Context(), srv, sink, mcp.WithConnectBound(300*time.Millisecond))
	spec := pub_models.Specification{Name: "mcp_hang_echo"}
	tool := mcp.NewTool(connector, "echo", spec, 0, "hang", nil, 50*time.Millisecond)

	q := &Querier[*MockQuerier]{Raw: true, out: &strings.Builder{}, mcpSink: sink, tooling: tooling{run: map[string]pub_models.LLMTool{"mcp_hang_echo": tool}}}
	e := toolExecutor[*MockQuerier]{querier: q}

	for i := range 3 {
		out := e.invokeToolCall(t.Context(), pub_models.Call{Name: "mcp_hang_echo", Inputs: &pub_models.Input{"text": "hi"}})
		if !strings.Contains(out, "ERROR") {
			t.Fatalf("call %d: out = %q, want the actionable authorization error", i, out)
		}
	}
	if spawns := countSpawnLines(t, spawnLog); spawns != 2 {
		t.Errorf("spawn log has %d lines across three tool calls, want exactly 2 (D36: one dial plus one bounded re-dial per server per run, not one pair per call)", spawns)
	}
}

// TestDebugDumpNeverLeaksArgsOrEnvSecret pins R1-10/R1-03 through the real
// production carrier: `DEBUG=1` dumps mcpServers verbatim at setup
// (querier_setup_tools.go), and args is the documented `mcp-remote
// --header "Authorization: Bearer ..."` shape a credential is smuggled
// through (R1-03), while env values are credential carriers by the same
// invariant-6 amendment. Before the redaction fix, debug.IndentedJsonFmt
// serialised pub_models.McpServer verbatim, so a secret placed in either
// field reached stdout whole.
func TestDebugDumpNeverLeaksArgsOrEnvSecret(t *testing.T) {
	t.Setenv("DEBUG", "1")
	dir := t.TempDir()
	const argsSecret = "super-secret-bearer-token-in-args"
	const envSecret = "super-secret-api-key-in-env"
	conf := fmt.Sprintf(`{"command":"/nonexistent-clai-test-binary","args":["--header","Authorization: Bearer %s"],"env":{"API_KEY":%q}}`, argsSecret, envSecret)
	if err := os.WriteFile(filepath.Join(dir, "leaky.json"), []byte(conf), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	stdout := testboil.CaptureStdout(t, func(t *testing.T) {
		// The ambient server fails to spawn (no such binary), which is
		// irrelevant here: the DEBUG dump runs before any connect attempt,
		// unconditionally, so this still exercises the real carrier.
		_, _ = setupMcpManager(t.Context(), dir, Configurations{}, &recordingSuccessSink{}, testSchemaCache(t), nil)
	})
	if strings.Contains(stdout, argsSecret) {
		t.Errorf("DEBUG dump leaked the args secret: %s", stdout)
	}
	if strings.Contains(stdout, envSecret) {
		t.Errorf("DEBUG dump leaked the env secret: %s", stdout)
	}
	if !strings.Contains(stdout, "API_KEY") {
		t.Error("DEBUG dump dropped the env key name too; only the value should be redacted")
	}
	if !strings.Contains(stdout, "leaky") {
		t.Error("DEBUG dump dropped the server name, which is not secret")
	}
}
