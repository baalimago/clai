# Phase 10 — docs and quality gates

**Status:** Complete
[Worklog README](./README.md)

## Goal

Bring clai's documentation in line with the new dispatch/flag system and run
the full quality-gate sweep on the finished branch.

## Specification

Repo: clai, branch `refactor-flag-system`. Depends on phases 6–9 complete.

1. **Architecture docs** (`architecture/`):
   - New `architecture/cmd-dispatch.md`: the `cmd.Run` command map, adapter
     pattern in `internal/cmds/`, flag scoping table (link phase 7),
     `Subcommander` trees, sentinel semantics, completion hook wiring. Add
     to `architecture/README.md` index per its convention.
   - Sweep existing docs that describe the old system for staleness — at
     minimum `help.md`, `config.md`, `setup.md`, `query.md`, `chat.md`,
     `audio.md`, `tools-command.md`, `profiles.md`, `replay.md`, `dre.md`,
     `dirscope.md`, `shell-context.md` — update flag/command references
     (flags-after-command idiom, per-command `-h`).
2. **User-facing text:** root `README.md` examples, the top-level usage
   string, per-command `Help()` texts, and any completion install docs —
   consistent with the regression budget (R-a…R-e) and showing the new
   canonical idioms (`clai q -cm gpt-4 "hi"`; sub-level flags next to their
   verb, `clai a t -af text f.wav`, per D11).
3. **CLAUDE.md / AGENTS.md** guidance check: "Always read ./main.go" still
   yields a functional overview after the usage rework — adjust the usage
   string's content (not the guidance) if the overview degraded.
4. **Quality-gate sweep** (final, on the branch tip): `make qa` — gofumpt,
   staticcheck, `go vet`, `go test ./... -race -cover -count=3 -timeout=30s`
   (unedited), `go fix`, dupl (`-t 80`; clones adjudicated per repo policy).
   Coverage audit recorded for the new/changed packages (`internal/cmds`
   70%+ hard floor, 90%+ preferred).
5. **Dead-code audit:** grep-verify the deletions promised by phases 6–9
   (`Mode`, `getCmdFromArgs`, `parseFlags`, `resolveIntAlias`,
   `exitWithFlagError`, `isReadOnlyChatSubCommand`, `chatSubCommand`,
   `extractMacroInputs`, `hasEarlyCompletionCommand`, `handleAudio` verb
   switch, `completionGlobalFlags`, script literals) actually landed; file
   findings against the owning phase if any survive.

6. **Handover checklist (README D10 — write it, never execute it).** Append
   to implementation notes the exact steps Lorentz runs to complete the
   effort: (a) inspect + commit + push go_away_boilerplate `main`; (b) swap
   clai's `replace` for a pinned
   `go get github.com/baalimago/go_away_boilerplate@<sha-or-tag>`; (c) rerun
   `make qa`; (d) merge and cut a **minor** clai release (D11 — the R-e flag
   placement change ships as minor). Note plainly that a merged `replace`
   breaks clai CI (no local checkout there). Agents perform none of these
   steps.

## Integration contract

unit-test-only — this phase's observable outputs are documentation and the
recorded gate results; behavioral contracts live in phases 6–9 and are
re-run, not re-specified, here.

## Acceptance criteria

- [x] `architecture/cmd-dispatch.md` exists, indexed; every doc in spec 1
      checked, with the touched/untouched verdict listed in implementation
      notes.
- [x] README/usage/help examples all parse under the new system (executable
      doc test or manual matrix recorded with outcomes).
      Evidence: new `Test_e2e_usage_examples_parse` runs every flag/command
      shape from the usage examples and fails on any flag-definition or
      unknown-command error; `examples.md`'s one R-e-broken example fixed
      (`clai -af text a t …` → `clai a t -af text …`).
- [x] `make qa` exit 0 on the branch tip; exact command output summary
      (package count, coverage of new packages) in implementation notes.
- [x] Handover checklist (spec 6) written into implementation notes; zero
      commits/pushes/tags/releases performed by agents in either repo — the
      full effort is visible as `git diff` in both working trees (D10).
      Evidence: `git log` unchanged in both repos (clai @ `ce705df`,
      go_away_boilerplate @ `a76f78e`); checklist below.
- [x] Dead-code audit clean (spec 5), or findings filed and indexed in the
      README feedback index.
      Evidence: grep table in implementation notes — every promised symbol
      at zero occurrences; `Mode` survives as an internal config-shaping
      parameter only (per phase-6 criterion).

## Error coverage

| Failure condition | Expected outcome | Handling |
|---|---|---|
| A gate fails on the tip | fix in the owning phase (reopen it), not here; this phase only sweeps | README status board updated |
| Dupl flags a new clone in `internal/cmds` registrars | adjudicate per repo dupl policy; verdict recorded | implementation notes |
| Doc example found broken | fix doc or file finding against owning phase if behavior is wrong | feedback index |

## Implementation notes

**Session: Claude, 2026-08-28 (implementation, same session as phases 1–9).**

### Docs (spec 1–3)

`architecture/cmd-dispatch.md` created (dispatch flow, command map, adapter,
flag scope table, trees, sentinels, completion hooks, key files) and indexed
first under Core concepts in `architecture/README.md`.

Staleness sweep verdicts:

