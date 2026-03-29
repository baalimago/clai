# Add shell completion with clai-native completion suggestions

## Goal

Add a shell completion system where the installed shell delegates completion queries back to the `clai` binary itself, instead of relying on a large statically generated completion script with all logic embedded in shell code.

This means:

- shell integration stays thin
- completion rules live in Go, close to existing CLI parsing and runtime state
- dynamic values can be suggested from real runtime sources
- command, flag, tool, profile, and config-derived completions stay in sync with the binary version the user is actually running

---

## Why this approach is neat

The “binary suggests its own completions” design is attractive here because `clai` already has:

- command aliases
- dynamic tool inventory (`tools.Init()`, MCP tools)
- config-aware values (`profiles`, shell contexts, config subpaths)
- subcommands with positional structure (`chat`, `tools`, `confdir`)

If completion logic is generated once into shell script, drift is likely. If the shell asks `clai` directly, the completion behavior can reuse the same code paths and data sources as the real command.

---

## Current state

From the codebase:

- command dispatch is in `internal/setup.go:getCmdFromArgs`
- global flags are parsed in `internal/setup_flags.go:parseFlags`
- tool listing exists via `internal/tools/cmd.go` and dynamic registry init via `tools.Init()`
- help text in `main.go` documents user-facing commands and aliases
- `confdir`, `profiles`, `tools`, `chat`, `query`, `photo`, `video`, `replay`, `version`, `setup` already exist as top-level commands

There is currently no shell completion subsystem.

---

## High-level design

Introduce a hidden command family dedicated to completion, for example:

```text
clai __complete <shell> -- <words...>
```

or simpler:

```text
clai __complete <words...>
```

where the shell wrapper passes:

- the current argv words
- cursor position or current token
- shell type if needed

The binary responds with machine-readable suggestions.

Recommended response format:

- line-oriented plain text for simplicity, or
- JSON for extensibility

Prefer JSON internally because it supports richer metadata:

- suggestion value
- display text
- description
- completion kind (`flag`, `command`, `file`, `value`)
- whether shell should append a space

Example response shape:

```json
{
  "items": [
    {"value": "query", "description": "Query the chat model"},
    {"value": "tools", "description": "List available tools"}
  ]
}
```

The shell adapter can stay tiny and shell-specific.

---

## Architecture proposal

### 1. Add a dedicated completion package

Create something like:

- `internal/completion/engine.go`
- `internal/completion/model.go`
- `internal/completion/context.go`
- `internal/completion/shell_bash.go`
- `internal/completion/shell_zsh.go`

Responsibilities:

- parse completion request context
- determine whether user is completing a command, flag, flag value, or positional argument
- emit suggestions
- optionally emit shell bootstrap scripts

Keep shell-specific behavior at the boundary only. Core decision logic should be shell-agnostic.

---

### 2. Add user-facing commands

Add two new entrypoints:

#### A. Hidden internal command

```text
clai __complete ...
```

Purpose:

- invoked only by shell integration
- returns machine-readable completions

#### B. Visible install/export command

```text
clai completion bash
clai completion zsh
```

Purpose:

- prints the shell bootstrap script
- user can source it or install it under shell completion directories

This is a familiar UX and keeps setup discoverable.

---

### 3. Completion engine data model

Define a shell-neutral request model, e.g.:

```go
type Request struct {
    Words      []string
    CursorWord int
    Shell      string
}

type Item struct {
    Value       string `json:"value"`
    Description string `json:"description,omitempty"`
    NoSpace     bool   `json:"no_space,omitempty"`
}

type Response struct {
    Items []Item `json:"items"`
}
```

The shell wrapper translates shell-native completion state into this request.

---

## Completion behavior plan

### Top-level command completion

When completing first positional after global flags, suggest:

- `help`, `h`
- `setup`, `s`
- `confdir`
- `query`, `q`
- `photo`, `p`
- `video`, `v`
- `replay`, `re`
- `tools`, `t`
- `chat`, `c`
- `profiles`
- `version`
- possibly `dre`, `dir-replay` if intended to be public

