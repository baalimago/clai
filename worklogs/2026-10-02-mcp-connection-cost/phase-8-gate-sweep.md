# Phase 8 — Quality gate sweep

**Status:** Not Started

Back to [README](README.md).

## Goal

Prove the repository's gates pass unedited, the coverage floor is met, the architecture notes match
the shipped behaviour, and the headline cost reduction is observed on a real host.

## Specification

### Gates

Run the repository's own gates, exactly as `CLAUDE.md` defines them, with no modification to the
test invocation. The race, count and timeout flags are not to be altered, no test may be skipped,
and no test may be weakened to pass.

| Gate | Command |
| --- | --- |
| Format | `go run mvdan.cc/gofumpt@latest -w -l .` |
| Static analysis | `go run honnef.co/go/tools/cmd/staticcheck@latest ./...` |
| Vet | `go vet ./...` |
| Test | `go test ./... -race -cover -count=3 -timeout=30s` |
| Fix | `go fix ./...` |
| Duplication | `go run github.com/mibk/dupl@latest -t 80 .` |
| Format, static analysis, fix and test together | `make qa` |

The repository `Makefile` defines `qa` as `lint` plus the test invocation, and `lint` as
staticcheck, gofumpt and `go fix`. **`go vet` and the duplication gate are not in the target** and
must be run separately from the rows above. The duplication gate in particular is the one this
worklog singles out as load-bearing, so a green `make qa` is not evidence that it ran.

Duplication is a signal, not a verdict. The two `Conn` implementations will share shape around
demux and framing; any clone the duplication gate reports is either lifted into the shared
connection core or justified in this phase's implementation notes with the reason the two paths must
stay distinct.

Note the host sensitivity already recorded for this repository: the race gate is load-sensitive and
times out above roughly load eight. A timeout under load is retried on a quiet host before it is
treated as a regression, and no new default-on behaviour from this worklog may be active in the
shared end-to-end fixture.

### Invariants

This phase introduces no behaviour, so it binds no actor and no invariant row exists.

### Limits

This phase owns no runtime limit. Its one owned parameter is the coverage floor, recorded in the
README parameters table; the gate invocations in the table above are external commands rather than
worklog tunables.

### Coverage

New code meets the repository's coverage floor, with the higher figure preferred. The gap, if any,
is reported per package in this phase's implementation notes, naming which uncovered paths were
judged acceptable and why.

### Documentation consistency

Every claim in the architecture notes must match shipped behaviour, and every surface this worklog
touched must be reachable from the notes.

| Check | Where |
| --- | --- |
| The connection model, demux contract and both transports are described | `architecture/mcp.md` |
| The MCP sections of the tooling note are a pointer, with no stale duplicate of the lifecycle | `architecture/tooling.md` |
| The configuration reference lists every new server field | the configuration architecture note |
| The new subcommand appears wherever commands are enumerated | the command-dispatch note and the root usage text |
| The tool listing marker is described | the tools-command note |
| Typed errors introduced by this worklog are enumerated | the errors note |
| No note still describes unconditional eager startup of every configured server | all notes under `architecture/` |

A claim stated in two places is a defect: the lifecycle is described once, in the MCP note, and
referenced elsewhere.

### Human required

The automated suite proves process counts and behaviour. It cannot prove the fleet-level memory
outcome, which needs the maintainer's own host and a multi-agent run.

- **Artifact the person produces:** an observed steady-state resident-memory figure for a run of
  several concurrent agents configured with a representative mix of local and remote servers,
  compared against the same configuration before this worklog.
- **Where the agent stops:** after the gates and the documentation checks pass, the agent stops and
  asks the maintainer to run the comparison.
- **Before:** the agent prepares the configuration pair, confirms the automated process-count
  assertions pass, and states which figures to capture.
- **After:** the agent records the observed figures in this phase's implementation notes and in the
  README session journal, and marks the corresponding Definition of Success rows proven or not.

## Integration contract

Unit-test-only for this phase's own additions. The phase runs existing gates and the suites that
earlier phases introduced; it adds no new behaviour and therefore no new scenario of its own.

## Acceptance criteria

| Outcome | Test or command |
| --- | --- |
| Formatter reports nothing to change | `go run mvdan.cc/gofumpt@latest -w -l .` |
| Static analysis is clean | `go run honnef.co/go/tools/cmd/staticcheck@latest ./...` |
| Vet is clean | `go vet ./...` |
| The full suite passes unedited | `go test ./... -race -cover -count=3 -timeout=30s` |
| Fix reports nothing to change | `go fix ./...` |
| Every duplication report is resolved or justified | `go run github.com/mibk/dupl@latest -t 80 .` plus a note per retained clone |
| Format, static analysis, fix and test pass together | `make qa` |
| Vet and duplication, which `make qa` does not run, pass separately | `go vet ./...` and the duplication command above |
| New code meets the coverage floor | Coverage output from the test gate, reported per package |
| Every documentation row above is satisfied | The documentation consistency table, each row checked and recorded |
| The fleet memory outcome is observed | Human-required comparison recorded in implementation notes |

## Error coverage

This phase introduces no behaviour, so its failures are gate outcomes rather than code paths and
each row names the repeatable command that detects it rather than a test. Every row's corrective
action is to fix the owning phase, never to weaken the gate here.


| Failure | Expected outcome | Test |
| --- | --- | --- |
| A gate fails | The phase is not complete; the failure is fixed in the phase that owns the code, not suppressed here | `make qa` |
| The race gate times out | Retried on a quiet host before being treated as a regression; if it reproduces quietly, it is a real finding | `go test ./... -race -count=3 -timeout=30s` |
| Coverage falls below the floor | Missing tests are added to the owning phase rather than to this one | Coverage output from the test gate |
| Duplication is reported between the two transports | The shared shape is lifted into the connection core, or the clone is justified in writing | `go run github.com/mibk/dupl@latest -t 80 .` |
| An architecture note contradicts shipped behaviour | The note is corrected in this phase, and the contradiction is recorded as a finding | The documentation consistency table |
| A new surface is absent from the notes | Added in this phase | The documentation consistency table |

## Implementation notes

Not started.

## Review findings

None.
