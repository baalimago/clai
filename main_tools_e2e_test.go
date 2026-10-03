package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/baalimago/clai/internal/tools/mcp/schemacache"
	pub_models "github.com/baalimago/clai/pkg/text/models"
	"github.com/baalimago/go_away_boilerplate/pkg/testboil"
)

func Test_goldenFile_TOOLS_lists_tools_and_footer(t *testing.T) {
	oldArgs := os.Args
	t.Cleanup(func() {
		os.Args = oldArgs
	})

	_ = setupMainTestConfigDir(t)

	var gotStatus int
	stdout := testboil.CaptureStdout(t, func(t *testing.T) {
		gotStatus = run(strings.Split("tools", " "))
	})

	testboil.FailTestIfDiff(t, gotStatus, 0)

	// We don't assert the entire listing because it changes as tools are added.
	// Instead, assert stable behaviors described in architecture/tools.md.
	testboil.AssertStringContains(t, stdout, "Run 'clai tools <tool-name>' for more details.\n")
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

	confDir := setupMainTestConfigDir(t)

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
		gotStatus = run(strings.Split("tools", " "))
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

	_ = setupMainTestConfigDir(t)

	var gotStatus int
	stdout := testboil.CaptureStdout(t, func(t *testing.T) {
		gotStatus = run(strings.Split("tools definitely_not_a_tool", " "))
	})

	if gotStatus == 0 {
		t.Fatalf("expected non-zero status code")
	}
	if stdout != "" {
		t.Fatalf("expected no stdout, got: %q", stdout)
	}
}
