# Phase 7 — Runtime boundary

**Parent:** root · **Class:** contract · [README](README.md)

## Goal

Return dispatch and shutdown failures to the CLI instead of printing them.

## Interface

Written to `internal/runtimeboundary/runtime.go`. `DispatchMonitor` is a Go
interface inside clai, not a process. The CLI owns it and owns terminal error
presentation; the boundary prints no diagnostic.

```go
package runtimeboundary

import (
	"context"
	"os"

	"github.com/baalimago/clai/pkg/diagnostic"
	"github.com/baalimago/go_away_boilerplate/pkg/cmd"
)

type ExitStatus int

type Invocation struct {
	Args     []string
	Commands map[string]cmd.Command
}

type Dispatcher interface {
	// Dispatch runs the command named by Args and returns its exit status with
	// the original parse, setup, or run error, so errors.Is and errors.As reach
	// the cause. A help request returns status 0 and an error that errors.As
	// matches cmd.HelpRequestedError; a user-initiated exit returns status 0
	// and an error that errors.Is matches cmd.ErrUserInitiatedExit or
	// table.ErrUserInitiatedExit; no arguments returns status 1 and
	// cmd.ErrNoArgs. It writes no diagnostic and no usage. (Dispatch.1, F2, F9)
	//
	// Completion output and each command's result stream are unchanged.
	// (Dispatch.2, F1, F9)
	Dispatch(context.Context, Invocation) (ExitStatus, error)
}

type ShutdownMonitor interface {
	// Monitor observes interrupt signals until ctx is done and reports each
	// signal as a diagnostic through Reporter.Report. The first signal cancels
	// through cancel; the third calls the owner's force-exit collaborator. A
	// diagnostic delivery failure is sent on the returned channel, which closes
	// when observation stops. It returns an error, and starts nothing, when
	// the owner's signal source cannot be observed. It never calls os.Exit
	// itself. (Monitor.1, F9)
	Monitor(context.Context, context.CancelFunc) (<-chan error, error)
}

type DispatchMonitor interface {
	Dispatcher
	ShutdownMonitor
}

// SignalSource is where the owner's interrupt signals come from.
type SignalSource interface {
	// Subscribe starts delivery on signals until stop runs, or returns an
	// error when the source cannot be observed. (Subscribe.1)
	Subscribe() (signals <-chan os.Signal, stop func(), err error)
}

// NewDispatchMonitor returns the DispatchMonitor that observes source,
// reports each signal as an event of signalType through reporter, and calls
// forceExit on the third signal. It reads no process state itself.
// (NewDispatchMonitor.1, F9)
func NewDispatchMonitor(source SignalSource, reporter diagnostic.Reporter, signalType string, forceExit func()) DispatchMonitor
```

Consumes: `Reporter.Report` and `Event` (phase 4); `cmd:RunSilent`,
`cmd:HelpRequestedError`, `cmd:ErrNoArgs`, `cmd:ErrUserInitiatedExit`, and
`cmd:Command` (import, the local `go_away_boilerplate` checkout named under
Human required); `table:ErrUserInitiatedExit` (import).

## Acceptance criteria

| #   | Criterion                                                                                | Proves             | Proof                     |
| --- | ---------------------------------------------------------------------------------------- | ------------------ | ------------------------- |
| 1   | Parse, setup, and run failures return status 1 and their original typed cause.           | Dispatch.1, F2, F9 | `TestDispatcherContract`  |
| 2   | Help, no arguments, and user exits return their documented status and identifying error. | Dispatch.1, F9     | `TestDispatcherContract`  |
| 3   | Dispatch writes nothing to the diagnostic stream; completion and results are unchanged.  | Dispatch.2, F1     | `TestDispatcherContract`  |
| 4   | The signal sequence keeps the current cancel, warn, and force-exit order.                | Monitor.1, F9      | `TestShutdownMonitorSignals` |
| 5   | A diagnostic delivery failure reaches the owner, and the channel closes with the ctx.    | Monitor.1          | `TestShutdownMonitorSignals` |
| 6   | The constructor wires the owner's signal source, Reporter, signal type, and force exit; a source that cannot be observed fails `Monitor`. | NewDispatchMonitor.1, Subscribe.1 | `TestShutdownMonitorSignals` |

## Test cases

| Test                         | Level          | Proves                 | Given                                                                                                  | Then                                                                                         | Must not                                        |
| ---------------------------- | -------------- | ---------------------- | ------------------------------------------------------------------------------------------------------ | -------------------------------------------------------------------------------------------- | ----------------------------------------------- |
| `TestDispatcherContract`     | contract suite | Dispatch.1, Dispatch.2 | Fake commands failing in parse, setup, and run; `-h`; no arguments; a user exit; `__complete`          | Statuses and error identities match the clause; stderr is empty; completion reaches stdout   | Print a diagnostic or usage, or change a result |
| `TestShutdownMonitorSignals` | contract suite | Monitor.1, Subscribe.1, NewDispatchMonitor.1 | An injected signal source, a Reporter whose `Report` fails, a force-exit recorder, and a cancelled ctx | Cancel on the first signal, an event per signal, force-exit on the third, failure on channel | Call `os.Exit` or leak a goroutine              |

