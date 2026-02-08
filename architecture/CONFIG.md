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
- globbing selection (via `-g` flag which then modifies prompt building)

Mode config loading happens inside `internal.setupTextQuerierWithConf` / `internal.Setup`:

- `utils.LoadConfigFromFile(confDir, "textConfig.json", migrateOldChatConfig, &text.Default)`
- `utils.LoadConfigFromFile(confDir, "photoConfig.json", migrateOldPhotoConfig, &photo.DEFAULT)`
- `utils.LoadConfigFromFile(confDir, "videoConfig.json", nil, &video.Default)`

`LoadConfigFromFile` is responsible for:

- creating the file from defaults if it doesn’t exist
- `json.Unmarshal` into the provided struct
- optionally running a migration callback

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

Profiles are created/edited via `clai setup` (stage 2), and inspected via `clai profiles list`.

Profiles are applied inside `text.Configurations.ProfileOverrides()` (see `internal/text/conf.go` + `internal/text/profile_overrides.go` if present).

### 4) Conversations and reply pointers (context state)

Stored under:

- `<config>/conversations/*.json`
- `<config>/conversations/prevQuery.json` (global reply context)
- `<config>/conversations/dirs/*` (directory-scoped binding metadata)

These are described in `architecture/CHAT.md`.

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
- `-dre` is implemented in `internal.Setup` by copying the directory-scoped conversation into `prevQuery.json` and then turning on reply mode.

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

### Reply/dir-reply configuration

- `-re` sets `tConf.ReplyMode`.
- `-dre` is handled before text setup:
  - `chat.SaveDirScopedAsPrevQuery(confDir)`
  - flips reply mode on

This means the rest of the system only needs to understand one reply mechanism: loading `prevQuery.json`.

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

- Set `DEBUG=1` to print some config snapshots during setup.
- `DEBUG_PROFILES=1` prints tooling glob selection during setup.
- Most “why isn’t my flag working?” issues are precedence/cascade issues; trace:
  1. mode config loaded
  2. early flag overrides
  3. profile overrides
  4. tool selection
  5. late overrides
  6. initial chat construction
