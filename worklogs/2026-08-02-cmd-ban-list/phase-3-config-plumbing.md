# Phase 3 — Config plumbing

**Status:** Complete

[← README](./README.md)

## Goal

Expose the ban list through the full config cascade — `textConfig.json`,
profiles, the `-cmd-ban` CLI flag, and the public agent API — and inject the
effective list into `pkg/tools` per run.

## Specification

### Modified components

- **`internal/text/conf.go`** (struct fields) and
  **`internal/text/conf_profile.go`** (`ProfileOverrides`, Review 2 R2-05):
  - `Configurations` gains `CmdBan []string `json:"cmd-ban"`` (base list).
  - `Profile` gains `CmdBan []string `json:"cmd-ban,omitempty"``.
  - `ProfileOverrides()` APPENDS `profile.CmdBan` to `c.CmdBan`
    (`c.CmdBan = append(c.CmdBan, profile.CmdBan...)`) ONLY when the profile
    explicitly sets `cmd-ban` (`profile.CmdBan != nil`); a profile that omits
    `cmd-ban` contributes nothing. The cascade is purely additive: no source
    removes another source's bans — a ban is removed by editing the source
    that added it. An explicit `cmd-ban: []` contributes nothing (it is NOT a
    clear switch; 2026-08-02 revision of Review 1 R1-04; D5).
- **`internal/setup_flags.go`**:
  - Flag-side `Configurations` gains `CmdBan string` (raw comma-separated
    flag value, mirroring `UseTools`).
  - `parseFlags` registers `-cmd-ban` (string flag; no short form to avoid
    flag-soup; documented in `main.go` usage).
- **`internal/setup.go`**:
  - New `setupCmdBanConfig(tConf *text.Configurations, flagSet Configurations)`
    — when `flagSet.CmdBan != ""`, split on commas, trim spaces, drop empties,
    and **append** to `tConf.CmdBan` (flag can only add restrictions, D5).
    Called from `setupTextQuerierWithConf` after `setupToolConfig`.
- **`internal/text/querier_setup.go`** (`NewQuerier`):
  - Before `setupTooling(...)`, call `tools.SetCmdBanList(userConf.CmdBan)`.
    Unconditional (D6): the default empty list keeps behavior permissive when
    no bans are configured.
- **`pkg/agent/agent.go`**:
  - `Agent` gains `cmdBan []string`; `WithCmdBanList(entries ...string) Option`.
  - `asInternalConfig()` passes `CmdBan: a.cmdBan`.
- **`main.go`**: usage line for `-cmd-ban` next to the `-t/-tools` entry.

### Cascade (D5, purely additive)

There is no precedence — sources only add. Effective list =
default(empty) + textConfig.json (`cmd-ban`) + profile (`cmd-ban`, only when
explicitly set) + flag (`-cmd-ban`). Nothing removes anything; a ban is
removed by editing the source that added it (2026-08-02 revision of Review 1
R1-04). No config migration needed (D11): absent fields unmarshal to empty.
No setup-wizard changes (D10): the interactive editor handles the new JSON
key generically via `editSlice`.

### What it does NOT do

- Does NOT apply bans when tools are disabled (`UseTools=false`): no command
  tools are registered, so nothing to protect; the setter still runs
  harmlessly.
- Does NOT touch MCP tool config or skills.

## Integration contract

| Scenario | Input | Observable result |
|----------|-------|-------------------|
| textConfig.json has `"cmd-ban": ["rm"]`, no profile/flag | `clai q ...` with tools | `tools.SetCmdBanList(["rm"])` called; `cmd` refuses `rm -rf /` |
| Profile has `"cmd-ban": ["git commit"]` | `clai -p gopher q ...` | Profile list merges onto the file base (additive); file + profile bans all apply |
| File base `["rm"]` + flag `-cmd-ban=sudo` | `clai -cmd-ban=sudo q ...` | Effective list `["rm", "sudo"]` (append) |
| File `["rm"]` + profile `["git commit"]` + flag `-cmd-ban=sudo` | all three sources | Effective list `["rm", "git commit", "sudo"]` (union, additive) |
| Agent embeds `WithCmdBanList("rm", "sudo")` | agent run | Effective list `["rm", "sudo"]` |
| No ban config anywhere | any run | `SetCmdBanList(nil)` → permissive (D4) |
| `clai setup` on textConfig.json | wizard | `cmd-ban` key editable as a string slice (generic editor) |

## Acceptance criteria

