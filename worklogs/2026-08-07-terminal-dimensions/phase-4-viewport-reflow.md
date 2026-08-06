# Phase 4 — Rolling viewport reflow

**Status:** Done  
[Back to README](./README.md)

## Goal

Make the rolling activity viewport rebuild its visible content when width or height changes.

## Specification

The viewport keeps an ordered list of logical blocks. Each block stores its kind (reasoning, assistant-text, tool-activity), the full sanitized content (uncolored), the tool-call metadata for tool headers, and the display policy (header color, role color, 2-space body indentation, tool-output-marker and `ERROR:` handling). The active reasoning/text blocks still coalesce streamed tokens into one open block; finished blocks keep their logical content until the retention policy evicts them. Colors and indentation are re-derived at rewrap time from the display policy; wrapped rows never carry the source of truth.

Each block keeps at most a documented rune budget of content, and the viewport keeps at most a documented number of blocks, so memory stays bounded. The display policy "keep header and trailing rows, drop middle" is re-applied at each rewrap: the policy is width-stable even though the visible result changes with the current width. Height-grow reappearance is bounded by retention, not by the cap.

`Resize(width, terminalHeight)` mutates state only: it stores the new dimensions, computes the effective height as min(configured cap, terminalHeight), re-wraps all retained blocks at the new width with terminal-cell-aware wrapping, re-trims to the effective height, invalidates the previous render bookkeeping (`drawnRows`, `lastRendered`), and marks the viewport dirty. It never writes to the writer, and it is a no-op when the supplied dimensions equal the current ones. `Render` emits the complete atomic frame when the viewport is dirty (move up `drawnRows`, clear down, full rewrite); the diff path is used only for normal appends. A partial frame writer error leaves the viewport dirty; the next `Render` retries the full frame. The constructor binds the initial effective height to min(configured cap, terminal height); `Resize` keeps the same bound on later dimension changes (R5-01).

When the terminal height shrinks below the drawn window, rows scrolled off the top cannot be erased by ANSI escapes; the redraw clears from the current cursor down and rewrites the new effective window. The unerasable scrolled-off rows are a documented residual artifact, not a cursor-drift failure.

`Resize` is invoked only from the serialized session loop, never from a signal callback, and adds no mutex; mutation and rendering stay on the loop.

## Integration contract

| Trigger | Collaborator | Observable result | Required side effect | Prohibited side effect |
|---|---|---|---|---|
| Width narrows | viewport blocks | Rows rewrap within new width | One coherent redraw on next render | No stale old-width rows |
| Height shrinks | viewport history | Only effective-height rows remain | Old region is cleared | No cursor drift |
| Height grows | retained blocks | More retained content can reappear | Rebuild from logical blocks | No loss of tool/reasoning content |
| Same dimensions supplied | viewport | No state invalidation | No redraw | No output |
| `Resize` called | retained logical blocks | New width and effective height applied, viewport dirty | Next `Render` emits the full frame | No write from `Resize` |

## Acceptance criteria

- Reasoning, assistant prose, and tool activity survive resize/reflow.
- Effective height never exceeds terminal height or configured cap.
- Render state is invalidated safely after dimension changes.
- Final-answer pop and tool transitions remain correct after resize.
- Tests cover narrow/wide, short/tall, unchanged, and post-render resize cases.
- New viewport code has focused coverage for nil inputs, clamp paths, empty blocks, partial renders, writer failures, and redraw invalidation, without introducing artificial complexity for coverage numbers.
- `Resize` never writes to the writer (verified with a failing writer).
- `Resize` with unchanged dimensions leaves the viewport clean (no redraw).
- Effective height equals min(configured cap, terminal height).
- Retention bounds memory; height-grow reappearance is bounded by retention, not the cap.
- Resize before the first append (lazy viewport creation) renders at the new dimensions.
- Resize during the final-answer pop sequence keeps the pop correct.

## Error coverage

| Failure | Expected behavior | Test |
|---|---|---|
| Nil viewport/writer | Existing safe behavior is preserved | nil receiver/writer tests |
| Width or height below one | Clamp to safe minimum | dimension clamp test |
| Empty logical block | Render stable empty rows | empty block test |
| Resize after partial render | Complete redraw replaces stale region | render transition test |
| frame writer fails midway | Error and dirty-state behavior match the frame contract | partial-writer failure test |
| Resize with equal dimensions | No dirty state, no redraw | unchanged-dimension test |
| Resize before first append | First render uses new dimensions | lazy-creation test |
| Two consecutive resizes | Last dimensions win; one dirty transition | double-resize test |
| Resize while text block active | Coalescing continues at new width | active-block test |
| Resize during final-answer pop | Pop sequence stays correct | pop-resize test |
| Terminal shrinks below drawn window | Full clear and rewrite; scrolled-off rows are a documented artifact | shrink test |
| Writer fails mid full frame | Viewport stays dirty; next `Render` retries the full frame | partial-writer failure test |

## Implementation notes

