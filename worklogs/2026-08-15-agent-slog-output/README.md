# Agent slog output

Replace the broken `WithOutputTo` writer injection on `pkg/agent` with an
optional `log/slog` logger. Embedded (library) use becomes logger-only and
never writes raw terminal output. The interactive CLI display path is
untouched.

## Status board

| Phase | Deliverable                                                                                                                       | Status      |
| ----- | --------------------------------------------------------------------------------------------------------------------------------- | ----------- |
| 1     | [Agent API](./phase-1-agent-api.md) — remove `WithOutputTo`, add `WithLogger`/`WithSlogLevel`/`WithSlogRuneLimit`                 | Complete (2026-08-15) |
| 2     | [Config + querier plumbing](./phase-2-config-plumbing.md) — `AgentSettings` struct, `Querier` field, rune middle-truncation helper | Complete (2026-08-15) |
| 3     | [Emit hooks](./phase-3-emit-hooks.md) — log each completed message at its semantic completion site                                | Complete (2026-08-15) |
| 4     | [CLI-invariance + gates](./phase-4-cli-invariance-gates.md) — prove display unchanged, full gate sweep                            | Complete (2026-08-15) |

## Feedback index

| ID  | Severity | Phase | Summary |
| --- | -------- | ----- | ------- |
| —   | —        | —     | —       |

## Strategy

The public library API today offers `agent.WithOutputTo(io.Writer)` to choose
where the querier's terminal-style display goes. For embedded consumers that
knob is broken: with structured output (`WithResponseFormat`) the streamed
tokens, reasoning, and tool rendering are suppressed, and with a non-terminal
`out` the final answer is also suppressed. So the writer is set but nothing
useful is written.

The fix removes the writer knob from `pkg/agent` and replaces it with three
options that feed a structured, per-message logging channel:

```go
agent.WithLogger(l *slog.Logger)      // nil = disabled (default)
agent.WithSlogLevel(level slog.Level) // default slog.LevelDebug
agent.WithSlogRuneLimit(n int)        // default 200
```

Those three values, plus the existing recorder hooks, are carried into the
querier through one agent-only `text.AgentSettings` pointer (D7), not as loose
`Configurations` fields.

Load-bearing invariants (every phase must preserve them):

1. **CLI output must not change.** The CLI does not use `pkg/agent`; it builds
   `internal/text.Configurations` directly and sets `Out` to stdout/file. We
   do not modify any existing write to `Querier.out`. The new logger hooks are
   additive only.
2. **Library output is logger-only.** `pkg/agent` stops exposing `Out` and
   hardcodes `Out: io.Discard` in `asInternalConfig`, so embedded use never
   writes raw terminal output. This reproduces exactly what sakfråga did by
   hand with `WithOutputTo(io.Discard)`.
3. **The logger is unconditional.** It fires on every completed message and is
   not gated by `structuredOutput`, `rawDisplay`, `debug`, or terminal
   detection. Those gates remain display-only.
4. **No streaming.** The logger fires once per _completed_ message, never per
   streamed token.
5. **Rune-safe middle truncation.** Every logged text is capped to
   `AgentSettings.RuneLimit` runes by a head/tail split around the single-rune
   marker `…`; a multi-byte rune is never cut in half.

Completed-message kinds (the `kind` attribute): `assistant`, `reasoning`,
`tool_call`, `tool_result`, `final_answer`.

Log line shape:

```go
logger.Log(ctx, level, "clai message",
    "kind", kind,
    "text", truncatedText,
    "truncated", wasTruncated,
    // tool_call / tool_result only:
    "tool", toolName,
)
```

`ctx` is the live query context, threaded through the hooks (D6). `level` is
the single caller-set `SlogLevel` (D3). The caller controls the level; the
`kind` attribute is how the caller filters if it needs finer control.

## Decisions log

- **D1 (Q1):** inject `*slog.Logger` directly (`WithLogger`), not a narrow
  interface and not a content event sink. `log/slog` is stdlib, and sakfråga
  already logs with `slog` + `slogcolor` and a `corrID` attr, so a per-job
  `slog.With("corrID", ...)` propagates for free.
