# Text Query Refactor v1

## Purpose

Design a non-recursive text query runtime where one user query maps to one explicit execution session.

This design is based on the current implementation in:

- `internal/text/querier.go`
- `internal/text/querier_tool.go`
- `internal/text/querier_setup.go`
- `internal/text/querier_setup_tools.go`
- `internal/cost/manager.go`
- `internal/tools/*`
- `pkg/text/models/*`

The main goal is to preserve current behavior while making the runtime modular, testable, and deterministic.

---

## Current state summary

The current `Querier` mixes too many responsibilities:

1. model invocation
2. raw stream consumption
3. terminal rendering
4. tool call handling
5. vendor-specific tool-call patching
6. recursive continuation after tools
7. token usage capture from model object state
8. cost enrichment
9. persistence of final chat/global scope
10. rate limit retry behavior

### Important current coupling points

#### 1. Recursion drives continuation

`handleToolCall()` calls `TextQuery()` again with an updated chat.

That means a single user query becomes multiple nested query executions.

Consequences:

- finalization is guarded indirectly through flags like `hasPrinted`
- state ownership is split across stack frames
- token usage semantics are ambiguous
- cost enrichment happens in `postProcess()` rather than at an explicit session end
- cancellation behavior becomes harder to reason about

#### 2. Streaming and orchestration are fused

`Query()` both:

- invokes the model
- reads the event stream
- interprets event meaning
- handles tools
- performs final save/print side effects

This makes it difficult to test any one part without invoking the whole runtime bundle.

#### 3. Persistence boundaries are unclear

Today:

- token usage is copied from `UsageTokenCounter` in a `defer`
- cost enrichment is executed in `postProcess()`
- `postProcess()` also appends assistant message, prints output, and saves chat

These are different lifecycle moments, but they are folded into one method.

#### 4. Tool execution is not represented as a first-class step

Tool handling currently mutates chat and jumps back to model execution recursively.

This prevents a clean “step machine” mental model.

---

## Design goals

The new architecture must satisfy all of the plan requirements and additionally make each abstraction independently testable.

### Functional goals

1. preserve current CLI behavior
2. preserve current vendor integrations
3. preserve current tool registry and MCP loading model
4. persist token usage after every completed model stream
5. persist query cost once per top-level user session
6. remove recursive tool-driven re-entry

### Structural goals

1. one clear owner of session state
2. one explicit orchestration loop
3. isolated stream-to-event translation
4. isolated tool execution layer
5. isolated persistence/finalization layer
6. isolated presentation layer
7. narrow interfaces that are easy to fake in tests

---

## Proposed architecture

The runtime should be reorganized into five layers.

1. **Session State**
2. **Model Step Runner**
3. **Tool Step Runner**
4. **Session Finalizer / Persistence**
5. **Presentation**

On top of those sits a single **Orchestrator**.

---

## Layer 1: Session State

### Responsibility

Hold all mutable state for one top-level user query.

### Proposed type

```go
type Session struct {
	Chat              pub_models.Chat
	CurrentStep       int
	CurrentAssistant  AssistantTurnBuffer
	CompletedCalls    []CallRecord
	PendingToolCall   *pub_models.Call
	ToolCallsUsed     int
	MaxToolCalls      *int
	ShouldSaveReply   bool
	Raw               bool
	Finalized         bool
	StartedAt         time.Time
	FinishedAt        time.Time
	RuntimeModelName  string
	GeminiWorkaround  GeminiSessionState
	ExecutionErr      error
}

type AssistantTurnBuffer struct {
	Text             strings.Builder
	SawToolCall      bool
	StreamFinished   bool
	Usage            *pub_models.Usage
	StartedAt        time.Time
	FinishedAt       time.Time
}

type CallRecord struct {
	StepIndex        int
	Model            string
	StartedAt        time.Time
	FinishedAt       time.Time
	Usage            *pub_models.Usage
	EndedWithTool    bool
	EndedWithReply   bool
}
```

### Why this helps

This makes state explicit instead of implicitly spread across:

- `Querier.fullMsg`
- `Querier.chat`
- `Querier.callStackLevel`
- `Querier.hasPrinted`
- `Querier.execErr`
- model object token counters

### Session rules

The session owns:

