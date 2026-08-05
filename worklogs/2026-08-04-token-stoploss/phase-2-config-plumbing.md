# Phase 2 — Config plumbing

**Status:** Complete
**Back to:** [README](./README.md)

## Goal

Add the `stoploss` nested config and the `max-tool-calls` 0=unlimited
semantics, and expose them through `pkg/agent`. The `token-warn-limit`
sunset is its own phase (Phase 1) and is not touched here.

## Specification

### `text.Stoploss`

New struct in `internal/text/conf.go`:

```go
// Stoploss is the token stoploss policy. MaxTokens <= 0 disables the
// stoploss. MaxTokensHandoverMsg is the user message injected into the chat
// when the limit is crossed; empty means the default message (DefaultHandoverInstructions).
type Stoploss struct {
	MaxTokens              int    `json:"max-tokens"`
	MaxTokensHandoverMsg   string `json:"max-tokens-handover-instructions"`
}
```

New constant in `internal/text/conf.go` (or `querier.go`):

```go
const DefaultHandoverInstructions = "You are approaching the context window limit. Summarize your work and prepare for handover."
```

`text.Configurations` gains:

```go
Stoploss *Stoploss `json:"stoploss,omitempty"`
```

Effective semantics: stoploss active iff `Configurations.Stoploss != nil &&
Stoploss.MaxTokens > 0`. The injected message is
`Stoploss.MaxTokensHandoverMsg` when non-empty, else `DefaultHandoverInstructions`.

### `max-tool-calls` 0 = unlimited

`Configurations.MaxToolCalls *int` stays `json:"max-tool-calls,omitempty"`.
Semantics change (no struct change): nil OR `<= 0` means no limit. This is
enforced in Phase 3 (`applyToolCallBudget`); Phase 2 only documents it and
fixes any test that asserted the old 0 = refuse-all behavior. No such test
exists today (`tool_executor_budget_test.go` and `querier_tool_test.go` use
1 and 3).

### `pkg/agent`

- New public struct and option:

```go
type Stoploss struct {
	MaxTokens            int
	MaxTokensHandoverMsg string
}

func WithStoploss(s Stoploss) Option
```

- `asInternalConfig()` maps it to `text.Configurations.Stoploss`
  (`&text.Stoploss{MaxTokens: s.MaxTokens, MaxTokensHandoverMsg: s.MaxTokensHandoverMsg}`)
  when `MaxTokens > 0` (a zero-value `Stoploss` must not create a non-nil
  pointer — the agent default must stay unlimited).
- `WithMaxToolCalls` stays unchanged; its doc comment gains "0 means no
  limit".
- `defaultConf` gains nothing (stoploss off by default).

### Wiring

- `internal/text/querier_setup.go` `NewQuerier`: copy
  `querier.stoploss = userConf.Stoploss` (the controller consumes it in
  Phase 3; Phase 2 only carries the field).

### No migration

Old `textConfig.json` files lacking `stoploss` unmarshal without error
(encoding/json leaves the new pointer field nil). Regenerated configs (via
`clai setup` / `utils.CreateFile`) gain the key only when the user sets it.
Legacy handling of the dead `token-warn-limit` key is Phase 1's contract.

## Integration contract

| Input / trigger                                    | Collaborators / fakes                     | Externally observable result                                                                                          | Required side effects                                     | Prohibited side effects                          |
| -------------------------------------------------- | ----------------------------------------- | --------------------------------------------------------------------------------------------------------------------- | -------------------------------------------------------- | ------------------------------------------------ |
| `textConfig.json` with `"stoploss": {"max-tokens": 100, "max-tokens-handover-instructions": "wrap up"}` | `utils.LoadConfigFromFile` → `text.Default` | `Configurations.Stoploss` non-nil; `MaxTokens==100`; `MaxTokensHandoverMsg=="wrap up"`                                  | none                                                     | none                                             |
| `textConfig.json` with `"stoploss": {"max-tokens": 0}` | same                                      | `Stoploss.MaxTokens == 0`; effective stoploss disabled (Phase 3 asserts no injection)                                  | none                                                     | none                                             |
| `agent.New(WithStoploss(Stoploss{MaxTokens: 50, MaxTokensHandoverMsg: "m"}))` | `pkg/agent` → `asInternalConfig`          | `text.Configurations.Stoploss` carries both values                                                                     | none                                                     | zero-value `Stoploss` must not enable the limit  |
| `agent.New(WithMaxToolCalls(0))`                   | same                                      | `Configurations.MaxToolCalls` non-nil pointing at 0; Phase 3 treats it as unlimited                                    | none                                                     | none                                             |

## Acceptance criteria

1. `Configurations.Stoploss` marshals to the nested `stoploss` object with
   keys `max-tokens` and `max-tokens-handover-instructions`; absent pointer
   marshals to nothing (`omitempty`).
2. `DefaultHandoverInstructions` is exported and used when the configured
   message is empty.
3. `pkg/agent` exposes `WithStoploss` and keeps `WithMaxToolCalls`; a
   zero-value `WithStoploss(Stoploss{})` produces a nil internal pointer.
4. Old configs lacking `stoploss` load cleanly (unit test).

## Error coverage

