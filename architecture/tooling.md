# Tooling System Architecture

This document describes **how clai’s tooling system works end-to-end**, including:

- how tools are registered and discovered
- how tools are *selected/allowed* for a given run (`-t/-tools`)
- how tool calls flow through the runtime (LLM ↔ tool executor)
- how **MCP servers** are configured and exposed as tools

> Related docs:
>
>- `architecture/mcp.md` owns MCP connections: the seam, the demux contract, bounds and lifecycle.
>- `architecture/tools-command.md` describes the **`clai tools` inspection command**.
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

The `jq`, `head`, and `tail` built-ins are thin wrappers around their system
executables and are registered only when those executables are available.
`jq` accepts a normal jq filter and exposes the commonly needed `-c`, `-r`,
`-s`, `-S`, and `-e` flags. `head` and `tail` expose mutually exclusive line
(`-n`) and byte (`-c`) counts; blocking tail follow mode is intentionally not
available.

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

MCP tools are discovered from configured MCP servers and namespaced as `mcp_<server>_<tool>`, so
their origin is explicit and they cannot collide with a built-in.

They are **not** registered into the process-global registry that holds the built-ins. They live in a
per-run registry owned by the querier that discovered them, because the global one's entries are
overwritten by every concurrent `Setup`. One consequence is worth knowing here rather than
discovering it: `clai tools` cannot read MCP tools from the global registry either, so it has its own
cache-only source — see [tools-command.md](./tools-command.md).

The connection model, the handshake, the demultiplexing contract and the failure posture are owned
by **[mcp.md](./mcp.md)**.

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

- `-t=*` means **all tools** are allowed, `view_image` and `generate_image` included.
- `-t=a,b,c` means only those tools are allowed.
- If the final allow-list is empty, tool calling is disabled for that run.
- `generate_image` is not in the registry: the turn registers it (see below). Selection
  treats the name as known, so `-t generate_image` alone keeps tools enabled and prints no
  unknown-tool warning, and registry setup skips it silently. Selecting it costs nothing until
  the model calls it: the generator is built on the first call.

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

### Image results

A tool whose result can hold images implements `models.PartsCaller` (`pkg/text/models/tools.go`).
`CallParts` returns its result as text and image parts, in order, and every image has origin `tool`, or `generated` when the tool generated it (`generate_image`).
`Call` returns the string form of the same result: the `Describe()` of every part joined by `"\n"`,
so each image reads `[image <handle>]`. Wherever only a string can travel, such a tool behaves as a
text tool. An error from `CallParts` is returned unchanged by `Call`.