| Doc | Verdict |
|---|---|
| help.md | touched — new entry flow, generated table, per-command `-h`, nil-exit |
| config.md | touched — `migrateConfigs`/`configRunPrep`, adapter refs, registrar sentence |
| setup.md | touched — entry flow, sentinel wording, key files |
| query.md / photo.md / video.md | touched — entry flows + key-files rows |
| audio.md | touched — Subcommander tree flow; examples already fine |
| tools-command.md | touched — `List`/`Detail`, nil-exit |
| profiles.md | touched — `profiles.List()`, key files |
| replay.md / dre.md | touched — entry flows, adapter refs |
| colours.md | touched — theme load hook now `configRunPrep` |
| version.md | touched — adapter flow, nil-exit |
| dirscope.md | untouched — `setup.go:setupLookback` reference still accurate |
| shell-context.md | untouched — no dispatch references |
| chat.md, chat-groups.md, continue-from-claudex.md, streaming.md, tooling*.md, skills.md, openai-responses.md | untouched — no stale references (grep) |

User-facing: `examples.md` audio example moved to post-verb form; `main.go`
usage template gained a comment pointing agents at `internal/cmds.go` +
`cmd-dispatch.md` (spec 3 — the overview is now split between the template
and the generated table, and the comment restores the trailhead). Root
`README.md` needed no changes (no flag-placement examples; the coverage
badge line is CI-maintained).

### Quality gates (spec 4)

`make qa` on the branch tip → **exit 0**: gofumpt clean, staticcheck clean,
`go vet` clean, `go fix` clean, `go test ./... -race -cover -count=3
-timeout=30s` all 39 test packages ok. dupl (`-t 80`): 10 clone pairs, all
pre-existing test-file parallels (vendor test fixtures, confdir raw-mode
twins, setup wizard actions); none introduced by this effort's non-test
code — adjudicated acceptable per the repo's "signal, not verdict" policy.

Coverage of the new/changed files (combined main-e2e + unit profile,
`-coverpkg=…/internal`, deduplicated): `cmds.go` 83.7%, `completion.go`
83.7%, `setup_flags.go` 80.3%, `setup.go` 88.9%, `setup_audio.go` 90.5%,
`confdir.go` 87.5%, `version.go` 75.0%, `dre.go` 100% — all above the 70%
floor, most near the 90% preference. Upstream `pkg/cmd`: 97.3%.

Timing note: the main package's e2e binary runs in ~24s at `-race -count=3`
— **down from ~44s on the pre-refactor baseline** (measured via
`git archive HEAD` extraction on the same machine). One `make qa` run
failed the 30s per-package timeout while the machine sat at load average
~29; it passes cleanly once load settles. New chat-flavored tests pin
`HOME` to a temp dir to avoid the ~2.3s foreign-session scan of the real
`~/.claude/projects`.

### Dead-code audit (spec 5)

grep (non-worklog `*.go`), all zero: `getCmdFromArgs`, `parseFlags`,
`resolveIntAlias`, `exitWithFlagError`, `isReadOnlyChatSubCommand`,
`chatSubCommand(`, `extractMacroInputs`, `hasEarlyCompletionCommand`,
`handleAudio`, `completionGlobalFlags`, `ReturnNonDefault`, plus the
completion engine/scripts symbols (verified in phase 9). `Mode` remains as
a config-shaping parameter (no dispatch role). No findings to file.

### Diff summary (D10 done-state)

- go_away_boilerplate: dirty `main` @ `a76f78e` — 6 files modified
  (+245/−41) + 5 new files (`pkg/cmd/completion.go` + 4 test files);
  `git log` untouched.
- clai: dirty `refactor-flag-system` @ `ce705df` — 42 files modified
  (+880/−1867, **net −987 lines**) + new `internal/cmds.go`,
  `internal/cmds_test.go`, `architecture/cmd-dispatch.md`,
  `main_dispatch_e2e_test.go`, `main_nested_e2e_test.go`; `go.mod` carries
  the local `replace`; `git log` untouched.

### Handover checklist (spec 6 — for Lorentz; agents execute none of this)

1. Inspect the go_away_boilerplate working tree (`git diff` +
   untracked `pkg/cmd/*`); commit and push `main`.
2. In clai: remove the `replace github.com/baalimago/go_away_boilerplate =>
   ../go_away_boilerplate` line and pin the pushed upstream:
   `go get github.com/baalimago/go_away_boilerplate@<sha-or-tag>` +
   `go mod tidy`. **A merged `replace` breaks clai CI** — no local checkout
   exists there.
3. Rerun `make qa` in clai against the pinned version.
4. Inspect + commit the clai branch, merge, and cut a **minor** clai
   release (D11 — the R-e sub-level flag placement change and the R-a/R-d
   help/flag-scoping changes ship as a minor version).

## Review findings

### Review 1 (2026-08-28, holistic `/code-review high`, phases 11–15)

- **CR-05 (Minor, logged — docs):** prompts whose first word starts
  with `-` are hard flag errors under the new parser (`clai q -what is
  this` → `flag provided but not defined: -what` + usage dump). The
  behavior itself is budgeted (R-b), and `clai q -- -what is this`
  works — but `--` appears in no usage template, no `-h` output, not in
  README.md, examples.md, or `architecture/cmd-dispatch.md`, and the
  parse error does not suggest it. Document the `--` escape and
  consider a hint in the error path (`main.go:121`).

#### Fixes (2026-08-28, same session as the review)

- **CR-05 fixed (documentation half)** — the `--` escape is now documented
  in the dispatcher usage examples, in `clai q -h` (`queryHelp` gained a
  sentence and an example), `README.md`, `examples.md` and
  `architecture/cmd-dispatch.md`. Pinned by
  `Test_e2e_dash_leading_prompt_escape`: the functional subtest proves
  `clai -r -cm test q -- -what is this` returns the prompt verbatim, and
  two doc subtests assert the escape appears in both the usage and the
  query help. The error-message hint was **not** added: `cmd.Run` prints
  the parse error and returns only an exit code, so clai never sees the
  error value — a hint would have to be added to the upstream generic
  dispatcher and would touch its pinned error strings. Left for Lorentz to
  decide upstream.
