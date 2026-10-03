package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/baalimago/clai/internal"
	"github.com/baalimago/clai/internal/tools/mcp/schemacache"
	"github.com/baalimago/clai/internal/tools/mcp/serverconfig"
	"github.com/baalimago/clai/internal/utils"
	pub_models "github.com/baalimago/clai/pkg/text/models"
	"github.com/baalimago/go_away_boilerplate/pkg/cmd"
	"github.com/baalimago/go_away_boilerplate/pkg/table"
)

// Detail prints the specification of one tool, a built-in or an MCP tool
// that the schema cache has a record of.
func Detail(toolName string) error {
	var spec pub_models.Specification
	if tool, exists := Registry.Get(toolName); exists {
		spec = tool.Specification()
	} else if entry, exists := findMcpListingEntry(toolName); exists {
		spec = entry.spec
	} else {
		return fmt.Errorf("tool '%s' not found", toolName)
	}
	jsonSpec, err := json.MarshalIndent(spec, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal tool specification: %w", err)
	}
	fmt.Printf("%s\n", string(jsonSpec))
	return nil
}

// List prints every available tool with its aliases and description: every
// built-in, plus every MCP tool the schema cache has a record of for a
// configured server (phase 7, D21). A footer names any built-in that may
// cover a capability also offered by a configured, command-based MCP
// server, without naming which tool or server (sign-off review, 2026-10-03:
// a per-tool marker keyed on the bare remote tool name was wrong whenever a
// command-based server was itself a remote bridge — e.g. "npx -y mcp-remote
// https://...", a shape this package's own schema cache comments document —
// since a bare-name match there says nothing about the remote capability
// behind it. A footer carries the same "check whether you need this"
// information with no claim about any specific listed tool to be wrong
// about).
func List() error {
	tls := Registry.All()
	aliases := Registry.Aliases()
	mcpEntries, shadowedBuiltins := mcpListingEntries()
	mcpByName := make(map[string]mcpListingEntry, len(mcpEntries))

	var names []string
	for k := range tls {
		if _, isAlias := aliases[k]; !isAlias {
			names = append(names, k)
		}
	}
	for _, e := range mcpEntries {
		mcpByName[e.name] = e
		names = append(names, e.name)
	}
	sort.Strings(names)

	fmt.Printf("Available Tools:\n")
	for _, name := range names {
		var spec pub_models.Specification
		aliasSuffix := ""
		if e, ok := mcpByName[name]; ok {
			spec = e.spec
		} else {
			spec = tls[name].Specification()
			var aliasesOfTool []string
			for alias, canonical := range aliases {
				if canonical == name {
					aliasesOfTool = append(aliasesOfTool, alias)
				}
			}
			if len(aliasesOfTool) > 0 {
				sort.Strings(aliasesOfTool)
				aliasSuffix = " (alias: " + strings.Join(aliasesOfTool, ", ") + ")"
			}
		}
		prefix := fmt.Sprintf("- %s%s: ", name, aliasSuffix)
		// The listing writes to stdout, so it resolves one snapshot bound to
		// stdout's fd (R2-02): a non-terminal stdout yields the deterministic
		// fallback width.
		line := table.WidthAppropriateStringTruncWithWidth(spec.Description, prefix, 5, utils.SessionDimensions(os.Stdout).Width)
		fmt.Println(line)
	}
	if len(shadowedBuiltins) > 0 {
		fmt.Printf("\nNote: a configured MCP server's tool may already be covered by a local built-in: %s. Check each server's own tools before assuming redundancy.\n", strings.Join(shadowedBuiltins, ", "))
	}
	fmt.Println("\nRun 'clai tools <tool-name>' for more details.")
	return nil
}

// Command builds the tools command tree.
func Command() *internal.Command {
	nonInteractive := &internal.NonInteractiveFlag{}
	c := &internal.Command{
		Name:           "tools",
		Desc:           "List available tools, or show details for a specific tool",
		HelpText:       "tools [tool name]. Lists built-in tools and any MCP tool cached from a prior successful run, or one tool's specification.",
		Register:       nonInteractive.Register,
		NonInteractive: nonInteractive,
		CompleteArgsFn: toolNameArgs,
	}
	c.OnRun = func(_ context.Context, c *internal.Command) error {
		Init()
		if args := c.Args(); len(args) > 1 {
			return Detail(args[1])
		}
		return List()
	}
	toolsList := &internal.Command{
		Name: "list",
		Desc: "List available tools, both mcp and built-in",
		HelpText: `tools list. Lists every available tool.

Examples:
  clai tools list`,
	}
	toolsList.OnRun = func(_ context.Context, _ *internal.Command) error {
		Init()
		return List()
	}
	c.Subs = map[string]cmd.Command{"list": toolsList}
	return c
}

// toolNameArgs completes the tools command's detail-view positional.
func toolNameArgs(args []string, partial string) []cmd.CompletionItem {
	if len(args) > 0 {
		return []cmd.CompletionItem{}
	}
	return internal.PlainItems(partial, Names())
}