- chat mutation
- current assistant buffered text
- per-model-call usage snapshots
- tool call count / limits
- final output to render
- final error state

The session does **not** directly:

- invoke the model
- invoke tools
- save files
- print to terminal

### Testability

Pure unit tests can validate:

- appending assistant text
- appending tool call/result messages
- call usage recording
- state transitions after model step completion
- tool limit behavior

---

## Layer 2: Model Step Runner

### Responsibility

Execute exactly one model stream against the current chat and normalize the output into session-relevant events.

This layer should not know about recursion, persistence, or reply/global scope saving.

### Proposed interfaces

```go
type StreamModel interface {
	StreamCompletions(context.Context, pub_models.Chat) (chan models.CompletionEvent, error)
}

type UsageReader interface {
	TokenUsage() *pub_models.Usage
}

type InputTokenCounter interface {
	CountInputTokens(context.Context, pub_models.Chat) (int, error)
}

type ModelStepRunner interface {
	Run(context.Context, *Session) (ModelStepResult, error)
}

type ModelStepResult struct {
	Outcome         ModelStepOutcome
	AssistantText   string
	ToolCall        *pub_models.Call
	Usage           *pub_models.Usage
	StopRequested   bool
}

type ModelStepOutcome string

const (
	ModelStepReplyReady  ModelStepOutcome = "reply_ready"
	ModelStepToolCall    ModelStepOutcome = "tool_call"
	ModelStepStopped     ModelStepOutcome = "stopped"
)
```

### Internal structure

Internally, the step runner should have two smaller pieces:

#### A. Stream consumer

Consumes `CompletionEvent`s and emits normalized step events:

```go
type StepEventConsumer interface {
	Consume(context.Context, <-chan models.CompletionEvent) (ConsumedStream, error)
}

type ConsumedStream struct {
	AssistantText string
	ToolCall      *pub_models.Call
	StopRequested bool
	EndedNormally bool
}
```

This is the replacement for `handleCompletion()` being mixed into `Query()`.

#### B. Usage snapshotter

At the end of a completed stream, capture usage exactly once from the model object if available.

```go
type UsageSnapshotter interface {
	Snapshot() *pub_models.Usage
}
```

### Important behavioral rule

One model step ends when one of these happens:

1. stream closes after assistant text
2. stream emits a tool call
3. stream emits stop event
4. stream errors

If a tool call is encountered, this step returns control to the orchestrator. It does **not** call the model again.

### Testability

This layer can be tested with fake completion channels:

- plain assistant text
- assistant text then close
- tool call encountered mid-stream
- error event
- stop event
- usage available only at end

---

## Layer 3: Tool Step Runner

### Responsibility

Take one model-requested tool call, validate/patch/execute it, and append the resulting assistant/tool messages into session chat.

This layer should not invoke the model.

### Proposed interfaces

```go
type ToolInvoker interface {
	Invoke(context.Context, pub_models.Call) (string, error)
}

type ToolCallLimiter interface {
	CheckAndConsume(session *Session) ToolLimitDecision
}

type ToolResultFormatter interface {
	Normalize(output string, limit int) string
}

type ToolStepRunner interface {
	Run(context.Context, *Session, pub_models.Call) error
}

type ToolLimitDecision struct {
	Allowed bool
	Message string
	Abort   bool
}
```

### Internal responsibilities

1. patch vendor-specific shape with `call.Patch()`
2. append assistant tool-call message
3. execute tool through injected invoker
4. apply max-tool-call policy
5. apply output truncation policy
6. convert empty output to `<EMPTY-RESPONSE>`
7. append tool result message

### Explicitly excluded

- no recursive re-entry
- no cost enrichment
- no final persistence
- no terminal pretty printing decisions, except via injected presentation callbacks if wanted

### Testability

Independent tests can cover:

- tool call patching
- allowed tool path
- missing tool path
- max-tool-calls soft warning and hard stop behavior
- truncation
- empty tool output normalization
- proper chat mutations

---

## Layer 4: Usage Persistence and Finalization

This layer should be separated into two components because they happen at different lifecycle moments.

### 4A. Call usage recorder

### Responsibility

Persist usage after every completed model step.

This is the key missing boundary in the current runtime.

### Proposed interface

```go
type CallUsageRecorder interface {
	RecordCompletedCall(context.Context, pub_models.Chat, CallRecord) error
}
```

