# Output, diagnostics and `-json`

`internal/cli` is the composition root of one run (`compose.go`). It reads the
process once — its streams, their terminal states, the environment, and its
signals — and builds everything a producer may write through.

## The run's Env

`internal/runenv.Env` rides the context. Every producer reaches stdout,
stderr, stdin and terminal checks only through it:

| Need | Call |
| --- | --- |
| command result, model reply | `runenv.From(ctx).Stdout()` |
| progress, transcript on stderr | `runenv.From(ctx).Stderr()` |
| input, its terminal state | `Stdin()`, `StdinIsTerminal()` |
| terminal check of an output | `IsTerminal(stdio.Stdout\|stdio.Stderr)` (false under `-json`) |
| color choice | `Color()` (false under `-json` and `NO_COLOR`) |
| diagnostic | `runenv.Warnf/Noticef/Failuref/Debugf(ctx, subsystem, ...)` |
| a question before a read | `ShowPrompt(stream, display.Prompt)` |
| `$EDITOR` | `RefuseEditor(path)` before starting it |

A context without an Env yields a silent one, so `pkg/agent` and other
library callers write nothing unless they pass a Reporter. Test binaries set
a process fallback (`internal/testenv/procenv`). No runtime code outside
`internal/cli`, `internal/stdio`, and the test servers names `os.Stdout`,
`os.Stderr`, `os.Stdin` or `fmt.Print*`; `e2e/diagnostic_static_test.go`
(`TestJSONNoDirectWrites`) parses the tree to keep it so. `ancli` is gone
from every runtime path (`TestDiagnosticRuntimeAncli`).

## Diagnostics

```text
LOG_LEVEL=<level>[,<subsystem>=<level>...] clai ...
```

Levels are `error`, `warn` (alias `warning`), `info` (default), `debug`,
`trace`; the longest dotted subsystem match wins and an unknown selector
exits 1 naming it. The legacy controls (`NO_WARNINGS`, `DEBUG`,
`DEBUG_<SUBSYSTEM>`, the vendor debug variables, `LOG_LEVEL_SKILLS`) form
the lowest layer. A `LOG_LEVEL` entry replaces the legacy entry for the same
selector; a legacy selector `LOG_LEVEL` does not name stays. Subsystems and
their types are registered in `internal/runenv/types.go` plus `agent.DiagnosticTypes()`,
which includes that registry, so a library caller's Reporter or Deliverer receives
clai's internal diagnostics too (each type is registered once).

Diagnostics always go to stderr, never into a reply, the model's context or a
saved conversation. A type is immediate or buffered; buffered types (tool
call debug detail, MCP schema cache failures) share one 1 MiB buffer per run
that spills to timestamped files under `<cache dir>/diagnostics/`. At the end
of the run stderr lists the spill paths and the rest of the buffer. A failed
spill falls back to stderr; if stderr fails too the run panics (F6).

A terminal error is reported once by the Presenter, with its causes and
remedies; a dispatch failure adds the usage. `main.go` is only
`os.Exit(cli.RunProfiled(...))`.

## `-json`

`-json` (or `--json`) is recognised where command flags are parsed — before
the command, or among its flags before the first positional argument — and
`Run` finds it before dispatch, so argument errors are records too. Like
Go's bool flags it also takes a value: `-json=false` (or `0`, `f`, …)
selects text, `-json=true` JSON, and any other value is a usage error with
exit status 1. A `-json` written elsewhere is reported as a misplaced flag
whose hint says where the global flag goes. It changes the encoding only:
every destination, input and exit status stays, except that `$EDITOR` is
refused with an `error` record and exit 1.

Every line on stdout and stderr is one JSON object with a string `type`
first:

| Type | Written by |
| --- | --- |
| `reply_delta`, `reasoning_delta`, `reply`, `tool_call`, `tool_result` | the query path (`internal/text/json_display.go`) through the JSON `Displayer` of the destination the text form uses |
| `progress` | audio_record status, the audio transcription chunk board (title `audio_transcribe`, one `row` per chunk), video generation, one record per state change |
| `prompt` | every question before its read: trust, confirm and calibrate questions (`ShowPrompt`), the setup wizard's and the cloud first pull's `select` questions, the `mcp auth` paste question, and the selection prompt of every go_away_boilerplate table (`Env.TablePrompter` via `table.WithPrompter`, kind `select`, the prompt line as text) |
| `diagnostic`, `error` | the JSON `Presenter` on stderr |
| `child_output` | MCP server stderr (`source` `mcp_<server>`), one record per line through `mcp.StderrRelay` |
| `output` | everything else: the `output` Wrapper of each destination strips ANSI and `\r` redraws and wraps each line |

Producers see both outputs as non-terminals under `-json`, so they write their
pipe form and glow is never started; text with a terminal look (`look.Style`,
the cloud commands, `mcp auth` stages) is written `Plain`, without markers,
boxes or alignment. Reading `Stdin()` first flushes both Wrappers, so a
table's rows (`output` records) and its selection prompt (a `prompt` record)
are visible before its read. Shell completion (`__complete` candidates and the
`completion` scripts) is a result: dispatch hands go_away_boilerplate the
run's stdout (`cmd.RunSilentTo`), so under `-json` each line is an `output`
record.

A server's stderr is the same text in both formats. Its reader ends a line at
`\n` and at `\r`, so text a server redraws over is a line of its own rather
than erased, and skips empty lines. Each line is redacted as a diagnostic is
(best effort). Nothing else is removed: under `-json` the record's string
escapes an ANSI sequence or a bell, and the text form (`mcp_<server>: <line>`
off a terminal) shows each control character as the visible escape a
diagnostic uses (`\x1b`, `\x07`). The rolling window on a terminal gets the
redacted line and strips the control characters itself. A line shown while the
server ran is left out of the tail printed when it exits.

A run waits, before it closes its streams, for the relays of its children's
output: it cancels the run, which kills each MCP server's process group, and
waits up to two seconds for each server's stderr relay to drain, so the last
`child_output` lines are written while the streams are open.

## Interrupts, failed writes and the standard logger

The first SIGINT or SIGTERM cancels the run context with an
`interrupt.SignalError` as its cause (`internal/interrupt`,
`internal/cli/compose.go`). The session runner turns a context that ended
mid-run into an `interrupt.StoppedError`, which matches `ctx.Err()` and the
cause: what arrived is flushed to the session and persisted as a failed run
persists it, no final `reply` record is written, and the run summary's
outcome is `stopped`. The CLI presents the interruption once as its terminal
error (`interrupted by signal: interrupt`), never the failures the cancelled
context caused, and exits 128 plus the signal number (130, 143). The signal
ladder's three diagnostics are "shutting down gracefully", "graceful shutdown
ongoing, signal again to force exit" and "forcing exit". When stdout and
stderr are both terminals, the first notice starts on its own row
(`runenv.OpenRow`). A library `Query` returns the same `StoppedError`.

A reply that cannot be written to stdout fails the run in both formats
(`write the reply to stdout: ...`, exit 1). A stdout closed before clai
starts is indistinguishable from `/dev/null`: the Go runtime opens the null
device on a closed standard descriptor.

`routeStandardLog` (`internal/cli/stdlog.go`) sends Go's standard logger,
and with it the default `slog` logger and net/http's own lines, to the run's
diagnostics as `clai` warnings for the whole run and restores it afterwards.
Only the composition root does this; a library caller keeps its own logger.
`TestNoWriterBypassesTheRunStreams` keeps runtime code off the standard
logger, the default `slog` logger and `os.NewFile`.

Under `-json` a `reply` record is the concatenation of the `reply_delta`
records since the previous `reply`: reasoning has only its
`reasoning_delta` records, text streamed before a tool call gets its `reply`
before the `tool_call` record, and an answer without text has no `reply`.
