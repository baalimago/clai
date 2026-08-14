# Configuration Architecture

This document describes **how configuration works** in clai: where config is stored, how files are created, and how the *override cascade* is applied (defaults → mode config → model-specific config → profiles → flags).

It is the “index” doc for understanding why a command behaves the way it does.

## Config directories

A clai install uses two primary directories:

- **Config dir**: `utils.GetClaiConfigDir()` ⇒ typically:

  ```text
  <os.UserConfigDir()>/ .clai
  ```

- **Cache dir**: `utils.GetClaiCacheDir()` ⇒ typically:

  ```text
  <os.UserCacheDir()>/ .clai
  ```

On startup, `main.run()` ensures the config dir exists:

- `utils.CreateConfigDir(configDirPath)`

The config dir is also printed in `clai help` (see `main.go` usage template).

## Config file types

There are *three* main axes:

1. **Mode configs** (coarse per-command defaults)
2. **Model-specific vendor request configs** (fine-grained provider settings)
3. **Profiles** (workflow presets that override mode+model config)

Plus chat transcripts and reply pointers, which aren’t “config” but strongly affect behavior.

### 1) Mode configs

Stored at:

- `<config>/textConfig.json`
- `<config>/photoConfig.json`
- `<config>/videoConfig.json`

They contain settings that are broadly applicable to that “mode” (text vs image vs video). For text this includes:

- chosen model
- printing options (raw vs glow)
- system prompt
- tool use selection defaults
- tool-call budget (`max-tool-calls`; nil or 0 = unlimited)
- token stoploss policy (`stoploss`: `max-tokens` + `max-tokens-handover-instructions` + `max-tool-calls-after-handover`; absent or 0 = unlimited post-handover tools)
- globbing selection (via `-g` flag which then modifies prompt building)

The pre-query interactive token-count warning prompt is **sunset**: a legacy
config key for it is ignored if present in old configs (encoding/json drops
unknown keys, so no migration is needed), and the stoploss replaces it as the
context guard.

Mode configs are migrated from a united place: `internal.Setup` upgrades all
three files before mode dispatch, so every command sees the current schema
(completion commands bypass this deliberately):

- `utils.LoadConfigFromFileCollect(confDir, "textConfig.json", migrateOldChatConfig, &text.Default)`
- `utils.LoadConfigFromFileCollect(confDir, "photoConfig.json", migrateOldPhotoConfig, &photo.DEFAULT)`
- `utils.LoadConfigFromFileCollect(confDir, "videoConfig.json", nil, &video.Default)`

The per-mode loads inside `internal.setupTextQuerierWithConf` / `internal.Setup`
remain, but are idempotent after the united migration.

`LoadConfigFromFile` is responsible for:

- creating the file from defaults if it doesn’t exist
- `json.Unmarshal` into the provided struct
- optionally running a migration callback
- **upgrading the file in place**: keys absent from the on-disk JSON are filled
  from the non-zero defaults (recursively into nested objects) and the file is
  rewritten. Fields tagged `migrate:"true"` are filled even when their default
  is the zero value, so new feature knobs whose natural default is 0 (e.g.
  `stoploss.max-tool-calls-after-handover`, 0 = unlimited) surface in upgraded
  configs. Keys already present are never touched, even when their value is
  the zero value — the file is the user’s source of truth. When a
  pre-existing file is upgraded, clai announces the added fields, e.g.
  `added new field(s) to textConfig.json: stoploss`. This is what appends the
  disabled `stoploss` template (`max-tokens: 0` + the default handover
  message) to configs that predate the feature.

`LoadConfigFromFileCollect` is the same loader without the stdout
announcement: it returns the added field paths instead. `internal.Setup` uses
it for the united migration and prints the announcements just before the
interactive setup wizard starts. The wizard's TUI redraws by clearing its own
frame plus one line above the header (`go_away_boilerplate/pkg/table`
`ClearTermTo` clears `upTo+1` lines), so the announcement block ends with a
blank separator line that absorbs the overshoot; without it the first redraw
would wipe the announcement. Deep multi-table navigation can still scroll it
off (each `Run()` exit consumes one line). All other commands announce
immediately.

Raw (machine-readable) runs — `-r`/`-raw` — are read-only: `internal.Setup`
sets `utils.ReadonlyConfig` before any load, and the loaders fill missing
fields in memory but never rewrite the config files and never print upgrade
announcements. This keeps shell hooks and scripts that call clai (e.g. a zsh
precmd running `clai -r chat dirv2`) from silently migrating the user’s
configs before their own commands run, and from corrupting machine output
with the human announcement. The migration then happens on the next
interactive (non-raw) run, where the announcement is visible.

Read-only chat subcommands — `chat list`, `chat dir`, `chat dirv2`, and
`chat help` — go further: `internal.Setup` sets `utils.NoCreateConfig` before
any load, so no config dir, default config file, migration callback, or model
querier is produced as a side effect. This lets those commands run against a
read-only filesystem or a config dir that does not exist yet; a missing config
file simply yields the in-memory defaults. The conversation index cache is
treated as an optimization: when `utils.NoCreateConfig` is set, a missing
cache is rebuilt in memory for listing, but the rebuild is silent (no
"Building cache index" progress chatter) and the persist attempt is skipped,
so a read-only mount produces no stderr noise and no failed write.

