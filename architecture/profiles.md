# Profiles Command Architecture

Command: `clai profiles [list|install|update|trust|publish]`

The **profiles** command lists the configured profile JSON files under the clai config directory. It also installs, updates and trusts remote profile targets (`cchub:`, `gh:`, `https://`); [remote-targets.md](./remote-targets.md) covers those. `publish` sends a local profile to the Hub (below).

Profiles themselves are used primarily via the `-p/-profile` and `-prp/-profile-path` flags on text-like commands (`query`, `chat`, `cmd`). Configuration semantics are described in `CONFIG.md`.

Loading a profile (`-p` or `-prp`) is read-only (`utils.ReadConfigFile`): fields missing from the file are filled from `DefaultProfile` in memory, the file is never rewritten, nothing is created beside it, and a missing profile file is an error.

## Entry Flow

```text
internal/cli:Run()
  → cmd.Run(...)                  # go_away_boilerplate/pkg/cmd dispatch
    → profiles command Run (internal/profiles/cmd.go)
      → profiles.List()
```

## Key Files

| File | Purpose |
|------|---------|
| `internal/profiles/cmd.go` | profiles command + `list`, `trust`, `install`, `update`, `publish` subs (see [cmd-dispatch.md](./cmd-dispatch.md)) |
| `internal/profiles/publish.go` | `CommandDeps.Publish`, `publishCommand`; `PublishRequest`, `Published`, `HubPublisher`, `HandleRequiredError` |
| `internal/cli/cli.go` | `hubPublisher`: posts to `/api/v1/hub` with the `clai cloud login` key; the secret check is `sync.FindSecret` |
| `internal/profiles/remote.go` | `CommandDeps.Trust`, `Install`, `Update` and the `source` stamp |
| `internal/profiles/cmd.go` | Implements `clai profiles` command |
| `internal/utils/config_dir.go` (or similar) | `GetClaiConfigDir()` |
| `internal/utils/json.go` (or similar) | `ReadAndUnmarshal()` helper |

## Behavior

Implemented in `internal/profiles/cmd.go`.

### Supported subcommands

- `clai profiles`
- `clai profiles list`
- `clai profiles trust <target>`
- `clai profiles install <target> [name]`
- `clai profiles update [name]`
- `clai profiles publish <profile> [-name] [-title] [-summary] [-note] [-handle]`

Any other subcommand returns an error.

### Publishing

`publish` reads `<config>/profiles/<profile>.json`, or the path given when
it holds a separator or ends in `.json`, and sends the bytes as they are on
disk to clai.chat's `POST /api/v1/hub` as the account of the
`clai cloud login` key (`CLAI_CLOUD_TOKEN` and `CLAI_CLOUD_URL` stand in, as
for sync). Before anything leaves the machine it runs `remote.Validate`,
the strict check `-prp` gives a remote profile, and `sync.FindSecret`; a
failure of either sends nothing. The name defaults to the file's base name
and must be a Hub name. The first publish of a name creates the agent; the
same name again is its next version, with `-note` as the changelog. An
account without a handle gets `HandleRequiredError` with the suggested
handle, printed as the command to rerun with `-handle`. Output is the
address and version, the page URL, and the `-prp` line pinned by the
returned sha256. Flags may follow the profile: `publishCommand` parses
what comes after it again. The profiles command's config prep may have
added default fields to the file first, so the published bytes, and their
hash, are the upgraded file.

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
   - Source, when the profile was installed from a remote target
   - First sentence/line from `Prompt`

8. If no valid profiles were found, prints a warning.

Returns `utils.ErrUserInitiatedExit`.

## Developer notes

- This command is intentionally conservative: it does not validate the full profile schema; it only displays what it can read.
- Creation/editing of profiles is done via `clai setup` (stage `2`).