### Notes

- This may initially be implemented as a no-op if there is no durable storage format yet.
- The important thing is architectural placement: the call usage recorder runs after each completed model stream, before the next tool/model step starts.
- This recorder should never enrich final query cost. It is only for per-call usage durability.

### Testability

- verifies a record is emitted after each model step
- verifies tool steps do not trigger call usage recording
- verifies usage from the final stream chunk is captured

---

### 4B. Session finalizer

### Responsibility

Run exactly once when the top-level query returns control to the user.

### Proposed interface

```go
type SessionFinalizer interface {
	Finalize(context.Context, *Session) (FinalizationResult, error)
}

type FinalizationResult struct {
	SavedChat pub_models.Chat
	RenderedMessage pub_models.Message
}
```

### Finalizer sub-responsibilities

1. append final buffered assistant text to chat if needed
2. optionally enrich final chat with query cost exactly once
3. save reply/global scope/conversation
4. produce final message for rendering

### Supporting interfaces

```go
type CostEnricher interface {
	Enrich(pub_models.Chat) (pub_models.Chat, error)
}

type ConversationSaver interface {
	SavePreviousQuery(string, pub_models.Chat) error
}
```

### Important rule

Final cost enrichment runs once per session, not once per model call.

### Why separate from call recorder

Because these are semantically different:

- per-call usage = model-step completion boundary
- query cost = top-level session completion boundary

If they stay fused, the old ambiguity returns.

---

## Layer 5: Presentation

### Responsibility

Render incremental output and final pretty output, without owning business logic.

### Proposed interface

```go
type Presenter interface {
	OnAssistantToken(token string) error
	OnAssistantToolCall(pub_models.Message) error
	OnToolResult(pub_models.Message) error
	OnFinalAssistantMessage(pub_models.Message) error
	OnRawNewline() error
}
```

### Notes

This abstracts away the current direct use of:

- `fmt.Fprint`
- `utils.AttemptPrettyPrint`
- `utils.ClearTermTo`
- `utils.UpdateMessageTerminalMetadata`

The existing terminal behavior can be preserved by implementing `TerminalPresenter`.

### Testability

With a fake presenter, tests can validate orchestration order without asserting on stdout.

---

## Top-level orchestrator

### Responsibility

Drive the explicit loop for one top-level query session.

### Proposed interface

```go
type Orchestrator interface {
	Run(context.Context, *Session) error
}
```

### Main algorithm

```text
create session
warn on token length
loop:
  run one model step
  update session assistant buffer and completed call record
  persist per-call usage

  if stop requested:
    break

  if tool call returned:
    run one tool step
    continue

  if reply ready:
    break

finalize once
render final output once
return
```

### More concrete flow

1. `TokenLengthWarner.Warn(session.Chat)`
2. `ModelStepRunner.Run(ctx, session)`
3. snapshot usage and append `CallRecord`
4. `CallUsageRecorder.RecordCompletedCall(...)`
5. if tool call exists:
   - `ToolStepRunner.Run(ctx, session, call)`
   - reset session current assistant buffer
   - next loop iteration uses updated chat
6. else finalize session
7. `SessionFinalizer.Finalize(ctx, session)`

### Required orchestrator dependencies

```go
type QueryOrchestrator struct {
	TokenWarner      TokenWarner
	ModelRunner      ModelStepRunner
	ToolRunner       ToolStepRunner
	CallUsageRecorder CallUsageRecorder
	Finalizer        SessionFinalizer
}
```

### Testability

This can be tested fully with fakes:

- no tools, single model step
- one tool call then answer
- many tool calls
- tool limit exhaustion
- model step error mid-session
- finalizer error
- per-call usage recorder called N times
- cost enrichment called exactly once

---

## Proposed module boundaries in the codebase

Suggested package structure under `internal/text`:

```text
internal/text/
  session.go                 # session state types
  orchestrator.go            # top-level explicit loop
  token_warning.go           # token warning policy
  finalize.go                # finalization and save behavior
  presenter.go               # terminal/raw presenter
  modelstep/
    runner.go                # one model invocation
    consumer.go              # completion stream normalization
    usage.go                 # usage snapshot logic
    rate_limit.go            # retry policy extracted from querier
  toolstep/
    runner.go                # tool call execution and chat mutation
    limiter.go               # max-tool-calls policy
    formatter.go             # output truncation / empty output normalization
    gemini.go                # vendor-specific tool quirk handling
  persistence/
    call_usage.go            # per-call usage recorder
    conversation.go          # save previous query adapter
```

