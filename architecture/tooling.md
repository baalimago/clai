# Tooling System Architecture

This document describes **how clai’s tooling system works end-to-end**, including:

- how tools are registered and discovered
- how tools are *selected/allowed* for a given run (`-t/-tools`)
- how tool calls flow through the runtime (LLM ↔ tool executor)
- how **MCP servers** are configured and exposed as tools

> Related docs:
>
>- `architecture/tools.md` describes the **`clai tools` inspection command**.
>- `architecture/query.md` describes query/chat runtime behavior.
>- `architecture/config.md` documents config layout and flags.

## Terminology

- **Tool**: A callable capability exposed to the model with a JSON schema (name, description, parameters) and an implementation.
- **Registry**: The in-process catalog of all known tools (built-ins and MCP-derived).
- **Allowed tools**: The subset of registered tools the model is permitted to call for a given run.
- **Built-in tool**: Implemented inside this repo (e.g. filesystem, `rg`, `go test`).
- **MCP tool**: A tool whose implementation is provided by an external **Model Context Protocol (MCP)** server.

## High-level flow

At a high level, tool usage is:

1. **Startup** initializes the tool registry.
2. The user’s flags/config determine which tools are **allowed** for that run.
3. The runtime sends the tool specifications of the allowed tools to the LLM.
4. The LLM may respond with a **tool call** (name + JSON arguments).
5. clai executes the tool (built-in handler or MCP client call).
6. The tool result is returned to the LLM as a tool result message.
7. The loop continues until a final answer is produced.

## Registry: discovery and registration

All tools that can possibly be used by clai must be present in the **tool registry**.

### Built-in tools

Built-ins are registered during tooling initialization. Conceptually:

- tooling init constructs a registry
- each built-in tool is registered with:
  - a **stable tool name**
  - a **JSON schema** for parameters
  - an **executor** (Go code) that runs the tool and returns a structured result

Built-ins backed by a fixed external executable are registered only when that
executable resolves through `exec.LookPath`. Therefore, unavailable tools are
absent from both `clai tools` and the schemas sent to a model. Dynamic command
tools such as `async_cmd` remain available because their executable is supplied
at call time. `audio_transcribe` also remains available because `ffmpeg` and
`ffprobe` are needed only when an input must be split.

The `cp` built-in copies files and directories recursively through the system
`cp` executable. It preserves metadata and symbolic links by default; callers
can disable preservation. The `rsync` built-in uses archive and partial-transfer
modes. Remote paths are rejected unless `allow_remote` is explicitly true, and
destructive destination cleanup requires the separate `delete` option.

Built-in tools typically run locally (e.g., execute a Go command, search files, read file contents) and must:

- validate arguments
- produce deterministic/structured output
- return errors with context (`fmt.Errorf("<context>: %w", err)`) so failures are explainable

The `mktemp` built-in creates a private directory under the `clai` subdirectory
of the operating system's temporary directory (normally `/tmp/clai/<randdir>`).
It removes the private directory when the calling context is canceled. The
cleanup lives inside the tool itself, so the CLI and `pkg` agents get identical
behavior: whoever cancels the query context triggers removal, with no extra
wiring at the call site.

### MCP tools

