# Phase 2 — Table migration

**Status:** Done  
[Back to README](./README.md)

## Goal

Refactor `go_away_boilerplate/pkg/table` to use `pkg/dimensions` as its only terminal-size implementation.

## Specification

Remove the private ioctl and `COLUMNS` precedence from `pkg/table/term.go`. Preserve compatible `TermWidth` behavior as a wrapper where needed, but delegate it to `dimensions`. Add snapshot-aware width/truncation APIs so callers can avoid repeated discovery during one render operation. Update table tests to assert the shared provider contract rather than environment precedence.

## Integration contract

| Trigger | Collaborator | Observable result | Required side effect | Prohibited side effect |
|---|---|---|---|---|
| `table.TermWidth` called | dimensions viewer/provider | Current width returned | Delegates to shared package | No table-local ioctl |
| Width-aware truncation called with snapshot | Dimensions value | Text fits supplied width | Uses supplied width exactly | No second terminal query |
| Existing table caller used | Compatibility wrapper | Existing API remains usable | Same documented fallback | No breaking unrelated table behavior |

## Acceptance criteria

- `pkg/table` has no `TIOCGWINSZ`, `syscall` width query, or `COLUMNS` precedence.
- Snapshot-aware truncation is available and tested.
- Existing table tests are updated and pass.
- A repository search proves the old implementation is gone.
- New or changed table code has focused coverage for provider errors, invalid supplied dimensions, and compatibility-wrapper paths.

## Error coverage

| Failure | Expected behavior | Test |
|---|---|---|
| Shared provider fails | Preserve table's documented fallback/error behavior | wrapper failure test |
| Supplied width is zero/negative | Clamp safely | truncation edge test |
| Dimensions change between operations | Each explicit snapshot is respected | snapshot isolation test |
| explicit-width helper receives writer/formatting failure | Error is returned without a second query or partial silent fallback | helper error test |

## Implementation notes (2026-08-07, worker session 2 — execution)

Implemented in `go_away_boilerplate/pkg/table`. The package now consumes
`pkg/dimensions` as its only terminal-size implementation.

### API contract

- `TermWidth()` delegates to `dimensions.DefaultReader(os.Stderr.Fd())`
  through the injectable core `termWidth(read dimensions.Reader)`. The ioctl
  query, zero-size policy, and fallback value live in `pkg/dimensions`;
  `pkg/table` has no `TIOCGWINSZ`, `syscall`, `COLUMNS`, or `unsafe` code
  left.
- Failure policy (R1-02): on any provider failure `TermWidth` returns
  `(dimensions.Fallback.Width, nil)` = `(80, nil)`. This preserves the
  legacy silent fallback exactly: the historical implementation never
  returned a non-nil error either, so existing callers keep usable output.
  The wrapped `ErrUnavailable` is intentionally not observable through
  `TermWidth`; callers that need terminal awareness use `pkg/dimensions`
  directly and check `errors.Is(err, dimensions.ErrUnavailable)` (phase 3).
- Snapshot-aware APIs added so callers can render one operation with a
  resolved dimension set and no second query:
  - `WidthAppropriateStringTruncWithWidth(toShorten, prefix string, padding, width int) string`
  - `WidthAppropriateStringTruncColoredWithWidth(toShorten, prefix, prefixColor, truncColor string, padding, width int) string`
  Both use `width` exactly as supplied, never query the terminal, and clamp
  zero or negative widths to the prefix only.
- `WidthAppropriateStringTruncColored` now composes `TermWidth` with
  `WidthAppropriateStringTruncColoredWithWidth`; its `(string, error)`
  signature is unchanged for compatibility.

### Error-coverage mapping

| Failure | Expected behavior | Test |
|---|---|---|
| Shared provider fails | `TermWidth` returns `(Fallback.Width, nil)`; no error propagates | wrapper failure test (injected failing reader) |
| Supplied width is zero/negative | Clamp to prefix only, no panic | truncation edge test |
| Dimensions change between operations | Each explicit width is respected exactly | snapshot isolation test |
| explicit-width helper misuse | Resolved: the string truncation helpers have no writer or formatting failure surface, so the equivalent guarantee is “no second terminal query and no silent fallback to another width”. The helpers take `width` directly (their signatures contain no reader), and the snapshot-isolation and clamp tests prove the guarantee | helper error test (amended) |

### Test changes

- Removed the COLUMNS-precedence tests; replaced them with provider-contract
  tests that inject a `dimensions.Reader` (success and failure), plus one
  ambient-independent smoke test that `TermWidth()` always returns a
  positive width and nil error.
- Legacy wrapper tests (`WidthAppropriateStringTrunc`,
  `WidthAppropriateStringTruncColored`) now assert only ambient-independent
  properties (nil error, prefix preserved), because they query real stderr
  whose size varies by environment.
- The explicit-width tests are deterministic: fits, infix truncation, empty
  string, infix-length boundary, escaping, colors, clamp, and snapshot
  isolation.
- Removed the vestigial `COLUMNS` environment from `Test_table_selectNumbers`.

### Commands and results

Baseline before changes: `go test ./pkg/table/... -count=1 -timeout=120s`
passed; full module `go test ./... -race -count=1 -timeout=120s` passed.

After the change: `go test ./... -race -cover -count=3 -timeout=30s` passed
(table package coverage 96.6%), plus `go vet ./...`, `go run
honnef.co/go/tools/cmd/staticcheck@latest ./...`, `go run
mvdan.cc/gofumpt@latest -w -l .`, and `go fix ./...` were clean.
Cross-platform builds passed for darwin/amd64, linux/arm64, and
windows/amd64 (the whole module, including `pkg/table`). A repository search
finds no `TIOCGWINSZ`, `SYS_IOCTL`, `syscall`, `COLUMNS`, or `unsafe` in
`pkg/table`.

`dupl -t 80` reports only pre-existing clones in legacy test files and
`pkg/testboil`; none are in the changed code.

## Review findings (review 1, 2026-08-07)

**R1-02 — Major — resolved.** [x] The wrapper behavior is defined above and
in the phase 1 implementation notes: `TermWidth` returns `(80, nil)`
(`dimensions.Fallback.Width`) whenever the shared provider fails, preserving
usable output for existing callers; terminal-aware callers use
`pkg/dimensions` and `errors.Is`. Concrete cases and expected values are in
the error-coverage mapping.

Verified good: the phase explicitly requires removal of table-local ioctl and
`COLUMNS` precedence, and it requires explicit-width helpers that can prove no
second query occurs.