package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	pub_models "github.com/baalimago/clai/pkg/text/models"
	pkgtools "github.com/baalimago/clai/pkg/tools"
)

// cmdBanChatForPrompt builds a fresh chat carrying the given user prompt. Each
// query uses a new chat so the mock vendor re-emits its tool call.
func cmdBanChatForPrompt(prompt string) pub_models.Chat {
	return pub_models.Chat{
		Created:  time.Now(),
		ID:       fmt.Sprintf("cmd-ban-e2e-%d", time.Now().UnixNano()),
		Messages: []pub_models.Message{{Role: "user", Content: prompt}},
	}
}

// queryCmdBanAgent runs the real agent path (Agent.Setup → CreateTextQuerier →
// TextQuery) once and returns the mutated chat.
func queryCmdBanAgent(t *testing.T, a *Agent, prompt string) pub_models.Chat {
	t.Helper()
	ctx := context.Background()
	if err := a.Setup(ctx); err != nil {
		t.Fatalf("Agent.Setup: %v", err)
	}
	chat, err := a.Query(ctx, cmdBanChatForPrompt(prompt))
	if err != nil {
		t.Fatalf("Agent.Query: %v", err)
	}
	return chat
}

// newCmdBanAgent builds an agent with an isolated config dir and pre-creates
// the mock vendor's model config with a price entry, so the cost manager can
// enrich the conversation instead of printing "missing pricing" errors
// (mirrors the price fixture the CLI e2e harness writes).
func newCmdBanAgent(t *testing.T, opts ...Option) Agent {
	t.Helper()
	cfgDir := t.TempDir()
	a := New(append([]Option{WithConfigDir(cfgDir)}, opts...)...)

	modelDir := filepath.Join(cfgDir, "clai")
	if err := os.MkdirAll(modelDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(%q): %v", modelDir, err)
	}
	priceBytes, err := json.Marshal(map[string]any{
		"price": map[string]any{
			"input_usd_per_token":        0.001,
			"input_cached_usd_per_token": 0.0005,
			"output_usd_per_token":       0.002,
		},
	})
	if err != nil {
		t.Fatalf("Marshal(price config): %v", err)
	}
	if err := os.WriteFile(filepath.Join(modelDir, "mock_test_mock_test.json"), priceBytes, 0o644); err != nil {
		t.Fatalf("WriteFile(mock_test_mock_test.json): %v", err)
	}
	return a
}

// runCmdBanCycles runs iterations of Setup+Query on the agent and returns the
// matched entry of every refusal the run observed.
func runCmdBanCycles(ctx context.Context, a *Agent, prompt string, iterations int) ([]string, error) {
	var refusals []string
	for range iterations {
		if err := a.Setup(ctx); err != nil {
			return nil, err
		}
		chat, err := a.Query(ctx, cmdBanChatForPrompt(prompt))
		if err != nil {
			return nil, err
		}
		if entry := refusalEntry(chat); entry != "" {
			refusals = append(refusals, entry)
		}
	}
	return refusals, nil
}

// refusalEntry returns the matched entry named by the run's refusal, or ""
// when the run refused nothing. Every query in these tests produces at most
// one refusal (the mock emits a single tool call per chat).
func refusalEntry(chat pub_models.Chat) string {
	for _, msg := range chat.Messages {
		if msg.Role != "tool" || !strings.Contains(msg.Content, "banned by policy") {
			continue
		}
		idx := strings.Index(msg.Content, `matched entry "`)
		if idx < 0 {
			continue
		}
		rest := msg.Content[idx+len(`matched entry "`):]
		if end := strings.Index(rest, `"`); end > 0 {
			return rest[:end]
		}
	}
	return ""
}

// assertCmdBanRefusal asserts the run refused a command and named entry.
func assertCmdBanRefusal(t *testing.T, chat pub_models.Chat, entry string) {
	t.Helper()
	if got := refusalEntry(chat); got != entry {
		t.Fatalf("expected refusal naming %q, got %q (messages=%d)", entry, got, len(chat.Messages))
	}
}