- [x] `text.Configurations.CmdBan` persists via `textConfig.json` key `cmd-ban` — `TestConfigurations_CmdBanPersistsViaTextConfigJSON`
- [x] `Profile.CmdBan` persists via key `cmd-ban` and merges onto the base in `ProfileOverrides` only when explicitly set (nil-checked append, R1-04 revision) — `TestFindProfile_CmdBanPersistsViaJSON`, `TestConfigurations_ProfileOverrides_CmdBanCascade` (explicit merge / omitted contributes nothing / explicit empty contributes nothing)
- [x] `-cmd-ban=a,b,c` parses; flag entries append to the active list — `TestSetupFlags` cases "Cmd ban flag parses comma-separated entries" + "kept raw for later trimming", `Test_setupCmdBanConfig_AppendsParsedFlagEntries`
- [x] `NewQuerier` calls `tools.SetCmdBanList(userConf.CmdBan)` every run — `Test_NewQuerier_AppliesCmdBanList` (banned command refused after NewQuerier), `Test_NewQuerier_NoCmdBanStaysPermissive` (empty list keeps permissive default)
- [x] `pkg/agent` exposes `WithCmdBanList` and passes it through — `TestAgent_WithCmdBanList`, `TestAgent_WithCmdBanList_PropagatesToInternalConfig`
- [x] `main.go` usage documents `-cmd-ban` — usage const, row next to `-t/-tools` (verified by inspection; no test)
- [x] `go test ./internal/... ./pkg/agent/... -timeout=30s` passes — 32/32 packages ok, 0 failures

## Error coverage

| Failure | Expected outcome |
|---------|-----------------|
| `-cmd-ban=rm, sudo` (stray space) | Entries trimmed; `["rm", "sudo"]` |
| `-cmd-ban=rm,,sudo` (empty segment) | Empty segments dropped |
| Profile + flag both set | File + profile + flag union (additive) |
| Malformed textConfig.json | Existing `LoadConfigFromFile` error path, unchanged |
| Agent with no `WithCmdBanList` | `cmdBan` nil → permissive |
| Profile active without `cmd-ban` | Contributes nothing; file + flag list unchanged (nil-checked, R1-04 revision) |
| Profile with explicit `cmd-ban` + file base | Both apply (merge, additive — R1-04 revision) |

## Implementation notes

Executing agent: clai (worker session 2026-08-02-03).

- `Configurations.CmdBan` (`json:"cmd-ban"`) and `Profile.CmdBan`
  (`json:"cmd-ban,omitempty"`) were added to `internal/text/conf.go`; the
  `omitempty` on the profile field keeps profiles without `cmd-ban` clean on
  rewrite, matching the existing `tools`/`mcp_servers` conventions. No config
  migration is needed (D11): absent fields unmarshal to nil/empty.
- `ProfileOverrides` merges with a nil-guarded append
  (`if profile.CmdBan != nil { c.CmdBan = append(c.CmdBan, profile.CmdBan...) }`),
  placed right after the `RequestedToolGlobs` assignment. Omitted and
  explicitly-empty `cmd-ban` both contribute nothing (R1-04 revision, D5).
- The flag-side `Configurations` (internal/setup_flags.go) carries `CmdBan`
  as the raw comma-separated string, mirroring `UseTools`. `parseFlags`
  registers `-cmd-ban` with no short form (spec); the raw value is kept
  untouched so `setupCmdBanConfig` owns trimming/splitting, exactly like
  `setupToolConfig` owns `-t` interpretation.
- `setupCmdBanConfig` (internal/setup.go) splits on commas, trims spaces,
  drops empties, and appends onto the file+profile base. It is called in
  `setupTextQuerierWithConf` immediately after `setupToolConfig`, before the
  late `applyProfileOverridesForText` — the spec-defined slot (review 2
  verified-good order: `ProfileOverrides` → `setupToolConfig` →
  `setupCmdBanConfig` → `applyProfileOverridesForText`).
- `NewQuerier` (internal/text/querier_setup.go) calls
  `pkgtools.SetCmdBanList(userConf.CmdBan)` unconditionally, right before
  `setupTooling` and after the model config file is loaded, so the per-run
  list is installed at the spawn point before any tool can execute (D6). The
  empty default keeps the tools fully permissive (D4).
- `pkg/agent` gains `cmdBan []string` on `Agent`, the `WithCmdBanList(entries
  ...string) Option`, and passes `CmdBan: a.cmdBan` through
  `asInternalConfig`. A nil default means agents without the option stay
  permissive.
- `main.go` usage gained one `-cmd-ban` row directly under `-t/-tools`.

Verification (all run from the repo root):

```bash
go test ./internal/... ./pkg/agent/... -timeout=30s   # 32/32 packages ok
```

```bash
go test ./internal/ ./internal/text/ ./pkg/agent/ ./pkg/tools/ -race -count=1 -timeout=120s   # all ok (R2-01 guard holds)
```