| Failure condition                                  | Expected error / recovery / external outcome                              | Test                                       |
| -------------------------------------------------- | ------------------------------------------------------------------------ | ------------------------------------------ |
| Malformed `textConfig.json`                         | Existing `LoadConfigFromFile` error path, unchanged                       | existing config tests                       |
| `stoploss` value is not an object (e.g. a number)   | `json.Unmarshal` type error, propagated by `LoadConfigFromFile`           | new unit test                              |
| Agent given `WithStoploss(Stoploss{})`              | No limit configured (nil pointer in internal config)                      | new agent test                             |

## Implementation notes

Executed 2026-08-04 (imago + clai, worker session 2).

Added:

- `internal/text/conf.go`: `Stoploss` struct (`max-tokens`,
  `max-tokens-handover-instructions` JSON keys), the exported
  `DefaultHandoverInstructions` const, and `Stoploss.HandoverInstructions()`
  — the effective-message seam (configured message when non-empty, else the
  default; nil receiver returns the default). The method pins acceptance
  criterion 2 now and is the seam the Phase 3 controller calls.
- `internal/text/conf.go`: `Configurations.Stoploss *Stoploss
  json:"stoploss,omitempty"` placed next to `MaxToolCalls` (both are run
  budgets). `Default` gains nothing: stoploss is off by default, so
  `setNonZeroValueFields` never regenerates the key into old configs.
- `internal/text/querier.go`: `Querier.stoploss *Stoploss` (unexported),
  carried from the user config for the Phase 3 controller.
- `internal/text/querier_setup.go`: `querier.stoploss = userConf.Stoploss`
  next to the `maxToolCalls` copy.
- `pkg/agent/agent.go`: public `agent.Stoploss` struct, `WithStoploss`
  option, `Agent.stoploss Stoploss` field, and the `asInternalConfig`
  mapping. The internal pointer is created only when `MaxTokens > 0`, so a
  zero-value `WithStoploss(Stoploss{})` leaves the agent unlimited.
  `WithMaxToolCalls` gains a doc comment: 0 means no limit.

No enforcement change: `applyToolCallBudget` keeps today's behavior
(0 = refuse all) until Phase 3 flips it to 0 = unlimited (D5). No test
asserted the old 0 = refuse-all behavior (verified: `tool_executor_budget_test.go`
and `querier_tool_test.go` use 1 and 3).

Tests (written first, red against the pre-phase code):

- `internal/text/stoploss_test.go` (new): nested-object load from
  `textConfig.json` via `utils.LoadConfigFromFile`; `max-tokens: 0` loads
  (disabled semantics); absent `stoploss` key loads with nil pointer
  (acceptance criterion 4); non-object `stoploss` value propagates the
  `json.Unmarshal` type error; `omitempty` marshaling (present → nested
  object with both keys, absent → nothing); `HandoverInstructions` fallback
  (configured / default / nil receiver); `NewQuerier` carries `stoploss` onto
  the querier.
- `pkg/agent/agent_test.go`: `TestAgent_WithStoploss` (both values reach the
  internal config), `TestAgent_WithStoploss_ZeroValueDisabled` (nil internal
  pointer), `TestAgent_WithMaxToolCalls_Zero` (non-nil pointer at 0, Phase 3
  treats it as unlimited).

Gates (all before and after the change):

- Before: `go test ./internal/text/ ./pkg/agent/ ./pkg/text/ -timeout=120s` ✓
- After: `go test ./internal/text/ ./pkg/agent/ ./pkg/text/ -timeout=120s` ✓;
  `go build ./...` ✓; `go vet ./...` ✓;
  `go run mvdan.cc/gofumpt@latest -w -l .` ✓ (no diffs);
  `go run honnef.co/go/tools/cmd/staticcheck@latest ./...` ✓;
  `go fix ./...` ✓; `go test ./... -race -cover -count=3 -timeout=30s` ✓;
  dupl baseline unchanged (see Phase 7).

Verification of acceptance criteria:

1. Marshal round-trip: present `Stoploss` → `"stoploss":{"max-tokens":200000,
   "max-tokens-handover-instructions":"wrap up"}`; absent → no key
   (`TestConfigurations_StoplossMarshalsNestedObject`).
2. `DefaultHandoverInstructions` exported and applied for empty configured
   messages (`TestStoploss_HandoverInstructions`).
3. `WithStoploss` exposed; zero-value option produces a nil internal pointer
   (`TestAgent_WithStoploss_ZeroValueDisabled`).
4. Old configs lacking `stoploss` load cleanly
   (`TestConfigurations_StoplossAbsentLoadsCleanly`).

## Review findings

None yet.

## Review findings (review 9, 2026-08-04)

- [x] **R9-03 — Low (cross-reference):** D15 promised
      `Stoploss.HandoverInstructions()` as “the seam the Phase 3 controller
      calls”; the Phase 3 controller instead re-implements the
      configured-or-default fallback (`internal/text/stoploss.go:186`), so
      the exported method has no production caller. The fix is tracked in
      Phase 3's Review 9 findings (resolve once at construction) — resolved
      in the fix round: `newStoploss` resolves the message once via
      `HandoverInstructions()` (R9-03, worker session 8). Phase 2's own
      acceptance criteria and tests remain satisfied.

## Review findings (review 12, 2026-08-05)

None. Verified nested stoploss marshaling, default-message resolution, agent
zero-value disabling, and querier configuration plumbing.
