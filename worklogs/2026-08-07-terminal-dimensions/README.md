# Shared Unix Terminal Dimensions and Dynamic Rolling Viewport

## Status board

| #   | Phase                                                         | Status              | Summary                                                                                                                                                                                                                                                                                                                      |
| --- | ------------------------------------------------------------- | ------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| 1   | [dimensions package](./phase-1-dimensions-package.md)         | Done                | Add the shared Unix terminal-dimensions viewer in `go_away_boilerplate/pkg/dimensions`.                                                                                                                                                                                                                                      |
| 2   | [table migration](./phase-2-table-migration.md)               | Done                | Make `pkg/table` consume the dimensions package and remove its private ioctl implementation.                                                                                                                                                                                                                                 |
| 3   | [clai width unification](./phase-3-clai-width-unification.md) | Done                | Route all clai width-aware output through one dimensions snapshot per session (`utils.SessionDimensions`) and explicit-width table helpers; remove `Querier.termWidth` and all hidden terminal queries.                                                                                                                      |
| 4   | [viewport reflow](./phase-4-viewport-reflow.md)               | Done                | ActivityViewport stores logical blocks, `Resize` rewraps retained blocks and marks the viewport dirty, and `Render` emits one full atomic frame on dirty state with a diff path for normal appends; retention bounds blocks and per-block content.                                                                           |
| 5   | [SIGWINCH integration](./phase-5-sigwinch-integration.md)     | Done                | One dimensions watcher per terminal rolling-output session refreshes the snapshot, resizes the viewport, and redraws in the serialized session loop; raw/structured/non-terminal sessions never start a watcher. R5-01 fixed: the viewport constructor binds the initial effective height to min(cap, terminal height), so the first frame never exceeds the terminal even without a resize. |
| 6   | [quality gates](./phase-6-quality-gates.md)                   | Done                | All repository QA gates pass in both repositories; searches confirm the single terminal-dimension system; released-dependency verification recorded with the release hand-off for the temporary local replace.                                                                                                               |

Phase order is strict. Phase 1 must land before Phase 2. Phase 2 must land before the clai migration in Phase 3. Phases 3 and 4 may proceed in parallel after Phase 2, but Phase 5 depends on both. Phase 6 is always last.

## Motivation

clai's rolling activity viewport currently captures terminal width once and uses several independent width paths afterward. In tmux, resizing a pane changes the foreground pseudo-terminal dimensions and delivers `SIGWINCH`, but clai does not currently re-query and reflow its output.

The shared implementation belongs in `go_away_boilerplate`, because `pkg/table` and clai both need terminal dimensions. clai must not add a second ioctl implementation.

## Strategy

`go_away_boilerplate/pkg/dimensions` is the sole terminal-dimension implementation. It is Unix-only for this effort and returns width and height together. A stateful `Viewer` owns the output target, current snapshot, and resize notifications. `SIGWINCH` only invalidates dimensions; rendering remains serialized by the consumer loop.

For implementation, the viewer contract must define the selected file descriptor, the exact fallback/error result for non-terminals and zero sizes, notification coalescing semantics, and ownership of `signal.Notify` registration. A consumer must be able to inject both signal events and dimension reads without sending process-global signals from unit tests.

The implementation must be easy to test and must exercise the meaningful error
edges: provider failure, zero-size dimensions, fallback, signal bursts,
cancellation, stop, and writer errors. Aim for very high coverage in the new
`pkg/dimensions` package, preferably near 100%, but do not shape the API or
production design around an arbitrary percentage. The clai integration must
test its important failure behavior at the real boundary. Coverage is evidence
of design quality, not a substitute for a clear lifecycle and behavioral tests.

`pkg/table` consumes that package. Existing `TermWidth` APIs may remain as compatibility wrappers, but they must delegate to `dimensions` and must not retain a separate ioctl or `COLUMNS` implementation. Snapshot-aware table helpers are required so callers can use one resolved dimension set for a complete operation.