This can still keep a thin compatibility `Querier` type in `internal/text/querier.go`.

---

## Backward-compatible `Querier` role after refactor

`Querier` should become a composition root and compatibility wrapper, not the runtime itself.

### New responsibility of `Querier`

1. hold legacy config/setup fields
2. construct dependencies
3. create session from initial chat
4. invoke orchestrator
5. satisfy existing interfaces:
   - `models.Querier`
   - `models.ChatQuerier`

### Example shape

```go
type Querier[C models.StreamCompleter] struct {
	Raw                bool
	username           string
	termWidth          int
	configDir          string
	shouldSaveReply    bool
	tokenWarnLimit     int
	toolOutputRuneLimit int
	out                io.Writer
	Model              C
	costManager        CostManager
	...

	orchestrator       Orchestrator
	presenter          Presenter
	finalizer          SessionFinalizer
	modelRunner        ModelStepRunner
	toolRunner         ToolStepRunner
}
```

This preserves public behavior while moving logic out.

---

## Separation of concerns by behavior

### Concern: model streaming

**Owner:** `modelstep.Runner`

Tests:

- stream text
- stream tool call
- stream stop event
- stream error

### Concern: tool invocation

**Owner:** `toolstep.Runner`

Tests:

- tool call appended as assistant message
- tool output appended as tool message
- truncation/empty-output behavior
- max tool call policy

### Concern: usage capture

**Owner:** `modelstep.UsageSnapshotter` + `persistence.CallUsageRecorder`

Tests:

- usage captured only after completed stream
- one record per model step
- correct usage passed through after tool continuation

### Concern: final query cost

**Owner:** `finalize.SessionFinalizer`

Tests:

- enrich called once
- save called once
- final rendered message built correctly
- save still happens when enrich fails or is unavailable

### Concern: rendering

**Owner:** `Presenter`

Tests:

- streaming token order
- tool call display order
- final pretty print invoked once

### Concern: rate limits

**Owner:** `modelstep.RetryPolicy`

Tests:

- retry on rate limit
- stop after configured retry count
- token-count aware backoff if supported

---

## Suggested testing pyramid

### 1. Pure unit tests

Fast tests with no filesystem and no stdout assertions.

Targets:

- session state transitions
- stream event consumer
- tool limiter
- tool output formatter
- call usage record construction

### 2. Service tests with fakes

Inject fake model runner, tool invoker, presenter, saver, and cost enricher.

Targets:

- orchestrator loop behavior
- finalizer behavior
- error propagation and exactly-once finalization

### 3. Compatibility tests on `Querier`

Keep a smaller set of end-to-end tests ensuring the public behavior is unchanged.

Targets:

- `Query()` saves reply correctly
- `TextQuery()` returns updated chat
- one tool-call flow still works end-to-end

This replaces the current dependence on fairly large black-box `Querier` tests.

---

## Vendor-specific quirks placement

Vendor workarounds should not remain mixed into orchestration.

### Current example

`checkIfGemini3Preview()` and branch logic in `handleToolCall()` are execution-path conditionals.

### Proposed placement

Move them behind a vendor quirk policy.

```go
type ToolCallPolicy interface {
	BeforeToolStep(session *Session, call pub_models.Call) (ToolCallDecision, error)
}

type ToolCallDecision struct {
	Skip bool
	Call pub_models.Call
}
```

Examples:

- Gemini-specific call skipping
- vendor-specific call patching
- future vendor-specific argument normalization

This keeps orchestrator generic.

---

## Data flow for one session

```text
CLI/setup
  -> Querier builds Session
  -> Orchestrator.Run(session)
      -> ModelStepRunner.Run(chat)
          -> stream consumer reads events
          -> presenter emits incremental output
          -> returns text/tool/usage outcome
      -> Session records completed call
      -> CallUsageRecorder persists call usage
      -> if tool call:
           ToolStepRunner.Run(call)
             -> vendor policy patch/validate
             -> presenter shows assistant tool call
             -> invoker executes tool
             -> formatter truncates output
             -> presenter shows tool result
           loop again
         else:
           SessionFinalizer.Finalize(session)
             -> append final assistant message
             -> cost enrich once
             -> save chat once
             -> presenter pretty-prints final answer once
```

