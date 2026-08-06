# Phase 5 — SIGWINCH integration

**Status:** Done  
[Back to README](./README.md)

## Goal

Reflect tmux pane resizes in clai's interactive streaming output without concurrent terminal writes.

## Specification

Start one dimensions watcher only for terminal rolling-output sessions. Consume resize notifications in the serialized session loop alongside completion events. Refresh dimensions, resize the viewport, and redraw before subsequent output. Coalesce bursts. Never render in the signal callback. Stop the watcher on query completion, cancellation, error, or noninteractive mode.

## Integration contract

| Trigger | Collaborator | Observable result | Required side effect | Prohibited side effect |
|---|---|---|---|---|
| tmux pane resized | SIGWINCH/viewer | Next frame reflects new width/height | Fresh ioctl query and viewport redraw | No tmux command |
| Resize during token handling | session loop | Resize is applied before next render | Writes remain serialized | No concurrent writer use |
| Burst of resize signals | buffered watcher | Final/latest dimensions are used | Notifications coalesce | No unbounded queue |
| Query ends/cancels | watcher | Resources stop cleanly | Signal registration removed | No post-query redraw |
| Raw/structured/non-terminal mode | output mode | No interactive resize behavior | Watcher is not started | No ANSI viewport output |

## Acceptance criteria

- A simulated `SIGWINCH` changes rolling output dimensions during streaming.
- No concurrent terminal writes occur under race testing.
- Resize bursts are safe and converge on the latest dimensions.
- Watcher cleanup is verified on success, error, and cancellation.
- Final answer and tool transitions remain valid after a resize.
- New watcher/session integration code has focused tests for startup rejection, refresh failure, termination paths, pending notifications, and writer errors.

## Error coverage

| Failure | Expected behavior | Test |
|---|---|---|
| Dimension refresh fails after signal | Preserve last valid snapshot and continue or report documented warning | refresh failure test |
| Stream ends while resize pending | Finish once without late redraw | termination race test |
| Context cancellation during watch | Stop watcher and query cleanly | cancellation integration test |
| Output writer fails | Return render error without goroutine leak | writer failure test |
| watcher startup is rejected | Session falls back or returns the documented error without starting partial cleanup | watcher-start failure test |
| notification arrives after termination | No redraw or write occurs after teardown | late-notification test |

## Implementation notes

### Watcher gate and lifecycle (R2-03)

One dimensions watcher runs only for terminal rolling-output sessions. It
starts exactly when `usesActivityViewport()` holds —
`RollingOutputEnabled && !rawDisplay && !debug && !structuredOutput`, where
`rawDisplay` encodes the terminal check — and the session writer is an
`*os.File`. `Querier.Query` starts it via `startResizeWatcher(ctx)` before the
runner and `defer`s the viewer's `Stop`, so the watcher ends on success,
error, and cancellation alike. Non-rolling terminal sessions, raw,
structured, debug, and redirected output never start a watcher and keep
phase 3's one-shot `q.dims` read (D8). The watcher binds to the session
writer's fd — the file clai actually writes to — so the observed size always
matches the output target (R2-02).

The watcher's event channel is passed to the `sessionRunner` as
`resizeEvents`; the runner consumes it in the serialized `executeModelStep`
select alongside completion events. `signal.Notify(SIGWINCH)` ownership and
the injectable signal source live in `pkg/dimensions` (D4); clai injects a
channel into the runner in tests, so no unit test sends a process-global
signal (R1-06). The viewer releases the registration on stop (verified in
go_away_boilerplate viewer tests and in
`Test_Querier_startResizeWatcher`).

### Resize consumption and ordering (R1-03)

When the select receives a fresh `dimensions.Dimensions` value,
`applyResize` (1) stores it as the new session snapshot `q.dims`, (2) calls
`ActivityViewport.Resize(width, height)`, which rewraps every retained
logical block at the new width and marks the viewport dirty, and (3) calls
`Render`, which emits the complete atomic frame (phase 4 contract). The
snapshot used for the next frame is therefore the event's dimensions, and
all subsequent renders — tokens, tools, the final-answer pop — use it. A
failed re-query never delivers an event (the viewer keeps the last valid
snapshot), so the session loop simply continues with the last applied
`q.dims` and viewport state: preserve-last-valid-value, no warning needed.

The viewer's `Events` channel has capacity one and coalesces bursts (D3); a
burst of resizes converges on the latest dimensions because each delivered
value is applied in order and `Resize` is a no-op for equal dimensions.
When a token, completion, error, and resize are simultaneously ready, Go's
select picks arbitrarily; every order converges because the resize redraw
happens before the loop continues and the redraw replaces the stale region.
When the stream ends while a resize is pending, the pending value is never
applied: the query finishes once without a late redraw.

