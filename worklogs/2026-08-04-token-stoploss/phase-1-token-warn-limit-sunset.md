# Phase 1 — token-warn-limit sunset

**Status:** Complete
**Back to:** [README](./README.md)

## Goal

Remove the pre-query interactive `token-warn-limit` machinery entirely: no
field, no default, no prompt, no whitespace-heuristic token counter. This
phase runs first and is independent of the `stoploss` config plumbing
(Phase 2). Old configs that still carry the `token-warn-limit` key keep
loading cleanly.

## Specification

### Sunset `token-warn-limit` (delete, do not repurpose)

- `internal/text/conf.go`: remove the `TokenWarnLimit int
  json:"token-warn-limit"` field and the `TokenWarnLimit: 333333` entry in
  `text.Default`.
- `internal/text/querier.go`: remove the `TokenCountFactor` const, the
  `tokenWarnLimit` field, `tokenLengthWarning()`, and `countTokens()`.
- `internal/text/querier_setup.go:238`: remove
  `querier.tokenWarnLimit = userConf.TokenWarnLimit`.
- `internal/text/session_runner.go:52`: remove the
  `r.querier.tokenLengthWarning()` call and its error wrap
  (`run token warning`).
- `pkg/text/full.go:49`: remove `TokenWarnLimit: 300000`.
- Do NOT remove `internal/text/generic/stream_completer.go` or the anthropic
  `heuristicTokenCountFactor` — those back `InputTokenCounter`, which the
  stoploss fallback uses (D12).

### No migration

Old `textConfig.json` files carrying `token-warn-limit` unmarshal without
error (encoding/json ignores unknown keys). Regenerated configs (via
`clai setup` / `utils.CreateFile`) simply omit the dead key.

### Architecture docs

The architecture-doc updates (`architecture/query.md` "Token warning" step,
`architecture/config.md` sunset note) land with the feature docs in
Phase 6. This phase removes production code only; do not edit `architecture/`.

## Integration contract

| Input / trigger                                    | Collaborators / fakes                     | Externally observable result                                                                                            | Required side effects                                     | Prohibited side effects                          |
| -------------------------------------------------- | ----------------------------------------- | ----------------------------------------------------------------------------------------------------------------------- | -------------------------------------------------------- | ------------------------------------------------ |
| Old `textConfig.json` with `"token-warn-limit": 333333` | `utils.LoadConfigFromFile` → `text.Default` | Loads without error; the struct has no `TokenWarnLimit` field                                                           | none                                                     | no crash, no interactive prompt                  |
| `clai q ...` with an oversized first query          | existing runner                           | Query sent as-is; no y/N prompt, no token-length pre-check                                                               | none                                                     | interactive prompt; hang on stdin                |

## Acceptance criteria

1. `token-warn-limit` is gone from production source: no field, no default,
   no prompt, no `countTokens`, no `TokenCountFactor`, no `full.go` entry.
   The search may return the worklog's historical contract text. The generic
   and anthropic `InputTokenCounter` heuristic implementations are
   intentionally kept and are not part of the sunset.
2. `go build ./...` and `go vet ./...` pass after the deletions.
3. Old configs with `token-warn-limit` load cleanly (unit test); a run with
   an oversized first query neither prompts nor blocks on stdin.
4. `go test ./internal/text/ ./pkg/text/ -timeout=60s` green.

## Error coverage

| Failure condition                            | Expected error / recovery / external outcome               | Test                           |
| -------------------------------------------- | --------------------------------------------------------- | ------------------------------ |
| Old config still carries `token-warn-limit`  | Loads without error; key ignored                           | new unit test                 |
| Oversized first query                        | No prompt; request proceeds (provider may reject at inference level) | runner test (existing harness) |

## Implementation notes

Executed 2026-08-04 (imago + clai, worker session 1).

Deletions (delete, do not repurpose):

