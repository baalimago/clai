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
| JSON format     | The `-json` output encoding: one record per line (F11, F12).     |
| Record          | One JSON object on one line, with a `type` from Record types.    |

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

### JSON run

```text
user runs clai -json
       |
       v
CLI.Run (phase 9)
  |
  +-- stdio.New(Config{Format: FormatJSON, ...}) (phase 4.1.1.1) --> StreamProvider
  |
  +-- StreamProvider.Encoder(Stdout | Stderr) ------------------> Encoder, one per destination
  |
  +-- NewJSONPresenter(stderr Encoder, level) (phase 6) --------> Presenter
  |       Presenter.Deliver(Event) --> Encoder.Encode("diagnostic")
  |       Presenter.ReportError(err) --> Encoder.Encode("error")
  |
  +-- NewJSONDisplayer(Encoder) (phase 4.1.2) ------------------> Displayer per destination
  |       Displayer.ReplyDelta | ReasoningDelta | Reply | ReportToolCall
  |       | ReportToolResult | ReportProgress | ShowPrompt --> Encoder.Encode
  |
  +-- NewWrapper(stderr Encoder, "child_output", source) (phase 4.1.1) --> Wrapper
  |       Wrapper.Write(MCP server stderr) --> Encoder.Encode("child_output")
  |
  +-- StreamProvider.Writer(Stdout | Stderr) ------------------> output Wrapper
  |       Wrapper.Write(any other text) --> Encoder.Encode("output")
  |
  `-- StreamProvider.Input() ----------------------------------> stdin
          Read --> Wrapper.Flush (both outputs), then the read
