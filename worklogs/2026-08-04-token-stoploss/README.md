# Token Stoploss

## Status board

| #   | Phase                                                           | Status      | Summary                                                                                                    |
| --- | --------------------------------------------------------------- | ----------- | ---------------------------------------------------------------------------------------------------------- |
| 1   | [token-warn-limit sunset](./phase-1-token-warn-limit-sunset.md) | Not Started | Remove `TokenWarnLimit` from config, querier, and session runner; old configs still load                   |
| 2   | [Config plumbing](./phase-2-config-plumbing.md)                 | Not Started | `stoploss` nested config, `max-tool-calls` 0=unlimited, agent API                                          |
| 3   | [Stoploss controller](./phase-3-stoploss-controller.md)         | Not Started | Runtime enforcement: per-step token check, handover message injection, post-handover tool refusal ladder   |
| 4   | [CLI flags](./phase-4-cli-flags.md)                             | Not Started | `-max-tokens` / `-max-tool-calls` flags; instructions are NOT a flag                                       |
| 5   | [Setup wizard](./phase-5-setup-wizard.md)                       | Not Started | Verify + pin interactive editing of the `stoploss` object (existing `editMap`); polish only if gaps appear |
| 6   | [Integration & e2e](./phase-6-integration-e2e.md)               | Not Started | Mock-vendor e2e for handover flow + refusal ladder, legacy config compat, docs                             |
| 7   | [Quality gates](./phase-7-quality-gates.md)                     | Not Started | gofmt/gofumpt, staticcheck, vet, build, `make qa`, dupl baseline re-run                                    |

Phase order: Phase 1 (token-warn-limit sunset) is independent and runs first
— it removes the dead pre-query machinery before any new config lands.
Phases 2–5 build the feature; Phase 3 depends on Phase 2 (config fields and
semantics), Phases 4 and 5 depend on Phase 2 and may run in parallel with
Phase 3; Phase 6 depends on Phases 2–5 and runs before Phase 7; Phase 7 is
the final quality-gate sweep and always runs last.

## Motivation

An agentic clai run (tool-using loop in `internal/text/session_runner.go`) can
keep going until the vendor rejects the request: the context grows with every
tool result and every reply, so a runaway agent burns money and eventually
fails hard when the context window overflows. There is a tool-call budget
(`max-tool-calls`), but no equivalent guard on context consumption.

The requested feature: a configurable token stoploss. When the run's context
usage approaches the configured limit, clai injects an automatic user message
that tells the agent to summarize its work and prepare for handover. The agent
then produces a final summary and the run ends. The injected message text is
configurable in `textConfig.json`.

The pre-query interactive `token-warn-limit` prompt (ask `[yY]` before sending
an oversized first request) is sunset: the stoploss makes it redundant, huge
queries are blocked at the inference-provider level anyway, and the blocking
prompt is hostile to embedded/automated runs.

## Strategy

**Metric = latest request footprint.** The check runs after each completed model
step. When the vendor reports usage, the measured value is
`prompt_tokens + completion_tokens`; if both are zero, it uses `total_tokens`.
This is the latest request's context-plus-output footprint, not cumulative
spend. It is not guaranteed to be monotonic because completion size can vary.
A nil or all-zero usage value is treated as unavailable. The controller
then calls the model's `InputTokenCounter` (`CountInputTokens`) to estimate the
current chat. When neither source is available, the check is skipped for that
step and the limitation is documented. The old `countTokens()` whitespace
heuristic on the querier is NOT resurrected — it dies with `token-warn-limit`;
the generic/vendor `InputTokenCounter` implementations already provide the
heuristic where needed.

**One nested config object named `stoploss`.** Both new settings live in one
object so the whole stoploss policy is visible and editable as a unit:

```json
"stoploss": {
  "max-tokens": 200000,
  "max-tokens-handover-instructions": "You are approaching the context window limit. Summarize your work and prepare for handover."
}
```

`max-tokens: 0` or omitted = disabled. Empty instructions = default message
(above). `max-tool-calls` stays a flat top-level key with its existing
behavior and warning ladder; it is NOT absorbed into `stoploss` (the tool
message system stays as is).

