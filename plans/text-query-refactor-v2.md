# Text Query Refactor v2

## Purpose

Refine the v1 design into a plan that reaches the non-recursive-session goal **without breaking existing behavior around rendering, saved chat shape, token usage, rate limits, and current `Querier` entrypoints**.

This version is based on the goal summary in `plans/text-query-refactor.md`, the proposed structure in `plans/text-query-refactor-v1.md`, and the current implementation in:

- `internal/text/querier.go`
- `internal/text/querier_tool.go`
- `internal/text/conf.go`
- `internal/cost/manager.go`
- `internal/models/models.go`
- `pkg/text/models/chat.go`

---

## Executive summary

v1 is directionally correct: the runtime should move from recursion to an explicit session loop.

However, v1 has several design issues that would cause behavior drift or unnecessary complexity if implemented as written:

1. it introduces too many layers and interfaces at once
2. it separates streaming from presentation too hard, even though current behavior depends on tight ordering
3. it models call usage persistence without defining where it lives or how failures affect the user query
4. it does not preserve an important current behavior: **saved final assistant replies are currently stored with role `system`, not `assistant`**
5. it treats tool-call rendering as if it were purely presentation, but current behavior also depends on the assistant/tool messages being appended to chat in the same step
6. it does not fully account for the current Gemini workaround semantics
7. it under-specifies finalization on error, cancellation, and stop-event paths
8. it still leaves ambiguity around when `chat.TokenUsage` should represent the last model call versus session totals

This v2 plan keeps the same core goal, but uses a more incremental and behavior-preserving design:

- introduce a **single session runner** first
- keep `Querier` as the compatibility shell
- preserve the current saved chat shape unless there is an intentional migration
- record **per-call usage** separately from the existing `chat.TokenUsage`
- define exactly when finalization happens, and when it must not happen
- keep vendor/tool quirks as policies, but avoid premature package explosion

---

## Goal restatement

One top-level text query should execute as one explicit session:

1. build or receive the initial chat
2. invoke the model
3. stream tokens/events
4. if the model requests a tool:
   - append assistant tool-call message
   - invoke the tool
   - append tool result message
   - continue the same session loop
5. after each completed model call, snapshot and persist that call's usage
6. when control returns to the user, finalize exactly once:
   - set final session-level token usage
   - optionally enrich query cost once
   - save prior query / conversation once
   - render final output once

No recursive re-entry into `TextQuery()` should remain in tool handling.

---

## What v1 gets right

These parts of v1 should be kept:

1. a session object should own the mutable runtime state
2. recursion should be replaced with an orchestration loop
3. tool execution should become a normal loop step
4. call-level usage persistence and end-of-query cost enrichment should be separate lifecycle boundaries
5. `Querier` should become a compatibility wrapper instead of the logic dump
6. vendor quirks should be isolated from orchestration

---

## Design issues found in v1

### 1. Too many interfaces too early

v1 proposes a wide set of tiny interfaces:

- `ModelStepRunner`
- `StepEventConsumer`
- `UsageSnapshotter`
- `ToolStepRunner`
- `ToolCallLimiter`
- `ToolResultFormatter`
- `CallUsageRecorder`
- `SessionFinalizer`
- `Presenter`
- `ToolCallPolicy`

That is test-friendly in theory, but for this codebase it is too much surface area to introduce in one refactor. The current implementation is not suffering from lack of abstraction alone; it is suffering from a confused lifecycle.

**Refinement:**

Start with a smaller object graph:

- `SessionRunner`
- `ToolExecutor`
- `Finalizer`
- `CallUsageRecorder`

Inside those, use unexported helpers instead of interface-first decomposition. Add interfaces only where fakes are clearly needed in tests.

### 2. Presentation is not fully separable from stream handling

v1 places presentation in a separate layer, but current runtime behavior depends on immediate token-by-token output ordering:

- streaming text is written as tokens arrive
- tool call pretty-printing happens at the exact handoff moment
- raw mode prints a newline before final save/render boundaries
- `line`, `lineCount`, and terminal clearing depend on what was streamed already

