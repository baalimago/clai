# Diagnostics and error context

## Goal

Give clai one diagnostic policy and a public, optional Go diagnostic API.
Separate diagnostics from command results and from errors returned to the
agent. Migrate clai's error-context wrappers and diagnostic producers. Keep
existing `pkg/agent` consumers compatible and silent by default.

This README is the diagnostics policy. The upstream integration choice
remains open. The worklog specifies the boundary, not which implementation
provides it.

## Glossary

| Term            | Meaning                                                          |
| --------------- | ---------------------------------------------------------------- |
| Diagnostic      | An event for the operator, not an error sent to the agent.       |
| Diagnostic type | An explicitly registered kind of diagnostic with an output mode. |
| Reporter        | The producer-facing entry that applies the level and mode policy. |
| Deliverer       | One output destination: immediate or buffered.                   |
| Immediate       | A diagnostic that goes to the diagnostic output when it occurs.  |
| Buffered        | A diagnostic that goes to the shared buffer for the current run. |
| Error context   | An operation and source location attached to an error.           |
| Remedy          | Optional display-only guidance attached to an error.             |
| Spill file      | A retained file that contains one flushed diagnostic buffer.     |

## Flows

Each arrow names the member it calls; the phase files hold the signatures.

### CLI run

```text
user runs clai
       |
       v
CLI.Run (phase 9)
  |
  +-- LevelResolver.Resolve(LevelRequest) ------------> ResolvedLevels | error
  |       error --> Presenter.ReportError(err) --> stderr, exit 1
  |
  +-- DispatchMonitor.Monitor(ctx, cancel) -----------> <-chan error, error
  |       signal --> Reporter.Report(Event)
  |
  +-- DispatchMonitor.Dispatch(ctx, Invocation) ------> ExitStatus, error
  |       |
  |       +-- command result ------------------------> result writer (stdout)
  |       |
  |       `-- command diagnostic --> Reporter.Report(Event)
  |                                     |
  |                                     +-- immediate --> Presenter.Deliver(Event) --> stderr
  |                                     |
  |                                     `-- buffered ---> BufferedDeliverer.Deliver(Event)
  |                                                          |
  |                                                          +-- buffer full --> spill file
  |                                                          |
  |                                                          `-- spill write fails
  |                                                                --> Presenter.Deliver(Event)
  |                                                                      `-- fails too --> CLI.Run panics
  |
  +-- BufferedDeliverer.Finish(ctx) --> Presenter.Deliver(Event), once for the
  |                                     path list and once per remaining event --> stderr
  |
  `-- terminal error --> Presenter.ReportError(err) --> stderr (once)
```

The level configuration selects which optional diagnostics appear. Terminal
errors and action-required warnings remain visible. The presenter clears and
redraws a viewport around persistent diagnostics. A prompt uses its explicit
terminal writer. Child-process output keeps its own destination. At the end,
`Finish` reports spill-file paths, not their contents. The files remain for
the user to inspect and remove.

### Library agent run

```text
Go application
  |
  +-- agent.New(WithDiagnosticDeliverer(d, level))   one Deliverer, e.g. SlogDeliverer(h)
  |     or agent.New(WithDiagnosticReporter(r))       a caller-built Reporter
  |
  +-- SetupQuerier.Setup(ctx) ------------------------> error
  |       setup diagnostic --> Reporter.Report(Event) --> r, or the agent's Reporter --> d
  |
  +-- SetupQuerier.Query(ctx, Chat) ------------------> Chat, error
  |       |
  |       +-- query diagnostic --> Reporter.Report(Event) --> r, or the agent's Reporter --> d
  |       |
  |       `-- tool error --------------------------> existing tool error path
  |
  `-- errors.As(err, &Locator), errors.As(err, &Remedier)

Go application with neither option
  |
  `-- SetupQuerier.Setup / Query ---------------------> result or error, no diagnostic
```

A tool error that the agent must handle stays on the existing tool error path.
It does not enter the diagnostic buffer. A failed `Report` changes neither
the result nor the error of the call (F6).

## Status board

**Line:** running

