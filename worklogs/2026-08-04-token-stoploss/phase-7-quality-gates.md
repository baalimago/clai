# Phase 7 — Quality gates

**Status:** Not Started
**Back to:** [README](./README.md)

## Goal

Run the repository's full quality gate and re-establish the duplication
baseline after the change.

## Specification

Commands (from AGENTS.md and the Makefile):

```bash
go run github.com/mibk/dupl@latest -t 80 .
make qa
go run github.com/mibk/dupl@latest -t 80 .
```

`make qa` runs `lint` (staticcheck, gofumpt, go fix) and
`go test ./... -race -count=3 -cover -timeout=30s`.

Baseline before the change (2026-08-04): dupl reports 29 clone groups; the
README "Verified good" section records it. The post-change run must not add
needless clone groups beyond what the feature legitimately requires (the
ladder text duplication between `applyToolCallBudget` and any new warning
paths is the known acceptable case — prefer extracting shared warning strings
as constants).

## Acceptance criteria

1. `go build ./...` passes.
2. `go vet ./...` passes.
3. `make qa` passes (staticcheck + gofumpt + go fix + race tests ×3 + cover).
4. `go run github.com/mibk/dupl@latest -t 80 .` shows no new clone groups
   beyond the 29-group baseline (same count or a documented delta).
5. Every phase in the README status board is Complete, with each acceptance
   criterion citing its test.

## Implementation notes

To be written by the executing agent.
