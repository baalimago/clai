package tools

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/baalimago/clai/internal/tools/mcp/schemacache"
	pub_models "github.com/baalimago/clai/pkg/text/models"
)

// writeMcpConfig writes a minimal MCP server config file under
// <configDir>/mcpServers/<name>.json, extraJSON merged in verbatim (e.g.
// `"startup":"lazy"`), and returns the pub_models.McpServer the listing's
// own config parser (serverconfig.FindConfiguredServers) derives from it,
// so a caller can build the matching schema cache identity.
func writeMcpConfig(t *testing.T, configDir, name, command, extraJSON string) pub_models.McpServer {
	t.Helper()
	serversDir := filepath.Join(configDir, "mcpServers")
	if err := os.MkdirAll(serversDir, 0o755); err != nil {
		t.Fatalf("mkdir mcpServers: %v", err)
	}
	cfg := fmt.Sprintf(`{"command":%q%s}`, command, extraJSON)
	path := filepath.Join(serversDir, name+".json")
	if err := os.WriteFile(path, []byte(cfg), 0o644); err != nil {
		t.Fatalf("write config %q: %v", path, err)
	}
	return pub_models.McpServer{Name: name, Command: command}
}

// writeMcpHttpConfig writes a minimal endpoint-based MCP server config file
// under <configDir>/mcpServers/<name>.json and returns the matching
// pub_models.McpServer, mirroring writeMcpConfig for the url transport.
func writeMcpHttpConfig(t *testing.T, configDir, name, url string) pub_models.McpServer {
	t.Helper()
	serversDir := filepath.Join(configDir, "mcpServers")
	if err := os.MkdirAll(serversDir, 0o755); err != nil {
		t.Fatalf("mkdir mcpServers: %v", err)
	}
	cfg := fmt.Sprintf(`{"url":%q}`, url)
	path := filepath.Join(serversDir, name+".json")
	if err := os.WriteFile(path, []byte(cfg), 0o644); err != nil {
		t.Fatalf("write config %q: %v", path, err)
	}
	return pub_models.McpServer{Name: name, Url: url}
}

// captureListingCacheEntry warms the schema cache under cacheDir for
// server with toolsList as its tools/list result, exactly as a prior
// successful handshake would have (phase 3's Capture), so the listing's
// cache-only read can find it without any process ever running.
func captureListingCacheEntry(t *testing.T, cacheDir string, server pub_models.McpServer, toolsList []cachedRemoteTool) {
	t.Helper()
	cache, err := schemacache.New(filepath.Join(cacheDir, schemacache.DefaultDirName))
	if err != nil {
		t.Fatalf("schemacache.New: %v", err)
	}
	toolsJSON, err := json.Marshal(toolsList)
	if err != nil {
		t.Fatalf("marshal tools: %v", err)
	}
	identity := schemacache.BuildIdentityWithScopes(server)
	if err := cache.Capture(identity, "2025-06-18", json.RawMessage(`{}`), toolsJSON); err != nil {
		t.Fatalf("Capture: %v", err)
	}
}

// unreachableCommand never resolves to a real executable, so if the
// listing ever tried to spawn it (it must not), the attempt would fail
// loudly and immediately rather than silently succeeding.
const unreachableCommand = "/definitely-not-a-real-binary-clai-phase7"

// spawnSentinelCommand writes a real, executable script that touches a
// sentinel file (its own path, baked into the script body) when run, and
// returns the script's path and the sentinel's. Unlike unreachableCommand,
// this is observable: a regression that spawns it is caught by the
// sentinel's presence, not inferred from "it would fail loudly" (R1-35b).
func spawnSentinelCommand(t *testing.T, dir string) (command, sentinel string) {
	t.Helper()
	sentinel = filepath.Join(dir, "spawned")
	command = filepath.Join(dir, "sentinel.sh")
	script := fmt.Sprintf("#!/bin/sh\ntouch %q\n", sentinel)
	if err := os.WriteFile(command, []byte(script), 0o755); err != nil {
		t.Fatalf("write sentinel script: %v", err)
	}
	return command, sentinel
}

