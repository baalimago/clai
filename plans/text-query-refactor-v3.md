# Text Query Refactor v3

## Purpose

Define a finalized, implementation-oriented plan for refactoring the text query runtime into a **non-recursive, session-based execution flow** that preserves current behavior where required, while making lifecycle boundaries explicit and testable.

This version is an unbiased merge of the strongest parts of:

- `plans/text-query-refactor.md`
- `plans/text-query-refactor-v1.md`
- `plans/text-query-refactor-v2.md`

It is also grounded in the current implementation and architecture, especially:

- `internal/text/querier.go`
- `internal/text/querier_tool.go`
- `internal/models/models.go`
- `pkg/text/models/chat.go`
- `architecture/query.md`
- `architecture/streaming.md`

---

## Executive summary

The right target is:

1. **one top-level user query = one explicit session**
2. **no recursion** for tool continuation
3. **no recursion** for rate-limit retries
4. **per-model-call usage persisted at end of each completed stream**
5. **query cost enriched once, at end-of-session only**
6. **exactly one finalization path**
7. **current externally visible behavior preserved unless intentionally migrated**

v1 is strongest on architecture and lifecycle separation.
v2 is strongest on compatibility constraints, migration safety, and current-behavior accuracy.

The finalized design should therefore:

- keep the **session/orchestrator model** from v1
- keep the **compatibility-first implementation strategy** from v2
- avoid introducing too many public abstractions at once
- explicitly preserve the current semantics that tests and saved data rely on

---

## Current implementation findings

The current runtime has two separate recursion problems and one hidden finalization problem.

### 1. Tool continuation recursion

Today, `handleToolCall()` in `internal/text/querier_tool.go`:

- appends assistant tool-call message
- invokes the tool
- appends tool result
- then recursively calls `TextQuery()`

That means one user query becomes multiple nested `Query()` / `TextQuery()` executions.

### 2. Rate-limit retry recursion

Today, `handleRateLimitErr()` in `internal/text/querier.go` retries by calling `q.Query(ctx)` recursively.

So even if tool recursion is removed, lifecycle fragmentation would remain unless rate-limit recursion is removed too.

### 3. Hidden finalization through `defer q.postProcess()`

Today, `Query()` always defers `postProcess()`, which currently does several distinct lifecycle jobs:

- raw-mode newline handling
- final assistant message append
- optional cost enrichment
- save previous query
- final pretty rendering

This is the central reason the flow is hard to reason about: multiple lifecycle boundaries are folded into one deferred method.

---

## Confirmed compatibility facts from current code

The refactor must account for these real current behaviors.

### 1. Final ordinary replies are saved as `system`

`postProcess()` currently appends:

```go
pub_models.Message{
	Role:    "system",
	Content: q.fullMsg,
}
```

This is surprising, but it is current behavior and existing tests rely on it.

### 2. Tool-call assistant messages are saved as `assistant`

Tool handoff currently appends:

```go
pub_models.Message{
	Role:      "assistant",
	Content:   call.PrettyPrint(),
	ToolCalls: []pub_models.Call{call},
}
```

### 3. Tool results are saved as `tool`

Tool output is appended as:

```go
pub_models.Message{
	Role:       "tool",
	Content:    out,
	ToolCallID: call.ID,
}
```

### 4. `chat.TokenUsage` currently behaves like final-call usage

During nested tool recursion, `Query()` only assigns `q.chat.TokenUsage` when:

- `q.callStackLevel == 0`, or
- `q.chat.TokenUsage == nil`

In effect, final saved query-cost behavior depends on the final assistant-returning call's usage, not an accumulated total across all model calls.

### 5. Cost enrichment is best-effort and non-fatal

Current behavior:

- waits briefly for cost manager readiness
- skips enrichment if readiness takes too long
- logs on enrich failure
- still saves reply data

### 6. Save is attempted even on failure paths

Because `postProcess()` is deferred, current reply-saving behavior often still occurs even when streaming exits early.

### 7. Gemini-preview tool behavior is not a generic patch only

Current code uses `thought_signature` heuristics and treats some later tool calls without `ExtraContent` as a signal to stop the tool path rather than execute another tool.

That is a continuation policy, not just call patching.

---

## Primary objective

Refactor the text query runtime into a session-based, non-recursive execution engine where one user query is processed by one explicit orchestration loop that:

- streams model output
- handles tool calls as normal loop steps
- persists usage after each completed model stream
- finalizes exactly once when control returns to the user
- preserves current CLI behavior and saved chat semantics unless intentionally migrated later

---

## Non-goals for this refactor

To keep the refactor safe, the following are **not** goals of v3:

1. changing the saved final reply role from `system` to `assistant`
2. changing the normalized streaming event model
3. redesigning vendor implementations
4. introducing durable aggregated session-usage accounting into `chat.TokenUsage`
5. splitting `internal/text` into many new subpackages immediately
6. changing the CLI surface or `main` package behavior

These may be valid follow-up changes, but not part of this refactor.

---

## Final design direction

The finalized design uses **one concrete session runner** with a small set of focused collaborators.

This keeps v1's explicit lifecycle while adopting v2's warning against over-abstracting too early.

### Core runtime pieces

1. `Querier` — thin compatibility wrapper
2. `QuerySession` — mutable state for one top-level user query
3. `SessionRunner` — explicit non-recursive loop
4. `ToolExecutor` — one tool handoff step
5. `Finalizer` — exact-once end-of-session behavior
6. `CallUsageRecorder` — optional per-call usage durability, soft-fail

Where needed for tests, small interfaces may be introduced, but the implementation should prefer:

- concrete types
- unexported helpers
- narrow seams only where mocking is useful

---

## Finalized architecture

### 1. `Querier` becomes a compatibility shell

`Querier` should remain the entrypoint that existing setup code constructs, and it must continue to satisfy:

- `models.Querier`
- `models.ChatQuerier`

Its responsibilities after refactor:

1. hold config/setup/runtime fields already established by construction
2. build a `QuerySession` from current `q.chat`
3. invoke `SessionRunner.Run(ctx, session)`
4. copy compatible finalized state back onto `q` where existing tests or callers depend on it

`Querier.Query()` should stop being the place where the full lifecycle lives.

### 2. `QuerySession` becomes the owner of mutable query state

This is the execution unit for one top-level user request.

Suggested shape:

```go
type QuerySession struct {
	Chat                pub_models.Chat
	StartedAt           time.Time
	FinishedAt          time.Time
	PendingText         strings.Builder
	FinalAssistantText  string
	FinalUsage          *pub_models.Usage
	CompletedCalls      []CompletedModelCall
	ToolCallsUsed       int
	ShouldSaveReply     bool
	Raw                 bool
	Finalized           bool
	SawAnyText          bool
	SawStopEvent        bool
	LikelyGeminiPreview bool
	StepIndex           int
	Line                string
	LineCount           int
}

type CompletedModelCall struct {
	StepIndex      int
	Model          string
	StartedAt      time.Time
	FinishedAt     time.Time
	Usage          *pub_models.Usage
	EndedWithTool  bool
	EndedWithReply bool
	EndedWithStop  bool
}
```

Notes:

- `PendingText` replaces the implicit `q.fullMsg` role during active step execution
- `FinalAssistantText` is the exact text returned to the user at session end
- `FinalUsage` is the final completed user-facing model call usage only
- `CompletedCalls` stores step-level usage and metadata for persistence/debugging
- terminal metadata can stay attached to session or remain on `Querier`; either is acceptable as long as output compatibility is preserved

### 3. `SessionRunner` owns the explicit loop

This is the main control flow.

Responsibilities:

1. run token warning once per top-level query
2. execute one model step at a time
3. handle rate-limit retries iteratively
4. persist per-call usage after each completed model step
5. dispatch tool execution when a tool call is encountered
6. continue the same session loop with updated chat
7. finalize exactly once when control returns to the user

### 4. `ToolExecutor` owns one tool handoff

Responsibilities:

1. flush pending streamed text at the tool boundary in a compatibility-safe way
2. patch the tool call
3. apply vendor continuation policy
4. append assistant tool-call message
5. render assistant tool-call message in correct order
6. invoke tool
7. apply tool-limit policy
8. truncate/normalize output
9. append tool result message
10. render tool result in correct order

It must **not** call back into `Query()` or `TextQuery()`.

### 5. `Finalizer` owns exact-once end-of-session behavior

Responsibilities:

1. append final assistant reply if present
2. assign final `chat.TokenUsage` if present
3. attempt one query-cost enrichment if configured and ready
4. save previous query once when configured
5. perform final render once
6. preserve raw-mode newline behavior

### 6. `CallUsageRecorder` owns per-call usage durability

Responsibilities:

1. receive one record after each completed model stream
2. never mutate `chat.TokenUsage`
3. never enrich cost
4. fail soft by default

If no durable storage is ready yet, a noop implementation is acceptable as long as the architectural hook exists.

---

## Model-step contract

The session loop should use one concrete per-step result.

```go
type ModelStepResult struct {
	AssistantText string
	ToolCall      *pub_models.Call
	Usage         *pub_models.Usage
	StopRequested bool
	EndedNormally bool
}
```

### Rules for one model step

One model step means one call to:

```go
Model.StreamCompletions(ctx, chat)
```

