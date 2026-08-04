# Phase 2 — Config plumbing

**Status:** Not Started
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

To be written by the executing agent.

## Review findings

None yet.