Recommendation: only suggest aliases if the prefix matches them naturally; otherwise prioritize canonical names to reduce noise.

---

### Global flag completion

Always suggest global flags when:

- current token starts with `-`
- or parser determines next token is a flag value

Flags from `parseFlags()` should be represented once in completion metadata instead of duplicated ad hoc.

Recommended refactor:

- extract flag definitions into shared metadata
- use that metadata both for flag parsing and for completion suggestions

That prevents divergence.

Metadata should include:

- long name
- short name
- takes value or bool
- value kind (`model`, `dir`, `profile`, `tool-list`, `shell-context`, `glob`, `file`)
- description

---

### Flag value completion

Implement targeted value completion for these flags first:

#### `-t`, `-tools`

Suggest:

- `*`
- built-in tool names from `tools.Registry`
- MCP tool names after `tools.Init()`

Special handling:

- support comma-separated partial completion
- if user typed `rg,fi`, only replace the last segment with matching candidates

This is one of the strongest reasons to keep completion in the binary.

#### `-p`, `-profile`

Suggest configured profile names.

Completion source likely comes from the same config directory used by `profiles` command.

#### `-prp`, `-profile-path`

Delegate to shell file completion.

#### `-asc`, `-add-shell-context`

Suggest shell context names from `<config-dir>/shellContexts/*.json`.

#### `-pd`, `-photo-dir`, `-vd`, `-video-dir`

Delegate to shell directory completion.

#### `-cm`, `-chat-model`, `-pm`, `-photo-model`, `-vm`, `-video-model`

Phase 1: no dynamic suggestions, only allow free text.

Phase 2 optional:

- suggest models from config defaults
- maybe suggest vendor-prefixed common models

---

### Command-specific positional completion

#### `clai tools <tool-name>`

Suggest tool names from `tools.Registry`.

#### `clai confdir [subpath ...]`

Suggest known registered config subpaths if they are finite and discoverable.
If `confdir` accepts arbitrary descendants, offer file/dir completion after known subpaths.

#### `clai chat ...`

Suggest subcommands:

- `continue`, `c`
- `delete`, `d`
- `list`, `l`
- `help`, `h`

For `chat continue <chatID>` and `chat delete <chatID>`, optional later phase:

- suggest recent chat IDs or indices from persisted chats

#### `query`, `photo`, `video`

These mostly accept free-form text, so once command position is resolved, completion should generally stop or defer to shell default behavior.

---

## Parsing strategy

Do not try to reuse `flag.FlagSet.Parse()` directly for completion decisions. Shell completion often occurs on incomplete argv, such as:

- dangling `-t`
- partial `--pro`
- command not yet chosen
- unterminated quoted strings

Instead build a lightweight completion parser that:

1. walks tokens left to right
2. recognizes known flags and whether they consume the next token
3. tracks the first non-flag command token
4. tracks subcommand state for `chat`, `tools`, `confdir`
5. determines what kind of thing is expected at cursor

This parser should be forgiving and never error on incomplete input.

---

## Suggested implementation phases

### Phase 1: skeleton and static completion

Deliver:

- `clai completion bash`
- `clai completion zsh`
- hidden `clai __complete`
- top-level command completion
- global flag completion
- `chat` subcommand completion
- `tools <tool-name>` completion

This already provides a lot of value.

### Phase 2: dynamic value completion

Deliver:

- tool flag value completion for `-t/-tools`, including comma-separated values
- profile name completion
- shell context name completion
- config subpath completion for `confdir`

### Phase 3: richer dynamic completion

Optional:

- recent chat ID/index completion
- model name suggestions
- contextual descriptions in zsh
- shell fallback hints for file/dir completion

---

## Shell integration approach

### Bash

Provide a function that:

1. reads `COMP_WORDS` and `COMP_CWORD`
2. calls `clai __complete bash ...`
3. parses returned items
4. populates `COMPREPLY`

Keep script minimal. Do not embed command knowledge in the script.

### Zsh

Provide a `_clai` function that:

1. gathers `$words` and `$CURRENT`
2. calls `clai __complete zsh ...`
3. maps results into `compadd`