// mcpListingEntry is one MCP tool discovered from the schema cache for the
// tools listing: its full mcp_<server>_<tool> name (matching what a
// connected run would register it as) and its specification. The shadow
// advisory is never per-entry (sign-off review, 2026-10-03): see List's own
// footer.
type mcpListingEntry struct {
	name string
	spec pub_models.Specification
}

// mcpListingEntries builds the tools listing's cache-only MCP tool set, and
// alongside it the distinct, sorted set of built-in names the footer names
// (sign-off review, 2026-10-03). For each server configured under the clai
// config directory it builds the identity and reads its schema-cache entry,
// exactly as setup does, and lists the tools it finds into a listing-scoped
// set that is never the process-global Registry. It never connects and
// never spawns: a listing is not a run. A server with no cache entry —
// never run, or its last run failed — contributes nothing, and any failure
// to even resolve the config or cache directory degrades to an empty
// result rather than failing the listing (phase 7, D21: a tool listing is
// not a diagnostic surface).
func mcpListingEntries() ([]mcpListingEntry, []string) {
	servers := configuredMcpServersForListing()
	if len(servers) == 0 {
		return nil, nil
	}
	cacheDir, err := utils.GetClaiCacheDir()
	if err != nil {
		return nil, nil
	}
	hits, err := schemacache.ListCachedServers(filepath.Join(cacheDir, schemacache.DefaultDirName), servers)
	if err != nil {
		return nil, nil
	}

	serverByName := make(map[string]pub_models.McpServer, len(servers))
	for _, s := range servers {
		serverByName[s.Name] = s
	}

	shadowed := map[pub_models.ToolName]bool{}
	var entries []mcpListingEntry
	for _, hit := range hits {
		var remoteTools []cachedRemoteTool
		if err := json.Unmarshal(hit.Record.Tools, &remoteTools); err != nil {
			continue
		}
		// The declared mapping names a capability of a local, command-based
		// server (the phase's own Goal section); an endpoint-based server's
		// "read_file" may read a file on the remote host, which local "cat"
		// cannot replace, so a url-based server never contributes to the
		// footer (R1-22, corrected again in R2-17). A command-based server
		// can itself be a remote bridge ("npx -y mcp-remote https://...",
		// which this package's own schema cache comments already document),
		// so even here the match is only ever reported in aggregate, with no
		// claim about this specific server or tool (sign-off review B3's
		// sibling finding).
		commandBased := serverByName[hit.ServerName].Command != ""
		for _, t := range remoteTools {
			t.InputSchema.Patch()
			if !t.InputSchema.IsOk() {
				continue
			}
			name := fmt.Sprintf("mcp_%s_%s", hit.ServerName, t.Name)
			entries = append(entries, mcpListingEntry{
				name: name,
				spec: pub_models.Specification{Name: name, Description: t.Description, Inputs: &t.InputSchema},
			})
			if commandBased {
				if builtin, ok := shadowingBuiltin(t.Name); ok && builtinRegistered(builtin) {
					shadowed[builtin] = true
				}
			}
		}
	}
	var shadowedNames []string
	for name := range shadowed {
		shadowedNames = append(shadowedNames, string(name))
	}
	sort.Strings(shadowedNames)
	return entries, shadowedNames
}

// findMcpListingEntry looks up one MCP tool from the same cache-only set
// List renders, for Detail's fallback when name is not a built-in.
func findMcpListingEntry(name string) (mcpListingEntry, bool) {
	entries, _ := mcpListingEntries()
	for _, e := range entries {
		if e.name == name {
			return e, true
		}
	}
	return mcpListingEntry{}, false
}

// configuredMcpServersForListing reads every MCP server config under the
// clai config directory's mcpServers subdirectory, tolerating an absent
// config directory or an absent mcpServers subdirectory as no servers
// rather than an error: this is a listing, not setup, and must remain
// correct with no MCP server configured at all.
func configuredMcpServersForListing() []pub_models.McpServer {
	configDir, err := utils.GetClaiConfigDir()
	if err != nil {
		return nil
	}
	mcpServersDir := filepath.Join(configDir, "mcpServers")
	if _, err := os.Stat(mcpServersDir); err != nil {
		return nil
	}
	files, err := filepath.Glob(filepath.Join(mcpServersDir, "*.json"))
	if err != nil {
		return nil
	}
	// A per-file parse failure degrades to the partial result rather than
	// an error (D21: the listing is never a diagnostic surface); the
	// discarded error carries only that diagnostic, never a tool.
	servers, _ := serverconfig.FindConfiguredServers(files)
	return servers
}

// cachedRemoteTool decodes one entry of a schema cache record's verbatim
// tools/list "tools" array — the same shape mcp.RegisterTools decodes,
// kept as its own type here rather than importing internal/tools/mcp,
// which would reintroduce the cycle serverconfig's relocation avoids: that
// package's own tests import internal/tools.
type cachedRemoteTool struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description,omitempty"`
	InputSchema pub_models.InputSchema `json:"inputSchema"`
}
