# Tools Command Architecture

Command: `clai [flags] tools [tool name]` (aliases: `t`)

The **tools** command is an *inspection/UI* command. It does **not** enable tools for a query; it lists every built-in clai ships, names each one's dependency and marks the ones this machine cannot load, and can print the JSON schema/spec for one tool.

> Related flag: `-t/-tools` (string) on `query`/`chat` controls *which tools the LLM may call* during that run. See `QUERY.md` and `CONFIG.md`.

## Entry Flow

```text
internal/cli:Run()
  → cmd.Run(...)                  # go_away_boilerplate/pkg/cmd dispatch
    → tools command Run (internal/tools/cmd.go)
      → tools.Init()
      → tools.List() or tools.Detail(name)
```

## Key Files

| File | Purpose |
|------|---------|
| `internal/tools/cmd.go` | tools command + `list` sub; calls `tools.Init()` then `List`/`Detail` (see [cmd-dispatch.md](./cmd-dispatch.md)) |
| `internal/tools/handler.go` | `builtins()`: the whole built-in catalog; `Init()` and `registerLocalTools`: populate the process-global registry with the built-ins this machine can load |
| `internal/tools/builtin_shadow.go` | The declared MCP-tool-to-built-in mapping behind the shadow marker |
| `internal/tools/mcp/schemacache` | The cache the listing reads its MCP tools from, without connecting |
| `internal/tools/cmd.go` | Implements `clai tools` CLI behavior |
| `internal/tools/registry.go` | Tool registry: `Get`, `All`, wildcard selection |
| `pkg/text/models/tool.go` (or similar) | Public tool spec types serialized to JSON |

## Behavior

### Two facts, not one

The catalog is what clai ships; availability is what this machine can load.
They are separate, and the listing shows both.

```text
builtins()                        the same entries on every machine
   │                              executable "" == runs in-process
   ├─ registerLocalTools(lookup)  loads each entry whose program resolves
   │     └─ Registry              what a run here may call, hidden tools too
   └─ buildListing()              lists every visible catalog entry, plus
         │                        any other visible registry entry, plus
         │                        the cached MCP tools
         └─ available = the registry holds it, or it needs no program
```

Registration is unchanged by the catalog split: an entry whose program is
absent is not registered, so a profile naming that tool is still refused by
the same check with the same message. Only the listing changed.

### `clai tools`

`internal/tools/cmd.go` (`List`/`Detail`):

1. Walks `builtins()` for every visible catalog entry, and takes each one's
   specification from the registry when the machine loaded it, from the
   catalog otherwise. An entry that needs a program the machine lacks is
   marked, never dropped.
1a. Loads MCP tools from the schema cache only, never by connecting. For each
   configured server it builds the cache identity and reads that server's
   entry; an entry exists only where a handshake previously completed, so an
   entry is the evidence of a prior successful run. A server with no entry —
   never run, or whose last run failed — contributes nothing and is omitted
   rather than flagged, because a tool listing is what a user reads to find
   out what they can call, not a diagnostic surface. These tools are never
   written into the process-global `Registry`; see `architecture/mcp.md`.
2. Loads the alias map via `Registry.Aliases()` and removes alias names from
   the listing.
3. Sorts the remaining (canonical) tool names.
4. Prints a human readable list:

   - one entry per canonical tool; a tool's aliases are annotated on its
     canonical row (`- async_cmd (alias: async_cmd_run): ...`) instead of
     being listed as duplicate entries
   - an entry this machine cannot load carries one mark, the program it
     needs: `- rg [needs rg]: ...`. Hidden tools stay out of the listing, as
     before
   - attempts to fit descriptions to the width of the session's output writer
     via `utils.SessionDimensions(os.Stdout)` and the explicit-width helper
     `table.WidthAppropriateStringTruncWithWidth`
   - does **not** annotate individual MCP tools. A per-tool marker was built
     and removed: its mapping keys on an MCP tool's name, and a name does not
     establish locality, so the dominant local-launcher-proxying-an-endpoint
     shape (`npx -y mcp-remote https://…`) had its remote tools marked as
     covered by local built-ins. The marker reports and never
     resolves: selection, registration, the schemas sent to a model and
     execution are all unchanged by it. A built-in counts as available to that
     footer only when the registry actually holds it, so one whose executable
     is absent is left out of the footer; its own listing line carries the
     `[needs <program>]` mark instead.

5. Prints a single advisory footer naming the in-process built-ins that may
   already cover something a configured command-based server offers, drawn from
   the declared mapping in `internal/tools/builtin_shadow.go`, deduplicated, and
   restricted to built-ins the registry actually holds. It carries the saving —
   a local server replaced by a built-in is a process removed from every run —
   with no claim about any specific listed tool.

5a. When any entry is marked, prints a second footer explaining the mark:

   ```text
   Note: tools marked [needs <program>] are built in, but this machine's PATH
   has no such program, so they cannot load here.
   ```

6. Prints an instruction footer:

   ```text
   Run 'clai tools <tool-name>' for more details.
   ```

Returns nil so the process exits with code 0.

### `clai tools -json`

The one escape from the `-json` record stream (`runenv.WriteJSONDocument`,
see [output.md](./output.md)): the listing is data to parse, so it is one
array document on the unwrapped stdout, one object per entry. The fields are
the specification (`name`, `description`, `input_schema`, `arguments`) plus:

| Field | Meaning |
|-------|---------|
| `aliases` | names that resolve to this entry; empty array, never null |
| `dependency` | the program the entry runs; empty for an in-process tool and for an MCP tool, so every entry carries the key |
| `available` | whether this machine can load the entry: true for an in-process tool, otherwise whether `dependency` resolved |

The document is additive: every entry is listed on every machine, so a
consumer may compare the set exactly, and read `available` to know what the
machine that printed it could load.

### `clai tools <tool-name>`

If a second CLI arg exists (`args[1]`), it is interpreted as the tool name:

1. Looks up the tool in the registry: `Registry.Get(toolName)`. A built-in
   the machine cannot load is not registered, so the catalog answers instead:
   the mark in the listing never names a tool this command cannot describe.
   A hidden built-in gets no such fallback, so it stays as reachable here as
   it was: the registry, and nothing else, answers for it.
2. An alias name resolves to the same tool instance, so `clai tools
   async_cmd_run` prints the canonical `async_cmd` specification.
3. If missing: returns an error (`tool '<name>' not found`).
4. If present: marshals the tool `Specification()` as pretty JSON and prints it.

Also returns `utils.ErrUserInitiatedExit`.

## Registry and Init

`tools.Init()` must be called before listing tools.

Conceptually, Init is responsible for:

- registering every catalog built-in whose program resolves in `PATH`, and
  every catalog built-in that runs in-process (filesystem, `go test`, `rg`,
  etc.)
- omitting fixed-executable built-ins whose command is not available in
  `PATH`: the listing still names them, but a run here cannot load them
- **not** adding MCP tools. `Init()` populates the process-global registry with built-ins only, by
  design: MCP tools are scoped to the run that discovered them and must never enter a registry whose
  entries a concurrent setup would overwrite. The listing reads them from the schema cache instead,
  as described above and in [mcp.md](./mcp.md)

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