// assertCmdBanNoRefusal asserts the run refused nothing.
func assertCmdBanNoRefusal(t *testing.T, chat pub_models.Chat) {
	t.Helper()
	if got := refusalEntry(chat); got != "" {
		t.Fatalf("expected no refusal, got entry %q", got)
	}
}

// assertCmdBanRunCompleted asserts the run finished normally: the mock vendor
// finalizes with its "done after tool" response after the tool result.
func assertCmdBanRunCompleted(t *testing.T, chat pub_models.Chat) {
	t.Helper()
	msg, _, err := chat.LastOfRole("assistant")
	if err != nil {
		t.Fatalf("expected a final assistant message (run completed): %v", err)
	}
	if !strings.Contains(msg.Content, "done after tool") {
		t.Fatalf("expected the mock finalization in the transcript, got %q", msg.Content)
	}
}

func assertMarkerAbsent(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("banned command must never spawn, marker exists: %v", err)
	}
}

func assertMarkerPresent(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected marker to exist: %v", err)
	}
}

// TestAgentCmdBan_SingleAgentRefusal is the real-path proof that
// WithCmdBanList reaches the spawn-point check behind the public agent API:
// the mock fabricates `touch <marker>`, the freetext tool refuses it, the
// marker is never created, and the run completes normally (D7, D14).
func TestAgentCmdBan_SingleAgentRefusal(t *testing.T) {
	t.Setenv("CLAI_DISABLE_COST_ERR_LOG_GOROUTINE", "1")
	t.Cleanup(pkgtools.ResetCmdBanListForTests)

	marker := filepath.Join(t.TempDir(), "banned-marker")
	t.Setenv("CLAI_MOCK_CMD_COMMAND", "touch "+marker)

	a := newCmdBanAgent(t,
		WithModel("mock_test"),
		WithToolGlobs("cmd"),
		WithCmdBanList("touch"),
		WithOutputTo(io.Discard),
	)

	chat := queryCmdBanAgent(t, &a, "please tool_cmd")
	assertCmdBanRefusal(t, chat, "touch")
	assertCmdBanRunCompleted(t, chat)
	assertMarkerAbsent(t, marker)
}

// TestAgentCmdBan_SequentialPerRunIsolation proves each run's NewQuerier
// setter (Phase 3) replaces the previous run's list: a permissive run does not
// inherit a ban, and a banned run does not leak into the next.
func TestAgentCmdBan_SequentialPerRunIsolation(t *testing.T) {
	t.Setenv("CLAI_DISABLE_COST_ERR_LOG_GOROUTINE", "1")
	t.Cleanup(pkgtools.ResetCmdBanListForTests)

	marker1 := filepath.Join(t.TempDir(), "banned-marker")
	marker2 := filepath.Join(t.TempDir(), "permissive-marker")

	banned := newCmdBanAgent(t,
		WithModel("mock_test"),
		WithToolGlobs("cmd"),
		WithCmdBanList("touch"),
		WithOutputTo(io.Discard),
	)
	permissive := newCmdBanAgent(t,
		WithModel("mock_test"),
		WithToolGlobs("cmd"),
		WithOutputTo(io.Discard),
	)

	// Run 1: the banned agent refuses `touch <marker1>`.
	t.Setenv("CLAI_MOCK_CMD_COMMAND", "touch "+marker1)
	chat1 := queryCmdBanAgent(t, &banned, "please tool_cmd")
	assertCmdBanRefusal(t, chat1, "touch")
	assertMarkerAbsent(t, marker1)

	// Run 2: the permissive agent must not inherit the previous ban.
	t.Setenv("CLAI_MOCK_CMD_COMMAND", "touch "+marker2)
	chat2 := queryCmdBanAgent(t, &permissive, "please tool_cmd")
	assertCmdBanNoRefusal(t, chat2)
	assertMarkerPresent(t, marker2)

	// Run 3: the banned agent again — the permissive run must not have cleared
	// the ban for the next banned run.
	t.Setenv("CLAI_MOCK_CMD_COMMAND", "touch "+marker1)
	chat3 := queryCmdBanAgent(t, &banned, "please tool_cmd")
	assertCmdBanRefusal(t, chat3, "touch")
	assertMarkerAbsent(t, marker1)
}

