# Parallel tool calls plan for clai

## Goal

Bring `clai` tool calling up to OpenAI/OpenAI-go parity for **parallel tool calls**.

Today, `clai` effectively handles one streamed tool call at a time and only tracks the first tool-call delta. That breaks correctness for models that emit multiple tool calls in one assistant turn, and it also prevents concurrent execution of independent tools.

## Main findings

### In clai

1. `internal/text/generic/stream_completer.go` only reads `choice.Delta.ToolCalls[0]`.
   - This means only the first tool call in a chunk is considered.
   - Interleaved multi-call streaming cannot work correctly with the current state model.

2. Generic request encoding currently disables parallel tool calls.
   - `internal/text/generic/stream_completer.go:69` sets `ParalellToolCalls: false`.
   - `internal/text/generic/stream_completer_models.go:101` also has both a misspelling and the wrong JSON field name: `parallel_tools_call`.

3. The querier only reacts to a single `pub_models.Call` event.
   - `internal/text/querier.go:248-276` switches on one `pub_models.Call`.
   - `internal/text/querier_tool.go` then mutates chat and recursively continues immediately after that single call.

4. Tool execution is synchronous at the invocation layer.
   - `internal/tools/handler.go` exposes `Invoke(call)` which is synchronous.
   - That is fine; execution can still be parallelized in the caller with goroutines.

### In openai-go / OpenAI

1. OpenAI request models expose `parallel_tool_calls`.
   - `openai-go/chatcompletion.go:2984-2987`
   - `openai-go/responses/inputtoken.go:79-80`

2. OpenAI-go accumulates streamed tool calls by index, not “first one wins”.
   - See `openai-go/streamaccumulator.go` references.
   - This is the key design cue: streamed tool calls must be assembled by tool-call index / identity.

3. OpenAI semantics are batch-oriented.
   - The model emits one assistant turn containing potentially multiple tool calls.
   - The client executes them, then submits all tool outputs back before continuing.

## Recommendation

Implement this as a **tool-call batch per assistant turn**.

That means:

1. collect all tool calls emitted in the current assistant turn
2. execute them in parallel
3. append all resulting tool messages in deterministic order
4. perform exactly one follow-up model call

This is the cleanest way to match OpenAI behavior and to keep execution fast.

## Concrete suggestion

### 1. Fix request shape first

In `internal/text/generic/stream_completer_models.go`:

- rename `ParalellToolCalls` to `ParallelToolCalls`
- change the JSON tag from `parallel_tools_call` to `parallel_tool_calls`

In `internal/text/generic/stream_completer.go`:

- set `ParallelToolCalls: true` when tools are enabled

Reason:

- this is required for parity
- this also makes intent explicit to OpenAI-compatible vendors

### 2. Replace single-call stream state with indexed batch assembly

Current state only supports one in-flight tool call:

- `toolsCallName`
- `toolsCallArgsString`
- `toolsCallID`
- `extraContent`

Suggested replacement:

```go
type toolCallAssembly struct {
	Index        int
	ID           string
	Name         string
	Type         string
	Arguments    string
	ExtraContent map[string]any
}
```

inside `StreamCompleter`:

```go
toolCalls map[int]*toolCallAssembly
```

Rules:

- key by tool call index
- merge chunks into the matching assembly entry
- keep arrival order stable by index
- tolerate weird provider indices conservatively

This is the core blocker today.

### 3. Stop emitting tool calls one-by-one during partial assembly

Current behavior emits a `pub_models.Call` as soon as one argument buffer becomes parseable JSON.

That is too early for parallel tool calling because:

- multiple calls can be interleaved
- one parseable call does not mean the assistant turn is complete
- immediate recursion after the first call prevents collecting siblings

Suggested change:

- add a batch completion event, for example:

```go
type ToolCallsEvent struct {
	Calls []pub_models.Call
}
```

- while tool-call deltas stream in, accumulate only
- emit the batch event only when the assistant tool-call turn is complete

This should happen on the appropriate finish signal for that stream format.

### 4. Execute tool calls concurrently in the querier

Add a batch path in `internal/text/querier.go` / `internal/text/querier_tool.go`.

Suggested new method:

```go
func (q *Querier[C]) handleToolCalls(ctx context.Context, calls []pub_models.Call) error
```

Behavior:

1. post-process any pending assistant text once
2. patch all calls
3. append one assistant message containing `ToolCalls: calls`
4. run all tools concurrently with goroutines
5. gather results by original index
6. append one tool message per result in deterministic order
7. do one recursive `TextQuery()` afterwards

Important detail:

- run tool execution in parallel
- do **not** mutate chat concurrently
- do **not** print from worker goroutines

That gives speed without nondeterministic history or noisy output.

### 5. Preserve order in chat even if execution finishes out of order

Parallel execution should not change conversation ordering.

Use a result container like:

```go
type toolCallResult struct {
	Index int
	Call  pub_models.Call
	Out   string
}
```

Collect concurrently, then append results in original call order.

That matters because tool output messages must reference stable `ToolCallID`s and should match the assistant’s tool-call array ordering.

### 6. Keep max-tool-calls logic deterministic

Current limit accounting is serial.

For a batch:

- determine remaining budget before launching goroutines
- only allow the first `N` calls in the batch to run
- synthesize the current error text for excess calls
- increment `amToolCalls` deterministically, not from workers

This avoids races and preserves current semantics.

## Proposed implementation order

1. **Fix request model field and request body generation**
   - low risk
   - immediately aligns with OpenAI field naming

2. **Introduce batch event type for tool calls**
   - lets querier reason about a full assistant tool turn

3. **Refactor stream completer to accumulate tool calls by index**
   - this is the real functional change

4. **Add concurrent execution in querier**
   - goroutines + wait group
   - collect outputs in original order

5. **Preserve pretty-printing and chat mutation outside workers**
   - keep UX deterministic

## Test plan

Per project rules, write the failing tests first.

### Stream-level tests

Add tests covering:

1. request body includes `parallel_tool_calls: true`
2. multiple tool calls in the same delta are both accumulated
3. interleaved deltas for tool index `0` and `1` are assembled correctly
4. no tool-call event is emitted prematurely for the first completed call
5. the final emitted batch preserves original order

### Querier tests

Add tests covering:

1. one assistant tool-call message plus N tool output messages are appended
2. multiple tool calls trigger exactly one follow-up `TextQuery()`
3. tool execution happens in parallel
4. output ordering remains stable even when execution completion order differs
5. `maxToolCalls` is enforced predictably across a batch

For the concurrency test:

- register two fake tools that block on channels
- release them together
- assert elapsed time is much closer to one tool latency than the sum

## Short architectural verdict

If you want `clai` to be on-par with OpenAI, the right model is **batch tool collection + parallel execution + single continuation round**.

Trying to keep the current “react immediately to the first tool call seen” model will stay fragile and will never correctly support interleaved parallel tool streams.

## Suggested next step

If you want, next I can implement this properly in TDD order:

1. failing tests for request field + multi-call stream assembly
2. implementation for batch events
3. failing tests for concurrent querier execution
4. implementation and validation with `-timeout=30s`

## Current progress

### Implemented

The following is now done and passing:

1. **Request shape fixed**
   - `ParallelToolCalls` is used in the generic request model.
   - request JSON includes `parallel_tool_calls: true` when tools are enabled.

2. **Stream completer batches tool calls**
   - `internal/text/generic/stream_completer.go` now accumulates tool calls by index.
   - final tool-call chunks are merged before flushing on `finish_reason == "tool_calls"`.
   - batch output is emitted as `models.ToolCallsEvent`.

3. **Querier handles tool-call batches**
   - `internal/text/querier.go` now handles `models.ToolCallsEvent`.
   - `internal/text/querier_tool.go` has batch handling via `handleToolCalls(...)`.

4. **Parallel execution implemented**
   - tool invocations are run concurrently with goroutines.
   - chat mutation remains deterministic and happens after collection.
   - tool output messages are appended in original call order.
   - exactly one follow-up `TextQuery()` happens per emitted batch.

5. **Assistant banner formatting improved**
   - for **1 tool call**, assistant output uses normal single-call formatting:
     - `Call: 'tool_a', inputs: [ ... ]`
   - for **2+ tool calls**, assistant output uses:
     - `parallel tool calls: <n>, tools: ["tool_a", "tool_b", ...]`
   - multi-call banner preserves original model-emitted order.