clai owns one dimensions snapshot per interactive output session. Every width-aware path uses that snapshot: rolling output, glow, cleanup metadata, photo animation, chat listings, tool listings, MCP descriptions, and tool activity. No clai production code may call `table.TermWidth` or hidden width-discovery helpers. The snapshot binds to the fd of the session's output writer: the viewer must observe the same file the writer writes to, not a fixed global fd. Today `table.TermWidth` queries stderr while clai writes to stdout, so `clai 2>/dev/null` on a wide terminal renders at the 80-column fallback.

The rolling viewport retains logical activity blocks so a width change can rewrap reasoning, assistant text, and tool output. A resize invalidates the previous render bookkeeping and emits one complete redraw. Height is bounded by the configured rolling-window cap and the current terminal height; the effective viewport height equals min(cap, terminal height) at every point of the viewport lifecycle, including creation — the constructor must never render taller than the terminal it writes to (R5-01). Retained logical blocks need a bounded retention policy: overflowing and compacted tool/reasoning content is currently dropped at append time, so phase 4 must define what "retain" means under the row cap.

## Decisions and invariants

| Decision                          | Invariant                                                                                    |
| --------------------------------- | -------------------------------------------------------------------------------------------- |
| Unix only                         | No Windows implementation is required or added.                                              |
| ioctl is authoritative            | `COLUMNS` must not override the live PTY size in production.                                 |
| One implementation                | ioctl and fallback logic exist only in `pkg/dimensions`.                                     |
| One snapshot per render operation | Related output uses identical width and height values.                                       |
| Signal-safe design                | `SIGWINCH` handlers never write to stdout or mutate viewport render state.                   |
| Logical viewport state            | Reflow never reconstructs content from already wrapped rows.                                 |
| Noninteractive safety             | Raw, structured, debug, and non-terminal output do not start an interactive resize renderer. |

## Phase decision log

