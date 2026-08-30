# Phase 2 — arity-aware command scan

**Status:** Complete
[Worklog README](./README.md)

## Goal

Make `cmd.parse` resolve the command correctly when space-separated
value flags precede it (`app -cm gpt-4 q hi` → command `q`), and align token
classification with stdlib `flag` semantics (`-flag=value`, `--`, bare `-`).

## Specification

Repo: go_away_boilerplate, branch `main` (README D9). File: `pkg/cmd/setup.go`
(scan loop, currently lines 49–58). Depends on phase 1 (Flagset purity —
the scanner walks every command's flagset each invocation).

1. **Arity table.** Before scanning, build `valueFlags map[string]bool` as the
   union over all registered commands:

   ```go
   c.Flagset().VisitAll(func(f *flag.Flag) {
       b, ok := f.Value.(interface{ IsBoolFlag() bool })
       if !ok || !b.IsBoolFlag() {
           valueFlags[f.Name] = true
       }
   })
   ```

   Conflict rule (documented on `Run`): same flag name with different arity
   across commands resolves as value-taking during the scan. Scope
   (README D11, documented on `Run` as well): the union covers **top-level**
   commands only — subcommand flagsets (phase 3) are invisible to the scan.
   A sub-owned flag must appear after its subcommand; placed before the
   command it is scanned as an unknown bool-arity flag and the invocation
   errors downstream. Levels are independent flag namespaces.

2. **Scan rules**, in order, per token:
   - `--`: stop treating subsequent dash-tokens as flags; the next token is
     the command candidate. The `--` token itself is **not** removed from the
     args later handed to the command's flagset (stdlib handles it).
   - bare `-`: positional (stdlib semantics) → it is the command candidate.
     (Today it is misclassified as a flag.)
   - `-name=value` / `--name=value`: flag, consumes nothing extra.
   - `-name` / `--name` where `valueFlags[name]`: flag, **skip the next
     token** (its value).
   - `-name` otherwise (bool or unknown): flag, consumes nothing extra.
   - anything else: command candidate; stop.
3. **After the scan**, behavior is unchanged: candidate matched against
   `"name|shortcut"` keys; matched token removed; all remaining args parsed
   by the command's flagset (which natively supports `-flag value`).
4. Unknown candidate still yields `ArgNotFoundError(candidate)`; no-candidate
   still yields `ErrNoArgs`.

Exported API unchanged; behavior is strictly additive — every invocation that
resolved a command before still resolves the same command.

## Integration contract

Mock commands: `q|query` with `String("cm")` + `Bool("re")`, `serve` with no
flags.

| # | Scenario | argv (after binary) | Observable result | Prohibited |
|---|----------|---------------------|-------------------|------------|
| 1 | value flag, space form, before command | `-cm gpt-4 q hi` | command `q`; after parse `cm=="gpt-4"`, positional `["hi"]` | `ArgNotFoundError("gpt-4")` |
| 2 | bool flag before command | `-re q hi` | command `q`; `re==true`; positional `["hi"]` | — |
| 3 | `=` form before command | `-cm=gpt-4 q` | command `q`; `cm=="gpt-4"` | — |
| 4 | flags after command (regression guard) | `q -cm gpt-4 hi` | identical outcome to row 1 | — |
| 5 | `--` terminator | `-re -- q -cm` | command `q`; `re==true`; positional `["-cm"]` (stdlib `--` handling) | `-cm` parsed as flag |
| 6 | bare `-` as command candidate | `-` | `ArgNotFoundError("-")` (candidate found, unmatched) | `ErrNoArgs` |
| 7 | unknown bool-arity flag before command | `-nope q` | command `q` still resolved; command's flagset then errors on `-nope` | scanner error |
| 8 | value flag at end, no command | `-cm gpt-4` | `ErrNoArgs`-class outcome (no candidate) | `gpt-4` resolved as command |
| 9 | arity conflict across commands | `-x` bool in cmd A, `String("x")` in cmd B; argv `-x A` | `x` treated value-taking: `A` consumed as value → no candidate → `ErrNoArgs`-class outcome, documented | silent divergence between runs |

## Acceptance criteria

- [x] All nine contract rows pass as table-driven tests against `parse`
      (rows 1–8) and `Run` (at least rows 1 and 4 end-to-end).
      Evidence: `Test_parse_arityAwareScan` (rows 1–5),
      `Test_parse_scanErrors` (rows 6–9 + error matrix),
      `Test_Run_arityAwareScan` (rows 1 and 4 through `Run`), all in
      `pkg/cmd/scan_test.go`.
- [x] Every pre-existing `pkg/cmd` test passes unmodified, except where one
      asserted the old broken classification (e.g. bare `-` as a flag, so
      `ErrNoArgs` where the new scan yields `ArgNotFoundError("-")`) — each
      such edit justified in implementation notes.
      Evidence: `go test ./pkg/cmd/... -race -count=3` green with zero
      pre-existing edits (no existing test asserted the old classification).
- [x] The arity-conflict rule and the `--`/`-` semantics are documented on
      `Run` (godoc), not only in tests.
      Evidence: `setup.go` `Run` doc comment (scan semantics, conflict rule,
      top-level-only scope per D11).
- [x] Scan cost is one `VisitAll` pass per command per invocation — no
      parsing, no allocation beyond the map (spot-check with `-bench` or
      reasoning recorded in notes).
      Evidence: reasoning in implementation notes.

## Error coverage

| Failure condition | Expected outcome | Test |
|---|---|---|
| No non-flag token at all (`-re -r`) | `ErrNoArgs`-class outcome → usage printed, exit 1 | row 8 variant |
| Candidate matches no command | `ArgNotFoundError` naming the candidate token (not its preceding flag's value) | row 1 negative variant: `-cm gpt-4 qq` names `qq` |
| Command flagset rejects a pre-command flag it doesn't own | flagset parse error surfaces via existing "failed to parse flagset" path | row 7 |
| Nil flagset on any registered command | error before/during scan; no panic from `VisitAll` | new unit |

## Implementation notes

**Session: Claude, 2026-08-28 (implementation, same session as phase 1).**

Deltas from spec:

- Scan implemented as two unexported helpers in `setup.go`:
  `valueFlagUnion(commands)` (arity table; errors on any nil flagset with the
  pre-existing "flagset is nil, please define flagset" text) and
  `findCommandCandidate(args, valueFlags)` (token classification per spec
  rules). `parse` shrank: the old post-match nil-flagset check is gone —
  `valueFlagUnion` now covers every registered command before the scan, which
  is strictly stricter (a nil flagset on *any* command errors, matching the
  error-coverage row) and keeps the existing error text so the pre-existing
  nil-flagset test passes untouched.
- Behavior change beyond old-broken cases: "no candidate found" previously
  fell through to `ArgNotFoundError("")` (empty candidate); it now returns
  `ErrNoArgs` (rows 8, 9). No pre-existing test asserted the old shape.
- Scan-cost reasoning (criterion 4): per invocation the scan does exactly one
  `VisitAll` per registered command to build one `map[string]bool`, then a
  single O(len(args)) index walk with no allocation (`TrimLeft`/`Contains`
  on existing strings); no `flag.Parse` runs before dispatch. No benchmark
  needed — the work is linear in flag count + arg count, identical in shape
  to the old scan plus one map.

Verification:

- `go test ./pkg/cmd/... -race -count=3 -cover -timeout=30s` → ok, 100.0%
  coverage on `pkg/cmd`.
- `make qa` → green (staticcheck, gofumpt, `go fix`, full test suite).
- Working tree still uncommitted on `main` per D10.

## Review findings

_(appended by reviewers)_
