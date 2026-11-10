# Phase 4 — Event reporting

**Parent:** root · **Class:** contract · [README](README.md)

## Goal

Expose a concurrent, optional diagnostic reporter with explicit event types and output modes.

## Interface

Written to `pkg/diagnostic/event.go`.

```go
package diagnostic

import (
	"context"
	"log/slog"
)

type OutputMode uint8

const (
	Immediate OutputMode = iota
	Buffered
)

// Type registers one kind of diagnostic. Name is unique within a registry.
type Type struct {
	Name           string
	Subsystem      string
	Level          Level
	Mode           OutputMode
	ActionRequired bool
}

type Event struct {
	// Type is the registered name. Subsystem and Level are that type's
	// registered subsystem and level, filled by the Reporter.
	Type      string
	Subsystem string
	Level     Level
	Message   string
	Facts     []slog.Attr
}

// Deliverer is one output destination. The owner supplies one per OutputMode.
type Deliverer interface {
	// Deliver writes one event to its destination and returns a failed
	// delivery. It applies no level policy of its own. Concurrent calls are
	// safe. (Deliver.1, F6)
	Deliver(context.Context, Event) error
}

// SlogDeliverer adapts a slog.Handler as a Deliverer. The event's facts and
// subsystem become the record's attributes, the subsystem is bound before
// Enabled is consulted at the event's Level, and Handle's error is Deliver's
// error. A handler that is not enabled for that level receives nothing.
// (SlogDeliverer.1, F6)
func SlogDeliverer(slog.Handler) Deliverer

type Reporter interface {
	// Report accepts an event of a registered type and delivers it through
	// the owner's Deliverer for the type's registered mode. The routing
	// implementation and its constructor are internal to clai. (Report.1, F4, F10)
	//
	// An event below its subsystem's resolved level is silent and not a
	// failure. An action-required event is delivered at every level.
	// (Report.2, F7)
	//
	// Report returns an error that names an unknown type, and returns a
	// failed delivery. Concurrent calls are safe and share no process-wide
	// state. (Report.3, F6)
	Report(context.Context, Event) error
}
```

The routing implementation is internal (F10). Written to
`internal/diag/reporter.go`:

```go
package diag

import "github.com/baalimago/clai/pkg/diagnostic"

// NewReporter builds the routing Reporter over types, the resolved levels,
// and one Deliverer per output mode. It returns an error that names a
// duplicate type name, a type whose subsystem has no entry in levels, or a
// registered mode without a Deliverer; a Reporter it returns never fails for
// those reasons. (NewReporter.1, F4)
func NewReporter(types []diagnostic.Type, levels ResolvedLevels, deliverers map[diagnostic.OutputMode]diagnostic.Deliverer) (diagnostic.Reporter, error)
```

Consumes: `Level` and `ResolvedLevels` (phase 3); Go's `slog.Handler` and
`slog.Record`.

## Acceptance criteria

| #   | Criterion                                                                   | Proves                   | Proof                                             |
| --- | --------------------------------------------------------------------------- | ------------------------ | ------------------------------------------------- |
| 1   | Type registration selects the subsystem, level, and output mode's Deliverer. | Report.1, F4             | `TestReporterContract`                            |
| 2   | Required warnings are delivered even when optional detail is disabled.      | Report.2, F7             | `TestReporterContract`                            |
| 3   | Unknown types and delivery errors reach the caller; unresolved subsystems and missing Deliverers are refused at construction. | Report.3, NewReporter.1, Deliver.1 | `TestReporterContract`, `TestDelivererContract`   |
| 4   | Concurrent events do not race or share mutable global configuration.       | Report.3, Deliver.1      | `TestReporterConcurrent`, `TestDelivererContract` |
| 5   | A slog handler receives enabled events as records and its errors surface.   | SlogDeliverer.1          | `TestSlogDeliverer`                               |
| 6   | Construction refuses a registry the Reporter could not route.             | NewReporter.1            | `TestNewReporter`                                 |

## Test cases

| Test                     | Level          | Proves                       | Given                                                                        | Then                                                                            | Must not                                           |
| ------------------------ | -------------- | ---------------------------- | ---------------------------------------------------------------------------- | ------------------------------------------------------------------------------- | -------------------------------------------------- |
| `TestReporterContract`   | contract suite | Report.1, Report.2, Report.3 | Registered immediate, buffered, disabled, required, and unknown types, with a recording Deliverer per mode | Each event reaches its mode's Deliverer or is silent; an unknown type returns an error naming it | Suppress required warnings or deliver disabled detail |
| `TestDelivererContract`  | contract suite | Deliver.1                    | Each Deliverer implementation, a failing destination, and concurrent calls   | The event reaches the destination or the failure is returned, without races    | Apply a level policy or use process-wide state     |
| `TestSlogDeliverer`      | contract suite | SlogDeliverer.1              | An enabled `slog.Handler` whose `Handle` fails, and a disabled one           | `Deliver` returns the handler's error; the disabled handler receives nothing   | Lose the handler error through a convenience logger |
| `TestReporterConcurrent` | contract suite | Report.3                     | Concurrent calls and separate Reporter instances                             | Events follow each instance's configuration without races                       | Use mutable process-wide logging state             |
| `TestNewReporter`        | contract suite | NewReporter.1                | Types with a duplicate name, a subsystem absent from the levels, and a mode without a Deliverer | Each returns an error naming the offending name, subsystem, or mode | Return a Reporter alongside the error |

## Error management