```bash
go test ./... -timeout=120s   # all packages ok
```

```bash
go build ./...   # clean
```

```bash
go vet ./internal/ ./internal/text/ ./pkg/agent/ .   # clean
```

```bash
go run mvdan.cc/gofumpt@latest -l internal/ pkg/agent/ main.go   # no output
```

```bash
go run honnef.co/go/tools/cmd/staticcheck@latest ./internal/... ./pkg/agent/... .   # clean
```

```bash
go run github.com/mibk/dupl@latest -t 80 .   # 29 clone groups — unchanged from baseline
```

## Review findings (review 1, 2026-08-02)

Reviewer: imago. The phase was Not Started; this review amends the contract
in place. Severity taxonomy and the full findings index live in the README.

- **R1-04 (High) — profile override silently clears the file ban list.** As
  originally specced, `ProfileOverrides()` sets `c.CmdBan = profile.CmdBan`
  unconditionally whenever a profile is active. A new field unmarshals to
  nil when absent (D11), so EVERY existing profile — none of which has a
  `cmd_ban` key — would silently wipe the file's bans the moment `-p` is
  used. For a safety policy this defeats the feature on the most common
  invocation, and it contradicts the D5 principle that configured
  restrictions are not silently removed (stated for flags; the profile did
  it silently). The codebase's idiom for "absent = don't override" is
  optional pointers (`UseSkills *bool`, `SaveReplyAsConv *bool`,
  `UseLookback *bool` in internal/text/conf.go). Amended: nil-guard the
  assignment so only an explicit `cmd_ban: []` clears; unit tests must
  cover "profile without `cmd_ban` keeps file list" and "profile with
  explicit `cmd_ban` replaces". README Strategy and D5-adjacent text
  updated.

  **2026-08-02 revision (user decision):** the resolution was strengthened to
  purely additive — `ProfileOverrides` APPENDS the profile's list onto the
  file base (nil-guarded) instead of replacing it; `cmd_ban: []` contributes
  nothing and is no longer a clear switch; no source removes another source's
  bans. The cascade is now union-only (D5). Unit tests must cover "profile
  merges onto file base" and "profile without `cmd_ban` contributes
  nothing"; the spec above reflects this revision.

Verified good: `setupToolConfig` (internal/setup.go) appends `-t` globs —
the `-cmd-ban` append mirrors a real precedent. Note the deliberate
DIVERGENCE from the tool-selection pattern after the R1-04 revision: for
`RequestedToolGlobs` a profile REPLACES (`c.RequestedToolGlobs =
profile.Tools`) and the flag appends, whereas `CmdBan` is now union-only
across all sources — this is intentional (restrictions accumulate; the
analogy holds for the flag, not for the profile).
`setupTextQuerierWithConf` calls `ProfileOverrides()` then `setupToolConfig`
— the `setupCmdBanConfig` insertion point is well-defined. `NewQuerier`
calls `setupTooling` (internal/text/querier_setup.go:194) — the
`tools.SetCmdBanList` call site exists. `pkg/agent` has the `Option` /
`asInternalConfig` pattern (agent.go:45,122) — `WithCmdBanList` slots in
cleanly. `editSlice` (internal/setup/setup_actions.go:816) handles the new
JSON key generically — D10 verified. `main.go` usage has a `-t/-tools` row
to mirror for `-cmd-ban`.

## Review findings (review 2, 2026-08-02)

Reviewer: clai. The phase was Not Started; this review amends the contract
in place. Full findings index in README.

- **R2-05 (Low) — stale file/line citations.** The spec's `ProfileOverrides`
  bullet pointed at `internal/text/conf.go`; the method lives in
  `internal/text/conf_profile.go` (the struct fields are in `conf.go`). The
  review-1 verified-good text cites the `setupTooling` call site as
  `querier_setup.go:143`; the actual call is at `querier_setup.go:194`.
  Amended the spec header; the review-1 text is left as history. No
  behavioral impact — the seams themselves were re-verified against the
  current tree.

Verified good: the R1-04 additive-cascade revision is consistent with the
codebase idiom (optional-pointer nil-guards in `ProfileOverrides`), and the
`ProfileOverrides` → `setupToolConfig` → `applyProfileOverridesForText` order
(internal/setup.go:236,241,245) gives `setupCmdBanConfig` a well-defined
slot after the profile merge and flag append.


## Review findings (review 8, 2026-08-02)

Independent re-audit found no new findings. File, nil-guarded profile, flag,
and agent sources remain union-only; `NewQuerier` installs the resulting list
before tooling can execute. Persistence and setup tests pass. Phase remains
Complete.
