# Phase 3 — Emit hooks

**Status:** Complete (2026-08-15)

[← README](./README.md)

## Goal

Emit one truncated log line per completed message, additively, at each
semantic completion site.

## Specification

Add one helper on `Querier` (in `slog_output.go`):

```go
func (q *Querier[C]) logMessage(ctx context.Context, kind, text, tool string) {
    s := q.agentSettings
    if s == nil || s.Logger == nil {
        return
    }
    truncated, was := truncateMiddleRunes(text, s.RuneLimit)
    attrs := []any{"kind", kind, "text", truncated, "truncated", was}
    if tool != "" {
        attrs = append(attrs, "tool", tool)
    }
    s.Logger.Log(ctx, s.Level, "clai message", attrs...)
}
```

### Context threading

`logMessage` takes the live query `context.Context` (D6). Thread `ctx` through
these signatures. Every production caller already holds the query ctx, except
the legacy `Querier.postProcess` noted below.

- `closeReasoningIfOpen(ctx, session)` — the six call sites in
  `session_runner.go` (`executeModelStep`) all hold `ctx`.
- `finalizeAssistantTextBeforeToolCall(ctx, session, call)` →
  `finalizeAssistantTextPlain(ctx, session, pending, call)` /
  `finalizeAssistantTextRolling(ctx, session, pending, call)`.
- `emitAssistantToolCall(ctx, session, call)` /
  `emitAssistantToolCalls(ctx, session, calls)`.
- `emitToolResult(ctx, session, call, out)`.
- `sessionFinalizerer.Finalize(ctx, session)` /
  `sessionFinalizer.Finalize(ctx, session)` → `postProcessOutput(ctx, msg)`.
  The `Run` defer already captures `ctx`.
- legacy `Querier.postProcess()` (test-only) passes `context.Background()` to
  `Finalize`.

Update the direct test call sites to pass a ctx (usually `context.Background()`):
`countingFinalizer.Finalize` in `session_runner_test.go`,
`finalizeAssistantTextBeforeToolCall` in `session_runner_test.go`, and
`emitToolResult` in `querier_tool_test.go`.

### Hook sites

Wire the hook at these sites, without removing or reordering any existing write
to `q.out`:

1. `reasoning` — `closeReasoningIfOpen` (`internal/text/querier.go`),
   immediately after `q.reasoningActive = false`, once per block, using
   `q.reasoningBuf.String()` before any branch resets the buffer:
   `q.logMessage(ctx, "reasoning", q.reasoningBuf.String(), "")`.
2. `assistant` — `finalizeAssistantTextPlain` and
   `finalizeAssistantTextRolling` (`internal/text/tool_executor.go`), after the
   echoed-tool-call check, when the finalized `pending` prose is non-empty:
   `q.logMessage(ctx, "assistant", pending, "")`.
3. `tool_call` — `emitAssistantToolCalls` (`internal/text/tool_executor.go`),
   once per call in the loop: `q.logMessage(ctx, "tool_call", call.PrettyPrint(), call.Name)`.
4. `tool_result` — `emitToolResult` (`internal/text/tool_executor.go`), after
   `displayOut` is computed: `q.logMessage(ctx, "tool_result", displayOut, call.Name)`.
   The `load_skill` success path in `executeLoadSkill` must also log a
   `tool_result` with `text = userVisibleContent` (the final display body, after
   warnings are folded in) and `tool = "load_skill"`.
5. `final_answer` — `postProcessOutput` (`internal/text/querier.go`), at the
   top, before any display branch: `q.logMessage(ctx, "final_answer", newSysMsg.Content, "")`.

`logMessage` is the sole gate: it no-ops when `agentSettings` or its `Logger`
is nil, and is never gated by `structuredOutput`, `rawDisplay`, `debug`, or
`q.Raw`.

## Integration contract

`unit-test-only`. Display-invariance is asserted in Phase 4.

## Acceptance criteria

