# Phase 1 — token-warn-limit sunset

**Status:** Not Started
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

To be written by the executing agent.

## Review findings

- [ ] **R1-07 — Low:** Sunset searches must exclude the worklog and the
  intentionally retained `InputTokenCounter` heuristic implementations
  (`internal/text/generic/stream_completer.go`, anthropic
  `heuristicTokenCountFactor`). These are fallback seams for the stoploss
  (D12), not sunset leftovers.
