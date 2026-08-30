# Phase 12 — commands move into their domain packages

**Status:** Complete (review 1 findings fixed)
[Worklog README](./README.md)

## Goal

Dissolve the flat `internal/cmds.go`: each command definition moves into its
domain package (kinoview-style `Command()` constructors), with the command
map assembled in `main.go` as the composition root.

## Specification

Repo: clai, branch `refactor-flag-system`. Depends on phase 11 (glob command
gone; 12 commands remain). Placement decision (Lorentz, 2026-08-28,
superseding the earlier `internal/cmd/<name>/` layout): commands live **in
their respective domain packages** — `internal/tools/cmd.go` and
`internal/profiles/cmd.go` already follow this convention. Domain-less
commands get uniform tiny packages (Lorentz, same session).

**Cycle constraint (verified 2026-08-28):** `internal/vendors/openai`
imports `internal/photo`, `internal/video`, `internal/tools`; package
`internal` imports every domain package. Therefore the querier factories
(`CreateTextQuerier`, `CreatePhotoQuerier`, `CreateVideoQuerier`,
`CreateAudioQuerier`, `setupTextQuerierWithConf`,
`setupAudioTranscribeQuerier`) and `configRunPrep`/`migrateConfigs` **stay
in package `internal`** and are injected into the domain commands from
`main.go`. This is the repo's strict-IoC doctrine applied to command
construction; no vendor or factory logic moves.

