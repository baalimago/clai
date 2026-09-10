# Phase 10 — Review 3 fixes (addendum)

Status: Complete

Addendum for review round 3 (holistic, 2026-09-10). Consolidates the three
Medium findings and the Low findings the maintainer wants fixed in place.
Read the README and this file; the originals are referenced by finding id
(phase 2, 4, 5, 7, 8, 9 `Review findings` sections).

## Goal

The scope-crept areas meet the bar of the core: the shared board renders
correctly with colour on at the fallback width, `chat list` never drifts
on an eighty-column terminal, and no writer reachable from a one-off
querier touches a process stream outside the seams.

## Specification

### Board truncation after colour (R3-01, R3-13)

- `render` truncates the plain line and colours afterwards (header,
  columns, warn rows, phase label), or `Truncate` skips SGR runs when
  counting and re-appends the reset when it cuts a coloured line. Either
  way, a coloured line at exactly the terminal width keeps its last
  column and ends with a reset.
- `Update`, `SetPhase`, `SetFooterFunc`, `Log` return at once after
  `Finish`.
- Rename the constant `Tick` to `TickInterval`; doc comments on the mark
  constants.
- Tests: colour-on live render at the fallback width asserting the last
  column and a trailing reset (board and summarize); frame-shrink
  (`SetFooterFunc(non-empty) → Update → Finish("")`); mutators after
  `Finish` are no-ops in live and plain mode.

### `chat list` prompt width (R3-02)

- The prompt line fits the terminal width with both toggles present:
  short labels (`[d]ir: off|on`, `[f]oreign: shown|hidden`) or a measured
  fallback to the short form. Emphasis and state words stay.
- Test: a render at the fallback width with both toggles counts one
  prompt line under the width; `TestToggleLabel` follows the labels.

### Cost manager warnings through the seam (R3-03)

- `cost.Manager` gains a warn function (setter, default `ancli.Warnf`
  for the store failure's current `Errf` semantics is a maintainer call:
  keep it an error on stderr for the main run, route it for a querier
  with `CostWarnf` set). `NewQuerier` passes `costWarnf`; the store
  failure and `Enrich`'s user-role warning go through it.
- Test: a manager with the seam set and a failing store writes nothing
  to the process streams; the summarizer's `coldPriceIsSilent` gains an
  unwritable-dir row.

### Fixture hygiene (R3-04, R3-05)

- `seedConfigDir` and `setupSummaryE2E` blank `DEBUG`, `DEBUG_*`,
  `CLAI_SUMMARIZER`, `OPENAI_API_KEY`, `ANTHROPIC_API_KEY`; the summary
  fixture's text config uses `Model: "test"` so the ladder floor is the
  mock. Promote the rule to the README validation policy (done by the
  review).

### One-line items

- R3-06: return the held label when `holder.get()` succeeds after a
  `TextQuery` error (caller-cancel check first); test through the seam.
- R3-07: state the real call bound (up to eight tool round trips, at
  least two model calls per label) in the README parameter row and
  `summaries.md`, or stop the run on `accepted`.
- R3-08: an empty `Summary` is a failure in `joinSummary`.
- R3-09: temp file created with `perm` under the umask; drop `Chmod`.
- R3-10: `writeChatIndex` through `utils.WriteFileAtomic`.
- R3-11: `-workers` below one is a usage error.
- R3-12: the confirmation prompt goes to stderr under `-r`.
- R3-14: the stale-text list in phase 7.
- R3-15, R3-16, R3-17: the test rows and the trace sink named in phases
  2 and 4.
- R3-18: one sentence in `summaries.md` that the in-flight label
  describes the prompt, the batch label the transcript.
- R3-20: drop `.claude/settings.local.json` from the commit.

## Integration contract

| Boundary | Oracle | Test |
| --- | --- | --- |
| Live board, colour on, width 80, summarize layout | header keeps `tokens  time` and its reset; the last SGR on every line is a reset; no line wider than 80 columns | `TestHandleSummarize_liveBoardColour`, `TestBoardLiveColourKeepsLastColumnAndReset` |
| `chat list` at width 80 with dirscope binding and a foreign row, no page counter | prompt line under 80 visible columns with both state words; width 140 long labels | `TestListChats_PromptFitsWidth`, `TestListPromptTier` |
| Cost manager with the warn seam set and a failing price store | nothing on stdout or stderr; the message reaches the seam; unset keeps `Errf` | `TestManager_SetWarnf`; `TestNewQuerier_costWarnfRoutesManagerWarnings` (querier built with `CostWarnf`, `Enrich` warning routed) |

## Acceptance criteria

- [x] R3-01, R3-02, R3-03 checked in their phase files with the tests above.
- [x] Every Low is checked in its phase file with a test or the doc line
      it names (R3-19 resolved by D34).
- [x] Unedited gates green; `internal/board` 97.9% → 98.3%; `internal/chat`
      76.7% → 77.0%.

## Error coverage

| Error | Behavior |
| --- | --- |
| Board line coloured and wider than the terminal | truncated on visible columns, reset kept |
| Prompt line wider than the terminal | short labels; no drift |
| Price store fails under a summarizer querier | routed to the trace; summary still applied |

