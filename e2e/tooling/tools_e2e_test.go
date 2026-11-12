package tooling

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/baalimago/clai/e2e/e2etest"
	"github.com/baalimago/clai/internal/tools"
	"github.com/baalimago/clai/internal/tools/mcp/schemacache"
	pub_models "github.com/baalimago/clai/pkg/text/models"
	"github.com/baalimago/go_away_boilerplate/pkg/testboil"
)

func Test_goldenFile_TOOLS_lists_tools_and_footer(t *testing.T) {
	oldArgs := os.Args
	t.Cleanup(func() {
		os.Args = oldArgs
	})

	_ = e2etest.SetupMainTestConfigDir(t)

	var gotStatus int
	stdout := testboil.CaptureStdout(t, func(t *testing.T) {
		gotStatus = harness.Run(strings.Split("tools", " "))
	})

	testboil.FailTestIfDiff(t, gotStatus, 0)

	// We don't assert the entire listing because it changes as tools are added.
	// Instead, assert stable behaviors described in architecture/tools.md.
	testboil.AssertStringContains(t, stdout, "Run 'clai tools <tool-name>' for more details.\n")
}

// Test_goldenFile_TOOLS_json_is_one_document pins the one exception to the
// record stream: `clai tools -json` is the full listing as one JSON document
// on stdout, so a script parses the tools directly instead of reading each
// output record's truncated text.
func Test_goldenFile_TOOLS_json_is_one_document(t *testing.T) {
	_ = e2etest.SetupMainTestConfigDir(t)

	var gotStatus int
	stdout := testboil.CaptureStdout(t, func(t *testing.T) {
		gotStatus = harness.Run(strings.Split("-json tools", " "))
	})

	testboil.FailTestIfDiff(t, gotStatus, 0)
	if strings.Contains(stdout, `"type":"output"`) {
		t.Fatalf("the -json listing is still the record stream:\n%s", stdout)
	}
	var listed []struct {
		Name        string   `json:"name"`
		Description string   `json:"description"`
		Aliases     []string `json:"aliases"`
	}
	if err := json.Unmarshal([]byte(stdout), &listed); err != nil {
		t.Fatalf("unmarshal the listing: %v\n%s", err, stdout)
	}
	for _, tool := range listed {
		if tool.Name == "async_cmd" {
			if !slices.Contains(tool.Aliases, "async_cmd_run") {
				t.Fatalf("async_cmd aliases = %v, want async_cmd_run", tool.Aliases)
			}
			if tool.Description == "" {
				t.Fatal("async_cmd has no description in the JSON listing")
			}
			return
		}
	}
	t.Fatalf("the listing holds no async_cmd:\n%s", stdout)
}

// Test_goldenFile_TOOLS_lists_mcp_tools_from_cache closes R2-12's phase-7
// share: before this, the only e2e evidence for the listing path asserted
// just the footer string, so the cache-sourced tool set and the shadow
// advisory the reworded "tools -h" text advertises were both unexercised
// above the unit level. A real cache entry under CLAI_CACHE_DIR, read by
// the real `clai tools` command path, closes that gap. The advisory is a
// single footer line, not a per-tool marker (sign-off review, 2026-10-03):
// see TestShadowFooterNeverAttributesToASpecificRemoteTool.
func Test_goldenFile_TOOLS_lists_mcp_tools_from_cache(t *testing.T) {
	oldArgs := os.Args
	t.Cleanup(func() {
		os.Args = oldArgs
	})

	confDir := e2etest.SetupMainTestConfigDir(t)

	server := pub_models.McpServer{Name: "fs", Command: "/definitely-not-a-real-binary-clai-main-e2e"}
	mcpConfig := fmt.Sprintf(`{"command":%q}`, server.Command)
	if err := os.WriteFile(filepath.Join(confDir, "mcpServers", "fs.json"), []byte(mcpConfig), 0o644); err != nil {
		t.Fatalf("write mcp server config: %v", err)
	}

	cache, err := schemacache.New(filepath.Join(confDir, "cache", schemacache.DefaultDirName))
	if err != nil {
		t.Fatalf("schemacache.New: %v", err)
	}
	toolsJSON, err := json.Marshal([]map[string]any{
		{"name": "write_file", "description": "Write a file"},
	})
	if err != nil {
		t.Fatalf("marshal tools: %v", err)
	}
	if err := cache.Capture(schemacache.BuildIdentity(server), "2025-06-18", json.RawMessage(`{}`), toolsJSON); err != nil {
		t.Fatalf("Capture: %v", err)
	}

	var gotStatus int
	stdout := testboil.CaptureStdout(t, func(t *testing.T) {
		gotStatus = harness.Run(strings.Split("tools", " "))
	})

	testboil.FailTestIfDiff(t, gotStatus, 0)
	testboil.AssertStringContains(t, stdout, "- mcp_fs_write_file: Write a file\n")
	testboil.AssertStringContains(t, stdout, "Note: a configured MCP server's tool may already be covered by a local built-in: write_file.")
}

func Test_goldenFile_TOOLS_unknown_tool_errors(t *testing.T) {
	oldArgs := os.Args
	t.Cleanup(func() {
		os.Args = oldArgs
	})

	_ = e2etest.SetupMainTestConfigDir(t)

	var gotStatus int
	stdout := testboil.CaptureStdout(t, func(t *testing.T) {
		gotStatus = harness.Run(strings.Split("tools definitely_not_a_tool", " "))
	})

	if gotStatus == 0 {
		t.Fatalf("expected non-zero status code")
	}
	if stdout != "" {
		t.Fatalf("expected no stdout, got: %q", stdout)
	}
}

// Test_goldenFile_TOOLS_lists_every_builtin_on_a_bare_machine pins the
// registry contract: the listing is clai's catalog, not the machine's
// toolchain, so a machine whose PATH resolves nothing still names every
// built-in and says which program each missing one needs.
func Test_goldenFile_TOOLS_lists_every_builtin_on_a_bare_machine(t *testing.T) {
	_ = e2etest.SetupMainTestConfigDir(t)
	t.Setenv("PATH", t.TempDir())

	tools.WithTestRegistry(t, func() {
		var gotStatus int
		text := testboil.CaptureStdout(t, func(t *testing.T) {
			gotStatus = harness.Run(strings.Split("tools", " "))
		})
		testboil.FailTestIfDiff(t, gotStatus, 0)
		testboil.AssertStringContains(t, text, "- rg [needs rg]: ")
		testboil.AssertStringContains(t, text, "Note: tools marked [needs <program>]")
		testboil.AssertStringContains(t, text, "Run 'clai tools <tool-name>' for more details.\n")

		jsonOut := testboil.CaptureStdout(t, func(t *testing.T) {
			gotStatus = harness.Run(strings.Split("-json tools", " "))
		})
		testboil.FailTestIfDiff(t, gotStatus, 0)
		var listed []struct {
			Name       string `json:"name"`
			Dependency string `json:"dependency"`
			Available  bool   `json:"available"`
		}
		if err := json.Unmarshal([]byte(jsonOut), &listed); err != nil {
			t.Fatalf("unmarshal the listing: %v\n%s", err, jsonOut)
		}
		for _, entry := range listed {
			if entry.Name != "rg" {
				continue
			}
			if entry.Dependency != "rg" || entry.Available {
				t.Fatalf("rg = %+v, want dependency rg on a machine without it", entry)
			}
			return
		}
		t.Fatalf("the listing holds no rg:\n%s", jsonOut)
	})
}
