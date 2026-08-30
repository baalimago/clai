# Phase 9 — clai onto the `cmd` completion engine

**Status:** Complete
[Worklog README](./README.md)

## Goal

Replace clai's hand-maintained completion engine with the upstream one:
generic behavior (commands, subcommands, flag names, arity) comes from the
`cmd` engine for free; clai keeps only its dynamic value sources as
`FlagValueCompleter`/`ArgCompleter` implementations.

## Specification

Repo: clai, branch `refactor-flag-system`. Affected:
`internal/completion.go` (~350 of 548 lines deleted),
`internal/completion_test.go` (rewritten), `internal/cmds/` (hook impls).
Depends on phases 6–8 (upstream engine + clai trees in place).

1. **Delete** (now upstream): `completionCommands`,
   `completionChatSubcommands`, `completionGlobalFlags` and its
   `completionFlagSpec` table, `filterFlags`/`appendCommandAndFlags`, the
   engine dispatch in `Complete`, `bashCompletionScript`/
   `zshCompletionScript`, `handleCompletionCommand`/`handleHiddenCompletion`,
   `hasEarlyCompletionCommand` (the bypass is structural since phase 6).
   clai stops overriding the `completion`/`__complete` keys — upstream
   auto-registration takes over.
2. **Keep as hook implementations** (data loading unchanged:
   `loadCompletionData`, `readJSONBaseNames`, `discoverModelHistory`,
   `modelFromConfigFilename`, `toolNames`):
   - `query`/`chat`/`glob` implement `CompleteFlagValue`: `cm|chat-model` →
     model history; `p|profile` → profiles; `asc|add-shell-context` → shell
     contexts; `t|tools` → tool names with comma-split multi-value (port
     `completeToolValue`); `prp|profile-path` → `{Kind:"file"}`;
     `g|glob` → free.
   - `photo`/`video` implement it for `pd|photo-dir`/`vd|video-dir` →
     `{Kind:"dir"}`; models free-text.
   - Prompt commands (`query`, `photo`, `video`, `glob`) implement
     `CompleteArgs` returning empty non-nil once prompt text begins —
     preserving today's "stop completing inside the prompt" rule.
   - `tools` parent implements `CompleteArgs` → tool names (detail view).
   - `completion` shell-name completion (`bash`/`zsh`) comes from upstream.
   - Chat subcommand names come from the phase-8 tree — the hand-list dies.
3. **Laziness constraint:** hook implementations load completion data
   (profiles/models/contexts read config dir; `tools.Init()` for tool names)
   lazily inside the hook call, memoized per process — `Flagset()` and
   `Subcommands()` stay pure per the README invariant.
4. **Behavior deltas vs today** (accepted, documented in notes): flag-name
   suggestions are per-command *and per-level* instead of one global list
   (strictly more correct post-phases-7/8, D11 — e.g. `clai a t -` offers
   `-af/-am/-parallelism`, `clai a -` does not); value completion for a
   flag typed before its command is no longer offered (upstream documented
   limitation);
   suggestions remain sorted, protocol/kinds unchanged so already-installed
   user shell scripts keep working — but scripts are re-emitted by upstream
   templates, so `clai completion bash|zsh` output changes shape
   (functional parity required, byte parity not).

## Integration contract

All rows drive the real binary path end-to-end (`cmd.Run` with
`__complete ...` argv), seeded temp config dir (profiles `gopher`, a
`textConfig`-derived model history entry, shell context `minimal`).

| # | Scenario | `__complete` words (after binary) | Expected lines include | Prohibited |
|---|----------|-----------------------------------|------------------------|------------|
| 1 | top-level commands | `clai ""` | `query`, `q`, `chat`, `c`, `completion` | `__complete` |
| 2 | per-command flags | `clai q -` | `-cm`, `-t`, `-mt` | `-pm`, `-af` |
| 3 | profile values | `clai q -p ""` | `gopher` | — |
| 4 | model history | `clai q -cm ""` | seeded model name | — |
| 5 | tool comma-split | `clai q -t website_text,we` | `website_text,web...` continuation | losing the prefix |
| 6 | file/dir kinds | `clai q -prp ""` / `clai photo -pd ""` | `\tfile` / `\tdir` kinded line | plain kind |
| 7 | chat subs from tree | `clai chat ""` | `list`, `l`, `dirv2` | hand-list drift |
| 8 | prompt suppression | `clai q hello ""` | no suggestions | flag/command fallback |
| 9 | no config dir | `clai q -p ""` with empty HOME | no suggestions, exit 0 | error output, dir creation |
| 10 | scripts functional | `clai completion bash` sourced in bash; complete `clai q -c` | `-cm` offered by the shell | script error |

## Acceptance criteria

- [x] Spec-1 symbols deleted; grep proves no references.
      Evidence: grep for all spec-1 symbols returns nothing;
      `internal/completion.go` shrank 548 → 256 lines (data loaders + hooks
      only).
