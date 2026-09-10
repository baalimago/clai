# Phase 9 — Summarize progress board (shared with audio)

**Status:** Complete

[← README](./README.md)

## Goal

Give `chat summarize` the terminal UI of the audio transcribe parallel
flows: the in-place progress table with a ▸ title, phase line, marked
rows, live elapsed times and a footer. Extract that table from
`internal/audio/board.go` into a reusable package so both consumers render
identically (maintainer request, 2026-09-10).

Reads: this file and the README.

## Specification

### `internal/board`

- `Config{Title, Index, Columns []Column{Name, Width, Mark}, Time}`,
  `Row{Cells, Mark, Active, Started, Elapsed}`, `New(out, live, cfg, rows)`.
- Methods: `Update`, `SetPhase`, `SetFooterFunc`, `Log`, `Tick`,
  `Animate`, `Finish`, `Rows`, `Footer`, `SetClock`, `SetWidth`.
- Helpers: `InFlight`, `ElapsedText`, `Truncate`; constants `MarkDone`,
  `MarkWarn`, `MarkPending`, `Tick`.
- Rendering is byte-identical to the audio board for the audio layout:
  the board owns the position column (width five) and the elapsed column,
  a `Mark` column's header is two wider than its cells, a finished `✗`
  row is coloured in the tool colour, plain mode prints no column header.
- `Log` is new: live mode moves over the drawn frame, prints the line,
  resets the drawn count and redraws; plain mode prints the line.

### Audio adapter

`internal/audio/board.go` keeps `newProgressBoard(out, live, title, spans)`
returning `*board.Board` with the diarization columns, the `col*` indices,
`unknownSoFar`, `spanText` and `clock`. `coordinator.go` and `split.go`
address cells through the indices; no behaviour change (their tests assert
the rendered strings).

### `chat summarize`

- `summarizeProgress` interface: `started(worker, id)`, `finished(res)`,
  `done(selected, labelled, failed)`; `plainSummarizeProgress` is today's
  output (lines, JSON under `-r`); `boardSummarizeProgress` is the board.
- Live iff `forceLive || (!raw && utils.Live && IsTerminalWriter(out))`;
  the board writes to `cq.out` so a piped stdout keeps the plain lines.
- Board: index `slot`, one row per worker; columns conversation (16),
  state (22, mark), model (16), tokens (7); title
  `summarizing  N conversations since <time>  W workers  model <m|ladder>`;
  phase `summarizing · N conversations · W workers`, then
  `done · a labelled · f failed · elapsed`; every result logged above the
  table as `✓ <id>  <title>` or `✗ <id>  <error>`; footer
  `labelled a/n · failed f · in flight k · <elapsed> elapsed · eta ~x`
  where the estimate is the mean wall time of finished jobs times the
  remaining rounds over the workers (`eta ·` before the first result);
  `Finish` footer is the totals line.
- Test seams on `ChatHandler`: `forceLive`, `boardWidth`, `now`.

| Invariant | Test |
|---|---|
| Audio diarization and split output unchanged | existing `internal/audio` coordinator and split tests (rendered strings) |
| Board layout, plain/live modes, log-above, finish idempotence, animate stop, nil safety, helpers | `internal/board` tests |
| Live summarize: rows per worker, logged results, totals footer, no plain lines | `TestHandleSummarize_liveBoard` |
| Board only on an interactive terminal | `TestHandleSummarize_liveOnlyOnInteractiveTerminal` |
| Footer estimate | `TestBoardSummarizeProgress_footerEstimate` |
| Plain and `-r` output unchanged | existing `TestHandleSummarize_*`, batch e2e rows |

## Integration contract

| Trigger | Collaborators / fakes | Observable result | Required side effects | Prohibited side effects |
|---|---|---|---|---|
| `chat summarize` with `forceLive`, two workers, three seeded rows of which one fails, fixed width and clock | fake summarizer | output holds the ▸ title, the phase, the column header, `✓ a  title-a`, `✗ bad  boom`, `labelled 2/3 · failed 1`, `phase  done · 2 labelled · 1 failed`, the totals footer, cursor control | files and index labelled as in plain mode | no `a: title-a` plain line; the title appears once |
| Same through `run()` in a pipe (`clai c summarize -y -sm test 7d` in the e2e helpers) | mock vendor | today's plain lines and totals | as phase 5 | no `▸`, no cursor control |

## Acceptance criteria

