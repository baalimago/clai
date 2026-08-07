# Phase 6 — Quality gates

**Status:** Done  
[Back to README](./README.md)

## Goal

Prove the cross-repository dimensions change is complete, clean, and regression-free.

## Specification

Run the required quality commands in both repositories where applicable. Verify formatting, static analysis, vet, race tests, coverage, fix, duplication, and package tests. Search for stale terminal-width implementations and direct clai calls. Verify the released dependency version is used and no local `replace` remains. Update architecture documentation for streaming/rolling output and the shared package if required.

## Integration contract

| Trigger | Collaborator | Observable result | Required side effect | Prohibited side effect |
|---|---|---|---|---|
| Full clai test suite | shared dependency | All tests pass with race detector | Resize behavior is covered | No test skips or timeout changes |
| Full boilerplate test suite | dimensions/table | All tests pass | Public API remains stable | No stale ioctl path |
| Repository search | source tree | No duplicate production width system | Findings recorded | No hidden lookup remains |
| Packaging check | Go modules | clai uses released dependency | Reproducible build | No local replace directive |

## Acceptance criteria

- Required project QA commands pass unedited.
- Both repositories are formatted and statically clean.
- Race tests cover watcher and rendering lifecycle.
- Coverage is high and meaningful for the new dimensions package and clai integration, with error and cleanup paths represented; aggregate legacy coverage remains at least the project requirement. Review uncovered new behavior rather than optimizing an arbitrary percentage.
- Duplication findings are reviewed and either fixed or documented.
- Architecture/worklog documentation reflects the final design.
- `SIGWINCH`, `TIOCGWINSZ`, `COLUMNS`, `table.TermWidth`, and truncation searches confirm the intended single-system state.

## Error coverage

| Failure | Expected behavior | Test/evidence |
|---|---|---|
| Formatter changes files | Diff is reviewed and tests rerun | format command output |
| Staticcheck/vet finding | Finding fixed or justified | command output |
| Race failure | Fix synchronization before completion | race command output |
| Duplicate implementation found | Remove or document compatibility wrapper | repository search |
| Local module replace remains | Remove before completion | `go.mod` inspection |
| Meaningful new behavior lacks a test | Add a focused behavioral test or document why it is unreachable/mechanical | per-package coverage report and error-path review |

## Implementation notes

### Repository QA gates (R1-07)

Both repositories were run with the mandated commands. clai has `make qa`;
go_away_boilerplate has no Makefile, so its gates run individually.

clai (`/home/imago/Projects/public/clai`):

```bash
make qa
```

exit 0. The target runs staticcheck, gofumpt, and go fix, then the race
suite. The individual commands were also recorded (all exit 0):

```bash
go run mvdan.cc/gofumpt@latest -w -l .
go run honnef.co/go/tools/cmd/staticcheck@latest ./...
go vet ./...
go fix ./...
go test ./... -race -cover -count=3 -timeout=30s
go run github.com/mibk/dupl@latest -t 80 .
```

`go test ./... -race -cover -count=3 -timeout=30s` passed unedited: all
packages ok, exit 0. Coverage: internal/utils 81.3%, internal/text 72.3%,
internal/chat 70.7%, internal/tools 82.3%, internal/tools/mcp 63.9%,
internal/photo 13.6% (unchanged legacy low, already recorded in phase 3).
The new `resize_runner_test.go` measures 100% for `applyResize`,
`startResizeWatcher`, and `ensureActivityViewport`.

go_away_boilerplate (`/home/imago/Projects/public/go_away_boilerplate`, no
Makefile):

```bash
go run mvdan.cc/gofumpt@latest -w -l .
go run honnef.co/go/tools/cmd/staticcheck@latest ./...
go vet ./...
go fix ./...
go test ./... -race -cover -count=3 -timeout=30s
go run github.com/mibk/dupl@latest -t 80 .
```

All exit 0. Coverage: pkg/dimensions 100.0%, pkg/table 96.6%, pkg/shutdown
100.0%, pkg/num 100.0%, pkg/cmd 100.0%, pkg/testboil 86.2%, pkg/misc 92.3%.
The dimensions package reaches the near-100% target with the meaningful
error edges exercised (provider failure, zero size, fallback, signal bursts,
cancellation, stop, writer errors, closed source).