## Error management

| Failure                            | Expected outcome                                                          | Test                         |
| ---------------------------------- | ------------------------------------------------------------------------- | ---------------------------- |
| Command parse, setup, or run fails | Status 1 and the original typed cause; nothing printed.                   | `TestDispatcherContract`     |
| Help requested or no arguments     | Status 0 with `cmd.HelpRequestedError`, or status 1 with `cmd.ErrNoArgs`. | `TestDispatcherContract`     |
| Signal diagnostic delivery fails   | The failure is sent on the channel; signal handling continues.            | `TestShutdownMonitorSignals` |
| Signal source cannot be observed   | `Monitor` returns an error and starts no goroutine.                       | `TestShutdownMonitorSignals` |

## Golden diff

None.

## Removes

None.

## Local facts

The owner supplies the signal source, the diagnostic Reporter, the registered
signal event type, and the force-exit collaborator. The boundary writes to no
process-global logger. Table terminal cleanup is a write to the writer the
owner passes to `Table.WithWriter` (`go_away_boilerplate/pkg/table`); observing it is the owner's choice and is
not a member of this boundary.

The contract of `cmd.RunSilent` and `cmd.HelpRequestedError` is their doc
comment in `pkg/cmd/setup.go` of the checkout named under Human required.

## Human required

The dispatch provider is the maintainer's uncommitted `go_away_boilerplate`
checkout at `../go_away_boilerplate`, reached through the `replace` line in
clai's `go.mod`. The maintainer accepted it as the working state on 2026-11-02
and commits neither repository until the diagnostic system is proven; the
release is the root Human check. Before the start gate the agent runs

```sh
go list -m -f '{{.Dir}}' github.com/baalimago/go_away_boilerplate
go doc github.com/baalimago/go_away_boilerplate/pkg/cmd.RunSilent
```

and stops when either fails: it writes its journal line and sets
`**Line:** waiting on human: phase 7`. The agent changes nothing in that
checkout; a needed upstream change is a design andon.

## Implementation notes

| Run       | Commit      | Command | Result |
| --------- | ----------- | ------- | ------ |
| replan    | 2c9c4ff+wip | —       | V1-01: `TableSelector`, `TableRun`, and `Selection` removed; table cleanup is a write to the owner's writer. `Dispatch` consumes upstream `cmd.RunSilent`, which the maintainer releases. |
| replan    | 2c9c4ff+wip | —       | V1-11: `RuntimeBoundary` renamed `DispatchMonitor`; consumes `Reporter.Report`. V1-03: F1 and F2 now bind this phase. |
| replan    | 2c9c4ff+wip | —       | dry run: Dispatch.1 names both user-exit sentinels that `cmd.RunSilent` passes through. |
| replan    | 2c9c4ff+wip | —       | round 3 note: Monitor.1 names the condition for its returned error. |
| replan    | 0ea7c95+wip | —       | A03 sweep: `SignalSource` and `NewDispatchMonitor` declared so the owner can build the boundary; criterion 6. |
| start     | 0ea7c95+wip | gofumpt -w -l . && staticcheck ./... && go vet ./... && go test ./... -race -cover -count=3 -timeout=30s && go fix ./... && dupl -t 80 . | all rc 0; 91 packages ok, 0 FAIL; dupl 41 clone groups; Human required checks `go list -m` and `go doc cmd.RunSilent`: rc 0 |
| end       | 0ea7c95+wip | same six gates, command order shuffled | all rc 0; 92 packages ok, 0 FAIL; `internal/runtimeboundary` coverage 94.3%; dupl 41 clone groups |
| end       | 0ea7c95+wip | same six gates, command order shuffled | all rc 0; 92 packages ok, 0 FAIL; `internal/runtimeboundary` coverage 94.3%; dupl 41 clone groups |

Andon: none.
Surprises:
- A first end pair failed staticcheck on an unused test variable (`errRun`) this phase added; removed, both end runs repeated.
- Monitor stops at ctx done, so the warn and force-exit steps need the owner to pass an observation ctx that the first signal's cancel does not end (unlike `MonitorV2`'s detached drainer).

## Review findings

### Review 1, 2026-11-09 (root)

None.

Verified good:
- The Interface matches.
- The steps run cancel, warn, then force-exit, each reported with `WithoutCancel`.
- The failure channel holds 3 and closes when observation ends; the source is stopped.
- The composition's context outlives the first cancel (`compose.go:325`).