- `internal/text/conf.go`: removed the `TokenWarnLimit int
  json:"token-warn-limit"` field and the `TokenWarnLimit: 333333` entry in
  `text.Default`. The now-orphaned cost comment above the default
  (`Approximately $1 for an average flagship model`) was removed with it — it
  documented the dead threshold, not `ToolOutputRuneLimit`.
- `internal/text/querier.go`: removed `TokenCountFactor`, the
  `tokenWarnLimit` field, `tokenLengthWarning()`, `countTokens()`, and the now
  unused `bufio`, `errors`, and `path` imports. `strings` stays (reasoning
  buffer); `fmt`, `io`, `os`, `time` stay.
- `internal/text/querier_setup.go`: removed
  `querier.tokenWarnLimit = userConf.TokenWarnLimit`.
- `internal/text/session_runner.go`: removed the `tokenLengthWarning()` call
  and its `run token warning` error wrap from `Run`.
- `pkg/text/full.go`: removed `TokenWarnLimit: 300000` from
  `pubConfigToInternal`.

Kept (R1-07): `internal/text/generic/stream_completer.go` and the anthropic
`heuristicTokenCountFactor` — they back `models.InputTokenCounter` (D12), the
stoploss fallback seam.

Tests (written first, red-green where possible):

- `TestConfigurations_LegacyTokenWarnLimitKeyIgnored` (conf_test.go): wrote a
  `textConfig.json` carrying `"token-warn-limit":333333`, loaded it via
  `utils.LoadConfigFromFile`, asserted the load succeeds, the model value
  survives, and the regenerated file no longer contains the dead key. This
  test was red against the pre-sunset code (the struct field marshaled the key
  back into the regenerated file) and green after the deletions.
- `Test_sessionRunner_Run_OversizedFirstQueryNoTokenPrecheck`
  (session_runner_test.go): a 50k-word first query is delivered to the model
  as-is, the run completes, and no pre-check/prompt/stdin machinery engages.
  This is a behavior pin — a direct `sessionRunner` construction never set the
  old `tokenWarnLimit`, so the old prompt path could not fire in that harness;
  the observable contract (query sent as-is, run completes) is what is pinned.
  The no-prompt end-to-end claim is further covered by Phase 6 e2e case 5
  (pre-validated in Review 8, R8-03).

Gates (all before and after the change):

- Before: `go test ./internal/text/ ./pkg/text/ -timeout=60s` ✓
- After: `go test ./internal/text/ ./pkg/text/ -timeout=60s` ✓;
  `go build ./...` ✓; `go vet ./...` ✓; `go run mvdan.cc/gofumpt@latest -w -l
  .` ✓ (formatted `conf.go` alignment once);
  `go run honnef.co/go/tools/cmd/staticcheck@latest ./...` ✓;
  `go fix ./...` ✓; `go test ./... -race -cover -count=3 -timeout=30s` ✓;
  dupl baseline unchanged at 29 clone groups.

Verification of acceptance criterion 1: the sunset search
(`token-warn-limit|TokenWarnLimit|tokenWarnLimit|tokenLengthWarning|countTokens|TokenCountFactor`)
returns only the worklog contract text, `architecture/query.md:89` (deferred
by contract to Phase 6), the new pin tests, and the intentionally retained
`heuristicTokenCountFactor` implementations (R1-07).

## Review findings

- [x] **R1-07 — Low:** Sunset searches must exclude the worklog and the
  intentionally retained `InputTokenCounter` heuristic implementations
  (`internal/text/generic/stream_completer.go`, anthropic
  `heuristicTokenCountFactor`). These are fallback seams for the stoploss
  (D12), not sunset leftovers. — Verified by the executing agent: both
  implementations compile and are referenced by the rate-limit backoff path
  (`session_runner.go:waitForRateLimitReset`); the sunset search above lists
  them as the only remaining non-worklog, non-arch, non-test hits.

## Review findings (review 12, 2026-08-05)

None. Verified legacy configuration compatibility, removal of the interactive
pre-query path, and the architecture sunset search.