// TestToolsListShowsMcpToolsFromCache pins the load-bearing half of this
// phase: a configured server with a warm cache entry contributes its tools
// to the listing, with no process started.
func TestToolsListShowsMcpToolsFromCache(t *testing.T) {
	configDir, cacheDir := t.TempDir(), t.TempDir()
	t.Setenv("CLAI_CONFIG_DIR", configDir)
	t.Setenv("CLAI_CACHE_DIR", cacheDir)

	command, sentinel := spawnSentinelCommand(t, t.TempDir())
	server := writeMcpConfig(t, configDir, "fs", command, "")
	captureListingCacheEntry(t, cacheDir, server, []cachedRemoteTool{
		{Name: "unrelated_tool", Description: "does something unrelated"},
	})

	WithTestRegistry(t, func() {
		var err error
		got := captureSubCmdOut(t, func() { err = List() })
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if !strings.Contains(got, "- mcp_fs_unrelated_tool: ") {
			t.Fatalf("expected cached mcp tool in listing, got:\n%s", got)
		}
		if _, statErr := os.Stat(sentinel); statErr == nil {
			t.Fatalf("listing spawned the configured server: sentinel %q was touched", sentinel)
		}
		if !strings.Contains(got, "does something unrelated") {
			t.Fatalf("expected cached mcp tool's description in listing, got:\n%s", got)
		}
	})
}

