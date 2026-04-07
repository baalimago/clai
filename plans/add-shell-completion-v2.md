# Add shell completion with clai-native completion suggestions (v2)

This document is an updated implementation plan for shell completion in `clai`.

It builds directly on the earlier plan in:

- `plans/add-shell-completion.md`

The original plan is good and the core direction remains the same:

- completion should be driven by the `clai` binary
- shell wrappers should stay thin
- dynamic suggestions should come from real runtime-local state

This revised plan keeps that architecture, but narrows the MVP, makes the completion path cheaper and safer, and clarifies a few behaviors that were underspecified in the earlier version.

---

## Why this revision exists

The previous plan correctly identified the right architecture, but a few parts were still broad enough to create implementation risk:

- it leaned toward a richer protocol before proving the MVP
- it suggested shared CLI metadata extraction early, which could balloon scope
- it did not fully pin down cursor semantics for incomplete shell states
- it did not explicitly require completion to bypass expensive normal setup
- it did not define replacement behavior for comma-separated values like `-t rg,fi`

Those are all fixable. This document updates the plan so implementation can proceed in smaller, safer steps.

---

## What stays unchanged from the previous plan

The following decisions from `add-shell-completion.md` are still recommended:

- use a hidden completion entrypoint, such as `clai __complete`
- expose a user-facing `clai completion <shell>` command
- keep completion logic in Go, not embedded in shell code
- support dynamic suggestions for tools, profiles, shell contexts, and command-specific arguments
- use a forgiving parser rather than strict `flag.FlagSet.Parse()`
- phase the feature in instead of trying to deliver everything at once

---

## Main upgrades in this revised plan

### 1. Prefer a minimal response protocol for MVP

The previous plan suggested JSON for completion responses. That is still a valid future option, but it is not required for the first version.

For MVP, prefer a tiny text protocol:

- one suggestion per line
- optionally `value<TAB>description`

Example:

```text
query\tQuery the chat model
tools\tList available tools
```

### Motivation

- shell completion is latency-sensitive
- bash integration is simpler with line-oriented output
- zsh can still use descriptions with tab-separated fields
- the protocol remains easy to inspect manually during debugging

If richer metadata becomes necessary later, JSON can still be added behind a flag or alternate hidden endpoint. The MVP should optimize for speed and simplicity first.

---

### 2. Do not block on full CLI metadata unification

The previous plan rightly suggested centralizing command and flag metadata. That is still a strong long-term direction, but it should not be a prerequisite for shipping completion.

For v1, introduce only the metadata needed by the completion engine itself:

- top-level commands
- aliases
- a targeted set of global flags
- command-specific follow-up words like `chat continue`

### Motivation

- current CLI behavior is spread across usage text, setup routing, and flag parsing
- forcing total unification first would create a much larger refactor than completion needs
- completion can ship sooner if it starts with a small internal model

After the feature works, shared metadata can be extracted incrementally.

---

### 3. Define request semantics around the current token, not just cursor index

The previous plan used a request shape centered on `Words` and `CursorWord`. That is close, but not explicit enough for shell completion edge cases.

Instead, model the request around token state:

```go
type Request struct {
    Shell            string
    Args             []string // command line after the binary name
    Current          string   // token under cursor, empty after trailing space
    Prev             string   // previous token if any
    HasTrailingSpace bool
}
```

The hidden completion command does not need to expose this exact type publicly, but the engine should operate with these semantics.

### Motivation

This removes ambiguity in common cases:

- `clai chat ` → `Current == ""`, previous token is `chat`
- `clai -t ` → current token is empty, parser still knows a flag value is expected
- `clai qu` → `Current == "qu"`

That makes completion logic more reliable than using only a cursor index.

---

### 4. Define comma-separated replacement behavior now

For `-t` / `-tools`, the earlier plan correctly called out comma-separated completion, but did not define what the engine should actually emit.

This revised plan makes that explicit:

- the engine returns the full token to insert

Example:

- input token: `rg,fi`
- completion candidate: `rg,file_read`

### Motivation

- shell wrappers stay dumb
- replacement behavior becomes shell-independent
- testing is easier because one layer owns the transformation

This is a high-value detail to settle early because tool selection is a major dynamic completion case in `clai`.

---

### 5. Make “cheap completion path” a hard design requirement

The previous plan mentioned performance risk, especially around dynamic initialization. This revised plan upgrades that from a warning to a rule.

Completion must:

- avoid network I/O
- avoid model/provider setup
- avoid query-mode setup
- avoid expensive MCP handshakes
- load only the config needed for the requested completion

If some data source is unavailable cheaply, completion should degrade gracefully rather than trying to fully initialize runtime state.

### Motivation

Completion quality is judged heavily by responsiveness. A feature that is architecturally elegant but slow will feel broken.

In this repository, that matters especially because normal command setup can grow expensive as model/tool integrations evolve.

---

### 6. Add completion result kinds for file and directory fallback

The previous plan suggested delegating file/dir completion to the shell. Keep that behavior, but improve the abstraction slightly.

The engine should be able to describe the expected completion mode:

- `plain`
- `file`
- `dir`

If the engine decides a flag like `-prp` expects a file path, it can signal `file`. If `-pd` or `-vd` expects a directory, it can signal `dir`.

The shell wrapper then decides how to use native file-system completion for that case.

### Motivation

- core logic stays shell-agnostic
- shell-specific file completion remains at the boundary
- parser logic no longer has to bake shell behavior directly into completion decisions

---

### 7. Make command-boundary behavior explicit after free-form commands

Commands like:

- `query`
- `photo`
- `video`

mostly transition into free-form text after command resolution.

The completion engine should explicitly stop suggesting positional command structure once it determines the user is now supplying prompt text.

One exception remains:

- if the current token starts with `-`, and runtime parsing truly allows flags in that position, flag completion may still be offered

### Motivation

Without this rule, the engine can become noisy and keep suggesting commands where the user is clearly typing a prompt.

The completion behavior should mirror real CLI expectations, not generic subcommand logic.

---

### 8. Match the actual flag style of this CLI

The current usage primarily documents single-dash long-ish flags such as:

- `-reply`
- `-chat-model`
- `-photo-dir`

Therefore, completion should first mirror what the parser actually accepts, not what users might expect from GNU-style CLIs.

For MVP:

- prioritize single-dash long flags
- only support `--long-flag` suggestions if the runtime parser truly accepts them

### Motivation

Completion should reinforce real behavior, not invent alternate syntax.

That avoids confusion and keeps the completion layer honest.

---

### 9. Completion must bypass normal setup early

This was underemphasized in the earlier plan and needs to be explicit.

Both of these command paths must be detected before normal runtime setup:

- hidden `clai __complete`
- visible `clai completion <shell>`

They should not flow through code paths that prepare query execution, model routing, or anything else unrelated to completion.

### Motivation

- keeps completion fast
- reduces accidental coupling to query features
- avoids side effects from unrelated initialization

This is one of the most important operational requirements in the plan.

---

### 10. Add minimal per-invocation caching interfaces

The previous plan noted dynamic provider cost but did not shape interfaces around it.

In v1, providers should be lazy and memoized within a single completion invocation:

- load tools once
- load profiles once
- load shell contexts once

This does not require a persistent cache yet, only request-local memoization.

### Motivation

- avoids repeated filesystem scans during one request
- keeps provider implementations simple
- leaves room for future persistent caching if needed

---

## Revised architecture

Create a focused completion package, for example:

- `internal/completion/engine.go`
- `internal/completion/request.go`
- `internal/completion/response.go`
- `internal/completion/parse.go`
- `internal/completion/providers.go`
- `internal/completion/shell_bash.go`
- `internal/completion/shell_zsh.go`

Suggested responsibilities:

- parse incomplete argv into a shell-neutral request state
- determine completion context
- fetch suggestions from cheap local providers
- emit shell-ready output
- print bootstrap scripts for supported shells

Shell-specific logic should remain limited to:

- collecting shell state
- invoking `clai __complete`
- adapting output into native shell completion APIs

---

## Revised command surface

### Visible command

```text
clai completion bash
clai completion zsh
```

Purpose:

- print shell bootstrap code
- make completion installation discoverable

### Hidden command

```text
clai __complete ...
```

Purpose:

- invoked by the shell adapter only
- outputs completion candidates in a machine-friendly line format

The hidden command should be intentionally cheap and side-effect free.

---

## Completion behavior plan

### Top-level command completion

When completing the first command position after any recognized global flags, suggest canonical commands first:

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

Aliases may still be supported, but should be de-emphasized to reduce noise.

### Motivation

Canonical-first output is usually easier to scan, while still allowing aliases when directly matched by prefix.

---

### Global flag completion

When the current token begins with `-`, suggest known global flags from a completion-local flag table.

That table should include at least:

- `-reply`
- `-raw`
- `-chat-model`
- `-photo-model`
- `-photo-dir`
- `-photo-prefix`
- `-video-dir`
- `-video-prefix`
- `-tools`
- `-glob`
- `-profile`
- `-profile-path`
- `-append-shell-context`

Short aliases may also be suggested if they are useful and unambiguous.

### Motivation

This brings immediate value without requiring completion to know every internal CLI detail on day one.

---

### Flag value completion

#### `-tools`

Suggest:

- `*`
- built-in tool names
- fast local dynamic tool names, if available without expensive initialization

Support comma-separated replacement by emitting full-token candidates.

#### `-profile`

Suggest known profile names.

#### `-append-shell-context`

Suggest names derived from:

- `<config-dir>/shellContexts/*.json`

#### `-profile-path`

Return result kind `file`.

#### `-photo-dir`, `-video-dir`

Return result kind `dir`.

#### `-chat-model`, `-photo-model`

For MVP, treat as free text.

### Motivation

This keeps the highest-value dynamic completions in scope while avoiding unnecessary complexity around model discovery.

---

### Command-specific positional completion

#### `clai tools <tool-name>`

Suggest tool names.

#### `clai confdir [subpath ...]`

Suggest known config subpaths if they are finite and cheap to obtain. If completion becomes path-like after that, return file/dir kind as appropriate.

#### `clai chat ...`

Suggest:

- `continue`
- `delete`
- `list`
- `help`

and aliases when directly matched.

For `chat continue <chatID>` and `chat delete <chatID>`:

- MVP may return no suggestions
- later phases can suggest recent chat IDs or indices

#### `query`, `photo`, `video`

Once these commands are resolved and prompt text begins, stop structural suggestions.

### Motivation

This captures the biggest practical UX wins without trying to autocomplete arbitrary prompt text.

---

## Parsing strategy

As in the earlier plan, do not use strict flag parsing for completion.

Instead, implement a forgiving parser that:

1. walks tokens left-to-right
2. recognizes known flags and whether they expect a value
3. tracks whether the current token is a flag, a flag value, a command, or a positional argument
4. tracks subcommand state for at least:
   - `chat`
   - `tools`
   - `confdir`
   - `completion`
5. never errors on incomplete input

It should also tolerate:

- unknown flags
- unknown commands
- dangling values

Unknown input should result in either no suggestions or best-effort suggestions, never a hard failure.

### Motivation

Shell completion regularly operates on incomplete and partially invalid argv. The parser must be built for that reality.

---

## Revised implementation phases

### Phase 1: command surface and static completion

Deliver:

- hidden `clai __complete`
- visible `clai completion bash|zsh`
- tiny text response protocol
- top-level command completion
- global flag completion
- `chat` subcommand completion

### Motivation

This proves the architecture with minimal scope and without dynamic provider risk.

---

### Phase 2: high-value dynamic completion

Deliver:

- `tools <tool-name>` completion
- `-tools` dynamic value completion
- `-profile` completion
- `-append-shell-context` completion
- file/dir result kinds for path-oriented flags

### Motivation

These are the most useful dynamic suggestions and directly justify binary-driven completion.

---

### Phase 3: richer dynamic and contextual completion

Optional later additions:

- `confdir` subpath completion
- chat ID/index completion
- model name suggestions
- richer zsh descriptions or alternate response formats
- stronger metadata sharing with runtime/help systems

### Motivation

These features are valuable, but nonessential for proving the system.

---

## Testing plan

The original plan had the right direction on testing. This revision adds more explicit shell-state cases.

### Engine/parser tests

Cover at least:

- `clai ` → top-level commands
- `clai -` → global flags
- `clai qu` → `query`
- `clai chat ` → chat subcommands
- `clai chat c ` → no panic, valid state tracking
- `clai tools ` → tool names
- `clai -tools ` → tool names
- `clai -tools rg,fi` → full-token replacement behavior
- `clai -profile de` → matching profiles
- `clai -append-shell-context mi` → matching shell contexts
- `clai q hello` → no structural suggestions
- unknown flag → no panic
- unknown command → no panic

### Provider tests

Cover:

- built-in tool loading
- request-local provider memoization
- profile discovery
- shell context discovery

### Command output tests

Cover:

- `clai completion bash` emits a wrapper that calls `clai __complete`
- `clai completion zsh` emits a wrapper that calls `clai __complete`
- hidden completion command returns stable line-oriented output
- successful completion emits no stderr noise

### Integration tests

Exercise the hidden command directly with representative argv states to ensure dispatch bypass works and no unrelated setup is triggered.

---

## Risks and mitigations

### 1. Completion becomes slow

Mitigation:

- completion path bypasses normal setup
- no network I/O
- only cheap local providers
- invocation-local memoization

### 2. CLI drift between runtime and completion

Mitigation:

- start with small completion-local metadata
- later extract shared metadata incrementally
- add behavioral tests around documented commands and flags

### 3. Alias noise overwhelms suggestions

Mitigation:

- prefer canonical names
- only surface aliases when directly useful for current prefix

### 4. Shell-specific escaping differences

Mitigation:

- keep protocol simple
- keep shell adapters small
- let shell wrappers handle native insertion behavior

### 5. Dynamic tool discovery is expensive

Mitigation:

- built-ins first
- local fast sources only for MVP
- graceful degradation if dynamic inventory is unavailable cheaply

---

## Concrete rollout plan

1. Add command dispatch support for:
   - `completion`
   - `__complete`

2. Ensure both paths bypass normal query/runtime setup

3. Implement a small shell-neutral completion engine
   - request semantics centered on current token
   - forgiving parser
   - static command and flag completion

4. Add bash and zsh bootstrap output

5. Add dynamic providers
   - tools
   - profiles
   - shell contexts

6. Add result kinds
   - plain
   - file
   - dir

7. Add tests before expanding scope further

8. Only then consider broader CLI metadata extraction for help/runtime/completion reuse

---

## Recommended MVP

Ship this first:

- `clai completion bash|zsh`
- hidden `clai __complete`
- top-level command completion
- global flag completion
- `chat` subcommands
- `tools <tool-name>`
- `-tools` dynamic value completion
- `-profile` completion
- `-append-shell-context` completion
- path-kind handling for file/dir flags

This gives `clai` a genuinely useful completion system while keeping implementation risk bounded.

---

## Final recommendation

The original plan was correct about the big architectural choice: `clai` should own its own completion logic.

This revised plan keeps that design, but improves it by:

- reducing MVP protocol complexity
- avoiding a broad prerequisite refactor
- precisely defining cursor/token semantics
- clarifying replacement behavior for comma-separated values
- making fast-path dispatch and low-cost execution explicit

In short:

- keep the binary-driven completion design
- ship a smaller, faster MVP
- evolve toward shared CLI metadata after the behavior is proven