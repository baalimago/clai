package text

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/baalimago/clai/internal/tools/mcp/schemacache"
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

// TestWarmCacheLauncherOnlyServerPrintsSetupNotice pins the sign-off
// review's B3: for the dominant "npx -y <package>" shape, only the launcher
// (npx itself) is fingerprinted, so a package upgrade or breakage other
// than npx changing is invisible at setup for the whole freshness bound,
// and silent if the model never calls the tool. A cache hit against such a
// server must still print a one-line setup-time notice naming the server
// and the fact that it was served from cache with no handshake this run, so
// an operator is not left believing the advertised tools were just
// observed. A warm hit against a direct-binary server (testServerBinary)
// must print no such notice: Executable already fingerprints the server
// itself there, so the cache already wins on its own, per B3.
func TestWarmCacheLauncherOnlyServerPrintsSetupNotice(t *testing.T) {
	server := pub_models.McpServer{Name: "launcheronly", Command: "npx", Args: []string{"-y", "@scope/server"}}
	identity := schemacache.BuildIdentity(server)
	cache := testSchemaCache(t)
	if err := cache.Capture(identity, "2025-06-18", []byte(`{}`), []byte(`[{"name":"t"}]`)); err != nil {
		t.Fatalf("seed warm cache: %v", err)
	}

	conf := Configurations{
		SkipAmbientMcpServers: true,
		McpServers:            []pub_models.McpServer{server},
	}
	stdout := testboil.CaptureStdout(t, func(t *testing.T) {
		if _, err := setupMcpManager(t.Context(), t.TempDir(), conf, &recordingSuccessSink{}, cache, nil); err != nil {
			t.Fatalf("setupMcpManager: %v", err)
		}
	})
	if !strings.Contains(stdout, server.Name) || !strings.Contains(stdout, "cache") {
		t.Errorf("warm launcher-only cache hit printed no setup notice naming the server: stdout=%q", stdout)
	}
}

// TestWarmCacheDirectBinaryServerPrintsNoNotice is the control for
// TestWarmCacheLauncherOnlyServerPrintsSetupNotice: a direct-binary server's
// Executable component already fingerprints the server's own content, so
// B3's notice would be noise here and must not print.
func TestWarmCacheDirectBinaryServerPrintsNoNotice(t *testing.T) {
	spawnLog := filepath.Join(t.TempDir(), "spawns.log")
	server := pub_models.McpServer{
		Name:    "directbinary",
		Command: testServerBinary(t),
		Env:     map[string]string{"TEST_SERVER_SPAWN_LOG": spawnLog},
	}
	identity := schemacache.BuildIdentity(server)
	cache := testSchemaCache(t)
	if err := cache.Capture(identity, "2025-06-18", []byte(`{}`), []byte(`[{"name":"t"}]`)); err != nil {
		t.Fatalf("seed warm cache: %v", err)
	}

	conf := Configurations{
		SkipAmbientMcpServers: true,
		McpServers:            []pub_models.McpServer{server},
	}
	stdout := testboil.CaptureStdout(t, func(t *testing.T) {
		if _, err := setupMcpManager(t.Context(), t.TempDir(), conf, &recordingSuccessSink{}, cache, nil); err != nil {
			t.Fatalf("setupMcpManager: %v", err)
		}
	})
	if strings.Contains(stdout, "served from schema cache") {
		t.Errorf("direct-binary warm hit printed B3's notice, want none: stdout=%q", stdout)
	}
}