- [x] `logMessage` with nil settings or nil logger is a no-op — `TestQuerier_LogMessage_nilLogger`
- [x] `logMessage` truncates over-budget text, sets `truncated=true` — `TestQuerier_LogMessage_truncates`
- [x] `logMessage` forwards the passed ctx to the handler — `TestQuerier_LogMessage_ctx`
- [x] one record per completed message with correct `kind` — `TestQuerier_LogMessage_kind` (table over five kinds)
- [x] `tool_call`/`tool_result` carry `tool`; other kinds do not — `TestQuerier_LogMessage_toolAttr`
- [x] reasoning emits once per block, not per token — `TestQuerier_CloseReasoningIfOpen_logsOnce`
- [x] assistant prose emits only when finalized non-empty, not for echoed tool-call text — `TestFinalizeAssistantText_Logs`
- [x] `postProcessOutput` logs `final_answer` even when display returns early (raw) — `TestPostProcessOutput_LogsFinalAnswer_unconditionally`
- [x] `load_skill` success logs one `tool_result` with `tool=load_skill`, `text=userVisibleContent` — `TestExecuteLoadSkill_LogsToolResult`
- [x] full `internal/text` suite passes unchanged — package gate

## Error coverage

| Failure                          | Expected outcome                                                       |
| -------------------------------- | ---------------------------------------------------------------------- |
| Logger nil                       | No-op                                                                  |
| Text over rune limit             | Truncated + `truncated=true`                                           |
| Empty reasoning buffer on close  | No `reasoning` record                                                  |
| Empty pending prose on tool call | No `assistant` record                                                  |
| Echoed tool-call text            | No `assistant` record                                                  |
| `load_skill` success             | One `tool_result` record, `tool=load_skill`, `text=userVisibleContent` |
| Ctx-aware handler                | Observes the ctx value passed to `logMessage`                          |

## Implementation notes

Executing agent: clai (worker session 2026-08-15-03).

- `logMessage` landed at the bottom of `internal/text/slog_output.go` next to
  the truncation helpers. It is the sole gate: nil `agentSettings` or nil
  `Logger` returns before logging; nothing else gates it. The record level is
  the single caller-set `AgentSettings.Level`, and the `kind` attribute is the
  only per-message discriminator (D3).
- Context threading follows D6: `closeReasoningIfOpen`, the
  `finalizeAssistantTextBeforeToolCall` → plain/rolling pair,
  `emitAssistantToolCall(s)`, `emitToolResult`, `sessionFinalizerer`/
  `sessionFinalizer`, and `postProcessOutput` all gained a `ctx` parameter
  carrying the live query context. The session runner passes its own `ctx`;
  the legacy test-only `Querier.postProcess` passes `context.Background()`.
- Semantic-empty gates live at the hook sites (D10): `closeReasoningIfOpen`
  captures `q.reasoningBuf.String()` before any branch resets the buffer and
  skips the record when it is empty; both `finalizeAssistantText*` variants
  log only after the echoed-tool-call check, so echoed or empty prose emits
  nothing.
- `emitToolResult` logs after the `<NO-OUTPUT>` substitution, so `displayOut`
  is the final display body. The `load_skill` success path never calls
  `emitToolResult`; it logs its own `tool_result` with the warning-folded
  `userVisibleContent` and `tool = "load_skill"` right before the assistant
  tool-call echo.
- No existing write to `q.out` was moved or reordered: the hooks are purely
  additive, and display-invariance is asserted by Phase 4.
- Test helpers: `captureHandler` (a slog handler that records every record
  with its context) and `recordAttrs` (flattens a record's attributes) live in
  `slog_output_test.go`; `emit_hooks_test.go` holds the four hook-site tests.
  `logMessage` reaches 100% statement coverage.

Verification (all run from the repo root):

```bash
go build ./...   # clean
```

```bash
go vet ./...   # clean
```

```bash
go test ./internal/text/ -run 'TestQuerier_LogMessage|TestQuerier_CloseReasoningIfOpen|TestFinalizeAssistantText_Logs|TestPostProcessOutput_LogsFinalAnswer|TestExecuteLoadSkill_LogsToolResult' -v -count=1 -timeout=60s   # all pass
```

```bash
go test ./... -race -cover -count=3 -timeout=30s   # all ok; internal/text 75.6%, pkg/agent 94.2%
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
go run github.com/mibk/dupl@latest -t 80 .   # 32 clone groups, all pre-existing, none in this phase's files
```

## Review findings

_(empty)_