1. **Command homes** (keys/aliases in the `main.go` map byte-identical to
   today's):
   | Command | Package | New file |
   |---|---|---|
   | query | `internal/text` | `cmd.go` |
   | chat (+subs), replay, dre | `internal/chat` | `cmd.go` |
   | photo | `internal/photo` | `cmd.go` |
   | video | `internal/video` | `cmd.go` |
   | audio (+subs) | `internal/audio` | `cmd.go` |
   | setup | `internal/setup` | `cmd.go` |
   | tools (+subs) | `internal/tools` | into existing `cmd.go` |
   | profiles (+subs) | `internal/profiles` | into existing `cmd.go` |
   | version | `internal/version` (new) | `cmd.go` |
   | confdir | `internal/confdir` (new) | `cmd.go` |
   Each exposes `Command(deps...) *clicmd.Command` with the narrowest dep
   set (config-prep func, querier factory func); commands with no external
   deps take none. Help literals move with their command.
2. **New leaf package `internal/clicmd`** — the adapter and flag machinery,
   importable by every domain package (it imports only `flag`, upstream
   `cmd`, `internal/models`, `internal/utils`):
   - `claiCommand` → `clicmd.Command`: construction fields exported
     (`Name`, `Desc`, `HelpText`, `Register`, `OnSetup`, `OnRun`, `Parent`,
     `Subs`, `CompleteFlagValueFn`, `CompleteArgsFn`) plus the runtime
     surface closures need (`Conf`, `Args`, querier setter — executor's
     choice of fields vs accessors, recorded in notes). Methods unchanged;
     the dead `mode` field is deleted. `triggerCompletionNotification`
     moves here (unexported).
   - `claiFlags` → `clicmd.Flags` with exported `Register*` group methods;
     `Configurations` and `defaultFlags` → `clicmd.Configurations`,
     `clicmd.DefaultFlags`. Package `internal` switches to the `clicmd`
     types (type alias or direct use — executor's choice, recorded).
   - Generic completion helpers (`plainItems`, `filterValues`,
     `suppressArgs`, media/dir hooks) move here; the config-dir-derived
     `completionSources` moves here with tool-name lookup accepted as an
     injected `func() []string` (query/chat wire `tools`-registry lookup
     themselves — both their packages may import `internal/tools`;
     `clicmd` must not).
3. **Overrides move to their structs.** `applyFlagOverridesForText/Photo/
   Video/Audio` and `applyProfileOverridesForText` become exported
   `ApplyFlagOverrides`/`ApplyProfileOverrides` in their domain packages
   (operating on `clicmd.Configurations`); package `internal` call sites
   updated. `migrateOldPhotoConfig` exported for the photo command's
   injected loader (or folded into the injected factory — executor's
   choice, recorded).
4. **Macro input relocates.** `setup.Input` moves to `internal/utils`
   (next to `NewMacroReader`) so tools/profiles commands can inject macro
   inputs without importing `internal/setup` (setup → tools would cycle).
   `internal/setup` and the trust-prompter call site read the utils var.
5. **`internal.Commands()` dissolves** into a `var commands` literal in
   `main.go`, kinoview-style, wiring injected deps from `internal`
   (exported as needed: `ConfigRunPrep`, `SetupTextQuerierWithConf`,
   `SetupAudioTranscribeQuerier`, `ApplyDirReplyChatID`,
   `NewReadOnlyChatHandler`, `PrintConfDir` → moves to confdir package if
   dependency-free, executor records). `internal/cmds.go`,
   `internal/dre.go`, `internal/confdir.go` deleted;
   `internal/version.go` shrinks or moves per item 6.
6. **version package + ldflags.** `BuildVersion`/`BuildChecksum` and
   `printVersion` move to `internal/version`;
   `.github/workflows/release.yml` `version-var` updated to
   `github.com/baalimago/clai/internal/version.BuildVersion`.
   **Handover flag for Lorentz:** eyeball this before the next release —
   a stale ldflags target fails silently (version prints the module
   version instead).
7. **Behavior freeze.** Pure relocation: every e2e test passes unmodified
   (no R-budget draw). Flag scoping (phase 7 table), nesting (phase 8
   trees), completion hooks (phase 9) are contract-frozen.
8. **Mode pruning.** With the adapter's dead `mode` field gone, `Mode`
   survives only as `SetupTextQuerierWithConf`'s QUERY/CHAT discriminator;
   unused constants deleted (deltas recorded if any resist).
9. **Docs.** `architecture/cmd-dispatch.md` documents the placement
   convention, `clicmd`, and the composition-root wiring; references to
   `internal/cmds.go` retargeted across `architecture/`.

## Integration contract

Relocation phase — the externally observable contract is "nothing moved":

| # | Scenario | Observable result | Prohibited |
|---|----------|-------------------|------------|
| 1 | full e2e suite (`main` package) | passes with zero test edits | any assertion change |
| 2 | bare `clai` usage table | same command table (keys/aliases/describes) | — |
| 3 | `clai <cmd> -h` for all 12 + subs | same help text as pre-split | — |
| 4 | `__complete` protocol | same completions (flag names, hook values) | config-dir side effects |
| 5 | release build path | `go build -ldflags "-X github.com/baalimago/clai/internal/version.BuildVersion=x"` stamps `clai version` output | release.yml var left stale |

## Acceptance criteria

- [x] `internal/cmds.go` deleted; commands live per the placement table;
      `main.go` holds the map + injection wiring; `go vet ./...` clean;
      no import cycles (`go build ./...`).
      Evidence: files deleted (`cmds.go`, `dre.go`, `confdir.go`,
      `version.go`, `completion.go`, `setup_flags.go`); build + vet clean
      in `make qa`.
- [x] Contract rows 1–4 evidenced by the existing e2e suite passing
      unmodified; row 5 by a one-shot stamped build recorded in notes.
      Evidence: `go test . -race` green with zero e2e edits; ldflags
      check in notes.
- [x] `internal/clicmd` ≥70% coverage (90%+ preferred); new `cmd.go` files
      covered ≥70% within their packages' `go test -cover` runs; numbers
      recorded in notes.
      Evidence: coverage table in notes (clicmd 80.4%).
- [x] `release.yml` version-var updated in the same diff as the var move;
      handover note for Lorentz added to phase 10's checklist section or
      README journal.
      Evidence: `.github/workflows/release.yml:13` →
      `internal/version.BuildVersion`; README journal entry.
- [x] Unused `Mode` constants and the dead `mode` field removed; grep
      proves no dangling references.
      Evidence: `Mode` is now `QUERY|CHAT` only (`internal/setup.go`);
      adapter has no mode field.
- [x] `architecture/cmd-dispatch.md` updated; no doc references
      `internal/cmds.go`.
      Evidence: repo-wide grep clean; 15 architecture docs retargeted.
- [x] `make qa` exit 0.
      Evidence: exit 0 (first run; log in notes).

## Error coverage

| Failure condition | Expected outcome | Test |
|---|---|---|
| Unknown command | same dispatcher error + usage as pre-split | existing e2e kept green |
| Sub-level flag before command (R-e shape) | same scanner error as phase 8 pinned | `Test_e2e_audio_sub_flag_before_command_rejected` kept green |
| Read-only chat subs on missing/RO config dir | structurally read-only guarantee preserved | existing chmod/missing-dir e2e kept green |
| Broken theme/config at `ConfigRunPrep` | same warn-and-continue behavior | existing tests kept green |
| Nil/omitted injected dep (programmer error) | compile-time where possible; otherwise a clear construction-time error | clicmd unit test |

## Implementation notes

**Session: Claude, 2026-08-28 (extension, same session as phase 11).**

Deltas from the specification:

- **Spec item 4 (macro input) implemented differently:** `setup.Input`
  stays where it is — moving it to `utils` would have forced an edit to
  `main_test.go` (the e2e helper swaps `setup.Input`), breaking the
  zero-test-edit freeze. Instead tools/profiles take a
  `MacroInput func(args []string)` dep; `main.go` wires the closure
  `setup.Input = utils.NewMacroReader(args)`. The setup command (in the
  setup package) sets its own `Input` directly. Strictly more IoC than
  the spec's version.
- **Adapter runtime surface:** accessor methods (`Conf()`, `Args()`,
  `SetArgs()`, `SetQuerier()`) rather than exported runtime fields — the
  construction fields are exported, the parse/run state stays private.
- **Package `internal` keeps `Configurations` as a type alias**
  (`= clicmd.Configurations`), so `SetupTextQuerierWithConf` and friends
  kept their signatures; `ProfileHelp` is re-exported
  (`= profiles.Help`) because `main_help_e2e_test.go` asserts
  `internal.ProfileHelp` (freeze again).
- **`newReadOnlyChatHandler` moved into chat** (as `newReadOnlyHandler`)
  rather than being injected — it only touches `chat.New`.
- **Video loads its own config** (`utils.LoadConfigFromFile`, nil
  migration) — only photo needs the injected `internal.LoadPhotoConfig`,
  because its old-config migration imports `vendors/openai`.
- **`registryToolNames` is duplicated** in text and chat (8 lines): chat
  cannot import text (text imports chat). Below the dupl threshold.

Test relocations: `cmds_test.go`→`clicmd` (notification + new adapter
suite), `completion_test.go`→`clicmd` (tool-name lookup now passed as a
plain slice), `setup_flags_test.go` split three ways (flag/alias tests →
`clicmd/flags_test.go`, override cascades → `text/cmd_test.go`, lookback
tests → `internal/setup_lookback_test.go`), audio override test →
`audio/cmd_test.go`, `dre_test.go`→`chat`. New unit tests for every
command constructor (stub deps, flag-surface checks, setup/run paths).

Coverage after (`make qa`, `-race -count=3`): clicmd 80.4%, audio 91.8%,
chat 71.7%, confdir 91.7%, photo 51.2% (package total — up from 13.6%
pre-split; the deficit is pre-existing untested store/prompt code, the
new `cmd.go` functions are covered), profiles 89.5%, setup 79.1%,
text 82.5%, tools 80.8%, version 87.5%, video 76.2%. Main e2e package
40.9%→60.6% (`commands()` now attributed).

Verification: full e2e suite green with **zero** test edits
(rows 1–4); ldflags row 5:
`go build -ldflags "-X github.com/baalimago/clai/internal/version.BuildVersion=v0.0.0-ldflags-check"`
→ `clai version` prints `version: v0.0.0-ldflags-check`. `make qa`
exit 0 (first run). Binary reinstalled via `go install .`.

Dupl signal: one new clone pair — `photo/cmd.go:78,100` vs
`video/cmd.go:63,85` (`ApplyFlagOverrides` cascades). Accepted:
parallel-by-design over distinct config types; the same shape existed
side-by-side in the deleted `setup_flags.go`. The
`captureStdout` helper clone (`setup_lookback_test.go` vs
`text/stoploss_debug_test.go`) is a pre-existing test-helper pattern.

**Handover note (Lorentz):** `.github/workflows/release.yml` now stamps
`github.com/baalimago/clai/internal/version.BuildVersion`. Eyeball this
with the `replace`-swap before the next release — a stale ldflags target
fails silently (version falls back to the module version).

## Review findings

### Review 1 (2026-08-28, holistic `/code-review high`, phases 11–15)

- **CR-02 (Critical, reopens):** `clai replay`/`clai dir-replay` never
  call ConfigPrep, so the theme is never loaded. Reproduced: with
  `theme.json = {"notificationBell": false}` and a valid dirscope
  binding, `clai dre` output ends with `\a` — the pre-refactor flow
  loaded the theme for every mode. The adapter's default `Run`
  (`internal/command.go:142`) still rings the completion bell off the
  unloaded `globalTheme` (default `NotificationBell: true`); replay also
  renders default role colors (that half was acknowledged as cosmetic in
  phase 6; the bell regression was acknowledged nowhere). Root cause:
  `utils.LoadTheme` now lives only in `setup.ConfigRunPrep`
  (`internal/setup/config_lifecycle.go:28`), which
  `ReplayCommand`/`DirscopeReplayCommand` (`internal/chat/cmd.go:195`)
  never reach. Originates in the phase-6 strategy invariant (replay
  classed as non-config-touching) but is fixable where the commands now
  live.
- **CR-04 (Major, reopens):** chat sub-commands never set
  `CompleteFlagValueFn`, so shell flag-value completion (`-cm` model
  history, `-p` profiles, `-t` tools) is dead after any chat verb.
  Reproduced: `clai __complete clai chat -cm ""` lists model history;
  `clai __complete clai chat continue -cm ""` prints nothing. The
  upstream engine reads the hook from the deepest resolved command
  (`go_away_boilerplate/pkg/cmd/completion.go:91-92`) and `chatSub`
  (`internal/chat/cmd.go:97`) leaves it nil — only the chat parent sets
  it. The old engine completed these values anywhere on the line.
- **CR-08 (Minor, logged):** dead `MacroInput` plumbing — tools and
  profiles feed positional args into `setup.Input`
  (`internal/tools/cmd.go:74`, `internal/profiles/cmd.go:144-153`), but
  nothing on either run path reads it (the wizard path sets `Input`
  itself in `internal/setup/cmd.go:29`). `clai profiles bogus` mutates
  the process-global, then errors. Delete `CommandDeps`/`OnSetup` macro
  hooks from both packages plus the `main.go` wiring.
- **CR-09 (Minor, logged):** `registryToolNames()` is defined
  byte-identically in `internal/text/cmd.go:77` and
  `internal/chat/cmd.go:160`, with a third inline variant in
  `internal/tools/cmd.go:118-127` and a fourth enumeration in
  `internal/setup/setup_actions.go:530`. Standing `dupl -t 80` hit; a
  name-sourcing change will miss a copy. Fix: one exported
  `tools.Names()` — all call sites already import `internal/tools`.

#### Fixes (2026-08-28, same session as the review)

All four fixed; `make qa` exit 0, binary reinstalled.

- **CR-02 fixed** — theme prep split out of the migration pass: new
  `internal.PrepTheme()` (config dir + `utils.LoadTheme`, broken theme
  degrades to a warning) is now the single implementation;
  `setup.ConfigRunPrep` delegates to it, and `ReplayCommand`/
  `DirscopeReplayCommand` call it in their `OnSetup`. Pinned by
  `Test_e2e_replay_loads_theme` (both verbs, themed role color reaches
  stdout) and `Test_e2e_dirscope_replay_honors_notification_bell`
  (bell true/false table). **Test-design note:** the theme is a process
  global, so an in-process second `run()` inherits the seeding query's
  theme and hides the defect — both tests seed in-process, then invoke the
  built binary in a fresh process (`builtClaiDir`, built before the
  `chdirTemp` since the build runs in the current directory).
- **CR-04 fixed** — `chatSub` takes the tree's `*internal.CompletionSources`
  and sets `CompleteFlagValueFn` on every sub, so `-cm`/`-p`/`-t`/`-prp`
  complete after a chat verb. Pinned by the `chat sub flag values` subtest
  in `Test_e2e_complete_engine` (`chat continue -cm`, `chat continue -p`,
  alias form `c c -cm`).
- **CR-08 fixed** — `CommandDeps`/`MacroInput` deleted from both `tools`
  and `profiles` (with their `OnSetup` hooks and the `main.go` closure);
  `Command()` now takes no arguments. Their unit tests dropped the macro
  assertions; the tools one now pins that the positional survives Setup
  for the detail route.
- **CR-09 fixed** — one exported `tools.Names()` (Init + sorted registry
  keys, aliases included) replaces all four enumerations: both
  `registryToolNames` copies deleted, `toolNameArgs` and
  `setup.sortedToolNames` retargeted.
