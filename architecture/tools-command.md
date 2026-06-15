# Tools Command Architecture

Command: `clai [flags] tools [tool name]` (aliases: `t`)

The **tools** command is an *inspection/UI* command. It does **not** enable tools for a query; it lists what tools are available to the runtime (built-ins registered in the local registry) and can print the JSON schema/spec for one tool.

> Related flag: `-t/-tools` (string) on `query`/`chat` controls *which tools the LLM may call* during that run. See `QUERY.md` and `CONFIG.md`.

## Entry Flow

```text
main.go:run()
  → internal.Setup(ctx, usage, args)
    → parseFlags()
    → getCmdFromArgs() → TOOLS
    → tools.Init()
    → tools.SubCmd(ctx, allArgs)
```

## Key Files

| File | Purpose |
|------|---------|
| `internal/setup.go` | Dispatches TOOLS mode and calls `tools.Init()` then `tools.SubCmd()` |
| `internal/tools/init.go` (and friends) | Initializes the tool registry (built-in tools + MCP tools, if configured) |
| `internal/tools/cmd.go` | Implements `clai tools` CLI behavior |
| `internal/tools/registry.go` | Tool registry: `Get`, `All`, wildcard selection |
| `pkg/text/models/tool.go` (or similar) | Public tool spec types serialized to JSON |

## Behavior

### `clai tools`

`internal/tools/cmd.go:SubCmd`:

1. Loads all registered tools via `Registry.All()`.
2. Sorts tool names.
3. Prints a human readable list:

   - one entry per tool
   - attempts to fit descriptions to terminal width via `utils.WidthAppropriateStringTrunc`.

4. Prints an instruction footer:

   ```text
   Run 'clai tools <tool-name>' for more details.
   ```

Returns `utils.ErrUserInitiatedExit` so the top-level `main.run()` exits with code 0.

### `clai tools <tool-name>`

If a second CLI arg exists (`args[1]`), it is interpreted as the tool name:

1. Looks up the tool in the registry: `Registry.Get(toolName)`.
2. If missing: returns an error (`tool '<name>' not found`).
3. If present: marshals the tool `Specification()` as pretty JSON and prints it.

Also returns `utils.ErrUserInitiatedExit`.

## Registry and Init

`tools.Init()` must be called before listing tools.

Conceptually, Init is responsible for:

- registering built-in tools (filesystem, `go test`, `rg`, etc.)
- reading MCP server configs under `<clai-config>/mcpServers/*.json` and adding `mcp_...` tools (via an MCP client integration)

The CLI *selection* logic for `-t/-tools` lives in `internal/setup.go:setupToolConfig()`:

- `-t=*` ⇒ clear `RequestedToolGlobs` ⇒ interpreted as “allow all tools”.
- `-t=a,b,c` ⇒ validate each name:
  - built-ins must exist in the registry (wildcards supported)
  - MCP tools are accepted if prefixed with `mcp_`
- if no valid tools are selected, tooling is disabled for that run.

## Error handling and exit codes

- Listing tools is considered a user-driven info command: it returns `utils.ErrUserInitiatedExit`.
- Unknown tool name is a real error from `tools.SubCmd` and propagates to `main` => non-zero exit.