- [x] All ten contract rows automated (row 10 via a bash-driven test, skipped
      cleanly when bash absent — never faked).
      Evidence: `Test_e2e_complete_engine` (rows 1–8 as subtests, incl.
      shell-name completion), `Test_e2e_complete_no_config_dir` (row 9),
      `Test_e2e_completion_script_drives_bash` (row 10: builds the real
      binary, sources the script, drives `_clai_completion` with
      `COMP_WORDS=(clai q -c)` → `-cm`; `t.Skip` when bash absent).
- [x] `__complete` remains side-effect-free: row 9 also asserts zero
      filesystem writes; no `tools.Init` occurs unless a tool-value hook is
      actually invoked (lazy proof: test with hook untriggered).
      Evidence: `Test_e2e_complete_no_config_dir` (config dir not created);
      `Test_e2e_complete_tool_init_is_lazy` (fresh registry stays empty on
      flag-name completion, populates on `-t` value completion).
- [x] Value-source parity: every `ValueSource`/`ValueKind` row of the old
      `completionGlobalFlags` table is either covered by a hook + test or
      explicitly retired in implementation notes.
      Evidence: parity table in implementation notes.
- [x] `make qa` exit 0.
      Evidence: exit 0 (both repos).

## Error coverage

| Failure condition | Expected outcome | Test |
|---|---|---|
| Malformed profile json in config dir | skipped silently, other suggestions intact | unit (existing data-loader behavior) |
| Config dir unreadable (perm) | no suggestions, exit 0, shell unharmed | e2e |
| Hook data load fails mid-completion | empty suggestions for that source only; exit 0 | unit |

## Implementation notes

**Session: Claude, 2026-08-28 (implementation, same session as phases 1–8).**

Deltas from spec:

- **Upstream extension** (recorded here and in the phase-4 addendum sense —
  same working tree, still uncommitted): the phase-4 engine suggested only
  subcommand names for a `Subcommander`, making the spec'd "tools parent
  implements CompleteArgs → tool names" unreachable. `completeWords` now
  appends `ArgCompleter` results after a Subcommander's sub names
  (`ArgCompleter` godoc updated; upstream `Test_complete_subcommanderArgMerge`).
  Second upstream addition: the built-in `completion` command implements
  `CompleteArgs` offering `bash`/`zsh` (upstream
  `Test_complete_builtinCompletionShells`), and
  `cmd.NewCompletionCommand(binName)` is exported so clai's `help` can list
  the auto-registered command in its table. Upstream `make qa` exit 0,
  `pkg/cmd` 97.3% coverage.
- Hooks are optional function fields on `claiCommand`
  (`completeFlagValue`/`completeArgs`); nil field → nil result → no
  suggestions, so one adapter type serves both hooked and plain commands.
- `completionSources` memoizes the config-dir data per `Commands()`
  construction (one per process in production; per-`run()` in tests, which
  keeps temp-config-dir tests isolated). Tool names are not memoized —
  `tools.Init` is already once-gated per registry.
- Mode constants `COMPLETION`/`HIDDEN_COMPLETION` deleted with their last
  references; clai no longer registers `completion`/`__complete` keys.

Value-source parity vs the old `completionGlobalFlags` table:

| Old row | New coverage |
|---|---|
| `cm/chat-model` (model) | `textFlagValues` → model history; e2e "model history" |
| `p/profile` (profile) | `textFlagValues`; e2e "profile values" |
| `asc/add-shell-context` (shell-context) | `textFlagValues`; e2e "shell context values" |
| `t/tools` (tool, comma-split) | `toolValueItems`; unit `Test_toolValueItems_commaSplit` + e2e |
| `prp/profile-path` (file) | `textFlagValues`; e2e "file and dir kinds" |
| `pd/photo-dir`, `vd/video-dir` (dir) | `mediaFlagValues`; unit + e2e |
| all value-taking rows without source (`I`, `af`, `am`, `parallelism`, `g`, `pm`, `pp`, `vm`, `vp`, `rf`, …) | retired: free text then and now (old engine's default branch also offered nothing) |
| bool rows (`r`, `re`, `dre`, `i`, …) | flag-name completion, now per-level from flagsets |

Accepted behavior deltas (spec 4) confirmed: per-level flag names
(`a t -` offers audio flags, `a -` does not — engine derives from level
flagsets), pre-command value completion gone (upstream limitation),
scripts re-emitted from upstream templates (`clai __complete` wiring +
`complete -F`/`#compdef clai` asserted; byte shape changed). New delta:
`clai tools <tab>` now also offers `list` (the phase-8 sub) ahead of tool
names.

Test upgrades: `TestHiddenCompletionOutputsExpectedFormat` superseded by
`Test_e2e_complete_engine/chat subs from tree` (the tree now emits alias
forms too); old engine unit tests deleted with the engine (upstream owns
that coverage); loader tests kept verbatim.

Verification: upstream `make qa` exit 0; clai `make qa` exit 0; full clai
suite green. (One earlier full-suite run hit the 10-minute go-test default
timeout in `internal/vendors/anthropic` while two QA sweeps ran
concurrently — pure resource contention; the package passes alone with
`-race -timeout=30s` and in the clean rerun.)

## Review findings

_(appended by reviewers)_