If presentation is separated too aggressively, byte-for-byte behavior can drift.

**Refinement:**

Keep presentation callbacks, but make them part of the session runner contract rather than a fully independent layer. The stream consumer should be allowed to emit directly to a `Presenter` during event consumption.

In other words:

- parsing event meaning = session logic
- writing streamed output in order = still inside the model-step execution path

### 3. v1 does not preserve current saved chat role semantics

Current `postProcess()` appends the final assistant response as:

```go
pub_models.Message{Role: "system", Content: q.fullMsg}
```

This is surprising, but it is current behavior, and tests assert it. Tool-call assistant messages are stored as `assistant`, but ordinary final replies are stored as `system`.

That means any refactor that silently changes final replies to `assistant` will alter persisted chat format and may break reply behavior or tests.

**Refinement:**

v2 must explicitly define one of two paths:

1. **compatibility path**: preserve final saved reply role as `system`
2. **migration path**: change to `assistant`, but only with explicit compatibility tests, migration decision, and likely follow-up changes in replay/chat behavior

For this refactor, choose the compatibility path.

### 4. Query-cost enrichment and per-call persistence need different failure semantics

v1 separates them conceptually, which is correct, but it does not define operational behavior.

The current behavior is:

- if cost enrichment fails, saving still happens
- if cost manager readiness is slow, we wait briefly and then skip enrichment
- the query itself still succeeds

Per-call usage persistence should not accidentally become a hard dependency which can fail the entire user query unless that is an explicit product decision.

**Refinement:**

Define failure policy explicitly:

- **call usage recording failures are non-fatal** and logged/warned
- **cost enrichment failures are non-fatal** and logged/warned
- **conversation save failures are non-fatal for output delivery but returned/logged according to current behavior expectations**

Given current behavior, the top-level query should continue to deliver output even if enrichment or per-call recording fails.

### 5. `chat.TokenUsage` must remain the final-call usage, not session aggregate

The goal text says token usage should be persisted at end of every completed model stream. That does **not** mean `chat.TokenUsage` should become a sum of all model calls in the session.

Current tests clearly expect final query-cost enrichment to use the **final assistant turn token usage**, not an accumulated total across tool turns.

Examples already in tests:

- outer tool-call stream usage may be `2/4/6`
- final assistant stream usage may be `3/5/8`
- cost enrichment expects `3/5/8`, not `5/9/14`

**Refinement:**

Use two distinct concepts:

1. `chat.TokenUsage` = usage from the final completed model call that returned control to the user
2. `session.CompletedCalls` = per-call records for each completed model stream

This is critical. If these are conflated, the refactor misses current behavior.

### 6. Tool-step ownership needs to include chat mutation, not just execution

v1 says the tool runner should append assistant tool-call message and tool result message, which is right. But it also frames presentation as a separate layer, which risks splitting one atomic behavior into two places.

Current behavior for a tool step is effectively atomic:

1. flush previous streamed assistant text
2. patch call
3. append assistant tool-call message
4. render it
5. invoke tool
6. truncate/normalize result
7. append tool result message
8. render it

**Refinement:**

Treat the whole tool handoff as one operation on session state, with rendering hooks embedded in that step.

### 7. Gemini workaround is under-modeled

Current logic does more than patch calls:

- it detects likely Gemini 3 preview behavior by looking for `thought_signature`
- if Gemini is likely and a later tool call arrives without `ExtraContent`, it returns `nil` intentionally to stop the tool path
- this is used as a workaround for a vendor bug near the end of the chain

That is not just generic tool-call policy. It is a vendor-specific continuation decision.

**Refinement:**

Model this as a `ToolContinuationPolicy` or `VendorStepPolicy` with outcomes like:

```go
type ToolDecision struct {
	SkipToolExecution bool
	TreatAsSessionReturn bool
	PatchedCall pub_models.Call
}
```

`SkipToolExecution` alone is not expressive enough; the orchestrator needs to know whether to keep looping or interpret this as end-of-session behavior.