**`max-tool-calls` semantics change: 0 = unlimited.** Today `max-tool-calls:
0` refuses every tool call (first call trips `ToolCallsUsed >= 0`). New
semantics: nil or 0 means no limit. This also makes `-max-tool-calls=0` a
sensible "disable the limit for this run" override. The escalation ladder
(prefix remaining → "No more tool calls allowed" → HARD SHUT DOWN → LAST
WARNING → `io.EOF`) is unchanged.

**Unified `stoploss` controller in code.** Both budgets are enforced by one
controller so they compose. After handover, every subsequent tool call,
including `load_skill` and lookback tools, enters the existing refusal ladder
before the tool's side effect runs. The call and refusal remain a valid
assistant/tool exchange. Persisting past the final warning hard-stops the run
with `io.EOF` (clean end, `sessionRunner.Run` returns nil). The refusal ladder
is not removed or weakened.

**Handover injection ordering.** The handover message is appended to the chat
AFTER the crossing step's tool batch executes, so the chat keeps valid
ordering: `[assistant tool-call] → [tool results] → [handover user msg]`.
Injection only ever happens on a tool-call step (the loop only continues when
there are tool calls); a step that ends with a plain reply ends the run
without injection — there is nothing to hand over. The agent gets exactly one
unconstrained post-handover step. If that step, or a later refusal-ladder
follow-up step, ends with tool calls, every call is refused before invocation.
The refusal ladder may produce the HARD SHUT DOWN and LAST WARNING follow-up
steps; after persistence past the final warning, the run ends with `io.EOF`.

**Preflight before side effects.** A post-handover refusal must be decided
before any tool implementation, skill loader, lookback reader, or command
runner is invoked. The refusal path must still append the assistant tool-call
and a tool-result message (including the ladder text), so every refused call
remains a valid exchange. The controller therefore needs a preflight/reservation
operation (or equivalent decision object); applying the ladder only to the
output returned by an already-invoked tool is not sufficient.

**Config scope.** `textConfig.json` + CLI flags (`-max-tokens`,
`-max-tool-calls`) + `pkg/agent` options (`WithStoploss`,
`WithMaxToolCalls`). No profile key, no setup-wizard-only surface. The
instructions message is configurable in `textConfig.json` and via
`WithStoploss`, but is NOT a CLI flag.

**Sunset `token-warn-limit` (Phase 1).** Remove `TokenWarnLimit` from
`text.Configurations` and `text.Default`, `tokenLengthWarning()`,
`countTokens()`, `TokenCountFactor`, the `querier.tokenWarnLimit` field and
its `querier_setup.go` wiring, the `TokenWarnLimit: 300000` line in
`pkg/text/full.go`, and the call site in `session_runner.go`. Old configs that
still carry the `token-warn-limit` key unmarshal cleanly (encoding/json
ignores unknown keys) — no migration needed.

**Setup wizard needs no new editing machinery.** `internal/setup` already
routes `map[string]any` values through `editMap` (add/update/remove keys,
recursive `handleValue`, `castPrimitive` int/float/bool/string casting), so
selecting `stoploss` in the interactive reconfigure flow already opens an
object editor. Phase 5 pins this with tests and adds small polish only if the
tests reveal a gap.

**Budget decisions must be made before side effects.** The controller's
preflight operation must cover a complete model tool batch, not only individual
calls. This applies to both the positive `max-tool-calls` budget and the
post-handover refusal budget. If a batch contains one allowed call followed by
a refused call, the executor must not invoke any call until it knows how every
call will be represented in the transcript. It must then emit an assistant
tool-call and a tool result for every call, including the final `io.EOF`
refusal. A refused call has no tool output; its result contains the ladder text.

## Decisions

