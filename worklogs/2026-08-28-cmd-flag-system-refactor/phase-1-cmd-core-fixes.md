# Phase 1 — `pkg/cmd` core fixes

**Status:** Complete
[Worklog README](./README.md)

## Goal

Fix the four foundational `pkg/cmd` defects — `Flagset()` purity contract,
`Setup` ctx pass-through, deterministic help ordering, and the
`cmd.ErrUserInitiatedExit` clean-exit sentinel — without breaking any existing
consumer.

## Specification

Repo: go_away_boilerplate, branch `main` (local checkout per README D9;
one-time clone to `~/Projects/not_wasmer/go_away_boilerplate` if absent).
Files:
`pkg/cmd/types.go`, `pkg/cmd/setup.go`, `pkg/cmd/version/version.go`.

1. **Flagset contract.** Document on `Command.Flagset()` that it must be pure
   and memoized: repeated calls return the same instance; no IO or side
   effects. Fix `cmd/version/version.go:29-31` (its `Flagset()` returns a
   fresh `flag.NewFlagSet` per call) by memoizing the flagset on the
   `command` struct.
2. **ctx pass-through.** `cmd/setup.go:24`: `command.Setup(context.Background())`
   → `command.Setup(ctx)`.
3. **Deterministic help.** `formatCommandDescriptions` sorts the command-map
   keys before writing to the tabwriter.
4. **Clean-exit sentinel** (decision D3). Add to `pkg/cmd`:

   ```go
   // ErrUserInitiatedExit signals that the user deliberately ended the
   // command (quit key, ctrl-c, "no thanks"). Run treats it as a clean,
   // silent, successful exit.
   var ErrUserInitiatedExit = errors.New("user initiated exit")
   ```

   `cmd.Run` checks the error from **both** `Setup` and `Run`: when
   `errors.Is(err, cmd.ErrUserInitiatedExit) || errors.Is(err, table.ErrUserInitiatedExit)`,
   return 0 and print nothing (an `ancli` debug/notice line is permitted only
   behind an env-gated debug check if one already exists upstream; default is
   silence). `table.ErrUserInitiatedExit` itself is not modified, moved, or
   aliased. Import direction is `cmd → table`; verify no cycle (`table` must
   not import `cmd`).

Constraints: exported API is add-only (`ErrUserInitiatedExit` is the only new
symbol); no new dependencies; `testboil` conventions for new tests.

## Integration contract

| # | Scenario | Input / trigger | Collaborators | Observable result | Prohibited |
|---|----------|-----------------|---------------|-------------------|------------|
| 1 | Setup gets caller ctx | `cmd.Run(ctx, ...)` with a value on ctx; mock command reads it in `Setup` | mock `Command` | value visible in `Setup`; `Run` receives same ctx | `context.Background()` reaching `Setup` |
| 2 | Setup returns `cmd.ErrUserInitiatedExit` | mock `Setup` returns the sentinel | mock `Command` | `cmd.Run` returns 0 | any stderr/stdout output; `Run` being called |
| 3 | Run returns wrapped `table.ErrUserInitiatedExit` | mock `Run` returns `fmt.Errorf("x: %w", table.ErrUserInitiatedExit)` | mock `Command` | `cmd.Run` returns 0 | error text printed |
| 4 | Run returns ordinary error | mock `Run` returns `errors.New("boom")` | mock `Command` | `cmd.Run` returns 1; `ancli` error line contains "boom" | exit 0 |
| 5 | Help order stable | ≥3 commands with unsorted map keys; trigger usage print (no args) | mock commands | command lines appear in lexicographic key order; two invocations byte-identical | order varying between runs |
| 6 | version flagset memoized | `version.Command().Flagset()` called twice | `cmd/version` | same pointer both calls; `Parse` state persists | fresh instance per call |

## Acceptance criteria

- [x] `Command.Flagset()` doc comment states the purity + memoization
      contract; `cmd/version` satisfies it (contract row 6).
      Evidence: `types.go` interface doc; `version.TestFlagsetMemoized`.
- [x] `Setup` receives the ctx passed to `cmd.Run` (row 1).
      Evidence: `cmd.Test_Run_ctxPassthrough` (value visible in Setup, Run
      gets identical ctx).
- [x] Both sentinels, direct and wrapped, from `Setup` or `Run`, yield exit 0
      with no output (rows 2, 3).
      Evidence: `cmd.Test_Run_userInitiatedExit` (5 subtests: both sentinels,
      both phases, wrapped and double-wrapped; asserts empty stdout+stderr and
      that Run is not called after Setup exit).
- [x] Ordinary errors still print and yield exit 1 (row 4).
      Evidence: `cmd.Test_Run_ordinaryErrorStillFails` ("boom" on stderr,
      exit 1) plus existing `Test_Run` rows kept green.
- [x] Usage/command listing is deterministic (row 5).
      Evidence: `cmd.Test_formatCommandDescriptions_sorted` (4 unsorted keys,
      lexicographic order asserted, 10 invocations byte-identical).
- [x] All pre-existing `pkg/cmd` and `pkg/table` tests pass unmodified except
      where they asserted the old broken behavior (each such edit justified in
      implementation notes).
      Evidence: `go test ./pkg/cmd/... ./pkg/table/... -race -count=3` green;
      zero pre-existing assertions edited (see notes).

## Error coverage

| Failure condition | Expected outcome | Test |
|---|---|---|
| `Flagset()` returns nil | existing "flagset is nil" error preserved, exit 1 | existing test kept green |
| Sentinel double-wrapped (`%w` twice) | still exit 0 (via `errors.Is`) | new unit |
| `Setup` returns non-sentinel error | "failed to setup command" path, exit 1 | existing test kept green |
| ctx already canceled before `Run` | `Setup`/`Run` receive the canceled ctx unaltered (dispatcher adds no masking) | new unit |

## Implementation notes

**Session: Claude, 2026-08-28 (implementation).**

Deltas from spec:

- One-time clone performed (upstream `main` @ `a76f78e`, matches README).
- Error-coverage row "ctx already canceled" implemented as
  `Test_Run_canceledCtxUnaltered` (asserts `ctx.Err() == context.Canceled`
  inside both `Setup` and `Run`).
- Sentinel checks factored into unexported `isUserInitiatedExit(err)` —
  exported API remains add-only (`ErrUserInitiatedExit` is the sole new
  symbol). No env-gated debug line existed upstream, so sentinel exits are
  fully silent per spec default.
- `mockCommand` gained an additive optional `setupCtxFunc` field (Setup
  prefers it when set) so new tests can observe ctx; no pre-existing test
  body or assertion was modified. New tests live in `pkg/cmd/run_test.go`.
- `version` memoization: nil-check lazy init on the `command` struct;
  `Command()` constructor untouched.

Verification (all run at repo root):

- `go test ./pkg/cmd/... ./pkg/table/... -race -count=3 -cover -timeout=30s`
  → ok; `pkg/cmd` 100.0% coverage, `pkg/cmd/version` 73.7%, `pkg/table`
  96.6% (untouched).
- `make qa` (staticcheck, gofumpt, `go fix`, full `go test ./... -race
  -count=3 -cover -timeout=30s`) → green, no lint output, all packages ok.
- Working tree left dirty on `main` per D10: 5 modified files + new
  `pkg/cmd/run_test.go`, nothing committed.

## Review findings

_(appended by reviewers)_
