# Phase 9 — CLI composition

**Parent:** root · **Class:** composition · [README](README.md)

## Goal

Wire the diagnostic contracts into the CLI and migrate its producers.

## Interface

The existing usage synopsis remains in `internal/cli/cli.go`. The diagnostic
configuration below extends that CLI contract.

```text
clai [command flags] <command> [command flags] [args]

LOG_LEVEL=<level>[,<subsystem>=<level>...] clai ...

levels: the names and alias F7 defines
default level: the Diagnostic level parameter (README)
legacy controls: the Legacy controls table (README)
result: stdout or the command's explicit result destination
diagnostics: stderr or the caller's diagnostic destination
spill files: the Spill-file directory (README)
success, usage, command failures, and user exits: existing exit statuses
invalid LOG_LEVEL or legacy control: diagnostic on stderr and exit status 1

clai -json [command flags] <command> [command flags] [args]

-json, --json: for every command, recognised where command flags are parsed:
  before the command, or among its flags before its first positional
  argument; elsewhere it is text (a prompt word stays a prompt word).
  -json=<value> takes the values Go's bool flags take: false selects text,
  any other non-bool value is a usage error with exit status 1.
  CLI.Run finds it by the same rule before dispatch, so argument errors are
  records too. Selects the JSON format for the run (F11)
JSON format: every line on stdout and stderr is one record of F14, on the
  destination its text form has (F1, F12, F14)
JSON prompts and input: as F13 states
JSON and $EDITOR: the refusal F13 states, with exit status 1
exit statuses: unchanged by -json, except that refusal (F11)
```

`CLI.Run` preserves existing command syntax, flags, help, completion, prompts,
and signal behavior. It reports an unhandled terminal error once. It uses the
registered immediate or buffered mode for diagnostics. It lists retained spill
paths and the remainder at run completion. It does not send diagnostics to the
model or to saved replies. It keeps all agent-facing tool errors on the tool
error path. It exports nothing new from `pkg/`. (F1–F10)

`CLI.Run` builds one `StreamProvider` per run from the process streams and
their terminal states, and every producer reaches stdout, stderr, stdin, and
terminal checks only through it. Under `-json` it builds the `Presenter` with
`NewJSONPresenter` over the stderr Encoder, has the producers of the query
and chat paths call a JSON `Displayer` over the Encoder of their destination
instead of drawing,
relays MCP server stderr through a `child_output` Wrapper over the stderr
Encoder, and lets every other write reach the `output` Wrapper of its
destination. Without `-json` the Presenter comes from `NewTextPresenter` and
every producer draws as it does today. Destinations, input handling, and exit
statuses are the same in both formats, except the `$EDITOR` refusal.
(F11–F14)

Consumes:
`Locator` (phase 1)
`Remedier` (phase 2)
phase 1 criterion 5 (its `TestPublicAPISurface`)
`LevelResolver.Resolve` (phase 3)
`ResolvedLevels` (phase 3)
`Reporter.Report` (phase 4)
`Deliverer` (phase 4)
`NewReporter` (phase 4)
`Format` (phase 4.1)
`Encoder` (phase 4.1)
`NewWrapper` (phase 4.1.1)
`Wrapper` (phase 4.1.1)
`StreamProvider` (phase 4.1.1.1)
`Config` (phase 4.1.1.1)
`Stream`, `Stdout`, `Stderr` (phase 4.1.1.1)
`New` (phase 4.1.1.1)
`Displayer` (phase 4.1.2)
`NewJSONDisplayer` (phase 4.1.2)
`ToolCall`, `ToolResult`, `Progress`, `Prompt` (phase 4.1.2)
`NewBufferedDeliverer` (phase 5)
`ByteSize` (phase 5)
`BufferedDeliverer.Finish` (phase 5)
`ErrUndeliverable` (phase 5)
`Presenter.ReportError` (phase 6)
`Presenter.Attach` (phase 6)
`NewTextPresenter` (phase 6)
`NewJSONPresenter` (phase 6)
`DispatchMonitor` (phase 7)
`NewDispatchMonitor` (phase 7)
`SignalSource` (phase 7)
`SetupQuerier` (phase 8)
`DiagnosticTypes` (phase 8)
`WithDiagnosticReporter` (phase 8)
`WithDiagnosticDeliverer` (phase 8)
`internal/cli:Run` (import)
`internal/cli:RunProfiled` (import)
`internal/cli:DefaultDeps` (import)
`internal/utils:GetClaiCacheDir` (import)
`e2e/e2etest:Harness.Run` (import)
`e2e/e2etest:Harness.RunProfiled` (import)
`go_away_boilerplate/pkg/table:WithInput` (import)
`go_away_boilerplate/pkg/table:WithWriter` (import)

## Acceptance criteria