| Outcome | Test |
|---|---|
| Shared package at or above the coverage floor | `go test ./internal/board/ -cover` (97.9% at authoring) |
| Audio unchanged | `go test ./internal/audio/` |
| Summarize board rows | tests named in the invariant table |
| Docs | `architecture/board.md`, README index row, `audio.md` key-files row, `summaries.md` batch step five |

Files: `internal/board/board.go`, `internal/board/board_test.go`,
`internal/audio/board.go`, `internal/audio/board_test.go`,
`internal/audio/coordinator.go`, `internal/audio/coordinator_test.go`,
`internal/audio/split.go`, `internal/chat/handler.go`,
`internal/chat/handler_summarize.go`, `internal/chat/handler_summarize_test.go`,
`architecture/board.md`, `architecture/README.md`, `architecture/audio.md`,
`architecture/summaries.md`.

## Error coverage

| Failure | Expected outcome | Test |
|---|---|---|
| A job fails | `✗` row and log line, counted in the footer, exit non-zero as before | `TestHandleSummarize_liveBoard` |
| Out-of-range or nil board calls | no-ops | `TestBoardNilAndBoundsAreSafe` |
| Context cancelled mid-run | `Animate` exits, `Finish` still draws the final frame from `done` | `TestBoardAnimateStopsOnFinish`, `TestHandleSummarize_flushesOnCancel` (plain) |

## Implementation notes

Session `session_01KXDFdab6MmuuKetC1EBowJ`, 2026-09-10 11:30–12:20 UTC.
Maintainer request: "nice UI for the summarization, inspired by the audio
transcribe parallel flow, reuse the same tabular structure, extract it
into a reusable table". Deltas:

- **Extraction, not a rewrite.** `internal/board` is the audio board
  with the domain removed: the audio row's seven typed fields became
  `Row.Cells` addressed by the adapter's `col*` indices, the fixed
  format strings became `Config.Columns` with a `Mark` column, and the
  position/elapsed columns stayed board-owned. The audio coordinator and
  split tests, which assert rendered strings (`✓ calibrated · 6 speakers`,
  `4/4    22:30–30:00`, `phase  bootstrap …`), pass unchanged, which is
  the parity proof. `board.Tick` is exported because a coordinator test
  paces a slow assembler by it.
- **`Log` is the one addition**: a line that stays above the live frame.
  Without it the summarize board would either drop the titles (per-worker
  rows only) or need one row per conversation, which does not fit a
  terminal for a wide window.
- **Rows are worker slots, not conversations.** Audio's rows are cores
  (bounded); a `-since 30d` window can select hundreds of conversations,
  so the table has `-workers` rows and the conversation history scrolls
  above it through `Log`. Flippable in one place if per-conversation rows
  are wanted for small windows.
- **The board writes to stdout** (`cq.out`), unlike audio, which puts
  status on stderr and the transcript on stdout: summarize's output *is*
  the result lines, so live mode routes them through `Log` on the same
  stream and a piped stdout keeps today's plain lines; `-r` and `-n`
  never draw.
- The footer's estimate is mean wall time of finished jobs × remaining
  rounds over the workers; `eta ·` until the first result. The `tokens`
  column sums every `purpose: "summary"` row of the conversation (a
  `-force` run therefore shows the cumulative figure), a hint rather than
  an invoice.
- Test clocks: the board reads the clock while rendering, so the estimate
  test advances an explicit clock; the live-board test's stepping clock
  only has to stay monotonic.

Maintainer follow-up (12:30 UTC): "conversation column is too narrow;
show the job index in the slot column; time didn't seem to update".

- `board.Row.Index` overrides the position text; summarize sets it to
  `job/selected` when a slot starts a job (`TestBoardRowIndexOverride`,
  the live test rejects `1/2`-style slot positions).
- The layout is now sized from the terminal (`boardWidth` seam, else
  `utils.SessionDimensions(cq.out)`): fixed columns state twelve, model
  fourteen, tokens six, time seven; the conversation column takes the
  rest between twelve and a full UUID (`TestHandleSummarize_liveBoardNarrow`
  at eighty and one hundred forty columns). The model moved from the
  title to the phase line so the title fits eighty columns.
