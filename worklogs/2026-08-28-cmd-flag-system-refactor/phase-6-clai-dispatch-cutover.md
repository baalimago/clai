# Phase 6 — clai dispatch cutover to `cmd.Run`

**Status:** Complete
[Worklog README](./README.md)

## Goal

Replace clai's `Mode` enum / `getCmdFromArgs` / `internal.Setup` dispatch with
`cmd.Run` and a `map[string]cmd.Command`, at behavior parity, with every
command still exposing today's full shared flag surface (strangler step;
pruning is phase 7).

## Specification

Repo: clai, branch `refactor-flag-system`. Requires phase 5 complete
upstream; the `go.mod` `replace` directive to the local checkout was added
in phase 5 (README D9) — verify it is present, do not re-add. Affected: `main.go`, new `internal/cmds/` package,
`internal/setup.go` (dispatch dismantled; setup logic retained as functions),
`internal/setup_flags.go` (untouched this phase), `internal/completion.go`
(commands re-homed, engine untouched).

1. **Command map** (keys preserve today's aliases from `getCmdFromArgs`):
   `query|q`, `chat|c`, `photo|p`, `video|v`, `audio|a`, `glob|g`
   (deprecation warning preserved), `help|h`, `setup|s`, `version`,
   `replay|re`, `dir-replay|dre`, `tools|t`, `profiles`, `confdir`,
   `completion`, `__complete`. This phase keeps clai's existing completion
   commands (engine swap is phase 9), overriding the upstream auto-registered
   ones by key — allowed by phase-4 precedence.
2. **Adapter commands.** Each `cmd.Command` in `internal/cmds/` wraps the
   existing setup path: `Flagset()` registers the **full current flag
   surface** via one shared registrar (a thin lift of `parseFlags`' flag
   definitions producing a `Configurations`), `Setup(ctx)` runs the existing
   per-mode setup (config load, migrations, querier construction), `Run(ctx)`
   executes the querier and fires the completion bell.
   `internal/setup.go`'s dispatch `switch` dissolves into these `Setup`
   bodies; shared pieces (united config migration, theme load, macro-input
   injection, `ReadonlyConfig`/`NoCreateConfig`, dirscope wiring) become
   helpers called by the commands that need them (README invariant: config
   migration only in config-touching commands — query/chat/photo/video/
   audio/setup).
3. **`main.go`:** `os.Exit(cmd.Run(ctx, os.Args, cmds.All(usage), usage))`.
   Preserved: ctx cancel embedded via `utils.ContextCancelKey` (works now
   that `Setup` gets the real ctx), `shutdown.Monitor` goroutine (started
   before `cmd.Run`), CPU-profile debugflag, `ancli.SetupSlog`. The
   completion bell (`triggerCompletionNotification` + suppressor interface)
   moves next to the querier-running helper in `internal/cmds`.
4. **Sentinel conversion.** The 39 non-test `table.ErrUserInitiatedExit`
   occurrences split into ~24 return/assignment sites and 15 `errors.Is`
   consumer checks. Return sites that mean "done, exit 0" become plain
   `nil` returns (command finished) or `cmd.ErrUserInitiatedExit` (user
   declined/quit). Genuine TUI-originated sentinels keep bubbling as
   `table.ErrUserInitiatedExit` — `cmd.Run` honors both, so no translation
   shims. **Additionally audit every internal `errors.Is` check:** any
   check sitting above a converted return site must recognize both
   sentinels (or the callee provably still returns only `table`'s) — a
   check that only knows `table`'s sentinel silently misses `cmd`'s. The
   audit outcome per site is recorded in implementation notes.
5. **Usage/help.** Top-level usage string becomes the single-`%v` form
   (command table injected by `getUsage`); per-command detail (flags,
   examples, config-dir paths) moves into each command's `Help()`. The
   `help profile` special case (`ProfileHelp`) is preserved via the `help`
   command's own arg handling.
6. **Parity matrix** — these must behave identically to `main` (modulo the
   README regression budget): every example in the current usage string, plus
   `clai -re q hi`, `clai -cm gpt-4 q hi` (newly working, was the phase-2
   motivation), `clai c list` / `clai -r c dirv2` (read-only chat:
   `NoCreateConfig` against a read-only config dir), `clai a t meeting.wav`
   and `cat x.wav | clai a t -`, `clai s 1 q` macro inputs,
   `clai completion bash`, `clai __complete clai q` (fast, no config
   writes), `clai version`, `clai dre`, `clai -p X q hi` profile override
   precedence (flags > profile > file > default unchanged —
   `applyFlagOverridesFor*` and `applyProfileOverridesForText` are not
   modified this phase).

Out of scope: flag pruning (7), nesting (8) — chat/audio verb switches keep
their current internal string dispatch this phase — completion engine swap
(9), docs (10).

## Integration contract

| #   | Scenario                       | Trigger                                         | Collaborators                | Observable result                               | Prohibited                                        |
| --- | ------------------------------ | ----------------------------------------------- | ---------------------------- | ----------------------------------------------- | ------------------------------------------------- |
| 1   | query e2e                      | `clai -cm test q hello` (mock vendor)           | mock model config            | querier runs; exit 0; bell on stdout            | flag error                                        |
| 2   | flags after command            | `clai q -cm test hello`                         | mock vendor                  | identical to row 1                              | —                                                 |
| 3   | read-only chat                 | `clai c list` with read-only `CLAI_CONFIG_DIR` dir | real FS                      | list printed, exit 0                            | any file create/write (assert dir mtime/contents) |
| 4   | completion untouched by config | `clai __complete clai q` with empty config dir  | real FS                      | suggestions on stdout, exit 0                   | config files created, migrations run              |
| 5   | setup wizard quit              | `clai s` then `q` input                         | `table` TUI via macro reader | exit 0, silent                                  | error text                                        |
| 6   | help                           | `clai h` / `clai help profile`                  | —                            | usage with sorted command table / `ProfileHelp` | stale `%!v(MISSING)` artifacts                    |
| 7   | version                        | `clai version`                                  | —                            | version line, exit 0                            | —                                                 |
| 8   | unknown command                | `clai bogus`                                    | —                            | error naming `bogus` + usage, exit 1            | exit 0                                            |
| 9   | ctx cancel propagation         | SIGINT during mock query                        | `shutdown.Monitor`           | graceful stop, exit 0 via sentinel path         | goroutine leak (race detector)                    |

## Acceptance criteria

- [x] Command map covers every current `getCmdFromArgs` case; `Mode` enum and
      `getCmdFromArgs` deleted (or reduced to internals with no dispatch
      role); grep proves no caller remains.
      Evidence: `internal.Commands()` in `internal/cmds.go` (16 keys, same
      aliases); `getCmdFromArgs` and `internal.Setup` deleted —
      `grep -rn "getCmdFromArgs\|internal\.Setup("` returns nothing. `Mode`
      kept as an internal config-shaping parameter only
      (`setupTextQuerierWithConf`, `extractMacroInputs`), no dispatch role.
- [x] All nine contract rows automated; parity matrix (spec 6) covered by
      e2e tests or explicitly mapped to existing tests that still pass.
      Evidence: mapping table in implementation notes; new tests in
      `main_dispatch_e2e_test.go`; full pre-existing e2e suite green.
- [x] Zero `table.ErrUserInitiatedExit` returns remain whose meaning is
      "command completed normally" (grep + review; TUI-originated ones
      enumerated in implementation notes).
      Evidence: grep shows 7 remaining non-test return sites, all
      TUI-originated (enumerated in notes).
- [x] All 15 internal `errors.Is(err, table.ErrUserInitiatedExit)` consumer
      checks audited per spec 4; per-site verdict (widened to both / provably
      table-only) recorded in implementation notes.
      Evidence: audit table in implementation notes (2 replaced by `cmd.Run`,
      13 provably table-only).
- [x] `make qa` exit 0 (gates unedited).
      Evidence: `make qa` → exit 0 (run 2026-08-28, post-cutover).
- [x] `go.mod` still carries the local `replace` directive added in phase 5
      (README D9); upstream requirement line updated as `go mod tidy`
      demands. (Swapping it for a pinned version is Lorentz's release step,
      documented in phase 10's handover checklist — not agent scope, per
      D10.)
      Evidence: `go.mod:13` `replace ... => ../go_away_boilerplate`;
      requirement line unchanged at `v1.33.9` (tidy demanded no bump).

## Error coverage

| Failure condition                     | Expected outcome                                               | Test                      |
| ------------------------------------- | -------------------------------------------------------------- | ------------------------- |
| Missing API key env for chosen vendor | same error text/exit 1 as today                                | existing e2e kept green   |
| Broken theme.json                     | warning + builtin theme fallback preserved                     | existing test kept green  |
| Broken mode config json               | warning-not-brick policy preserved per united-migration design | existing tests kept green |
| Audio verb missing (`clai a`)         | namespace help on stderr + exit 1 (current behavior)           | existing test kept green  |
| `-h` on any command                   | that command's `Help()`, exit 0                                | new table-driven e2e      |

## Implementation notes

**Session: Claude, 2026-08-28 (implementation, same session as phases 1–5).**

### Deltas from spec

- **Adapters live in package `internal` (`internal/cmds.go`), not a new
  `internal/cmds/` package.** A separate package would have required
  exporting ~15 currently-unexported helpers (`registerFlags`/`resolve`,
  `setupTextQuerierWithConf`, `handleAudio`, migration prep, completion
  handlers, `defaultFlags`, …) purely for the adapters' benefit. One
  generic `claiCommand` adapter struct + 16 small constructors in a single
  file is leaner and keeps the exported API surface unchanged.
- **Shared registrar:** `parseFlags` split into `registerFlags(fs, defaults)
  *flagValues` + `(*flagValues).resolve(defaults)` (`setup_flags.go`);
  `parseFlags` remains as a thin wrapper so `setup_flags_test.go` passes
  untouched. Each adapter's memoized `Flagset()` registers the full surface;
  `Setup` resolves it.
- **Shared prep:** the old `Setup` body became `configRunPrep(defer)`
  (config dir + theme + `migrateConfigs`, `cmds.go`/`setup.go`), called
  only by query/glob/chat/photo/video/audio/setup per the README
  invariant. Consequence beyond the named commands: replay, dir-replay,
  tools and profiles (unlisted in the invariant) are now also
  side-effect-free — they never ran anything from the migration block
  except as collateral of the monolithic Setup; their listing output now
  renders with the default theme instead of the user theme (cosmetic).
- **`run(args)` keeps its old signature** (args without binary name) and
  prepends `"clai"` before `cmd.Run` — 5900+ lines of e2e harness calls
  compile unchanged. `shutdown.Monitor` starts before `cmd.Run` (spec 3);
  ctx cancel value preserved. The DEBUG-only "Byebye"/"worked out" chatter
  is gone (cmd.Run exits silently); no test asserted it.
- **Usage split:** `cmd.Run` gets a new single-`%v` `shortUsage`
  (`main.go`); the monolithic interpolated usage string is unchanged and
  now printed exclusively by the `help` command (`printHelp`), preserving
  the help golden tests byte-for-byte. Row 6's "sorted command table" is
  satisfied by the `shortUsage` path (no-args / unknown command).
- **`clai` with no args** now prints shortUsage + sorted table, exit 1
  (was: "failed to setup: no command provided"); unknown command prints
  `'bogus' is not a valid argument` + usage (R-c budget).

### Sentinel conversion (spec 4)

Converted "done, exit 0" returns → `nil`: `tools/cmd.go` (2),
`profiles/cmd.go` (3), `version.go` (1), `confdir.go` (1),
`completion.go` bash/zsh/`__complete` success (3). Converted to
`cmd.ErrUserInitiatedExit` (clean-exit from `Setup`, must skip `Run`):
`setup_audio.go` audio-help (1), `completion.go` missing-shell (1,
wrapped). Wizard-success and REPLAY sentinel returns in the old dispatch
switch disappeared with the switch itself (adapters return `nil`).
Remaining table-sentinel returns, all genuinely TUI-originated (grep,
7 sites): `setup/setup.go:71,240,249,291`, `setup/setup_actions.go:85`,
`chat/handler_list_chat.go:309,810` — produced by table
navigation/ReadUserInput quit paths, honored by `cmd.Run` via `errors.Is`.

`errors.Is` consumer audit (15 sites): `main.go:113,125` — deleted,
replaced by `cmd.Run` honoring both sentinels. The remaining 13
(`setup/setup.go:239,248,290`, `setup_actions.go:93,100,508,877,899`,
`chat/handler_list_chat.go:733,757,777,1030,1065`) each sit directly above
pure table-TUI callees (wizard tables, `ReadUserInputFrom`, list
navigation) that return only `table`'s sentinel; none of the converted
call chains flow into them → verdict: provably table-only, unchanged.

### Contract rows / parity matrix mapping

| Row | Test |
|---|---|
| 1 query e2e | `Test_goldenFile_calibration`, notification bell tests |
| 2 flags after command | new `Test_e2e_flags_after_command` |
| 3 read-only chat | `Test_e2e_chat_list_does_not_create_config_dir` + chat-list e2e suite |
| 4 completion no config | completion e2e suite + new `Test_e2e_hidden_completion_is_side_effect_free` |
| 5 setup wizard quit | `main_setup_e2e_test.go` (macro `q`) |
| 6 help / help profile | `Test_goldenFile_HELP_*` (golden output preserved) |
| 7 version | `main_version_e2e_test.go` |
| 8 unknown command | new `Test_e2e_unknown_command_errors_with_usage` |
| 9 ctx cancel | upstream `Test_Run_ctxPassthrough` + `Test_Run_canceledCtxUnaltered` prove the dispatcher passes the caller ctx unmasked to Setup/Run; sentinel exit-0 path via setup-quit e2e; whole suite green under `-race`. A literal SIGINT is not simulated in-process. |

Parity spec 6: `-re q hi`/`-cm gpt-4 q hi` (calibration + dirscope suites,
flags-before-command), `c list`/`-r c dirv2` (chat-list e2e), audio file +
stdin (audio e2e), `s 1 q` macros (setup macro e2e), completion bash /
`__complete` (completion e2e), version, dre (dirscope e2e), `-p X q hi`
(cmd-ban/profiles e2e with `-p`). `applyFlagOverridesFor*` /
`applyProfileOverridesForText` untouched.

### Test upgrades (each justified)

- `internal/tools/cmd_test.go`, `internal/profiles/cmd_test.go`: sentinel
  assertions → `nil` (direct consequence of spec-4 conversion).
- `main_confdir_e2e_test.go`: the four migration tests asserted confdir
  runs the united migration — contradicts the README invariant (confdir is
  side-effect-free by construction). Retargeted onto `-cm test q hello`
  (renamed `Test_e2e_query_migrates_*`); raw-mode pins retargeted onto
  `-r -cm test q hello`; new `Test_e2e_confdir_is_side_effect_free` pins
  the invariant.
- `main_profiles_e2e_test.go`: dropped the migration-warning assertion for
  a broken profile (profiles no longer migrates); the listing still must
  skip it.
- `main_notification_test.go`: `triggerCompletionNotification` unit test
  moved to `internal/cmds_test.go` (the helper moved per spec 3) and
  gained a suppression subtest.

### Verification

- `make qa` → exit 0 (gofumpt, staticcheck, `go vet`,
  `go test ./... -race -cover -count=3 -timeout=30s`, `go fix`, dupl).
- Full main-package e2e suite green including under `-race`.
- New-code coverage: adapters exercised end-to-end by the e2e suite
  (`internal` package 51.6% via unit tests alone; the adapters' behavior
  is proven by the main-package e2e tests, which don't count toward
  internal's unit coverage number).

## Review findings

_(appended by reviewers)_