- **D2 (Q2):** per-completed-message logging with rune-based middle
  truncation. Runes, not tokens: clai's token count is a whitespace heuristic,
  while `utf8.RuneCountInString` is exact. 50 tokens ≈ 200 runes. The marker is
  the single rune `…`: the `truncated` bool is the machine signal, `…` is the
  human signal.
- **D3 (Q3):** single caller-set level via `WithSlogLevel`; no per-kind level
  table inside clai. Default Debug.
- **D4 (Q4/Q5):** remove `WithOutputTo` entirely. Library mode is silent on
  stdout; the logger is the sole embedded output channel. The CLI's internal
  display (`Configurations.Out` → `Querier.out`) stays and must not change.
- **D5:** `RuneLimit <= 0` means no cap: `truncateMiddleRunes` returns the input
  unchanged with `truncated=false`.
- **D6:** the slog hooks receive the live query `context.Context`, threaded
  through the session runner, finalizer, and tool executor. `log/slog` forwards
  ctx to the handler, so a ctx-aware external handler (e.g. OpenTelemetry) can
  enrich or correlate records. No `context.Background()` at the hook sites.
- **D7:** batch agent-only runtime settings into `text.AgentSettings`, carried
  on `Configurations` as `AgentSettings *AgentSettings` with `json:"-"`. It
  holds `Logger`, `Level`, `RuneLimit`, `UsageRecorder`, and `ToolCallRecorder`.
  `RequestedToolGlobs` stays on `Configurations`: the CLI (`-use-tools`),
  profiles, and the `pkg/text` full querier also populate it, so it is not
  agent-only.
