# Profiles Command Architecture

Command: `clai profiles [list]`

The **profiles** command is a small inspection command that lists the configured profile JSON files under the clai config directory.

Profiles themselves are used primarily via the `-p/-profile` and `-prp/-profile-path` flags on text-like commands (`query`, `chat`, `cmd`). Configuration semantics are described in `CONFIG.md`.

## Entry Flow

```text
main.go:run()
  → internal.Setup(ctx, usage, args)
    → parseFlags()
    → getCmdFromArgs() → PROFILES
    → profiles.SubCmd(ctx, allArgs)
```

## Key Files

| File | Purpose |
|------|---------|
| `internal/setup.go` | Dispatches PROFILES mode |
| `internal/profiles/cmd.go` | Implements `clai profiles` command |
| `internal/utils/config_dir.go` (or similar) | `GetClaiConfigDir()` |
| `internal/utils/json.go` (or similar) | `ReadAndUnmarshal()` helper |

## Behavior

Implemented in `internal/profiles/cmd.go`.

### Supported subcommands

- `clai profiles`
- `clai profiles list`

Any other subcommand returns an error.

### Listing logic

`runProfilesList()`:

1. Resolve config dir (`utils.GetClaiConfigDir()`).
2. Determine `<configDir>/profiles`.
3. If the directory doesn’t exist:
   - prints a warning
   - returns `utils.ErrUserInitiatedExit`.
4. Reads all `*.json` files.
5. For each file, tries to unmarshal a small subset view:

   ```go
   type profile struct {
     Name   string   `json:"name"`
     Model  string   `json:"model"`
     Tools  []string `json:"tools"`
     Prompt string   `json:"prompt"`
   }
   ```

   Malformed profiles are skipped.

6. Backward compatible naming: if `Name` is empty, derive from filename.
7. Prints a small summary block per profile:

   - Name
   - Model
   - Tools
   - First sentence/line from `Prompt`

8. If no valid profiles were found, prints a warning.

Returns `utils.ErrUserInitiatedExit`.

## Developer notes

- This command is intentionally conservative: it does not validate the full profile schema; it only displays what it can read.
- Creation/editing of profiles is done via `clai setup` (stage `2`).
