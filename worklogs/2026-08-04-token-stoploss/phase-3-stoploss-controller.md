# Phase 3 — Stoploss controller (runtime enforcement)

**Status:** Not Started
**Back to:** [README](./README.md)

## Goal

Enforce the token stoploss in the agent loop: after each model step, check
context usage against `max-tokens`; on crossing, inject the handover user
message once and let the agent produce the summary; post-handover tool calls
are refused by the existing escalation ladder.

## Specification

### Controller

New file `internal/text/stoploss.go`:

```go
// stoploss owns both run budgets: the token stoploss (max-tokens + handover
// injection) and the tool-call budget (max-tool-calls). It composes them so
// that once a handover has been requested, further tool calls are treated as
// over-budget and run the existing refusal ladder.
type stoploss struct {
	maxTokens            int    // <= 0 disables the token stoploss
	maxToolCalls         *int   // nil or <= 0 means no tool-call limit
	maxTokensHandoverMsg string // empty => DefaultHandoverInstructions
}

// ApplyToolCallBudget is the EXISTING budget ladder, moved 1:1 from
// toolExecutor.applyToolCallBudget (tool_executor.go:177), with two deltas:
//   - nil OR <= 0 maxToolCalls passes output through untouched (0 = unlimited, D5)
//   - session.HandoverRequested forces the over-budget branch (D2)
// Returns io.EOF after persisting past the final warning; the caller ends the
// run cleanly on io.EOF.
func (s *stoploss) ApplyToolCallBudget(session *QuerySession, out string) (string, error)

// PreflightToolCallBudget decides whether a complete batch may run and reserves
// budget slots. It is side-effect-free. No call in the batch is invoked until
// every call has a decision. Refused calls return ladder text and are emitted
// as tool results without invoking their implementations.

// CheckContextBudget computes the latest request footprint. It uses
// usage.PromptTokens + usage.CompletionTokens; when those are both zero it
// uses usage.TotalTokens. A nil or all-zero usage falls back to
// type-asserts model to models.InputTokenCounter and calls CountInputTokens(ctx, chat).
// On first crossing it appends the handover user message to
// session.Chat.Messages, sets session.HandoverRequested, and prints an
// ancli notice. Later crossings are no-ops. Returns (justInjected, error).
func (s *stoploss) CheckContextBudget(ctx context.Context, model models.StreamCompleter, session *QuerySession, usage *pub_models.Usage) (bool, error)
```

`effectiveBudget(session)` helper: `0` when `session.HandoverRequested`; else
`*maxToolCalls` when set and `> 0`; else `-1` (no limit). The over-budget
branch is taken when `effectiveBudget >= 0 && session.ToolCallsUsed >=
effectiveBudget`. `prefixToolCallsRemainingWithCount` moves onto the
controller (or stays a querier method — executor's choice) and is only called
in the within-budget branch, where `maxToolCalls` is provably set and `> 0`.

### Session state

`QuerySession` (`internal/text/session.go`) gains:

```go
// HandoverRequested is set once the token stoploss has injected the handover
// user message. It forces the tool-call refusal ladder.
HandoverRequested bool
```

### Tool executor integration (`internal/text/tool_executor.go`)

`toolExecutor` stops owning the budget. Its `applyToolCallBudget` method is
removed; ordinary tools, `load_skill`, and lookback tools call the controller
before invoking the tool, so any over-budget refusal has no side effect. The
controller then produces the refusal output for the tool result. Unlimited
behavior and warning text remain unchanged; positive-limit refusals now happen
before invocation by design. The existing tests
(`tool_executor_budget_test.go`, `querier_tool_test.go:Test_maxToolCalls`)
must pass unchanged (their struct literals may need the controller moved onto
`Querier`).

`executeLoadSkill` remains exempt from the normal `max-tool-calls` budget, but
it must still pass through complete-batch preflight before loading a skill.
Thus no tool, including `load_skill`, can execute after handover.

For a batch, preflight all calls first. Then append the assistant tool-call
message and emit one tool result per call in original order. Allowed calls are
invoked only after complete preflight succeeds. Refused calls receive ladder
text and are never invoked. If a refusal reaches `io.EOF`, its tool result is
emitted before the runner returns cleanly.

### Runner integration (`internal/text/session_runner.go`)

Loop change — the ONLY addition is the token check after the tool batch:

```go
if len(stepResult.ToolCalls) > 0 {
	if err := r.toolExecutor.ExecuteBatch(ctx, session, stepResult.ToolCalls); err != nil {
		if errors.Is(err, io.EOF) { return nil }
		return fmt.Errorf("execute tool step %d: %w", stepIndex, err)
	}
	// Token check AFTER the batch so the chat order stays
	// [assistant tool-call] [tool results] [handover user msg].
	if _, err := r.stoploss.CheckContextBudget(ctx, r.querier.Model, session, stepResult.Usage); err != nil {
		return fmt.Errorf("stoploss check step %d: %w", stepIndex, err)
	}
	stepIndex++
	continue
}
```

No handover branch is needed in the runner: when `session.HandoverRequested`
is already true, `ExecuteBatch` → `executeTool` →
`ApplyToolCallBudget` takes the over-budget branch automatically (warning as
tool result; `io.EOF` after persistence → clean `return nil`). Even on the
`io.EOF` path, the assistant tool-call and refusal tool-result must be appended
before the runner ends; otherwise the transcript contains an invalid dangling
call.

Notes:

- A plain-reply step never reaches `CheckContextBudget` (loop returns) — the
  run finished on its own; nothing to hand over (D11).
- The crossing step's tool batch executes first (legitimate pre-handover
  decisions); the handover message lands after those results.