| #   | Decision                                                                                                                                      | Rationale                                                                                                                                                                                    |
| --- | --------------------------------------------------------------------------------------------------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| D1  | Metric = current context size: last step's `prompt_tokens + completion_tokens` (fallback `total_tokens`; then `InputTokenCounter`; else skip) | Maps 1:1 to "approaching the context window limit"; vendor usage is already collected per step (user Q1: A)                                                                                  |
| D2  | Post-handover tool calls run the existing refusal ladder (prefix → warn → HARD SHUT DOWN → LAST WARNING → io.EOF)                             | Tried and tested behavior; transcript stays consistent; the agent still has the handover message in context to produce the summary (user Q2: B)                                              |
| D3  | `token-warn-limit` fully sunset; no pre-send check, no interactive prompt                                                                     | Redundant with the stoploss; providers block oversized requests; the blocking prompt breaks embedded runs (user Q3: Option 3)                                                                |
| D4  | Nested config object `stoploss` = `{ max-tokens, max-tokens-handover-instructions }`; `max-tool-calls` stays flat                             | Whole stoploss policy editable as a unit; tool message system untouched (user Q4/Q5 revision)                                                                                                |
| D5  | `max-tool-calls` semantics: nil OR 0 = unlimited (was: 0 = refuse all)                                                                        | Makes 0 a meaningful "disabled" value and a valid flag override; nobody sanely configures 0 to mean "no tools" (user decision)                                                               |
| D6  | CLI flags `-max-tokens` and `-max-tool-calls`; instructions are NOT a flag                                                                    | Per-run stoploss override for operators; the message is policy text, not a run knob (user decision)                                                                                          |
| D7  | Agent API: `WithStoploss(Stoploss)` (new, public struct in `pkg/agent`); `WithMaxToolCalls` stays                                             | Mirrors the nested config; keeps the existing tool API for compatibility                                                                                                                     |
| D8  | Scope = textConfig + flags + agent API; no profile key                                                                                        | Parity with `max-tool-calls` (textConfig + agent only today); profiles gain nothing for an operational guard                                                                                 |
| D9  | Default handover message: "You are approaching the context window limit. Summarize your work and prepare for handover."                       | Plain, imperative, STE-compliant; empty configured message falls back to it                                                                                                                  |
| D10 | No config migration needed for `token-warn-limit` removal                                                                                     | encoding/json ignores unknown keys; regenerated configs simply omit the key                                                                                                                  |
| D11 | Handover message appended AFTER the crossing step's tool batch; injection only on tool-call steps                                             | Chat ordering stays valid (`assistant` → `tool` → `user`); a run that finishes on its own has nothing to hand over                                                                           |
| D12 | Fallback estimation uses `InputTokenCounter` (already implemented by `generic.StreamCompleter` and vendors); no local estimator               | `countTokens` dies with the sunset; the interface already exists (`internal/models/models.go`) and is exercised by rate-limit backoff                                                        |
| D13 | Setup wizard phase = verify + pin tests on existing `editMap`; polish only on revealed gaps                                                   | Discovery: `handleValue` already dispatches `map[string]any` to `editMap` (`internal/setup/setup_actions.go`); no new editor machinery anticipated (user allowed an upgrade phase if needed) |

## Rejected alternatives

| Idea                                                               | Reason rejected                                                                                                                     |
| ------------------------------------------------------------------ | ----------------------------------------------------------------------------------------------------------------------------------- |
| Cumulative processed tokens (sum over all steps)                   | Trips far earlier than the window fills (every step re-sends history); that is a cost budget, not a context-window stoploss (Q1: B) |
| Whitespace heuristic as the primary metric                         | Imprecise; vendor usage is available for all major vendors; heuristic is only a fallback (Q1: C)                                    |
| End the run immediately when a post-handover step calls tools      | Leaves a dangling assistant tool-call message and can cut off the summary; the refusal ladder is strictly better (Q2: A)            |
| Allow post-handover tool calls                                     | Weakens the stoploss to a suggestion (Q2: C)                                                                                        |
| Keep an interactive pre-send y/N prompt (re-based on `max-tokens`) | Blocks embedded/automated runs; providers already reject oversized requests (Q3)                                                    |
| Absorb `max-tool-calls` into the `stoploss` object                 | Requires migrating an existing flat key; the user chose to keep the tool message system as is (Q5: A)                               |
| No CLI flags; textConfig + agent API only                          | Operators need a per-run override without editing config files (user decision, Q6 revision)                                         |
| Profile key for the stoploss                                       | No use case; parity with `max-tool-calls` which has no profile key (D8)                                                             |

## Out of scope

- Pre-send token warnings / cost confirmation prompts (sunset)
- Per-chat or per-directory stoploss limits
- Absorbing `max-tool-calls` into the `stoploss` object
- Profile-level stoploss configuration
- Vendor-specific context-window auto-detection (the limit is user-configured)