### Tests added/updated

#### Generic streaming

- request body includes `parallel_tool_calls`
- incremental tool-call assembly works
- multi-call batch assembly works
- MCP tool-call streaming emits `models.ToolCallsEvent`

Files:
- `internal/text/generic/stream_completer_test.go`
- `internal/text/generic/stream_completer_mcp_toolcall_test.go`

#### Querier batching

- one batch triggers exactly one follow-up query
- batch execution happens in parallel
- output ordering remains deterministic
- batched assistant banner is printed
- single-call batch falls back to normal call formatting with inputs
- banner preserves tool order

Files:
- `internal/text/querier_parallel_tool_calls_test.go`

### Validation completed

These have already been run successfully:

- `go test ./internal/text/generic -timeout=30s`
- `go test ./internal/text -timeout=30s`
- `go test ./... -timeout=30s`

## Upcoming work

### 1. Add `DEBUG_TOOLS` logging

Status: **not started yet**.

Goal:
- make tool-flow debugging much easier with explicit `ancli` notices/prints when `DEBUG_TOOLS` is truthy.

Recommended scope:

#### In single-call flow

Inside `handleToolCall(...)` / `doToolCallLogic(...)`, log:

- when a tool call is received
- when the call is patched
- before invocation
- after invocation
- when tool output is appended
- before recursive follow-up query

Suggested examples:

```go
if misc.Truthy(os.Getenv("DEBUG_TOOLS")) {
	ancli.Noticef("tool call received: %s", call.PrettyPrint())
}
```

```go
if misc.Truthy(os.Getenv("DEBUG_TOOLS")) {
	ancli.Noticef("invoking tool %q", call.Name)
}
```

```go
if misc.Truthy(os.Getenv("DEBUG_TOOLS")) {
	ancli.Noticef("tool %q returned %d chars", call.Name, len(out))
}
```

#### In batch flow

Inside `handleToolCalls(...)`, log:

- batch receipt
- number of calls
- tool names in original order
- remaining tool-call budget before launch
- each launch
- each completion
- deterministic append phase
- single follow-up query after the batch

Very useful message:

```go
ancli.Noticef("parallel tool batch received: %s", formatParallelToolCallsBanner(calls))
```

Important:
- keep worker goroutine logging minimal if possible
- if logging from workers, include tool index/name to make interleaving readable

Implementation notes from repo inspection:

- Relevant code lives in `internal/text/querier_tool.go`.
- Existing debug knobs already present:
  - `DEBUG`
  - `TEXT_QUERIER_DEBUG`
  - `DEBUG_CALL`
- There is currently **no** `DEBUG_TOOLS` handling in text querier flow.
- Best insertion points are:
  - `handleToolCall(...)`
  - `doToolCallLogic(...)`
  - `handleToolCalls(...)`
- Existing batch helper `formatParallelToolCallsBanner(...)` should be reused for concise logging.
- Single-call flow currently prints a debug JSON blob only when `q.debug || DEBUG_CALL`; `DEBUG_TOOLS` should be additive and more human-oriented.

Recommended concrete log points:

1. `handleToolCall(...)`
   - `tool call received`
   - `follow-up query after single tool call`

2. `doToolCallLogic(...)`
   - `patched tool call`
   - `invoking tool`
   - `tool returned N chars`
   - `appending tool output`

3. `handleToolCalls(...)`
   - `parallel tool batch received: ...`
   - `remaining tool-call budget before launch: N`
   - `launching tool[i]: <name>`
   - `tool[i] completed: <name>, chars: N`
   - `appending batched tool outputs in original order`
   - `follow-up query after tool call batch`

Testing recommendation:

- Add focused tests in `internal/text/querier_parallel_tool_calls_test.go` and/or `internal/text/querier_tool_test.go`.
- Capture output with `strings.Builder`.
- Set `DEBUG_TOOLS=1` via `t.Setenv`.
- Assert for stable substrings rather than full exact output, since normal pretty-print output may coexist.
- Run first with:
  - `go test ./internal/text -timeout=30s`