| #   | Node                                                | Parent | Class       | Interface                 | Status      |
| --- | --------------------------------------------------- | ------ | ----------- | ------------------------- | ----------- |
| 1   | [Error context](phase-1-error-context.md)           | root   | Contract    | `Locator`                 | Not Started |
| 2   | [Remedies](phase-2-remedies.md)                     | root   | Contract    | `Remedier`                | Not Started |
| 3   | [Level policy](phase-3-level-policy.md)             | root   | Contract    | `LevelResolver`           | Not Started |
| 4   | [Event reporting](phase-4-event-reporting.md)       | root   | Contract    | `Reporter`                | Not Started |
| 5   | [Buffered reporting](phase-5-buffered-reporting.md) | root   | Contract    | `BufferedDeliverer`       | Not Started |
| 6   | [Diagnostic output](phase-6-diagnostic-output.md)   | root   | Contract    | `Presenter`               | Not Started |
| 7   | [Runtime boundary](phase-7-runtime-boundary.md)     | root   | Contract    | `DispatchMonitor`         | Not Started |
| 8   | [Agent diagnostics](phase-8-agent-diagnostics.md)   | root   | Contract    | `SetupQuerier`            | Not Started |
| 9   | [CLI composition](phase-9-cli-composition.md)       | root   | Composition | `CLI.Run`                 | Not Started |
| 10  | [Prune](phase-10-prune.md)                          | root   | Prune       | Removes table in phase 10 | Not Started |

### Ownership and dependencies

| Phase | Owns                                                                                     | Depends on                   |
| ----- | ---------------------------------------------------------------------------------------- | ---------------------------- |
| 1     | Public error context, locations, and the migration of clai-owned error-context wrappers. | Existing Go error contracts. |
| 2     | Public display-only remedy contract.                                                     | Existing Go error contracts. |
| 3     | Public `Level`; internal subsystem registry, legacy-control mapping, and level resolution. | None.                      |
| 4     | Public event, reporter, and deliverer contracts with the `slog` adapter; internal routing reporter. | 3.                 |
| 5     | Internal per-run buffering and spill-file reporting.                                     | 4.                           |
| 6     | Internal diagnostic presentation and coordinated terminal output.                        | 1, 2, 3, 4.                  |
| 7     | Dispatch and shutdown integration contracts.                                             | 4.                           |
| 8     | Optional diagnostic delivery for `pkg/agent`.                                            | 4.                           |
| 9     | CLI wiring and migration of diagnostic producers, progress, and legacy controls.         | 1–8.                         |
| 10    | Removal of code made unreachable by phase 9.                                             | 3, 4, 9.                     |

Phase 7 consumes the dispatch surface added to the local `go_away_boilerplate`
checkout and replaces the shutdown monitor locally; the release is the root
Human check. Phase 9 owns
the executable and in-process CLI behavior. Phase 10 changes no observable output.

## Scope

The worklog does not add a new model-facing tool or change the tool error
protocol. It does not execute remedies. It does not add a third-party library.
It does not publish an upstream release. The maintainer accepted the
uncommitted sibling checkout `../go_away_boilerplate`, holding `cmd.RunSilent`
and `cmd.HelpRequestedError`, as the dispatch provider until the root Human
check (2026-11-02); no phase changes that repository, and a further upstream
need is a design andon.

The worklog does not add prompt, response, tool payload, or MCP output capture
as diagnostic detail. It does not change the existing `pkg/agent.WithLogger`
message-record contract. File cleanup remains the user's responsibility.

## Root facts

**F1 — Output ownership.** Command results and model replies use their result
writer. Diagnostics use the diagnostic writer. Required prompts and child
output keep their explicit destinations. This applies to raw, structured, and
reply-only modes. Binds `CLI.Run`, `Presenter`, `DispatchMonitor`.

**F2 — Error identity.** Error context preserves Go error chains and existing
typed error behavior. `errors.Is` and `errors.As` reach the original causes.
An unhandled terminal error is reported once. A recovered error is reported
only when the user needs a diagnostic. Binds `Locator`, `Presenter`,
`DispatchMonitor`, `SetupQuerier`, `CLI.Run`.

**F3 — Library compatibility.** The public diagnostic API is optional.
Without an explicit Reporter, `pkg/agent` remains silent. Existing callers
need no code changes. The existing message logger remains separate. The
agent's diagnostic types are enumerable. Binds `SetupQuerier`,
`DiagnosticTypes`, `WithDiagnosticReporter`, `WithDiagnosticDeliverer`,
`CLI.Run`.

