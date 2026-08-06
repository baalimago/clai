# Phase 3 — Stoploss controller (runtime enforcement)

**Status:** Complete (fix round, review 11)
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
is already true, `ExecuteBatch` → the preflight ladder (`PreflightToolCallBudget`)
takes the over-budget branch automatically (warning as tool result; `io.EOF`
after persistence → clean `return nil`). Even on the
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

Executed 2026-08-04 (imago + clai, worker session 3).

Added:

- `internal/text/stoploss.go` (new): the `stoploss` controller owning both run
  budgets. `effectiveBudget` returns 0 after handover, the configured positive
  limit, or -1 (unlimited); `ladderText` is the shared escalation builder
  (plain refusal → HARD SHUT DOWN → LAST WARNING → io.EOF).
  `ApplyToolCallBudget` is the 1:1 moved ladder with the two deltas (D5: nil
  or <= 0 limit passes through untouched; D2: handover forces the over-budget
  branch). `PreflightToolCallBudget` is the side-effect-free batch decision
  (R2-01/R3-02): it reserves budget slots and returns one `toolCallBudgetPlan`
  per call (allowed + remaining-count prefix, or refused + ladder text +
  hardStop). `CheckContextBudget` computes the latest request footprint
  (prompt+completion, else total_tokens, else the `models.InputTokenCounter`
  estimate, else skip) and on the first crossing appends the handover user
  message, sets `session.HandoverRequested`, and prints one `ancli` notice.
  `Querier.newStoploss()` builds the controller from the querier's config;
  all run state stays on the session, so the controller is stateless.
- `internal/text/session.go`: `QuerySession.HandoverRequested`.
- `internal/text/tool_executor.go`: `Execute` now delegates to `ExecuteBatch`;
  the batch preflights every call before invoking any tool, emits the grouped
  assistant tool-call turn, then runs each planned call and emits one tool
  result per call in original order, returning io.EOF only after the final
  refusal result is emitted (R2-02). `applyToolCallBudget`,
  `prefixToolCallsRemainingWithCount`, and the output-based `executeTool` are
  gone. `load_skill` self-emits its assistant tool-call after a successful
  load (D17); a refused `load_skill` emits assistant + ladder result so the
  exchange stays valid.
- `internal/text/tool_executor_lookback.go`: `executeLookbackTool` is gone;
  lookback dispatch lives in `runPlannedCall` after the budget preflight.
- `internal/text/session_runner.go`: the runner owns `stoploss *stoploss`
  (built lazily in `Run` when nil) and calls `CheckContextBudget` AFTER each
  tool batch so the chat order stays `[assistant tool-call] [tool results]
  [handover user msg]` (D11). io.EOF from the ladder ends `Run` with nil.
- `internal/text/querier.go`: `Query()` wires `stoploss: q.newStoploss()`.

Tests (written first, red against the pre-phase code):

- `internal/text/stoploss_controller_test.go` (new): `CheckContextBudget` unit
  contract — disabled at max-tokens 0, non-crossing no-op, crossing injects
  once with one notice, total-tokens fallback, single-side prompt+completion
  sum, nil/all-zero usage → InputTokenCounter fallback, skip when neither
  source exists, counter error propagation, no re-injection after handover;
  `PreflightToolCallBudget` — nil/0 = unlimited, ordered slot reservation,
  escalation ladder to io.EOF, handover forces refusal incl. load_skill,
  load_skill exempt pre-handover. The CheckContextBudget rows construct the
  controller through `newStoploss` so they exercise the construction-time
  message resolution (R9-03).
- `internal/text/stoploss_runner_test.go` (new): acceptance 3 (crossing
  injects handover after the tool results, summary ends the run), acceptance
  4 (post-handover tool refused before invocation, ladder text in the result,
  clean end), acceptance 5 + R2-02 (persistence past the final warning ends
  Run nil with a fully paired transcript), R2-01 ordinary/load_skill/lookback
  side-effect instrumentation with positive controls, R3-02 atomic batch
  (allowed + refused side effects, post-handover batch refuses all), R9-01
  mixed-batch pairing (`[load_skill, cmd]`, `[cmd, load_skill]`,
  `[cmd, load_skill, cmd]`, and both post-handover refusal orderings).