MCP tools are discovered from configured MCP servers (see [MCP servers](#mcp-servers)). During tooling initialization:

1. clai reads the MCP server configurations.
2. For each configured server, clai connects (or prepares a client) and fetches tool metadata.
3. clai registers those tools into the same registry as built-ins.

To avoid name collisions and to make origin explicit, MCP tools are typically namespaced/prefixed (for example with `mcp_...`).

Registry aliases are lookup names, not additional model capabilities. When
tools are selected, clai sends one schema per specification name. This keeps a
canonical tool and its legacy aliases from producing duplicate tool names in
provider requests.

## Allowed tools: selection and enforcement

Tool *existence* (registered) is separate from tool *permission* (allowed).

### Sources of allowed-tool configuration

Which tools are allowed is driven by:

- CLI flags (`-t/-tools`)
- configuration defaults (profile/config files)

The selection process:

- resolves wildcards/globs
- validates the requested tools exist (or are acceptable MCP tool references)
- produces the final allow-list (or disables tooling if empty)

### Semantics

Common patterns:

- `-t=*` means **all tools** are allowed.
- `-t=a,b,c` means only those tools are allowed.
- If the final allow-list is empty, tool calling is disabled for that run.

### Enforcement points

Enforcement happens in two key places:

1. **Before sending tool specs to the model**: only allowed tools are advertised.
2. **Before executing a tool call**: the executor checks the tool name is allowed. If not, it fails with an error explaining the tool is not permitted.

This prevents accidental execution even if a model “hallucinates” a tool name.

## Tool call execution model

A model tool call is represented as:

- `tool_name`: string
- `arguments`: JSON object

Execution steps:

1. Look up `tool_name` in the registry.
2. Validate the tool is allowed.
3. Validate/parse arguments according to the tool’s schema.
4. Execute:
   - built-in executor (local)
   - MCP executor (RPC to server)
5. Capture stdout/stderr (where applicable), structure the result, and return it to the model.

### Tool budgets

The stoploss controller (`internal/text/stoploss.go`) owns both run budgets and
preflights a complete model tool batch before any side effect runs.

- `max-tool-calls` caps the number of tool calls per run with the escalating
  refusal ladder. `max-tool-calls: 0` (or nil) means **unlimited**.
- `stoploss.max-tokens` is the token stoploss: after the crossing step's tool
  batch, clai injects the handover user message. The handover starts a fresh
  wrap-up phase: post-handover tool calls are counted against
  `stoploss.max-tool-calls-after-handover` (absent or 0 = **unlimited**, the
  default), never against the pre-handover `max-tool-calls` consumption. Once
  the wrap-up allowance is exhausted, the refusal ladder applies.

A refused call never reaches its executor: the tool result carries the refusal
text instead of tool output.

Tool execution should be:

- bounded (context-aware cancellation)
- safe (respect configured project roots / allowed paths where applicable)
- explicit about failures (errors with context)

## MCP servers

MCP (Model Context Protocol) servers let clai use tools implemented outside this repository.

### What clai uses MCP for

clai treats each MCP server as a provider of:

- a set of tool specifications (name/description/JSON schema)
- a protocol endpoint to execute tool calls

Those tools are imported into the registry and become selectable via `-t/-tools` like any other tool.

### Configuration layout

MCP servers are configured under the clai config directory, conceptually:

- `<clai-config>/mcpServers/*.json`

Each JSON file describes one MCP server. The exact schema is defined by the project’s config code, but typically includes:

- a display/name/ID
- how to start/connect to the server (e.g. command + args, or URL)
- environment variables
- optional allow/deny lists of tools
- an optional per-call timeout (`timeout_seconds`): bounds a single tool call so a hung server cannot block an agent forever. `0` or absent means unbounded (the caller's context is the only bound).

### Lifecycle

MCP server lifecycle is:

1. **Load configuration** from `mcpServers/*.json`.
2. **Start/connect** to the MCP server.
3. **Discover tools** exposed by that server.
4. **Register tools** with namespacing to avoid collisions.
5. When the model calls an MCP tool, clai:
   - serializes arguments
   - performs an MCP request
   - returns the MCP response as the tool result
6. On shutdown/cancel, clai closes client connections and terminates spawned processes.

### Naming and selection

Because MCP servers are external and tool names can overlap with built-ins, MCP-derived tools should be distinguishable.

Practically:

- MCP tools are accepted/validated by name (often with an `mcp_` prefix)
- `-t=*` includes MCP tools in addition to built-ins
- `clai tools` will list MCP tools if they are configured and initialized

### Error handling

MCP calls can fail due to:

- server startup/connect errors
- tool not found on the server
- invalid arguments
- server-side execution errors
- timeouts/cancellation

All such failures should be surfaced as contextual errors (e.g. `fmt.Errorf("call mcp tool %q on server %q: %w", tool, server, err)`).

## Inspection vs execution

Two related but distinct concepts:

- **Inspection** (`clai tools ...`) lists tools and shows their JSON specs. It does not run a query.
- **Execution** (`clai query` / `clai chat`) uses the allowed-tool list to decide what the model can call.

The inspection command is useful for:

- verifying your MCP servers are configured correctly
- seeing the exact JSON schema the model sees
- checking tool naming

## Security and safety considerations

Tooling can execute code or access local files. The design relies on:

- explicit opt-in via `-t/-tools` (or config defaults)
- path scoping / allowed-root enforcement for filesystem tools
- context cancellation + timeouts
- clear logging/error messages

### Command ban list

The freetext command tools (`cmd`, `async_cmd`; legacy aliases
`freetext_command`, `async_cmd_run`) can be restricted with an opt-in command
ban list. The default list is empty, so ad-hoc command execution works exactly
as before (permissive default, D4).

Matching is word-boundary phrase matching (D2): each entry is a
whitespace-separated phrase of one or more tokens; a command is banned when
the entry's tokens appear as a contiguous, in-order run in the command's
flattened token list. Tokens are whitespace-split with one layer of quotes
stripped per token, quoted words flattened into inner tokens, and shell
metacharacters (`; | & ( ) < >` and backtick) split apart — so
`sh -c "rm -rf /"` is caught by entry `rm`, and `sh -c 'git commit'` is caught
by entry `git commit`. Matching is exact and case-sensitive. Entries are
whitespace-split only: quotes and metachars are never processed inside an
entry.

Scope (D1): only the two freetext/direct command-execution tools `cmd` and
`async_cmd` (plus their legacy aliases `freetext_command` and `async_cmd_run`).
Structured tools with fixed binaries (`go`, `git`, `sed`, `ls`, ...),
`clai_run`, and MCP tools are not affected.

The effective list is purely additive (D5): default(empty) + `textConfig.json`
(`cmd-ban`) + profile (`cmd-ban`, only when explicitly set) + `-cmd-ban` flag
(append) + agent API (`WithCmdBanList`). No source removes another source's
bans; a ban is removed by editing the source that added it.

Enforcement happens at the spawn point in `pkg/tools`. A banned command is
never spawned: the tool returns an error naming the matched entry and stating
the rule, the model sees it as a normal tool result, and the run continues —
a refusal never aborts the run (D14).

Documented matching limits (literal-text matching):

- Banning is by literal content: a command is refused whenever the phrase's
  tokens occur in it, even when nothing executes (`echo git commit` IS banned
  by entry `git commit`).
- The matcher sees literal text only: variable assignments are not expanded
  (`x=git; $x commit` is NOT caught by `git commit`), and command
  substitutions are not evaluated.
- Contiguity: interleaved arguments can evade a phrase (`git -C /path commit`
  is NOT caught by `git commit`).
- Literal spelling: alternate spellings that change the literal tokens evade
  (`/bin/rm -rf /` is NOT caught by entry `rm`).

If you add a new tool:

- keep the schema minimal and strict
- ensure arguments are validated
- avoid implicit ambient access (require explicit paths / commands)
- make failures actionable with contextual errors