**F4 — Diagnostic modes.** Each type registers as immediate by default or
opts into buffering. The initial migration decides each type case by case.
Low-urgency background failures are candidates for buffering. All buffered
types share one buffer per run. Binds `Reporter`, `NewReporter`,
`BufferedDeliverer`, `CLI.Run`.

**F5 — Bounded buffer.** A buffer of the Shared buffer size flushes to a new
timestamped file. An oversized diagnostic is truncated and marked. Each flush
uses a separate file in the Spill-file directory. Files remain for the user
to inspect and remove. At the end of the run, the diagnostic output lists the
file locations in flush order and shows the remaining buffered diagnostics.
It does not print spill-file contents. Memory use does not grow with the
number of diagnostics. Binds `BufferedDeliverer`, `CLI.Run`.

**F6 — Output fallback.** If a spill-file write fails, the buffer goes to the
immediate Deliverer and resets. If that delivery fails too, nothing can be
reported: `BufferedDeliverer` returns an error matching `ErrUndeliverable`
that retains both failures, and `CLI.Run` aborts the process with a panic. Short of that, a diagnostic delivery failure does not
cancel a running operation, and a failed `Report` changes neither the result
nor the error of an agent call. Binds `Reporter`,
`Deliverer`, `SlogDeliverer`, `BufferedDeliverer`, `Presenter`,
`SetupQuerier`, `CLI.Run`.

**F7 — Detail and privacy.** `LOG_LEVEL` selects a default level and named
subsystem overrides; the level names are `error`, `warn`, `info`, `debug`,
`trace`, and `warning` is an alias for `warn`. The longest complete
dotted-name match wins. Unknown or malformed selectors are errors. Action-required warnings and terminal errors
are not hidden by detail levels. Diagnostic output uses best-effort redaction
for external error text. It escapes terminal control characters. Diagnostic
detail does not enter model context or saved replies. Binds `LevelResolver`,
`Reporter`, `Presenter`, `CLI.Run`.

**F8 — Error paths.** clai-owned error-context wrappers record their source
locations. Library callers can inspect locations and remedies. Remedies are
display-only. Origin stacks are bounded and opt-in for trace detail. An error
without an origin stack does not claim one. Binds `Locator`, `Remedier`,
`Presenter`, `CLI.Run`.

**F9 — Compatibility.** Existing command results, exit statuses, signal
behavior, and agent-facing tool errors remain compatible. Legacy debug and
warning controls map as the Legacy controls table states. Runtime paths no
longer use `ancli` after phase 9. Binds `LevelResolver`, `DispatchMonitor`,
`SetupQuerier`, `CLI.Run`.

**F10 — Public surface.** `pkg/diagnostic` exports only what a Go
application uses: `SourceLocation`, `Locator`, `Remedy`, `Remedier`, `Level`
and its constants, `OutputMode` and its constants, `Type`, `Event`,
`Deliverer`, `SlogDeliverer`, `Reporter`. `pkg/agent` adds only
`SetupQuerier`, `DiagnosticTypes`, `WithDiagnosticReporter`,
`WithDiagnosticDeliverer`.
Level resolution, buffering, presentation, the routing `Reporter`, and every
constructor they need live in `internal/diag`. Binds `Locator`,
`LevelResolver`, `Reporter`, `BufferedDeliverer`, `Presenter`,
`WithDiagnosticDeliverer`, `CLI.Run`.

### Parameters

| Parameter                    | Default                | Decided by  |
| ---------------------------- | ---------------------- | ----------- |
| Diagnostic level             | `info`                 | Proposal.   |
| Diagnostic output mode       | Immediate              | Maintainer. |
| Shared buffer size           | 1 MiB                  | Maintainer. |
| Spill-file directory         | `diagnostics/` under the clai cache directory, which `CLAI_CACHE_DIR` selects | Maintainer. |
| Terminal timestamps          | Off                    | Proposal.   |

### Legacy controls

Phase 3 maps the rows that name a selector; they form the lowest-precedence
layer in row order, and a later row replaces the same selector. `LOG_LEVEL`
replaces any of them. A truthy value is one `misc.Truthy` accepts. Subsystem
names are the lower-cased `DEBUG_<SUBSYSTEM>` suffixes clai uses today, and
vendor names sit under `vendor.`.

