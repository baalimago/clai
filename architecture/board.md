# Progress board (`internal/board`)

A parallel-work progress table shared by the audio transcribe flows
(calibrated diarization, ffmpeg split) and `clai chat summarize`. It was
extracted from `internal/audio/board.go` so every parallel flow renders the
same way.

## Shape

```text
▸ summarizing  12 conversations since 2026-09-03 09:00
  phase  summarizing · 12 conversations · 4 workers · model ladder
  slot   conversation                          state           model           tokens  time
  6/12   0192a7b3-4c5d-7e6f-8a9b-0c1d2e3f4a5b  ⠹ summarizing   ·               ·       3s
  5/12   0192a7b3-4c5d-7e6f-8a9b-0c1d2e3f4a5c  ✓ labelled      gpt-5.2         612     4s
  ·      ·                                     · idle          ·               ·       ·
  ·      ·                                     · idle          ·               ·       ·
  labelled 5/12 · failed 0 · in flight 1 · 21s elapsed · eta ~28s
```

- **Title** after `▸` in the theme's primary colour.
- **Phase** line (optional), each change printed once in plain mode.
- **Columns**: the board owns the first column (the row's position,
  `i/n`, header from `Config.Index`) and the last (elapsed, header from
  `Config.Time`); the domain supplies the middle `Config.Columns`. A
  `Mark` column renders `✓`/`✗` (or the spinner while the row is active)
  in front of its cell; its header is two cells wider.
- **Rows** (`board.Row`): `Cells` per column, `Mark`, `Active`, `Started`
  (elapsed runs live from it) and `Elapsed` once done; `Index` replaces
  the position text when a row stands for a slot working through a
  queue. A finished `✗` row is coloured in the tool role colour.
- **Footer**: a fixed string, or `SetFooterFunc` for one rendered at every
  frame from a row snapshot (must not call back into the board).
- **Log** lines stay above the table: live mode moves over the frame,
  prints the line and redraws the table below it; plain mode prints it.

## Live versus plain

`New(out, live, cfg, rows)`: live is true when `out` is a terminal
(`utils.IsTerminalWriter`); the diarization coordinator and `chat summarize`
keep a `forceLive` seam for tests, the ffmpeg split path probes the terminal only.
Live mode redraws the whole table in place on every change and on every
`Animate` tick (`board.TickInterval`, 200 ms) and truncates whole lines to
the terminal width — so a consumer whose fixed columns exceed the width
loses its last column (the elapsed time); size a flexible column from
`utils.SessionDimensions` as `chat summarize` does. `Truncate` counts
display width (wide, fullwidth and emoji runes as two columns, combining
marks as none), skips SGR escapes when counting but keeps them, and closes
a cut coloured line with a reset, so colour never bleeds past a cut line; plain mode prints the title once, then one static line per row
change, phase change, log line and the footer at `Finish`, with no cursor
control. `Finish` is idempotent and draws the final frame; after it,
`Update`, `SetPhase`, `SetFooterFunc`, `Log`, `Tick` and `Animate` are
no-ops in both modes. `Footer()` returns the footer function's current
result while one is set, else the fixed footer, so a deferred
`Finish(b.Footer())` keeps the last live footer on an error exit.
`MarkPending` doubles as the placeholder for an empty elapsed or index cell.

## Consumers

| Flow | Rows | Index | Columns | Footer |
|---|---|---|---|---|
| audio calibrated diarization (`internal/audio/coordinator.go`) | one per core | `core` | span, state (mark), req, verified, unresolved, unknown | ETA, requests, in flight, speakers, unknown speech, uploaded, elapsed |
| audio ffmpeg split (`internal/audio/split.go`) | one per chunk | `core` | same layout | in flight, elapsed |
| `chat summarize` (`internal/chat/handler_summarize.go`) | one per worker slot, `Index` = the job's position in the selected set (`7/12`) | `slot` | conversation (sized from the terminal width: a full UUID at ≥ 94 columns, truncated down to twelve), state (mark), model, tokens | labelled/selected, failed, in flight, elapsed, eta |

The audio adapter (`internal/audio/board.go`) keeps the diarization column
indices and `unknownSoFar`; `chat summarize` keeps its plain and `-r`
output for pipes and `-n` and draws the board only on an interactive
terminal, logging `✓ <id>  <title>` / `✗ <id>  <error>` above it.