| #   | Criterion                                                                                                                          | Proves      | Proof                            |
| --- | ---------------------------------------------------------------------------------------------------------------------------------- | ----------- | -------------------------------- |
| 1   | Result output remains separate from diagnostics in every output mode.                                                              | F1, F7      | `TestDiagnosticCLIScenarios`     |
| 2   | Terminal failures are rendered once with their causes and remedies.                                                                | F2, F8      | `TestDiagnosticCLIScenarios`     |
| 3   | Registered types use immediate or buffered delivery and retained spill files.                                                      | F4, F5      | `TestDiagnosticCLIScenarios`     |
| 4   | A spill failure falls back to stderr without cancelling a running operation. | F6 | `TestDiagnosticOutputFailure` |
| 5   | Levels, colors, redaction, legacy controls, and exit statuses follow the root facts.                                               | F7, F9      | `TestDiagnosticCLIScenarios`     |
| 6   | The executable path (`cli.RunProfiled`, as `main.go` calls it) and the in-process path (`cli.Run`) use the same diagnostic policy. | F1, F9      | `TestDiagnosticExecutableParity` |
| 7   | Library calls and the tool error path remain compatible.                                                                           | F2, F3, F9  | `TestDiagnosticAgentIsolation`   |
| 8   | Runtime paths no longer use `ancli`.                                                                                               | F9          | `TestDiagnosticRuntimeAncli`     |
| 9   | Each declared snapshot changes only as specified.                                                                                  | Golden diff | `TestDiagnosticOutputGoldens`    |
| 10  | The composition adds no exported identifier outside the F10 allowlist.                                                             | F10         | `TestPublicAPISurface`           |
| 11 | When the spill write and stderr both fail, `Report` matches `ErrUndeliverable` and the run aborts with a panic. | F6 | `TestDiagnosticOutputFailure` |
| 12 | Under `-json`, every line of stdout and stderr is a JSON object with a string `type` first and no raw ESC or `\r`, in every JSON scenario, also when stderr is a terminal. | F12 | `TestJSONModeScenarios` |
| 13 | Under `-json`, the reply, reasoning, tool calls and results, progress (one record per state change), prompts, diagnostics, terminal errors, child output, and other output appear as the records F14 names, on the destinations they have without `-json`. | F11, F14 | `TestJSONModeScenarios` |
| 14 | Under `-json`, a prompt is a record before its read and is answered by the same input as without `-json`; a prompt skipped without a terminal stays skipped. | F13 | `TestJSONModeInput` |
| 15 | Under `-json`, `$EDITOR` is refused before it starts with the `error` record F13 names and exit status 1. | F11, F13 | `TestJSONModeInput` |
| 16 | Without `-json`, output is unchanged apart from the Golden diff: producers draw as today. | F11 | `TestDiagnosticOutputGoldens` |
| 17 | No runtime code writes to the process streams or calls a print function outside the composition root and the allowlist, and the test-only harness files it allows are linked by no runtime file. | F12 | `TestJSONNoDirectWrites`, `TestTestHarnessIsTestOnly` |

## Test cases