// TestAgentCmdBan_ConcurrentDistinctLists is the enforcement test for cmdBanMu
// (R2-01): two agents with distinct ban lists run Setup+Query concurrently so
// spawn-path reads genuinely overlap the other run's SetCmdBanList write. The
// race detector must stay clean, every refusal must name the observer's own
// entry, and no command may ever spawn.
//
// Design notes (recorded in phase-5-pkg-agent-e2e.md):
//   - The two agents use different tools (cmd vs async_cmd) because the
//     mock fabricates freetext inputs from one process-global env var
//     (CLAI_MOCK_CMD_COMMAND), so two concurrent cmd agents could not carry
//     different commands.
//   - Markers live under a non-existent directory: under the package-global
//     list design (D6) a concurrent query may observe the other agent's list
//     (the mutex prevents data races, not logical cross-talk), so a
//     cross-talked execution must fail harmlessly and create nothing.
//   - A refusal for agent X implies X's own list was active when its command
//     was validated (each command's tokens match only its own entry), so
//     "every refusal names the observer's own entry" holds deterministically.
//   - At least one refusal is guaranteed: the agent performing the final
//     SetCmdBanList write reads its own list at its next spawn-path check (no
//     further writes can intervene).
func TestAgentCmdBan_ConcurrentDistinctLists(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test spawns POSIX binaries (rm) via async_cmd")
	}
	t.Setenv("CLAI_DISABLE_COST_ERR_LOG_GOROUTINE", "1")
	t.Cleanup(pkgtools.ResetCmdBanListForTests)
	t.Cleanup(pkgtools.ResetAsyncCmdManagerForTests)

	nowhere := filepath.Join(t.TempDir(), "does-not-exist")
	m1 := filepath.Join(nowhere, "m1")
	m2 := filepath.Join(nowhere, "m2")

	t.Setenv("CLAI_MOCK_CMD_COMMAND", "touch "+m1)
	t.Setenv("CLAI_MOCK_ASYNC_CMD_RUN_COMMAND", "rm")
	t.Setenv("CLAI_MOCK_ASYNC_CMD_RUN_ARGS", "-rf "+m2)

	touchAgent := newCmdBanAgent(t,
		WithModel("mock_test"),
		WithToolGlobs("cmd"),
		WithCmdBanList("touch"),
		WithOutputTo(io.Discard),
	)
	rmAgent := newCmdBanAgent(t,
		WithModel("mock_test"),
		WithToolGlobs("async_cmd"),
		WithCmdBanList("rm"),
		WithOutputTo(io.Discard),
	)

	const iterations = 3
	var wg sync.WaitGroup
	start := make(chan struct{})
	var (
		touchRefusals []string
		rmRefusals    []string
		touchErr      error
		rmErr         error
	)
	wg.Add(2)
	go func() {
		defer wg.Done()
		<-start
		touchRefusals, touchErr = runCmdBanCycles(context.Background(), &touchAgent, "please tool_cmd", iterations)
	}()
	go func() {
		defer wg.Done()
		<-start
		rmRefusals, rmErr = runCmdBanCycles(context.Background(), &rmAgent, "please tool_async_cmd", iterations)
	}()
	close(start)
	wg.Wait()

	if touchErr != nil {
		t.Fatalf("touch agent: %v", touchErr)
	}
	if rmErr != nil {
		t.Fatalf("rm agent: %v", rmErr)
	}
	// Every command is refused under the policy carried by its own query
	// context, regardless of the other agent's concurrent setup.
	if len(touchRefusals) != iterations {
		t.Fatalf("expected %d touch refusals, got %v", iterations, touchRefusals)
	}
	if len(rmRefusals) != iterations {
		t.Fatalf("expected %d rm refusals, got %v", iterations, rmRefusals)
	}
	for _, entry := range touchRefusals {
		if entry != "touch" {
			t.Fatalf("touch agent observed a refusal naming %q", entry)
		}
	}
	for _, entry := range rmRefusals {
		if entry != "rm" {
			t.Fatalf("rm agent observed a refusal naming %q", entry)
		}
	}
	// No command may ever spawn.
	assertMarkerAbsent(t, m1)
	assertMarkerAbsent(t, m2)
}