### Resize before lazy viewport creation (R2-03)

The three lazy viewport creation sites (reasoning event, tool activity,
assistant-text finalization) are unified in `Querier.ensureActivityViewport`,
which reads the current `q.dims` snapshot. Because `applyResize` stores the
fresh snapshot before any later event can create the viewport, a resize that
arrives before the first reasoning/tool event makes the first render use the
new dimensions immediately. The constructor receives the session snapshot's
height (`NewActivityViewport(width, cap, q.dims.Height)`), so the initial
effective height is `min(cap, terminal height)` from the first frame; the
first render never exceeds the terminal even without a resize (R5-01). Every
later resize keeps the same bound.

### Error-coverage mapping

| Failure | Behavior | Test |
|---|---|---|
| Dimension refresh fails after signal | Viewer keeps last valid snapshot and delivers no event; the loop continues with the applied `q.dims` (viewer-side: `TestSnapshot_RefreshFailsAfterValid_KeepsLastValid` in go_away_boilerplate; loop-side: `Test_sessionRunner_Run_NoWatcherFallsBackToOneShotRead` and the unchanged-dims assertions) | refresh failure test |
| Stream ends while resize pending | Query finishes once; no late redraw; output unchanged after teardown | `Test_sessionRunner_Run_StreamEndsWhileResizePendingNoLateRedraw` |
| Context cancellation during watch | Loop returns promptly on `ctx.Done`; watcher stops via `Query`'s deferred stop | `Test_sessionRunner_Run_ContextCancellationStopsCleanly` |
| Output writer fails during resize redraw | Render error propagates (`render activity viewport after resize`); run stops without goroutine leak | `Test_sessionRunner_Run_ResizeRenderWriterFailureReturnsError` |
| Watcher startup is rejected | Nil event channel + no-op stop; session falls back to the one-shot read without partial cleanup | `Test_sessionRunner_Run_NoWatcherFallsBackToOneShotRead`, `Test_Querier_startResizeWatcher` (raw, structured, non-file) |
| Notification arrives after termination | Never applied; no redraw or write after teardown | `Test_sessionRunner_Run_StreamEndsWhileResizePendingNoLateRedraw` |
| Watcher channel closes mid-stream | Resize case nils out; stream finishes normally | `Test_sessionRunner_Run_ResizeChannelClosedMidStreamKeepsStreaming` |
| Resize before first reasoning/tool event | First render uses the new dimensions (no old-width row) | `Test_sessionRunner_Run_ResizeBeforeViewportCreationUsesNewDimensions` |
| Resize burst | Each redraw serialized; last event wins | `Test_sessionRunner_Run_ResizeBurstConvergesOnLatestDimensions` |
| Resize during streaming | Rolling output rewraps at the new width; trailing tokens render at it | `Test_sessionRunner_Run_ResizeDuringStreamingRewrapsRollingOutput` |
| Resize across a tool transition | Thinking/prose/tool order and final answer stay valid | `Test_sessionRunner_Run_ResizeKeepsToolTransitionAndFinalAnswerValid` |

### Commands and results

Baseline (start of session):

```bash
cd /home/imago/Projects/public/clai && go test ./internal/text/ ./internal/utils/ -count=1 -timeout=120s
```

Passed: exit 0 (phase-4 state).

After the change:

```bash
go test ./... -race -cover -count=3 -timeout=30s
```

All packages ok, exit 0 (internal/text 72.3% coverage). The new
`resize_runner_test.go` measures 100% for `applyResize`,
`startResizeWatcher`, and `ensureActivityViewport`; the resize select case
is exercised by every streaming resize test.

```bash
go run mvdan.cc/gofumpt@latest -w -l .
go run honnef.co/go/tools/cmd/staticcheck@latest ./...
go vet ./...
go fix ./...
go run github.com/mibk/dupl@latest -t 80 .
```

All clean; dupl reports only pre-existing clones outside phase-5 files.

```bash
cd /home/imago/Projects/public/go_away_boilerplate && go test ./pkg/dimensions/... -count=1 -timeout=60s
```

Passed: exit 0 (no library change in phase 5).

### R5-01 fix — 2026-08-07 (worker session 7)

```bash
cd /home/imago/Projects/public/clai && go test ./... -count=1 -timeout=120s
```

All packages ok, exit 0. `NewActivityViewport` gained the terminal-height
parameter (`NewActivityViewport(width, maxRows, terminalHeight)`, effective
height `min(maxRows, max(terminalHeight, 1))` from the start) and
`ensureActivityViewport` passes `q.dims.Height`, closing R5-01: the initial
frame renders at `min(cap, terminal height)` even without a resize. New
tests: `Test_sessionRunner_Run_InitialHeightBindsTerminalHeight` (session
boundary) and `initial height binds the terminal height at creation`
(viewport unit).

