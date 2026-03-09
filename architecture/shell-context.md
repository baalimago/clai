# Shell Context (ASC) Architecture

This document specifies the **auto-append shell context** feature (ASC): a configurable mechanism to render runtime “shell context” and inject it into the **system prompt** for text queries.

ASC is enabled by selecting a named shell-context definition file, which:
- defines a **template** (Go `text/template`) used to render the shell context block
- defines a **vars** map where each variable value is produced by running a command in a subprocess shell
- defines execution settings such as which shell to run and per-variable timeout

---

## User-facing behavior (end-to-end)

### CLI
ASC is enabled by selecting a context **by name**:

- `-asc <name>` (short)
- `-add-shell-context <name>` (long)

If the flag is **not** provided (empty name), no shell context is injected.

Examples:
```bash
clai -add-shell-context minimal q "why is my build slow?"
clai -asc git q "what changed since yesterday?"
```

### Profiles
Profiles add an **optional** field:

- `shell-context`: string

If set, it provides the default shell context name for that profile.

### Precedence / override cascade
ASC follows the existing configuration precedence conventions:

`defaults  <  config file(s)  <  profile  <  CLI flags`

Meaning:
- a profile may specify a default shell context
- CLI flags override the profile selection

---

## Where ASC is applied (prompt assembly)

ASC must **not** change stdin handling or token replacement.

Prompt assembly currently happens in `text.Configurations.SetupInitialChat(...)` (see `architecture/query.md`). The flow is:

1. Build the system prompt normally from `tConf.SystemPrompt`
2. If `tConf.ShellContext` (string name) is non-empty:
   - load the selected shell context definition (see below)
   - evaluate its variables by running subprocess commands
   - render the template into a text block
   - inject that block into the **system prompt**
3. Build the user prompt normally:
   - `prompt := utils.Prompt(tConf.StdinReplace, args)`
   - (glob/reply context logic remains unchanged)
4. Continue existing image detection, message append, chat ID creation, etc.

This keeps ASC orthogonal to:
- stdin piping
- `{}` / `-I` replacement
- globbing
- reply/dir-reply context

### Current injection format
The rendered shell context is inserted using the existing helper wrapper format:

```text
<shell context>
...rendered template output...
</shell context>
...system prompt...
```

This means the shell context is visible to the model as part of the system message, while the user message remains unchanged.

### Failure behavior
ASC is **best-effort**:
- if loading or rendering the shell context fails during system-prompt setup, clai prints a warning and continues without shell context
- user prompt assembly and query execution continue normally

---

## On-disk layout

ASC is configured via **multiple named files** in a directory under the clai config dir.

Directory:

- `<configDir>/shellContexts/`

Files:

- `<configDir>/shellContexts/<name>.json`

The name used in `-add-shell-context <name>` (and profile `shell-context`) maps directly to `<name>.json`.

On first run / setup, clai should ensure:
- the `shellContexts/` directory exists
- at least one default context exists (e.g. `minimal.json`)

---

## Setup UX (create/edit contexts)

Shell contexts are managed via the interactive `clai setup` flow (similar to other JSON configs).

### Editing hotkeys (selected context)

When a shell context is selected in setup:

- **`e`**: Edit the entire JSON config for the context in the user’s editor.
  - This is for power users who want full control over all fields.
  - After edit, clai should re-parse JSON and validate the template compiles.

- **`t`**: Edit the `template` field in the user’s editor (template-only editing).
  - This exists because `template` is a JSON-escaped string; editing it inside raw JSON is error-prone.
  - Template-only editing opens the *unescaped* template text in the editor and, on save:
    - validates it compiles as a Go `text/template`
    - writes it back into the JSON (properly escaped via JSON marshal)

Template-only editing is analogous to how prompt editing is handled elsewhere in setup.

---

## Shell-context JSON schema (fully scoped)

Each file `shellContexts/<name>.json` describes:

- which shell to spawn for commands
- per-variable timeout
- placeholder values for timeouts and errors
- a Go template used to format the rendered shell context block
- a set of variables (command map)