| #   | Decision                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                  | Rationale                                                                                                                                                                                                                                                                                                                       |
| --- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| D1  | `Viewer` binds to the fd of the actual output writer and borrows it: it never closes the fd                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                               | R2-02: the observed size must match the written file; `table.TermWidth` queries stderr today, so `clai 2>/dev/null` renders at the 80-column fallback (2026-08-07, worker session 1)                                                                                                                                            |
| D2  | One size policy: a read is usable only when it succeeds and reports positive width and height; failures wrap `ErrUnavailable`; `Snapshot` returns the last valid size or `Fallback` (80x24) plus the wrapped error                                                                                                                                                                                                                                                                                                                                                                                        | R1-02: the legacy silent 80-column fallback stays reachable while callers can detect non-terminals with `errors.Is`; the viewer normalizes injected readers too, so the policy is uniform (2026-08-07, worker session 1)                                                                                                        |
| D3  | `Events` is a capacity-one channel of fresh `Dimensions`; bursts coalesce via non-blocking sends; the channel closes on stop and receivers use the two-value receive or `range`                                                                                                                                                                                                                                                                                                                                                                                                                           | The consumer needs one snapshot per redraw and must distinguish stop from resize; closing the channel gives `range` and `select` a uniform end-of-life signal (2026-08-07, worker session 1)                                                                                                                                    |
| D4  | The viewer owns the `signal.Notify(SIGWINCH)` registration when no source is injected; `WithSignalSource` replaces it in tests; a closed source stops the viewer                                                                                                                                                                                                                                                                                                                                                                                                                                          | R1-01/R1-06: unit tests must never send process-global signals; `signal.Stop` runs in the watcher's deferred cleanup so no registration leaks (2026-08-07, worker session 1)                                                                                                                                                    |
| D5  | Lifecycle: `New(ctx, fd, opts...)` performs the initial read and starts the watcher; `Stop` is idempotent, blocks until the watcher exited, and stays safe after context cancellation or a closed source; `Snapshot` stays usable after stop                                                                                                                                                                                                                                                                                                                                                              | Context cancellation, explicit stop, and source close must all release resources exactly once without deadlock or goroutine leak (2026-08-07, worker session 1)                                                                                                                                                                 |
| D6  | Platform split: the `TIOCGWINSZ` implementation is `unix`-tagged; `!unix` builds get an `ErrUnavailable` stub reader and a no-op SIGWINCH registration                                                                                                                                                                                                                                                                                                                                                                                                                                                    | Unix-only per the plan, but the package still compiles on other platforms; no Windows terminal implementation is added (2026-08-07, worker session 1)                                                                                                                                                                           |
| D7  | `table.TermWidth` is a pure compatibility wrapper: it delegates to `dimensions.DefaultReader(stderr)` and maps every provider failure to `(Fallback.Width, nil)`; new explicit-width truncation helpers (`WidthAppropriateStringTruncColoredWithWidth` and friends) use a supplied width exactly and never query the terminal                                                                                                                                                                                                                                                                             | The legacy implementation never returned an error, so preserving `(80, nil)` keeps existing callers usable; snapshot-aware helpers let phase 3 render one operation with one resolved dimension set (2026-08-07, worker session 2)                                                                                              |
| D8  | `utils.SessionDimensions(w)` is clai's single width-discovery point: it binds to the writer's fd and returns `dimensions.Fallback` on any non-terminal/failure path; `Querier` and `ChatHandler` each store one snapshot per session from their output writer, and standalone operations (glow, photo animation, tools listing, MCP debug) resolve one snapshot from the writer they use                                                                                                                                                                                                                  | R2-02: the observed size must match the written file; a single discovery point plus explicit-width table helpers makes the phase-3 acceptance criteria provable by repository search (2026-08-07, worker session 3)                                                                                                             |
| D9  | Phase 4 storage: `ActivityViewport` keeps an ordered list of logical `activityBlock`s (kind, full sanitized content, tool-call metadata, tool body budget) plus a derived visible-row cache; `NewActivityViewport(width, maxRows, terminalHeight)` binds the initial effective height to min(cap, terminal height) (R5-01 fix); `Resize(width, terminalHeight)` mutates state only (rewrap, effective height = min(cap, terminal height), dirty flag, no-op on equal dimensions) and `Render` emits one complete atomic frame on dirty state while the diff path stays for normal streaming appends; retention is bounded by `maxActivityBlocks` (16) and `maxActivityBlockRunes` (64 KiB) with over-budget content reduced to head and tail | R2-01: reflow must reconstruct content from logical blocks, never from wrapped rows; R1-04: a partial frame write leaves the viewport dirty so the next `Render` retries the full frame; the phase-5 watcher will be the only caller of `Resize`, invoked from the serialized session loop (2026-08-07, worker session 4; R5-01 fix in worker session 7)       |
| D10 | One dimensions watcher per query, started in `Querier.Query` via `startResizeWatcher` exactly when `usesActivityViewport()` holds and the writer is an `*os.File`; the viewer's `Events` channel is passed to the `sessionRunner` as `resizeEvents`, consumed in the `executeModelStep` select; `applyResize` stores the event as the new `q.dims`, resizes the viewport, and redraws; the three lazy viewport creation sites are unified in `ensureActivityViewport` reading the current snapshot; the watcher is stopped by a deferred `viewer.Stop` on success, error, and cancellation                | R2-03: the watcher gate must equal the viewport predicate and the first render after a pre-creation resize must use the new dimensions; R1-06: clai tests inject a channel into the runner, so no unit test sends a process-global signal; the observed fd must match the session writer (R2-02) (2026-08-07, worker session 5) |
| D11 | The temporary local `replace` stays until a maintainer tags and pushes a go_away_boilerplate release containing `pkg/dimensions`; phase 6 records the exact hand-off (tag, `go get`, remove replace, `go mod tidy`)                                                                                                                                                                                                                                                                                                                                                                                       | The final clai change must consume a released module version; removing the replace now would break the build because no released version contains `pkg/dimensions` (v1.33.8 is the newest release, verified via module proxy and `go list -m -versions`) (2026-08-07, worker session 6)                                         |

## Error severity