| Failure                          | Expected outcome                                    | Test                    |
| -------------------------------- | --------------------------------------------------- | ----------------------- |
| Unknown event type               | Return an error that identifies the type.           | `TestReporterContract`  |
| Duplicate type name              | `NewReporter` returns an error that identifies the name. | `TestNewReporter`  |
| Subsystem has no resolved level  | `NewReporter` returns an error that identifies the subsystem. | `TestNewReporter` |
| No Deliverer for the mode        | `NewReporter` returns an error that identifies the mode. | `TestNewReporter`  |
| Delivery fails                   | Return the Deliverer's error from `Report`.         | `TestReporterContract`  |
| `slog.Handler.Handle` fails      | Return the handler's error from `Deliver`.          | `TestSlogDeliverer`     |

## Golden diff

None.

## Removes

None.

## Local facts

The owner supplies the type registry, the resolved levels, and one Deliverer
per output mode. The zero `OutputMode` is the Diagnostic output mode
parameter's default (README Parameters). The routing `Reporter` and its
constructor `NewReporter` live in `internal/diag`; `pkg/diagnostic` holds the
interface, and `TestReporterContract` lives in an external test package
(`diagnostic_test`) so it can import `internal/diag` without a cycle. `SlogDeliverer` constructs one
`Deliverer` implementation, the proposal's `slog` adapter; it declares no
second interface. Phases 5 and 6 implement
`Deliverer` and run `TestDelivererContract` against their implementations.
The error from `Report` is available to its caller; F6 states how an agent
consumer treats it.

## Human required

None.

## Implementation notes

| Run       | Commit      | Command | Result |
| --------- | ----------- | ------- | ------ |
| replan    | 2c9c4ff+wip | —       | V1-10: `Deliverer` declared as the owner-supplied destination per mode; `SlogDeliverer` is the `slog` boundary. V1-11: `EventSink` renamed `Reporter`, `DiagnosticType` renamed `Type`. V1-03: Report.1 cites F4 only. |
| replan    | 2c9c4ff+wip | —       | dry run: Report.3 names a type whose subsystem has no resolved level; Local facts cite the output-mode parameter and bound `SlogDeliverer`'s role. |
| replan    | 2c9c4ff+wip | —       | F10: the routing Reporter implementation and constructor are internal; the public package keeps the interface, the types, and `SlogDeliverer`. |
| replan    | 2c9c4ff+wip | —       | V3-02: `NewReporter` declared in `internal/diag`; construction-time refusals move from Report.3 to NewReporter.1; the contract suite lives in `diagnostic_test`. |
| replan    | 2c9c4ff+wip | —       | round 4 note: criterion 3 cites NewReporter.1; F4 binds `NewReporter`. |
| start+wip | 1bebb27+wip | README Validation policy (all six gates) | Pass: gofumpt, staticcheck, vet, race/coverage tests ×3, fix, and dupl returned 0. |
| replan+wip | 1bebb27+wip | interface fit inspection | A02: `SlogDeliverer` cannot obtain an event's registered level or subsystem from `Event` or its constructor. |
| replan+wip | 1bebb27+wip | — | A02 closed: `Event` carries `Subsystem` and `Level`, filled by the routing `Reporter` from the registered `Type`; SlogDeliverer.1 cites them. |
| start+wip | 1bebb27+wip | README Validation policy (all six gates) | Pass: gofumpt, staticcheck, vet, race/coverage tests ×3, fix, and dupl returned 0. |
| test+wip | 1bebb27+wip | `go test ./pkg/diagnostic ./internal/diag -race -cover -count=3 -timeout=30s` | Pass: reporter, Deliverer, slog adapter, construction, and concurrency contracts; coverage 93.3% and 84.2%. |
| end-1+wip | 1bebb27+wip | README Validation policy (all six gates, randomized order) | Pass: vet, dupl, gofumpt, race/coverage tests ×3, staticcheck, and fix returned 0. |
| end-2+wip | 1bebb27+wip | README Validation policy (all six gates, randomized order) | Pass: staticcheck, fix, race/coverage tests ×3, gofumpt, vet, and dupl returned 0. |
| complete+wip | 1bebb27+wip | Acceptance criteria, Test cases, and Error management walk | Complete: every row is covered by its named test; no skips, gate changes, or open andons. |

Andon: A02 (closed by plan).

## Review findings

### Review 1, 2026-11-09 (root)

None.

Verified good:
- Both Interface blocks match.
- `TestDelivererContract` covers the fake, slog, buffered, text and JSON Deliverers.

See R1-11 (README) for fakes in other packages that run no suite.

### Review 2, 2026-11-09 (root)

R1-11 resolved: the listed fakes are gone. Tests use `diagfake.Deliverer`, which is in `TestDelivererContract`, and the real `diag.NewReporter`. `erringReporter` (`pkg/agent/diagnostics_contract_test.go:442`) is a stub for the F6 failure clause, which the rule allows.

**R2-03 · minor · owner fix · node root.** `internal/testenv/procenv/procenv.go:43-80` `envReporter` is a Reporter fake that runs through no `TestReporterContract`. `procenv.Context` hands it to tests in several packages. It re-resolves levels from `os.Environ` at every `Report` using its own copy of the legacy-control key list, which the README Legacy controls table and the composition already define. So a test can pass against behaviour the CLI does not have: it treats any `DEBUG_<X>` as a legacy key, and it falls back to info for every subsystem on a resolve error, where the CLI exits 1.
- [ ] Add `envReporter` to `TestReporterContract`, or build it once from the composition's own level reading.