During that step:

1. `string` events are written immediately to output and appended to `PendingText`
2. `pub_models.Call` ends the current model step and returns control to the session runner
3. `models.StopEvent` ends the step with stop semantics
4. `models.NoopEvent` is ignored
5. `error` fails the step
6. when the stream ends, usage is snapshotted once from `models.UsageTokenCounter`, if implemented

This keeps output timing compatible with today while making the orchestration explicit.

---

## Token usage semantics

This is the most important behavioral contract.

### A. Per-call usage

At the end of **every completed model stream**, the runner should create a `CompletedModelCall` and pass it to `CallUsageRecorder`.

This data is call-level history.

### B. Final session-visible usage

At finalization:

```go
session.Chat.TokenUsage = session.FinalUsage
```

where `session.FinalUsage` is the usage from the final completed model call that returned control to the user.

### C. No aggregation into `chat.TokenUsage`

Do **not** sum all model-call usages into `chat.TokenUsage` during this refactor.

Reason:

- current cost-related behavior expects final-call usage semantics
- current tests already assert this behavior
- per-call durability and final user-facing usage are separate concerns

### D. Recorder failure policy

If `CallUsageRecorder.Record(...)` fails:

- log or warn
- continue the session
- do not fail the user query

---

## Finalization rules

There must be exactly one finalization path per top-level session.

### Finalizer order

1. if raw mode requires a compatibility newline, emit it once
2. if `FinalAssistantText` is non-empty, append final message to chat with role `system`
3. if `FinalUsage` exists, assign to `chat.TokenUsage`
4. if `ShouldSaveReply` is true:
   - wait briefly for cost manager readiness
   - enrich cost once if ready
   - skip cost enrichment if readiness times out
   - save previous query once
5. if `FinalAssistantText` is non-empty and non-raw mode is active, pretty-render once

### Error-path rules

The new design must preserve the spirit of today's deferred `postProcess()` without preserving its hidden control flow.

Rules:

1. finalization must happen at most once
2. if partial assistant text has already streamed and a later step fails, finalize once with what is already user-visible
3. if no final assistant text exists, save may still happen when `ShouldSaveReply` is true
4. stop events and context cancellation must not cause duplicate finalization
5. enrichment failures must not prevent saving

### Save failure policy

Current code prints save errors rather than making output delivery impossible.

The refactor should keep output delivery as primary. That means save failure should not cause duplicate output or duplicate finalization. Whether the error is returned or only logged should match current tested behavior.

---

## Tool execution semantics

Tool handling should become a normal session step, never a recursive re-entry.

### Tool handoff sequence

For a normal tool request:

1. complete the current model step
2. snapshot and record that step's usage
3. patch the tool call
4. append assistant tool-call message to chat
5. render assistant tool-call message
6. invoke tool
7. enforce max-tool-call policy
8. truncate output if needed
9. normalize empty output to `<EMPTY-RESPONSE>`
10. append tool-result message to chat
11. render tool-result message
12. continue next model step in the same top-level loop

### Tool output rules to preserve

The current behavior that should remain:

- empty output becomes `<EMPTY-RESPONSE>`
- long output is truncated by rune count
- non-raw tool output may be shortened for presentation
- raw mode prints the tool result directly in compatible order

### Tool limit policy

The current max-tool-calls behavior is soft-first, then hard-stop after repeated persistence.

That policy may remain as-is during this refactor, but it should be owned by `ToolExecutor` or a helper it uses, not by recursive query flow.

---

## Vendor-specific continuation policy

Vendor quirks should be isolated from orchestration.

The current Gemini-preview workaround means the design needs a richer decision than just “patch or skip”.

Suggested result:

```go
type ToolDecision struct {
	PatchedCall         pub_models.Call
	SkipExecution       bool
	TreatAsReturnToUser bool
}
```

### Example: Gemini preview workaround

Current behavior to preserve:

1. detect likely Gemini preview from `thought_signature`
2. if likely preview and a later tool call arrives without `ExtraContent`, do not execute another tool
3. interpret that as end-of-chain / return-to-user behavior

That decision belongs in vendor/tool policy logic, not in the general session loop.

---

## Rate-limit handling

Rate-limit retry must also become iterative.

### Rules

1. bounded retry count remains
2. current adaptive wait logic based on `InputTokenCounter` remains
3. current fallback wait behavior remains
4. retries do not recurse into `Query()`
5. retries reset only transient data for the current model step, not the whole session

### Important boundary

If a rate limit occurs before a model step has completed, that step should be retried without inventing a completed-call record.

Previously completed tool and model steps must remain intact.

---

## Presentation and stream handling

