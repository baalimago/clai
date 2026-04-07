# Add shell completion with clai-native suggestions

## Goal

Add shell completion where the user’s shell delegates completion queries back to the installed `clai` binary.

This keeps:

- shell scripts thin
- completion logic in Go
- dynamic suggestions close to real runtime state
- behavior aligned with the exact binary/version the user is running

This should be implemented as a focused MVP first, without requiring a broad CLI refactor up front.

---

## Summary

`clai` is a good fit for binary-driven completion because it already has:

- command aliases
- dynamic tools
- profiles and shell contexts from config
- command/subcommand structure (`chat`, `tools`, `confdir`)

The recommended shape is:

- a visible `completion` command for installing/exporting shell wrappers
- a hidden `__complete` command used by those wrappers
- a shell-neutral completion engine in Go
- shell-specific adapter scripts that stay minimal

---

## Design principles

### 1. Keep the completion path cheap

Completion must feel instant.

Hard requirements:

- no network I/O
- no model/provider setup
- no expensive query-path initialization
- no blocking MCP handshakes
- only load local config/state that is actually needed

If some dynamic source is slow or unavailable, completion should degrade gracefully instead of blocking.

### 2. Do not block MVP on a full CLI metadata refactor

Long term, shared command/flag metadata would be good.

For MVP, do **not** require a large unification of:

- usage text
- runtime command dispatch
- flag parsing
- completion data

Instead:

- introduce completion-local metadata first
- keep it small and behavior-oriented
- extract shared metadata later if the implementation proves stable

### 3. Shell wrappers should stay dumb

The shell-specific code should:

- collect shell completion context
- call `clai __complete ...`
- render the returned suggestions using native shell facilities

The wrappers should not contain command knowledge.

### 4. Prefer a simple response protocol for MVP

Do not overdesign the wire format initially.

Recommended MVP response:

- line-oriented text
- one candidate per line
- optional tab-separated description: `value<TAB>description`

This is sufficient for:

- bash candidate insertion
- zsh descriptions

If richer metadata is needed later, the hidden command can grow a structured mode after the MVP ships.

---

## Command surface

Add two command entrypoints.

### Visible command

```text
clai completion bash
clai completion zsh
```

Purpose:

- print shell bootstrap/wrapper scripts
- let users source or install them using familiar CLI patterns

### Hidden command

```text
clai __complete ...
```

Purpose:

- used only by shell wrappers
- returns completion suggestions for the current shell state

Important:

- `completion` and `__complete` must bypass normal query/setup paths
- they must be detected early in dispatch
- they must not trigger expensive initialization from normal command execution

---

## Request semantics

Do not model completion requests using only `Words` and `CursorWord`.

The engine should receive enough information to distinguish:

- the already-parsed argv excluding the binary name
- the token currently being completed
- whether the cursor is after a trailing space
- the previous token
- the shell type if needed for adapter-specific behavior

Suggested shape:

```go
type Request struct {
    Shell            string
    Args             []string
    Current          string
    Prev             string
    HasTrailingSpace bool
}
```

This makes ambiguous states easier to handle correctly:

- `clai chat `
- `clai -t `
- `clai qu`
- `clai -asc mi`

---

## Response semantics

For MVP, return plain candidates with optional descriptions.

Suggested internal model:

```go
type Item struct {
    Value       string
    Description string
}
```

The hidden command can print these as:

```text
query\tQuery the chat model
tools\tList available tools
```

If a description is empty, emit just:

```text
query
```

---

## Completion kinds

The engine should distinguish between logical result kinds even if the output protocol remains simple.

Suggested kinds:

- `plain`
- `file`
- `dir`

Why:

- some flags should trigger native file completion
- some flags should trigger native directory completion
- this decision belongs at the engine boundary, not scattered inside shell scripts

Shell wrappers can then:

- use returned candidates for `plain`
- invoke native file completion for `file`
- invoke native directory completion for `dir`

---

## Parsing strategy

Do **not** use strict `flag.FlagSet.Parse()` behavior for completion logic.

Completion happens in incomplete states such as:

- partial flags
- unknown partial commands
- dangling values
- trailing spaces

Instead build a forgiving parser that:

1. walks argv left to right
2. recognizes known global flags and whether they consume a value
3. tracks when a top-level command has been selected
4. tracks known subcommand state where relevant
5. determines what should be completed at the cursor

Unknown tokens should never cause errors in completion mode.

They should either:

- be ignored for state tracking where possible, or
- end in “no suggestions” if that is the safest outcome

No stderr noise should be emitted on successful completion requests.

---

## Flag behavior

The current CLI prominently uses single-dash long-ish flags like:

- `-chat-model`
- `-profile`
- `-photo-dir`

MVP completion should mirror actual parser behavior rather than assume GNU-style `--long`.

Decision:

- prioritize support for the flag forms that runtime parsing actually accepts
- only add `--long` completion if parser support already exists or is intentionally added

---

## Command-specific behavior

### Top-level command completion

When completing the main command position, suggest canonical commands first:

- `help`
- `setup`
- `confdir`
- `query`
- `photo`
- `video`
- `replay`
- `tools`
- `chat`
- `completion`

Include aliases only when useful and not too noisy, for example:

- on clear prefix matches
- or as explicit additional candidates when the alias is a common entrypoint

The goal is to avoid flooding the user with duplicate meanings.

### Global flag completion

When the current token begins with `-`, suggest matching known global flags.

These should initially be described in small completion-local metadata, including:

- name
- aliases if any
- whether the flag takes a value
- completion behavior for its value

### Flag value completion

Initial targeted support:

#### `-t`, `-tools`

Suggest:

- `*`
- built-in tool names
- any locally, cheaply discoverable dynamic tool names

Support comma-separated values.

Important behavior decision:

- the engine should return the **full replacement token**, not only the last segment

Example:

- input token: `rg,fi`
- returned candidate: `rg,file_read`

This keeps shell wrappers simple and consistent.

#### `-p`, `-profile`

Suggest configured profile names.

#### `-asc`

Suggest shell context names from the configured shell context directory.

#### `-prp`

Return `file` completion kind.

#### `-pd`, `-photo-dir`, `-vd`, `-video-dir`

Return `dir` completion kind.

#### `-cm`, `-chat-model`, `-pm`, `-photo-model`

MVP: free text, no special model suggestions required.

Model completion can be added later if it proves useful and cheap.

### `clai tools <tool-name>`

Suggest tool names.

### `clai chat ...`

Suggest subcommands:

- `continue`
- `delete`
- `list`
- `help`

Aliases may also be suggested selectively.

For `chat continue <chatID>` and `chat delete <chatID>`:

- MVP may return no suggestions
- a later phase can add recent chat IDs or indices

### `clai confdir ...`

MVP may suggest known config subpaths if they are already centrally defined and cheap to enumerate.

If this is not already easy, defer deeper `confdir` completion until after the core system ships.

### `query`, `photo`, `video`

These become free-form prompt commands after command selection.

After entering prompt territory, completion should generally stop suggesting command structure.

Specifically:

- do not keep suggesting subcommands
- only suggest flags if the runtime parser genuinely supports flag parsing in that position

Completion should mirror real CLI behavior rather than inventing extra parsing flexibility.

---

## Initial package shape

Suggested package layout:

- `internal/completion/engine.go`
- `internal/completion/request.go`
- `internal/completion/items.go`
- `internal/completion/providers.go`
- `internal/completion/shell_bash.go`
- `internal/completion/shell_zsh.go`

This is only a suggestion. The core point is:

- shell-neutral engine separated from shell-specific wrappers

---

## Dynamic providers

Dynamic providers should be lazy and per-invocation cached.

Useful providers:

- tools
- profiles
- shell contexts

Requirements:

- initialize only when the active completion path needs them
- memoize results for the lifetime of one `__complete` invocation
- prefer local/config-driven discovery only

For tools specifically:

- built-ins should be available cheaply
- dynamically configured tools may be included only if discoverable without expensive setup
- if not cheap, skip them rather than slowing completion down

---

## Phased rollout

### Phase 1: completion skeleton

Ship:

- hidden `clai __complete`
- visible `clai completion bash|zsh`
- shell wrapper output
- top-level command completion
- global flag completion
- `chat` subcommand completion

### Phase 2: high-value dynamic completion

Ship:

- `tools <tool-name>` completion
- `-t/-tools` completion with comma-separated token replacement
- `-p/-profile` completion
- `-asc` completion

### Phase 3: optional additions

Consider:

- `confdir` subpath completion
- recent chat ID/index completion
- model suggestions
- richer metadata/protocol if warranted
- broader CLI metadata unification

---

## Testing plan

Follow repository convention:

- write tests first
- validate they fail
- implement
- validate they pass

Use explicit timeouts for Go tests.

### Engine behavior tests

At minimum:

- `clai ` → top-level commands
- `clai -` → matching flags
- `clai chat ` → chat subcommands
- `clai tools ` → tool names
- `clai -t ` → tool names
- `clai -t rg,fi` → full-token replacement candidates
- `clai -p pr` → profile matches
- `clai -asc mi` → shell context matches
- `clai q hello` → no structural suggestions
- unknown command → no panic, no error
- unknown flag → no panic, does not poison parsing

### Hidden command tests

Verify:

- stable line-based output format
- no stderr noise for successful completion calls
- early dispatch path works without entering expensive runtime setup

### Shell wrapper tests

For both bash and zsh output:

- emitted script calls `clai __complete`
- script remains thin and does not embed command knowledge

---

## Risks

### 1. Over-refactoring before MVP

Risk:

- trying to unify all CLI metadata before completion works at all

Mitigation:

- start with completion-local metadata
- refactor only after behavior is proven

### 2. Slow completion from dynamic sources

Risk:

- completion feels laggy if it initializes too much

Mitigation:

- treat completion speed as a design constraint
- lazy-load providers
- skip expensive sources

### 3. Ambiguous cursor handling

Risk:

- incorrect suggestions around trailing spaces and partial tokens

Mitigation:

- define request semantics precisely
- test incomplete shell states explicitly

### 4. Wrapper complexity leaking shell logic

Risk:

- shell scripts grow completion rules of their own

Mitigation:

- keep shell wrappers as transport/render layers only

---

## Recommended MVP

Ship this first:

- `clai completion bash|zsh`
- `clai __complete`
- top-level commands
- global flags
- `chat` subcommands
- `tools <tool-name>`
- `-t/-tools`
- `-p/-profile`
- `-asc`

That is enough to deliver visible value while keeping scope controlled.

---

## Recommendation

Yes, binary-driven completion is the right design for `clai`.

The implementation should optimize for:

- fast execution
- thin shell integration
- forgiving parsing
- dynamic local suggestions
- incremental rollout

Do the MVP first, then consider protocol expansion and metadata unification once the behavior is stable.