### 8. Finalization boundaries on errors are underspecified

Current code uses `defer q.postProcess()`, which means saving/final append may happen even when the query exits early. That behavior is subtle and important.

The new design must define what happens when:

1. model stream returns an error before any tokens
2. model stream returns some tokens then errors
3. a tool invocation fails
4. context is canceled
5. a stop event is received

**Refinement:**

Define finalization rules explicitly:

- if any assistant text has already been streamed in the current top-level session, finalize output/save path once unless the session has already been finalized
- if failure happens before any final assistant text exists, still save chat when current behavior would save reply mode state
- cancellation and stop-event should not trigger duplicate finalization

The key is exactly-once finalization, not simply finalization-on-success.

### 9. Rate limit retry should be iterative, not recursively preserved under a new orchestrator

Current `handleRateLimitErr()` recursively calls `Query(ctx)`. v1 notes rate limit extraction but does not insist strongly enough on removing this recursion too.

If tool recursion is removed but rate-limit recursion remains, the lifecycle is still split.

**Refinement:**

The session runner should own a simple iterative retry loop around one model-step invocation.

### 10. Package split in v1 is probably too aggressive

v1 proposes multiple `internal/text/modelstep`, `toolstep`, and `persistence` packages immediately. That may be reasonable later, but it increases migration risk now.

**Refinement:**

Phase 1 should keep most code under `internal/text`:

- `session.go`
- `session_runner.go`
- `tool_executor.go`
- `finalizer.go`
- `call_usage_recorder.go`

Only extract subpackages once stable seams are proven.

---

## Refined architecture

The runtime should be split into four concrete responsibilities, with one thin compatibility wrapper.

### 1. `Querier` remains the public/legacy wrapper

Responsibilities:

- keep existing exported shape and interfaces working
- carry config/setup fields
- build a session from current `q.chat`
- delegate execution to a session runner
- copy finalized state back into `q.chat`, `q.fullMsg`, and terminal metadata as needed for compatibility

`Querier.Query()` should become thin.

### 2. `SessionRunner` owns the non-recursive loop

Responsibilities:

- run token warning once per top-level query
- run model steps iteratively
- handle rate-limit retries iteratively
- detect tool calls / stop events / normal reply completion
- trigger per-call usage recording
- invoke tool execution when needed
- finalize exactly once

### 3. `ToolExecutor` owns one model-requested tool handoff

Responsibilities:

- apply vendor/tool continuation policy
- flush any pending streamed text before tool handoff
- append assistant tool-call message to chat
- render assistant tool-call message
- invoke tool
- enforce tool call limit policy
- truncate/normalize tool output
- append tool message to chat
- render tool message

### 4. `Finalizer` owns end-of-session persistence and final render

Responsibilities:

- append final buffered reply to chat if needed
- set `chat.TokenUsage` to final step usage if any
- optionally enrich cost once
- save previous query / conversation once
- perform final pretty rendering once when appropriate

### 5. `CallUsageRecorder` owns optional per-call durability

Responsibilities:

- record each completed model stream's usage snapshot
- never mutate `chat.TokenUsage`
- never enrich final query cost
- fail soft by default

---

## Refined session model

Keep the session state minimal and behavior-oriented.

```go
type QuerySession struct {
	Chat                   pub_models.Chat
	StartedAt              time.Time
	FinishedAt             time.Time
	PendingAssistantText   strings.Builder
	FinalAssistantText     string
	FinalUsage             *pub_models.Usage
	CompletedCalls         []CompletedModelCall
	ToolCallsUsed          int
	ShouldSaveReply        bool
	Raw                    bool
	Finalized              bool
	SawAnyStreamText       bool
	SawStopEvent           bool
	LikelyGeminiPreview    bool
}

type CompletedModelCall struct {
	StepIndex       int
	Model           string
	StartedAt       time.Time
	FinishedAt      time.Time
	Usage           *pub_models.Usage
	EndedWithTool   bool
	EndedWithStop   bool
	EndedWithReply  bool
}
```