### 2) Model-specific vendor configs

These are JSON files created per *vendor+model*.

They exist because different vendors expose different request options and clai avoids a combinatorial CLI flag explosion.

Location:

- `<config>/<vendor>_<model-type>_<model-name>.json`

Example (illustrative):

- `openai_gpt_gpt-4.1.json`
- `anthropic_chat_claude-sonnet-4-20250514.json`

Creation/loading typically occurs during querier creation (`CreateTextQuerier`, `CreatePhotoQuerier`, etc.) and is vendor-specific.

**Important characteristic**:

> These JSON files are effectively “request templates” that are unmarshaled into whatever request struct the vendor implementation uses.

That is why setup exposes them as “model files” rather than as first-class flags.

### 3) Profiles

Profiles are stored as:

- `<config>/profiles/<name>.json`

Profiles are applied only for text-like modes (query/chat/cmd) and are intended to:

- quickly switch prompts/workflows
- pin a model
- restrict or expand tool choices
- opt into skills for a specialized workflow

Profiles are created/edited via `clai setup` (stage 2), and inspected via `clai profiles list`.

Profiles are applied inside `text.Configurations.ProfileOverrides()` (see `internal/text/conf.go` + `internal/text/profile_overrides.go` if present).

### 4) Conversations and reply pointers (context state)

Stored under:

- `<config>/conversations/*.json`
- `<config>/conversations/globalScope.json` (global reply context)
- `<config>/conversations/dirs/*` (directory-scoped binding metadata)

These are described in `architecture/chat.md`.

They aren’t traditional config, but they influence prompt assembly (`-re`, `-dre`, `chat continue`, etc.).

## The override cascade (text/query/chat/cmd)

Text-like commands are configured in `internal/setup.go:setupTextQuerierWithConf`.

The effective precedence is:

1. **Hard-coded defaults** (`text.Default`) – lowest precedence
2. **Mode config file** (`textConfig.json`)
3. **Profiles** (`-p/-profile` or `-prp/-profile-path`)
4. **Flags** (CLI)

There is also a *model-specific vendor config* layer which is loaded during querier creation.

A more faithful mental model:

```text
text.Default
  → merge textConfig.json
  → apply “early” flag overrides (model/raw/reply/profile pointers)
  → if glob mode: build glob context
  → apply profile overrides (prompt/tools/model/etc)
  → finalize tool selection (flags + profiles + defaults)
  → re-apply “late” overrides (some flags override profile, e.g., -cm)
  → build InitialChat (including reply context)
  → CreateTextQuerier(...) loads vendor model config and produces runtime Model
```

### Where flags apply

Flags are parsed in `internal/setup_flags.go:parseFlags` into `internal.Configurations`.

For **text** the important override functions are:

- `applyFlagOverridesForText(tConf, flagSet, defaultFlags)`
- `applyProfileOverridesForText(tConf, flagSet, defaultFlags)` (currently only ensures `-cm` can override profile model)

Key behaviors:

- default flags should *not* override file values; overrides only happen when the user provided a non-default flag value.
- `-mt`/`-max-tokens` and `-mtc`/`-max-tool-calls` override the run limits: an explicit `0` disables the corresponding file limit (unlimited), and an explicit `-max-tokens=N` keeps the configured handover message.
- `-dre` is handled before text setup by flagging reply mode and letting
  `setupTextQuerierWithConf` load the directory-scoped head directly into
  `InitialChat` (via `chat.LoadDirScopedContext`).

### Tool selection configuration

Tool usage is controlled by:

- `-t/-tools` CLI flag (string): `""`, `"*"`, or comma-separated list.
- `text.Configurations.UseTools` boolean (enable tool calling)
- `text.Configurations.RequestedToolGlobs` (names or wildcards)
- profiles can also set tool behavior

`internal/setup.go:setupToolConfig` is the bridge between:

- CLI’s `UseTools` string
- text configuration’s `UseTools` + `RequestedToolGlobs`

Notable rules:

- if `-t` is provided at all (even a list), it is interpreted as intent to enable tooling.
- `-t=*` clears requested list (meaning “allow all”).
- unknown tools are skipped with warnings.
- if nothing valid remains, tooling is disabled for that run.
- MCP tools are not validated against the local registry; names prefixed with `mcp_` are allowed.

### Command ban configuration

The freetext command tools (`cmd`, `async_cmd`; legacy aliases
`freetext_command`, `async_cmd_run`) can be restricted with a command ban
list. The effective list is purely additive (D5): no source removes another
source's bans.

- `textConfig.json`: `"cmd-ban": ["rm", "git commit"]`
- profiles: `"cmd-ban": ["git commit"]` — merges onto the file base only
  when explicitly set; an omitted or explicitly empty list contributes
  nothing