### 2. Add deterministic fake model for debug/e2e

Status: **not started yet**.

Goal:
- have a fake model with predictable scripted tool-call behavior.
- use it for end-to-end-ish tests and manual debugging.

Suggested script:

1. first assistant turn emits **one** `ls`
2. second assistant turn emits **two parallel** `ls` calls
3. third assistant turn emits **one** `ls`
4. final assistant turn emits normal text / stop

This matches the requested pattern:
- one single tool call
- one parallel tool batch
- one single tool call again

### Recommended implementation approach

Add a new vendor mock type, separate from the current simple `vendors.Mock`.

Suggested file:

- `internal/vendors/mock_parallel.go`

Suggested type:

```go
type MockParallel struct{}
```

Suggested behavior:

- inspect chat state to decide which turn comes next
- return deterministic events from `StreamCompletions(...)`
- emit `models.ToolCallsEvent` directly rather than simulating network stream parsing

Why this is a good first version:
- simpler than streaming fake partial deltas
- directly exercises querier batch handling
- ideal for e2e-ish query tests

Implementation notes from repo inspection:

- There is already a very small `internal/vendors/mock.go` with type:

```go
type Mock struct{}
```

- It implements `models.StreamCompleter` and simply echoes the last user message then emits `models.StopEvent{}`.
- A parallel-tool fake should likely sit next to it as a separate type rather than overloading existing `Mock` behavior.
- `models.ToolCallsEvent` already exists in `internal/models/models.go`, so the fake model can emit whole call batches directly.

Recommended scripted behavior:

- Count tool messages in `chat.Messages`.
- Emit by phase:
  - `0` tool messages -> single call `ls_1`
  - `1` tool message  -> parallel calls `ls_2a`, `ls_2b`
  - `3` tool messages -> single call `ls_3`
  - `4` tool messages -> final text + `models.StopEvent{}`

Suggested event payloads:

```go
models.ToolCallsEvent{Calls: []pub_models.Call{...}}
```

with fixed names/IDs:

- `{ID: "ls_1", Name: "ls", Inputs: &pub_models.Input{"directory": "."}}`
- `{ID: "ls_2a", Name: "ls", Inputs: &pub_models.Input{"directory": ".", "long": true}}`
- `{ID: "ls_2b", Name: "ls", Inputs: &pub_models.Input{"directory": ".", "all": true}}`
- `{ID: "ls_3", Name: "ls", Inputs: &pub_models.Input{"directory": "."}}`

Routing notes:

- `internal/create_queriers.go` currently special-cases `conf.Model == "test"` to use `vendors.Mock`.
- Add another explicit branch before vendor substring matching, e.g.:
  - `conf.Model == "mock-parallel-tools"`
- `internal/text/querier_setup.go:vendorType(...)` currently routes any model containing `test` to mock and any model containing `mock` to generic mock.
- Because `mock-parallel-tools` contains `mock`, `vendorType(...)` will currently collapse config naming to `mock/mock/mock`.
- If deterministic config filenames matter, add an explicit `mock-parallel-tools` branch there too.

Alternative later:
- a second fake stream model that emits **partial streamed tool-call chunks**
- useful if wanting more realistic end-to-end testing of stream assembly too

### Hint for determining conversation phase

Use chat contents to count completed tool outputs:

- 0 tool messages seen → emit first single `ls`
- 1 tool message seen → emit parallel `ls` x2
- 3 tool messages seen → emit last single `ls`
- 4 tool messages seen → emit final text + stop

This makes the model stateless and fully deterministic from the transcript alone.

Pseudo-logic:

```go
toolMsgs := 0
for _, msg := range chat.Messages {
	if msg.Role == "tool" {
		toolMsgs++
	}
}

switch toolMsgs {
case 0:
	return oneLS()
case 1:
	return twoParallelLS()
case 3:
	return oneLSAgain()
default:
	return finalText()
}
```

### Tool IDs for determinism

Use fixed IDs:

- `ls_1`
- `ls_2a`
- `ls_2b`
- `ls_3`

This makes debugging and assertions much easier.

### Model routing suggestion

Wire it through model selection in a controlled way.

Possible names:

- `test-parallel-tools`
- `mock-parallel-tools`