## Session journal

### 2026-08-04 — Planning (imago + clai)

Design interview concluded: metric = current context size (Q1: A),
post-handover tool refusal via the existing ladder (Q2: B), full
`token-warn-limit` sunset with no pre-send check (Q3: Option 3), nested
`stoploss` config with `max-tool-calls` staying flat and gaining 0=unlimited
(Q4/Q5), CLI flags for both limits but not for the instructions message
(Q6 revision), unified `stoploss` controller owning both budgets. Discovery:
`internal/setup` already edits nested objects via `editMap`, so the setup
wizard phase shrinks to verify+pin. Baseline: dupl 29 clone groups;
`go build ./...` ✓; `go vet ./...` ✓. All phases Not Started; verdict: ready
at Phase 1.

### 2026-08-04 — Phase split (imago + clai)

The `token-warn-limit` sunset is split out of the config-plumbing phase into
its own phase, now Phase 1, so the dead pre-query machinery is removed first
and independently of the new `stoploss` config. The remaining phases
renumber: config plumbing 2, stoploss controller 3, CLI flags 4, setup
wizard 5, integration & e2e 6, quality gates 7. All phases Not Started;
verdict: ready at Phase 1.

### 2026-08-04 — Phase 5 macro-mode trial (imago + clai)

Feasibility trial: the Phase 5 setup-wizard flow was driven end-to-end via
clai macro mode against a sandbox config dir (`CLAI_CONFIG_DIR` + `-n`,
built binary, `go build ./...` ✓). All reachable contract rows pass: update
int/string, remove key, add to an empty `stoploss` object, invalid int stays
a string. One High finding (R7-01): the row "`stoploss` absent from file"
is unreachable in `interractiveReconfigure` — `selectFieldToEdit` has no
top-level add, and `[a]dd` needs the key present. The integration row was
amended to the reachable "present but empty (`{}`)" case in Phase 5; full
findings in Review 7.

### 2026-08-04 — Phase 6 e2e harness probe (imago + clai)

Pre-implementation probe of the mock-vendor e2e harness (built binary,
sandbox `CLAI_CONFIG_DIR`, `clai -r -cm test q ...`). Phase 6 cases 3 and 5
pass today: `max-tool-calls: 1` executes exactly one tool and refuses the
second pre-invocation with `ERROR: No more tool calls allowed`; a legacy
`token-warn-limit` config loads and runs with no prompt and no stdin block.
The probe also pins the current `max-tool-calls: 0` = refuse-all baseline
(refusal before side effects) that Phase 2 changes. No contract amendment
needed; evidence and findings in Review 8.

## Review feedback

Reviewer: imago. Scope: planning contracts vs the codebase — no implementation
exists yet (every phase Not Started), so this review amends the contracts in
place instead of auditing code. Review rounds are numbered; a later round
revisits these findings with new `R{n}-*` IDs.

### Severity taxonomy

| Severity | Meaning                                                                        |
| -------- | ------------------------------------------------------------------------------ |
| Blocker  | Contract cannot be implemented as written; execution must not start            |
| High     | Implemented literally, produces failing acceptance criteria or unsafe behavior |
| Medium   | Incorrect rationale or unaddressed edge that bites in realistic use            |
| Low      | Documentation/consistency nit; non-blocking                                    |

Reopen rule: High and Medium must be resolved before their phase can be
marked Complete; Low findings are non-blocking. With all phases Not
Started, fixes are recorded as contract amendments inside the phase files.

### Findings index

The planning review findings were resolved by the amendments below before
implementation starts.