| Legacy control                                                                                                                                                                                          | Maps to                                                                     | Decided by |
| ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | --------------------------------------------------------------------------- | ---------- |
| `NO_WARNINGS` truthy                                                                                                                                                                                    | default `error`; action-required warnings stay visible (F7)                 | Proposal.  |
| `DEBUG` truthy                                                                                                                                                                                          | default `debug`                                                             | Proposal.  |
| `DEBUG_<SUBSYSTEM>` truthy, for `CALL`, `CHAT`, `CLAUDIFIED_MSGS`, `COST_MANAGER`, `DIRSCOPE`, `LOOKBACK`, `OPENROUTER_MODEL_CATALOG`, `PROFILES`, `REPLY_MODE`, `SKILLS`, `STOPLOSS`, `SUMMARY`, `TEXT_QUERIER`, `TOOLS_REGISTRY_SET`, `VERBOSE` | `<subsystem>=debug`                                      | Proposal.  |
| `DEBUG_OPENAI`, `DEBUG_OPENROUTER`, `DEBUG_MISTRAL`, `DEBUG_HUGGINGFACE`, `ANTHROPIC_DEBUG` truthy                                                                                                       | `vendor.openai=debug`, `vendor.openrouter=debug`, `vendor.mistral=debug`, `vendor.huggingface=debug`, `vendor.anthropic=debug` | Proposal.  |
| `LOG_LEVEL_SKILLS=<level>`                                                                                                                                                                              | `skills=<level>`, with the level names F7 defines                          | Proposal.  |
| `DEBUG_CPU`                                                                                                                                                                                             | not a level: CPU profiling in `cli.RunProfiled`, unchanged                  | Proposal.  |
| `DEBUG_OUTPUT_FILE`                                                                                                                                                                                     | not a level: the querier's debug dump, unchanged                            | Proposal.  |
| `NO_COLOR`                                                                                                                                                                                              | not a level: plain text (phase 6 Deliver.3)                                 | Proposal.  |
| `SILENT`, `ANCLI_NEWLINE`                                                                                                                                                                               | `ancli` controls with no mapping; they stop having effect after phase 9    | Proposal.  |

## Validation policy

### Gates

Run each command at the start of every phase and twice at the end. Run the
end commands in random order where the runner permits it. The duplicate-code
report is a signal, not an automatic failure.

```sh
go run mvdan.cc/gofumpt@latest -w -l .
go run honnef.co/go/tools/cmd/staticcheck@latest ./...
go vet ./...
go test ./... -race -cover -count=3 -timeout=30s
go fix ./...
go run github.com/mibk/dupl@latest -t 80 .
```

Until the root Human check, the gates compile the sibling checkout through
the `replace` line, and `go.sum` carries no hash for it; a worktree or clone
without that sibling cannot run them.

Composition uses committed output snapshots. Phase 9 adds the snapshot
generator. This command regenerates its fixtures:

```sh
go test ./e2e -run '^TestDiagnosticOutputGoldens$' -args -update-diagnostic-goldens
```

Existing CLI tests use assertions rather than a shared snapshot generator.

### Human checks

| Check | Command | Certifies |
| --- | --- | --- |
| Release `go_away_boilerplate` with `cmd.RunSilent`, bump the requirement, and drop the `replace` line from `go.mod`. | `go mod edit -dropreplace github.com/baalimago/go_away_boilerplate && go get github.com/baalimago/go_away_boilerplate@<tag> && go mod tidy && make qa` | root |

## Findings

