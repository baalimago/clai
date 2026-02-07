# Setup Command Architecture

Command: `clai [flags] setup` (aliases: `s`)

The **setup** command is an interactive configuration wizard. It helps users create and edit:

- mode config files (`textConfig.json`, `photoConfig.json`, `videoConfig.json`)
- model-specific vendor config files (e.g. `openai_gpt_gpt-4.1.json`)
- profiles (`<config>/profiles/*.json`)
- MCP server definitions (`<config>/mcpServers/*.json`)

It is intentionally a “manual editing UI” rather than a declarative config generator.

## Entry Flow

```text
main.go:run()
  → internal.Setup(ctx, usage, args)
    → parseFlags()
    → getCmdFromArgs() → SETUP
    → setup.SubCmd()
```

`internal.Setup` treats this as a user-initiated command and returns `utils.ErrUserInitiatedExit` after the wizard completes.

## Key Files

| File | Purpose |
|------|---------|
| `internal/setup.go` | Dispatch to `setup.SubCmd()` |
| `internal/setup/setup.go` | Main interactive wizard flow and top menu |
| `internal/setup/setup_actions.go` | The concrete actions (configure, delete, create new, editor-based edits) |
| `internal/setup/mcp_parser.go` | Parses pasted MCP server JSON (Model Context Protocol configuration) |
| `internal/utils/*` | File creation, JSON marshal/unmarshal, user input helpers, editor invocation |
| `internal/text/conf.go` | Provides `text.DefaultProfile` template and config defaults |

## Wizard stages (interactive UI)

The UI begins with `stage_0`:

```text
0. mode-files
1. model files
2. text generation profiles
3. MCP server configuration
```

### Stage 0 → Mode-files (selection `0`)

- Uses `getConfigs(<claiDir>/*Config.json, exclude=[])`.
- Immediately enters `configure(configs, conf)`.

Intent: let users quickly edit top-level per-mode config such as `textConfig.json`.

### Stage 0 → Model files (selection `1`)

- Globs `<claiDir>/*.json` excluding `textConfig`, `photoConfig`, `videoConfig`.
- Prompts for an action: `configure`, `delete`, `configure with editor`.

These are vendor/model-specific “raw request config” JSON files (see `CONFIG.md`).

### Stage 0 → Profiles (selection `2`)

- Operates in `<claiDir>/profiles/*.json`.
- Prompts for an action: configure / delete / create new / configure with editor / prompt edit with editor.
- If “create new” is chosen:
  - asks for profile name
  - writes `<name>.json` using `text.DefaultProfile`
  - then falls through into configuration step.

### Stage 0 → MCP servers (selection `3`)

- Operates in `<claiDir>/mcpServers/*.json`.
- Ensures at least one server exists by writing `everything.json` with `defaultMcpServer` if the directory is absent.
- Prompts for an action: configure / delete / create new / configure with editor / paste new config.

Special flow: **paste new config**

- Reads stdin until `Ctrl+D` or a literal `EOF` line.
- Parses JSON that contains `{"mcpServers": {...}}` via `ParseAndAddMcpServer`.
- Writes one server file per entry (e.g. `<serverName>.json`).

## Actions

Actions are defined as an enum-like type `action`:

- `conf` – reconfigure JSON by asking questions in-terminal (structured prompts)
- `confWithEditor` – open full JSON in `$EDITOR`
- `promptEditWithEditor` – open *only the prompt field* in `$EDITOR` (profiles)
- `del` – delete file
- `newaction` – create new profile / MCP server file

The implementation details live in `internal/setup/setup_actions.go`.

## Error handling and exit codes

- Any filesystem or parse errors are returned with context and cause a non-zero exit.
- Explicit quit commands (`q/quit/e/exit`) return `utils.ErrUserInitiatedExit`.

## Developer notes

- Setup is a user-driven wizard. Adding a new config category generally means:
  1. adding a new top-level menu choice in `stage_0`
  2. adding a new `getConfigs` glob
  3. implementing a `configure(...)` action for the new file type
- MCP “paste” support is the quickest way for users to onboard external tools without manually crafting many files.