- `internal/text/tool_executor_budget_test.go`: `Test_applyToolCallBudget`
  drives `stoploss.PreflightToolCallBudget` with single-call slices (nil /
  positive / 0=unlimited / escalation to hardStop), unchanged assertions for
  nil and positive limits (R9-02).

`Test_maxToolCalls` (querier_tool_test.go) and all pre-existing
`session_runner` / `tool_executor` tests pass unchanged: the single-call path
now goes through the same preflight, and the load_skill assistant emission
order (trust prompt → call echo → body) is preserved (D17).

Verification of acceptance criteria:

1. `Test_applyToolCallBudget` passes for nil and positive limits, with a new
   0 = unlimited case, driving `PreflightToolCallBudget` single-call slices
   (tool_executor_budget_test.go, R9-02).
2. `Test_stoploss_CheckContextBudget` covers crossing-once, non-crossing,
   nil/all-zero fallbacks (TotalTokens, InputTokenCounter), single notice,
   and the default handover message for an empty configured message.
3. `Test_sessionRunner_Run_StoplossCrossingInjectsHandoverAfterToolResults`:
   handover injected after the tool results; the follow-up step sees it in the
   chat; the run ends with the summary as FinalAssistantText.
4. `Test_sessionRunner_Run_PostHandoverToolRefusedBeforeInvocation`: tool
   result carries the refusal warning, tool side effect never runs, summary
   follows, nil error.
5. `Test_sessionRunner_Run_PostHandoverPersistenceEndsCleanly`: io.EOF ends
   Run nil with a valid assistant/tool transcript (12 messages, all paired).
6. `go test ./internal/text/ -timeout=60s` ✓ (before and after).

### Fix round (2026-08-04, worker session 8)

The reopened phase was fixed per review 9; the deltas amend the notes above:

- `tool_executor.go`: the grouped up-front assistant emission is replaced by
  segment-based emission. `ExecuteBatch` walks the preflighted plans and
  splits at each `load_skill` call: consecutive non-skill calls share one
  grouped assistant turn (the single-type replay grouping is unchanged), and
  each `load_skill` runs as its own assistant→tool pair in the model's
  emission order. This keeps immediate pairing for mixed batches (R9-01).
- `stoploss.go`: `ApplyToolCallBudget` is deleted; `ladderText` +
  `preflightToolCall` are the single ladder implementation (R9-02).
  `newStoploss` resolves the effective handover message once via
  `q.stoploss.HandoverInstructions()`; `CheckContextBudget` injects the
  stored message and the private `handoverInstructions()` method is gone
  (R9-03).
- `tool_executor_budget_test.go`: `Test_applyToolCallBudget` drives
  `PreflightToolCallBudget` with single-call slices (nil / positive /
  0 = unlimited / escalation to hardStop); io.EOF propagation stays pinned
  by `Test_sessionRunner_Run_PostHandoverPersistenceEndsCleanly`.
- `stoploss_controller_test.go`: the CheckContextBudget rows construct the
  controller through `newStoploss`; the dead `ApplyToolCallBudget` test is
  removed. `stoploss_runner_test.go` gains the R9-01 mixed-batch pairing
  tests. `architecture/query.md` describes the segment emission.

## Review findings (review 2, 2026-08-04)

- [x] **R2-01 — High:** Resolved. `stoploss.PreflightToolCallBudget` decides
      the complete batch side-effect-free before any tool runs; `runPlannedCall`
      never invokes a refused call. Tests instrument ordinary tools
      (`Test_sessionRunner_Run_PostHandoverToolRefusedBeforeInvocation`),
      load_skill (`...LoadSkillRefusedBeforeLoading`, with a pre-handover
      positive control), and lookback tools
      (`Test_toolExecutor_PostHandoverLookbackRefusedWithoutExecution`, with a
      pre-handover control).

- [x] **R2-02 — High:** Resolved. `ExecuteBatch` emits the grouped assistant
      tool-calls and one tool result per call BEFORE returning io.EOF;
      `Test_sessionRunner_Run_PostHandoverPersistenceEndsCleanly` asserts the
      12-message transcript has no dangling assistant call, and
      `assertValidToolExchanges` checks every assistant tool-call has a tool
      result.

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

## Review findings (review 9, 2026-08-04)

Holistic review of the finished branch (all phases were Complete). Phase 3's
contract is **not fully met**: one High and one Medium finding reopen the
phase; one Low finding is non-blocking. Commands re-run and the overall
verdict are in the README's Review 9 entry.

