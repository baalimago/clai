package text

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/baalimago/clai/internal/tools/mcp"
	"github.com/baalimago/clai/internal/tools/mcp/launcher"
	"github.com/baalimago/clai/internal/tools/mcp/schemacache"
	"github.com/baalimago/clai/internal/tools/mcp/serverconfig"
	"github.com/baalimago/clai/pkg/claierr"
	pub_models "github.com/baalimago/clai/pkg/text/models"
	"github.com/baalimago/go_away_boilerplate/pkg/testboil"
)

// countSpawnLines reports how many lines TEST_SERVER_SPAWN_LOG has
// accumulated at path, or zero when the file was never created. Each line
// is one real testserver process birth.
func countSpawnLines(t *testing.T, path string) int {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return 0
		}
		t.Fatalf("read spawn log: %v", err)
	}
	return bytes.Count(data, []byte("\n"))
}

// writeLazyEchoConfig writes an ambient, explicitly lazy testserver config
// whose process appends to spawnLog on every birth, so a test can tell a
// cache hit from a miss by whether the log grew.
func writeLazyEchoConfig(t *testing.T, mcpDir, spawnLog string) {
	t.Helper()
	conf := fmt.Sprintf(`{"command":%q,"env":{"TEST_SERVER_SPAWN_LOG":%q},"startup":"lazy"}`, testServerBinary(t), spawnLog)
	if err := os.WriteFile(filepath.Join(mcpDir, "echo.json"), []byte(conf), 0o644); err != nil {
		t.Fatalf("write lazy echo config: %v", err)
	}
}

// TestSchemaCacheHitRegistersToolsWithoutTransport pins the phase's headline
// contract: once a cold run has warmed the cache, a second setup against the
// same server registers the same tools with no further process birth.
func TestSchemaCacheHitRegistersToolsWithoutTransport(t *testing.T) {
	mcpDir := t.TempDir()
	spawnLog := filepath.Join(t.TempDir(), "spawns.log")
	writeLazyEchoConfig(t, mcpDir, spawnLog)
	cache := testSchemaCache(t)

	if _, err := setupMcpManager(t.Context(), mcpDir, Configurations{}, &recordingSuccessSink{}, cache, nil); err != nil {
		t.Fatalf("cold run: %v", err)
	}
	warmedSpawns := countSpawnLines(t, spawnLog)
	if warmedSpawns == 0 {
		t.Fatal("cold run never connected, so the cache was never warmed")
	}

	got, err := setupMcpManager(t.Context(), mcpDir, Configurations{}, &recordingSuccessSink{}, cache, nil)
	if err != nil {
		t.Fatalf("warm-cache run: %v", err)
	}
	if _, ok := got["mcp_echo_echo"]; !ok {
		t.Errorf("warm-cache run did not register tools; got: %v", got)
	}
	if after := countSpawnLines(t, spawnLog); after != warmedSpawns {
		t.Errorf("warm-cache run spawned again: spawn log has %d lines, want %d (unchanged)", after, warmedSpawns)
	}
}

// TestCacheHitAndMissRegisterIdenticalToolSets pins the phase's real
// contract: a cache that avoids a transport but registers nothing has
// achieved nothing. A cold (miss) and a warm (hit) run must register the
// exact same tool names for the same server state.
func TestCacheHitAndMissRegisterIdenticalToolSets(t *testing.T) {
	mcpDir := t.TempDir()
	spawnLog := filepath.Join(t.TempDir(), "spawns.log")
	writeLazyEchoConfig(t, mcpDir, spawnLog)
	cache := testSchemaCache(t)

	missGot, err := setupMcpManager(t.Context(), mcpDir, Configurations{}, &recordingSuccessSink{}, cache, nil)
	if err != nil {
		t.Fatalf("miss run: %v", err)
	}
	hitGot, err := setupMcpManager(t.Context(), mcpDir, Configurations{}, &recordingSuccessSink{}, cache, nil)
	if err != nil {
		t.Fatalf("hit run: %v", err)
	}

	missNames := toolNames(missGot)
	hitNames := toolNames(hitGot)
	if !slices.Equal(missNames, hitNames) {
		t.Errorf("miss registered %v, hit registered %v, want identical sets", missNames, hitNames)
	}
	if len(missNames) == 0 {
		t.Fatal("neither run registered any tool")
	}
}