---

## Migration strategy

Implement in stages to reduce breakage.

### Stage 1: Extract session and presenter

- introduce `Session`
- move `fullMsg`, `execErr`, `hasPrinted`, tool call counts out of `Querier`
- keep `Querier.Query()` in control for now

### Stage 2: Extract model step runner

- move completion consumption out of `Query()`
- make one model stream return `ModelStepResult`
- preserve existing behavior

### Stage 3: Extract tool step runner

- move `doToolCallLogic()` into `toolstep.Runner`
- keep recursive continuation temporarily at caller

### Stage 4: Introduce orchestrator loop

- replace recursive `TextQuery()` re-entry with loop
- session owns current chat across iterations

### Stage 5: Extract finalizer and call usage recorder

- remove `postProcess()` as the central mixed lifecycle function
- finalization becomes explicit and exactly once
- persist usage at every completed model step

### Stage 6: Reduce `Querier` to wrapper

- `Query()` becomes thin composition and delegation
- keep existing entrypoints intact

---

## Risks and mitigations

### Risk: behavior drift in output rendering

**Mitigation:**

- keep presenter behavior byte-for-byte compatible where feasible
- preserve end-to-end tests around raw and pretty modes

### Risk: token usage currently comes from mutable model object state

**Mitigation:**

- snapshot usage immediately after stream completion in the model runner
- never read usage later in finalizer

### Risk: existing tests assume recursive semantics implicitly

**Mitigation:**

- reframe tests around user-visible results and lifecycle guarantees
- add explicit orchestrator tests for multi-step sessions

### Risk: cost manager currently enriches `chat.TokenUsage`, not per-call records

**Mitigation:**

- short term: finalizer uses final session-level token usage for final query cost, preserving behavior
- medium term: add dedicated storage for `CompletedCalls` if historical per-step accounting is needed in saved chats

---

## Recommended concrete interfaces

These are the minimum interfaces needed to make modules independently testable.

```go
type TokenWarner interface {
	Warn(context.Context, pub_models.Chat) error
}

type ModelStepRunner interface {
	Run(context.Context, *Session) (ModelStepResult, error)
}

type ToolStepRunner interface {
	Run(context.Context, *Session, pub_models.Call) error
}

type CallUsageRecorder interface {
	RecordCompletedCall(context.Context, pub_models.Chat, CallRecord) error
}

type SessionFinalizer interface {
	Finalize(context.Context, *Session) (FinalizationResult, error)
}

type Presenter interface {
	OnAssistantToken(string) error
	OnAssistantToolCall(pub_models.Message) error
	OnToolResult(pub_models.Message) error
	OnFinalAssistantMessage(pub_models.Message) error
	OnRawNewline() error
}

type ToolInvoker interface {
	Invoke(context.Context, pub_models.Call) (string, error)
}

type ConversationSaver interface {
	SavePreviousQuery(string, pub_models.Chat) error
}

type CostEnricher interface {
	Enrich(pub_models.Chat) (pub_models.Chat, error)
}
```

---

## What success looks like in code

After refactor, these statements should be true:

1. `handleToolCall()` no longer calls `TextQuery()` recursively.
2. `Query()` is mostly orchestration setup, not business logic.
3. token usage is snapshotted at the end of each stream and can be persisted immediately.
4. final cost enrichment happens in one explicit finalizer path.
5. tool execution logic is testable without invoking the model.
6. stream consumption is testable without filesystem or terminal behavior.
7. presenter is replaceable in tests.
8. vendor-specific workarounds sit behind policies, not inside the loop.

---

## Final recommendation

The best refactor shape is:

- keep `Querier` as compatibility wrapper
- introduce `Session` as the single owner of runtime state
- introduce an explicit `QueryOrchestrator` loop
- split model streaming, tool execution, per-call usage recording, finalization, and presentation into separate modules
- make per-call usage persistence and end-of-query cost enrichment two different lifecycle operations

That structure directly resolves the current recursion problem and gives clear interfaces so each level can be tested independently.