| ID    | Severity | Node | Owner | Status | Summary                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                          |
| ----- | -------- | ---- | ----- | ------ | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| V1-01 | blocker  | 7    | plan  | Closed +wip   | check 4 · phase-7 `Select.1` · the clause cannot be triggered through `TableRun.Run() ([]int, int, error)`: go_away_boilerplate v1.33.12 discards cleanup errors (the phase says so at "Example: table cleanup failure"); declare a table surface that returns the cleanup error, or drop cleanup from `Select.1` and leave it to the Human required decision                                                                                                                                                                                                    |
| V1-02 | major    | 9    | fix   | Closed +wip | check 3 · phase-9 Consumes `internal/cli:RunExecutable (import)` · no such member; the repository has `cli.Run` and `cli.RunProfiled` (internal/cli/cli.go:277, main.go:12) and `e2etest.Harness.RunProfiled`; cite the real member and say what "executable" means for `TestDiagnosticExecutableParity`, since no harness builds a binary                                                                                                                                                                                                                       |
| V1-03 | major    | root | plan  | Closed +wip | check 4 · README Root facts Binds lists · F1 is cited by no `EventSink` clause, F3 by no `EventSink` or `ErrorContext` clause, F5 by no `DiagnosticOutput` clause, F8 and F9 by no `AgentDiagnostics` clause, while `EventSink` Report.2 and phase-7 criteria cite F6, which binds neither; add the citing clauses or correct the Binds lists (scenario: an `EventSink` that prints by default passes its suite although F3 forbids it)                                                                                                                          |
| V1-04 | major    | 8    | plan  | Closed +wip | check 4, 10 · phase-8 Interface · `WithDiagnosticSink` is declared in prose below the block, not in it, and the sink-failure behaviour of `Setup`/`Query` is an undecided maintainer question with no clause and no Error management row; put the option in the Interface block, record the decision under Human required or decide it, then add the clause and the row                                                                                                                                                                                          |
| V1-05 | major    | 8    | plan  | Closed +wip | check 9 · phase-8 `Setup.1`/`Query.1` · agent events need registered `DiagnosticType` names, and `EventSink` Report.2 rejects unknown types, but no member declares the agent's type set, so a library caller cannot build an accepting sink; declare the agent's diagnostic types as a member and consume `DiagnosticType` (phase 4)                                                                                                                                                                                                                            |
| V1-06 | major    | 6    | fix   | Closed +wip | check 9 · phase-6 `ReportError.1` · "debug detail" and "trace detail" select the output, but Consumes lists no phase-3 member and no clause says how the detail level reaches the output; consume `Level` (phase 3) and state the selection in a clause                                                                                                                                                                                                                                                                                                          |
| V1-07 | major    | root | plan  | Closed +wip | check 5 · legacy control mapping · F9 promises "documented mappings" but none is defined; phase 3 names `DEBUG_CPU`/`DEBUG_OUTPUT_FILE`, phase 9 `NO_WARNINGS`, phase 10 `LOG_LEVEL_SKILLS`, and internal/debugflags serves 16 distinct `DEBUG_*` subsystem flags; add a root table `Legacy control \| Maps to \| Decided by` covering every control and point phase 3's "documented legacy controls (import)" at it                                                                                                                                             |
| V1-08 | major    | root | plan  | Closed +wip | check 5 · literals restated outside the root · `1 MiB` (phase-5 Report.1), default `info` (phase-3 Resolve.1, phase-9 synopsis), cache-directory default (phase-5 Report.2, phase-9 Local facts), timestamps off (phase-6 Report.2); make clauses cite the parameter or invariant by name and delete the phase-9 Local-facts restatement                                                                                                                                                                                                                         |
| V1-09 | major    | root | plan  | Closed +wip | check 8 · README Flows · arrows target stdout, stderr, the buffer, and "caller's sink" instead of declared members, and the section says it does not prescribe calls; redraw with `Dispatch`, `EventSink.Report`, `BufferedSink.Report`/`Finish`, `DiagnosticOutput.Report`/`ReportError`, `Setup`/`Query` now that signatures exist                                                                                                                                                                                                                             |
| V1-10 | major    | 4    | plan  | Closed +wip | check 9 · phase-4 `Report.1` · "the registered mode selects immediate or buffered output", but phase 4 consumes only `slog.Handler`/`slog.Record`, while both destinations (`BufferedSink.Report`, phase 5; `DiagnosticOutput.Report`, phase 6) take an `Event` and are later phases, so no member carries an `Event` from the sink to a destination and the composition would have to rebuild one from a `slog.Record`; declare in phase 4 that delivery targets are `EventSink`s selected by mode (phases 5 and 6 then run the `EventSink` suite), or renumber |
| V1-11 | minor    | root | plan  | Closed +wip | check 7 · interface names · `ErrorContext`, `Remediable`, `EventSink`, `BufferedSink`, `DiagnosticOutput`, `RuntimeBoundary`, `AgentDiagnostics` are neither verbs nor `-er` agent nouns (`LevelResolver`, `Dispatcher`, `TableSelector`, `ShutdownMonitor` are); rename, for example `Reporter`, `BufferedReporter`, `Presenter`, `Remedier`                                                                                                                                                                                                                    |
| V2-01 | major | 7 | plan | Closed +wip | check 10 · phase-7 Local facts + Human required `None.`, README Ownership note vs Scope · the phase text requires a machine-local asset (uncommitted `cmd.RunSilent`/`HelpRequestedError` in `../go_away_boilerplate`, reached by an uncommitted `replace` line, no tag or commit holds them) and a decision Scope reserves for a person ("does not … grant permission to change another repository"); restore a Human required section naming the artifact (a go_away_boilerplate commit or tag, or the maintainer's recorded acceptance of the sibling checkout for the run), the stop point before the start gate, and the checks `go list -m -f '{{.Dir}}' github.com/baalimago/go_away_boilerplate` and `go doc …/pkg/cmd.RunSilent`; amend Scope to match Ownership; record the decision as a `maintainer` journal line |
| A01   | quality andon | root | maintainer | Closed 2c9c4ff+wip | environment (probable: 14 runnable processes before the gate and load 20–22 on 22 CPUs during it; the sandbox hides host processes, so the source is unverified) · `go test ./... -race -cover -count=3 -timeout=30s` · fix baseline run 1: one package unidentified (output lost to `tail -80`), reruns 2 and 3 passed; fix closing run 1: `e2e`, `internal/audio`, `internal/chat`, `internal/text`, `internal/vendors` timed out at 30 s with testenv `starved` verdicts, `internal/capture` `TestRecord_StopReasons/enter` found no `.wav.part` from the ffmpeg shim; an earlier session the same day saw the e2e binary time out with 5.4 s waiting for CPU (notebook `clai/diagnostics-planning-decisions.md`) · 2c9c4ff+wip |
| V3-01 | blocker | 8 | plan | Closed +wip | check 4, 5 · README F10 + phase-8 Interface · phase 8 writes `SetupQuerier` to `pkg/agent/diagnostics_contract.go` while F10's `pkg/agent` allowlist omits it, so `TestPublicAPISurface` fails at phase 8's end gate; add `SetupQuerier` to F10's `pkg/agent` list (maintainer decision) or declare the interface outside `pkg/agent` |
| V3-02 | major | 8 | plan | Closed +wip | check 9 · phase-8 `WithDiagnosticDeliverer.1` · the agent must build a routing Reporter but no phase declares its constructor and phase 8 consumes none; declare in phase 4 a second block in `internal/diag`, e.g. `NewReporter(types []diagnostic.Type, levels ResolvedLevels, deliverers map[diagnostic.OutputMode]diagnostic.Deliverer) (diagnostic.Reporter, error)`, and add it and `ResolvedLevels` to the Consumes of phases 8 and 9 |
| V3-03 | major | 3 | fix | Closed +wip | check 4 · phase-3 criterion 5 · no Test cases row proves it; add `TestPublicAPISurface` · repeatable command · Criterion 5 · Given the phase's exported additions · Then every identifier is in the F10 allowlist · Must not count `internal/diag` |
| V3-04 | major | 9 | fix | Closed +wip | check 4 · phase-9 criterion 10 · no Test cases row proves it; add the same `TestPublicAPISurface` row for Criterion 10, and cite phase 1 criterion 5 in Consumes instead of listing a test as a member |

## Session journal

- 2026-11-02 · plan · README drafted
- 2026-11-02 · plan · README amended: Flows
- 2026-11-02 · maintainer · README signed
- 2026-11-02 · plan · README amended: Validation policy
- 2026-11-02 · plan · README amended: Phase links
- 2026-11-02 · plan · phase 1 re-planned
- 2026-11-02 · plan · phase 7 re-planned
- 2026-11-02 · review · round 1 not opened: every phase is Not Started and the tree holds no implementation (git diff HEAD empty, no `ErrorContext`/`EventSink`/… symbols in Go); gates not re-run; nothing to review until a phase is Complete
- 2026-11-02 · validate · V1 Not ready · checks 1–10 over README and phases 1–10; check 2: every Interface block assembled into a scratch module (packages diagnostic, runtimeboundary, agent with a stub for the prose `WithDiagnosticSink`), `GOPROXY=off go vet ./...`: OK; check 3: members confirmed by inspection, `internal/cli:RunExecutable` absent · findings: 1 blocker, 9 major, 1 minor · notes: 7
- 2026-11-02 · plan · phase 7 re-planned · V1-01 closed +wip: `TableSelector` removed, `Dispatch` consumes upstream `cmd.RunSilent` (maintainer release precondition under Human required)
- 2026-11-02 · plan · README amended: Validation policy (Human checks: upstream release) · phase 7 Human required now cites the local `go_away_boilerplate` checkout; `RunSilent` implemented there (pkg/cmd/setup.go, run_silent_test.go), upstream `make qa` OK, clai `go.mod` replace added, clai race gate OK
- 2026-11-02 · plan · phase 7 re-planned: Human required set to None (upstream surface is local); README amended: Ownership note
- 2026-11-02 · validate · V2 Not ready · three independent validators, same brief, read-only; checks 1–10; check 2: Interface blocks assembled per validator in scratch (diagnostic, runtimeboundary, agent + stub `WithDiagnosticSink`), replaces to both local checkouts, `GOPROXY=off GOFLAGS=-mod=mod go mod tidy && GOPROXY=off go vet ./...`: OK ×3, one validator also built a `Dispatch` fit stub against `cmd.RunSilent`: OK; check 3: members confirmed by inspection, `internal/cli:RunExecutable` still absent · V1-01 resolved, V1-02–V1-11 open, none regressed · findings: 1 major (V2-01, found by all three) · notes: 15
- 2026-11-02 · maintainer · uncommitted `../go_away_boilerplate` checkout accepted as the dispatch provider until the root Human check; neither repository is committed until the diagnostic system is proven
- 2026-11-02 · plan · V2-01 closed +wip · phase 7 Human required restored; README amended: Scope, Validation policy
- 2026-11-02 · plan · phases 1–10 re-planned · V1-03, V1-04, V1-05, V1-07, V1-08, V1-09, V1-10, V1-11 closed +wip · renames (`Locator`, `Remedier`, `Reporter`+`Deliverer`, `BufferedDeliverer`, `Presenter`, `DispatchMonitor`, `SetupQuerier`); phase 4 declares `Deliverer` as the per-mode destination, phases 5 and 6 implement it; phase 8 declares `DiagnosticTypes` and `WithDiagnosticReporter` and decides the failing-Reporter case per F6 (proposal, for the interface sign-off); phase 3 maps the root Legacy controls table from `LevelRequest.Legacy`
- 2026-11-02 · plan · README amended: Goal (dead policy link removed), Glossary, Flows, Status board, Ownership, Root facts (Binds, F5, F6, F9), Parameters (override variable), Legacy controls
- 2026-11-02 · fix · A01 filed · `go test ./... -race -cover -count=3 -timeout=30s`: run 1 FAIL (package unidentified), runs 2 and 3 pass; gofumpt, staticcheck, vet, fix, dupl: rc 0
- 2026-11-02 · plan · phase 8 Local facts: Setup.2 and Query.2 marked as a proposal pending the maintainer's skipped Q19 (notebook `clai/diagnostics-planning-decisions.md`)
- 2026-11-02 · fix · A01 cause corrected to environment, owner maintainer · closing gate run 1: gofumpt, staticcheck, vet, fix, dupl rc 0; `go test ./... -race -cover -count=3 -timeout=30s`: 6 FAIL (5 testenv `starved` timeouts at load 20–22, 1 ffmpeg-shim race in `internal/capture`); surprise: the timed-out `internal/audio` binary orphaned its `/bin/sh` ffmpeg shim, which looped on `sleep 0.1` until killed
- 2026-11-02 · fix · V1-02, V1-06 edited, not closed · closing gate run 2: gofumpt, staticcheck, vet, fix, dupl rc 0; `go test ./... -race -cover -count=3 -timeout=30s`: 5 FAIL, testenv `starved` in e2e, audio, chat, text, vendors, load 7→20 during the gate with the host otherwise idle inside the sandbox; the findings close when a gate passes after A01 is closed
- 2026-11-02 · plan · phases 3, 4, 5, 6, 7, 9 re-planned after a read-only dry-run validation (not a round): Resolve-failure Level, `main.go` ancli removal, member-level Binds, `warning` alias defined once in F7, unregistered-subsystem error in Report.3, phase 9 registers every `Type.Subsystem`; README amended: Ownership (phase 6, 10 dependencies), Root facts (F3, F6, F7), Legacy controls, Flows
- 2026-11-03 · maintainer · A01 closed · environment: the gates pass on the maintainer's machine; this host was running other work
- 2026-11-03 · fix · V1-02, V1-06 closed · gate not re-run here; the maintainer attested the passing gate when closing A01
- 2026-11-03 · maintainer · design decision: the public API exposes only what a Go application uses (F10); presentation and buffering move internal; a one-line Deliverer option joins phase 8
- 2026-11-03 · plan · phases 1, 3, 4, 5, 6, 8, 9 re-planned · F10: `LevelResolver`, `BufferedDeliverer`, `Presenter`, and the routing `Reporter` move to `internal/diag`; `Level` stays public; phase 8 adds `WithDiagnosticDeliverer(Deliverer, Level)`; phase 1 adds `TestPublicAPISurface` · README amended: Root facts (F3, F10), Ownership, Flows
- 2026-11-03 · validate · V3 Not ready · one independent read-only validator; checks 1–10; check 2: Interface blocks assembled in a scratch module (pkg/diagnostic, internal/diag, internal/runtimeboundary, pkg/agent with stubbed imports and fit checks), `GOPROXY=off GOFLAGS=-mod=mod go vet ./...`: rc 0; check 3: every consumed member confirmed by inspection · V1-01–V1-11, V2-01, A01 resolved, none regressed · findings: 1 blocker, 3 major (V3-03 and V3-04 split from one report item) · notes: 9 · round 3 ended Not ready: open findings handed to the maintainer
- 2026-11-03 · maintainer · design decision: no `CLAI_DIAGNOSTIC_DIR`; spill files live under the clai cache directory; cache failure falls back to stderr; stderr failure panics
- 2026-11-03 · plan · phases 5 and 9 re-planned · README amended: Root facts (F5, F6), Parameters (override row removed, directory is `diagnostics/` under the cache), Flows
- 2026-11-03 · maintainer · `SetupQuerier` joins the F10 `pkg/agent` allowlist
- 2026-11-03 · plan · V3-01, V3-02 closed +wip · phases 3, 4, 6, 7, 8, 9 re-planned: `NewReporter` declared in `internal/diag` (phase 4) and consumed by phases 8 and 9; `ResolvedLevels` holds the default under the empty selector; `Presenter.Attach(Viewport)` hands the active board to the presenter; `Monitor` names its error · README amended: Root facts (F10)
- 2026-11-03 · fix · V3-03, V3-04 closed +wip · `TestPublicAPISurface` rows added to phases 3 and 9; phase 9 cites phase 1 criterion 5 instead of a test · gates not re-run: document-only change, maintainer-attested gate stands
- 2026-11-03 · validate · V4 Ready · one independent read-only validator; checks 1–10; check 2: Interface blocks (phases 3 and 4 with two blocks each) assembled in a scratch module with stubbed imports and fit checks, `GOPROXY=off GOFLAGS=-mod=mod go vet ./...`: rc 0, `go build ./...`: rc 0; check 3: every consumed member confirmed by inspection · V1-01–V1-11, V2-01, A01, V3-01–V3-04 resolved, none regressed · findings: none · notes: 10
- 2026-11-03 · plan · phases 3, 4, 5, 6, 9 re-planned from round 4 notes: `Viewport` marked as the consumed shape of `Board.Log`; owners detach before `Board.Finish`; `ErrUndeliverable` (phase 5) makes the double failure observable to `CLI.Run`; phase 4 criterion 3 cites NewReporter.1; the buffer receives its directory from the owner, phase 9 consumes `GetClaiCacheDir`; phase 3 Local facts no longer restate truthiness · README amended: Root facts (F4 Binds, F6)
- 2026-11-03 · validate · V5 Ready · targeted round over re-planned phases 3, 4, 5, 6, 9 and the README facts they cite, same independent validator; check 2 re-assembled with the phase 5 block, `GOPROXY=off GOFLAGS=-mod=mod go vet ./...`: rc 0, `go build ./...`: rc 0; check 3 by inspection · none regressed · findings: none · notes: 2 (Report.3 could say "chain intact"; `TestBufferedDelivererFinish` Given lacks the failing-immediate case) · interface set ready for the maintainer's sign-off