v1 is right that presentation should not own business logic.
v2 is right that presentation cannot be separated so aggressively that stream ordering changes.

So the merged rule is:

- **presentation remains a collaborator, not a fully detached layer**
- **stream consumption may emit directly to presenter/output in real time**
- **session runner still owns meaning and lifecycle**

This preserves ordering for:

- streamed text tokens
- tool handoff rendering
- raw newlines
- final pretty rendering

without keeping orchestration fused into `Querier.Query()`.

---

## Recommended code shape

The first implementation should stay conservative and mostly inside `internal/text`.

### Phase-1 files

```text
internal/text/
  querier.go               # thin compatibility wrapper
  querier_tool.go          # reduced or replaced by tool executor logic
  session.go               # QuerySession + CompletedModelCall
  session_runner.go        # explicit top-level non-recursive loop
  tool_executor.go         # tool mutation + rendering + invocation
  finalizer.go             # exact-once finalization
  call_usage_recorder.go   # recorder seam + noop implementation
  rate_limit.go            # iterative retry helper
```

Do not split into many new subpackages until the new seams have proven stable.

---

## Migration plan

### Stage 1: lock down current behavior with tests

Before moving logic, codify all critical current behavior.

Must-have tests:

1. final saved non-tool reply uses role `system`
2. tool-call assistant message uses role `assistant`
3. tool-output message uses role `tool`
4. one tool turn then final answer preserves message ordering
5. cost enrichment uses final step usage, not aggregate usage
6. cost enrichment happens once per top-level query
7. save still happens when cost enrichment fails
8. Gemini workaround behavior is preserved
9. rate-limit retries do not duplicate finalization
10. partial-stream failure finalizes at most once

### Stage 2: introduce `QuerySession`

- move mutable execution state out of `Querier`
- keep existing top-level behavior temporarily

### Stage 3: extract one-step model execution

- make one streaming pass yield `ModelStepResult`
- snapshot usage exactly once per completed stream

### Stage 4: extract `ToolExecutor`

- move tool append/render/invoke logic into one unit
- preserve chat mutation and print order

### Stage 5: replace tool recursion with session loop

- remove `handleToolCall()` → `TextQuery()` recursion
- continue within same session instead

### Stage 6: replace rate-limit recursion with iterative retry

- remove recursive `handleRateLimitErr()` behavior
- retry the current step iteratively

### Stage 7: introduce explicit finalizer

- remove `postProcess()` as hidden lifecycle controller
- replace with exact-once finalization call from session runner

### Stage 8: add call usage recorder

- noop is acceptable initially
- failures must be non-fatal

---

## Test strategy

The refactor should be driven by tests that verify behavior, not abstraction count.

### Unit tests

1. tool output truncation
2. tool output empty normalization
3. Gemini tool decision policy
4. finalizer appends final reply with role `system`
5. finalizer assigns `chat.TokenUsage` from final usage only
6. finalizer enriches at most once

### Session-runner tests

1. single model step, plain reply
2. one tool call then final reply
3. multiple tool calls in one session
4. stop event ends session once
5. stream error after partial text finalizes once
6. rate-limit retry is iterative
7. completed-call recorder invoked once per completed model step
8. recorder failure does not fail query
9. finalizer invoked once only

### Compatibility tests on `Querier`

1. `Query()` still saves reply correctly
2. `TextQuery()` still returns updated chat
3. saved message ordering matches current behavior
4. final saved ordinary response remains role `system`
5. raw output behavior remains compatible
6. pretty output behavior remains compatible

---

## Success criteria

The refactor is successful when all of the following are true:

1. one top-level text query executes as one explicit session loop
2. tool handling no longer recursively re-enters `TextQuery()` or `Query()`
3. rate-limit retry no longer recursively re-enters `Query()`
4. each completed model stream can emit its own usage record
5. query-cost enrichment happens once per completed top-level query only
6. `chat.TokenUsage` reflects final-call usage, not aggregate session usage
7. finalization happens exactly once
8. current saved chat shape and rendering behavior remain compatible
9. vendor-specific quirks are isolated from orchestration logic

---

## Final recommendation

Adopt the session-based, explicit-loop design from v1, but implement it with the compatibility discipline and narrower initial object graph advocated by v2.

The best finalized plan is:

- keep `Querier` thin and compatible
- introduce `QuerySession` as the state owner
- implement one explicit `SessionRunner`
- make `ToolExecutor` a normal loop step
- make `Finalizer` the only end-of-session path
- record per-call usage separately from `chat.TokenUsage`
- preserve current saved message roles and rendering order
- remove **all** recursion, including rate-limit recursion

In short:

**v1 provides the right architecture; v2 provides the right constraints. v3 should implement both.**