| ID    | Severity | Resolution                                                                                             |
| ----- | -------- | ------------------------------------------------------------------------------------------------------ |
| R1-01 | High     | Nil and all-zero usage use the input-counter fallback; see Strategy and Phase 3.                       |
| R1-02 | High     | Every tool is refused after handover, including `load_skill` and lookback tools.                       |
| R1-03 | Medium   | Refusal is checked before invocation, so no post-handover side effect runs.                            |
| R1-04 | Medium   | The metric is explicitly the latest request footprint; fallback is current chat size.                  |
| R1-05 | Medium   | Equal CLI aliases are accepted; differing aliases are rejected.                                        |
| R1-06 | Medium   | Controller tests use usage independent of mock prompt text.                                            |
| R1-07 | Low      | Sunset searches exclude the worklog and intentionally retained heuristic implementations; see Phase 1. |
| R3-01 | High     | Resolved: Phase 3 now requires the optional `InputTokenCounter` assertion.                             |
| R3-02 | High     | Resolved: preflight is atomic for complete batches, including positive budgets.                        |
| R3-03 | Medium   | Resolved: alias conflicts return errors from `parseFlags`; process exit stays at the top-level caller. |
| R3-04 | High     | Resolved: all over-budget calls are refused before invocation; Phase 6 pins the safety behavior.       |
| R2-01 | High     | Define a preflight refusal API; the current output-based API cannot prevent tool side effects.         |
| R2-02 | High     | Preserve a tool result on the `io.EOF` refusal path; do not leave a dangling tool call.                |
| R2-03 | Medium   | Resolved: removed the unsupported monotonicity claim from the metric rationale and D1.                 |

### Verified good

- `toolExecutor.applyToolCallBudget` (`internal/text/tool_executor.go:177`)
  is the single enforcement point for `max-tool-calls`, with pinned tests
  (`tool_executor_budget_test.go`, `querier_tool_test.go:Test_maxToolCalls`).
- `sessionRunner.Run` (`internal/text/session_runner.go`) is the per-step loop;
  each step's `Usage` is already captured in `CompletedModelCall.Usage` and
  `stepResult.Usage` via `currentTokenUsage()` / `UsageTokenCounter`
  (`internal/models/models.go`).
- `generic.StreamCompleter.CountInputTokens` and the anthropic vendor
  implement `InputTokenCounter`; the fallback seam exists (used by rate-limit
  backoff in `session_runner.go:waitForRateLimitReset`).
- The mock vendor (`internal/vendors/mock.go`) reports per-step usage from the
  last user message (`mockUsageForPrompt`) and drives agent loops via `tool_X`
  tokens; e2e can force a crossing with a small `max-tokens` and force the
  refusal ladder with a handover message containing several `tool_X` tokens.
- `internal/setup/setup_actions.go` already edits nested objects:
  `handleValue` → `editMap` (add/update/remove, recursive, `castPrimitive`).
- Flag plumbing pattern exists: `parseFlags` + `applyFlagOverridesForText`
  (`internal/setup_flags.go`), explicit-set tracking via `fs.Visit`
  (the `-lb/-lookback` precedent, `UseLookbackSet`).
- `pkg/agent` `Option`/`asInternalConfig` pattern (`pkg/agent/agent.go`) makes
  `WithStoploss` and the 0=unlimited tool semantics trivial to wire.
- Gates baseline: `go build ./...` ✓, `go vet ./...` ✓, dupl baseline → 29
  clone groups (2026-08-04, matches the session journal).

### Review amendment (2026-08-04)

The specification was clarified before implementation. Usage with no token
fields now uses the input-counter fallback. Handover refusal covers every tool
and occurs before invocation. The metric is explicitly the latest request
footprint, with an estimated current-chat fallback. CLI alias conflict behavior
and test seams are defined in the phase contracts.

### Review 2 (2026-08-04)

Commands re-run: `go test ./...` ✓; `go vet ./...` ✓. This was a planning
review; no implementation gates were applicable. The code-path audit found that
the current executor invokes ordinary tools at `internal/text/tool_executor.go:103`,
loads skills at `:223`, and executes lookback tools through
`tool_executor_lookback.go:44` before the existing budget method at `:104`/`:44`
can inspect their output. It also returns `io.EOF` before `emitToolResult` at
`tool_executor.go:108`, which can leave the final assistant tool call without
a tool result. Phase 3 must therefore specify and test a preflight refusal and
a result-emitting EOF path. The “monotonic” description of a latest-request
footprint was also corrected below. Verdict: **not ready for implementation until
R2-01 and R2-02 are resolved in the phase contract**; R2-03 is documentation-only.

### Verdict

Ready to start at Phase 1 with the contract amendments above.

### Review 3 (2026-08-04)