Critical, major, and normal findings reopen a phase. Minor findings do not reopen a phase unless they affect a stated invariant or acceptance criterion.

## Cross-repository integration

The implementation starts in `/home/imago/Projects/public/go_away_boilerplate`. During development clai may use a temporary local `replace` directive, but the final clai change must consume a released module version and must not retain the local replace.

## Session journal

### 2026-08-07

Created the worklog after investigating clai's cached `termWidth`, direct `table.TermWidth` calls, indirect `WidthAppropriateStringTrunc` calls, and the existing ioctl implementation in `go_away_boilerplate/pkg/table`.

### Phase 1 execution — 2026-08-07 (worker session 1)

Implemented phase 1 in `go_away_boilerplate/pkg/dimensions`. The API contract,
zero-size policy, and phase 2 mapping are recorded in the phase 1 spec
implementation notes; material decisions are D1-D6. Baseline and post-change
commands and results are in the phase 1 spec.

### Phase 2 execution — 2026-08-07 (worker session 2)

Implemented phase 2 in `go_away_boilerplate/pkg/table`. `TermWidth` now
delegates to `pkg/dimensions` through an injectable core; the private ioctl,
`COLUMNS` precedence, and `unsafe` code are gone. Explicit-width truncation
helpers were added, and the table tests now assert the shared provider
contract instead of environment precedence. The wrapper failure policy
resolves R1-02: `TermWidth` returns `(Fallback.Width, nil)` on any provider
failure. The API contract, error-coverage mapping, and commands and results
are in the phase 2 spec; material decision D7. All repository QA gates
passed; cross-platform builds passed for darwin/amd64, linux/arm64, and
windows/amd64.

### Phase 3 execution — 2026-08-07 (worker session 3)

Implemented phase 3 in clai. Completed the in-flight migration (broken photo
import, `messagePickerRow` arity, test `termWidth` field removal) and removed
the last terminal-querying truncation calls (`tools/cmd.go`, `tools/mcp`,
`chat/obfuscated_print.go`). `utils.SessionDimensions` is the single
width-discovery point; `Querier.dims` and `ChatHandler.dims` are the per-
session snapshots bound to the session writer's fd (R2-02). Material
decisions are recorded as D8; snapshot lifecycle (R1-03), the width-source
inventory (R1-05), and the error-coverage mapping are in the phase 3 spec.
Baseline and post-change commands and results are in the phase 3 spec.

### Phase 4 execution — 2026-08-07 (worker session 4)

Implemented phase 4 in clai (`internal/utils/print.go`). `ActivityViewport`
now stores logical blocks (kind, full sanitized content, tool-call metadata,
tool body budget) with a derived visible-row cache; the old wrapped-row
storage, builders, and `appendBlock`/`trim` are gone. `Resize` mutates state
only (clamped dimensions, effective height = min(cap, terminal height),
full rewrap, dirty flag, no-op on equal dimensions) and never writes;
`Render` emits one complete atomic frame on dirty state (move up, clear
down, rewrite) and keeps the diff path for normal appends, updating
bookkeeping only on success so a partial write leaves the viewport dirty
and the next render retries the full frame. Retention is bounded by
`maxActivityBlocks` (16) and `maxActivityBlockRunes` (64 KiB). The dupl
check flagged the pre-existing `AppendReasoning`/`AppendText` clone, merged
into the shared `appendCoalescing` helper. Material decisions are recorded
as D9; the storage model, retention policy, rewrap algorithm, frame
contract, and error-coverage mapping are in the phase 4 spec. Baseline and
post-change commands and results are in the phase 4 spec; all repository QA
gates pass (`go test ./... -race -cover -count=3 -timeout=30s` exit 0,
internal/utils coverage 81.4%).

### Phase 5 execution — 2026-08-07 (worker session 5)