| Test                             | Level                | Proves          | Given                                                                                                                     | Then                                                                         | Must not                                                          |
| -------------------------------- | -------------------- | --------------- | ------------------------------------------------------------------------------------------------------------------------- | ---------------------------------------------------------------------------- | ----------------------------------------------------------------- |
| `TestDiagnosticCLIScenarios`     | integration scenario | Criteria 1–3, 5 | Success, warnings, setup and run errors, raw, JSON, reply-only, nested levels, every Legacy controls row, and spill files | Outputs, statuses, and diagnostic modes follow the contract                  | Put warnings or trace detail in a reply                           |
| `TestDiagnosticOutputFailure` | integration scenario | Criteria 4, 11 | A spill failure during an operation, alone and with a failing stderr | Alone, stderr receives the buffer and the operation continues; together, the run aborts with a panic that names both failures | Retry without a bound, discard an independent operation error, or panic inside `BufferedDeliverer` |
| `TestDiagnosticExecutableParity` | integration scenario | Criterion 6     | The same command through `Harness.RunProfiled` and `Harness.Run`, both with `cli.DefaultDeps`                             | Both apply the same policy to stdout and stderr                              | Build a binary, or add process-only timestamps or color in a pipe |
| `TestDiagnosticAgentIsolation`   | integration scenario | Criterion 7     | An agent with no Reporter, a Reporter, and a failed tool call                                                             | The agent remains silent by default and the tool receives its existing error | Route agent-facing errors through the diagnostic buffer           |
| `TestDiagnosticRuntimeAncli`     | repeatable command   | Criterion 8     | `main.go` and every command, table, and shutdown runtime path                                                             | No reachable runtime diagnostic path calls `ancli`                           | Claim success from direct import removal alone                    |
| `TestDiagnosticOutputGoldens`    | integration scenario | Criterion 9     | Baseline output fixtures and deterministic input                                                                          | Only the listed snapshots differ                                             | Change any other committed golden                                 |
| `TestPublicAPISurface` | repeatable command | Criterion 10 | The exported identifiers of `pkg/diagnostic` and `pkg/agent` after this phase | Every identifier is in the F10 allowlist | Count `internal/diag` |
| `TestJSONModeScenarios` | integration scenario | Criteria 12, 13 | `-json` with: a streamed query with reasoning and a tool call; a failing tool (the audio_sources ffmpeg exit 127 shape); a provider 401 (claierr auth failure); a warning and a buffered diagnostic; `-reply-only`; `-raw`; an MCP server writing stderr; an `audio_record` status with stderr configured as a terminal; `profiles list`; `help`; an unknown argument; a recording `glow` stub on PATH | Every line parses; records and fields match F14; one `progress` record per state change; destinations match the same run without `-json` | Write ANSI, `\r`, glyph decoration, or a bare text line; start glow |
| `TestJSONModeInput` | integration scenario | Criteria 14, 15 | `-json` with a trust prompt whose stdin terminal check is injected true and `y\n` on the input; the same with stdin at `/dev/null` and the check false; a table selection given a line; an `$EDITOR` path | The `prompt` record (a table's selection prompt included) precedes the read; answers behave as without `-json`; with no terminal no prompt record is written and the run behaves as without `-json`; the editor is refused | Start `$EDITOR`, block on a closed input, or change the stdin terminal check |
| `TestJSONNoDirectWrites` | repeatable command | Criterion 17 | Every non-test Go file under `internal/` and `pkg/` | No reference to `os.Stdout`, `os.Stderr`, `os.Stdin`, `fmt.Print*`, or `println` outside `main.go`, `internal/cli` (the CLI composition root), `internal/cli/process` (the one process-stdin access point, used by both composition roots: `internal/cli` and `pkg/agent`), `internal/stdio`, the test servers, and the test-only harness files `internal/testenv/testenv.go`, `internal/testenv/quiet.go`, `internal/testenv/procenv/procenv.go`, which no non-test file outside `internal/testenv` imports | Claim success from a file list instead of a parse |
| `TestSignalSource` | integration scenario | Subscribe.1 (phase 7), F9 | The composition's `SignalSource` with a signal sent to the test process and with its stop run | The signal arrives on the channel; after stop no more arrive; a source that cannot subscribe returns its error | Leave a signal handler installed after the test |

## Error management

| Failure                                 | Expected outcome                                                   | Test                          |
| --------------------------------------- | ------------------------------------------------------------------ | ----------------------------- |
| Invalid `LOG_LEVEL` or legacy control   | Exit 1 and identify the invalid entry on stderr.                   | `TestDiagnosticCLIScenarios`  |
| Setup or run fails                      | Preserve the cause and report the terminal error once.             | `TestDiagnosticCLIScenarios`  |
| Spill write fails | Deliver through stderr per F6 without cancelling the running operation. | `TestDiagnosticOutputFailure` |
| Spill write and stderr both fail | `Report` matches `ErrUndeliverable`; abort the process with a panic naming both failures (F6). | `TestDiagnosticOutputFailure` |
| Operation and output both fail          | Retain both independent errors.                                    | `TestDiagnosticOutputFailure` |
| `-json` with `$EDITOR` | Refuse before starting it: the `error` record F13 names; exit 1. | `TestJSONModeInput` |
| A JSON record write fails | Handled as the failed diagnostic or result write it replaces (F6). | `TestJSONModeScenarios` |

## Golden diff

The phase captures baseline snapshots before it changes output. The generator
uses deterministic fixtures and records stdout, stderr, and exit status.

| Golden                                     | Change                                                                                   |
| ------------------------------------------ | ---------------------------------------------------------------------------------------- |
| `e2e/testdata/diagnostics/success.golden`  | Result stays on stdout; diagnostic progress moves to stderr.                             |
| `e2e/testdata/diagnostics/warning.golden`  | The warning moves to stderr and appears once.                                            |
| `e2e/testdata/diagnostics/error.golden`    | The terminal error appears once with the available cause and remedy. The `failed to run: ` prefix, which go_away_boilerplate's printing `Run` added, is gone: `cmd.RunSilent` returns the command's own error text. The cause names the provider code (`provider code: invalid_api_key`). |
| `e2e/testdata/diagnostics/buffered.golden` | At completion, stderr lists retained paths and the remaining buffer, not spill contents. |
| `e2e/testdata/diagnostics/help.golden`     | No change.                                                                               |
| `e2e/testdata/diagnostics/json-query.golden` (new) | `-json` query with a tool call: `reply_delta`, `tool_call`, `tool_result`, `reply` records. |
| `e2e/testdata/diagnostics/json-error.golden` (new) | `-json` run failing with a provider 401: one `error` record with status and provider code. |
| `e2e/testdata/diagnostics/json-output.golden` (new) | `-json profiles list`: `output` records only. |

## Removes

None.

## Local facts

The initial type registry chooses buffering case by case (F4). The level
registry holds every `Type.Subsystem` and every subsystem the Legacy
controls table names. The composition reads the environment once and passes
it to `LevelResolver.Resolve` as `LevelRequest`; when `Resolve` fails, the
`Presenter` that reports the failure uses the Diagnostic level parameter.
Every command that creates a board attaches it through `Presenter.Attach`
and detaches before `Board.Finish`, since a finished board drops lines. The
composition derives the Spill-file directory from `GetClaiCacheDir` and
supplies it to the buffer. `CLI.Run` panics only on a `Report` error that
matches `ErrUndeliverable`. The phase reduces `main.go` to `os.Exit` around
`cli.RunProfiled` by removing
`ancli.SetupSlog`, so `Harness.RunProfiled` is the executable path and no
test builds a binary.

Under `-json` the query and chat paths (reply, reasoning, tool activity,
boards, status lines, trust prompts) use the JSON `Displayer`; other commands
reach the JSON stream through the `output` Wrapper, so no command needs its
own records for F12 to hold. A table drawn by the `go_away_boilerplate`
table library writes its rows as `output` records and shows its selection
prompt as a `select` prompt record (`table.WithPrompter`), and the table
gets its input and writer from the `StreamProvider`. glow is never started under
`-json`. Every input reader the composition hands out, the table's included,
is `StreamProvider.Input`. The migration of every direct stream write to the
`StreamProvider` is part of this phase, as the `ancli` removal is.

## Human required

None.

## Implementation notes

| Run       | Commit      | Command                                                                                                                                                                          | Result                                                                                                                                                           |
| --------- | ----------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| replan    | 2c9c4ff+wip | —                                                                                                                                                                                | V1-08: the synopsis and Local facts cite the README parameters and the Legacy controls table instead of restating them. V1-11: Consumes use the renamed members. |
| fix-start | 2c9c4ff+wip | `go run mvdan.cc/gofumpt@latest -w -l . && go run honnef.co/go/tools/cmd/staticcheck@latest ./... && go vet ./... && go fix ./... && go run github.com/mibk/dupl@latest -t 80 .` | all rc 0                                                                                                                                                         |
| fix-start | 2c9c4ff+wip | `go test ./... -race -cover -count=3 -timeout=30s`                                                                                                                               | run 1 FAIL, package unidentified (A01); runs 2 and 3: 84 ok, slowest 19–21 s at load 7–12                                                                        |
| fix-end   | 2c9c4ff+wip | `go run mvdan.cc/gofumpt@latest -w -l . && go run honnef.co/go/tools/cmd/staticcheck@latest ./... && go vet ./... && go fix ./... && go run github.com/mibk/dupl@latest -t 80 .` | all rc 0, twice                                                                                                                                                  |
| fix-end   | 2c9c4ff+wip | `go test ./... -race -cover -count=3 -timeout=30s`                                                                                                                               | run 1: 6 FAIL (A01: 5 testenv `starved` timeouts at load 20–22, `internal/capture` shim race); run 2: 5 FAIL (same `starved` set, load 7→20 during the gate)     |
| replan    | 2c9c4ff+wip | —                                                                                                                                                                                | dry run: Local facts cover the Resolve-failure Level, the full level registry, and the `main.go` reduction; `TestDiagnosticRuntimeAncli` covers `main.go`.       |
| replan    | 2c9c4ff+wip | —                                                                                                                                                                                | F10: the composition cites F1–F10 and reuses `TestPublicAPISurface`.                                                                                             |
| replan    | 2c9c4ff+wip | —       | maintainer decision: `CLAI_DIAGNOSTIC_DIR` dropped; criterion 11 and an Error row for the double-failure panic (F6). |
| replan    | 2c9c4ff+wip | —       | V3-04: `TestPublicAPISurface` row for criterion 10; Consumes cite phase 1 criterion 5, `NewReporter`, `ResolvedLevels`, and `Presenter.Attach`; commands attach their boards. |
| replan    | 2c9c4ff+wip | —       | round 4 notes: detach before `Board.Finish`; `ErrUndeliverable` selects the panic; `GetClaiCacheDir` consumed here; criterion 11 moved after 10. |
| replan    | 0ea7c95+wip | —       | `-json`: synopsis, F11–F14 wiring, criteria 12–17, `TestJSONModeScenarios`, `TestJSONModeInput`, the terminal Displayer adapter, and three new JSON goldens; consumes phases 4.1–4.3. |
| replan    | 0ea7c95+wip | —       | V6: no terminal Displayer (text producers keep their drawing); synopsis cites F13; streams phase renumbered 4.1.1.1; Consumes add `Config`, `Stream`, and the display data types. |
| replan    | 0ea7c95+wip | —       | spawn-review round 1: `StreamProvider` (phase 4.1.3) carries every stream; flag position rule; `$EDITOR` exit exception and wording; prompt skipped without a terminal; no audio pipe change; `TestJSONNoDirectWrites`; terminal Displayer moved to phase 4.1.2; Consumes one member per line. |
| replan    | 0ea7c95+wip | —       | A03 sweep: consumes `NewBufferedDeliverer`, `ByteSize`, `NewDispatchMonitor`, `SignalSource`. |
| replan    | 0ea7c95+wip | —       | R1-06: criterion 17 names the three test-only harness files and `TestTestHarnessIsTestOnly`; R1-13: the Golden diff row of `error.golden` records the dropped `failed to run: ` stage prefix (the stage lives in go_away_boilerplate). |
| replan    | 0ea7c95+wip | —       | R3-07 follow-up: `TestJSONNoDirectWrites` names `internal/cli/process` explicitly as the process-stdin access point that `pkg/agent` also uses, instead of passing by the `internal/cli` prefix. |
| start     | 0ea7c95+wip | `go run mvdan.cc/gofumpt@latest -w -l . ; go run honnef.co/go/tools/cmd/staticcheck@latest ./... ; go vet ./... ; go test ./... -race -cover -count=3 -timeout=30s ; go fix ./... ; go run github.com/mibk/dupl@latest -t 80 .` | all rc 0, 92 packages ok, 0 FAIL, dupl 41 clone groups |
| start     | 0ea7c95+wip | same six gates (resume) | all rc 0, 92 packages ok, 0 FAIL |
| end       | 0ea7c95+wip | `go run mvdan.cc/gofumpt@latest -w -l . ; go run honnef.co/go/tools/cmd/staticcheck@latest ./... ; go vet ./... ; go test ./... -race -cover -count=3 -timeout=30s -shuffle=on ; go fix ./... ; go run github.com/mibk/dupl@latest -t 80 .` | all rc 0, 92 packages ok, 0 FAIL, dupl 41 clone groups |
| end       | 0ea7c95+wip | same, reverse order, fresh shuffle seed | all rc 0, 92 packages ok, 0 FAIL, dupl 41 clone groups |
| fix-start | 0ea7c95+wip | `go run mvdan.cc/gofumpt@latest -w -l . ; go run honnef.co/go/tools/cmd/staticcheck@latest ./... ; go vet ./... ; go test ./... -race -cover -count=3 -timeout=30s ; go fix ./... ; go run github.com/mibk/dupl@latest -t 80 .` | all rc 0, 92 packages ok, 0 FAIL, dupl 41 clone groups |
| fix-end   | 0ea7c95+wip | same six gates, `go test` with `-shuffle=on` | all rc 0, 93 packages ok, 0 FAIL, dupl 43 clone groups |
| fix-end   | 0ea7c95+wip | same six gates, fresh shuffle seed | all rc 0, 93 packages ok, 0 FAIL, dupl 43 clone groups |
| fix-start | 0ea7c95+wip | `go run mvdan.cc/gofumpt@latest -w -l . ; go run honnef.co/go/tools/cmd/staticcheck@latest ./... ; go vet ./... ; go test ./... -race -cover -count=3 -timeout=30s -shuffle=on ; go fix ./... ; go run github.com/mibk/dupl@latest -t 80 .` | all rc 0, 93 packages ok, 0 FAIL, dupl 43 clone groups |
| fix-end   | 0ea7c95+wip | same six gates | all rc 0, 94 packages ok, 0 FAIL, dupl 43 clone groups |
| fix-end   | 0ea7c95+wip | same six gates, fresh shuffle seed | all rc 0, 94 packages ok, 0 FAIL, dupl 43 clone groups |
| fix-start | 0ea7c95+wip | `go run mvdan.cc/gofumpt@latest -w -l . ; go run honnef.co/go/tools/cmd/staticcheck@latest ./... ; go vet ./... ; go test ./... -race -cover -count=3 -timeout=30s -shuffle=on ; go fix ./... ; go run github.com/mibk/dupl@latest -t 80 .` | all rc 0, 94 packages ok, 0 FAIL, dupl 43 clone groups |
| fix-end   | 0ea7c95+wip | same six gates | all rc 0, 96 packages ok, 0 FAIL, dupl 43 clone groups |
| fix-end   | 0ea7c95+wip | same six gates, fresh shuffle seed | all rc 0, 96 packages ok, 0 FAIL, dupl 43 clone groups |
| fix-start | 0ea7c95+wip | `go run mvdan.cc/gofumpt@latest -w -l . ; go run honnef.co/go/tools/cmd/staticcheck@latest ./... ; go vet ./... ; go test ./... -race -cover -count=3 -timeout=30s -shuffle=on ; go fix ./... ; go run github.com/mibk/dupl@latest -t 80 .` | test rc 1: `internal/tools/mcp` `TestHttpConnCreatesNoChildProcess` (A06), 95 packages ok; the other five rc 0, dupl 43 clone groups |
| fix-end   | 0ea7c95+wip | same six gates (R4-01..R4-07, R3-08, A06) | all rc 0, 97 packages ok, 0 FAIL, dupl 43 clone groups |
| fix-end   | 0ea7c95+wip | same six gates, reverse order, fresh shuffle seed | all rc 0, 97 packages ok, 0 FAIL, dupl 43 clone groups |
| fix-end   | 0ea7c95+wip | same six gates twice after the final code edit, fresh shuffle seeds | all rc 0, 97 packages ok, 0 FAIL, dupl 43 clone groups each |

Andon: A01, A06.
Surprises:

- The timed-out `internal/audio` binary orphaned its `/bin/sh` ffmpeg shim, looping on `sleep 0.1` under the sandbox's pid 1; killed by hand.
- ancli wrote warnings and notices to stdout (and, in the executable, through `SetupSlog` with timestamps), so every migrated warning moved stdout → stderr; e2e tests asserting them on stdout were moved to stderr.
- `cmd.RunSilent` had no exported stage marker; R2-01 added `cmd.IsCommandFailure` in the go_away_boilerplate checkout and removed `internal.ExitStatus.Dispatched`, which the built-in `completion` command never set.
- R1-01: no fallback Env remains; tests that capture the process streams pass `procenv.Context(ctx)`.
- DEBUG-only traces in `utils.ReadConfigFile` and `utils.PromptFrom` were dropped: no context reaches them.
- `tools` registry `Set`/`SetAlias` no longer report; registration debug moved to `tools.Init(ctx)` and `mcp.RegisterTools(ctx, …)`; `TestRegistrySet_readsDebugAtCallTime` removed.
- Vendor debug envs outside the Legacy table (`BERGET_DEBUG`, `XAI_DEBUG`, …) now select nothing; their `vendor.<name>` subsystems are registered for `LOG_LEVEL`.
- go_away_boilerplate's completion (`__complete`, completion scripts) printed with `fmt` itself; R1-05 added `cmd.RunSilentTo`, and dispatch now passes the run's stdout.
- `help.golden` must not change, so the `LOG_LEVEL`/`-json` synopsis is a doc comment above `usageTemplate` in `cli.go` and `architecture/output.md`, not help text.
- R2-02: under `-json` the audio chunk board (split and calibrated diarization, both the tool and `clai audio transcribe`) draws nothing and writes `progress` records titled `audio_transcribe`; `pkgtools.AudioTranscribeEngine` takes ctx and `AudioTranscribeTool` gained `CallWithContext`.
- `isCharDevice` counts `/dev/null` as a terminal; the stdin terminal state now uses the termios check moved from `cloudcmd` to `internal/cli`.
- A04: the run cancels and waits for the MCP stderr relays (`runenv.Children`) before closing its streams.
- `pkg/agent` `Setup` sets `chat.SkipIndex` for the process and `setup.Input` outlives a `clai setup` run; the new e2e tests restore both.
- Redaction ran eight regexps over each 1 MiB buffer flush and timed out the race gate; each pattern now has literal hints and is skipped when none occurs.
- Under `-json` a failed record write is ignored exactly where the text form ignores the write it replaces (tokens, transcript), so exit statuses match.
- The remote resolver's notices, the profiles trust prompt, and `help` now take the run's streams; `TerminalTrustPrompter` takes the stdin terminal state.
- End gates add `-shuffle=on` for the random order; `-race`, `-count`, `-timeout` unchanged.
- R3-03: F14 has no free-text kind; the setup wizard's free-text entries (names, values, keys, the pasted MCP config) are `select` prompts without choices.
- R3-07: criterion 17 forbids `os.Stdin` outside `internal/cli`, and `internal/stdio` may not import `os`, so pkg/agent reads the process stdin through the new `internal/cli/process.Stdin`; the termios check moved from `internal/cli` to `internal/term` for both.
- Not fixed here: the `clai cloud` commands keep their `look` heads under `-json` as `output` records, and `cloud pull`'s first-pull question (`Keep [l/r/p/q]`, terminal stdin only) is not a `select` prompt yet.

## Review findings

### Review 1, 2026-11-09 (root)

**R1-01 · major · owner fix.** Producers given `context.Background()` are silent in clai.
- `internal/runenv/runenv.go:42-48` keeps a package-level `fallback` that only test binaries set (`procenv.Install` in 15 `main_test.go` files), so tests print what production drops. This conflicts with CLAUDE.md "Avoid package level state, always inject dependencies".
- `internal/text/config_migration.go:42`: the "migrating old chat config" notice was `ancli.PrintOK` at HEAD and now never shows (F9).
- `internal/text/summary_launch.go:170,174,178` and `internal/text/cmd.go:402`: `DEBUG_SUMMARY=1 clai q …` with a failing in-flight summary prints nothing, against the Legacy controls row `summary=debug`.
- [x] Thread the run ctx into every `context.Background()` producer.
- [x] Remove `SetFallback`, or confine it to test-only code, and inject an Env in tests.
- [x] Add a `cli.Run` scenario per site with its legacy control set.

**R1-02 · major · owner fix.** The Golden diff row for `json-error.golden` promises "one `error` record with status and provider code". The golden has `"status":401` and no `provider_code`, although the body carries `"code":"invalid_api_key"`.
- Cause: `internal/vendors/openai/decode_error.go:26-30` sets `ProviderCode` only for insufficient quota.
- `TestJSONModeScenarios` (`e2e/tooling/json_mode_e2e_test.go:240-256`) asserts status only.
- Scenario: the web consumer gets no provider code for an openai 401.
- [x] Fill the provider code for non-OK envelopes in the vendor decoder.
- [x] Assert `provider_code` in the scenario, and regenerate the golden.

**R1-03 · major · owner fix.** F11 ("every input, and every exit status stay as they are") is unmet for empty stdin. The JSON `Input()` (`flushingReader`, `internal/stdio/provider.go:118`) has no `Stat`, so `utils.PipedStdin` (`internal/utils/prompt.go:38-52`) treats `/dev/null` as an empty pipe. Reproduced:
- `clai -cm test q </dev/null`: "found no prompt", exit 1.
- `clai -json -cm test q </dev/null`: `{"type":"reply_delta","text":""}`, exit 0, an empty query.
- `clai -json q hi </dev/null` sends "hi " instead of "hi".

A web page spawning clai without stdin hits this.
- [x] Forward `Stat` (or the stdin's kind) through `flushingReader`.
- [x] Add the `/dev/null` rows to `TestJSONModeInput`.

**R1-05 · major · owner fix.** F12 and criterion 12 are unmet for completion. `clai -json completion bash` writes the raw script to stdout, and `__complete` prints raw TSV (go_away_boilerplate prints with `fmt`; noted under Surprises, no finding filed).
- [x] Clai-side, under `-json`, refuse completion with an `error` record or wrap its output in `output` records; or have plan record the exception in F12/Scope.

**R1-06 · minor · owner fix.** The evidence is wider than criterion 17.
- `e2e/diagnostic_static_test.go:183-192` exempts all of `internal/testenv/` (holding `procenv` with `os.Stdout`/`os.Stdin`), and `TestDiagnosticRuntimeAncli` skips it too (line 108). Criterion 17 names only `main.go`, `internal/cli`, `internal/stdio` and the test servers.
- `internal/setup/terminal.go:12` adds a package-level `var session = context.Background()` that `setup.go:170` reassigns per run.
- [ ] Narrow the allowlist, or amend criterion 17. (Partly: `internal/testenv/` narrowed to `testenv.go`, `quiet.go`, `procenv/procenv.go`, guarded by `TestTestHarnessIsTestOnly`; those three must touch the process streams, so the rest needs criterion 17 to name the test harness — plan.)
- [x] Inject the setup context instead of the package variable.

**R1-13 · minor · owner fix.** `error.golden` also lost its `failed to run: ` message prefix, a change its Golden diff row does not state. The pre-existing goldens were regenerated without a byte baseline, and the reviewer rebuilt that baseline from 0ea7c95.
- [x] Add the change to the row (plan), or restore the prefix.

**A04 · quality andon (README).** Gate run 1, `go test ./... -race -cover -count=3 -timeout=30s`, failed: `TestJSONModeScenarios/MCP_server_writing_stderr`.
- The JSON run's stderr missed the `child_output` line.
- A data race: the MCP stderr reader (`conn_stdio.go:243` → `mcp_log_sink.go:492` → `jsonl` Encode) wrote into the run's stderr after `cli.Run` returned, while the test read the buffer (`json_mode_e2e_test.go:65`).
- The shuffled run 2 passed.
- Cause: own. Nothing awaits the stderr reader (the Surprise "MCP stderr read after a run ends is lost"), and the `child_output` Wrapper keeps writing after the run.

Verified good:
- Flag position rule, and argument errors as records with exit 1.
- Invalid `LOG_LEVEL` under `-json` is a record.
- `version`, `help`, `tools`, `chat list`, `setup`, `replay` and table output are records only.
- `$EDITOR` is refused with its path.
- glow is gated off.
- audio_sources exit 127 is `ok:false` with `error`.
- Wrappers close on every return path, and `ErrUndeliverable` alone panics.
- `help.golden` is byte-identical to HEAD.
- `success`, `warning` and `buffered` goldens change only as their rows say (buffered's retained-paths half is proven by `TestDiagnosticCLIScenarios`, not the golden).
- No other golden or testdata changed.
- `main.go` is `os.Exit(cli.RunProfiled(...))`, and no runtime code imports ancli.

Note: two test files still import ancli (`internal/text/querier_setup_tools_test.go`, `internal/chat/handler_list_chat_test.go`). MCP lines after the run ends were lost by the executable at HEAD too (`os.Exit`).

### Review 2, 2026-11-09 (root)

Round-1 findings:
- R1-01 resolved: `runenv` keeps no fallback, `SetFallback`/`procenv.Install` are gone, and the migration notice and `DEBUG_SUMMARY` traces have `cli.Run` scenarios. One producer was missed; see R2-02.
- R1-02 resolved: `json-error.golden` carries `"status":401,"provider_code":"invalid_api_key"`, and `TestJSONModeScenarios` asserts `provider_code`.
- R1-03 resolved. Reproduced with a native build: `clai -json -cm test q </dev/null` exits 1 with the same `found no prompt` error as the text form; `q hi </dev/null` sends `hi`; piped `hello` is read.
- R1-05 resolved: `clai -json completion bash` gives 23 `output` records, and `__complete` candidates are `output` records. Dispatch passes `runenv` stdout to `cmd.RunSilentTo` (`internal/runtimeboundary/monitor.go:43-50`), and without an Env it falls back to `RunSilent`. go_away_boilerplate `go test ./pkg/cmd -race`: ok.
- R1-06 resolved: criterion 17 names the three harness files, and `TestTestHarnessIsTestOnly` exists. `internal/setup` has no package-level `session`.
- R1-13 resolved: the Golden diff row states the dropped prefix and the provider code.
- A04 resolved: the run waits on `runenv.Children` (bound 2 s) before `streams.Close`, and both shuffled gate runs passed.

**R2-01 · major · owner fix.** A failure of go_away_boilerplate's built-in `completion` command prints the whole usage on stdout as well as the error. This happens in both formats.
- Cause: that command's `Setup` never calls clai's `markDispatched`, so `presentOutcome` (`internal/cli/compose.go:360,383`) reads `!exit.Dispatched()` as a dispatch failure.
- Reproduced: `clai completion fish </dev/null` writes 53 usage lines on stdout, then `error: unsupported shell "fish", …`. HEAD (0ea7c95, built from `git show`) wrote only the error line, with no usage. `clai -json completion fish` writes 53 `output` records before the `error` record.
- `clai completion` with no shell does the same, and so does a failed write of a script or a candidate.
- This breaks F9 and criterion 16: text output is unchanged apart from the Golden diff.
- [x] Decide a dispatch failure from the error's kind (`ArgNotFoundError`, `MisplacedFlagError`, `ErrNoArgs`, a flag parse error), or mark the built-in completion command as dispatched.
- [x] Add `completion fish` rows to `TestDiagnosticCLIScenarios` and `TestJSONModeScenarios`: stdout empty, one error.

**R2-02 · major · owner fix.** The `audio_transcribe` tool's engine (`internal/audio/create_querier.go:97,116`) still runs on `context.Background()`, so everything it reports through `runenv.From(ctx)` goes to the silent Env.
- What is lost: the splitting and calibrated-diarization progress board (`statusOf`, `split.go:233` and `coordinator.go:162`), which HEAD drew to `os.Stderr` through the `StatusOut` default (`split.go:83` at HEAD), and the audioConfig upgrade notice (`utils/config.go` `Noticef`).
- Scenario: `clai q "transcribe meeting.wav"`, with the model calling `audio_transcribe` on a file that needs splitting. Text mode shows no board on stderr. `-json` writes no `progress` records, and the Minutes page transcribes through exactly this tool.
- This is R1-01's "every `context.Background()` producer" checkbox, missed for `pkgtools.AudioTranscribeEngine(filePath, format)`, which has no ctx.
- [x] Give the engine the call's ctx, as `audio_record` and `audio_sources` do with `CallWithContext`. A new exported method or engine signature in `pkg/tools` needs plan's view of "exports nothing new from `pkg/`" first.
- [x] Add a `cli.Run` scenario in which the tool runs a split transcription, asserting the board in text and the `progress` records under `-json`.

Verified good (round 2):
- End-to-end probe of a native build with a scratch HOME/CLAI_CONFIG_DIR/CLAI_CACHE_DIR. These all wrote only lines that parse as JSON objects with a string `type` first and no ESC or `\r` on stdout or stderr: `-json -cm test q hi` (with stdin `/dev/null`, piped, and `-raw`/`-reply-only`), `profiles list`, `profiles`, `completion bash`, `__complete`, `completion fish`, `version`, `--version`, `help`, `-h`, `q -h`, `bogus`, `tools`, `tools apply_patch`, `chat list`, `chat continue 0`, `chat dir`, `confdir`, `replay`, `setup`, `skills`, `audio sources`, `cmd`, `re`, `shell`, `LOG_LEVEL=trace … q`, and a SIGTERM during a blocked stdin read.
- The exit statuses match the text form.
- The SIGTERM warning matches HEAD's `shutdown` text ("initiated forceful shutdown").

Notes:
- The doubled `failed to setup prompt: failed to setup prompt:` is HEAD's text too.
- The reviewer's `-json q -json hi` probe ran without `-cm test` and reached the default vendor with a key from the shell environment. One paid call. No code issue.