Important points:

- `PendingAssistantText` tracks the current stream chunk accumulation
- `FinalAssistantText` is the text to hand back to the user at session completion
- `FinalUsage` is the usage of the final model call that returned control to the user
- `CompletedCalls` tracks every completed model stream for persistence/debugging
- no `ExecutionErr` field is required if errors are returned directly

---

## Model-step contract

Use one concrete result type rather than many tiny interfaces.

```go
type ModelStepResult struct {
	AssistantText string
	ToolCall      *pub_models.Call
	Usage         *pub_models.Usage
	StopRequested bool
	EndedNormally bool
}
```

Rules:

1. one model step means one `StreamCompletions()` call
2. text tokens are streamed to the presenter as they arrive
3. text tokens are also accumulated in `AssistantText`
4. if a tool call appears, the step ends and returns that tool call
5. if a stop event appears, the step ends with `StopRequested`
6. at step completion, usage is snapshotted once from `UsageTokenCounter`, if supported

This keeps the lifecycle explicit while preserving current output timing.

---

## Token usage semantics

This is the most important behavioral rule.

### A. Per-call usage persistence

After every completed model step, persist one `CompletedModelCall` record.

This record is for history/durability only.

### B. Session-level `chat.TokenUsage`

At finalization, set:

- `chat.TokenUsage = session.FinalUsage`

where `session.FinalUsage` is the usage from the final completed model step that returned control to the user.

### C. No aggregation into `chat.TokenUsage`

Do **not** sum usages from multiple model calls into `chat.TokenUsage` during this refactor.

That would break current cost-enrichment behavior and existing tests.

---

## Finalization rules

Finalization must happen at most once per top-level session.

### Finalizer responsibilities in order

1. if `FinalAssistantText` is non-empty, append it to chat using the current compatibility role:
   - `Role: "system"`
2. if `FinalUsage` exists, assign it to `chat.TokenUsage`
3. if `ShouldSaveReply` is true:
   - optionally attempt cost enrichment once
   - save previous query once
4. if `FinalAssistantText` is non-empty:
   - raw mode: ensure newline behavior remains compatible
   - non-raw mode: pretty-render final answer once

### Error behavior

- cost enrichment failure: warn/log, proceed to save
- call usage recording failure: warn/log, continue session
- save failure: preserve normal error propagation policy, but do not duplicate rendering/finalization
- if session ends with no final assistant text, save may still happen if `ShouldSaveReply` is true

---

## Tool continuation policy

Represent vendor-specific tool quirks as a decision point before tool execution.

```go
type ToolDecision struct {
	PatchedCall          pub_models.Call
	SkipExecution        bool
	TreatAsReturnToUser  bool
}
```

Examples:

### Normal vendor

- patch call
- execute tool

### Gemini preview workaround

- first detect likely preview from `thought_signature`
- if likely preview and a later tool call arrives without `ExtraContent`:
  - skip execution
  - treat as return-to-user / end-of-chain workaround

This expresses current intent more clearly than a bare `Skip bool`.

---

## Rate limit handling

Move rate limit retry into the session runner and make it iterative.

### Rules

1. max retry count remains bounded
2. if model supports `InputTokenCounter`, use current adaptive wait logic
3. preserve current wait fallback behavior
4. retries must not recurse back into `Query()`
5. retries must not reset already-completed session history incorrectly

Important subtlety:

- only transient per-step buffers should reset before retrying the same step
- completed call records and prior tool steps must remain intact

---

## Compatibility requirements

The refactor should preserve all of these unless an explicit migration is separately approved:

1. `main` package behavior remains unchanged
2. `Querier` still satisfies `models.Querier` and `models.ChatQuerier`
3. streamed tokens appear in the same order and timing characteristics as today
4. raw mode still emits compatible newline behavior
5. tool call assistant message is appended as `assistant`
6. tool output message is appended as `tool`
7. final ordinary assistant reply is saved using current compatibility role `system`
8. `chat.TokenUsage` reflects the final completed assistant-returning model call
9. query cost enrichment runs once per top-level query only
10. tool handling does not recurse into `TextQuery()`

