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
```

`CLI.Run` preserves existing command syntax, flags, help, completion, prompts,
and signal behavior. It reports an unhandled terminal error once. It uses the
registered immediate or buffered mode for diagnostics. It lists retained spill
paths and the remainder at run completion. It does not send diagnostics to the
model or to saved replies. It keeps all agent-facing tool errors on the tool
error path. It exports nothing new from `pkg/`. (F1–F10)

Consumes: `Locator` (phase 1), `Remedier` (phase 2),
`LevelResolver.Resolve` and `ResolvedLevels` (phase 3), `Reporter.Report`,
`Deliverer`, and `NewReporter` (phase 4),
`BufferedDeliverer.Finish` and `ErrUndeliverable` (phase 5),
`Presenter.ReportError` and `Presenter.Attach` (phase 6),
`DispatchMonitor` (phase 7), `SetupQuerier`, `DiagnosticTypes`,
`WithDiagnosticReporter`, and `WithDiagnosticDeliverer` (phase 8),
phase 1 criterion 5 (its `TestPublicAPISurface`),
`internal/cli:Run`, `internal/cli:RunProfiled`, `internal/cli:DefaultDeps`,
and `internal/utils:GetClaiCacheDir` (imports), and `e2e/e2etest:Harness.Run` and `Harness.RunProfiled` (imports).

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

## Error management

| Failure                                 | Expected outcome                                                   | Test                          |
| --------------------------------------- | ------------------------------------------------------------------ | ----------------------------- |
| Invalid `LOG_LEVEL` or legacy control   | Exit 1 and identify the invalid entry on stderr.                   | `TestDiagnosticCLIScenarios`  |
| Setup or run fails                      | Preserve the cause and report the terminal error once.             | `TestDiagnosticCLIScenarios`  |
| Spill write fails | Deliver through stderr per F6 without cancelling the running operation. | `TestDiagnosticOutputFailure` |
| Spill write and stderr both fail | `Report` matches `ErrUndeliverable`; abort the process with a panic naming both failures (F6). | `TestDiagnosticOutputFailure` |
| Operation and output both fail          | Retain both independent errors.                                    | `TestDiagnosticOutputFailure` |

## Golden diff

The phase captures baseline snapshots before it changes output. The generator
uses deterministic fixtures and records stdout, stderr, and exit status.

| Golden                                     | Change                                                                                   |
| ------------------------------------------ | ---------------------------------------------------------------------------------------- |
| `e2e/testdata/diagnostics/success.golden`  | Result stays on stdout; diagnostic progress moves to stderr.                             |
| `e2e/testdata/diagnostics/warning.golden`  | The warning moves to stderr and appears once.                                            |
| `e2e/testdata/diagnostics/error.golden`    | The terminal error appears once with the available cause and remedy.                     |
| `e2e/testdata/diagnostics/buffered.golden` | At completion, stderr lists retained paths and the remaining buffer, not spill contents. |
| `e2e/testdata/diagnostics/help.golden`     | No change.                                                                               |

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

Andon: A01.
Surprises:

- The timed-out `internal/audio` binary orphaned its `/bin/sh` ffmpeg shim, looping on `sleep 0.1` under the sandbox's pid 1; killed by hand.

## Review findings

None.
