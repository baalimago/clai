package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/baalimago/clai/internal/tools/mcp"
	"github.com/baalimago/clai/internal/tools/mcp/schemacache"
	pub_models "github.com/baalimago/clai/pkg/text/models"
)

// countMcpLazySpawns reports how many lines the testserver's
// TEST_SERVER_SPAWN_LOG file carries, i.e. how many times the fixture
// process was actually born. A missing file means zero.
func countMcpLazySpawns(t *testing.T, path string) int {
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

// TestLazyStartupE2EUnderRace is the lazy path's own end-to-end fixture
// (README code-layout table, Phase 3 row): the pre-existing spawning tests
// in internal/text pin the eager posture, so this is the only fixture that
// runs the schema-cache-aware lazy path under the race detector. A cold run
// against a lazy, ambient command-based server must connect exactly once and
// warm the cache; a second run against the same warm cache must register the
// same tool, by name, without spawning the server again and without a
// "doesn't exist" warning (R2-12: exit status 0 alone cannot carry that
// claim, since an unresolved -t name is itself only a warning and a
// continue).
//
// The config carries no "startup" field at all, so this also proves the
// phase's flipped default (D16) rather than a pinned lazy posture (R2-27):
// without the flip, an unset startup would resolve eager and the cold run's
// spawn-on-connect would be indistinguishable from this test's actual claim.
func TestLazyStartupE2EUnderRace(t *testing.T) {
	oldArgs := os.Args
	t.Cleanup(func() { os.Args = oldArgs })

	confDir := setupMainTestConfigDir(t)
	// DEBUG makes setupTooling print each tool name it finds in -t, which is
	// the only production-printed, positive evidence that a registered tool
	// is present (R2-12) short of actually invoking it — invoking it would
	// itself spawn a connection through the lazy connector the warm run's
	// "no spawn" assertion below must stay true for.
	t.Setenv("DEBUG", "1")

	spawnLog := filepath.Join(t.TempDir(), "spawns.log")
	mcpDir := filepath.Join(confDir, "mcpServers")
	conf := fmt.Sprintf(`{"command":%q,"env":{"TEST_SERVER_SPAWN_LOG":%q}}`, testServerBinary(t), spawnLog)
	if err := os.WriteFile(filepath.Join(mcpDir, "echo.json"), []byte(conf), 0o644); err != nil {
		t.Fatalf("write mcp server config: %v", err)
	}

	queryArgs := strings.Split("-r -cm mock_test -t=mcp_echo_echo q hello", " ")

	var coldStatus int
	coldStdout, coldStderr := captureStdoutStderr(t, func() {
		coldStatus = run(queryArgs)
	})
	if coldStatus != 0 {
		t.Fatalf("cold run: status %d, stderr=%q", coldStatus, coldStderr)
	}
	if strings.Contains(coldStdout, "which doesn't exist") {
		t.Fatalf("cold run: stdout = %q, want mcp_echo_echo found, not warned about", coldStdout)
	}
	coldSpawns := countMcpLazySpawns(t, spawnLog)
	if coldSpawns != 1 {
		t.Fatalf("cold run spawn count = %d, want exactly 1 (one server, one connect)", coldSpawns)
	}

	var warmStatus int
	warmStdout, warmStderr := captureStdoutStderr(t, func() {
		warmStatus = run(queryArgs)
	})
	if warmStatus != 0 {
		t.Fatalf("warm run: status %d, stderr=%q", warmStatus, warmStderr)
	}
	if !strings.Contains(warmStdout, "mcp_echo_echo") {
		t.Fatalf("warm run: stdout = %q, want positive evidence mcp_echo_echo registered", warmStdout)
	}
	if strings.Contains(warmStdout, "which doesn't exist") {
		t.Fatalf("warm run: stdout = %q, want mcp_echo_echo found, not warned about", warmStdout)
	}
	if got := countMcpLazySpawns(t, spawnLog); got != coldSpawns {
		t.Errorf("warm-cache run spawned the server again: spawn log has %d lines, want %d (unchanged)", got, coldSpawns)
	}
}

// TestLazyMultiServerSpawnsOnlyTheCalledServer closes R2-04: the sentence
// this worklog exists to prove — "a clai run whose MCP cost is proportional
// to the servers it uses, not the servers it is configured with" — had no
// end-to-end evidence naming more than one server. The only existing proof
// (TestLazyStartupE2EUnderRace, schema_cache_setup_test.go) is single-server,
// so it cannot distinguish "proportional to servers used" from "proportional
// to servers configured, minus one cache hit": with only one server, those
// two claims predict the same spawn count.
//
// Both servers' schema entries are written straight into the cache
// (schemacache.Capture), not warmed through a real connect, so entering this
// run both are already at exactly zero spawns — unlike a real warm-up round
// trip, which would itself spawn both and make the "zero" assertion below
// trivially true regardless of what this run does. Only one server's tool
// is actually invoked (the mock vendor calls a tool only when the prompt
// carries its tool_<name> token, vendors/mock.go's nextToolCall): the called
// server's lazy connector must resolve, spawning exactly one process: the
// uncalled server's connector is never touched and must spawn none.
func TestLazyMultiServerSpawnsOnlyTheCalledServer(t *testing.T) {
	oldArgs := os.Args
	t.Cleanup(func() { os.Args = oldArgs })

	confDir := setupMainTestConfigDir(t)
	bin := testServerBinary(t)
	mcpDir := filepath.Join(confDir, "mcpServers")

	spawnLogA := filepath.Join(t.TempDir(), "a.log")
	spawnLogB := filepath.Join(t.TempDir(), "b.log")
	serverA := pub_models.McpServer{Name: "servera", Command: bin, Env: map[string]string{"TEST_SERVER_SPAWN_LOG": spawnLogA}}
	serverB := pub_models.McpServer{Name: "serverb", Command: bin, Env: map[string]string{"TEST_SERVER_SPAWN_LOG": spawnLogB}}

	for _, s := range []struct {
		fileName string
		server   pub_models.McpServer
	}{
		{"servera.json", serverA},
		{"serverb.json", serverB},
	} {
		conf := fmt.Sprintf(`{"command":%q,"env":{"TEST_SERVER_SPAWN_LOG":%q}}`, s.server.Command, s.server.Env["TEST_SERVER_SPAWN_LOG"])
		if err := os.WriteFile(filepath.Join(mcpDir, s.fileName), []byte(conf), 0o644); err != nil {
			t.Fatalf("write %s: %v", s.fileName, err)
		}
	}

	cache, err := schemacache.New(filepath.Join(confDir, "cache", schemacache.DefaultDirName))
	if err != nil {
		t.Fatalf("schemacache.New: %v", err)
	}
	toolsJSON, err := json.Marshal([]map[string]any{{"name": "echo", "description": "Echo"}})
	if err != nil {
		t.Fatalf("marshal tools: %v", err)
	}
	for _, srv := range []pub_models.McpServer{serverA, serverB} {
		if err := cache.Capture(schemacache.BuildIdentity(srv), mcp.ProtocolVersion, json.RawMessage(`{}`), toolsJSON); err != nil {
			t.Fatalf("Capture(%s): %v", srv.Name, err)
		}
	}
	if got := countMcpLazySpawns(t, spawnLogA); got != 0 {
		t.Fatalf("server a spawned %d times before this run even started", got)
	}
	if got := countMcpLazySpawns(t, spawnLogB); got != 0 {
		t.Fatalf("server b spawned %d times before this run even started", got)
	}

	queryArgs := strings.Split("-r -cm mock_test -t=mcp_servera_echo,mcp_serverb_echo q please tool_mcp_servera_echo", " ")
	var status int
	stdout, stderr := captureStdoutStderr(t, func() {
		status = run(queryArgs)
	})
	if status != 0 {
		t.Fatalf("run: status %d, stdout=%q, stderr=%q", status, stdout, stderr)
	}
	if strings.Contains(stdout, "unknown tool call") {
		t.Fatalf("stdout = %q, want mcp_servera_echo to be a known, registered tool", stdout)
	}

	if got := countMcpLazySpawns(t, spawnLogA); got != 1 {
		t.Errorf("called server's spawn log has %d lines, want exactly 1 (warm cache, resolved once on the actual call)", got)
	}
	if got := countMcpLazySpawns(t, spawnLogB); got != 0 {
		t.Errorf("uncalled server's spawn log has %d lines, want exactly 0 (warm cache, never resolved: cost tracks use, not configuration)", got)
	}
}