// TestToolsListOmitsServersWithoutCacheEntry pins D21, both ways a server
// can have nothing a prior successful handshake left behind: never run at
// all, and a corrupt entry, which the schema cache already treats as a
// miss (schemacache.Lookup) — the listing must surface that as the same
// plain omission, not an error.
func TestToolsListOmitsServersWithoutCacheEntry(t *testing.T) {
	configDir, cacheDir := t.TempDir(), t.TempDir()
	t.Setenv("CLAI_CONFIG_DIR", configDir)
	t.Setenv("CLAI_CACHE_DIR", cacheDir)

	cached := writeMcpConfig(t, configDir, "cached", unreachableCommand, "")
	writeMcpConfig(t, configDir, "nevercached", unreachableCommand+"-2", "")
	corrupt := writeMcpConfig(t, configDir, "corrupt", unreachableCommand+"-3", "")
	captureListingCacheEntry(t, cacheDir, cached, []cachedRemoteTool{
		{Name: "a_tool", Description: "a tool"},
	})

	// A corrupt entry: present on disk, at the identity's own key, but not
	// valid JSON.
	identity := schemacache.BuildIdentityWithScopes(corrupt)
	key, err := identity.Key()
	if err != nil {
		t.Fatalf("Key: %v", err)
	}
	schemaDir := filepath.Join(cacheDir, schemacache.DefaultDirName)
	if err := os.MkdirAll(schemaDir, 0o755); err != nil {
		t.Fatalf("mkdir schema cache dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(schemaDir, key+".json"), []byte("{not valid json"), 0o644); err != nil {
		t.Fatalf("write corrupt entry: %v", err)
	}

	WithTestRegistry(t, func() {
		var err error
		got := captureSubCmdOut(t, func() { err = List() })
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if !strings.Contains(got, "mcp_cached_a_tool") {
			t.Fatalf("expected the cached server's tool in listing, got:\n%s", got)
		}
		if strings.Contains(got, "nevercached") {
			t.Fatalf("expected the never-cached server to be omitted entirely, got:\n%s", got)
		}
		if strings.Contains(got, "corrupt") {
			t.Fatalf("expected the corrupt-entry server to be omitted entirely, got:\n%s", got)
		}
	})
}

// TestToolsListNeverWritesMcpIntoGlobalRegistry pins the per-run registry
// rule: the cache-only listing source lists into its own set and never
// mutates the process-global Registry.
func TestToolsListNeverWritesMcpIntoGlobalRegistry(t *testing.T) {
	configDir, cacheDir := t.TempDir(), t.TempDir()
	t.Setenv("CLAI_CONFIG_DIR", configDir)
	t.Setenv("CLAI_CACHE_DIR", cacheDir)

	server := writeMcpConfig(t, configDir, "fs", unreachableCommand, "")
	captureListingCacheEntry(t, cacheDir, server, []cachedRemoteTool{
		{Name: "a_tool", Description: "a tool"},
	})

	WithTestRegistry(t, func() {
		Registry.Set("cat", &mockLLMTool{spec: pub_models.Specification{Name: "cat"}})
		before := len(Registry.All())

		var err error
		got := captureSubCmdOut(t, func() { err = List() })
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if !strings.Contains(got, "mcp_fs_a_tool") {
			t.Fatalf("expected the cached tool to appear in the listing, got:\n%s", got)
		}

		after := Registry.All()
		if len(after) != before {
			t.Fatalf("global Registry size changed from %d to %d after List()", before, len(after))
		}
		for name := range after {
			if strings.HasPrefix(name, "mcp_") {
				t.Fatalf("global Registry gained an mcp tool %q", name)
			}
		}
	})
}

// TestToolsListNeverEmitsPerToolShadowMarker pins the sign-off review's
// correction: the per-tool "[shadowed by built-in: ...]" marker is gone.
// A declared, available built-in instead contributes to List's single
// footer line (TestShadowFooterNamesAvailableBuiltin), and an undeclared
// tool stays unmarked either way, with no other part of its listing line
// disturbed.
func TestToolsListNeverEmitsPerToolShadowMarker(t *testing.T) {
	configDir, cacheDir := t.TempDir(), t.TempDir()
	t.Setenv("CLAI_CONFIG_DIR", configDir)
	t.Setenv("CLAI_CACHE_DIR", cacheDir)

	server := writeMcpConfig(t, configDir, "fs", unreachableCommand, "")
	captureListingCacheEntry(t, cacheDir, server, []cachedRemoteTool{
		{Name: "read_file", Description: "Read a file"},
		{Name: "an_unmapped_tool", Description: "no built-in covers this"},
	})

	WithTestRegistry(t, func() {
		Registry.Set("cat", &mockLLMTool{spec: pub_models.Specification{Name: "cat"}})

		var err error
		got := captureSubCmdOut(t, func() { err = List() })
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if !strings.Contains(got, "- mcp_fs_read_file: Read a file\n") {
			t.Fatalf("expected read_file present with no per-tool marker, got:\n%s", got)
		}
		if !strings.Contains(got, "- mcp_fs_an_unmapped_tool: no built-in covers this\n") {
			t.Fatalf("expected the unmapped tool present and unmarked, got:\n%s", got)
		}
	})
}

// TestShadowFooterNamesAvailableBuiltin pins the footer's positive case: a
// declared mapping whose built-in is registered on this host is named in
// the single footer line, not on the tool's own listing line.
func TestShadowFooterNamesAvailableBuiltin(t *testing.T) {
	listSingleCachedToolWithBuiltinRegistered(t, "", "read_file", "Read a file", "cat", func(t *testing.T, got string) {
		if !strings.Contains(got, "- mcp_fs_read_file: Read a file\n") {
			t.Fatalf("expected read_file present with no per-tool marker, got:\n%s", got)
		}
		if !strings.Contains(got, "Note:") || !strings.Contains(got, "cat") {
			t.Fatalf("expected a footer naming cat, got:\n%s", got)
		}
	})
}

// TestShadowFooterOmitsUnregisteredBuiltin pins the error-coverage row: a
// declared mapping whose built-in is not registered on this host (its
// executable absent, exactly as registerLocalTools already gates it) is
// left out of the footer rather than naming an unavailable built-in.
func TestShadowFooterOmitsUnregisteredBuiltin(t *testing.T) {
	configDir, cacheDir := t.TempDir(), t.TempDir()
	t.Setenv("CLAI_CONFIG_DIR", configDir)
	t.Setenv("CLAI_CACHE_DIR", cacheDir)

	server := writeMcpConfig(t, configDir, "fs", unreachableCommand, "")
	captureListingCacheEntry(t, cacheDir, server, []cachedRemoteTool{
		{Name: "read_file", Description: "Read a file"},
	})

	WithTestRegistry(t, func() {
		// "cat" is deliberately never registered in this test's registry.
		var err error
		got := captureSubCmdOut(t, func() { err = List() })
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if !strings.Contains(got, "- mcp_fs_read_file: Read a file\n") {
			t.Fatalf("expected read_file present and unmarked, got:\n%s", got)
		}
		if strings.Contains(got, "Note:") || strings.Contains(got, "cat") {
			t.Fatalf("expected no footer naming an unavailable built-in, got:\n%s", got)
		}
	})
}

// TestShadowFooterDeduplicatesAcrossServers pins the error-coverage row:
// two servers exposing a tool that maps to the same built-in contribute
// that built-in to the footer exactly once, not once per server.
func TestShadowFooterDeduplicatesAcrossServers(t *testing.T) {
	configDir, cacheDir := t.TempDir(), t.TempDir()
	t.Setenv("CLAI_CONFIG_DIR", configDir)
	t.Setenv("CLAI_CACHE_DIR", cacheDir)

	fs1 := writeMcpConfig(t, configDir, "fs1", unreachableCommand+"-1", "")
	fs2 := writeMcpConfig(t, configDir, "fs2", unreachableCommand+"-2", "")
	captureListingCacheEntry(t, cacheDir, fs1, []cachedRemoteTool{{Name: "read_file", Description: "Read a file"}})
	captureListingCacheEntry(t, cacheDir, fs2, []cachedRemoteTool{{Name: "read_file", Description: "Read a file"}})

	WithTestRegistry(t, func() {
		Registry.Set("cat", &mockLLMTool{spec: pub_models.Specification{Name: "cat"}})

		var err error
		got := captureSubCmdOut(t, func() { err = List() })
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		for _, want := range []string{
			"- mcp_fs1_read_file: Read a file\n",
			"- mcp_fs2_read_file: Read a file\n",
		} {
			if !strings.Contains(got, want) {
				t.Fatalf("expected %q in listing, got:\n%s", want, got)
			}
		}
		footerLines := 0
		for line := range strings.SplitSeq(got, "\n") {
			if strings.HasPrefix(line, "Note:") {
				footerLines++
				if n := strings.Count(line, "cat"); n != 1 {
					t.Fatalf("expected the footer to name cat exactly once (deduplicated across both servers), got %d occurrences in footer %q", n, line)
				}
			}
		}
		if footerLines != 1 {
			t.Fatalf("expected exactly one footer line, got %d in:\n%s", footerLines, got)
		}
	})
}

// TestShadowFooterCoversLazyCachedServer pins the invariant naming a
// lazily unconnected server explicitly: a server configured "startup":
// "lazy" contributes to the footer from its cached schema exactly like an
// eager one, since the listing never looks at startup at all.
func TestShadowFooterCoversLazyCachedServer(t *testing.T) {
	listSingleCachedToolWithBuiltinRegistered(t, `,"startup":"lazy"`, "write_file", "Write a file", "write_file", func(t *testing.T, got string) {
		if !strings.Contains(got, "- mcp_fs_write_file: Write a file\n") {
			t.Fatalf("expected the lazy server's tool listed with no per-tool marker, got:\n%s", got)
		}
		if !strings.Contains(got, "Note:") || !strings.Contains(got, "write_file") {
			t.Fatalf("expected the footer to name write_file, got:\n%s", got)
		}
	})
}

// listSingleCachedToolWithBuiltinRegistered configures one command-based
// server with one cached tool, registers builtinName, runs the real
// List(), and hands the output to assert. Extracted once dupl flagged three
// of this file's footer tests (TestShadowFooterNamesAvailableBuiltin,
// TestShadowFooterCoversLazyCachedServer, TestShadowFooterCoversCreateDirectory)
// as a clone group: each configures exactly one server with exactly one
// cached tool and differs only in the literal names and the assertion.
func listSingleCachedToolWithBuiltinRegistered(t *testing.T, serverExtraJSON, toolName, toolDescription, builtinName string, assert func(t *testing.T, got string)) {
	t.Helper()
	configDir, cacheDir := t.TempDir(), t.TempDir()
	t.Setenv("CLAI_CONFIG_DIR", configDir)
	t.Setenv("CLAI_CACHE_DIR", cacheDir)

	server := writeMcpConfig(t, configDir, "fs", unreachableCommand, serverExtraJSON)
	captureListingCacheEntry(t, cacheDir, server, []cachedRemoteTool{
		{Name: toolName, Description: toolDescription},
	})
	listWithBuiltinRegistered(t, builtinName, assert)
}

// listWithBuiltinRegistered registers builtinName in a fresh test registry,
// runs the real List(), and hands the output to assert — the shape shared
// by TestShadowFooterNeverAttributesToASpecificRemoteTool and
// TestShadowFooterSkipsEndpointBasedServers, extracted once dupl flagged
// them as a clone pair.
func listWithBuiltinRegistered(t *testing.T, builtinName string, assert func(t *testing.T, got string)) {
	t.Helper()
	WithTestRegistry(t, func() {
		Registry.Set(builtinName, &mockLLMTool{spec: pub_models.Specification{Name: builtinName}})
		var err error
		got := captureSubCmdOut(t, func() { err = List() })
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		assert(t, got)
	})
}

// TestShadowFooterNeverAttributesToASpecificRemoteTool is the direct
// regression test for the sign-off review's finding: the marker used to
// gate on transport (command-based vs. url-based), not locality, so a
// command-based server that is itself a remote bridge — the documented
// "npx -y mcp-remote https://..." shape this package's own schema cache
// comments name — had its remote tools marked against a bare name match,
// e.g. a Notion page read marked "[shadowed by built-in: cat]", which cat
// cannot do. The footer carries the same information with no per-tool
// claim: this test pins that the listing line for such a tool never gets a
// marker, regardless of what the footer separately says.
func TestShadowFooterNeverAttributesToASpecificRemoteTool(t *testing.T) {
	configDir, cacheDir := t.TempDir(), t.TempDir()
	t.Setenv("CLAI_CONFIG_DIR", configDir)
	t.Setenv("CLAI_CACHE_DIR", cacheDir)

	// writeMcpConfig's own returned McpServer never carries Args, so it is
	// not used here: Args is part of the identity (ArgsDigest), and this
	// test's whole point depends on the cache being warmed under the exact
	// identity the listing's own config parser will compute from the file.
	serversDir := filepath.Join(configDir, "mcpServers")
	if err := os.MkdirAll(serversDir, 0o755); err != nil {
		t.Fatalf("mkdir mcpServers: %v", err)
	}
	cfg := `{"command":"npx","args":["-y","mcp-remote","https://mcp.notion.com/mcp"]}`
	if err := os.WriteFile(filepath.Join(serversDir, "notion.json"), []byte(cfg), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	server := pub_models.McpServer{Name: "notion", Command: "npx", Args: []string{"-y", "mcp-remote", "https://mcp.notion.com/mcp"}}
	captureListingCacheEntry(t, cacheDir, server, []cachedRemoteTool{
		{Name: "read_file", Description: "Read a Notion page"},
	})

	listWithBuiltinRegistered(t, "cat", func(t *testing.T, got string) {
		if !strings.Contains(got, "- mcp_notion_read_file: Read a Notion page\n") {
			t.Fatalf("expected the remote-bridge tool present with no per-tool marker naming cat, got:\n%s", got)
		}
		if strings.Contains(got, "mcp_notion_read_file: Read a Notion page [shadowed") {
			t.Fatalf("a remote bridge's tool was attributed a per-tool shadow claim it cannot back up, got:\n%s", got)
		}
	})
}

// TestShadowAdvisoryHandlesNoMcpServers pins that the listing is correct
// with an existing, empty mcpServers directory: built-ins render, no MCP
// entry appears, and no error occurs. The directory is created so the
// listing actually reaches filepath.Glob's zero-match branch rather than
// the os.Stat-missing branch TestShadowAdvisoryAbsentConfigDirIsNotAnError
// already pins (R1-35a: both tests used to hit the same early return).
func TestShadowAdvisoryHandlesNoMcpServers(t *testing.T) {
	configDir, cacheDir := t.TempDir(), t.TempDir()
	t.Setenv("CLAI_CONFIG_DIR", configDir)
	t.Setenv("CLAI_CACHE_DIR", cacheDir)
	if err := os.MkdirAll(filepath.Join(configDir, "mcpServers"), 0o755); err != nil {
		t.Fatalf("mkdir mcpServers: %v", err)
	}

	WithTestRegistry(t, func() {
		Registry.Set("cat", &mockLLMTool{spec: pub_models.Specification{Name: "cat", Description: "concatenate"}})

		var err error
		got := captureSubCmdOut(t, func() { err = List() })
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if !strings.Contains(got, "- cat: concatenate") {
			t.Fatalf("expected the built-in still listed, got:\n%s", got)
		}
		if strings.Contains(got, "mcp_") {
			t.Fatalf("expected no mcp entry with no servers configured, got:\n%s", got)
		}
	})
}

// TestShadowAdvisoryAbsentConfigDirIsNotAnError pins the other half: a
// config directory that does not exist at all, distinct from an existing
// empty one, is still not an error.
func TestShadowAdvisoryAbsentConfigDirIsNotAnError(t *testing.T) {
	cacheDir := t.TempDir()
	t.Setenv("CLAI_CONFIG_DIR", filepath.Join(t.TempDir(), "does-not-exist"))
	t.Setenv("CLAI_CACHE_DIR", cacheDir)

	WithTestRegistry(t, func() {
		var err error
		got := captureSubCmdOut(t, func() { err = List() })
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if strings.Contains(got, "mcp_") {
			t.Fatalf("expected no mcp entry with an absent config dir, got:\n%s", got)
		}
	})
}

// TestShadowAdvisoryDoesNotAlterSelection pins the invariant that the
// shadow advisory changes nothing about tool selection, registration or
// the specification sent to a model. It drives both halves of the real
// listing path over one cached, shadowed entry: List(), where the advisory
// is now a footer rather than text on the tool's own line, and Detail(),
// whose JSON specification is what a run's tool selection actually reads —
// the cached description must survive there unmodified and footer-free.
//
// R2-17: the prior version asserted this by driving mcp.RegisterTools
// directly, which has no reference to shadowingBuiltin or the shadow map
// at all, so its "no marker in spec.Description" check could not fail for
// the reason it claimed to pin; that registration path is already proven
// by internal/tools/mcp/tool_test.go. This version also folds in what
// TestToolsDetailShowsMcpToolFromCache proved on its own, since asserting
// both halves together is what makes the test able to fail for the right
// reason.
func TestShadowAdvisoryDoesNotAlterSelection(t *testing.T) {
	configDir, cacheDir := t.TempDir(), t.TempDir()
	t.Setenv("CLAI_CONFIG_DIR", configDir)
	t.Setenv("CLAI_CACHE_DIR", cacheDir)

	server := writeMcpConfig(t, configDir, "fs", unreachableCommand, "")
	captureListingCacheEntry(t, cacheDir, server, []cachedRemoteTool{
		{Name: "read_file", Description: "Read a file"},
	})

	WithTestRegistry(t, func() {
		Registry.Set("cat", &mockLLMTool{spec: pub_models.Specification{Name: "cat"}})

		var listErr error
		listing := captureSubCmdOut(t, func() { listErr = List() })
		if listErr != nil {
			t.Fatalf("List: %v", listErr)
		}
		if !strings.Contains(listing, "- mcp_fs_read_file: Read a file\n") {
			t.Fatalf("expected the listing line present with no per-tool marker, got:\n%s", listing)
		}
		if !strings.Contains(listing, "Note:") || !strings.Contains(listing, "cat") {
			t.Fatalf("expected the footer to name cat, got:\n%s", listing)
		}

		var detailErr error
		detailOut := captureSubCmdOut(t, func() { detailErr = Detail("mcp_fs_read_file") })
		if detailErr != nil {
			t.Fatalf("Detail: %v", detailErr)
		}
		var spec pub_models.Specification
		if err := json.Unmarshal([]byte(detailOut), &spec); err != nil {
			t.Fatalf("unmarshal Detail output: %v", err)
		}
		if spec.Name != "mcp_fs_read_file" {
			t.Fatalf("spec.Name = %q, want %q", spec.Name, "mcp_fs_read_file")
		}
		if spec.Description != "Read a file" {
			t.Fatalf("spec.Description = %q, want the cached description unmodified by the marker", spec.Description)
		}
		if strings.Contains(detailOut, "shadowed") {
			t.Fatalf("Detail output carries a marker: %s", detailOut)
		}
	})
}

// TestShadowFooterSkipsEndpointBasedServers pins R1-22/R2-17's correction,
// still true under the footer design: the footer is restricted to
// command-based servers, since a remote server's "read_file" may read a
// file on the remote host, a capability local "cat" cannot replace. An
// endpoint-based server exposing the same remote tool name a command-based
// server would contribute to the footer must not contribute to it either.
func TestShadowFooterSkipsEndpointBasedServers(t *testing.T) {
	configDir, cacheDir := t.TempDir(), t.TempDir()
	t.Setenv("CLAI_CONFIG_DIR", configDir)
	t.Setenv("CLAI_CACHE_DIR", cacheDir)

	server := writeMcpHttpConfig(t, configDir, "remotefs", "https://mcp.example.com/mcp")
	captureListingCacheEntry(t, cacheDir, server, []cachedRemoteTool{
		{Name: "read_file", Description: "Read a file on the remote host"},
	})

	listWithBuiltinRegistered(t, "cat", func(t *testing.T, got string) {
		if !strings.Contains(got, "- mcp_remotefs_read_file: Read a file on the remote host\n") {
			t.Fatalf("expected the endpoint-based tool present and unmarked, got:\n%s", got)
		}
		if strings.Contains(got, "Note:") {
			t.Fatalf("expected no footer: the only hit is endpoint-based, which never contributes, got:\n%s", got)
		}
	})
}

// TestShadowFooterCoversCreateDirectory pins R1-23/R2-23: create_directory
// maps to the registered native built-in mkdir. The map previously left it
// unmapped on the mistaken premise that ToolName had no mkdir constant;
// ToolName is a defined string type and mkdir is a real, unconditionally
// registered native tool (internal/tools/handler.go's nativeTools).
func TestShadowFooterCoversCreateDirectory(t *testing.T) {
	listSingleCachedToolWithBuiltinRegistered(t, "", "create_directory", "Create a directory", "mkdir", func(t *testing.T, got string) {
		if !strings.Contains(got, "- mcp_fs_create_directory: Create a directory\n") {
			t.Fatalf("expected create_directory present with no per-tool marker, got:\n%s", got)
		}
		if !strings.Contains(got, "Note:") || !strings.Contains(got, "mkdir") {
			t.Fatalf("expected the footer to name mkdir, got:\n%s", got)
		}
	})
}

// TestBuiltinShadowMapExcludesGetFileInfo pins the sign-off review's
// correction to the mapping itself: the MCP filesystem server's
// get_file_info returns size, modification time and permissions, a
// stat(1)-shaped answer, while FileTypeTool wraps file(1), which answers a
// different question (a file's content type). The two were never
// equivalent, so the entry is removed rather than repointed: no built-in
// here answers what get_file_info actually answers.
func TestBuiltinShadowMapExcludesGetFileInfo(t *testing.T) {
	if _, ok := shadowingBuiltin("get_file_info"); ok {
		t.Fatal("get_file_info is still mapped to a built-in; file_type (file(1)) answers a different question than get_file_info (size/mtime/permissions)")
	}
}