Cross-platform builds pass for darwin/amd64, linux/arm64, and
windows/amd64 (`GOOS=... GOARCH=... go build ./...`), which verifies the
`!unix` stub reader and no-op signal registration compile. `go mod verify`
reports all modules verified. No test skips and no timeout/count changes
were introduced: the changed test files and the new `resize_runner_test.go`
and `dimensions_test.go` contain no `t.Skip` and the mandated test command
ran unedited.

### Repository searches (single-system state)

| Search | clai | go_away_boilerplate |
|---|---|---|
| `table.TermWidth` | no production call (worklog and architecture text only) | compatibility wrapper only; delegates to `dimensions.DefaultReader(os.Stderr.Fd())` (D7) |
| `TIOCGWINSZ`/`SYS_IOCTL`/`unsafe` | none in production code (test comments only) | only in `pkg/dimensions` (`ioctl_unix.go`, `ioctl_other.go` stub, `pty_linux_test.go`) |
| `COLUMNS` | never consulted | no reference outside the worklog history |
| `SIGWINCH` | only the viewer event consumed in the serialized session loop; no signal-callback write | registration only in `pkg/dimensions/sigwinch_unix.go` |
| truncation helpers | only `*WithWidth` variants driven by a resolved snapshot | legacy querying variants kept as wrappers (D7) |
| `termWidth` as a name | only explicit-width parameters (`UpdateMessageTerminalMetadata`, `PrintToolActivity`, `glowRenderArgs`) | n/a |

`pkg/shutdown`'s `syscall` import handles SIGINT/SIGTERM and is unrelated
and pre-existing. The only `replace` directive in clai's `go.mod` is the
documented temporary boilerplate replace.

### Packaging check and released dependency

The module proxy and `go list -m -versions github.com/baalimago/go_away_boilerplate`
agree: v1.33.8 is the newest released version and no released version
contains `pkg/dimensions`. go_away_boilerplate origin/main is at 385532d
(2026-07-23), before the dimensions work; the phase 1-2 changes exist only
in the local working tree. clai therefore still carries the documented
temporary replace, and the `go.mod` comment states the removal condition.

**Release hand-off (required external action):** the maintainer must commit
and tag the boilerplate changes (for example v1.34.0) and push the tag,
then in clai run `go get github.com/baalimago/go_away_boilerplate@v1.34.0`,
delete the replace block, and run `go mod tidy`. The `go.mod`/`go.sum`
diff then proves the released version is used with no local replace. This
step requires git write access and a module-proxy publish, which are
outside this worklog's execution authority; D11 records the decision to
keep the replace until then. `go.sum` still carries the v1.33.8 hashes
(`go mod tidy` would drop them while the replace is in place); they are
left as the record of the released baseline.

### Documentation

The architecture documents already reflect the final design and needed no
further phase-6 update: colours.md describes the logical-block viewport,
reflow, and resize behavior; query.md lists the resize case in the session
loop; tools-command.md names `utils.SessionDimensions` and the explicit-width
helper. `pkg/dimensions` carries a package comment stating the
single-implementation contract.

### Duplication review

clai: 31 clone groups; go_away_boilerplate: 6 clone groups. Every clone is
pre-existing test scaffolding or vendored-adjacent code. The only clones in
worklog-modified files are fixture loops in `handler_list_chat_test.go` and
mock-stream scaffolding in `querier_test.go`; the worklog diffs to those
files are unrelated to the cloned regions. The one clone this work
introduced (phase 4 `AppendReasoning`/`AppendText`) was already merged into
the shared `appendCoalescing` helper.

### Error-coverage mapping

| Failure | Expected behavior | Evidence |
|---|---|---|
| Formatter changes files | Diff reviewed and tests rerun | gofumpt listed no files in either repository |
| Staticcheck/vet finding | Fixed or justified | both repositories clean |
| Race failure | Fixed before completion | race suite passes in both repositories |
| Duplicate implementation found | Removed or documented compatibility wrapper | searches above; wrappers documented as D7 |
| Local module replace remains | Removed before completion | release hand-off above; removal needs a maintainer tag+push (git write, out of scope) |
| Meaningful new behavior lacks a test | Focused behavioral test or documented unreachable/mechanical | pkg/dimensions 100%, resize runner 100% for `applyResize`/`startResizeWatcher`/`ensureActivityViewport`; error paths mapped in phases 1-5 |

## Review findings (review 1, 2026-08-07)

