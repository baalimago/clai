# Phase 8 — Agent diagnostics

**Parent:** root · **Class:** contract · [README](README.md)

## Goal

Let a Go application receive agent diagnostics without changing existing calls.

## Interface

Written to `pkg/agent/diagnostics_contract.go`.

```go
package agent

import (
	"context"

	"github.com/baalimago/clai/pkg/diagnostic"
	"github.com/baalimago/clai/pkg/text/models"
)

// DiagnosticTypes returns every diagnostic type the agent reports, so a
// caller can register them with its Reporter. Every event the agent reports
// uses one of these names. (DiagnosticTypes.1, F3)
func DiagnosticTypes() []diagnostic.Type

// WithDiagnosticReporter supplies a caller-owned Reporter for agent
// diagnostics on agent.New. It is absent by default and separate from
// WithLogger, which keeps its completed-message records. (WithDiagnosticReporter.1, F3)
func WithDiagnosticReporter(diagnostic.Reporter) Option

// WithDiagnosticDeliverer supplies one caller-owned Deliverer and a Level.
// The agent builds its Reporter with NewReporter from DiagnosticTypes, levels
// that hold the default and every subsystem at that Level, and the Deliverer
// for both output modes; WithDiagnosticDeliverer(diagnostic.SlogDeliverer(h), level) is
// the one-line slog path. The later of this option and WithDiagnosticReporter
// wins. (WithDiagnosticDeliverer.1, F3, F10)
func WithDiagnosticDeliverer(diagnostic.Deliverer, diagnostic.Level) Option

type SetupQuerier interface {
	// Setup preserves the existing setup contract. A supplied Reporter
	// receives setup events. Without one, the agent emits no diagnostics.
	// (Setup.1, F3)
	//
	// A failed Report does not change Setup's error. (Setup.2, F6)
	Setup(context.Context) error

	// Query preserves the existing result, typed errors, and agent-facing tool
	// error path. A supplied Reporter receives query events. Without one, the
	// agent emits no diagnostics. (Query.1, F2, F3, F9)
	//
	// A failed Report does not change Query's result or error. (Query.2, F6)
	Query(context.Context, models.Chat) (models.Chat, error)
}
```

Consumes: `Reporter.Report`, `Deliverer`, `Type`, `NewReporter` (phase 4),
`Level` and `ResolvedLevels` (phase 3), `pkg/agent:New`, `Option`,
`WithLogger`, `Agent.Setup`, and `Agent.Query` (imports), and
`pkg/text/models:Chat` (import).

## Acceptance criteria

| #   | Criterion                                                                 | Proves                                   | Proof                               |
| --- | ------------------------------------------------------------------------- | ---------------------------------------- | ----------------------------------- |
| 1   | A caller that supplies a Reporter receives agent diagnostics.             | WithDiagnosticReporter.1, Setup.1, Query.1 | `TestAgentDiagnosticsContract`    |
| 2   | Existing callers stay silent and need no option changes.                  | Setup.1, Query.1, F3                     | `TestAgentDiagnosticsCompatibility` |
| 3   | Typed errors and agent-facing tool errors stay on their existing paths.   | Query.1, F2, F9                          | `TestAgentDiagnosticsCompatibility` |
| 4   | The existing message logger continues to receive its original records.    | WithDiagnosticReporter.1, F3             | `TestAgentLoggerCompatibility`      |
| 5   | A Reporter built from the declared types accepts every agent event.       | DiagnosticTypes.1                        | `TestAgentDiagnosticTypes`          |
| 6   | A failing Reporter leaves results and errors unchanged.                   | Setup.2, Query.2, F6                     | `TestAgentDiagnosticsContract`      |
| 7   | One Deliverer and a Level receive every agent event at that level without a caller-built Reporter. | WithDiagnosticDeliverer.1, F3, F10 | `TestAgentDelivererOption`     |

## Test cases

| Test                                | Level          | Proves                                 | Given                                                                    | Then                                                       | Must not                                                    |
| ----------------------------------- | -------------- | -------------------------------------- | ------------------------------------------------------------------------ | ---------------------------------------------------------- | ----------------------------------------------------------- |
| `TestAgentDiagnosticsContract`      | contract suite | WithDiagnosticReporter.1, Setup.1, Setup.2, Query.1, Query.2 | An agent with an explicit Reporter, a diagnostic-producing operation, and a Reporter whose `Report` fails | The supplied Reporter receives the diagnostic; the failing Reporter changes neither result nor error | Send the diagnostic to the model or a process-global output |
| `TestAgentDiagnosticsCompatibility` | contract suite | Setup.1, Query.1, F2, F3, F9           | Existing callers, a typed error, and a failed tool call                  | Existing results and error handling remain available       | Require a new option or print diagnostics by default        |
| `TestAgentLoggerCompatibility`      | contract suite | WithDiagnosticReporter.1, F3           | Both the existing message logger and a diagnostic Reporter               | Each receives its own records                              | Mix diagnostics into message records                        |
| `TestAgentDelivererOption`          | contract suite | WithDiagnosticDeliverer.1              | A recording Deliverer at LevelDebug and at LevelError, both options given in each order | Events at or above the Level reach the Deliverer whatever their mode; the later option wins | Require the caller to import `internal/diag` or build a Reporter |
| `TestAgentDiagnosticTypes`          | contract suite | DiagnosticTypes.1                      | A Reporter registered with `DiagnosticTypes()` and every diagnostic-producing scenario | No event is refused as unknown; names are unique | Emit an event whose type is not declared                    |
| `TestAgentInternalDiagnostics`      | contract suite | DiagnosticTypes.1, WithDiagnosticReporter.1, WithDiagnosticDeliverer.1, Setup.1, F3 | A setup that reports through clai's internal producers (a warning and debug detail), with a Deliverer at LevelDebug and at LevelWarn, with a caller's Reporter, and with neither | The internal events reach the Deliverer at its level and the Reporter with their registered subsystem and level; `DiagnosticTypes()` declares them; with neither the run reports nothing | Write to the process streams or report without a Reporter |