## Implementation notes

### 2026-09-10 13:30–14:00 UTC — clai, session a515a374 (four parallel workers on disjoint packages plus the lead)

Deviations from the specification and surprises:

- **R3-01** fixed inside `Truncate` rather than by reordering `render`:
  `Log` lines are caller-owned and may carry colour, and the phase line
  is coloured mid-line, so one escape-aware cutter covers every path.
  `Truncate` counts display width through `golang.org/x/text/width`
  (already a dependency), copies SGR runs verbatim, and closes a cut
  coloured line with a reset. `internal/board` 98.3%.
- **R3-02** is a measured three-tier label (`listPromptTier`, thresholds
  one hundred and nineteen and one hundred and one columns for both
  toggles; unknown width keeps the long tier). Residual recorded in
  `architecture/chat.md`: the table library never measures or clears a
  wrapped prompt, so below about ninety-one columns a *paginated* list
  still wraps the terse prompt; the pre-feature prompt with a page
  counter already wrapped at eighty (eighty-two columns), so this is a
  library limitation the fix narrows but cannot close.
- **R3-03**: `cost.Manager.SetWarnf` (nil keeps `Errf`/`Warnf`
  byte-for-byte); `NewQuerier` sets it only when `CostWarnf` is set and
  folds the dead catalog-fetcher warning into the seam. The
  summarizer-level unwritable-dir row the specification asked for is not
  reachable without a catalog fetcher, and the only production fetcher
  hits the network; the store path is pinned at the manager level, the
  querier-level test uses the `Enrich` path. Integration row amended.
- **R3-09**: temp file created `O_CREATE|O_EXCL` with `perm` under the
  umask; the umask test lives in `file_atomic_unix_test.go`
  (`//go:build unix`) because `syscall.Umask` does not exist on Windows.
- **R3-15**: the `newQuerier` seam is typed `models.ChatQuerier`;
  `asChatQuerier` adapts `text.CreateQuerier` and is tested alone.
- **R3-16**: `isSideEffectFree` runs with the manager goroutine on; no
  flake in three race rounds because the trace returns before touching
  the stream when `DEBUG_SUMMARY` is blank.
- **R3-17**: both trace writers go to stderr; the constructor-error
  trace test now asserts stderr only.
- **R3-06** returns the held label even when the closing turn errors,
  tracing the dropped error; a rejected submission still surfaces its
  validation error.
- **R3-05** adds `Test_e2e_chat_summarize_floor_is_config_model` (batch
  row without `-sm` labels through the mock; red against the old fixture
  with `openai: missing OPENAI_API_KEY`). `setupMainTestConfigDir` is
  untouched; hygiene lives in the summary fixture and a shared helper.
- **R3-13**: mutators after `Finish` are no-ops; `Footer()` reflects the
  footer function, so the audio error-exit frame keeps its footer
  (`TestCoordinatorLiveErrorExitKeepsFooter`); `board.TickInterval`.
  Neither chat file referenced the old constant.
- **R3-12**: `ChatHandler.errOut` (default `os.Stderr`); the raw
  declined-confirmation test parses the whole stdout as JSON lines.
- **R3-20**: `.claude/settings.local.json` added to `.gitignore`.
- **R3-19** confirmed by the maintainer as a deliberate exception (D34).
- **Gate finding.** The first unedited gate failed two pre-existing root
  rows, `Test_e2e_chat_list_macro_dir_filter_toggle` and `_empty`: the
  e2e process sees the eighty-column fallback, where even a single
  toggle takes the terse tier (`[d]:off`), and the rows counted the
  literal `[d]irscoped`. Both now assert the toggle by its key and its
  state words (off, on, off across the three renders), whichever tier
  the width selects. Every other package was `ok` in that run.

Verification (workers, per package, `-race -count=3 -timeout=30s -cover`
unless stated): `internal/board` 98.3%, `internal/audio` 88.9% at
count=1 (count=3 timed out for the worker at host load above ten; see
the lead's gate below), `internal/chat` 77.0%, `internal` 80.6%,
`internal/cost` 63.9%, `internal/utils` 81.6%, `internal/text` 84.5%,
`internal/summary` 97.0%; hostile runs `DEBUG=1 CLAI_SUMMARIZER=off go
test ./internal/summary/` and `DEBUG=1 go test . -run
'Test_e2e_(summarizer|chat_summarize|query_labels)'` green (fifteen
failures before). Every new test was observed red before its fix.
Lead's unedited gate from a quiet start (load below five): `gofumpt -l .`
empty, `staticcheck`, `go vet`, `go fix` clean, `dupl -t 80 .` at the
thirty-one baseline groups; `go test ./... -race -cover -count=3
-timeout=30s` — forty-five packages `ok` on the first run with the two
root rows above failing; after the e2e adaptation the root package reran
alone at count=3: `ok 22.653s`, coverage 56.7%, gofumpt and vet clean.
Coverage after phase 10: `internal/board` 98.3%, `internal/summary`
97.0%, `internal/text` 84.5%, `internal/chat` 77.0%, `internal/audio`
88.9%, `internal/cost` 63.9%, `internal/utils` 81.6%.

## Review findings

None.
