# Phase 5 — upstream QA sweep and docs

**Status:** Complete
[Worklog README](./README.md)

## Goal

Take phases 1–4 through go_away_boilerplate's own quality gates on `main`
and finish the docs, leaving the full change as an uncommitted working-tree
diff for Lorentz's inspection (README D10). Nothing is committed, pushed,
tagged, or released.

## Specification

Repo: go_away_boilerplate, branch `main`, working tree only (README D9+D10).

1. **Discover and run the repo's gates.** Inspect `Makefile` and
   `.github/workflows/` at the clone; run everything CI runs. Baseline
   regardless of what is found: `gofumpt -l .` clean, `go vet ./...`,
   `staticcheck ./...`, `go test ./... -race`.
2. **Docs.** Update `pkg/cmd/docs.go` (currently three lines) to document:
   the `Command` contract incl. Flagset purity, the arity-aware scan and its
   flag-name/arity convention, `Subcommander`, the completion feature +
   hooks, `ErrUserInitiatedExit` semantics (both sentinels honored). Follow
   whatever changelog/readme convention the repo already has — do not invent
   one.
3. **Compatibility audit.** Grep the module's own packages and known
   consumers available locally (clai) for `pkg/cmd` usage; confirm the only
   behavior changes are the ones specified in phases 1–4. Exported-API diff
   is add-only: `ErrUserInitiatedExit`, `Subcommander`,
   `DescribeSubcommands`, `CompletionKind`, `CompletionItem`,
   `FlagValueCompleter`, `ArgCompleter`.
4. **Diff summary for inspection (D10 — no push, no commit, no tag).**
   Record in implementation notes: the base `main` sha the diff applies to,
   `git diff --stat`, and the exported-API diff. Then **this phase adds** the
   `go.mod` `replace` directive to the clai checkout
   (`replace github.com/baalimago/go_away_boilerplate => ../go_away_boilerplate`,
   plus whatever `go mod tidy` demands) and verifies `go build ./...`
   succeeds there. The replace is left in place — it stays for the duration
   of the effort (README D9); phase 6 verifies its presence, it does not
   re-add it. This is the one permitted clai-tree edit in an upstream phase.

## Integration contract

unit-test-only (gate + docs mechanics; the behavioral contracts live in
phases 1–4). The externally observable results of this phase are the green
gate run on the dirty working tree, the recorded diff summary, and a clai
build that compiles through the `replace` directive.

## Acceptance criteria

- [x] Full upstream gate suite exit 0 on the working tree; exact commands +
      outcomes recorded in implementation notes.
- [x] `docs.go` covers every item in spec 2.
      Evidence: `pkg/cmd/docs.go` sections Commands (Flagset purity),
      Argument scan (arity convention), Subcommands, Completion (+hooks),
      Clean exits (both sentinels).
- [x] Exported-API diff recorded in notes and is add-only.
- [x] Base sha + `git diff --stat` recorded; `replace` directive added to
      clai's `go.mod` (spec 4) and clai `go build ./...` through it succeeds
      (recorded); zero commits/pushes/tags made in either repo (`git log`
      unchanged from base sha).

## Error coverage

| Failure condition | Expected outcome | Handling |
|---|---|---|
| A gate fails | fix forward in the working tree before declaring the phase done | note in journal |
| clai build breaks through `replace` | fix upstream (API drift is a phase 1–4 defect — reopen the owning phase) | README status board updated |
| Upstream remote moved since clone | irrelevant this phase — do not fetch/rebase; Lorentz reconciles at release time | note in journal |

## Implementation notes

**Session: Claude, 2026-08-28 (implementation, same session as phases 1–4).**

Gates discovered: `Makefile` only (`make qa` = staticcheck + gofumpt +
`go fix` + `go test ./... -race -count=3 -cover -timeout=30s`); no
`.github/workflows/` in the repo, no changelog/readme convention beyond the
top-level README (untouched — it doesn't enumerate packages).

Gate run (repo root, all exit 0):

- `go run mvdan.cc/gofumpt@latest -l .` → no output (clean)
- `go vet ./...` → clean
- `go run honnef.co/go/tools/cmd/staticcheck@latest ./...` → clean
- `go fix ./...` → clean
- `go test ./... -race -count=3 -cover -timeout=30s` → all ok;
  `pkg/cmd` 96.8%, `pkg/cmd/version` 73.7%, `pkg/table` 96.6%

Diff summary (D10):

- Base: `main` @ `a76f78e` ("feat: Add slogcolor"); `git log` unchanged —
  zero commits/pushes/tags in either repo.
- Modified: `pkg/cmd/{docs.go,setup.go,types.go}`,
  `pkg/cmd/setup_test.go`, `pkg/cmd/version/{version.go,version_test.go}`
  (6 files, +245/−41). Untracked new: `pkg/cmd/completion.go` +
  4 test files (`run_test.go`, `scan_test.go`, `subcommander_test.go`,
  `completion_test.go`).
- Exported-API diff (via `go doc` new vs `git show HEAD:` old): add-only —
  `ErrUserInitiatedExit`, `Subcommander`, `DescribeSubcommands`,
  `CompletionKind` (+ 3 consts), `CompletionItem`, `FlagValueCompleter`,
  `ArgCompleter`. All pre-existing symbols and signatures unchanged.
- Compatibility audit: no other package in the module imports `pkg/cmd`;
  clai (local consumer) had zero `pkg/cmd` imports pre-migration.

clai linkage (spec 4): `replace github.com/baalimago/go_away_boilerplate =>
../go_away_boilerplate` added to clai `go.mod`; `go mod tidy` ran (go.sum
updated); `go build ./...` in clai → OK. Replace left in place for phases
6–10.

## Review findings

_(appended by reviewers)_