- The post-handover summary step ends the run via `EndedNormally` — no extra
  termination logic.
- When `maxToolCalls` is also set and handover is NOT requested, the budgets
  are independent; the ladder is shared.

### Budget ladder (preserved contract, two deltas)

Original contract (kept byte-for-byte for the non-handover, positive-limit
case):

1. within budget: prefix `[ Tool calls remaining: N ] ` onto the output, increment `ToolCallsUsed`.
2. over budget: replace output with `ERROR: No more tool calls allowed. `
   (+ `You will be HARD SHUT DOWN if you persist. ` when persistence > 0;
   - `This is your LAST WARNING. ` when persistence > 1).
3. persistence > 2: return `io.EOF` after the refusal tool result is appended
   (run ends cleanly).

Deltas: (a) nil or 0 limit passes through untouched (D5); (b)
`session.HandoverRequested` selects the over-budget branch with effective
budget 0 (D2).

### Notification

`CheckContextBudget` prints a human-facing notice once on first crossing,
e.g. `stoploss: context usage ~N tokens reached max-tokens (M); injecting
handover instructions` via `ancli.Warnf`. The agent-facing notification is the
injected user message itself. No notice on non-crossing steps, no repeat
notices.

## Integration contract

| Input / trigger                                                  | Collaborators / fakes                          | Externally observable result                                                                      | Required side effects                   | Prohibited side effects                                             |
| ---------------------------------------------------------------- | ---------------------------------------------- | ------------------------------------------------------------------------------------------------- | --------------------------------------- | ------------------------------------------------------------------- |
| Agent loop; step usage crosses `max-tokens`; step has tool calls | `MockQuerier` with `streamFn` + per-step usage | Handover user message appended after the tool results; `HandoverRequested == true`; run continues | `ancli` notice printed once             | message injected before the tool batch; re-injection on later steps |
| Post-handover step ends with tool calls                          | same                                           | Tool is not invoked; its tool result carries `No more tool calls allowed`; run continues          | `ToolCallsUsed` incremented per refusal | side effect runs; output visible to the model; summary cut off      |
| Agent persists with tools past the final warning after handover  | same                                           | `io.EOF`; `Run` returns nil (clean end)                                                           | none                                    | error surfaced to the user as a failure                             |
| Step usage nil and model implements `InputTokenCounter`          | `MockQuerier` implementing `CountInputTokens`  | Check uses the estimate; crossing still injects                                                   | none                                    | check silently skipped when an estimate is available                |
| Step usage is non-nil but all token fields are zero              | `MockQuerier` implementing `CountInputTokens`  | Same fallback as nil usage; crossing still injects                                                | none                                    | zero usage suppressing an available estimate                        |
| Step usage nil and model implements neither interface            | `MockQuerier` without either                   | Check skipped for that step; no panic, run continues                                              | none                                    | panic or error                                                      |
| `max-tokens` 0/absent; `max-tool-calls` nil                      | any                                            | No injection ever; no budget prefix; behavior identical to today                                  | none                                    | any behavior change                                                 |
| `max-tool-calls` 0, handover not requested                       | any                                            | Output passes through untouched; no increments (0 = unlimited)                                    | none                                    | refusal ladder triggered on 0                                       |
| `max-tool-calls` 3, handover not requested                       | any                                            | Exactly the current ladder: 3 prefixed calls, then warnings, then io.EOF                          | identical to today                      | regression (existing tests must stay green)                         |
| Post-handover step calls `load_skill`                            | `MockQuerier` + fake skill loader              | Skill is not loaded; the tool result carries the refusal warning                                  | refusal counter incremented             | skill loader invoked                                                |

## Acceptance criteria

1. `Test_applyToolCallBudget` (existing) passes unchanged for nil and positive
   limits; a new case asserts `maxToolCalls == 0` behaves as unlimited.
