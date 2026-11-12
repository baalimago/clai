package tools

import (
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/baalimago/clai/internal"
	"github.com/baalimago/clai/internal/diag"
	"github.com/baalimago/clai/internal/runenv"
	"github.com/baalimago/clai/internal/tools/mcp/schemacache"
	"github.com/baalimago/clai/internal/tools/mcp/serverconfig"
	"github.com/baalimago/clai/internal/utils"
	pub_models "github.com/baalimago/clai/pkg/text/models"
	"github.com/baalimago/go_away_boilerplate/pkg/cmd"
	"github.com/baalimago/go_away_boilerplate/pkg/table"
)

// Detail prints the specification of one tool, a built-in or an MCP tool
// that the schema cache has a record of. A visible built-in the listing names
// but this machine cannot load is still describable, so its catalog spec
// answers.
// Under -json it is one JSON document on stdout, not the record stream.
func Detail(ctx context.Context, toolName string) error {
	var spec pub_models.Specification
	if tool, exists := Registry.Get(toolName); exists {
		spec = tool.Specification()
	} else if tool, exists := builtinByName(toolName); exists {
		spec = tool.Specification()
	} else if entry, exists := findMcpListingEntry(toolName); exists {
		spec = entry.spec
	} else {
		return fmt.Errorf("tool '%s' not found", toolName)
	}
	env := runenv.From(ctx)
	if env.JSON() {
		return env.WriteJSONDocument(spec)
	}
	enc := jsontext.NewEncoder(env.Stdout(), jsontext.WithIndent("  "))
	if err := json.MarshalEncode(enc, spec, json.Deterministic(true)); err != nil {
		return diag.Errorf("failed to marshal tool specification: %w", err)
	}
	return nil
}

// unavailableMark names the program a tool needs that this machine lacks. The
// text listing appends it to the tool's own line and the footer explains it.
func unavailableMark(entry toolListingEntry) string {
	if entry.available {
		return ""
	}
	return " [needs " + entry.dependency + "]"
}

// shadowFooterPrefix opens the shadow advisory's footer line. The listing
// carries a second note, about tools this machine cannot load, so a test that
// asks whether the shadow advisory fired matches on this prefix.
const shadowFooterPrefix = "Note: a configured MCP server's tool"

// List prints every available tool with its aliases and description: every
// built-in clai ships, plus every MCP tool the schema cache has a record of for a
// configured server (phase 7, D21). A footer names any built-in that may
// cover a capability also offered by a configured, command-based MCP
// server, without naming which tool or server (sign-off review, 2026-10-03:
// a per-tool marker keyed on the bare remote tool name was wrong whenever a
// command-based server was itself a remote bridge — e.g. "npx -y mcp-remote
// https://...", a shape this package's own schema cache comments document —
// since a bare-name match there says nothing about the remote capability
// behind it. A footer carries the same "check whether you need this"
// information with no claim about any specific listed tool to be wrong
// about). A built-in this machine cannot load is still listed, marked with
// the program it needs; a second footer explains that mark.
func List(ctx context.Context) error {
	env := runenv.From(ctx)
	listing := buildListing()
	if env.JSON() {
		return env.WriteJSONDocument(listing.json())
	}
	out := env.Stdout()
	fmt.Fprintf(out, "Available Tools:\n")
	unloadable := 0
	for _, entry := range listing.entries {
		aliasSuffix := ""
		if len(entry.aliases) > 0 {
			aliasSuffix = " (alias: " + strings.Join(entry.aliases, ", ") + ")"
		}
		mark := unavailableMark(entry)
		if mark != "" {
			unloadable++
		}
		prefix := fmt.Sprintf("- %s%s%s: ", entry.name, aliasSuffix, mark)
		// The listing writes to stdout, so it resolves one snapshot bound to
		// stdout's fd (R2-02): a non-terminal stdout yields the deterministic
		// fallback width.
		line := table.WidthAppropriateStringTruncWithWidth(entry.spec.Description, prefix, 5, utils.SessionDimensions(out).Width)
		fmt.Fprintln(out, line)
	}
	if len(listing.shadowed) > 0 {
		fmt.Fprintf(out, "\n%s may already be covered by a local built-in: %s. Check each server's own tools before assuming redundancy.\n", shadowFooterPrefix, strings.Join(listing.shadowed, ", "))
	}
	if unloadable > 0 {
		fmt.Fprintf(out, "\nNote: tools marked [needs <program>] are built in, but this machine's PATH has no such program, so they cannot load here.\n")
	}
	fmt.Fprintln(out, "\nRun 'clai tools <tool-name>' for more details.")
	return nil
}