## Error management

| Failure                        | Expected outcome                                                                      | Test                                |
| ------------------------------ | ------------------------------------------------------------------------------------- | ----------------------------------- |
| Agent setup or query fails     | Return the existing typed failure through the agent API.                              | `TestAgentDiagnosticsCompatibility` |
| Agent-facing tool call fails   | Use the existing tool error path.                                                     | `TestAgentDiagnosticsCompatibility` |
| Reporter fails during a call   | The call's result and error are unchanged; the caller observes it in its own Reporter. | `TestAgentDiagnosticsContract`     |

## Golden diff

None.

## Removes

None.

## Local facts

The caller owns Reporter configuration and output. The Reporter does not
replace `WithLogger` and is absent by default.

The agent reports what its run reports (R4-03, a provisional decision of the
fix brief, 2026-11-10): with either option, the events of clai's internal
producers during `Setup` and `Query` reach the caller too, so
`DiagnosticTypes()` holds the internal registry (`internal/runenv` `Types`)
besides the agent's own types. With `WithDiagnosticDeliverer` the run's
levels are the option's Level; with `WithDiagnosticReporter` every event
reaches the Reporter, which applies its own level.

Setup.2 and Query.2 are a plan proposal. The maintainer skipped the question
of how a failing caller-supplied Reporter affects a call (Q19, notebook
`clai/diagnostics-planning-decisions.md`) and decides it at the interface
sign-off; neither option is approved until then.

## Human required

None.

## Implementation notes

| Run       | Commit      | Command | Result |
| --------- | ----------- | ------- | ------ |
| replan    | 2c9c4ff+wip | —       | V1-04: `WithDiagnosticReporter` is in the block; Setup.2 and Query.2 decide the failing-Reporter case per F6. V1-05: `DiagnosticTypes` declared. V1-11: `AgentDiagnostics` renamed `SetupQuerier`. V1-03: Query.1 cites F9; F8 no longer binds this phase. |
| replan    | 2c9c4ff+wip | —       | F10: `WithDiagnosticDeliverer(Deliverer, Level)` added as the one-line path; the agent builds its Reporter internally. |
| replan    | 2c9c4ff+wip | —       | V3-01: `SetupQuerier` is on the F10 allowlist. V3-02: WithDiagnosticDeliverer.1 builds through `NewReporter`; Consumes name it and `ResolvedLevels`. |
| start     | 0ea7c95+wip | gofumpt, staticcheck, vet, `go test ./... -race -cover -count=3 -timeout=30s`, fix, dupl | all rc 0; 92 packages ok, 0 failed; dupl 41 clone groups |
| end       | 0ea7c95+wip | gofumpt, staticcheck, vet, `go test ./... -race -cover -count=3 -timeout=30s`, fix, dupl (shuffled order) | all rc 0; 92 packages ok, 0 failed; `pkg/agent` coverage 96.2%; dupl 41 clone groups |
| end       | 0ea7c95+wip | gofumpt, staticcheck, vet, `go test ./... -race -cover -count=3 -timeout=30s`, fix, dupl (shuffled order) | all rc 0; 92 packages ok, 0 failed; `pkg/agent` coverage 96.2%; dupl 41 clone groups |

Andon: none.
Surprises:
- The Interface block's unnamed parameters (`WithDiagnosticReporter(diagnostic.Reporter)`, `WithDiagnosticDeliverer(diagnostic.Deliverer, diagnostic.Level)`) are named in the written functions so the bodies can use them; the Go signatures are identical.
- The agent's three types (`agent.setup.config_dir_unavailable` warn immediate, `agent.setup.completed` debug immediate, `agent.query.completed` debug buffered, subsystem `agent`) are body choices; the config-dir event reports the `os.Mkdir` failures `Setup` already ignored.

## Review findings

### Review 1, 2026-11-09 (root)

None.

Verified good:
- With no Reporter, `report` returns at once (silent by default, F3).
- A failed `Report` is discarded (F6).
- `DiagnosticTypes` returns a fresh slice; a later option wins; a nil Deliverer stays silent.
