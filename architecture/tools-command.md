# Tools Command Architecture

Command: `clai [flags] tools [tool name]` (aliases: `t`)

The **tools** command is an *inspection/UI* command. It does **not** enable tools for a query; it lists what tools are available to the runtime (built-ins registered in the local registry) and can print the JSON schema/spec for one tool.

> Related flag: `-t/-tools` (string) on `query`/`chat` controls *which tools the LLM may call* during that run. See `QUERY.md` and `CONFIG.md`.

## Entry Flow

```text
main.go:run()
  → cmd.Run(...)                  # go_away_boilerplate/pkg/cmd dispatch
    → tools command Run (internal/tools/cmd.go)
      → tools.Init()
      → tools.List() or tools.Detail(name)
```

## Key Files

| File | Purpose |
|------|---------|
| `internal/tools/cmd.go` | tools command + `list` sub; calls `tools.Init()` then `List`/`Detail` (see [cmd-dispatch.md](./cmd-dispatch.md)) |
| `internal/tools/init.go` (and friends) | Initializes the tool registry (built-in tools + MCP tools, if configured) |
| `internal/tools/cmd.go` | Implements `clai tools` CLI behavior |
| `internal/tools/registry.go` | Tool registry: `Get`, `All`, wildcard selection |
| `pkg/text/models/tool.go` (or similar) | Public tool spec types serialized to JSON |

## Behavior

### `clai tools`

`internal/tools/cmd.go` (`List`/`Detail`):

1. Loads all registered tools via `Registry.All()`.
2. Loads the alias map via `Registry.Aliases()` and removes alias names from
   the listing.
3. Sorts the remaining (canonical) tool names.
4. Prints a human readable list:

   - one entry per canonical tool; a tool's aliases are annotated on its
     canonical row (`- async_cmd (alias: async_cmd_run): ...`) instead of
     being listed as duplicate entries
   - attempts to fit descriptions to the width of the session's output writer
     via `utils.SessionDimensions(os.Stdout)` and the explicit-width helper
     `table.WidthAppropriateStringTruncWithWidth`.

5. Prints an instruction footer:

   ```text
   Run 'clai tools <tool-name>' for more details.
   ```

Returns nil so the process exits with code 0.

### `clai tools <tool-name>`

If a second CLI arg exists (`args[1]`), it is interpreted as the tool name:

1. Looks up the tool in the registry: `Registry.Get(toolName)`.
2. An alias name resolves to the same tool instance, so `clai tools
   async_cmd_run` prints the canonical `async_cmd` specification.
3. If missing: returns an error (`tool '<name>' not found`).
4. If present: marshals the tool `Specification()` as pretty JSON and prints it.

Also returns `utils.ErrUserInitiatedExit`.

## Registry and Init

`tools.Init()` must be called before listing tools.

Conceptually, Init is responsible for:

- registering built-in tools (filesystem, `go test`, `rg`, etc.)
- reading MCP server configs under `<clai-config>/mcpServers/*.json` and adding `mcp_...` tools (via an MCP client integration)

### Aliases

The registry supports tool aliases via `SetAlias(alias, canonical, tool)`: the
alias is registered under its own name (so `Get`, `WildcardGet`, and `-t`
selection keep working) and a separate alias → canonical map is recorded.
`Registry.Aliases()` returns that map; the `clai tools` listing uses it to
group aliases under their canonical tool and to make `clai tools <alias>`
resolve to the canonical specification. Current aliases: `freetext_command`
→ `cmd`, and `async_cmd_run` → `async_cmd`.

The CLI *selection* logic for `-t/-tools` lives in `internal/setup.go:setupToolConfig()`:

- `-t=*` ⇒ clear `RequestedToolGlobs` ⇒ interpreted as “allow all tools”.
- `-t=a,b,c` ⇒ validate each name:
  - built-ins must exist in the registry (wildcards supported)
  - MCP tools are accepted if prefixed with `mcp_`
- if no valid tools are selected, tooling is disabled for that run.

## Error handling and exit codes

- Listing tools is considered a user-driven info command: it returns `utils.ErrUserInitiatedExit`.
- Unknown tool name is a real error from `tools.Detail` and propagates to `cmd.Run` => non-zero exit.
