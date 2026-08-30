# Phase 11 — drop the `glob` command

**Status:** Complete
[Worklog README](./README.md)

## Goal

Remove the deprecated top-level `glob|g` command; `-g/-glob` on query/chat
remains the only globbing path.

## Specification

Repo: clai, branch `refactor-flag-system`. Depends on phases 6–10 (dispatch
cutover complete). The `glob` command has warned about its own deprecation
since before this effort; house policy ships breaking CLI changes as features
in the minor release this effort already requires (R-e precedent).

1. **Command removal.** `internal/cmds.go`: delete the `"glob|g"` map entry,
   `globHelp`, and the `mode == GLOB` deprecation-warning branch inside
   `textCommand`'s setup.
2. **Mode retirement.** `internal/setup.go`: delete the `GLOB` constant;
   `mode == GLOB || flagSet.Glob != ""` (line 204) loses its first operand;
   `mode != QUERY && mode != GLOB` (`shouldLogSkillDiscovery`, line 371)
   becomes `mode != QUERY`. Grep proves no remaining `GLOB` reference.
3. **Flag help de-staling.** `internal/setup_flags.go` `-g/-glob` description
   drops "This flag will deprecate glob mode in a future release." — the
   flag is now the only globbing path, describe it as such.
4. **Docs.** `architecture/cmd-dispatch.md`: remove `glob|g` from the command
   list and the four flag-scope table rows / config-touching list mentions.
   Stale comment in `internal/completion.go` ("query, chat, glob") updated.
5. **Kept.** The `-g/-glob` flag, `internal/glob` package, and
   `glob.Setup`-driven file loading are untouched.

## Integration contract

| # | Scenario | argv | Observable result | Prohibited |
|---|----------|------|-------------------|------------|
| 1 | glob command gone | `clai glob '*.go' hi` | unknown-command error naming `glob`, usage printed, exit 1 | glob query executed |
| 2 | `g` alias gone | `clai g '*.go' hi` | unknown-command error, exit 1 | glob query executed |
| 3 | flag path intact | `clai -g '*.txt' q summarize` (mock vendor, temp dir) | globbed files reach the prompt, exit 0 | — |
| 4 | usage table | bare `clai` | command table without `glob|g` | `glob|g` listed |

## Acceptance criteria

- [x] `grep -rn 'GLOB\|globHelp' internal/ main*.go` returns no non-test,
      non-`internal/glob` hits; `"glob|g"` absent from the command map.
      Evidence: grep clean post-edit (implementation notes).
- [x] Contract rows 1–4 automated (rows 1–2 may share one test; row 3 may be
      an existing `-g` e2e kept green, cited).
      Evidence: rows 1–2 `Test_e2e_glob_command_removed`; row 3
      `Test_e2e_usage_examples_parse` (`-glob *.go -cm test -r query hi`)
      kept green; row 4 `Test_goldenFile_usage_on_no_args`.
- [x] `main_help_e2e_test.go` usage golden updated: `glob|g` moves from the
      expected list to the forbidden list.
      Evidence: `Test_goldenFile_usage_on_no_args` forbidden list.
- [x] `architecture/cmd-dispatch.md` contains no `glob|g` command references
      (`-g` flag rows remain).
      Evidence: `grep -n glob architecture/cmd-dispatch.md` empty.
- [x] `make qa` exit 0.
      Evidence: exit 0 (second run; see load-flake note).

## Error coverage

| Failure condition | Expected outcome | Test |
|---|---|---|
| `clai glob ...` invoked | unknown-command error + usage, exit 1 | contract row 1 |
| `-g` with pattern matching no files | today's `glob.Setup` error behavior unchanged | existing glob package tests kept green |

## Implementation notes

**Session: Claude, 2026-08-28 (extension, same session as planning).**

No design deltas — implemented as specified. Removals: `"glob|g"` map
entry + `globHelp` + the deprecation-warning branch (`internal/cmds.go`),
`GLOB` constant + both mode checks (`internal/setup.go`), stale flag
description (`internal/setup_flags.go`), stale hook comment
(`internal/completion.go`), six doc lines (`architecture/cmd-dispatch.md`).
`internal/glob` package and its tests untouched.

Tests written first and observed red (`Test_e2e_glob_command_removed`,
updated usage golden) before the removal.

Verification: one `make qa` run tripped the main e2e package's 30s test
timeout under full-suite parallel load (alarm fired inside the skills
suite; package runs 20.4s standalone at `-race -count=3` — same
load-flake class phase 8 recorded, pre-existing budget pressure, not a
hang in new code). Standalone main-package run and a full `make qa` rerun
both exit 0.

## Review findings

_(appended by reviewers)_

## Review findings

_(appended by reviewers)_
