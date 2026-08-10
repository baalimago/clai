# Phase 8 — Post-handover tool budget

**Status:** Complete
**Back to:** [README](./README.md)

## Goal

Stop cutting tooling off after the stoploss handover fires: the agent keeps
tool access during the wrap-up phase so it can summarize context into a file,
with an optional configurable bound on that phase.

## Specification

### Config

`Stoploss` (`internal/text/conf.go`) gains:

```go
MaxToolCallsAfterHandover int `json:"max-tool-calls-after-handover" migrate:"true"`
```

Semantics: absent or 0 = **unlimited** post-handover tool calls (the new
default); a positive value bounds the wrap-up phase. The key is surfaced by
the config migration: `migrate:"true"` fills it even at its zero default, so
pre-existing configs gain `"max-tool-calls-after-handover": 0` once with the
standard upgrade announcement (the field drops `omitempty` so the zero
survives the rewrite). 0 in the file means the same as absent: unlimited.

### Controller (`internal/text/stoploss.go`)

The handover no longer zeroes the tool budget. The controller becomes
phase-aware:

```go
// effectiveBudget: post-handover -> maxToolCallsAfterHandover (<= 0 = -1);
// pre-handover -> maxToolCalls (nil or <= 0 = -1).
// effectiveUsed: post-handover -> session.PostHandoverToolCallsUsed;
// pre-handover -> session.ToolCallsUsed.
```

`PreflightToolCallBudget` uses `effectiveUsed`/`effectiveBudget` and reserves
slots via `incUsed`, which increments the phase-appropriate session counter.
The wrap-up allowance is **fresh**: pre-handover consumption never eats into
it. `load_skill` keeps its pre-handover exemption (allowed even when the
pre-handover budget is exhausted, no slot reserved); post-handover it is
allowed while the wrap-up allowance has room (no slot) and refused once the
allowance is exhausted, like every other tool.

### Session state (`internal/text/session.go`)

`QuerySession` gains:

```go
// PostHandoverToolCallsUsed counts tool calls made after the handover fired.
PostHandoverToolCallsUsed int
```

`HandoverRequested` now switches the budget phase instead of forcing refusal.

### CLI flag (`internal/setup_flags.go`)

`-max-tool-calls-after-handover` (long form only) overrides
`stoploss.max-tool-calls-after-handover` for the run; explicit `0` disables a
file budget. `applyFlagOverridesForText` creates the `Stoploss` object when
needed, mirroring `-max-tokens`.

### Agent API (`pkg/agent/agent.go`)

`agent.Stoploss` gains `MaxToolCallsAfterHandover`; `asInternalConfig` maps it
onto the internal `Stoploss` when `MaxTokens > 0` (a stoploss with
`max-tokens <= 0` is disabled, so a wrap-up budget without a stoploss is
meaningless).

## Integration contract

| Input | Collaborators/fakes | Externally observable result | Required side effects | Prohibited side effects |
| --- | --- | --- | --- | --- |
| Handover fires, no `max-tool-calls-after-handover` | `sessionRunner` + counting probe tool | Post-handover tool call EXECUTES; tool result carries real output; run ends with the summary, exit 0 | `PostHandoverToolCallsUsed` stays 0 (unlimited) | Refusal text in post-handover results |
| Handover fires, `max-tool-calls-after-handover: 1` | same | First post-handover call executes with remaining-count prefix; second is refused with ladder text before invocation; run exits 0 | Ladder slots reserved on `PostHandoverToolCallsUsed` | Pre-handover `ToolCallsUsed` mutation |
| Pre-handover budget exhausted, then handover | same + `max-tool-calls` | Wrap-up allowance is fresh: post-handover calls still execute | Independent counters | Budget carry-over |
| Handover fires, budget exhausted, `load_skill` | counting skill loader | `load_skill` refused before loading | Refusal pair in transcript | Loader invocation |
| Handover fires, budget has room, `load_skill` | counting skill loader | `load_skill` loads (no slot reserved) | Loader invocation | Slot reservation |