### Logical-block storage model (R2-01)

`internal/utils/print.go` now stores logical blocks, not wrapped rows:
`ActivityViewport` keeps an ordered `blocks []*activityBlock` history, two
open-block pointers (`activeReasoning`, `activeText`), and a derived visible
row cache (`rows []activityRow`) rebuilt from the blocks at each mutation.
Each `activityBlock` stores its `kind` (reasoning, text, tool), the full
sanitized content (the source of truth, uncolored), the tool-call metadata
for tool headers, the tool body compaction budget, and its wrapped rows as a
re-wrap cache. Colours and indentation are re-derived by `wrapBlock` at
rewrap time from the display policy; wrapped rows never carry the source of
truth.

### Retention bounds

- `maxActivityBlocks` (16) bounds the retained block count. `evict` drops
  the oldest finished blocks once the history exceeds the budget; the open
  coalescing blocks always survive because they still receive streamed
  tokens.
- `maxActivityBlockRunes` (64 KiB) bounds the sanitized content per block.
  `boundContent` reduces over-budget content to its head and tail around a
  `...[content truncated]...` marker, mirroring the display policy, so a
  later rewrap still has the content the policy would show.

### Rewrap algorithm

`Resize(width, terminalHeight)` clamps width to at least 1, computes the
new effective height as `min(configured cap, max(terminalHeight, 1))`, and
returns without mutation when both equal the current values (no-op).
Otherwise it stores the new dimensions, re-wraps every retained block with
`wrapBlock` at the new width, rebuilds the visible cache with `rewrap`
(trailing effective-height rows only), and sets `dirty`. It never writes to
the writer.

`wrapBlock` re-applies each block's display policy: the reasoning/text
header row plus the 2-space-indented body wrapped by terminal cells
(`terminalRows`), compacted by `compactActivityBlock` to the "keep header
and trailing rows, drop middle" policy with the effective height as budget;
the tool header plus the body compacted by `compactTerminalRows` with the
stored tool body budget, marker and `ERROR:` colouring, and the trailing
blank row. The policy is width-stable: the same policy runs at every width,
even though the visible rows change because wrapping changes. A
terminal-height grow brings retained content back into view, bounded by
retention, not by the cap.

### Frame contract (R1-04)

`Render` emits one complete atomic frame when `dirty` (after `Resize` or a
partial write): move up `drawnRows`, clear down with `\x1b[J`, rewrite the
full window. The diff path is used only for normal streaming appends
(unchanged rows are a no-op, pure appends print below, a changed tail
clears from the first changed row down). Render bookkeeping (`drawnRows`,
`lastRendered`, `dirty`) updates only after a successful write; a partial
or failed write leaves the viewport dirty and the next `Render` retries the
full frame. When the terminal height shrinks below the drawn window, the
full clear starts from the current cursor down and the scrolled-off rows
above the window top are a documented residual artifact, not a cursor-drift
failure.

`RemoveTextBlock` returns the number of rows the removed block occupied in
the visible cache (`min(len(rows), len(activeText.rows))`, the active text
block is always the cache tail) and rebuilds the cache without it.

### Error-coverage mapping

| Failure | Behavior | Test |
|---|---|---|
| Nil viewport/writer | All methods no-op safely | `nil viewport is safe` (print_test.go) |
| Width or height below one | Clamp to 1 | `clamps width and height below one`, `effective height is the cap capped by terminal height` |
| Empty logical block | Stable empty rows, no re-render | `empty text block renders stable empty rows` |
| Resize after partial render | Full frame replaces the stale region | `width narrows rewraps retained blocks`, `two consecutive resizes converge on the last dimensions` |
| Frame writer fails midway | Error; viewport stays dirty; next `Render` retries the full frame | `partial frame write leaves the viewport dirty and retries`, `write failure on the diff path marks the viewport dirty`, `short write without error leaves the viewport dirty` |
| Resize with equal dimensions | No dirty state, no redraw | `unchanged dimensions leave the viewport clean` |
| Resize before first append | First render uses the new dimensions | `resize before the first append renders at the new dimensions`, `resize with no content renders nothing and stays clean` |
| Two consecutive resizes | Last dimensions win, one dirty transition | `two consecutive resizes converge on the last dimensions` |
| Resize while text block active | Coalescing continues at the new width | `resize while a text block is active keeps coalescing` |
| Resize during final-answer pop | Pop stays correct, reasoning survives | `resize during the final-answer pop keeps the pop correct` |
| Terminal shrinks below drawn window | Full clear and rewrite; scrolled-off rows are a documented artifact | `terminal shrink below the drawn window clears and rewrites` |
| Height grows | Retained content reappears, bounded by retention | `height grow brings retained content back into view` |
| Retention over budget | Oldest finished blocks evicted; content bounded per block | `retention evicts the oldest finished blocks`, `retention bounds content per block` |
| Resize never writes | No writer is involved; `Render` is the only writer | `resize never writes to the writer` |

### Commands and results

Baseline (start of session):