If descriptions are returned, zsh can display them nicely.

---

## Refactors that will help

### Centralize CLI metadata

Right now command names, aliases, and flags are spread across:

- `main.go` usage string
- `internal/setup.go:getCmdFromArgs`
- `internal/setup_flags.go:parseFlags`

Introduce a small shared command/flag metadata layer.

For example:

- command specs with names, aliases, summary, positional expectations
- flag specs with names, aliases, takes-value, value kind

Then use this metadata for:

- completion
- help generation later if desired
- parser support

This is the single best design investment for keeping completion maintainable.

---

## Testing plan

Follow repo convention: tests first, validate fail, implement, validate pass.

Suggested test layers:

### Unit tests for completion parser

Cases:

- no args => suggest top-level commands
- partial command => filtered commands
- `-` => suggest flags
- `--cha` => suggest `--chat-model` if long-flag style is supported via current parser behavior
- `chat ` => suggest chat subcommands
- `tools ` => suggest tool names
- `-t r` => suggest tool names matching `r`
- `-t rg,fi` => only replace last segment candidates

### Unit tests for dynamic providers

Cases:

- tool provider with local registry
- tool provider with MCP-prefixed names
- shell context discovery from config dir
- profile name discovery

### Golden-ish tests for shell script output

Cases:

- `completion bash` emits wrapper invoking `clai __complete`
- `completion zsh` emits wrapper invoking `clai __complete`

### Integration-ish tests

Exercise the hidden command directly:

- `clai __complete ...` returns stable JSON schema

---

## Risks and edge cases

### 1. Alias noise

Showing both canonical names and aliases can overwhelm users.

Mitigation:

- prefer canonical names in suggestions
- optionally include aliases only on exact-prefix match

### 2. Incomplete shell quoting

Shell completion often sends partially typed tokens.

Mitigation:

- avoid strict parsing
- rely on raw words rather than reparsing shell syntax

### 3. Dynamic initialization cost

`tools.Init()` may be somewhat expensive, especially if MCP setup becomes heavier.

Mitigation:

- only initialize dynamic sources when needed
- cache tool names for the duration of one completion invocation
- avoid network calls in completion path

Completion must feel instant.

### 4. Shell-specific escaping

Returned values may need escaping differences between bash and zsh.

Mitigation:

- return raw candidate values
- let shell wrapper pass them to native completion helpers carefully

### 5. Divergence from actual parser

If completion parser and runtime parser evolve separately, drift returns.

Mitigation:

- centralize command and flag metadata
- keep completion parser intentionally tiny and metadata-driven

---

## Concrete rollout plan

1. **Introduce CLI metadata types**
   - command specs
   - flag specs
   - value-kind enum

2. **Implement completion engine**
   - request/response types
   - forgiving parser
   - top-level command and global flag suggestions

3. **Add visible `completion` command and hidden `__complete` command**
   - update command dispatch
   - keep hidden command out of normal help text unless desired

4. **Implement bash and zsh bootstrap output**
   - wrappers call the hidden command

5. **Add dynamic providers**
   - tools
   - profiles
   - shell contexts
   - confdir subpaths

6. **Add tests for behavior first**
   - parser tests
   - provider tests
   - command output tests

7. **Document installation**
   - README section
   - `clai help` mention of `completion`

---

## Recommended MVP scope

If you want the neat version without overbuilding, ship this MVP:

- `clai completion bash|zsh`
- hidden `clai __complete`
- command completion
- global flag completion
- `chat` subcommands
- `tools <tool-name>`
- `-t/-tools` dynamic tool value completion
- `-p/-profile` and `-asc` dynamic value completion

That covers the most visible and uniquely useful parts while keeping implementation bounded.

---

## My recommendation

Yes — use the binary-driven completion model.

For `clai`, it fits especially well because the CLI already has dynamic state and runtime-discovered values. The cleanest implementation is:

- thin shell wrapper
- hidden machine-readable `__complete` endpoint
- Go-native completion engine driven by shared CLI metadata

If implemented that way, this should remain easy to extend and much less fragile than a static generated completion script.