```bash
go run mvdan.cc/gofumpt@latest -w -l .
go run honnef.co/go/tools/cmd/staticcheck@latest ./...
go vet ./...
go fix ./...
go run github.com/mibk/dupl@latest -t 80 .
```

All clean.

## Review findings (review 1, 2026-08-07)

**R1-03 — Major — resolved in implementation notes.** [x] The snapshot used
for the next frame is the delivered event's dimensions, stored as `q.dims`
and applied to the viewport before the redraw; a failed refresh delivers no
event, so the last valid snapshot survives. Ordering among simultaneously
ready token/completion/error/resize cases converges because the resize
redraw replaces the stale region before the loop continues, and a pending
resize at stream end is never applied (no late redraw).

**R1-06 — Normal — resolved in implementation notes.** [x] clai tests inject
a channel into the runner and never send process-global signals; signal
registration ownership and release stay in `pkg/dimensions` (D4) and are
verified by the library's viewer tests plus `Test_Querier_startResizeWatcher`
(cleanup on stop). Cleanup on success, error, and cancellation is verified by
the runner termination tests.

Verified good: rendering stays in the serialized loop, bursts coalesce, and
raw, structured, redirected, and non-terminal output never start the
watcher.

## Review findings (review 2, 2026-08-07)

**R2-03 — Normal — resolved in implementation notes.** [x] The watcher gate
is `usesActivityViewport()` and the writer must be an `*os.File`; the
watcher starts exactly then, and non-rolling terminal sessions keep the
one-shot read. The three lazy creation sites are unified in
`ensureActivityViewport`, which reads the current snapshot; a resize before
the first reasoning/tool event is applied to `q.dims` first, so the first
render uses the new dimensions immediately.

Verified good: the resize case plugs into the single streaming entry point
(`executeModelStep` select), and the cleanup-on-termination matrix
(success, error, cancellation, late notification) is covered by tests.

## Review findings (review 5, 2026-08-07)

**R5-01 — Normal — resolved.** [x] The initial viewport height is the configured
cap, never the terminal height, so the phase-4 acceptance criterion
"Effective height never exceeds terminal height or configured cap" is unmet
on the initial path until the first `Resize`. `ensureActivityViewport`
(querier.go:252) creates `NewActivityViewport(q.dims.Width,
RollingOutputWindowCellHeight())`; `q.dims.Height` (available since
`NewQuerier`) is never consulted at creation, and no `Resize` runs until a
SIGWINCH arrives. `colours.md` states unconditionally "The effective window
height is min(window-cell-height, terminal height)", which is false before
the first resize. Concrete failure scenario: a 24-row terminal with the
default 30-row cap and no resize during the whole query. The first reasoning
render emits 30 rows; rows 1-6 scroll off the top immediately, so the
"∴ thinking" header is invisible; the first dirty redraw (for example the
final-answer pop) emits `\x1b[30A\r\x1b[J`, which the terminal clamps to the
top of the visible region and clears the entire screen instead of the
window region. The phase-5 implementation notes document this as deliberate
("The initial effective height stays the configured cap until the first
resize, matching the phase-4 contract"), but the phase-4 AC and the
architecture doc state the min() guarantee without an exception.

The fix: `NewActivityViewport` now takes the raw terminal height as a third
parameter and binds the effective height to `min(cap, max(terminalHeight, 1))`
at creation; `ensureActivityViewport` passes `q.dims.Height`, so the initial
height is `min(cap, terminal height)` and the first frame never exceeds the
terminal. The phase-4 AC "Effective height never exceeds terminal height or
configured cap" and the `colours.md` statement now hold from the first frame
without amending either document. The concrete scenario now renders a
24-row window: the "∴ thinking" header stays visible and the final-answer
pop clears exactly the drawn window. Tests:
`Test_sessionRunner_Run_InitialHeightBindsTerminalHeight` (session boundary,
first render bounded without any resize) and `initial height binds the
terminal height at creation` (viewport unit).

Verified good in this phase: the watcher gate equals the viewport predicate
(`usesActivityViewport() && *os.File`), the deferred `viewer.Stop` runs on
success, error, and cancellation, the resize event is consumed only in the
serialized `executeModelStep` select, a closed channel nils the case,
`applyResize` stores the event as `q.dims` before any lazy viewport
creation, a pending resize at stream end is never applied, and a resize
redraw writer error propagates and stops the run. Every one of these holds
on the failure and teardown branches I re-traced in review 5.