- **D8 (Phase 1, 2026-08-15):** the phase landed as a partial working tree:
  `pkg/agent/agent.go` and `internal/text/conf.go` already carried the
  production changes (the `AgentSettings` struct + field and the three slog
  options), but no test compiled (`WithOutputTo` references remained). This
  session finished the phase by updating the `pkg/agent` tests and docs.
  `text.AgentSettings` and the `Configurations.AgentSettings` field are a
  Phase 1 dependency (the spec's `asInternalConfig` snippet requires them);
  the loose `UsageRecorder`/`ToolCallRecorder` fields stay on
  `Configurations` and stay populated by `asInternalConfig` until Phase 2
  moves `NewQuerier`'s recorder source onto `AgentSettings` — the recorder
  e2e tests (Phase 1 AC) run through the current `NewQuerier`, which still
  reads the loose fields.
- **D9 (Phase 2, 2026-08-15, clai worker session 2026-08-15-02):** the loose
  `UsageRecorder`/`ToolCallRecorder` fields are removed from
  `Configurations`; `NewQuerier` now sources both recorders (and the whole
  `agentSettings` pointer) from `Configurations.AgentSettings` when it is
  non-nil, and leaves them nil otherwise. `Querier` gains the
  `agentSettings *AgentSettings` field that Phase 3's `logMessage` reads.
  The rune middle-truncation helper landed as `truncateMiddleRunes` +
  `slogTruncationMarker` in `internal/text/slog_output.go` with a
  range-based head/tail split that never allocates a full `[]rune` copy (the
  reasoning buffer can reach 1 MiB).
- **D10 (Phase 3, 2026-08-15, clai worker session 2026-08-15-03):** the
  semantic-empty gates live at the hook sites, not inside `logMessage`:
  `logMessage` stays the pure nil-settings/nil-logger gate, while
  `closeReasoningIfOpen` guards an empty reasoning buffer and the
  `finalizeAssistantText*` pair guards echoed/empty prose. The `tool_result`
  hook in `emitToolResult` fires after the `<NO-OUTPUT>` substitution, so
  `displayOut` is the final display body. The `load_skill` success path logs
  its own `tool_result` (it never calls `emitToolResult`), carrying the
  warning-folded `userVisibleContent`. `ctx` threading follows D6: the
  session runner, finalizer, and tool executor all forward the live query
  context; the legacy test-only `Querier.postProcess` passes
  `context.Background()`.
- **D11 (Phase 4, 2026-08-15, clai worker session 2026-08-15-04):**
  `TestSlogLogger_DoesNotPerturbDisplay` in `slog_output_test.go` is the
  CLI-invariance proof: one scripted stream (reasoning, assistant prose, one
  tool call, reasoning, final answer) runs twice — `agentSettings == nil`
  vs a capturing `*slog.Logger` — and asserts byte-identical `out` in both
  the raw display path and the default rolling-window path. The logger run
  must additionally emit the five kinds in stream order with the `tool`
  attribute on `tool_call`/`tool_result`; the nil run emits nothing. No
  production write to `out` was modified, reordered, or gated; the full
  pre-existing suite (including CLI e2e) passes unchanged.

## Test doctrine

No event bus. Phases are unit-test-only except Phase 4, whose contract is a
CLI-invariance test: with a scripted stream, the bytes written to `out` are
byte-identical with and without a logger attached, and the existing CLI/e2e
test suite passes unchanged.

## Session journal

- 2026-08-15 — design Q&A (D1–D4) and worklog authored. No code yet.
- 2026-08-15 — validation review: pinned D5 (limit ≤ 0 → no cap), D6 (thread
  live ctx through slog hooks), D7 (`AgentSettings` grouping); corrected the
  Phase 4 gates and the Phase 3 `load_skill`/marker contracts.
- 2026-08-15 — Phase 1 implemented (worker session 2026-08-15-01): production
  changes were already in the working tree; this session completed the phase
  with the `pkg/agent` test sweep (`WithOutputTo` removed everywhere, the
  three slog propagation tests, nil-logger and non-positive rune-limit
  coverage, typed-forwarding test, recorder assertions moved onto
  `AgentSettings`) and the documentation updates (D8). Full strict QA gate
  (`go test ./... -race -cover -count=3 -timeout=30s`) passes.
- 2026-08-15 — Phase 2 implemented (worker session 2026-08-15-02): removed
  the loose `UsageRecorder`/`ToolCallRecorder` fields from `Configurations`
  (D9), switched `NewQuerier`'s recorder source onto the `AgentSettings`
  pointer, added the `Querier.agentSettings` field, and landed
  `truncateMiddleRunes` + `slogTruncationMarker` in
  `internal/text/slog_output.go` (100% statement coverage on all three
  functions). The pre-written `slog_output_test.go` plus the new
  `TestConfigurations_AgentSettings`, `TestConfigurations_AgentSettings_jsonOmitted`,
  and `TestNewQuerier_AgentSettings` (with its nil-settings sub-case) cover
  every Phase 2 AC. Full strict QA gate passes unedited.
- 2026-08-15 — Phase 3 implemented (worker session 2026-08-15-03): added
  `Querier.logMessage` to `internal/text/slog_output.go` and wired the five
  hook sites (reasoning, assistant, tool_call, tool_result, final_answer),
  threading the live query `ctx` through `closeReasoningIfOpen`,
  `finalizeAssistantTextBeforeToolCall` (+ plain/rolling pair),
  `emitAssistantToolCall(s)`, `emitToolResult`, `sessionFinalizerer`/
  `sessionFinalizer`, and `postProcessOutput` (D6). The legacy test-only
  `Querier.postProcess` passes `context.Background()`. Every Phase 3 AC is
  covered: `logMessage` unit tests (nil gate, truncation, ctx forwarding,
  kind table, tool attribute) in `slog_output_test.go` and the four hook-site
  tests in the new `emit_hooks_test.go`. The `captureHandler` + `recordAttrs`
  helpers back both files. Full strict QA gate passes unedited; the dupl
  clone groups are all pre-existing and none touch the phase's files.
- 2026-08-15 — Phase 4 implemented (worker session 2026-08-15-04): added
  `TestSlogLogger_DoesNotPerturbDisplay` to `internal/text/slog_output_test.go`
  (D11). The test runs one scripted stream twice per display mode (raw and
  the default rolling-window path) — `agentSettings == nil` vs a capturing
  `*slog.Logger` — and asserts the `out` bytes are byte-identical, that the
  nil run emits no records, and that the logger run emits the five completed-
  message kinds in stream order with the `tool` attribute on
  `tool_call`/`tool_result`. A mutation check (logger writing to `out`)
  confirms the test detects display perturbation. No production code changed
  in this phase; the full strict QA gate (`go test ./... -race -cover
  -count=3 -timeout=30s`), gofumpt, staticcheck, `go vet`, `go fix`, `go
  build`, and dupl all pass unedited.
