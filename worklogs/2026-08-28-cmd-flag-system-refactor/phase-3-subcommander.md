# Phase 3 — `Subcommander` interface and nested dispatch

**Status:** Complete
[Worklog README](./README.md)

## Goal

Make command nesting first-class in `pkg/cmd` via an optional `Subcommander`
interface, so nested commands get real per-level flagsets, nested help, and
visibility to the completion engine (phase 4).

## Specification

Repo: go_away_boilerplate, branch `main` (README D9). Files: `pkg/cmd/types.go`,
`pkg/cmd/setup.go`. Depends on phases 1–2.

1. **Interface** (optional; detected via type assertion):

   ```go
   // Subcommander is optionally implemented by a Command that owns nested
   // subcommands. Keys use the same "name|shortcut" form as the top-level
   // command map. Subcommands() must be pure and memoized, like Flagset().
   type Subcommander interface {
       Subcommands() map[string]Command
   }
   ```

2. **Dispatch** (decision D5). After the top-level command is resolved and its
   flagset parsed, if it implements `Subcommander`:
   - Take `fs.Args()`. If the **first positional** matches a subcommand key
     (split on `|`), descend: recurse the resolution (sub's flagset parses
     the remaining args — arity-aware scan is unnecessary at depth because
     the parent's flagset already consumed parent-level flags; the sub name
     is by construction `fs.Args()[0]`), then `Setup`+`Run` the subcommand.
     Descent repeats if the subcommand is itself a `Subcommander`.
   - If it does **not** match (or there are no positionals), the parent's own
     `Setup`/`Run` proceed with the args untouched. The parent decides what
     unknown positionals mean (allows `profiles` ≡ `profiles list`; a parent
     that wants strictness errors in its own `Run`).
   - Placement rule (normative, README D11): parent-level flags go before
     the sub name (`app chat -r list`), sub-level flags after it
     (`app chat list -x`). A sub-level flag before the sub name is an
     error, not a fallback. Each level is an independent flag namespace —
     a name/abbreviation may be reused with different meaning or arity at
     different levels (the parent's flagset never sees the sub's flags and
     vice versa). Document all of this on the interface.
3. **Nested help.**
   - `flag.ErrHelp` from a subcommand's flagset routes to the *subcommand's*
     `Help()` (existing `printHelp` mechanism, exercised at depth).
   - The **dispatcher** appends a `Subcommander` parent's subcommand table
     (`formatCommandDescriptions` of the sub map, sorted per phase 1) to
     its `Help()` output automatically — the parent's own `Help()` does not
     need to know about it. The formatter is additionally exported so apps
     can compose custom layouts:
     `cmd.DescribeSubcommands(map[string]Command) string`.
4. `Setup` is called on the **executed** command only (the leaf that runs) —
   a parent whose subcommand executes does not get `Setup`/`Run` called.
   Document this; if a parent needs shared setup for its children, it does so
   in `Subcommands()` construction or the children's `Setup`.

Exported additions: `Subcommander`, `DescribeSubcommands`. No changes to the
`Command` interface itself.

## Integration contract

Mock tree: `chat|c` (Subcommander, own `Bool("r")`, subs `list|l` with
`Bool("x")`, `del`) plus plain `q|query`.

| # | Scenario | argv (after binary) | Observable result | Prohibited |
|---|----------|---------------------|-------------------|------------|
| 1 | descend by full name | `chat list` | `list.Run` executes; `chat.Run` not called; `chat.Setup` not called | parent Setup/Run firing |
| 2 | descend by shortcut at both levels | `c l` | same as row 1 | — |
| 3 | parent flag before sub, sub flag after | `chat -r list -x` | `r==true` on chat's flagset, `x==true` on list's; `list.Run` executes | flags leaking across levels |
| 4 | unmatched positional stays with parent | `chat banana` | `chat.Setup`+`chat.Run` execute; chat's flagset `Args()==["banana"]` | `ArgNotFoundError` from dispatcher |
| 5 | no positional | `chat` | `chat.Run` executes | error |
| 6 | sub help | `chat list -h` | exit 0; `list.Help()` printed | parent help printed |
| 7 | parent usage lists subs | `chat -h` | exit 0; output contains sub table (sorted: `del` before `list\|l`) | — |
| 8 | sentinel at depth | `list.Run` returns `cmd.ErrUserInitiatedExit` | exit 0, silent | error output |

## Acceptance criteria

- [x] All eight contract rows pass end-to-end through `cmd.Run`.
      Evidence: `Test_Run_subcommanderDescent` (rows 1–8) and
      `Test_Run_subcommanderErrors` in `pkg/cmd/subcommander_test.go`.
- [x] Two-level nesting proven (a sub that is itself a `Subcommander`) —
      one added scenario in tests.
      Evidence: subtest "two-level nesting reaches the leaf"
      (`top m leaf` executes the leaf).
- [x] `Subcommander` + `DescribeSubcommands` godoc states: key form, purity/
      memoization, flag placement rule + per-level namespace independence
      (D11), leaf-only `Setup`/`Run`, unmatched-positional fallthrough.
      Evidence: `types.go` `Subcommander` doc, `setup.go`
      `DescribeSubcommands` doc.
- [x] Existing single-level consumers unaffected (full upstream suite green).
      Evidence: `make qa` green; no pre-existing test edited.

## Error coverage

| Failure condition | Expected outcome | Test |
|---|---|---|
| Subcommand flagset parse error | existing "failed to parse flagset" path, exit 1 | `chat list -bogus` |
| Sub's `Flagset()` nil | same "flagset is nil" error as top level | unit |
| `Subcommands()` returns nil/empty map | treated as non-Subcommander (parent handles args) | unit |
| Sub `Setup` fails | "failed to setup command" path, exit 1; parent untouched | unit |

## Implementation notes

**Session: Claude, 2026-08-28 (implementation, same session as phases 1–2).**

Deltas from spec:

- Descent implemented as `resolveSubcommands(command)` called from `Run`
  between `parse` and `Setup`; errors route through the existing `printHelp`,
  so `flag.ErrHelp` from a sub's flagset (wrapped in "failed to parse
  flagset: %w") lands on the sub's `Help()` via `errors.Is` — no new help
  plumbing.
- The old inline OUTER matching loop in `parse` was extracted to
  `matchCommand(candidate, commands)` and reused for sub matching —
  net simplification, no behavior change.
- Sub-table help composition lives in `helpText(command)`, used by
  `printHelp`'s `flag.ErrHelp` branch; format is
  `Help() + "\n\nsubcommands:\n" + DescribeSubcommands(subs)`.
- `Subcommands()` is called once per descent level (nil/empty short-circuit
  via `matchCommand` returning nil on empty maps handled by `len` check in
  `helpText` only; dispatch needs no special case since matching an empty
  map yields nil → parent keeps the args).

Verification:

- `go test ./pkg/cmd/... -race -count=3 -cover -timeout=30s` → ok, 100.0%
  coverage on `pkg/cmd`.
- `make qa` → exit 0, no findings.
- Working tree still uncommitted on `main` per D10.

## Review findings

_(appended by reviewers)_