## Acceptance criteria

1. Default config: post-handover tool calls execute; the refusal ladder never
   fires in the wrap-up phase. Proven by
   `Test_sessionRunner_Run_PostHandoverToolExecutesByDefault`,
   `Test_e2e_stoploss_post_handover_tools_execute_by_default`,
   `Test_toolExecutor_ExecuteBatch_PostHandoverBatchExecutesByDefault`, and the
   `default (no post-handover budget) allows every call` controller case.
2. A configured positive budget allows exactly N post-handover calls, then the
   ladder escalates to `io.EOF`. Proven by
   `Test_sessionRunner_Run_PostHandoverBudgetExhaustionEndsCleanly`,
   `Test_toolExecutor_ExecuteBatch_PostHandoverBatchRefusesAfterBudget`, the
   `post-handover budget escalates the ladder when exhausted` controller case,
   and `Test_e2e_stoploss_post_handover_budget_refuses_after_budget`.
3. The wrap-up allowance is fresh. Proven by the
   `post-handover budget is a fresh allowance` controller case.
4. `load_skill` loads post-handover by default and is refused only when the
   allowance is exhausted. Proven by
   `Test_sessionRunner_Run_PostHandoverLoadSkillLoadsByDefault` /
   `...RefusedWhenBudgetExhausted` and the controller's load_skill cases.
5. Pre-handover `max-tool-calls` behavior is byte-identical (including the
   `load_skill` exemption). Proven by the unchanged pre-handover controller and
   runner cases plus `pre-handover load_skill exemption survives budget
   exhaustion`.
6. The new key loads, marshals, and migrates without churn; the flag parses and
   overrides; the agent API maps it. Proven by
   `TestConfigurations_StoplossMaxToolCallsAfterHandoverLoads` /
   `...AbsentStaysZero` / `...MarshalsPostHandoverBudget`,
   `Test_applyFlagOverridesForText_Stoploss` rows, `parseFlags` rows, and
   `TestAgent_WithStoploss`.

## Error coverage

| Failure condition | Expected outcome | Test |
| --- | --- | --- |
| Post-handover budget exhausted, agent persists | Refusal ladder escalates; past the final warning `io.EOF` ends the run cleanly; every declared call has a result | `Test_sessionRunner_Run_PostHandoverBudgetExhaustionEndsCleanly`, `Test_toolExecutor_ExecuteBatch_HardStopMidBatchEmitsAllResults` |
| Non-integer flag value | `parseFlags` returns `invalid value` | `parseFlags` row |
| `max-tool-calls-after-handover` absent from config | Migration fills it as the explicit 0 (unlimited) once; file rewritten; announcement lists `stoploss.max-tool-calls-after-handover`; second load silent | `TestConfigurations_StoplossMaxToolCallsAfterHandoverAbsentGetsMaterialized` |

## Implementation notes

2026-08-09, clai session: implemented after the design interview (Q1: option
C). Key findings:

- The old post-handover refusal tests were rewritten to the new semantics
  rather than deleted: refusal-pairing and mid-batch hardStop coverage now
  seeds `PostHandoverToolCallsUsed` and a budget of 1 to reach the exhausted
  state, so the R9-01 pairing and R11-01 deferred-EOF invariants stay pinned.
- The marshal assertion needed the empty
  `max-tokens-handover-instructions` field between `max-tokens` and the new
  key (field order is struct order).
- `main.go` usage is a `fmt.Printf` template; the new line added one `%v`
  verb, so `internal/setup.go printHelp` gained one matching `cfgDir`
  argument.
- No setup-wizard change: `editMap` edits whatever keys exist in the file's
  `stoploss` object, so the new key is editable once present.
- Verification: `go test ./... -count=1` green (all packages);
  `go vet ./...` clean.