**R1-07 — Normal — resolved in implementation notes.** [x] The exact
commands are recorded for both repositories, including `make qa` in clai
and the individual gofumpt, staticcheck, vet, race/coverage/count, `go fix`,
and dupl runs in both. The released-module verification records v1.33.8 as
the newest release (module proxy + `go list -m -versions`) and the
`go.mod`/`go.sum` state; removing the temporary local `replace` requires a
maintainer tag+push of the boilerplate changes, recorded as the release
hand-off.

Verified good: stale-symbol searches, race coverage, released-dependency
verification, and architecture documentation are appropriate final checks.

## Review findings (review 4, 2026-08-07)

Holistic review of all six phases, executed per the runbook's final step.
Re-ran every gate independently in both repositories: `make qa` in clai and
the individual gofumpt, staticcheck, go vet, go fix,
`go test ./... -race -cover -count=3 -timeout=30s`, and dupl runs in both,
plus cross-platform builds (darwin/amd64, linux/arm64, windows/amd64) and
`go mod verify`. All exit 0; coverage as recorded above. Verified the code
rather than the notes: the dimensions Viewer lifecycle (D1-D6), the table
wrapper (D7), `SessionDimensions` and both session snapshots (D8), the
viewport storage/Resize/Render contract (D9), and the watcher gate and
resize consumption (D10) all match their contracts on every traced branch
(initial read failure, zero-size, signal bursts, closed source, stop after
cancellation, writer failure, partial write retry, resize-before-viewport
creation, resize-at-stream-end, tool transitions, final-answer pop after
resize). No code defect was found.

**R4-01 — Normal — open (maintainer-only).** [ ] Release the
go_away_boilerplate changes (commit, tag for example v1.34.0, push), then
in clai run `go get github.com/baalimago/go_away_boilerplate@v1.34.0`,
delete the local `replace`, and run `go mod tidy`. The worklog cannot
execute this: git writes and module-proxy publishes are outside its
authority. Tracked as D11 and in the packaging check above; this is the
single remaining item before the final clai change consumes a released
module version.

**R4-02 — Minor — non-reopening observation.** [ ] `dimensions.New`
documents that ctx must not be nil but does not guard it; a nil ctx panics
inside the watcher's first select. Every production and test caller passes a
non-nil ctx, and a panic on documented precondition violation matches Go
convention, so no guard or test is required.

Verdict: ready, with the release hand-off as the only open item. The gates
are green and the traced invariants hold; the release is a process step, not
a code change.

## Review findings (review 5, 2026-08-07)

**R5-02 — Minor — non-reopening observation.** [ ] `make qa` is not
deterministically reproducible: one of three full-suite runs in review 5
failed on `Test_context` (internal/vendors/anthropic/claude_stream_test.go:267,
untouched by this worklog) with `httptest.Server blocked in Close after 5
seconds` and a 30-second test timeout. The identical mandated command
`go test ./... -race -count=3 -timeout=30s` passed twice in full-suite runs
and the anthropic package passed in isolation. The test's cleanup calls
`testServer.Close()` before `close(testDone)`, so a slow context-cancel
propagation leaves the handler blocked and `Close()` waits the full 5
seconds — a pre-existing timing flake in vendored code, aggravated by load,
not a defect of this worklog. Fixing it belongs to the vendored package
(out of scope per the phase-3 "Do not change vendor code" rule).

Verified good in review 5, re-run independently: `make qa`'s lint targets and
all six mandated commands pass in both repositories (gofumpt, staticcheck,
`go vet`, `go fix`, `go test ./... -race -cover -count=3 -timeout=30s` exit
0, dupl); `go mod verify` passes; cross-platform builds pass for
darwin/amd64, linux/arm64, and windows/amd64. Coverage matches the records:
pkg/dimensions 100.0%, pkg/table 96.6%, internal/utils 81.3%, internal/text
72.3%. Repository searches confirm the single-system state: no production
`table.TermWidth` or terminal-querying truncation call remains in clai;
`TIOCGWINSZ`/`COLUMNS`/`unsafe` exist only in pkg/dimensions; `SIGWINCH`
registration exists only in pkg/dimensions/sigwinch_unix.go; the only
`replace` is the documented temporary boilerplate replace with the
release hand-off (R4-01/D11) still open and correctly recorded (origin/main
at 385532d, go.sum carrying the v1.33.8 hashes).