Suggested `selectTextQuerier()` branch:

```go
if conf.Model == "mock-parallel-tools" {
	qTmp, err := text.NewQuerier(ctx, conf, new(vendors.MockParallel))
	...
}
```

Be careful:
- `vendorType()` in `internal/text/querier_setup.go` may also need a special case
- the config file naming should remain deterministic and not collide unexpectedly

### 3. Add end-to-end-ish tests using the deterministic fake model

Suggested test target:

- either under `internal/` alongside `create_queriers` / setup tests
- or under `internal/text/` if testing through a `Querier`

Recommended first test:

#### `TestMockParallelToolsModel_EndToEnd`

Setup:

- temp config dir
- initial chat with one user message
- model = `mock-parallel-tools`
- allow `ls`
- raw output to `strings.Builder`

Assert:

- first single call occurs
- then a parallel batch occurs
- then another single call occurs
- exactly 4 tool output messages exist in final chat
- assistant batch banner appears once for the parallel turn
- follow-up recursion proceeds cleanly to final answer

Also useful assertions:

- tool call IDs are the expected fixed ones
- assistant tool-call batches have correct lengths:
  - `1`, then `2`, then `1`

Recommended first test split:

1. `internal/create_queriers_test.go`
   - `TestSelectTextQuerier_MockParallelTools`
   - verify model is found and querier is created

2. `internal/text/...` or `internal/...`
   - `TestMockParallelToolsModel_EndToEnd`
   - verify transcript evolves through `1 -> 2 -> 1 -> final text`

Validation sequence for this phase:

- `go test ./internal/text -timeout=30s`
- `go test ./internal -timeout=30s`
- `go test ./... -timeout=30s`

## Suggested next concrete TDD slice

If continuing immediately, the smallest high-value next step is **`DEBUG_TOOLS` logging only**.

Recommended TDD order:

1. Add a failing single-call logging test in `internal/text/querier_tool_test.go`
   - set `DEBUG_TOOLS=1`
   - invoke `handleToolCall(...)`
   - assert output contains stable markers such as:
     - `tool call received`
     - `invoking tool`
     - `tool returned`
     - `follow-up query after single tool call`

2. Add a failing batch logging test in `internal/text/querier_parallel_tool_calls_test.go`
   - set `DEBUG_TOOLS=1`
   - invoke `handleToolCalls(...)`
   - assert output contains:
     - `parallel tool batch received`
     - `remaining tool-call budget before launch`
     - `appending batched tool outputs in original order`
     - `follow-up query after tool call batch`

3. Implement minimal logging helpers in `internal/text/querier_tool.go`
   - likely a tiny helper such as:

```go
func debugToolsEnabled() bool {
	return misc.Truthy(os.Getenv("DEBUG_TOOLS"))
}
```

   - and maybe a querier helper for consistent formatting, e.g. `q.noticeToolDebugf(...)`

4. Re-run:
   - `go test ./internal/text -timeout=30s`

5. Then move on to `mock-parallel-tools`

This order keeps the change small, observable, and easy to review.

### 4. Optional later improvement: fake streamed parallel chunks

Once the deterministic batch model exists, a stronger fake model can be added which emits:

- partial tool-call chunks
- interleaved indices
- final `finish_reason == "tool_calls"`

This would exercise both:

- stream completer assembly
- querier batch execution

That is not necessary for the immediate debugging goal, but is a good follow-up.

## Practical hints for next implementation session

1. Start with `DEBUG_TOOLS` tests first.
2. Keep all new logging behind `misc.Truthy(os.Getenv("DEBUG_TOOLS"))`.
3. Prefer `ancli.Noticef(...)` for flow-level logs.
4. Keep deterministic fake model transcript-driven rather than stateful when possible.
5. Use fixed tool IDs and fixed final response text.
6. Continue validating with:
   - `go test ./internal/text -timeout=30s`
   - `go test ./... -timeout=30s`

## Remaining repo-wide validations

Still recommended from `AGENTS.md` before final completion:

- `go test ./... -race -cover -timeout=10s`
- `go run honnef.co/go/tools/cmd/staticcheck@latest ./...`
- `go run mvdan.cc/gofumpt@latest -w .`