- CLI: `-cmd-ban=rm,git commit` — appends, comma-separated
- agent API: `WithCmdBanList(...)` (see `pkg/agent`)

A command matching any entry is refused before it spawns; the refusal names
the matched entry and does not abort the run. See `architecture/tooling.md`
(Security, "Command ban list") for the matching semantics and documented
limits.

### Skills enablement configuration

Skills are controlled as an explicit opt-in subsystem.

- `text.Configurations.UseSkills` boolean enables skills at the text-config/runtime layer
- profiles may optionally set `use_skills`
- `-s/-skills` is a string flag with special values, mirroring the parser style of `-t/-tools`

Rules:

- default is disabled
- omitted `use_skills` in existing config or profile files must continue to work for backwards compatibility
- setup-generated and migrated config surfaces should include `use_skills` so the feature becomes visible without breaking existing user files
- CLI has highest precedence:

```text
-s=*      => enable skills for the run
-s=none   => disable skills for the run
omitted   => no CLI override
```

Effective precedence:

```text
flags > profile > textConfig > default(false)
```

### Reply/dir-reply configuration

- `-re` sets `tConf.ReplyMode`.
- `-dre` is handled before text setup:
  - flips reply mode on
  - `setupTextQuerierWithConf` loads the directory-scoped head directly into `InitialChat` (via `chat.LoadDirScopedContext`)

`-re` continues to use `globalScope.json`. `-dre` bypasses it entirely,
loading the directory binding’s conversation directly.

## Non-text config flows

### Photo

- Load `photoConfig.json` (with default `photo.DEFAULT`)
- Apply flag overrides: model, output dir/prefix/type, reply and stdin replacement
- Build prompt via `photo.Configurations.SetupPrompts()`
- Create vendor querier via `CreatePhotoQuerier(pConf)`

See `PHOTO.md`.

### Video

Same pattern with `videoConfig.json` + `video.Configurations.SetupPrompts()`.

See `VIDEO.md`.

## Setup wizard and config file editing

`clai setup` is the primary user interface to edit all of these files.

It uses globbing under the config dir to find relevant files and offers actions:

- reconfigure via structured prompts
- open in `$EDITOR`
- delete
- paste or create MCP server definitions

See `SETUP.md`.

## Implementation index

If you need to follow configuration in code, start here:

- `internal/setup_flags.go`
  - CLI flags → internal struct
  - applies overrides into mode configs
- `internal/setup.go`
  - command dispatch
  - text setup (`setupTextQuerierWithConf`) and special cases (`-dre`)
- `internal/utils/config.go` + `internal/utils/json.go`
  - `LoadConfigFromFile`, `CreateFile`, etc.
- `internal/text/conf.go`
  - text defaults, initial chat setup, reply/glob integration
- `internal/create_queriers.go`
  - model name → vendor querier routing

## Common debugging tips

Every feature-scoped debug switch follows one scheme, resolved in
`internal/debugflags`: plain `DEBUG=1` enables all of the switches below,
`DEBUG_<SUBSYSTEM>=1` enables one.

| Env var | Effect |
|---|---|
| `DEBUG=1` | Global; enables every subsystem below plus config snapshots during setup |
| `DEBUG_CHAT=1` | `[DEBUG_CHAT]` trace lines in chat and text flows |
| `DEBUG_DIRSCOPE=1` | `[DEBUG_DIRSCOPE]` binding/history/search internals |
| `DEBUG_LOOKBACK=1` | Lookback setup notices |
| `DEBUG_SKILLS=1` | Skill discovery and activation logging |
| `DEBUG_PROFILES=1` | Tooling glob selection during setup |
| `DEBUG_CALL=1` | Tool call payloads |
| `DEBUG_MCP_TOOL=1` | MCP tool request/response details |
| `DEBUG_TOOLS_REGISTRY_SET=1` | Tool registry set operations |
| `DEBUG_TEXT_QUERIER=1` | Querier setup internals |
| `DEBUG_COST_MANAGER=1` | Cost manager internals |
| `DEBUG_STOPLOSS=1` | Stoploss budget decisions |
| `DEBUG_CPU=1` | CPU profiling to `cpu_profile.prof` |
| `DEBUG_REPLY_MODE=1` | Chat save-path diagnostics |
| `DEBUG_VERBOSE=1` | Additional verbose request diagnostics |
| `DEBUG_CLAUDIFIED_MSGS=1` | Anthropic message transformation diagnostics |

`DEBUG_OUTPUT_FILE` selects a debug trace file. Per-vendor stream debug uses `<VENDOR>_DEBUG` or `DEBUG_<VENDOR>` env vars
(e.g. `OPENAI_DEBUG`, `DEBUG_MISTRAL`, `OLLAMA_DEBUG`), also activated by
plain `DEBUG=1`.

Most “why isn’t my flag working?” issues are precedence/cascade issues; trace:
  1. mode config loaded
  2. early flag overrides
  3. profile overrides
  4. tool selection
  5. late overrides
  6. initial chat construction
