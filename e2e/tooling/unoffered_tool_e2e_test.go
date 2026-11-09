package tooling

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/baalimago/clai/e2e/e2etest"
	"github.com/baalimago/clai/internal/testenv/stubllm"
	"github.com/baalimago/clai/internal/tools"
)

const unknownCmd = "ERROR: unknown tool call: cmd"

// callOnce answers the first request with call and the one after its tool
// result with THE-REPLY.
func callOnce(call stubllm.Answer) stubllm.Script {
	return func(body string) stubllm.Answer {
		if stubllm.HasToolResult(body) {
			return stubllm.Answer{Text: "THE-REPLY"}
		}
		return call
	}
}

func assertNotRun(t *testing.T, marker string) {
	t.Helper()
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("the unoffered tool ran: marker stat err = %v, want not exist", err)
	}
}

// assertModelToldUnknown checks the request after the call: the model reads
// the refusal as the call's tool result.
func assertModelToldUnknown(t *testing.T, provider *stubllm.Provider, refusal string) {
	t.Helper()
	requests := provider.Requests()
	if len(requests) != 2 {
		t.Fatalf("provider got %d requests, want the call and its follow-up", len(requests))
	}
	if !strings.Contains(requests[1], refusal) {
		t.Fatalf("follow-up request = %q, want it to carry %q", requests[1], refusal)
	}
}

// A call of a tool the request did not offer is refused on every selection:
// a narrowed -t, no -t at all, and an MCP-only selection.
func TestUnofferedToolCallIsRefused(t *testing.T) {
	rows := []struct {
		name    string
		args    string
		offered []string
		setup   func(t *testing.T, confDir string)
	}{
		{name: "narrowed selection", args: "-cm gpt-stub -t pwd q hi", offered: []string{"pwd"}},
		{name: "no selection", args: "-cm gpt-stub q hi"},
		{
			name: "mcp selection", args: "-cm gpt-stub -t mcp_echo_echo q hi", offered: []string{"mcp_echo_echo"},
			setup: func(t *testing.T, confDir string) {
				conf := fmt.Sprintf(`{"command":%q}`, testServerBinary(t))
				if err := os.WriteFile(filepath.Join(confDir, "mcpServers", "echo.json"), []byte(conf), 0o644); err != nil {
					t.Fatalf("write mcp server config: %v", err)
				}
			},
		},
	}
	for _, row := range rows {
		for _, mode := range []string{"text", "json"} {
			t.Run(row.name+"/"+mode, func(t *testing.T) {
				confDir := e2etest.SetupMainTestConfigDir(t)
				// A host that has run any tool-using agent holds every built-in
				// in the process registry.
				tools.Init(context.Background())
				marker := filepath.Join(t.TempDir(), "ran")
				provider := stubllm.Start(t, callOnce(stubllm.TouchCall("cmd", marker)))
				provider.WriteModel(t, confDir, "gpt-stub")
				if row.setup != nil {
					row.setup(t, confDir)
				}
				args := strings.Split(row.args, " ")
				if mode == "json" {
					args = append([]string{"-json"}, args...)
				} else {
					args = append([]string{"-r"}, args...)
				}

				run := runOnStreams(args, streamOpts{})

				assertNotRun(t, marker)
				if run.status != 0 {
					t.Fatalf("status = %d, stdout=%q stderr=%q", run.status, run.stdout, run.stderr)
				}
				assertModelToldUnknown(t, provider, unknownCmd)
				offered := stubllm.OfferedTools(t, provider.Requests()[0])
				slices.Sort(offered)
				if !slices.Equal(offered, row.offered) {
					t.Fatalf("offered tools = %v, want %v", offered, row.offered)
				}
				if mode == "text" {
					if !strings.Contains(run.stdout, unknownCmd) || !strings.Contains(run.stdout, "THE-REPLY") {
						t.Fatalf("stdout = %q, want the refusal and the reply", run.stdout)
					}
					return
				}
				results := ofType(parseRecords(t, "stdout", run.stdout), "tool_result")
				if len(results) != 1 || results[0].str("name") != "cmd" || results[0]["ok"] != false ||
					results[0].str("error") != "unknown tool call: cmd" {
					t.Fatalf("tool_result records = %v, want one failed result for cmd", results)
				}
			})
		}
	}
}

// A call under a legacy alias of an offered tool runs that tool.
func TestAliasOfOfferedToolStillRuns(t *testing.T) {
	confDir := e2etest.SetupMainTestConfigDir(t)
	marker := filepath.Join(t.TempDir(), "ran")
	provider := stubllm.Start(t, callOnce(stubllm.TouchCall("freetext_command", marker)))
	provider.WriteModel(t, confDir, "gpt-stub")

	run := runOnStreams(strings.Split("-r -cm gpt-stub -t cmd q hi", " "), streamOpts{})

	if run.status != 0 {
		t.Fatalf("status = %d, stdout=%q stderr=%q", run.status, run.stdout, run.stderr)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("the aliased call did not run cmd: %v; stdout=%q", err, run.stdout)
	}
	if strings.Contains(run.stdout, "unknown tool call") {
		t.Fatalf("stdout = %q, want no refusal", run.stdout)
	}
}

// The alias names no tool of its own: without cmd offered it is refused.
func TestAliasOfUnofferedToolIsRefused(t *testing.T) {
	confDir := e2etest.SetupMainTestConfigDir(t)
	tools.Init(context.Background())
	marker := filepath.Join(t.TempDir(), "ran")
	provider := stubllm.Start(t, callOnce(stubllm.TouchCall("freetext_command", marker)))
	provider.WriteModel(t, confDir, "gpt-stub")

	run := runOnStreams(strings.Split("-r -cm gpt-stub -t pwd q hi", " "), streamOpts{})

	assertNotRun(t, marker)
	if run.status != 0 {
		t.Fatalf("status = %d, stdout=%q stderr=%q", run.status, run.stdout, run.stderr)
	}
	assertModelToldUnknown(t, provider, "ERROR: unknown tool call: freetext_command")
}