Implemented phase 5 in clai (`internal/text/querier.go`,
`internal/text/session_runner.go`, `internal/text/tool_executor.go`).
`Querier.Query` starts one dimensions watcher via `startResizeWatcher`
exactly when `usesActivityViewport()` holds and the writer is an `*os.File`,
and defers `viewer.Stop` so the watcher ends on success, error, and
cancellation. The viewer's `Events` channel is passed to the `sessionRunner`
as `resizeEvents`; the `executeModelStep` select consumes it and
`applyResize` stores the fresh snapshot as `q.dims`, resizes the viewport,
and redraws before subsequent output. The three lazy viewport creation sites
are unified in `ensureActivityViewport`, which reads the current snapshot so
a resize before the first reasoning/tool event makes the first render use
the new dimensions (R2-03). Material decision D10; the watcher gate,
consumption ordering (R1-03), signal-injection strategy (R1-06), and the
error-coverage mapping are in the phase 5 spec. Architecture docs
`colours.md` and `query.md` gained the resize behavior. Baseline and
post-change commands and results are in the phase 5 spec; all repository QA
gates pass (`go test ./... -race -cover -count=3 -timeout=30s` exit 0,
internal/text coverage 72.3%).

### Review 1 — 2026-08-07

Baseline verification: `go test ./...` passed in both clai and `/home/imago/Projects/public/go_away_boilerplate`. No implementation exists yet, so implementation-specific acceptance criteria and repository QA gates could not be verified. The plan is not ready for execution without resolving the findings below. The main risk is that “snapshot”, “fallback”, and “complete atomic frame” currently describe desired behavior but not testable API contracts.

### Review 2 — 2026-08-07

Re-ran `go test ./... -count=1 -timeout=120s` in both repositories: clai passed, go_away_boilerplate passed. No implementation exists; all phases remain Not Started. Verified the motivation against source: cached `Querier.termWidth` (querier_setup.go:207-210); direct `table.TermWidth` calls (photo/funimation_0.go:16, utils/print.go:148, chat/handler_list_chat.go:637, text/tool_executor.go:330); indirect truncation discovery (tools/cmd.go:55, tools/mcp/tool.go:92, chat/handler_dir.go:99, chat/obfuscated_print.go:126, chat/handler_list_chat.go:683/699/845/998); the single width implementation in go_away_boilerplate/pkg/table/term.go (COLUMNS precedence, stderr ioctl, fallback 80); and no SIGWINCH handling in clai. clai consumes released go_away_boilerplate v1.33.8 with no local replace. The R1 findings remain open and are confirmed against code. Two new findings refine the specs: R2-01 (major, phase 4 — the viewport stores wrapped rows, not logical blocks, so the retention/reflow model must be specified) and R2-02/R2-03 (normal, phases 1/3/5 — fd binding and watcher-gate/ordering). Verdict unchanged: the plan is not ready for execution until R1-01, R1-02, and R1-03 are resolved in the phase specs; R1-04 and R2-01 were resolved in the phase 4 spec amendment (review 2). Direction and motivation are sound; the work will increase clai quality if executed against the tightened contracts.

### Review 3 — 2026-08-07

Phase 3 is complete and all repository QA gates pass (`go test ./... -race
-cover -count=3 -timeout=30s` exit 0; gofumpt, staticcheck, go vet, go fix,
dupl clean; dupl clones are pre-existing and outside phase-3 files).
Acceptance criteria verified by search: no production `table.TermWidth` call
and no terminal-querying truncation helper remain in clai source; every
width-aware path uses `q.dims`/`cq.dims` or `utils.SessionDimensions`.
`clai 2>/dev/null` now observes stdout (R2-02): the querier and chat handler
bind the snapshot to the session output writer. Phase 4 (viewport reflow) is
the next eligible phase.

### Phase 6 execution — 2026-08-07 (worker session 6)