Commands re-run: `go test ./...` and `go vet ./...` (both pass). This remains a
planning review; no implementation gates apply. The code audit found that
`models.StreamCompleter` only provides streaming and setup, while
`models.InputTokenCounter` is a separate optional interface. The Phase 3 API
must use that assertion rather than call `CountInputTokens` on the stream
completer. The existing executor also handles a batch by invoking calls before
applying its output budget, so “preflight” must be specified as an atomic batch
operation. Finally, the flag parser's existing `exitWithFlagError` exits the
process, which does not satisfy a unit-testable “parse returns an error” contract,
and the Phase 3 preflight requirement conflicts with Phase 6's claim that an
over-budget second tool executes zero side effects. Verdict: **not ready for
implementation until R3-01, R3-02, and R3-04 are resolved; R3-03 should be
resolved in the Phase 4 contract.**

### Review 4 (2026-08-04)

The four Review 3 findings are resolved in the phase contracts. Phase 3 now
uses `models.InputTokenCounter` as an optional interface, defines atomic
preflight across complete tool batches, and requires transcript results for
every refusal. Phase 4 requires parsing errors to return normally. Phase 6
explicitly tests that positive-budget refusals do not invoke the refused tool.
Verdict: **ready for implementation.**

### Review 5 (2026-08-04)

Commands re-run: `go test ./...` ✓; `go vet ./...` ✓. No implementation exists
yet, so this is a contract review. The phase documents contain a lifecycle
contradiction: the Strategy says the agent gets “exactly one post-handover
step”, while the refusal ladder deliberately emits a refusal result and allows
additional model steps for the HARD SHUT DOWN and LAST WARNING messages. The
ladder behavior is retained, but the “exactly one” wording must be narrowed to
one unconstrained post-handover step. The flag phase also does not specify a
testable resolver for explicit aliases; `ReturnNonDefault` alone cannot
distinguish an explicitly supplied value equal to the default. Verdict:
**not ready for implementation until R5-01 and R5-02 are resolved.**

### Review feedback

| ID    | Severity | Resolution                                                                                                                            |
| ----- | -------- | ------------------------------------------------------------------------------------------------------------------------------------- |
| R5-01 | High     | Resolved in Review 6: one unconstrained post-handover step is distinguished from refusal-ladder follow-up steps; see Phase 3.         |
| R5-02 | Medium   | Resolved in Review 6: alias visitation and value resolution are explicitly defined and tested independently of defaults; see Phase 4. |

### Review 6 (2026-08-04)

This review applied the R5 contract fixes. The Strategy now distinguishes the
single unconstrained post-handover step from the refusal-ladder follow-up
steps. Phase 4 now requires independent visitation tracking and an explicit
alias resolver, including explicit zero and values equal to defaults. The
previous baseline gates remain green (`go test ./...`, `go vet ./...`).
Verdict: **ready for implementation at Phase 1.**

### Review 7 (2026-08-04)

Commands run: `go build ./...` ✓; macro-mode trials of the Phase 5 flow
(update int, update string, remove, add-to-empty, invalid int) against a
sandbox `CLAI_CONFIG_DIR`, all exit 0 and verified on disk. Finding R7-01
(High): the contract row "`stoploss` absent from file" cannot pass —
`selectFieldToEdit` lists only existing top-level keys and offers only
`[d]one`, so `[a]dd` is unreachable when the key is absent. Resolved in
place: the row now reads "present but empty (`{}`)". R7-02 through R7-05 are
non-blocking notes (config-dependent indices, editor-fallback reachability,
missing type annotations, live-dir timing). Verdict: **ready for
implementation at Phase 1; Phase 5 contract amended, phase still Not
Started.**

### Review 8 (2026-08-04)

Commands run: `go build ./...` ✓; mock-vendor probes of the Phase 6 e2e
harness (cases 3 and 5) against a sandbox `CLAI_CONFIG_DIR`, all exit 0 and
verified on disk. Findings R8-01 through R8-03 are notes for the implementing
agent (Call-log ordering vs the budget check, pinned refusal text,
legacy-config evidence for Phase 1). Verdict: **harness proven; cases 3 and
5 pre-validated; cases 1, 2, 4, and 6 remain post-implementation; no
contract amendment required.**