// toolJSON is one tool of the listing's -json document: the full
// specification, with the aliases that resolve to it, the program it needs to
// load and whether this machine has that program.
type toolJSON struct {
	pub_models.Specification
	Aliases []string `json:"aliases"`
	// Dependency is the program the tool runs. It is empty for a tool that
	// runs in-process, and for an MCP tool, whose server is not a PATH
	// program, so every entry carries the field.
	Dependency string `json:"dependency"`
	Available  bool   `json:"available"`
}

// toolListing is one listing: the entries in display order and the built-ins
// the shadow advisory names.
type toolListing struct {
	entries  []toolListingEntry
	shadowed []string
}

type toolListingEntry struct {
	name       string
	spec       pub_models.Specification
	aliases    []string
	dependency string
	available  bool
}

// json returns the listing's -json document: machine-readable data rather
// than the record stream, so a script parses the tools directly.
func (l toolListing) json() []toolJSON {
	out := make([]toolJSON, 0, len(l.entries))
	for _, entry := range l.entries {
		out = append(out, toolJSON{
			Specification: entry.spec,
			Aliases:       entry.aliases,
			Dependency:    entry.dependency,
			Available:     entry.available,
		})
	}
	return out
}

// buildListing collects clai's whole visible built-in catalog, every other
// visible registry entry, and every cached MCP tool, sorted by name, with each
// entry's aliases. A built-in's availability is the registry's own answer: a
// native tool needs no program, so it is always loadable, and every other
// built-in is loadable exactly when registration put it in the registry.
func buildListing() toolListing {
	tls := Registry.Visible()
	aliases := Registry.Aliases()
	mcpEntries, shadowedBuiltins := mcpListingEntries()

	entries := make(map[string]toolListingEntry, len(tls)+len(mcpEntries)+len(builtins()))
	for _, b := range builtins() {
		if b.hidden {
			continue
		}
		spec := b.tool.Specification()
		registered, loadable := tls[spec.Name]
		if loadable {
			spec = registered.Specification()
		}
		entries[spec.Name] = toolListingEntry{
			name:       spec.Name,
			spec:       spec,
			dependency: b.executable,
			available:  loadable || b.executable == "",
		}
	}
	for name, tool := range tls {
		if _, isAlias := aliases[name]; isAlias {
			continue
		}
		if _, known := entries[name]; known {
			continue
		}
		entries[name] = toolListingEntry{name: name, spec: tool.Specification(), available: true}
	}
	for _, e := range mcpEntries {
		entries[e.name] = toolListingEntry{name: e.name, spec: e.spec, available: true}
	}

	names := make([]string, 0, len(entries))
	for name := range entries {
		names = append(names, name)
	}
	sort.Strings(names)
	listing := toolListing{shadowed: shadowedBuiltins, entries: make([]toolListingEntry, 0, len(names))}
	for _, name := range names {
		entry := entries[name]
		entry.aliases = []string{}
		for alias, canonical := range aliases {
			if canonical == name {
				entry.aliases = append(entry.aliases, alias)
			}
		}
		sort.Strings(entry.aliases)
		listing.entries = append(listing.entries, entry)
	}
	return listing
}

// Command builds the tools command tree.
func Command() *internal.Command {
	nonInteractive := &internal.NonInteractiveFlag{}
	c := &internal.Command{
		Name: "tools",
		Desc: "List available tools, or show details for a specific tool",
		HelpText: "tools [tool name]. Lists built-in tools and any MCP tool cached from a prior successful run, or one tool's specification." + `

Every built-in clai ships is listed on every machine. One this machine
cannot load is marked [needs <program>]: the program it runs is not in
PATH here, so a run on this machine cannot offer it.

With -json the result is one JSON document of full tool data, not the usual
record stream: an array of specifications for the listing, one object for a
tool. So a script parses the tools directly. Each listing entry carries
'dependency', the program the tool runs (empty for an in-process tool or an
MCP tool), and 'available', whether this machine has that program.`,
		Register:       nonInteractive.Register,
		NonInteractive: nonInteractive,
		CompleteArgsFn: toolNameArgs,
	}
	c.OnRun = func(ctx context.Context, c *internal.Command) error {
		Init(ctx)
		if args := c.Args(); len(args) > 1 {
			return Detail(ctx, args[1])
		}
		return List(ctx)
	}
	toolsList := &internal.Command{
		Name: "list",
		Desc: "List available tools, both mcp and built-in",
		HelpText: `tools list. Lists every available tool.

Examples:
  clai tools list`,
	}
	toolsList.OnRun = func(ctx context.Context, _ *internal.Command) error {
		Init(ctx)
		return List(ctx)
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