func toolNames(m map[string]pub_models.LLMTool) []string {
	names := make([]string, 0, len(m))
	for name := range m {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

// TestSchemaCacheMissConnectsCapturesThenHits pins D18 end to end: a miss
// connects and writes the entry, and the schema cache package's own Lookup
// then reports a hit for the identity that setup just resolved.
func TestSchemaCacheMissConnectsCapturesThenHits(t *testing.T) {
	mcpDir := t.TempDir()
	spawnLog := filepath.Join(t.TempDir(), "spawns.log")
	writeLazyEchoConfig(t, mcpDir, spawnLog)
	cache := testSchemaCache(t)

	server := pub_models.McpServer{
		Name:    "echo",
		Command: testServerBinary(t),
		Env:     map[string]string{"TEST_SERVER_SPAWN_LOG": spawnLog},
	}
	identity := schemacache.BuildIdentity(server)
	if _, ok := cache.Lookup(identity); ok {
		t.Fatal("cache already had an entry before the first run")
	}

	if _, err := setupMcpManager(t.Context(), mcpDir, Configurations{}, &recordingSuccessSink{}, cache, nil); err != nil {
		t.Fatalf("setupMcpManager: %v", err)
	}

	rec, ok := cache.Lookup(identity)
	if !ok {
		t.Fatal("no entry captured after a miss connected")
	}
	if !bytes.Contains(rec.Tools, []byte(`"echo"`)) {
		t.Errorf("captured tools do not mention the echo tool: %s", rec.Tools)
	}
}

// TestSchemaCacheNeverPersistsConnectFailure pins invariant 2: a connect
// failure is a fact about one run, never written to disk. A lazy server
// whose process cannot spawn must leave the cache empty.
func TestSchemaCacheNeverPersistsConnectFailure(t *testing.T) {
	mcpDir := t.TempDir()
	conf := []byte(`{"command":"/nonexistent-clai-test-binary-schema-cache","startup":"lazy"}`)
	if err := os.WriteFile(filepath.Join(mcpDir, "broken.json"), conf, 0o644); err != nil {
		t.Fatalf("write broken config: %v", err)
	}
	cache := testSchemaCache(t)
	server := pub_models.McpServer{Name: "broken", Command: "/nonexistent-clai-test-binary-schema-cache"}
	identity := schemacache.BuildIdentity(server)

	testboil.CaptureStdout(t, func(t *testing.T) {
		if _, err := setupMcpManager(t.Context(), mcpDir, Configurations{}, &recordingSuccessSink{}, cache, nil); err != nil {
			t.Fatalf("setupMcpManager: %v", err)
		}
	})

	if _, ok := cache.Lookup(identity); ok {
		t.Fatal("a connect failure was persisted to the schema cache")
	}
}

// TestSchemaCacheWriteFailureDoesNotFailSetup pins that a cache which cannot
// be written to degrades to a warning: setup still succeeds and the server
// still connects and registers, because the cache is an optimisation, never
// a dependency.
func TestSchemaCacheWriteFailureDoesNotFailSetup(t *testing.T) {
	mcpDir := t.TempDir()
	spawnLog := filepath.Join(t.TempDir(), "spawns.log")
	writeLazyEchoConfig(t, mcpDir, spawnLog)

	// A file where the cache expects a directory makes MkdirAll (and so
	// every Capture) fail deterministically, independent of file permission
	// bits or the user running the test.
	blockedDir := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(blockedDir, []byte("x"), 0o644); err != nil {
		t.Fatalf("write blocking file: %v", err)
	}
	cache, err := schemacache.New(filepath.Join(blockedDir, "mcpSchemas"))
	if err != nil {
		t.Fatalf("schemacache.New: %v", err)
	}

	var got map[string]pub_models.LLMTool
	var setupErr error
	// ancli.Warnf prints to stdout, not stderr.
	stdout := testboil.CaptureStdout(t, func(t *testing.T) {
		got, setupErr = setupMcpManager(t.Context(), mcpDir, Configurations{}, &recordingSuccessSink{}, cache, nil)
	})
	if setupErr != nil {
		t.Fatalf("a cache write failure must not fail setup: %v", setupErr)
	}
	if _, ok := got["mcp_echo_echo"]; !ok {
		t.Errorf("server did not register tools despite the cache write failure; got: %v", got)
	}
	testboil.AssertStringContains(t, stdout, "schema cache")
}

// TestStartupDefaultIsLazyAfterCache pins the parameters table's flipped
// default, with its strict-explicit exception intact (D14): an unset
// startup field resolves to lazy, except for a server reached through
// agent.WithMcpServers while StrictMcpStartup is on, which still defaults
// eager. D35 (R1-02) made that exception unconditional: a configured
// "lazy" must not win over it, or a warm cache lets setup skip connecting
// and a strict failure never exists to report.
func TestStartupDefaultIsLazyAfterCache(t *testing.T) {
	unset := pub_models.McpServer{}
	if got, err := effectiveStartupMode(unset, false); err != nil || got != pub_models.StartupLazy {
		t.Errorf("effectiveStartupMode(unset, false) = %v, %v, want lazy, nil", got, err)
	}
	if got, err := effectiveStartupMode(unset, true); err != nil || got != pub_models.StartupEager {
		t.Errorf("effectiveStartupMode(unset, true) = %v, %v, want eager (strict-explicit exception), nil", got, err)
	}

	explicitLazy := pub_models.McpServer{Startup: pub_models.StartupLazy}
	if got, err := effectiveStartupMode(explicitLazy, false); err != nil || got != pub_models.StartupLazy {
		t.Errorf("effectiveStartupMode(explicitLazy, false) = %v, %v, want lazy, nil", got, err)
	}
	if got, err := effectiveStartupMode(explicitLazy, true); err != nil || got != pub_models.StartupEager {
		t.Errorf("effectiveStartupMode(explicitLazy, true) = %v, %v, want eager: D35 makes the strict-explicit exception unconditional", got, err)
	}
}

// TestEffectiveStartupModeRejectsUnrecognisedProgrammaticValue pins R2-15:
// StartupMode.UnmarshalJSON rejects a typo at parse time, but a value set
// programmatically through the public pub_models.McpServer struct (as
// agent.WithMcpServers takes) never goes through that unmarshal. Without
// its own check, effectiveStartupMode fell through to the eager branch
// silently for any value it didn't recognise.
func TestEffectiveStartupModeRejectsUnrecognisedProgrammaticValue(t *testing.T) {
	bad := pub_models.McpServer{Name: "bad-mode", Startup: pub_models.StartupMode("LAZY")}
	if _, err := effectiveStartupMode(bad, false); !errors.Is(err, claierr.ErrMcpServerStartup) {
		t.Errorf("effectiveStartupMode(bad, false) err = %v, want ErrMcpServerStartup", err)
	}
	// The strict-explicit exception must not short-circuit this check: a
	// strict caller handing in garbage still gets a typed error, not a
	// silent eager.
	if _, err := effectiveStartupMode(bad, true); !errors.Is(err, claierr.ErrMcpServerStartup) {
		t.Errorf("effectiveStartupMode(bad, true) err = %v, want ErrMcpServerStartup", err)
	}
}

// TestSchemaCacheMissingEnvfileIsTypedError pins that a configured envfile
// which does not exist is never silently absorbed into a miss: identity
// building encodes it as the canonical absent marker, the lookup misses,
// setup connects, and the pre-existing typed envfile error surfaces exactly
// as it does today without the cache. An explicit "lazy" startup, under
// strict mode, is what makes the failure a returned error instead of a
// stderr warning, so the test can assert on it directly.
func TestSchemaCacheMissingEnvfileIsTypedError(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing.env")
	conf := Configurations{
		AgentSettings: &AgentSettings{StrictMcpStartup: true},
		McpServers: []pub_models.McpServer{{
			Name:    "envfile-missing",
			Command: testServerBinary(t),
			EnvFile: missing,
			Startup: pub_models.StartupLazy,
		}},
	}

	_, err := setupMcpManager(t.Context(), t.TempDir(), conf, &recordingSuccessSink{}, testSchemaCache(t), nil)
	if !errors.Is(err, claierr.ErrMcpServerStartup) {
		t.Fatalf("err = %v, want errors.Is(err, claierr.ErrMcpServerStartup)", err)
	}
	if !bytes.Contains([]byte(err.Error()), []byte(missing)) {
		t.Errorf("error %v does not name the missing envfile %q", err, missing)
	}
}

// TestLazyCacheMissConnectBoundAppliesToStdioHandshake pins R1-14: the
// schema cache's cache-miss path bound only the connection-level handshake
// bound (30s default), ignoring connect_timeout_seconds entirely, so a
// server that spawns but never answers initialize blocked cold setup for
// 30s regardless of what the server configured. A 1s connect_timeout_seconds
// against a fake that never answers must fire in well under the 30s
// default.
func TestLazyCacheMissConnectBoundAppliesToStdioHandshake(t *testing.T) {
	conf := Configurations{
		SkipAmbientMcpServers: true,
		McpServers: []pub_models.McpServer{{
			Name:                  "hangs",
			Command:               testServerBinary(t),
			Env:                   map[string]string{"TEST_SERVER_AUTH_HANG": "1"},
			Startup:               pub_models.StartupLazy,
			ConnectTimeoutSeconds: 1,
		}},
	}

	start := time.Now()
	stdout := testboil.CaptureStdout(t, func(t *testing.T) {
		if _, err := setupMcpManager(t.Context(), t.TempDir(), conf, &recordingSuccessSink{}, testSchemaCache(t), nil); err != nil {
			t.Fatalf("an ambient-posture connect failure must degrade, not fail setup: %v", err)
		}
	})
	elapsed := time.Since(start)

	// Generous margin above the 1s connect_timeout_seconds, far short of the
	// 30s handshake-bound default the unfixed code fell back to. The fixture
	// now spawns a prebuilt binary (D41, invariant 15), so no compile-time
	// variance is in play at all; the margin stays generous for host load.
	if elapsed > 15*time.Second {
		t.Fatalf("elapsed %v, want the 1s connect_timeout_seconds to fire well before the 30s handshake-bound default", elapsed)
	}
	if !strings.Contains(stdout, "stage 'connect'") {
		t.Errorf("stdout = %q, want a connect-stage failure", stdout)
	}
}

// TestUnresolvableCommandLineConnectsAheadOfTime pins the launcher
// resolution's whole point: for a command-based server whose command line
// clai cannot resolve, a warm schema-cache entry must NOT be served at setup.
// The cached tool list was observed from a different program than the one
// this command line names, and no launcher evidence exists to prove they
// match, so setup connects and observes for real. This is what removes the
// need for the per-run notice sign-off review B3 added: the unverified case
// never reaches a cache hit in the first place.
func TestUnresolvableCommandLineConnectsAheadOfTime(t *testing.T) {
	mcpDir := t.TempDir()
	launcherPath, logPath := writeFakeLauncher(t, "uvx")
	server := writeServerConfig(t, mcpDir, pub_models.McpServer{
		Name:    "opaque",
		Command: launcherPath,
		Args:    []string{"serve"},
	}, logPath)

	if !launcher.RequiresEagerConnect(server) {
		t.Fatal("a launcher clai does not resolve must require an eager connect")
	}
	cache := testSchemaCache(t)
	// Seeded under the server's own identity, so the only thing that can
	// stop it being served is the eager connect itself.
	if err := cache.Capture(schemacache.BuildIdentity(server), mcp.ProtocolVersion, json.RawMessage(`{}`), json.RawMessage(`[{"name":"stale"}]`)); err != nil {
		t.Fatalf("seed warm cache: %v", err)
	}

	got, err := setupMcpManager(t.Context(), mcpDir, Configurations{}, &recordingSuccessSink{}, cache, nil)
	if err != nil {
		t.Fatalf("setupMcpManager: %v", err)
	}
	if _, ok := got["mcp_opaque_stale"]; ok {
		t.Error("a stale cached tool was advertised for a command line that cannot be resolved")
	}
	if _, ok := got["mcp_opaque_echo"]; !ok {
		t.Errorf("unresolvable server did not register its live tools; got: %v", toolNames(got))
	}
	if countSpawnLines(t, logPath) == 0 {
		t.Error("an unresolvable command line served from cache without ever connecting")
	}
}

// TestResolvablePinnedCommandLineStaysLazyOnWarmCache is the control for
// TestUnresolvableCommandLineConnectsAheadOfTime: a command line naming one
// exact package version is resolvable from the config alone, so a warm cache
// hit keeps the zero-process outcome. Without this the eager fallback would
// swallow the whole point of the schema cache.
func TestResolvablePinnedCommandLineStaysLazyOnWarmCache(t *testing.T) {
	mcpDir := t.TempDir()
	logPath := filepath.Join(t.TempDir(), "spawns.log")
	server := writeServerConfig(t, mcpDir, pub_models.McpServer{
		Name:    "pinned",
		Command: fakeLauncherOnPath(t, "npx"),
		Args:    []string{"-y", "slivingdoc@1.0.2", "serve"},
	}, logPath)

	if launcher.RequiresEagerConnect(server) {
		t.Fatal("a pinned package version must not require an eager connect")
	}
	cache := testSchemaCache(t)
	identity := schemacache.BuildIdentity(server)
	if identity.Launcher == nil {
		t.Fatal("a pinned spec produced no launcher identity component")
	}
	if identity.Launcher.Kind != launcher.Pinned || identity.Launcher.Version != "1.0.2" {
		t.Fatalf("launcher component = %+v, want pinned 1.0.2", identity.Launcher)
	}
	if err := cache.Capture(identity, mcp.ProtocolVersion, json.RawMessage(`{}`), json.RawMessage(`[{"name":"echo"}]`)); err != nil {
		t.Fatalf("seed warm cache: %v", err)
	}

	got, err := setupMcpManager(t.Context(), mcpDir, Configurations{}, &recordingSuccessSink{}, cache, nil)
	if err != nil {
		t.Fatalf("setupMcpManager: %v", err)
	}
	if _, ok := got["mcp_pinned_echo"]; !ok {
		t.Errorf("warm cache hit did not register the cached tool; got: %v", toolNames(got))
	}
	if n := countSpawnLines(t, logPath); n != 0 {
		t.Errorf("a resolvable command line spawned %d times on a warm cache hit, want 0", n)
	}
}

// TestEagerServerPersistsObservedTools pins that an eagerly connected server
// writes its schema cache entry too. The cache-only tools listing never
// connects, so without this an unresolvable command server — now eager by
// default — could never appear in `clai tools`, and a listing meant to show
// what can be called would silently omit it.
func TestEagerServerPersistsObservedTools(t *testing.T) {
	mcpDir := t.TempDir()
	launcherPath, logPath := writeFakeLauncher(t, "uvx")
	server := writeServerConfig(t, mcpDir, pub_models.McpServer{
		Name:    "eager",
		Command: launcherPath,
		Args:    []string{"serve"},
	}, logPath)

	if !launcher.RequiresEagerConnect(server) {
		t.Fatal("an unmodelled launcher must require an eager connect")
	}
	cache := testSchemaCache(t)
	got, err := setupMcpManager(t.Context(), mcpDir, Configurations{}, &recordingSuccessSink{}, cache, nil)
	if err != nil {
		t.Fatalf("setupMcpManager: %v", err)
	}
	if _, ok := got["mcp_eager_echo"]; !ok {
		t.Fatalf("eager server did not register its live tools; got: %v", toolNames(got))
	}
	rec, ok := cache.Lookup(schemacache.BuildIdentity(server))
	if !ok {
		t.Fatal("an eager server's observed tools were not persisted to the schema cache")
	}
	if !strings.Contains(string(rec.Tools), `"echo"`) {
		t.Errorf("captured tools do not name the server's tool: %s", rec.Tools)
	}
}

// TestNoSetupNoticeForLauncherShapedWarmCache pins B3's removal directly: a
// warm cache hit for the dominant launcher shape prints nothing. The notice
// it replaces carried no information that varied between runs, and the
// unverified case it warned about now connects instead of hitting the cache.
func TestNoSetupNoticeForLauncherShapedWarmCache(t *testing.T) {
	mcpDir := t.TempDir()
	server := writeServerConfig(t, mcpDir, pub_models.McpServer{
		Name:    "quiet",
		Command: fakeLauncherOnPath(t, "npx"),
		Args:    []string{"-y", "slivingdoc@1.0.2", "serve"},
	}, filepath.Join(t.TempDir(), "spawns.log"))

	cache := testSchemaCache(t)
	if err := cache.Capture(schemacache.BuildIdentity(server), mcp.ProtocolVersion, json.RawMessage(`{}`), json.RawMessage(`[{"name":"t"}]`)); err != nil {
		t.Fatalf("seed warm cache: %v", err)
	}

	stdout := testboil.CaptureStdout(t, func(t *testing.T) {
		if _, err := setupMcpManager(t.Context(), mcpDir, Configurations{}, &recordingSuccessSink{}, cache, nil); err != nil {
			t.Fatalf("setupMcpManager: %v", err)
		}
	})
	for _, banned := range []string{"served from schema cache", "no handshake this run", "locally fingerprinted"} {
		if strings.Contains(stdout, banned) {
			t.Errorf("warm cache hit still printed the removed notice (%q): stdout=%q", banned, stdout)
		}
	}
}

// fakeLauncherOnPath installs an executable named name into a temp directory
// that is first on PATH, and returns its path. The binary is never run in a
// test that expects a cache hit, so its body only has to be executable: what
// the test needs is for exec.LookPath to resolve the name to a real local
// file, which is the premise every generic-launcher resolution rests on.
// Depending on the host's own npx or uvx would make these tests read the
// developer's machine instead of their own fixture.
func fakeLauncherOnPath(t *testing.T, name string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, name)
	body := "#!/bin/sh\nexit 0\n"
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatalf("write fake launcher %q: %v", path, err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return path
}

// writeFakeLauncher installs a package runner whose body runs this
// repository's stdio test fixture, and returns its path together with the
// spawn log the fixture appends to. It reproduces a launcher clai cannot
// resolve: a real local executable whose installed content lives somewhere
// clai does not model. The server binary travels through the environment
// rather than the argument list on purpose, since a path argument would be
// exactly the local evidence that makes a command line resolvable.
func writeFakeLauncher(t *testing.T, name string) (string, string) {
	t.Helper()
	dir := t.TempDir()
	logPath := filepath.Join(t.TempDir(), "spawns.log")
	path := filepath.Join(dir, name)
	body := "#!/bin/sh\nexec \"$CLAI_TEST_STDIO_SERVER\" \"$@\"\n"
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatalf("write fake launcher %q: %v", path, err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("CLAI_TEST_STDIO_SERVER", testServerBinary(t))
	return path, logPath
}

// writeServerConfig writes server into mcpDir as a config file named after
// it, with spawnLog wired into its env so the spawned fixture reports every
// process birth, and returns the server as the config parser will read it
// back. The returned value is what a schema-cache entry must be captured
// under: capturing under the caller's pre-env value would miss by
// construction and the test would silently measure a cold run.
func writeServerConfig(t *testing.T, mcpDir string, server pub_models.McpServer, spawnLog string) pub_models.McpServer {
	t.Helper()
	env := maps.Clone(server.Env)
	if env == nil {
		env = map[string]string{}
	}
	env["TEST_SERVER_SPAWN_LOG"] = spawnLog
	written := pub_models.McpServer{
		Name:    server.Name,
		Command: server.Command,
		Args:    server.Args,
		Env:     env,
	}
	data, err := json.Marshal(written)
	if err != nil {
		t.Fatalf("marshal server config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(mcpDir, server.Name+".json"), data, 0o644); err != nil {
		t.Fatalf("write server config: %v", err)
	}
	parsed, err := serverconfig.FindConfiguredServers([]string{filepath.Join(mcpDir, server.Name+".json")})
	if err != nil {
		t.Fatalf("parse written server config: %v", err)
	}
	if len(parsed) != 1 {
		t.Fatalf("parsed %d servers from the written config, want 1", len(parsed))
	}
	return parsed[0]
}