```

Input is read as it is without `-json`; the question before a read is a
record that reaches the stream before the read starts. Every destination is
the one the text form has.

A tool error that the agent must handle stays on the existing tool error path.
It does not enter the diagnostic buffer. A failed `Report` changes neither
the result nor the error of the call (F6).

## Status board

**Line:** running

| #   | Node                                                | Parent | Class       | Interface                 | Status      |
| --- | --------------------------------------------------- | ------ | ----------- | ------------------------- | ----------- |
| 1   | [Error context](phase-1-error-context.md)           | root   | Contract    | `Locator`                 | Complete    |
| 2   | [Remedies](phase-2-remedies.md)                     | root   | Contract    | `Remedier`                | Complete    |
| 3   | [Level policy](phase-3-level-policy.md)             | root   | Contract    | `LevelResolver`           | Complete    |
| 4   | [Event reporting](phase-4-event-reporting.md)       | root   | Contract    | `Reporter`                | Complete    |
| 4.1 | [Record encoding](phase-4.1-record-encoding.md)     | root   | Contract    | `Encoder`                 | Complete |
| 4.1.1 | [Line wrapping](phase-4.1.1-line-wrapping.md)     | root   | Contract    | `Wrapper`                 | Complete |
| 4.1.1.1 | [Streams](phase-4.1.1.1-streams.md)             | root   | Contract    | `StreamProvider`          | Complete     |
| 4.1.2 | [Display](phase-4.1.2-display.md)                 | root   | Contract    | `Displayer`               | Complete    |
| 5   | [Buffered reporting](phase-5-buffered-reporting.md) | root   | Contract    | `BufferedDeliverer`       | Complete    |
| 6   | [Diagnostic output](phase-6-diagnostic-output.md)   | root   | Contract    | `Presenter`               | Complete    |
| 7   | [Runtime boundary](phase-7-runtime-boundary.md)     | root   | Contract    | `DispatchMonitor`         | Complete    |
| 8   | [Agent diagnostics](phase-8-agent-diagnostics.md)   | root   | Contract    | `SetupQuerier`            | Complete    |
| 9   | [CLI composition](phase-9-cli-composition.md)       | root   | Composition | `CLI.Run`                 | Complete    |
| 10  | [Prune](phase-10-prune.md)                          | root   | Prune       | Removes table in phase 10 | Complete    |

### Ownership and dependencies

| Phase | Owns                                                                                     | Depends on                   |
| ----- | ---------------------------------------------------------------------------------------- | ---------------------------- |
| 1     | Public error context, locations, and the migration of clai-owned error-context wrappers. | Existing Go error contracts. |
| 2     | Public display-only remedy contract.                                                     | Existing Go error contracts. |
| 3     | Public `Level`; internal subsystem registry, legacy-control mapping, and level resolution. | None.                      |
| 4     | Public event, reporter, and deliverer contracts with the `slog` adapter; internal routing reporter. | 3.                 |
| 4.1   | The JSON record encoder, the `Format` selection, and the `diagnostic` record mapping.    | 3, 4.                        |
| 4.1.1 | Wrapping unstructured text and child output into records.                                | 4.1.                         |
| 4.1.1.1 | The per-run stdout, stderr, stdin, and terminal states every producer uses.            | 4.1, 4.1.1.                  |
| 4.1.2 | The JSON display contract for reply, reasoning, tool activity, progress, and prompts.   | 4.1.                         |
| 5     | Internal per-run buffering and spill-file reporting.                                     | 4.                           |
| 6     | Internal diagnostic presentation and coordinated terminal output, in text or JSON format. | 1, 2, 3, 4, 4.1.            |
| 7     | Dispatch and shutdown integration contracts.                                             | 4.                           |
| 8     | Optional diagnostic delivery for `pkg/agent`.                                            | 4.                           |
| 9     | CLI wiring and migration of diagnostic producers, progress, and legacy controls; the `-json` flag, its producers, and the migration of every direct stream write. | 1–8, 4.1, 4.1.1, 4.1.1.1, 4.1.2. |
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
need is a design andon, except one maintainer-approved addition (2026-11-09):
`cmd.RunSilentTo`, `RunSilent` with the writer completion output goes to, and
`cmd.IsCommandFailure`, which tells a command's own failure from a dispatch
failure. The R4 fix (2026-11-10) added, under its brief and pending the
maintainer's acceptance at the root Human check, `table.WithPrompter`, the
function a table shows its selection prompt with (R4-01), and
`cmd.MisplacedFlagError.Reason` and `Hint`, the error's parts (R4-05).

The worklog does not add prompt, response, tool payload, or MCP output capture
as diagnostic detail. It does not change the existing `pkg/agent.WithLogger`
message-record contract. File cleanup remains the user's responsibility.

The JSON format adds no input protocol: no reply records and no structured
stdin. Go runtime crash output (`panic`, `fatal error`) is outside F12.
Commands other than the query and chat paths get no records of their own; the
`output` record carries them. The Minutes page in the parent repository, the
first consumer of `-json`, is changed outside this worklog.

## Root facts

**F1 — Output ownership.** Command results and model replies use their result
writer. Diagnostics use the diagnostic writer. Required prompts and child
output keep their explicit destinations. This applies to raw, structured, and
reply-only modes. Binds `CLI.Run`, `Presenter`, `Displayer`, `DispatchMonitor`.

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
`BufferedDeliverer`, `NewBufferedDeliverer`, `CLI.Run`.

**F5 — Bounded buffer.** A buffer of the Shared buffer size flushes to a new
timestamped file. An oversized diagnostic is truncated and marked. Each flush
uses a separate file in the Spill-file directory. Files remain for the user
to inspect and remove. At the end of the run, the diagnostic output lists the
file locations in flush order and shows the remaining buffered diagnostics.
It does not print spill-file contents. Memory use does not grow with the
number of diagnostics. Binds `BufferedDeliverer`, `NewBufferedDeliverer`, `CLI.Run`.

**F6 — Output fallback.** If a spill-file write fails, the buffer goes to the
immediate Deliverer and resets. If that delivery fails too, nothing can be
reported: `BufferedDeliverer` returns an error matching `ErrUndeliverable`
that retains both failures, and `CLI.Run` aborts the process with a panic. Short of that, a diagnostic delivery failure does not
cancel a running operation, and a failed `Report` changes neither the result
nor the error of an agent call. Binds `Reporter`,
`Deliverer`, `SlogDeliverer`, `BufferedDeliverer`, `NewBufferedDeliverer`,
`Presenter`, `SetupQuerier`, `CLI.Run`.

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
`NewDispatchMonitor`, `SetupQuerier`, `CLI.Run`.

**F10 — Public surface.** `pkg/diagnostic` exports only what a Go
application uses: `SourceLocation`, `Locator`, `Remedy`, `Remedier`, `Level`
and its constants, `OutputMode` and its constants, `Type`, `Event`,
`Deliverer`, `SlogDeliverer`, `Reporter`. `pkg/agent` adds only
`SetupQuerier`, `DiagnosticTypes`, `WithDiagnosticReporter`,
`WithDiagnosticDeliverer`.
Level resolution, buffering, presentation, the routing `Reporter`, and every
constructor they need live in `internal/diag`, `internal/jsonl`,
`internal/display`, or `internal/stdio`. Binds `Locator`,
`LevelResolver`, `Reporter`, `BufferedDeliverer`, `Presenter`,
`WithDiagnosticDeliverer`, `CLI.Run`.

**F11 — Output format.** `-json` selects the JSON format for one run. The
format changes the encoding only: every destination F1 assigns, every input,
and every exit status stay as they are without `-json`, except the refusal
F13 names. Without `-json` the text format applies. Binds `Encoder`,
`Presenter`, `Displayer`, `StreamProvider`, `CLI.Run`.

**F12 — JSON lines.** In the JSON format, every byte clai writes to stdout and
stderr belongs to one JSON object per line, ending in "\n", with a string
`type` first. No ANSI sequence, `\r` redraw, spinner, glyph decoration,
markdown rendering, table, or alignment is written; a state is a field, and
external text is a JSON string. Text without a structured record is carried
by the `output` record, and relayed child output by `child_output`. Each
destination has one Encoder per run, which every record writer for it uses,
and producers see stdout and stderr as non-terminals so they write their pipe
form. Line integrity holds per destination; a consumer that merges stdout and
stderr into one pipe gets whole lines only up to the pipe's atomic write size.
Binds `Encoder`, `Wrapper`, `Displayer`, `Presenter`, `StreamProvider`,
`CLI.Run`.

**F13 — Interaction.** In the JSON format, a question is a `prompt` record,
a library-drawn table's selection prompt included, that reaches the stream
before its read; the answer is read from the same input, in the same way, as without
`-json`, and the caller types or pipes it. A prompt that is skipped without a
terminal is skipped under `-json` too; stdin's terminal state is never
changed by the format. `audio_record` stops on input exactly as without
`-json`. `$EDITOR`, the one child that takes over the terminal, is refused
before it starts with an `error` record naming `$EDITOR` and, when the edit
target is a file the user can edit directly, its path. Binds `Prompter`,
`Wrapper`, `StreamProvider`, `CLI.Run`.

**F14 — Record types.** Every record is one of these. Fields not listed are
not written; a field marked optional is omitted when empty.

| Type           | Fields                                                                                                                               |
| -------------- | ------------------------------------------------------------------------------------------------------------------------------------ |
| `reply_delta`  | `text` (string)                                                                                                                      |
| `reasoning_delta` | `text` (string)                                                                                                                   |
| `reply`        | `text` (string)                                                                                                                      |
| `tool_call`    | `name` (string), `inputs` (object, `{}` when none)                                                                                   |
| `tool_result`  | `name` (string), `ok` (bool: false exactly when the output the model receives starts with `ERROR: `), `output` (string), `error` (optional string: the rest of that first line) |
| `progress`     | `title` (string), `row` (optional string), `state` (one of `start`, `update`, `done`, `failed`), `detail` (optional string)          |
| `prompt`       | `kind` (one of `trust`, `select`, `confirm`, `calibrate`), `text` (string), `choices` (optional array of strings)                    |
| `diagnostic`   | `name`, `subsystem`, `level` (F7 level name), `message` (strings), `facts` (object, `{}` when none)                                  |
| `error`        | `message` (string), `causes` (array of objects: `message` (string); optional `operation` (string); optional `location` (string `file:line`); optional `remedies` (array of strings); optional `status` (number); optional `provider_code` (string); optional `stack` (array of strings, one frame each)) |
| `child_output` | `source`, `stream` (`stdout` or `stderr`), `text` (strings)                                                                          |
| `output`       | `stream` (`stdout` or `stderr`), `text` (strings)                                                                                    |

Binds `EventRecord`, `Presenter`, `Displayer`, `Wrapper`, `CLI.Run`.

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
layer in row order, and a later row replaces the same selector. A `LOG_LEVEL`
entry replaces the legacy entry for the same selector (Resolve.2); a legacy
selector `LOG_LEVEL` does not name stays. A truthy value is one `misc.Truthy` accepts. Subsystem
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
| Release `go_away_boilerplate` with `cmd.RunSilent`, `cmd.HelpRequestedError`, `cmd.RunSilentTo`, `cmd.IsCommandFailure`, `table.WithPrompter`, and `cmd.MisplacedFlagError.Reason`/`Hint`, bump the requirement, and drop the `replace` line from `go.mod`. | `go mod edit -dropreplace github.com/baalimago/go_away_boilerplate && go get github.com/baalimago/go_away_boilerplate@<tag> && go mod tidy && make qa` | root |

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
| A02   | design andon | 4 | maintainer | Closed +wip | `SlogDeliverer` lacks the registered level and subsystem required by SlogDeliverer.1: its only input is `Event`, which contains only `Type`, `Message`, and `Facts`, while `SlogDeliverer(slog.Handler)` receives no registry; the member cannot bind the subsystem before `Enabled` or consult `Enabled` at the event level without changing the signed-off interface · decided: `Event` gains `Subsystem` and `Level`, filled by the routing `Reporter` from the registered `Type` |
| V6-01 | blocker | 4.1.2 | plan | Closed +wip | check 9 · phase-4.1.2 `NewTerminalDisplayer.1` + criterion 4 (`TestTerminalDisplayer`: "The bytes equal today's output of the replaced `internal/utils` and `internal/board` drawing") · today's boards need `live` (the destination's terminal state), a `board.Config` with columns, multi-cell `Row`s, the session width, and a clock (`board.New(out, live, cfg, rows)`; `internal/chat/handler_summarize.go:331`, `internal/audio/board.go:32`), but `NewTerminalDisplayer(out io.Writer, style look.Style)` and `Progress{Title, Row, State, Detail}` carry none of them (`look.Style` is only `Color`), and a board drawn inside the Displayer is no `Viewport` phase 9 can hand to `Presenter.Attach` (phase 9 Local facts); give the terminal Displayer its terminal state and board shape as inputs and expose the Viewport, or narrow criterion 4 and keep boards with their commands |
| V6-02 | major | 4.1 | fix | Closed +wip | check 9 · phase-4.1 `Encode.1` ("A group attribute becomes a nested object, an empty group \"{}\"; a slice becomes an array; a nil error field is omitted") · it does not say how a map, a slice's elements, or a nil, bool, or number `any` value is encoded, yet phase 6 ReportError.3 needs `causes` as an array of objects and phase 4.1.2 `ReportToolCall` must write `ToolCall.Inputs` (`map[string]any` of decoded model JSON, possibly nested arrays, objects, and nulls) as the F14 `inputs` object; state the encoding of every value kind those callers pass, with a `TestEncoderContract` row |
| V6-03 | major | 4.1.1 | fix | Closed +wip | check 9 · phase-4.1.3 `Input.1` ("each Read first flushes the Wrappers of both outputs") and `Writer.1`, phase 9 ("lets every other write reach the `output` Wrapper of its destination") · the shared `output` Wrapper replaces `*os.File`, which is safe for concurrent writes, but no Wrapper clause makes `Write`, `Flush`, and `Close` safe for concurrent use; a goroutine writing stderr text while another reads `Input()` races on the pending line under the `-race` gate; add a concurrency clause to the Wrapper and a concurrent Write/Flush row to `TestWrapperContract` |
| V6-04 | major | 6 | fix | Closed +wip | check 5 · phase-6 `ReportError.3` ("with operation and location, remedies, and the provider status and provider code of a claierr API error when present") vs README F14 `error` row, where only `operation` is marked optional and `location`, `remedies`, `status`, `provider_code` must be written · the clause restates F14's field list and disagrees with it: a non-API cause, or any cause below `LevelDebug` (ReportError.2), has no status, provider code, or location to write; decide in F14 which cause fields are optional and make ReportError.3 cite F14 instead of restating it |
| V6-05 | major | 4.1.2 | fix | Closed +wip | check 5 · phase-4.1.2 `ReportToolResult.1` ("It is a failure exactly when Output starts with \"ERROR: \"") restates the README F14 `tool_result` rule ("false exactly when the output the model receives starts with `ERROR: `") · one literal stated at two nodes; cite F14 in the clause instead of restating the prefix |
| V6-06 | major | 9 | fix | Closed +wip | check 5 · phase-9 synopsis ("JSON and $EDITOR: refused before it starts with an \"error\" record naming $EDITOR and, when the edit target is a file the user can edit directly, its path"; "a prompt skipped without a terminal is skipped under -json too") restates README F13 word for word · cite F13 as the synopsis cites the README parameters (V1-08), keeping only what F13 does not define (exit status 1) |
| V6-07 | major | root | fix | Closed +wip | check 4 · README F12 "Binds `Encoder`, `Wrapper`, `Displayer`, …" · no `Displayer` clause cites F12 (Displayer.1 cites F1, F11; the method clauses cite F13, F14); only the constructor clause NewJSONDisplayer.1 does; bind `NewJSONDisplayer` in F12, or cite F12 in a Displayer clause |
| V6-08 | minor | 4.1.3 | plan | Closed +wip | check 7 · phase-4.1.3 `StreamProvider` · one interface of six methods and several verbs (write, encode, report terminal state, read, close) without embedding; split it into small embedded interfaces as `Displayer` does |
| V7-01 | major | 4.1 | fix | Closed +wip | check 4 · phase-4.1 `Encode.1` ("an error as its Error text, and a nil error field is omitted" … "nil as null", the latter added by the V6-02 fix) · `slog.Any("error", error(nil))` and `slog.Any("x", nil)` are the same `slog.Value` (KindAny holding nil; `reflect.DeepEqual` true under Go 1.27.3), so no Encoder can omit one and write `null` for the other, and `TestEncoderContract`, whose Given holds both "nil errors" and "nil", cannot pass; drop the nil-error omission (callers leave absent optional fields out, as F14 requires) or tie omission to something the `Record` carries, and adjust the test row |
| A03 | design andon | 5 | maintainer | Closed +wip | `BufferedDeliverer` (phase 5) has no constructor: the signed interface declares only the interface and `ErrUndeliverable`, while Local facts say "the owner supplies the immediate Deliverer and the Spill-file directory" and no member carries them in; `TestDelivererContract` (phase 4's suite in `pkg/diagnostic` package `diagnostic_test`) cannot build an `internal/diag` implementation without an exported constructor, and phase 9 consumes only `Finish` and `ErrUndeliverable`, so it cannot build one either (same gap as V3-02 for `NewReporter`); declare e.g. `NewBufferedDeliverer(dir string, immediate diagnostic.Deliverer) BufferedDeliverer` (buffer size from the Shared buffer size parameter, or as an argument so the tests need not write 1 MiB), add it to phase 9 Consumes, and say whether phase 5 adds its implementation to phase 4's `TestDelivererContract` map or runs its own copy |
| V9-01 | blocker | 4.1 | fix | Closed 0ea7c95+wip | check 2, 3 · `internal/jsonl/encode.go` (phase 4.1 body: `import "github.com/baalimago/clai/internal/diag"`, nine `diag.Errorf` calls) vs the re-planned phase-6 Interface (`internal/diag/output.go` imports `internal/jsonl` for `NewJSONPresenter(enc jsonl.Encoder, level diagnostic.Level)`) · the Interface blocks of phases 5, 6, 7 over the working tree fail `go build ./...` with `internal/diag imports internal/jsonl from output.go imports internal/diag from encode.go: import cycle not allowed`, so phase 6 cannot be completed, and `pkg/diagnostic`'s `TestDelivererContract` (which phases 5 and 6 join) cannot compile either; remove the `internal/diag` import from `internal/jsonl` without changing a signature: either move the `Errorf` implementation (with `Locator`, location capture) into a leaf internal package that imports only `pkg/diagnostic`, which both `internal/diag` and `internal/jsonl` use (its `fmt.Errorf(format, …)` has a non-constant format, so phase 1's `TestLocatorInventory` stays green), or return typed errors with `Unwrap` (these attach context without a location, so check them against F8 and Location.1 first); keep `errors.Is(err, ErrInvalidRecord)` true; fix together with V9-02, which shares the package |
| V9-02 | blocker | 4.1.1 | fix | Closed 0ea7c95+wip | check 2, 3 · `internal/jsonl/wrapper.go` (phase 4.1.1 body: `diag.Errorf` at lines 64 and 151, same `internal/diag` import) · the same import cycle as V9-01: `internal/jsonl` is one package, so phase 6's `internal/diag` → `internal/jsonl` import still closes the cycle while either file imports `internal/diag`; apply the V9-01 remedy here, keeping `errors.Is(err, ErrWrapperClosed)` true |
| R1-01 | major | 9 | fix | Closed 0ea7c95+wip | `internal/runenv/runenv.go:42-48` package-level `fallback` set only by test binaries (CLAUDE.md: no package state), so producers given `context.Background()` print in tests but are silent in clai: the config-migration notice (`internal/text/config_migration.go:42`, `ancli.PrintOK` at HEAD) and `DEBUG_SUMMARY` traces (`summary_launch.go:170-178`, `cmd.go:402`) never show (F9, Legacy controls); thread the run ctx, drop or confine `SetFallback`, add `cli.Run` scenarios |
| R1-02 | major | 9 | fix | Closed 0ea7c95+wip | Golden diff row `json-error.golden` ("status and provider code") unmet: the golden has `status` 401 but no `provider_code`, because `internal/vendors/openai/decode_error.go:26-30` sets `ProviderCode` only for insufficient quota; `TestJSONModeScenarios` asserts status only; the web consumer of a 401 gets no provider code |
| R1-03 | major | 9 | fix | Closed 0ea7c95+wip | F11 inputs/exit status: the JSON `Input()` (`internal/stdio/provider.go:118` `flushingReader`) hides `Stat`, so `utils.PipedStdin` reads `/dev/null` as an empty pipe; `clai -cm test q </dev/null` exits 1 (no prompt) but `clai -json -cm test q </dev/null` sends an empty query and exits 0 (reproduced) |
| R1-04 | major | 3 | plan | Closed +wip | README Legacy controls "`LOG_LEVEL` replaces any of them" (and `architecture/output.md:40-41`) vs Resolve.2 same-selector replacement as coded (`internal/diag/levels.go:59-70`): held to the stricter reading, `DEBUG_SKILLS=1 LOG_LEVEL=warn` still shows skills debug, pinned by `internal/cli/skills_compat_test.go:30`; decide the reading, then amend the text or the code and flip the compat row |
| R1-05 | major | 9 | fix | Closed 0ea7c95+wip | F12, criterion 12: `clai -json completion bash` and `__complete` write raw text to stdout (go_away_boilerplate prints with `fmt`); refuse or wrap clai-side under `-json`, or plan records the exception in F12/Scope |
| R1-06 | minor | 9 | fix | Closed +wip | (fix 2026-11-09: `session` removed and the testenv prefix narrowed to three named harness files; criterion 17's list still omits the test harness, whose TestMain, `Quiet` and `procenv` must touch the process streams — narrowing further needs criterion 17 amended, plan) `TestJSONNoDirectWrites` (`e2e/diagnostic_static_test.go:183-192`) and `TestDiagnosticRuntimeAncli` exempt all of `internal/testenv/`, wider than criterion 17's allowlist; `internal/setup/terminal.go:12` adds package-level `var session` reassigned per run |
| R1-07 | minor | 10 | fix | Closed 0ea7c95+wip | Criterion 1 cites `TestPrunedSkillsDiagnostics`, which does not exist; the proof is a grep recorded once, so no gate guards the removed names |
| R1-08 | minor | 3 | plan | Closed +wip | `internal/diag/levels.go:51` exports `NewLevelResolver()`, which the phase-3 Interface does not declare (same gap class as A03) |
| R1-09 | minor | 1 | plan | Closed +wip | `Locator` producer `internal/errctx` (`Errorf`, `ErrorfWithStack`, `ErrorfAt`, …), called directly by `internal/jsonl`, is declared by no interface and not run through `TestLocatorContract` (`pkg/diagnostic/context_test.go:42`) |
| R1-10 | minor | 6 | plan | Closed +wip | Deliver.3 "the configured theme on a terminal" vs the fixed level palette in `internal/diag/presenter.go:16-23,166-177`; `NewTextPresenter` cannot carry a theme |
| R1-11 | minor | root | fix | Closed 0ea7c95+wip | Fakes outside the contract suites: Deliverer fakes `immediateRecorder`/`partialDeliverer` (`internal/diag/buffer_test.go:29,370`), `recordingDeliverer` (`pkg/agent/diagnostics_contract_test.go:46`); Reporter fakes `recordingReporter` (`pkg/agent/diagnostics_contract_test.go:28`, `internal/runtimeboundary/runtime_test.go:304`), `isolationReporter` (`e2e/diagnostic_parity_test.go:57`); the suites sit in `package diagnostic_test` and cannot be joined |
| R1-12 | minor | root | maintainer | Open | Human check "Release `go_away_boilerplate` with `cmd.RunSilent` … `make qa`" (certifies root) has no journal record |
| R1-13 | minor | 9 | fix | Closed 0ea7c95+wip | `error.golden` also dropped the `failed to run: ` prefix, a change its Golden diff row does not state (byte baseline rebuilt from 0ea7c95 by the reviewer) |
| A04 | quality andon | root | fix | Closed 0ea7c95+wip | own: the MCP stderr reader is never awaited and the `child_output` Wrapper writes into the run's stderr after `cli.Run` returns (`internal/tools/mcp/conn_stdio.go:243` → `internal/text/mcp_log_sink.go:492` → `jsonl` Encode) · `go test ./... -race -cover -count=3 -timeout=30s` · run 1 FAIL `e2e/tooling` `TestJSONModeScenarios/MCP_server_writing_stderr` (missing `child_output` line + DATA RACE with `json_mode_e2e_test.go:65`), run 2 (`-shuffle=on`) pass · 0ea7c95+wip |
| A05 | design andon | 6 | maintainer | Closed +wip | phase-6 ReportError.3 ("showing what ReportError.1 and ReportError.2 show at its Level", i.e. the origin stack at `LevelTrace`) cannot be met with F14's `error` causes, which have no `stack` field and write no unlisted field; `-json` can never carry F8's trace stack; decide: an optional F14 cause `stack` (minor, injected phase) or amend ReportError.3 to "within F14's fields" |
| R1-14 | major | 6 | fix | Closed 0ea7c95+wip | A05 reset (patch): the JSON `error` record carries each cause's origin stack at LevelTrace in the optional F14 `stack` field (array of frames), as ReportError.3 already requires; add the case to `TestPresenterJSON` |
| R2-01 | major | 9 | fix | Closed 0ea7c95+wip | F9, criterion 16: a failure of go_away_boilerplate's built-in `completion` command (`clai completion fish`, `clai completion`) prints the full usage on stdout before the error, in text and JSON; HEAD printed only the error. Cause: it never calls `markDispatched`, so `presentOutcome` (`internal/cli/compose.go:360,383`) treats it as a dispatch failure |
| R2-02 | major | 9 | fix | Closed 0ea7c95+wip | R1-01 residual: the `audio_transcribe` tool engine (`internal/audio/create_querier.go:97,116`) runs on `context.Background()`, so its split/diarization progress board (HEAD: `os.Stderr`) and the audioConfig upgrade notice are discarded. No board in text mode and no `progress` records under `-json` for the Minutes transcription path |
| R2-03 | minor | root | fix | Closed 0ea7c95+wip | `internal/testenv/procenv/procenv.go:43` `envReporter`, a Reporter fake that tests in many packages use, runs through no `TestReporterContract`, and it re-resolves levels using its own legacy-key list (R1-11 class) |
| R2-04 | minor | 3 | fix | Closed 0ea7c95+wip | `architecture/output.md:40-41` still says "`LOG_LEVEL` replaces them" (the legacy layer). R1-04 named this file, but only the README was amended to the same-selector reading of Resolve.2 |
| R3-01 | major | 9 | fix | Closed 0ea7c95+wip | sign-off spawn-review (falsifier, CONFIRMED) · `internal/text/tool_executor.go:390,504,606` guard the JSON tool records with `!q.debug`; at LOG_LEVEL=debug/DEBUG=1 `-json` drops `tool_call`/`tool_result` that text mode prints (F11, F14) |
| R3-02 | major | 9 | fix | Closed 0ea7c95+wip | sign-off spawn-review (seams, CONFIRMED) · `run_summary.go:86`, `loop_summary.go:78`, `step_retry.go:79-89`, `modelfallback.go:56` write `look`/▸ text to stderr under `-json`, so summaries, retries and fallbacks are decorated `output` records; F12 (a state is a field): emit `progress`/`diagnostic` records under `-json` |
| R3-03 | major | 9 | fix | Closed 0ea7c95+wip | sign-off spawn-review (falsifier, CONFIRMED) · clai's own questions (`chat/handler_summarize.go:136`, `setup/setup_actions.go:1003`, the audio calibration acknowledgement) are not `prompt` records; only `trust` is emitted (F13, F14 kinds `confirm`, `select`, `calibrate`) |
| R3-04 | minor | 9 | fix | Closed 0ea7c95+wip | sign-off spawn-review (seams, CONFIRMED) · `text/remote_profile.go:176` trust `prompt` text is the terminal-look `FormatRemoteTrustPrompt` (▸, aligned rows, `[y/N]`); F12 |
| R3-05 | minor | 4.1.1 | fix | Closed 0ea7c95+wip | sign-off spawn-review (falsifier, CONFIRMED) · `jsonl/wrapper.go:85-86` passes ESC sequences other than CSI/OSC (`ESC(B`, `ESC7`, `ESC8`) into record text; Write.2, F12 |
| R3-06 | minor | 4.1.2 | fix | Closed 0ea7c95+wip | sign-off spawn-review (falsifier, CONFIRMED) · `display/json.go:47` keeps the `\r` of a CRLF first line in `tool_result.error` |
| R3-07 | minor | 9 | fix | Closed 0ea7c95+wip | sign-off spawn-review (seams, PLAUSIBLE) · `audio/record.go:214-217` reads stdin from `runenv`; a `pkg/agent` run carries no Env, so library callers lose Enter-to-stop (F3) |
| R3-08 | minor | 9 | fix | Closed 0ea7c95+wip | found by the R3 fix run (Surprises) · under `-json`, `clai cloud` output keeps its ▸ look styling inside `output` records, and `cloud pull`'s `Keep [l/r/p/q]` question is not a `select` prompt (F12, F13) |
| R4-01 | major | 9 | fix | Closed 0ea7c95+wip | holistic review 2026-11-10 · F13, F14 · every go_away_boilerplate table selection (`chat list`, the message picker, `setup` categories, configs, fields, tools, slice elements, list choices, the Hub publish selectors) wrote its prompt line `(select, [p]rev, …): ` as an `output` record on stdout; only the six `ShowPrompt` sites wrote `prompt` records |
| R4-02 | minor | 9 | fix | Closed 0ea7c95+wip | holistic review 2026-11-10 · R3-08's class · besides `clai cloud` (R3-08: ▸ heads, → links, ■ rows, aligned columns, the `Keep [l/r/p/q]` question), `clai mcp auth` wrote `▸ mcp.auth` stages under `-json` and its printed-URL fallback's paste question was an `output` record (F12, F13) |
| R4-03 | major | 8 | fix | Closed 0ea7c95+wip | holistic review 2026-11-10 · `pkg/agent` `runContext` built a run Env without a Reporter, so every `runenv.Warnf/Noticef/Failuref/Debugf` of a library run was dropped although the caller passed `WithDiagnosticReporter` or `WithDiagnosticDeliverer`; decision (provisional, fix brief): internal events reach the caller, `DiagnosticTypes()` holds the internal registry, the CLI registers each type once |
| R4-04 | major | 9 | fix | Closed 0ea7c95+wip | holistic review 2026-11-10 · F1, F9, F11 · `internal/text/querier_setup.go:196` set `querier.debug` from `DebugEnabled(text_querier)`, so `LOG_LEVEL=debug` (and `DEBUG`, `DEBUG_TEXT_QUERIER`) stopped token streaming: no `reply_delta` under `-json`, the reply printed whole after a blank line in text; on main only `DEBUG`/`DEBUG_TEXT_QUERIER` did this, and the Legacy controls table maps them to levels only |
| R4-05 | minor | 6 | fix | Closed 0ea7c95+wip | holistic review 2026-11-10 · F14, ReportError.1, ReportError.3 · usage error messages kept their trailing `\n` (an empty continuation line in text, `\n` in JSON); a misplaced flag's `Hint:` sat inside the message instead of `remedies`/`hint:`; a missing API key error carried no remedy naming the variable |
| R4-06 | minor | 9 | fix | Closed 0ea7c95+wip | holistic review 2026-11-10 · Deliver.3, Legacy controls `NO_COLOR` · `compose.go` gave the presenter `misc.Truthy(NO_COLOR)` and the producers `NO_COLOR != "true"`; stdout and stderr terminal states used a character-device check (true for `/dev/null`) while stdin used `internal/term.IsTerminal` |
| R4-07 | minor | 9 | fix | Closed 0ea7c95+wip | holistic review 2026-11-10 · `-json=false`/`--json=false` were refused as a misplaced flag; the misplaced global `-json` hint named `profiles validate`; `internal/jsonl/encode.go` held literal U+2028, U+2029 and U+FFFD (also `jsonl_test.go`, `remote/printable.go`); a stale `present` comment in `compose.go`; the config debug line printed directory and file without a separator |
| A06 | quality andon | root | fix | Closed 0ea7c95+wip | own (test) · `go test ./... -race -cover -count=3 -timeout=30s -shuffle=on` · R4 fix-start run: `internal/tools/mcp` `TestHttpConnCreatesNoChildProcess` "child process count went from 1 to 0", reproduced 1 of 3 runs with `-shuffle=1794298605363862057`: the test counted the test process's children, and an earlier test's stdio server (`TestConnectorCancelDuringResolutionReapsProcess` cancels and returns while the reaper goroutine still runs) exited between the two counts; the test now compares the child PID sets (a new child still fails it); 8 of 8 runs with the same seed pass · 0ea7c95+wip |

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
- 2026-11-08 · work · phase 1 Complete
- 2026-11-08 · work · phase 2 Complete
- 2026-11-08 · work · phase 3 Complete
- 2026-11-08 · plan · A02 closed +wip · phase 4 re-planned: `Event` carries the registered `Type`'s `Subsystem` and `Level`, so `SlogDeliverer` binds the subsystem and consults `Enabled` at the event level; SlogDeliverer.1 cites both
- 2026-11-08 · work · phase 4 Complete
- 2026-11-09 · plan · README amended: Glossary, Flows (JSON run), Status board (phases 4.1–4.3), Ownership, Scope, Root facts (F11–F14)
- 2026-11-09 · plan · phase 4.1 re-planned (new)
- 2026-11-09 · plan · phase 4.2 re-planned (new)
- 2026-11-09 · plan · phase 4.3 re-planned (new)
- 2026-11-09 · plan · phase 6 re-planned (`-json`: Deliver.5, ReportError.3)
- 2026-11-09 · plan · phase 9 re-planned (`-json` wiring)
- 2026-11-09 · review · spawn-review round 1 (principal-engineer, rules-lawyer) on the `-json` amendment: not ready, 20 findings (6 found by both)
- 2026-11-09 · plan · phases 4.2 and 4.3 renumbered 4.1.1 and 4.1.2 (L.k rule); phase 4.1.3 added (new)
- 2026-11-09 · plan · README amended: Flows (JSON run), Status board, Ownership, Root facts (F1, F10, F11–F14)
- 2026-11-09 · plan · phase 4.1 re-planned
- 2026-11-09 · plan · phase 4.1.1 re-planned
- 2026-11-09 · plan · phase 4.1.2 re-planned
- 2026-11-09 · plan · phase 4.1.3 re-planned
- 2026-11-09 · plan · phase 6 re-planned
- 2026-11-09 · plan · phase 9 re-planned
- 2026-11-09 · validate · V6 Not ready · checks 1–10 over the README and phases 1–10, focus on phases 4.1, 4.1.1, 4.1.2, 4.1.3, 6, 9 and the 2026-11-09 README amendments; check 2: the Interface blocks of phases 4.1, 4.1.1, 4.1.2, 4.1.3, 6 written over a scratch copy of the repository (bodiless functions stubbed, replace pointed at the sibling checkout) with a phase-9 fit file (`*board.Board` as `diag.Viewport`, `NewJSONPresenter` as `diagnostic.Deliverer`, `NewWrapper` with `source`/`stream`), `GOPROXY=off GOFLAGS=-mod=mod go vet ./internal/jsonl ./internal/display ./internal/stdio ./internal/diag ./internal/v6fit`: rc 0, `go build ./...`: rc 0; check 3 by inspection (`internal/look:Style`, `claierr.APIErrorer`, `board.Board.Log`, table `WithInput`/`WithWriter` present) · V1-01–V3-04, V2-01, A01, A02 resolved, none regressed · findings: 1 blocker, 6 major, 1 minor · notes: 7
- 2026-11-09 · plan · V6-01, V6-08 closed +wip · phase 4.1.2 re-planned: JSON Displayer only, text producers keep their drawing; phase 4.1.3 renumbered 4.1.1.1 (L.k) and re-planned: `StreamProvider` embeds `OutputProvider`, `InputProvider`, `io.Closer`
- 2026-11-09 · fix · V6-02, V6-03, V6-04, V6-05, V6-06, V6-07 closed +wip · Encode.1 value kinds; Wrapper Write.4 concurrency and cross-write `\r\n`; ReportError.3 and ReportToolResult.1 cite F14; phase 9 synopsis cites F13; Displayer.1 cites F12; F14 cause fields each optional · gates not run: document-only change
- 2026-11-09 · plan · phase 4.1 re-planned
- 2026-11-09 · plan · phase 4.1.1 re-planned
- 2026-11-09 · plan · phase 4.1.1.1 re-planned
- 2026-11-09 · plan · phase 4.1.2 re-planned
- 2026-11-09 · plan · phase 6 re-planned
- 2026-11-09 · plan · phase 9 re-planned
- 2026-11-09 · validate · V7 Not ready · checks 1–10 over the README and phases 1–10, focus on the 2026-11-09 amendments (Flows JSON run, F11–F14, phases 4.1, 4.1.1, 4.1.1.1, 4.1.2, 6, 9); check 2: the Interface blocks of phases 4.1, 4.1.1, 4.1.1.1, 4.1.2, 5, 6, 7, 8 written over a scratch copy of the repository (bodiless functions stubbed, replace pointed at the sibling checkout) with a phase-9 fit file (`*board.Board` as `diag.Viewport`, `NewJSONPresenter` as `diagnostic.Deliverer`, `NewTextPresenter` over `Writer(Stderr)`, `NewJSONDisplayer`, a `child_output` `NewWrapper`, every `StreamProvider` method), `GOPROXY=off GOFLAGS=-mod=mod go vet ./internal/jsonl ./internal/display ./internal/stdio ./internal/diag ./internal/runtimeboundary ./pkg/agent ./internal/v7fit`: rc 0, `go build ./...`: rc 0; check 3 by inspection (`board.Board.Log`, `diag.NewReporter`, `ResolvedLevels`, table `WithInput`/`WithWriter`); slog probe `go run .`: a nil error attr and a nil attr are equal values · V6-01–V6-08 resolved, none regressed · findings: 1 major · notes: 5
- 2026-11-09 · fix · V7-01 closed +wip · Encode.1: no nil-error omission, nil is null, callers leave absent optional fields out of the Record; notes: phase 4.1.1 criteria order, phase 6 criterion 8 wording, phase 9 Consumes add table `WithInput`/`WithWriter` · gates not run: document-only change
- 2026-11-09 · plan · phase 4.1 re-planned
- 2026-11-09 · validate · V8 Ready · targeted round over phase 4.1 (Interface, criteria, test rows, Error management), README F11, F12, F14, phase 4.1.1 criteria, phase 6 criterion 8, phase 9 Consumes; check 2: the Interface blocks of phases 4.1 and 4.1.1 written over a scratch copy of the repository (bodiless functions stubbed, replace pointed at the sibling checkout) with a fit file (a `Record` with a nil field through `NewEncoder`, `ErrInvalidRecord`, `EventRecord`, `NewWrapper` `Flush`/`Close`), `GOPROXY=off GOFLAGS=-mod=mod go vet ./internal/jsonl ./internal/v8fit`: rc 0, `go build ./...`: rc 0; check 3 by inspection (`Event`, `Level`, `Table.WithInput`/`Table.WithWriter` in the sibling `pkg/table`) · V7-01 resolved, none regressed · findings: 0 · notes: 3
- 2026-11-09 · maintainer · README signed (instructed in session: sign after a clean review and a Ready validation)
- 2026-11-09 · maintainer · interfaces 4.1, 4.1.1, 4.1.1.1, 4.1.2, 5, 6, 7, 8, 9, 10 signed (same instruction)
- 2026-11-09 · work · phase 4.1 Complete
- 2026-11-09 · work · phase 4.1.1 Complete
- 2026-11-09 · work · phase 4.1.1.1 Complete
- 2026-11-09 · work · phase 4.1.2 Complete
- 2026-11-09 · work · phase 5 · A03
- 2026-11-09 · plan · phase 5 re-planned (A03: `NewBufferedDeliverer`, `ByteSize`)
- 2026-11-09 · plan · phase 7 re-planned (A03 sweep: `SignalSource`, `NewDispatchMonitor`)
- 2026-11-09 · plan · phase 6 re-planned (A03 sweep: suite extension point)
- 2026-11-09 · plan · phase 9 re-planned (A03 sweep: Consumes)
- 2026-11-09 · validate · V9 Not ready · targeted round over re-planned phases 5, 6, 7, 9 and the README facts they cite (F4, F5, F6, F9, F10, Parameters, Flows); check 2: the Interface blocks of phases 5, 6, 7 (bodiless functions stubbed) written over a scratch copy of the working tree, including the uncommitted `internal/jsonl`, `internal/stdio`, `internal/display` of phases 4.1–4.1.2, replace pointed at the sibling checkout, `GOPROXY=off GOFLAGS=-mod=mod go build ./...`: FAIL, import cycle `internal/diag` (output.go) → `internal/jsonl` (encode.go) → `internal/diag`; with `internal/jsonl`'s `diag.Errorf` swapped for `fmt.Errorf` in scratch only, `go build ./...`: rc 0, and `go vet ./internal/diag ./internal/jsonl ./internal/stdio ./internal/runtimeboundary ./internal/v9fit ./pkg/diagnostic` (phase-9 fit file: `NewBufferedDeliverer`, `ByteSize`, `NewDispatchMonitor` over an `os/signal` `SignalSource`, both presenters; `TestDelivererContract` map entries for the buffer and both presenters): rc 0; `internal/stdio` imports `internal/diag` and `internal/jsonl` but nothing planned imports `internal/stdio`, so it closes no cycle; check 3 by inspection · V1-01–V8 findings resolved, none regressed; A03 open (maintainer) · findings: 2 blocker (V9-01, V9-02; phases 4.1 and 4.1.1 set Reopened) · notes: 7
- 2026-11-09 · plan · V9 notes 1–6: phase 5 re-planned (Consumes cites phase 4 criterion 4; NewBufferedDeliverer.1 edge cases; suite entry construction; test Given); phase 9 re-planned (`TestSignalSource`); README amended: Root facts (F4, F5, F6, F9 Binds)
- 2026-11-09 · fix · V9-01, V9-02 closed · `Errorf`/`ErrorfWithStack` implementation moved to leaf `internal/errctx` (imports only stdlib and `pkg/diagnostic`), `diag.Errorf`/`diag.ErrorfWithStack` delegate with unchanged signatures and caller locations, `internal/jsonl` calls `errctx.Errorf`; phases 4.1 and 4.1.1 Complete · fix-start gofumpt -w -l . && staticcheck ./... && go vet ./... && go test ./... -race -cover -count=3 -timeout=30s && go fix ./... && dupl -t 80 .: all rc 0, 90 packages ok, dupl 41 clone groups; fix-end same: all rc 0, 91 packages ok, 0 FAIL, dupl 41 clone groups; `go list -deps ./internal/jsonl`: no `internal/diag`; phases 5, 6, 7 Interface blocks (bodies stubbed) over a scratch copy, `GOPROXY=off GOFLAGS=-mod=mod go build ./...` and `go vet ./...`: rc 0
- 2026-11-09 · validate · V10 Ready · targeted round over re-planned phases 5, 6, 7, 9 and the README facts they cite (F1, F2, F4–F12, F14, Parameters, Flows), including the V9-note edits; check 2: the Interface blocks of phases 5, 6, 7 (bodiless functions stubbed) written over a scratch copy of the current working tree (uncommitted `internal/errctx`, `internal/jsonl`, `internal/stdio`, `internal/display`), replace pointed at /home/lorkin/Projects/not_wasmer/go_away_boilerplate, with a phase-9 fit file (`NewBufferedDeliverer`, `ByteSize`, `GetClaiCacheDir`, `NewReporter` over both presenters and the buffer, `*board.Board` through `Attach`, `NewDispatchMonitor` over an `os/signal` `SignalSource`, `Dispatch`, `Finish`/`ErrUndeliverable`, `NewJSONDisplayer`, a `child_output` `NewWrapper`) and `TestDelivererContract` entries for the buffer and both presenters in `pkg/diagnostic`, `GOPROXY=off GOFLAGS=-mod=mod go build ./...`: rc 0, `go vet ./internal/diag ./internal/jsonl ./internal/errctx ./internal/stdio ./internal/display ./internal/runtimeboundary ./internal/v10fit ./pkg/diagnostic`: rc 0, `go list -deps ./internal/jsonl`: `internal/errctx` only, no `internal/diag`; check 3 by inspection (phase 4 criterion 4, `Event.Type` string, `Board.Log`, `look` → `utils` → `diag` still holds) · V9-01, V9-02 resolved, none regressed; A03 open (maintainer) · findings: 0 · notes: 4
- 2026-11-09 · plan · phase 5 re-planned (V10 notes 1–3: empty dir fails every spill; suite entry size 0; `CLAI_CACHE_DIR` probe)
- 2026-11-09 · plan · phase 9 re-planned (V10 note 4: `TestSignalSource` proves Subscribe.1, F9)
- 2026-11-09 · maintainer · interfaces 5, 6, 7, 9 signed (standing instruction in session: run unattended, sign after a clean review and a Ready validation; V10 Ready)
- 2026-11-09 · maintainer · A03 closed · re-plan · missed by check 9 (a declared interface with no constructor its consumers can call)
- 2026-11-09 · work · phase 5 Complete
- 2026-11-09 · work · phase 6 Complete
- 2026-11-09 · work · phase 7 Complete
- 2026-11-09 · work · phase 8 Complete
- 2026-11-09 · work · phase 9 In Progress · stopped at the context limit with the tree green; resume from the phase's Implementation notes
- 2026-11-09 · work · phase 9 Complete
- 2026-11-09 · work · phase 10 Complete
- 2026-11-09 · review · root round 1 Not ready · `gofumpt -l .`: rc 0, no files; `staticcheck ./...`: rc 0; `go vet ./...`: rc 0; `go test ./... -race -cover -count=3 -timeout=30s`: run 1 FAIL (`e2e/tooling` TestJSONModeScenarios, data race, A04), run 2 `-shuffle=on`: rc 0, 92 ok; `go fix -diff ./...`: rc 0, empty; `dupl -t 80 .`: rc 0, 41 clone groups; prune grep re-run: allowed hits only; HEAD golden baseline rebuilt from 0ea7c95; `/dev/null` stdin probe both formats · findings: 4 major, 8 minor, A04 quality andon, A05 design andon; phases 3 and 9 Reopened (review 1)
- 2026-11-09 · maintainer · A05 closed · patch · missed by check 5 (a clause cited F14 for a field F14 lacked) · F14 `error` causes gain optional `stack`; R1-14 filed (fix), phase 6 Reopened (review 1)
- 2026-11-09 · maintainer · decisions on review 1 (standing instruction to run unattended): R1-04 the README follows Resolve.2; R1-05 may be fixed with `cmd.RunSilentTo` in the sibling go_away_boilerplate checkout (Scope amended); R1-11 owner set to fix; R1-12 stays with the maintainer
- 2026-11-09 · plan · R1-04, R1-08, R1-09, R1-10 closed +wip · README amended: Legacy controls, Scope, Root facts (F14); phase 1, 3, 6 re-planned
- 2026-11-09 · plan · phase 1 re-planned
- 2026-11-09 · plan · phase 3 re-planned
- 2026-11-09 · plan · phase 6 re-planned
- 2026-11-09 · maintainer · interfaces 1, 3, 6 signed (standing instruction in session)
- 2026-11-09 · fix · A04, R1-01, R1-02, R1-03, R1-05, R1-07, R1-11, R1-13, R1-14 closed; R1-06 partly (residual: criterion 17's list omits the test harness) · A04 cause confirmed own: the run now cancels and waits (`runenv.Children`, bound 2 s) for each MCP stderr relay before closing its streams; `go test ./e2e/tooling -run 'TestJSONModeScenarios/MCP_server_writing_stderr' -race -count=30 -shuffle=on`: ok (diagnostic run); R1-01 `SetFallback`/`procenv.Install` removed, ctx threaded into the migration callbacks (`utils.MigrationFunc`), summarizer constructor, cost enricher, photo/audio config loads; R1-05 go_away_boilerplate `cmd.RunSilentTo` added (uncommitted; `make qa`: rc 0) and dispatch passes the run's stdout; R1-11 fakes replaced by `internal/diag/diagfake.Deliverer` (in `TestDelivererContract`) and the real `diag.NewReporter` · fix-start gofumpt -w -l . ; staticcheck ./... ; go vet ./... ; go test ./... -race -cover -count=3 -timeout=30s ; go fix ./... ; dupl -t 80 .: all rc 0, 92 ok, 0 FAIL, 41 clone groups · fix-end same six, `go test` with -shuffle=on, twice: all rc 0, 93 ok, 0 FAIL, 43 clone groups (two new test-only groups in internal/setup tests from the added ctx argument; signal)
- 2026-11-09 · plan · R1-06 closed +wip · phase 9 re-planned: criterion 17 and `TestJSONNoDirectWrites` name the test-only harness files, proven by `TestTestHarnessIsTestOnly`; Golden diff `error.golden` row records the dropped stage prefix (R1-13)
- 2026-11-09 · maintainer · interfaces 9 signed (standing instruction in session)
- 2026-11-09 · review · root round 2 Not ready · `gofumpt -l .`: rc 0, no files; `staticcheck ./...`: rc 0; `go vet ./...`: rc 0; `go fix -diff ./...`: rc 0, empty; `dupl -t 80 .`: rc 0, 43 clone groups; `go test ./... -race -cover -count=3 -timeout=30s -shuffle=on` twice: rc 0, 93 ok, 0 FAIL each (load 1–5); go_away_boilerplate `go test ./pkg/cmd -race`: ok; native build probe (scratch HOME/CLAI_CONFIG_DIR/CLAI_CACHE_DIR), 30 `-json` invocations: every stdout and stderr line is a JSON object with `type` first; HEAD 0ea7c95 built from `git show` for text comparison · R1-01–R1-03, R1-05–R1-11, R1-13, R1-14, A04 resolved; R1-04 resolved in the README with an output.md residual (R2-04); none regressed; R1-12 open (maintainer) · findings: 2 major, 2 minor; phase 9 Reopened (review 2)
- 2026-11-09 · fix · R2-01, R2-02, R2-03, R2-04 closed · R2-01: go_away_boilerplate checkout gained `cmd.IsCommandFailure` (tests first, `make qa` rc 0 twice, uncommitted; the root Human check's release must carry it) and `presentOutcome` prints the usage only when it is false, `ExitStatus.Dispatched` removed, `completion fish`/`completion` rows in `TestDiagnosticCLIScenarios` and `TestJSONModeScenarios`; R2-02: `AudioTranscribeEngine(ctx, …)` + `AudioTranscribeTool.CallWithContext`, the chunk board writes `progress` records under `-json` (`TestChunkBoardJSONProgress`, `TestAudioTranscribeToolProgress` text and json, upgrade notice asserted); R2-03: `procenv.Env` uses the real resolver and `diag.NewReporter` over `diag.LegacyControls`, the one Legacy controls list now shared with `internal/cli` (`TestLegacyControls`, `TestEnvLevelsFollowTheCLIResolution`); R2-04: `architecture/output.md` states same-selector replacement · fix-start six gates (`go test` with `-shuffle=on`): all rc 0, 93 ok, 0 FAIL, dupl 43 · fix-end same six gates twice, fresh shuffle seeds: all rc 0, 94 ok, 0 FAIL, dupl 43 each · native probe (scratch HOME/CLAI_CONFIG_DIR/CLAI_CACHE_DIR, keys unset): `completion fish`, `completion`, with and without `-json`: rc 1, stdout empty, one error; phase 9 Complete
- 2026-11-09 · plan · README amended: Scope, Validation policy (the release check names `RunSilentTo` and `IsCommandFailure`, added by the R1-05 and R2-01 fixes)
- 2026-11-09 · review · root round 3 (sign-off spawn-review: falsifier, seams) Not ready · native and WASIX builds ok; ~65 `-json` probes valid JSON · findings: 3 major, 4 minor (R3-01..R3-07); phases 4.1.1, 4.1.2, 9 Reopened (review 3)
- 2026-11-09 · maintainer · decision: the `pkg/tools.AudioTranscribeEngine` signature change (ctx parameter, R2-02) is an accepted public API break, recorded here, not a finding
- 2026-11-09 · fix · R3-01, R3-02, R3-03, R3-04, R3-05, R3-06, R3-07 closed · tests first, each failing before its fix: R3-01 `TestJSONModeScenarios/failing tool at debug level` (the JSON tool records no longer wait on `!q.debug`); R3-02 `query summary`, `loop summary`, `step retry`, `model fallback` scenarios plus a no-`▸` check in `parseRecords` (summaries are a `clai.run_summary` info diagnostic with facts, registered in `runenv.Types` and shown at every level; retries a `retry` progress record, state update; fallbacks a `model fallback` progress record via `modelfallback.Notice(env)`, state start, row the next model); R3-03 `TestConfirmSummarize_jsonPrompt`, `TestActOnChat_jsonSelectPrompt`, `TestSetupQuestions_jsonPrompts`, `TestRecordText_JSONProgress` (calibrate prompt; that test's calibration progress row became the prompt record); R3-04 `TestJSONModeInput/trust prompt on a terminal` text assertions, `TestPlain_dropsMarkersAndAlignment`, `TestSkillTrustQuestion` (`look.Style.Plain`; skill trust `[y/N]` only in the text form); R3-05 two `TestWrapperContract` Write.2 rows; R3-06 a CRLF `TestDisplayerContract` row; R3-07 `TestAgentRunEnv` (`Agent.runContext` in Setup and Query: process stdin and its terminal state, discarded writers, no Reporter; a caller's Env is kept; process stdin read through `internal/cli/process`, terminal check moved to `internal/term`) · fix-start six gates (`go test` with `-shuffle=on`): all rc 0, 94 ok, 0 FAIL, dupl 43 · fix-end same six gates twice, fresh shuffle seeds: all rc 0, 96 ok, 0 FAIL, dupl 43 each · TestDiagnosticOutputGoldens unchanged, no golden regenerated · WASIX `GOTOOLCHAIN=local GOOS=wasix GOARCH=wasm …/components/go/bin/go build`: rc 0 · native probes (scratch HOME/CLAI_CONFIG_DIR/CLAI_CACHE_DIR, every `*_API_KEY` unset, proxies to 127.0.0.1:9, `-cm test`): the R3-01/02/03 repros now write `tool_call`/`tool_result`, a `clai.run_summary` diagnostic, and a `confirm` prompt · phases 4.1.1, 4.1.2, 9 Complete
- 2026-11-09 · plan · phase 9 re-planned (criterion 17 allowlist names `internal/cli/process`)
- 2026-11-09 · maintainer · interfaces 9 signed (standing instruction in session)
- 2026-11-09 · review · R3-08 filed from the R3 fix run's Surprises (cloud command styling under `-json`)
- 2026-11-10 · fix · R4-01, R4-02, R4-03, R4-04, R4-05, R4-06, R4-07, R3-08, A06 closed · R4 rows filed from the 2026-11-10 holistic review, tests first, each failing before its fix (`TestJSONModeInput` table rows, `TestPublishInteractive_jsonSelectPrompt`, `TestTablePrompter`, `Test_e2e_cloud_json_plain_records_and_first_pull_prompt`, `TestPlainLook_dropsBoxesAndColumns`, `TestPasteQuestionAsksForTheCode`, `TestMcpAuthCommandUnderJSON`, `TestAgentInternalDiagnostics`, `TestCLITypesRegisterEachTypeOnce`, `TestDiagnosticLevelLeavesTheResultStream`, `TestPresenterTrimsTrailingNewlines`, `TestUsageErrorHints`, `TestMissingAPIKeyRemedy`, `TestJSONFlagValues`, `TestOSProcessTerminalStates`, `TestComposeColorFollowsTruthyNoColor`, `TestLoadConfigFromFile_debugNamesTheFilePath`, `TestNoInvisibleRunesInSource`; go_away_boilerplate `Test_Builder_prompterShowsThePromptLine`, `Test_Builder_prompterErrorEndsRun`, `Test_MisplacedFlagError_reasonAndHint`) · README amended: Root facts (F13), Scope, Validation policy (Human check names `table.WithPrompter`, `MisplacedFlagError.Reason`/`Hint`); phase 9 synopsis (`-json=<bool>`), Local facts and `TestJSONModeInput` row (table prompt record); phase 8 Local facts and a `TestAgentInternalDiagnostics` row · fix-start `go run mvdan.cc/gofumpt@latest -w -l . ; go run honnef.co/go/tools/cmd/staticcheck@latest ./... ; go vet ./... ; go test ./... -race -cover -count=3 -timeout=30s -shuffle=on ; go fix ./... ; go run github.com/mibk/dupl@latest -t 80 .`: gofumpt, staticcheck, vet, fix, dupl rc 0, test rc 1 (A06: `internal/tools/mcp` `TestHttpConnCreatesNoChildProcess`, 95 ok), dupl 43 clone groups; go_away_boilerplate `make qa`: rc 0 · fix-end same six gates four times (one in reverse order, the last two after the final code edit), fresh shuffle seeds: all rc 0, 97 packages ok, 0 FAIL, dupl 43 clone groups each; go_away_boilerplate `make qa` after its last edit: rc 0; `go test ./internal/tools/mcp -race -count=3 -shuffle=1794298605363862057` ×8: ok ×8 (1 FAIL in 3 before) · TestDiagnosticOutputGoldens unchanged, no golden regenerated · phase 9 Complete
