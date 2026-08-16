# Phase 1 — Agent API

**Status:** Complete (2026-08-15)

[← README](./README.md)

## Goal

Remove `WithOutputTo` from `pkg/agent` and add the three slog options.

## Specification

`pkg/agent/agent.go`:

- Delete `WithOutputTo` and the `out io.Writer` field on `Agent`.
- Add fields: `logger *slog.Logger`, `slogLevel slog.Level`, `slogRuneLimit int`.
- Add options:
  - `WithLogger(l *slog.Logger)`
  - `WithSlogLevel(level slog.Level)`
  - `WithSlogRuneLimit(n int)`
- Defaults (in `defaultConf`): `slogLevel: slog.LevelDebug`,
  `slogRuneLimit: 200`; `logger` stays nil (disabled).
- In `asInternalConfig()`, replace the removed `Out: a.out` with `Out: io.Discard`
  and set one `AgentSettings` pointer carrying all agent-only settings:

```go
conf.AgentSettings = &text.AgentSettings{
    Logger:           a.logger,
    Level:            a.slogLevel,
    RuneLimit:        a.slogRuneLimit,
    UsageRecorder:    a.usageRecorder,
    ToolCallRecorder: a.toolCallRecorder,
}
```

`pkg/agent/typed.go`:

- `NewTyped` / `NewTypedMetadata` already forward `Option`s through `New`; they
  hold no per-option fields. Keep that forwarding and add a propagation test.

`pkg/agent` tests:

- Remove/replace every `WithOutputTo` reference in `pkg/agent`, including
  `agent_test.go`, `typed_test.go`, `cmd_ban_e2e_test.go`, and
  `recorder_test.go`. With the agent now silent by default, the
  `WithOutputTo(io.Discard)` calls simply get dropped.
- Update recorder-propagation assertions to read `conf.AgentSettings.UsageRecorder`
  and `conf.AgentSettings.ToolCallRecorder`.
- Add propagation tests for the three slog options and the nil-logger case.

## Integration contract

`unit-test-only`.

## Acceptance criteria

- [x] `WithOutputTo` is gone from `pkg/agent` — `go test ./pkg/agent/` compiles with no reference
- [x] `asInternalConfig()` sets `Out: io.Discard` regardless of options — `TestAgent_asInternalConfig`
- [x] `asInternalConfig()` propagates `Logger`/`Level`/`RuneLimit` via `AgentSettings` — `TestAgent_WithLogger_propagates`, `TestAgent_WithSlogLevel_propagates`, `TestAgent_WithSlogRuneLimit_propagates`
- [x] `asInternalConfig()` propagates `UsageRecorder`/`ToolCallRecorder` via `AgentSettings` — `TestAgent_WithUsageRecorder`, `TestAgent_WithToolCallRecorder`
- [x] `asInternalConfig()` with a nil logger leaves `AgentSettings.Logger` nil — `TestAgent_WithLogger_nil_disables`
- [x] defaults: level `slog.LevelDebug`, rune limit 200, logger nil — `TestNew`
- [x] `NewTyped`/`NewTypedMetadata` forward the three options — `TestTypedQuerier_WithLogger_propagates`

## Error coverage

| Failure | Expected outcome |
| ------- | ---------------- |
| `WithLogger(nil)` | Logger disabled; `Configurations.AgentSettings.Logger == nil` |
| `WithSlogLevel` omitted | Defaults to `slog.LevelDebug` |
| `WithSlogRuneLimit(0)` or negative | Carried into `AgentSettings.RuneLimit` verbatim; D5 makes truncation treat `<= 0` as no cap |
| Recorders omitted | `AgentSettings.UsageRecorder`/`ToolCallRecorder` stay nil |

## Implementation notes

Executing agent: clai (worker session 2026-08-15-01).

- The phase landed as a partial working tree: `pkg/agent/agent.go` and
  `internal/text/conf.go` already carried the production changes (the
  `AgentSettings` struct + field and the three slog options), but no test
  compiled — every `WithOutputTo` reference in `pkg/agent` remained. This
  session completed the phase with the test sweep and docs (D8).
- `asInternalConfig` keeps setting the loose `UsageRecorder`/
  `ToolCallRecorder` fields alongside the grouped `AgentSettings` pointer.
  Phase 2 removes the loose fields from `Configurations` and moves
  `NewQuerier`'s recorder source onto `AgentSettings`; until then the loose
  assignments keep the recorder e2e tests (this phase's AC) green, because
  the current `NewQuerier` still reads them. *(Phase 2, 2026-08-15:
  completed — see D9 and the phase-2 doc.)*
- The `WithOutputTo(io.Discard)` calls in the e2e/recorder tests were simply
  dropped: with `Out` hardcoded to `io.Discard`, the agent is silent on
  stdout by construction.
- The old `TestAgent_Setup_receives_Out_in_config` and
  `TestAgent_Setup_receives_stdout_when_no_WithOutputTo` tests were replaced
  by one `TestAgent_Setup_receives_io_Discard`, which proves the querier
  creator receives `io.Discard` through the real `Setup` path.
- `stringsBuilderWriter` became dead after the `WithOutputTo` tests were
  removed and was deleted.

Verification (all run from the repo root):

```bash
go test ./pkg/agent/ -race -count=1 -timeout=120s   # ok 1.084s
```

```bash
go test ./pkg/agent/ -cover   # ok 0.193s, coverage: 94.2% of statements
```

```bash
go test ./... -race -cover -count=3 -timeout=30s   # all ok, pkg/agent 94.2%
```

```bash
go build ./...   # clean
```

```bash
go vet ./...   # clean
```

```bash
go run mvdan.cc/gofumpt@latest -w -l .   # no output (already formatted)
```

```bash
go run honnef.co/go/tools/cmd/staticcheck@latest ./...   # clean
```

```bash
go fix ./...   # no output
```

```bash
go run github.com/mibk/dupl@latest -t 80 .   # 10 clone groups, all pre-existing, none in pkg/agent
```

## Review findings

_(empty — no review findings raised for this phase.)_