Implemented phase 6 in both repositories. `make qa` in clai and the
individual mandated commands in both repositories all pass unedited
(gofumpt, staticcheck, go vet, go fix, `go test ./... -race -cover -count=3
-timeout=30s`, dupl). Coverage: `pkg/dimensions` 100.0%, `pkg/table`
96.6%, internal/utils 81.3%, internal/text 72.3%. Cross-platform builds
pass for darwin/amd64, linux/arm64, and windows/amd64; `go mod verify`
passes. Repository searches confirm the single-system state: no production
`table.TermWidth` call and no terminal-querying truncation helper remain in
clai; `TIOCGWINSZ`/`unsafe`/`COLUMNS` exist only in `pkg/dimensions`;
`SIGWINCH` writes never happen in a signal callback. Dupl findings are all
pre-existing and outside the changed regions. The released-dependency
verification recorded v1.33.8 as the newest released version; no released
version contains `pkg/dimensions`, so the temporary local `replace` remains
until the maintainer tags and pushes the boilerplate release (D11, exact
hand-off in the phase 6 spec). Architecture documentation required no
further update. Material decision D11.

### Review 4 — 2026-08-07

Holistic review of the completed worklog (all six phases). Re-ran every
gate independently in both repositories: `make qa` in clai; gofumpt,
staticcheck, go vet, go fix, `go test ./... -race -cover -count=3
-timeout=30s`, and dupl in both; cross-platform builds for darwin/amd64,
linux/arm64, and windows/amd64; `go mod verify`. All pass. Traced the
invariants through the code: the viewer lifecycle (D1-D6), the table wrapper
(D7), the per-session snapshots (D8), the viewport storage/Resize/Render
contract (D9), and the watcher gate and resize consumption (D10) hold on
every branch examined, including the failure and teardown paths. No code
defect was found; the only open item is the maintainer-only release hand-off
(R4-01, also D11). R4-02 is a minor non-reopening observation. Verdict:
ready.

### Review 5 — 2026-08-07

Independent re-review of the completed worklog. Re-ran the gates myself in
both repositories: `go test ./... -race -cover -count=3 -timeout=30s` (exit
0, twice, coverage matching the records: pkg/dimensions 100.0%, pkg/table
96.6%, internal/utils 81.3%, internal/text 72.3%), gofumpt, staticcheck,
`go vet`, `go fix`, dupl (clai 31 groups, boilerplate 6, all pre-existing
and outside the changed regions), `go mod verify`, and cross-platform builds
for darwin/amd64, linux/arm64, and windows/amd64 — all pass. One `make qa`
run failed on the pre-existing vendored `Test_context` flake
(claude_stream_test.go:267, untouched by this work); the identical mandated
test command passed in the other two full-suite runs and the package passed
in isolation (R5-02). Traced the invariants through the code again: the
viewer lifecycle, the table wrapper, the per-session snapshots, the
viewport storage/Resize/Render contract, and the watcher gate and resize
consumption all hold on the failure and teardown branches examined. One
contract deviation found: the initial viewport height is the cap, not
min(cap, terminal height), so the phase-4 AC "effective height never
exceeds terminal height or configured cap" and the colours.md statement
"The effective window height is min(window-cell-height, terminal height)"
are unmet until the first SIGWINCH (R5-01, normal, reopens phase 5). The
release hand-off (R4-01/D11) remains the only other open item. Verdict:
not ready as recorded — phase 5 is reopened for R5-01; everything else
verified good.

### Phase 5 R5-01 fix — 2026-08-07 (worker session 7)

Closed R5-01 in clai. `ActivityViewport` construction now binds the initial
effective height: `NewActivityViewport(width, maxRows, terminalHeight)` sets
`height = min(maxRows, max(terminalHeight, 1))` from the start, and
`ensureActivityViewport` passes `q.dims.Height`, so the first frame renders
at `min(cap, terminal height)` even without a resize. The phase-4 AC
"Effective height never exceeds terminal height or configured cap" and the
`colours.md` statement hold from the first frame; no doc amendment was
needed. `Test_sessionRunner_Run_InitialHeightBindsTerminalHeight` proves the
session-boundary behavior (5-row terminal, default 30-row cap, no resize:
the first reasoning render shows five rows and drops the middle), and the
viewport unit test `initial height binds the terminal height at creation`
pins the constructor and the live grow. Test fixtures with a bare
`dims.Width` gained `Height: 24` (the fallback height) to match the D2
invariant that the snapshot always carries positive width and height. All
repository QA gates pass; material decision updated in D9 (constructor now
binds the initial height).

