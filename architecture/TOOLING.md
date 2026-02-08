# Tooling System Architecture

This document describes **how clai’s tooling system works end-to-end**, including:

- how tools are registered and discovered
- how tools are *selected/allowed* for a given run (`-t/-tools`)
- how tool calls flow through the runtime (LLM ↔ tool executor)
- how **MCP servers** are configured and exposed as tools

> Related docs:
>
>- `architecture/TOOLS.md` describes the **`clai tools` inspection command**.
>- `architecture/QUERY.md` describes query/chat runtime behavior.
>- `architecture/CONFIG.md` documents config layout and flags.

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

Built-in tools typically run locally (e.g., execute a Go command, search files, read file contents) and must:

- validate arguments
- produce deterministic/structured output
- return errors with context (`fmt.Errorf("<context>: %w", err)`) so failures are explainable

### MCP tools

MCP tools are discovered from configured MCP servers (see [MCP servers](#mcp-servers)). During tooling initialization:

1. clai reads the MCP server configurations.
2. For each configured server, clai connects (or prepares a client) and fetches tool metadata.
3. clai registers those tools into the same registry as built-ins.

To avoid name collisions and to make origin explicit, MCP tools are typically namespaced/prefixed (for example with `mcp_...`).

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

If you add a new tool:

- keep the schema minimal and strict
- ensure arguments are validated
- avoid implicit ambient access (require explicit paths / commands)
- make failures actionable with contextual errors