// TestAgentCmdBan_ConcurrentPermissiveAndBanned proves concurrent permissive +
// banned runs behave independently: the permissive run executes and creates
// its marker while the banned run refuses every iteration.
//
// Design notes (recorded in phase-5-pkg-agent-e2e.md):
//   - The permissive agent uses `printf ok > <marker>` instead of a literal
//     `touch`: under the package-global list (D6) its concurrent queries may
//     observe the banned agent's list, which would refuse a literal `touch`.
//     The permissive command creates the marker without matching any entry.
//   - The banned agent runs on async_cmd (`touch <marker>`) and the
//     permissive agent on cmd so each agent's mock input comes from its own
//     env var. Only the banned agent writes the global list during the
//     concurrent phase (the permissive agent sets it once, before the phase),
//     so the banned agent's queries deterministically observe ["touch"] and
//     are refused every iteration.
func TestAgentCmdBan_ConcurrentPermissiveAndBanned(t *testing.T) {
	t.Setenv("CLAI_DISABLE_COST_ERR_LOG_GOROUTINE", "1")
	t.Cleanup(pkgtools.ResetCmdBanListForTests)

	permissiveMarker := filepath.Join(t.TempDir(), "permissive-marker")
	bannedMarker := filepath.Join(t.TempDir(), "banned-marker")

	t.Setenv("CLAI_MOCK_CMD_COMMAND", "printf ok > "+permissiveMarker)
	t.Setenv("CLAI_MOCK_ASYNC_CMD_RUN_COMMAND", "touch")
	t.Setenv("CLAI_MOCK_ASYNC_CMD_RUN_ARGS", bannedMarker)

	permissive := newCmdBanAgent(t,
		WithModel("mock_test"),
		WithToolGlobs("cmd"),
		WithOutputTo(io.Discard),
	)
	banned := newCmdBanAgent(t,
		WithModel("mock_test"),
		WithToolGlobs("async_cmd"),
		WithCmdBanList("touch"),
		WithOutputTo(io.Discard),
	)

	ctx := context.Background()
	// The permissive agent sets the empty list once, before the concurrent
	// phase; during the phase only the banned agent writes the global list.
	if err := permissive.Setup(ctx); err != nil {
		t.Fatalf("permissive Agent.Setup: %v", err)
	}

	const iterations = 3
	var wg sync.WaitGroup
	start := make(chan struct{})
	var (
		permissiveRefusals []string
		bannedRefusals     []string
		permissiveErr      error
		bannedErr          error
	)
	wg.Add(2)
	go func() {
		defer wg.Done()
		<-start
		for range iterations {
			chat, err := permissive.Query(ctx, cmdBanChatForPrompt("please tool_cmd"))
			if err != nil {
				permissiveErr = err
				return
			}
			if entry := refusalEntry(chat); entry != "" {
				permissiveRefusals = append(permissiveRefusals, entry)
			}
		}
	}()
	go func() {
		defer wg.Done()
		<-start
		bannedRefusals, bannedErr = runCmdBanCycles(ctx, &banned, "please tool_async_cmd", iterations)
	}()
	close(start)
	wg.Wait()

	if permissiveErr != nil {
		t.Fatalf("permissive agent: %v", permissiveErr)
	}
	if bannedErr != nil {
		t.Fatalf("banned agent: %v", bannedErr)
	}
	// The permissive run is unaffected: its command executed every iteration
	// and it never observed a refusal.
	assertMarkerPresent(t, permissiveMarker)
	if len(permissiveRefusals) != 0 {
		t.Fatalf("permissive agent must never be refused, got %v", permissiveRefusals)
	}
	// The banned agent is refused every iteration, always naming its own entry.
	if len(bannedRefusals) != iterations {
		t.Fatalf("expected %d refusals for the banned agent, got %v", iterations, bannedRefusals)
	}
	for _, entry := range bannedRefusals {
		if entry != "touch" {
			t.Fatalf("banned agent observed a refusal naming %q", entry)
		}
	}
	assertMarkerAbsent(t, bannedMarker)
}