Three tools implement it. MCP tools map a result's image content to images (see
[mcp.md](./mcp.md#tool-results)). `view_image` (`pkg/tools/view_image.go`), a registered native
tool (`internal/tools/handler.go`), returns a local png,
jpeg, gif or webp file as the text `image <handle> from <absolute path>` and a pending image. It
resolves the path with `utils.AbsHomePath`, which expands a leading `~/` only and which
`generate_image` shares. It then reads the file through `asset.ReadLocalImage`, the one local image
reader that `generate_image` and the input collector share: it refuses a file over 20 MiB
(`asset.LargestLocalImage`) with `ImageTooLargeError` before reading it, and bytes that are not an
image with `ErrNotImage`. `view_image` never writes to the asset store.

`generate_image` (`internal/imagegen/tool`) generates or edits images through the injected
`imagegen.NewGeneratorFunc` (see [photo.md](./photo.md#image-generators)), which it calls once, on
its first call. Its arguments are `prompt` and `rebus` (both required), `input_images` and `size`
(`auto`, `1024x1024`, `1536x1024` or `1024x1536`). `rebus` asks for three emoji that summarise the
image; the tool ignores it, and the turn shows it on the image line (`imagegen/tool.Rebus` accepts
one to three emoji; anything else is left off, and never fails the call). An `input_images` entry of six lowercase hex digits is a
handle, resolved in the asset store; any other entry is a local file read under the same rules as
`view_image`. Each generated image is stored in the asset store before the tool returns, as the
text `generated image <handle> saved at <absolute path>` (plus `revised prompt: <p>` when the
backend revised it) and a stored image with origin `generated`. A failed generation stores nothing
and returns the error wrapped with `generate_image:`; a missing prompt is `ErrPromptRequired` and an
unknown size `*InvalidSizeError`.

In a query turn, the tool executor keeps a `PartsCaller` result that holds an image as the tool
message's `Parts`, and presents only the images with origin `generated`: on a terminal, as
`Presenter.Rows` inside a tool block that stays on screen (after the rolling window's rows, which
stay in place; never compacted or cut), in place of the image label and `generate_image`'s
`SavedText` line; in raw or debug mode as a line of their own. A result without an
image, or a failed call, is the same string `tools.InvokeWith` gives, error folding included (see
[query.md](./query.md#images-in-a-turn)). The turn registers `generate_image` itself whenever it is
requested, on every vendor whose completer takes tools (a `models.ToolBox`); a model without
tools runs without it, as it runs without every tool. On the CLI it is requested by any `-t` entry or profile tool that
matches it (`generate_image`, `*_image`, `*`), its model is `-pm`, else `route.ImageModelFor` the
chat vendor and `photoConfig.json`'s `model`, and its generator constructor is
`cli.Deps.NewImageGenerator`; every
e2e `TestMain` injects one that refuses, so no test pays for an image (see [images.md](./images.md)).

### Tool budgets

The stoploss controller (`internal/text/stoploss.go`) owns both run budgets and
preflights a complete model tool batch before any side effect runs.

- `max-tool-calls` caps the number of tool calls per run with the escalating
  refusal ladder. `max-tool-calls: 0` (or nil) means **unlimited**.
- `stoploss.max-tokens` is the token stoploss: after the crossing step's tool
  batch, clai injects the handover user message. A loop iteration keeps the
  crossing silent because it is expected operation; a plain query prints a
  notice. The handover starts a fresh
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

MCP (Model Context Protocol) servers let clai use tools implemented outside this repository. Each
server is a provider of tool specifications plus an endpoint that executes calls, and its tools
become selectable via `-t/-tools` like any other tool.

Everything else about them lives in one place, **[mcp.md](./mcp.md)**: the connection model and why
a connection is never shared, the JSON-RPC demultiplexing contract, the goroutines per connection,
the handshake and the bounds that govern it, the `<clai-config>/mcpServers/` configuration layout,
ambient versus explicit failure posture, and how a server's standard error is surfaced.

A claim stated in two places drifts, so this section deliberately holds no detail of its own.

## Inspection vs execution

Two related but distinct concepts:

- **Inspection** (`clai tools ...`) lists built-in tools plus any MCP tool cached from a prior successful run, and shows their JSON specs. It does not run a query and never connects to a server; see [mcp.md](./mcp.md).
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

A nested clai's prompt is prose, not a command: when `async_cmd` runs `clai`
(by name or path), an argument of more than one word is a prompt for the
child's model and is not matched, so a worker brief saying "rm old files" is
not refused by entry `rm`. Every other token still is: a lone `rm` argument,
an entry naming `clai`, and anything a shell runs (`sh -c "clai q 'rm x'"`
is matched as before). The child enforces the bans instead: every command
`cmd` and `async_cmd` spawn under a policy gets it in `CLAI_CMD_BAN`
(`pkgtools.CmdBanEnv`, one entry per line), and a clai run appends those
entries to its own (`setupInheritedCmdBan`), so a worker refuses what its
parent would. Under a policy, a command or an `async_cmd` `env` that names
`CLAI_CMD_BAN` is refused, so a model cannot clear it for its children.

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

The policy is carried only on the tool-call context (worklog
2026-09-09-conversation-summaries, D29). `NewQuerier` keeps
`Configurations.CmdBan` on the querier, and the tool executor attaches it
with `pkgtools.WithCmdBanContext` at its single invoke site, so every
`CallWithContext` of `cmd` and `async_cmd` in that run sees that run's list
and nothing else. There is no package-level ban state: constructing a second
querier in the same process (a `pkg/agent` run, the summarizer) never alters
another run's policy. A context without a policy is permissive, and so is
the context-free `Call` entry point — enforcement requires
`CallWithContext`, which is what the executor uses. `WithCmdBanContext`
copies its input slice, so a caller mutating the list afterwards does not
change the installed policy.

Documented matching limits (literal-text matching):

- Banning is by literal content: a command is refused whenever the phrase's
  tokens occur in it, even when nothing executes (`echo git commit` IS banned
  by entry `git commit`, and so is `git commit -m "rm old files"` by entry
  `rm`). The one exception is a direct `clai` invocation's multi-word
  prompt, above; written into a `cmd` shell string, `clai q "rm x"` is still
  refused.
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

## Orchestration and the hidden clai worker tools

An agent orchestrates clai workers with `async_cmd` running the clai binary,
for example `async_cmd {"command": "clai", "args": ["-ro", "-cm", "<model>",
"-t", "rg,cat", "q", "<brief>"]}`, then `async_cmd_await` on the ids. Its
prompt has it run `clai help` and `clai q -h` first, so it learns the flags.

- **Reply-only.** `-reply-only` (`-ro`) sends the raw transcript (tool calls,
  tool output, reasoning) to stderr through `Querier.transcriptOut`, so a
  worker's stdout is its reply alone. Users get the same with
  `clai -ro ... q | git commit -F -`.
- **No prompt.** A profile run without a prompt or stdin acts on its defaults
  (see profiles.md), so `clai -prp <agent> q` works as a worker too.
- **Hidden tools.** `clai_run`, `clai_check`, `clai_result`,
  `clai_wait_for_workers` and `clai_help` are registered with
  `registry.SetHidden`: a profile or `-t` that names one exactly still gets it,
  but `*` and other wildcards, `use_tools` without a list, `clai tools` and
  completion leave them out. Their behaviour is kept for the profiles that
  still list them: `clai_run` splits the flags before `q` on whitespace and
  passes the rest verbatim as one prompt, and runs the child with `-r
  -reply-only -summarize=false`; `clai_wait_for_workers` returns each worker
  once, adds a failed worker's last 2000 bytes of stderr, and on timeout
  interrupts the rest and still returns the finished ones.