- [x] **R9-01 — High:** Resolved (fix round, 2026-08-04, worker session 8).
      `ExecuteBatch` now splits the batch into segments at each `load_skill`
      call and processes them in the model's emission order: consecutive
      non-skill calls keep the grouped assistant emission, and each
      `load_skill` runs as its own assistant→tool pair (`tool_executor.go`).
      Consecutive non-skill calls still share one assistant turn, so the
      single-type replay invariant is untouched. New tests pin both orderings
      (`[load_skill, cmd]`, `[cmd, load_skill]`), the three-call split
      (`[cmd, load_skill, cmd]`), and the refused post-handover paths
      (`[cmd, load_skill]` and `[load_skill, cmd]`), all asserting
      `assertValidToolExchanges` and result order.

- [x] **R9-02 — Medium:** Resolved (fix round, 2026-08-04, worker session 8).
      `stoploss.ApplyToolCallBudget` is deleted; `ladderText` + `preflightToolCall`
      are the single ladder implementation. `Test_applyToolCallBudget` now
      drives `PreflightToolCallBudget` with single-call slices (nil / positive /
      0 = unlimited / escalation to hardStop); the io.EOF propagation from a
      hardStop plan stays pinned by the runner suite
      (`Test_sessionRunner_Run_PostHandoverPersistenceEndsCleanly`). The
      handover-forces-over-budget row moved into the preflight handover
      sub-test, which already covered it.

- [x] **R9-03 — Low:** Resolved (fix round, 2026-08-04, worker session 8).
      `newStoploss` resolves the effective message once via
      `q.stoploss.HandoverInstructions()` (nil-safe) and stores it in
      `maxTokensHandoverMsg`; the private `handoverInstructions()` method is
      deleted and `CheckContextBudget` injects the stored message directly.
      One resolution site (D15).

Verified good: the metric/fallback branches of `CheckContextBudget` match
D1/R1-01 on every path; refusal-before-invocation holds for ordinary,
`load_skill`, and lookback tools (R2-01/R3-02/R3-04) with positive controls;
the io.EOF path emits the final tool result before the clean return for
single-type batches (R2-02); the ladder strings match the original byte for
byte incl. the trailing space (R8-02); 0 = unlimited (D5) and post-handover
budget 0 (D2) hold; the flag layer (R5-02 resolver, R3-03 error return,
explicit-0 disable, message preservation) is correct; `printHelp` 15/15
format args.

## Review findings (review 11, 2026-08-05)

Holistic review of the finished branch (runbook step 4; all phases were
Complete). One Medium finding reopens this phase; the exact gates and the
full verdict are in the README's Review 11 entry. The phase's own acceptance
criteria pass, but the Strategy invariant "emit an assistant tool-call and a
tool result for every call, including the final io.EOF refusal" (README,
Strategy, Budget decisions) is violated on one batch shape.

- [x] **R11-01 — Medium:** Resolved (fix round, 2026-08-05, worker session
      10). `ExecuteBatch` defers `io.EOF` until every plan of the batch has
      emitted its tool result (D26): a `pendingEOF` flag is set at each
      hardStop plan and returned only after the whole batch is emitted, so a
      mid-batch hardStop no longer skips the remaining plans. New
      `Test_toolExecutor_ExecuteBatch_HardStopMidBatchEmitsAllResults` pins
      the shape (post-handover batch of 6 calls → assistant tool-calls + 6
      ladder results, then `io.EOF`); both `assertValidToolExchanges`
      helpers now count one tool result per declared call, so the old
      defective transcript (5 declared, 4 results) would fail every
      exchange assertion.

Verified good in this review (phase-local): the preflight ladder is
side-effect-free and atomic over the whole batch (R3-02); refused
ordinary/load_skill/lookback calls never reach their implementation
(R2-01/R3-04, positive controls); the io.EOF path emits the final refusal
result before the clean return for single-call steps and for batches where
the hardStop plan is last (R2-02, as tested); the segment emission keeps
immediate assistant→tool pairing in the model's emission order (R9-01);
the ladder text is byte-identical to the pre-worklog ladder (R8-02).

## Review findings (review 12, 2026-08-05)

None. Independently traced every budget, handover, refusal, load_skill, and
mid-batch hard-stop branch. Verified metric fallback order, single injection
after tool results, refusal before side effects, and one result per declared
call. The phase remains Complete.
