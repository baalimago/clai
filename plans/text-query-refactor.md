## Quick overview

### Current state
The current text query flow works, but the execution model is overly coupled and hard to reason about. One path currently handles:

- model streaming
- tool execution
- recursive re-entry into query execution
- rendering
- persistence
- token tracking
- cost enrichment
- vendor-specific workarounds

### Core limitation
Tool calls are recursive today. That means one user query becomes several nested query executions instead of one explicit run.

This causes problems with:

- post-processing timing
- persistence correctness
- token usage accounting
- end-of-query cost recording
- cancellation/control flow
- maintainability and extensibility

### Refactor goal
Refactor text querying into one explicit, non-recursive execution flow per user query.

That flow should:

- run model steps
- handle tool steps
- continue in the same session
- finalize exactly once when control returns to the user

---

## Objective

### Primary objective
Design a session-based, non-recursive text query system that handles model output and tool calls through an explicit orchestration loop.

### Mandatory requirements
The upgraded system must:

1. preserve all practical current functionality
2. keep all `main` package files working
3. remove recursive tool-driven re-entry into `TextQuery()`
4. persist token usage at the end of every completed model stream
5. persist query cost only once, at end-of-query, when control returns to the user
6. make post-processing deterministic
7. scale cleanly for more vendors, tool types, and multi-step flows

---

## Target behavior

One top-level user query should become one execution session:

1. prepare initial chat
2. send chat to model
3. stream and process events
4. if a tool call appears:
   - append assistant tool-call message
   - invoke tool
   - append tool result
   - continue model execution
5. when a model stream ends, persist that call's token usage from the final usage-bearing chunk
6. repeat until the model returns control to the user
7. finalize once:
   - persist final chat state
   - record final query cost
   - render final output

---

## Design direction

### 1. Introduce a query session abstraction
Add a dedicated runtime object for one end-user query, e.g. `QuerySession` / `Run`.

It should own:

- current chat state
- current assistant output buffer
- per-call token usage
- cumulative session metadata
- tool call counters/limits
- termination/finalization state

This becomes the execution unit instead of recursion.

### 2. Replace recursion with an explicit step loop
Use a loop-based orchestrator:

- run model step
- consume events
- execute tool step if needed
- continue with updated chat
- finalize when no continuation is needed

This makes the flow predictable and testable.

### 3. Separate concerns
Split responsibility into clear layers:

- **orchestration**: session lifecycle and step transitions
- **stream handling**: normalize model events
- **tool execution**: invoke tools and append outputs
- **persistence**: usage per call, final cost at query end
- **presentation**: streaming output and pretty-printing

### 4. Persist token usage per model call
Each model invocation should produce a call-level usage record.

Suggested contents:

- model
- start/end time
- token usage
- step/result metadata

In most vendors, usage is only available in the final streamed chunk for that call.

That means the persistence boundary should be the end of the stream for each model call:

1. start model stream
2. process text/tool events while streaming
3. receive final usage-bearing chunk
4. close that model call
5. persist token usage for that completed call
6. only then continue to the next tool/model step

### 5. Persist query cost only at final handoff
Cost should be written once, after the full user query completes.

So:

- token usage persistence = end of every completed model stream
- query cost persistence = once per completed top-level query

### 6. Make finalization explicit
There should be exactly one finalization path for a top-level query session.

That stage should handle:

- final assistant message append
- final chat persistence
- final cost enrichment
- reply/global scope save
- final output formatting

### 7. Normalize tool handling
Tool handling should become a normal step in the loop:

- patch/validate tool call
- append assistant tool-call message
- invoke tool
- truncate if needed
- append tool output message
- return control to orchestrator

It should never re-enter querying by itself.

---

## Success criteria

The redesign is successful if:

- one user query maps to one explicit session
- tool continuation is non-recursive
- token usage is durably persisted at the end of every completed model stream
- query cost is recorded exactly once at end-of-query
- post-processing happens in one predictable place
- the core flow is easier to extend and debug
- vendor quirks are isolated from orchestration

## Objective statement
Refactor the text query flow into a session-based, non-recursive execution engine that processes model output and tool calls through an explicit orchestration loop, preserves current behavior, persists token usage at the end of every completed model stream, and records query cost only once when control returns to the user.