---

## Recommended code shape

Keep the first implementation conservative.

### Phase-1 files under `internal/text`

```text
internal/text/
  querier.go                 # compatibility wrapper, much thinner
  querier_tool.go            # reduced or replaced by ToolExecutor
  session.go                 # QuerySession + CompletedModelCall
  session_runner.go          # main non-recursive loop
  tool_executor.go           # tool handoff, rendering, mutation
  finalizer.go               # exact-once finalization
  call_usage_recorder.go     # optional recorder interface + noop impl
  rate_limit.go              # iterative retry helper extracted from querier
```

Only split into subpackages later if the new seams prove stable.

---

## Migration plan

### Stage 1: codify current behavior with tests

Before moving logic, ensure tests explicitly cover:

1. final saved non-tool reply role remains `system`
2. one tool turn then final answer preserves message ordering
3. cost enrichment uses final step usage, not aggregate session usage
4. cost enrichment happens once per top-level query
5. save still happens when enrich fails
6. Gemini workaround behavior is preserved
7. rate-limit retries do not duplicate finalization

### Stage 2: introduce `QuerySession`

- move transient execution state out of `Querier`
- keep current `Query()` flow but store state in session

### Stage 3: extract one-step model execution

- make one `StreamCompletions` pass return `ModelStepResult`
- snapshot usage at step end
- keep current top-level `Query()` controlling the loop temporarily

### Stage 4: extract `ToolExecutor`

- move tool append/render/invoke logic into one unit
- preserve current chat mutation order and render order

### Stage 5: replace recursion with iterative session loop

- remove `handleToolCall()` -> `TextQuery()` recursion
- remove rate-limit recursion too

### Stage 6: introduce finalizer

- remove `postProcess()` as the hidden lifecycle controller
- replace with explicit exactly-once finalizer invocation

### Stage 7: add optional per-call usage recorder

- default to noop if no durable format is ready
- make failures non-fatal

---

## Test plan

Focus on behavior, not abstraction count.

### Unit tests

1. tool output truncation
2. tool output empty-string normalization
3. Gemini tool decision policy
4. finalizer appends final reply with compatibility role `system`
5. finalizer sets `chat.TokenUsage` from final step usage only

### Session-runner tests with fakes

1. single model step, plain reply
2. one tool call then final reply
3. multiple tool calls in one session
4. stop event ends session once
5. stream error after partial text finalizes once
6. rate-limit retry remains iterative
7. call usage recorder invoked once per completed model step
8. call usage recorder failure does not fail the query
9. cost enrichment invoked once only

### Compatibility tests on `Querier`

1. `Query()` still saves reply correctly
2. saved message ordering matches current behavior
3. final saved response role remains `system`
4. `TextQuery()` still returns updated chat
5. raw and pretty output modes still work

---

## Success criteria

This refactor is successful when all of the following are true:

1. one top-level text query runs as one explicit session loop
2. no tool path recursively re-enters `TextQuery()` or `Query()`
3. no rate-limit path recursively re-enters `Query()`
4. each completed model stream can emit a separate persisted usage record
5. final query cost is enriched once per top-level query only
6. `chat.TokenUsage` reflects the final completed user-facing model step, not an aggregate
7. finalization happens exactly once
8. current persisted chat shape and rendering behavior are preserved unless intentionally migrated

---

## Final recommendation

Adopt the session-based non-recursive design from v1, but implement it with stricter compatibility rules and less abstraction churn.

The best near-term design is:

- a thin `Querier`
- one explicit `SessionRunner`
- one `ToolExecutor`
- one `Finalizer`
- one optional `CallUsageRecorder`

Most importantly:

- preserve existing chat message shape
- keep `chat.TokenUsage` as the **final-step** usage
- treat per-call usage persistence as separate data
- make all recursion disappear, including rate-limit retry

That achieves the original goal while avoiding the biggest risks in v1.