```bash
cd /home/imago/Projects/public/clai && go test ./internal/utils/ ./internal/text/ -count=1 -timeout=120s
```

Passed: exit 0. This confirmed the pre-change behaviour (phase-3 state).

After the change:

```bash
go test ./... -race -cover -count=3 -timeout=30s
```

All packages ok, exit 0 (internal/utils 81.4% coverage, up from 76.5% at
phase 3; the new viewport functions measure 100% for `NewActivityViewport`,
`AppendReasoning`, `AppendText`, `AppendTool`, `FinishReasoning`,
`FinishText`, `RemoveTextBlock`, `TextBlockActive`, `Content`, `Rows`,
`Resize`, `DetachRenderedRegion`, `evict`, `rewrap`, `boundContent`, and
`commonRowPrefix`).

```bash
go run mvdan.cc/gofumpt@latest -w -l .
go run honnef.co/go/tools/cmd/staticcheck@latest ./...
go vet ./...
go fix ./...
go run github.com/mibk/dupl@latest -t 80 .
```

All clean. The dupl check flagged the pre-existing `AppendReasoning` /
`AppendText` clone, which phase 4 merged into the shared
`appendCoalescing` helper; no phase-4 clone remains.

## Review findings (review 2, 2026-08-07)

**R2-01 — Major — resolved in spec (amendment, review 2).** [x] Specify the viewport's logical-block storage model. The Specification above now pins the storage, the retention bound, and the rewrap algorithm. Today `ActivityViewport` keeps only wrapped rows (`internal/utils/print.go` `rows []activityRow`, lines 358-361); finished blocks lose their logical content (`FinishReasoning`/`FinishText` reset the builders, print.go:414-430); oversized blocks drop their middle at append time (print.go:453-466, "keep header and trailing rows"); and tool output is compacted lossily at the append-time width (`compactTerminalRows`, print.go:663-676). The acceptance criterion "Reasoning, assistant prose, and tool activity survive resize/reflow" cannot be met by re-wrapping stored rows. Define the new storage (per-block type, full content, tool-call metadata, compaction policy), a bounded retention policy for content that overflowed the cap, the re-wrap algorithm `Resize` runs, and the methods that change (`Rows`, `Content`, `RemoveTextBlock`, `appendBlock`, `trim`, `Render`, `DetachRenderedRegion`). Concrete failure scenario: a 60-row reasoning block at width 120 is trimmed to 30 rows on append; `FinishReasoning` then discards the logical text; a resize to width 40 can only rewrap the 30 surviving wrapped rows and the dropped middle is unrecoverable.

Verified good: phase 4 correctly requires terminal-cell-aware rewrapping, effective-height bounds (cap and terminal height), unchanged-dimension no-op behavior, and post-render resize coverage; the phase-5 dependency on this phase is sound.

## Review findings (review 1, 2026-08-07)

**R1-04 — Major — resolved in spec (amendment, review 2).** [x] Specify the frame/writer boundary for a complete
redraw: define the operations for clearing the old region, positioning the
cursor, writing rebuilt rows, and restoring the cursor. Also define whether a
partial writer error leaves the viewport dirty and whether the next render
retries the full frame. Without this, “atomic” can mean only that the caller
holds a mutex while users still observe half a redraw or cursor drift. The
Specification above now defines the dirty-state model, the full-frame clear
sequence, and the retry-on-partial-write behavior.

Verified good: logical blocks, terminal-cell wrapping, effective-height bounds,
unchanged-dimension no-op behavior, and post-render resize coverage are the
right invariants for this phase.

## Review findings (review 5, 2026-08-07)

Cross-reference R5-01 (phase 5): the acceptance criterion "Effective height
never exceeds terminal height or configured cap" is unmet on the initial
path. The constructor keeps the cap (D9: "The constructor keeps the cap;
Resize takes raw terminal dimensions"), and `ensureActivityViewport` creates
the viewport with the cap height and never consults `q.dims.Height` until a
SIGWINCH arrives, so a terminal shorter than the cap renders a window taller
than the screen for the whole query. The fix (binding the initial height to
min(cap, terminal height) at creation, or amending the AC and colours.md to
state the initial-cap exception) is tracked in phase 5 and resolved in
worker session 7: `NewActivityViewport(width, cap, terminalHeight)` binds the
initial effective height to min(cap, terminal height) and
`ensureActivityViewport` passes `q.dims.Height`. No phase-4 test pins the
cap-at-creation behavior: every viewport-height test calls `Resize`
before asserting the effective height, so the fix is testable without
breaking current assertions.

Verified good in this phase: logical-block storage, per-block content bounds
(`maxActivityBlockRunes` head/tail reduction), `maxActivityBlocks` eviction
(open coalescing blocks always survive), `Resize` mutating state only with a
no-op on equal dimensions, single-write atomic frames on the dirty path,
the diff path for normal appends, partial/short-write dirty-retry, and the
height-grow reappearance bounded by retention — all confirmed against the
code and the tests in review 5.