2. New unit tests for `CheckContextBudget`: crossing injects once; non-crossing
   no-ops; nil and all-zero usage fallbacks (TotalTokens, InputTokenCounter); notice printed
   once; `DefaultHandoverInstructions` used for an empty configured message.
3. New integration test over `sessionRunner.Run` with a `MockQuerier`
   `streamFn`: multi-step agent loop crosses the limit, handover message
   injected after tool results, the final step is the summary, run ends with
   the summary as `FinalAssistantText`.
4. New integration test: post-handover step requests a tool; the tool result
   carries the `No more tool calls allowed` warning, the agent then produces
   the summary, and the run ends cleanly (nil error).
5. `io.EOF` from the refusal ladder ends `Run` with nil error (clean end),
   matching today's `max-tool-calls` behavior.
6. `go test ./internal/text/ -timeout=60s` green.

### Contract amendments from review R1

- Nil and all-zero usage are unavailable. The controller uses
  `InputTokenCounter` before skipping the check.
- The primary value is the latest request footprint, not cumulative spend; the
  fallback estimates the current chat.
- Handover refusal is checked before invocation for every tool, including
  `load_skill` and lookback tools. The normal positive `max-tool-calls` budget
  still exempts `load_skill` when handover has not been requested.

## Error coverage

| Failure condition                           | Expected error / recovery / external outcome                        | Test                                   |
| ------------------------------------------- | ------------------------------------------------------------------- | -------------------------------------- |
| `CheckContextBudget` estimate path errors   | Propagated as `stoploss check step N: ...`; run fails visibly       | unit: `CountInputTokens` returns error |
| Tool refusal persistence past final warning | `io.EOF` → `Run` returns nil                                        | integration (MockQuerier)              |
| Crossing step's `ExecuteBatch` fails        | Existing error path (`execute tool step N`); no injection attempted | existing behavior, no new test         |
| `session == nil`                            | Guarded error at `Run` entry (existing)                             | existing                               |

## Implementation notes

To be written by the executing agent.

## Review findings (review 2, 2026-08-04)

- [ ] **R2-01 — High:** Replace the output-only budget call with a side-effect-free
      preflight/reservation decision used by every execution path. Current code calls
      `tools.Invoke` at `tool_executor.go:103`, `LoadSkill` at `:223`, and lookback
      execution at `tool_executor_lookback.go:44` before budget enforcement. If the
      agent requests `cmd` after handover, the command runs despite the acceptance
      criterion that every post-handover tool is refused before invocation. Add
      ordinary, `load_skill`, and lookback tests that instrument the side effect.

- [ ] **R2-02 — High:** Emit the assistant tool-call and refusal tool-result before
      honoring `io.EOF`. Current `executeTool` returns the EOF from the budget method
      before `emitToolResult` (`tool_executor.go:104-108`); a persistent post-handover
      call can therefore be saved with no matching tool result. The required failure
      scenario is persistence past the final warning: `Run` returns nil and the
      transcript still contains a valid assistant/tool exchange.

- [x] **R2-03 — Medium:** Removed “monotonic across a run” from the metric
      rationale in the README and D1. A latest request footprint can decrease when a
      later completion is shorter, even though the chat itself grows. The chosen
      metric remains valid.

## Review findings (review 3, 2026-08-04)

- [x] **R3-01 — High:** Change the fallback contract and pseudocode to type
      assert `models.InputTokenCounter` before calling `CountInputTokens`. The
      generic `models.StreamCompleter` interface at `internal/models/models.go:20`
      does not define that method, so a literal implementation of
      `model.CountInputTokens` cannot compile. Test both an implementing model and a
      model implementing neither interface.

- [x] **R3-02 — High:** Define preflight as an atomic operation over the full
      `ExecuteBatch` call list. Reserve or decide all calls before invoking any
      tool, then emit one assistant call and one tool result for each call,
      including a refusal that returns `io.EOF`. For example, a batch containing
      `cmd` and a second post-handover call must not run `cmd` before discovering
      that the batch's other call is refused. Add a test that instruments both
      side effects.

## Review resolution (review 4, 2026-08-04)

- [x] **R3-01:** The fallback explicitly asserts `models.InputTokenCounter`;
      `StreamCompleter` remains unchanged.
- [x] **R3-02:** Batch preflight is atomic, applies to all over-budget paths,
      and requires a result message before the clean EOF return.

## Review findings (review 5, 2026-08-04)

- [x] **R5-01 — High:** Resolved in Review 6. The Strategy now defines exactly
      one unconstrained post-handover step, followed by refusal-ladder steps when
      the model persists. The phase requires tests for the follow-up warnings and
      clean `io.EOF` termination, so stopping after the first refusal is no longer
      compatible with the contract.