## Feedback index

| ID    | Severity | Phase                                                                            | Summary                                                                                                                                                                                                                                                                  |
| ----- | -------- | -------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| R1-01 | Major    | [1](./phase-1-dimensions-package.md)                                             | Define the Viewer API, lifecycle, fd ownership, and injectable signal source.                                                                                                                                                                                            |
| R1-02 | Major    | [1](./phase-1-dimensions-package.md), [2](./phase-2-table-migration.md)          | Resolve zero-size, ioctl failure, and fallback semantics before migration.                                                                                                                                                                                               |
| R1-03 | Major    | [3](./phase-3-clai-width-unification.md), [5](./phase-5-sigwinch-integration.md) | Define snapshot ownership and refresh timing for every render operation. Resolved in phase 5 implementation notes.                                                                                                                                                       |
| R1-04 | Major    | [4](./phase-4-viewport-reflow.md), [5](./phase-5-sigwinch-integration.md)        | Specify atomic frame writes and failure behavior at the writer boundary. Resolved in phase 4 spec (review 2 amendment).                                                                                                                                                  |
| R1-05 | Normal   | [3](./phase-3-clai-width-unification.md)                                         | Turn the broad width-aware-path list into an auditable source/test inventory.                                                                                                                                                                                            |
| R1-06 | Normal   | [5](./phase-5-sigwinch-integration.md)                                           | Make SIGWINCH injection and process-global signal ownership testable. Resolved in phase 5 implementation notes.                                                                                                                                                          |
| R1-07 | Normal   | [6](./phase-6-quality-gates.md)                                                  | Name the exact required QA commands and cross-repository evidence.                                                                                                                                                                                                       |
| R2-01 | Major    | [4](./phase-4-viewport-reflow.md)                                                | Specify the viewport's logical-block storage model; wrapped-row storage cannot reflow. Resolved in phase 4 spec (review 2 amendment).                                                                                                                                    |
| R2-02 | Normal   | [1](./phase-1-dimensions-package.md), [3](./phase-3-clai-width-unification.md)   | Bind the snapshot fd to the session writer; `table.TermWidth` queries stderr today.                                                                                                                                                                                      |
| R2-03 | Normal   | [5](./phase-5-sigwinch-integration.md)                                           | Name the watcher gate and handle resize-before-lazy-viewport-creation. Resolved in phase 5 implementation notes.                                                                                                                                                         |
| R4-01 | Normal   | [6](./phase-6-quality-gates.md)                                                  | Release the go_away_boilerplate changes and remove the local replace (maintainer-only; tracked as D11). Open in review 4.                                                                                                                                                |
| R4-02 | Minor    | [1](./phase-1-dimensions-package.md), [6](./phase-6-quality-gates.md)            | `dimensions.New` documents but does not guard a nil ctx; panic on documented precondition violation is idiomatic, so no guard or test is required. Non-reopening.                                                                                                        |
| R5-01 | Normal   | [4](./phase-4-viewport-reflow.md), [5](./phase-5-sigwinch-integration.md)        | Initial viewport height is the cap, never the terminal height, so "effective height never exceeds terminal height or configured cap" is unmet until the first resize; fix at `ensureActivityViewport` creation or amend the AC/docs. Resolved in phase 5 (worker session 7): the constructor now binds the initial height to min(cap, terminal height). |
| R5-02 | Minor    | [6](./phase-6-quality-gates.md)                                                  | `make qa` flaked once on the pre-existing vendored `Test_context` (anthropic) — timing flake in untouched code; the mandated command passed in the other full-suite runs. Non-reopening.                                                                                 |

The review should question untested meaningful behavior, especially error and
cleanup paths, but should not require artificial tests for unreachable or
mechanical branches. Prefer focused behavioral tests over coverage-driven API
or implementation choices.