- "Time didn't update": the frames do advance (a probe with a slow fake
  saw the active row's elapsed go `0s`, `1s`, `2s` over thirteen frames),
  but the old row was eighty-four characters wide while the dimensions
  fallback — and many terminals — are eighty, and the board truncates
  whole lines to the width, so the trailing `time` column rendered as `…`.
  The width-aware layout keeps `time` on screen at eighty columns. The
  rule ("a consumer whose fixed columns exceed the width loses its last
  column") is recorded in `architecture/board.md`.

Verification: `go test ./internal/board/ -race -count=3 -cover` → ok,
97.9%; `go test ./internal/audio/ -race -count=3 -cover` → ok, 88.9%;
`go test ./internal/chat/ -race -count=3 -cover` → ok, 76.3% (was 75.4%).
`make qa` on the final tree from load 0.9: exit 0, 46 packages `ok`
(`internal/board` is the new one), root 21.8 s; after the follow-up
(index override, width-aware layout) `make qa` again exit 0, 46 `ok`; `go vet` and `dupl` (31
baseline groups, none in the new package) clean.

## Review findings

### Review 3 — 2026-09-10 (`worklog-review`, holistic)

- [x] **R3-01 (Medium)** — Colour is applied before truncation, and
  `Truncate` (`internal/board/board.go:294`) counts escape runes as
  columns. `columns()` colours the whole header line, `rowLine` colours a
  whole `✗` row, and `render` then cuts each line to the terminal width by
  rune count. **Scenario:** colour on (the terminal default) at eighty
  columns: the summarize header is sized to exactly eighty visible runes
  plus twenty-three escape runes, so it is cut nineteen columns early —
  `model  tokens  time` vanish from the header and the dropped `\x1b[0m`
  leaves every following row painted in the header colour until a `✗` row
  resets it; a `✗` row likewise loses its `time` cell and bleeds the tool
  colour downward; the audio header (one hundred and one visible runes)
  is cut too. The phase note "the width-aware layout keeps `time` on
  screen at eighty columns" holds only with `ancli.UseColor=false`, which
  every board and summarize test sets, so no test can see it. Fix:
  truncate the plain line in `render` and colour afterwards (header,
  columns, warn rows, the phase label), or make `Truncate` skip
  `\x1b[...m` runs and re-append the reset when it cuts a coloured line;
  add a colour-on live test at eighty columns asserting the last column
  and a trailing reset.
- [x] **R3-13 (Low)** — Shared-package quality items. `Update`, `SetPhase`,
  `SetFooterFunc` and `Log` ignore `closed` while `Tick`/`Animate` honour
  it: a late call after `Finish(footer, extra...)` moves up only `drawn`
  lines and overwrites the extras (reproduced by the reviewer; no current
  consumer reaches it, but `board.md` promises only that later `Finish`
  calls are no-ops). The frame-shrink branch (`board.go:415`) is untested
  yet is the default error-exit frame of calibrated diarization (the
  deferred `Finish(c.board.Footer())` passes an empty footer because the
  coordinator only ever sets `footerFn`). `Truncate` counts runes, not
  display width, so a CJK or emoji title wraps and the redraw leaks the
  header's first half into scrollback on every tick (`x/text/width` is
  already a dependency). The constant `board.Tick` and the method
  `(*Board).Tick()` share a name; `MarkDone`/`MarkWarn`/`MarkPending` have
  no doc comment and `MarkPending` is overloaded as the elapsed and index
  placeholder. Fix: early-return on `closed` in the four mutators with a
  test; one shrink test (`SetFooterFunc(non-empty) → Update → Finish("")`)
  and consider keeping the last live footer in the coordinator's deferred
  `Finish`; display-width counting; rename the constant `TickInterval`;
  document the marks.
- **R3-21 (Note)** — `Log` relies on the frame height equalling `drawn`
  at the time of the call, which holds because every live state change
  renders; a `footerFn` alternating between empty and text would leave one
  stale line (no consumer does this; worth a one-line comment). Plain mode
  prints one line per `Update` call, not per state change, as before the
  extraction; `board.md:45` is slightly generous.

Verified good (review 3): every mutable field is read and written under
`mu`, `out`/`live`/`cfg` immutable after `New`; `Finish` idempotent via
`closed`, no channels to double-close, `Animate` exits on its first tick
after `Finish`; `Log` serialised by `mu` and pinned by
`TestBoardLiveModeRedrawsInPlace`; audio header and row formats
reproduced byte for byte by `columns()`/`rowLine()`, the diarization and
coordinator expectations unchanged except `boardTick → board.Tick`; no
dead pre-extraction identifiers left in `internal/audio`; no writes to
stdout other than through `b.out`; `Row.Index` override and the summarize
`job/selected` index. Coverage `internal/board` 97.9%, `internal/audio`
88.9%.