Proposed schema:
```json
{
  "shell": "/bin/zsh",
  "timeout_ms": 100,
  "timed_out_value": "<timed out>",
  "error_value": "<error>",
  "template": "[Shell context]\n{{- if .cwd }}cwd: {{.cwd}}\n{{- end }}",
  "vars": {
    "cwd": "pwd",
    "git_branch": "git branch --show-current 2>/dev/null"
  }
}
```

### Field meanings

- `shell` (string): Shell binary used to run var commands.
  - If empty, the implementation should default to:
    1. `$SHELL` if set
    2. fallback `sh` (via PATH) or `/bin/sh`

- `timeout_ms` (number): Per-variable command timeout in milliseconds.
  - Default: `100`

- `timed_out_value` (string): Value substituted for a variable when the command times out.
  - Default: `"<timed out>"`

- `error_value` (string): Value substituted for a variable when the command fails (non-zero exit, missing binary, etc.).
  - Default: `"<error>"` (may be empty string if desired)

- `template` (string): Go `text/template` template used to render the final shell context block.
  - Variables are accessed as `{{.varName}}`.
  - Templates may use conditionals (`if`, `with`), loops (`range`), etc.

- `vars` (map[string]string): Map of template variable name to shell command.
  - Each command is executed and its stdout becomes the variable value (after trimming/bounding).

---

## Command execution model

Each variable command is executed in a subprocess shell.

### Invocation
For each var `(name, cmd)`:
- run: `<shell> -c <cmd>`
- capture stdout (and optionally stderr for debugging)

### Timeout
- Each var is executed with a timeout from `timeout_ms`.
- If the command exceeds the timeout:
  - the variable value becomes `timed_out_value`
  - a warning is printed via `ancli` (not direct stderr writes), so it is visible and consistent with other output.

Example warning:
```text
shell-context "minimal": var "git_branch" timed out after 100ms; using "<timed out>"
```

### Errors
If the command fails quickly (non-zero exit, exec failure):
- variable value becomes `error_value`
- warnings are optional; at minimum, timeouts must warn

### Output normalization and bounds
To prevent prompt bloat and unsafe output sizes:
- trim whitespace/newlines from stdout
- cap stdout bytes per var (recommended)
- cap total rendered block size (recommended)

---

## Template rendering

Use Go `text/template` to render the configured `template` using a data object:

- `map[string]string` where keys are var names and values are command outputs (or timeout/error placeholders)

This enables user-authored rich contexts, e.g.:

```gotemplate
[Shell context]
wd: {{.cwd}}
{{- if .git_branch }}
git: {{.git_branch}}
{{- end }}
```

If template parsing or execution fails:
- the shell context should be omitted from the system prompt
- clai should continue best-effort
- optionally warn under `DEBUG`

---

## Configuration + wiring points (code-level)

### Flag parsing
- `internal/setup_flags.go`:
  - add string flags `-asc` and `-add-shell-context`
  - they are mutually exclusive (same short/long behavior as other flags)
  - store result in `internal.Configurations.ShellContext` (string)

### Text configuration
- `internal/text.Configurations`:
  - add `ShellContext string`

### Profiles
- profile parsing/application:
  - add optional profile field `shell-context`
  - apply during profile override step

### Prompt assembly
- in `internal/text/conf.go`:
  - during `setupSystemPrompt(...)`, if `ShellContext != ""`:
    - render shell context block
    - inject it into the system prompt
  - user prompt assembly via `utils.Prompt(...)` remains unchanged

---

## Testing requirements

Tests must be deterministic and avoid depending on the developer machine environment.

Recommended approach:
- implement command execution via an injected runner (func/interface), allowing tests to:
  - return fixed stdout per var
  - simulate timeouts
- add an end-to-end-ish contract test:
  - run `clai -r -cm test -add-shell-context minimal q hello`
  - assert stdout remains just the user-visible prompt/response path, i.e. shell context is **not** echoed as part of the user message
- add a test verifying timeout behavior:
  - a var exceeding `timeout_ms` yields `timed_out_value`
  - a warning is printed (via `ancli`) for each timed-out var

All tests should be run with:
```bash
go test ./... -race -cover -timeout=30s
```
