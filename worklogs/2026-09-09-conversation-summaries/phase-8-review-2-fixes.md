# Phase 8 — Review 2 fixes (addendum)

**Status:** Complete

[← README](./README.md)

## Goal

Close the review-2 findings in one pass: make every writer of a
per-model config file atomic so concurrent in-process queriers never
observe a torn file (R2-01), keep `chat summarize -r` one JSON object
per line on every path (R2-02), keep the summarizer's success path off
stderr without seeded price files (R2-03), and announce or document the
batch path's silent config upgrade (R2-04). Notes R2-05 to R2-08 need no
code beyond the one-line items below.

Reads: this file, the README, and the review-2 sections of
[phase 2](./phase-2-summary-domain.md#review-findings) and
[phase 5](./phase-5-batch-summarize.md#review-findings).

## Specification

### Atomic per-model config writes (R2-01)

- Add `utils.WriteFileAtomic(path string, data []byte, perm os.FileMode) error`
  in `internal/utils`: write to a temp file in the same directory, sync,
  rename over `path`. Rename is atomic on the platforms clai supports, so
  a concurrent reader sees the old file or the new file, never a prefix.
- `setupConfigFile` (`internal/text/querier_setup.go`) writes the default
  model config through it. The read-back after the write stays.
- `Manager.storePriceScheme` (`internal/cost/manager.go`) writes the
  updated config through it.
- Both are generic, vendor-agnostic code; no vendor package changes.
- `agentSummarizer` gains no mutex: construction stays concurrent, the
  file is the only shared state and the atomic write removes the tear.

| Invariant                                                                  | Mechanism                             | Test                                              |
| -------------------------------------------------------------------------- | ------------------------------------- | ------------------------------------------------- |
| A reader of a per-model config file never sees a partial write             | `utils.WriteFileAtomic` in both writers | `TestWriteFileAtomic_readerSeesWholeFile` (`internal/utils`) |
| N concurrent `NewQuerier` calls on a missing model config all succeed      | atomic default write                  | `TestSetupConfigFile_concurrentCold` (`internal/text`) |
| N concurrent price stores on one file leave a parseable file with `price` | atomic store                          | `TestStorePriceScheme_concurrent` (`internal/cost`) |
| N concurrent `Summarize` calls on a cold model config all return a label   | the two rows above                    | `TestAgentSummarizer_concurrentColdModelConfig` (`internal/summary`) |

### Raw output on every path (R2-02)

`handleSummarize` under `-r` prints `{"selected":0,"labelled":0,"failed":0}`
for the zero-row case and `{"aborted":true}` when the confirmation is
declined, instead of the plain-text lines. Non-raw output is unchanged.

### Summarizer querier warnings (R2-03)

The summarizer's querier must not warn on stdout or stderr on its success
path in any environment (`ancli.Warnf` prints on stdout; see the phase-2
R2-03 correction). Add `Configurations.CostWarnf func(string, ...any)`
(`json:"-"`, nil keeps `ancli.Warnf`) that `NewQuerier` hands to
`newCostEnricher`; `Summarize` sets it to `traceSummaryf`-equivalent
tracing under `DEBUG_SUMMARY` (the `debugflags` switch, reachable from
`internal/summary` through a small exported helper in `internal/text` or
by passing `func(string, ...any)` that the summary package builds on
`debugflags`). `TestAgentSummarizer_isSideEffectFree` gains a cold-price
row: the seeded price file removed, stderr still empty.

### Batch-path config upgrade (R2-04)

`summarizeSetup` announces the collected `textConfig.json` announcements
the way the announcing loader does (one `ancli` notice per entry) by
loading through `LoadConfigFromFileCollect` before `NewSummarizer`; or,
if the maintainer prefers silence, `architecture/summaries.md` states
that `chat summarize` upgrades the config silently. Pick one; the
acceptance row names the announcing option.

### Abandoned summarizer must not touch process globals (R2-09, gate finding)

Found by the full gate during this phase: an abandoned in-flight
summarizer keeps constructing its querier after `run()` returned, and
`newMcpLogSink` read `os.Stderr` while the e2e capture helper restored
the stream — `race detected during execution of test`. Fix:
`Configurations.ErrOut io.Writer` (`json:"-"`, nil keeps `os.Stderr`)
reaches the sink through `newMcpLogSinkTo`; `Summarize` sets it to
`io.Discard`. The cost manager's asynchronous error log now also goes
through `CostWarnf`, so the summarizer's querier owns no writer to a
global stream at all.

| Invariant                                                                 | Mechanism                                  | Test                                      |
| ------------------------------------------------------------------------- | ------------------------------------------ | ----------------------------------------- |
| The summarizer's querier references no process stream                     | `Out`, `ErrOut` = `io.Discard`; `CostWarnf` | `TestAgentSummarizer_querierConfig`       |
| `ErrOut` nil keeps `os.Stderr`; a writer replaces it and the dimension probes fall back | `newMcpLogSinkTo`             | `TestNewQuerier_errOut`                   |
| The manager error log uses `CostWarnf`, never `ancli`                     | `costWarnf` closure in `NewQuerier`        | `TestNewQuerier_costManagerErrorUsesCostWarnf` |
| No race on the root package                                               | the rows above                             | `go test . -race -count=3` looped in the notes |

### One-line items

- `architecture/summaries.md`: one sentence that `chat continue` never
  launches the in-flight summarizer; the batch command labels such
  conversations (R2-06).
- `Summary.ApplyTo`: `time.Now().UTC()` (R2-08).

## Integration contract

| Trigger                                                                                                       | Collaborators / fakes                    | Observable result                                                                    | Required side effects                       | Prohibited side effects                  |
| ------------------------------------------------------------------------------------------------------------- | ---------------------------------------- | ------------------------------------------------------------------------------------ | ------------------------------------------- | ---------------------------------------- |
| Seed 6 summarizable conversations inside the window; remove `mock_test_test.json`; `clai c summarize -y -sm test -workers 6 7d` through `run`, repeated 10 times | Mock vendor; real summarizer; e2e temp config dir | Every run: all 6 ids listed with `Mock title`; exit zero; the model config file exists and parses | Model config created once | No `ERROR` line; no partial-file error |
| `clai -r c summarize -y 7d` when nothing is selected                                                   | As above                                 | stdout is exactly one line, `{"selected":0,"labelled":0,"failed":0}`; exit zero      | None                                        | No plain-text line                       |
| `clai -r -cm test -t ls q "hello tool_submit_summary"` with the mock's config file lacking a `price` field and no `OPENROUTER_API_KEY` | Mock vendor; real summarizer | Answer printed; conversation labelled; stdout holds exactly one `failed to enrich chat with cost estimate` line (the main run's own); stderr empty | As phase 4 row one | No summarizer-originated warning on either stream |

## Acceptance criteria

| Outcome                                                              | Test                                                                                   |
| -------------------------------------------------------------------- | -------------------------------------------------------------------------------------- |
| Atomic write helper                                                  | `TestWriteFileAtomic_readerSeesWholeFile`                                              |
| Concurrent cold construction succeeds                                | `TestSetupConfigFile_concurrentCold`, `TestStorePriceScheme_concurrent`, `TestAgentSummarizer_concurrentColdModelConfig` |
| Batch e2e on a cold model config is clean across repeated runs       | `Test_e2e_chat_summarize_cold_model_config` (`main_summary_e2e_test.go`)                |
| Raw zero-row and aborted lines are JSON                              | `TestHandleSummarize_rawOutput`                                                        |
| Summarizer success path silent without seeded price files            | `TestAgentSummarizer_coldPriceIsSilent`, `Test_e2e_query_labels_in_flight_cold_price` |
| Batch path announces the config upgrade                              | `TestNewAgentSummarizer_announcesUpgrade`, `TestNewAgentSummarizer_loadsConfig` (row reworded) |
| Docs and `ApplyTo` UTC                                               | `TestSummaryApplyTo` (UTC row); `architecture/summaries.md` diff reviewed               |
| Real summarizer never built through the CLI in the test binary without opt-in; `off` refuses everywhere (D32) | `Test_e2e_summarizer_guard`, `TestSummarizerGuard` |
| Positional window; flags before it; trailing flag names the order (D33) | `TestChatCommand_summarizeSub`, `TestSummarizeFlags_register`, `Test_e2e_chat_summarize_window_required` |
| Abandoned summarizer touches no process stream (R2-09)               | `TestAgentSummarizer_querierConfig`, `TestNewQuerier_errOut`, `TestNewQuerier_costManagerErrorUsesCostWarnf` |

Files: `internal/utils/file.go` (new helper) and its test,
`internal/text/querier_setup.go`, `internal/text/querier_setup_test.go`,
`internal/text/conf.go`, `internal/text/cost_enricher.go`, `internal/text/mcp_log_sink.go`,
`internal/cost/manager.go`, `internal/cost/manager_test.go`,
`internal/summary/summarizer.go`, `internal/summary/summarizer_test.go`,
`internal/chat/handler_summarize.go`, `internal/chat/handler_summarize_test.go`,
`internal/chat/cmd.go`, `internal/chat/cmd_test.go`,
`internal/models/summary.go`, `internal/models/summary_test.go`,
`main_summary_e2e_test.go`, `architecture/summaries.md`.

## Error coverage

| Failure                                                   | Expected outcome                                                                 | Test                                      |
| --------------------------------------------------------- | -------------------------------------------------------------------------------- | ----------------------------------------- |
| Temp file cannot be created or renamed                    | The writer returns the wrapped error; the previous file, if any, is intact       | `TestWriteFileAtomic_errorLeavesOldFile`  |
| Price store fails after a successful fetch                | Today's behaviour: logged, price kept in memory, run continues                    | `TestStorePriceScheme_concurrent` (error row) |
| `CostWarnf` nil                                           | `ancli.Warnf` as today                                                            | `TestNewCostEnricher_defaultWarnf`        |

## Implementation notes

Session `session_01KXDFdab6MmuuKetC1EBowJ` (reviewer turned phase-8
worker on the maintainer's "fix it in place"), 2026-09-10 09:30–10:10 UTC. Tests first, then the fixes; deltas only.

Deviations and decisions:

- **R2-03 re-rated High.** `ancli.Warnf` is `PrintWarn`, which writes to
  `os.Stdout`, so the cold-price warning broke "the summarizer never
  writes to the host's stdout", not the stderr clause. The oracle rows
  written "stderr" were corrected to stdout before they went green;
  `testboil.CaptureStderr` never sees ancli warnings, which is why no
  earlier e2e caught it. Phase 2's finding text, the README feedback index
  and the notebook note carry the correction.
- **R2-04 supersedes V2-03's "loads silently" row.** The constructor now
  prints the collected upgrade announcement through `ancli.PrintOK`; the
  query path stays silent because `SetupQuerier` upgraded the file first
  (`TestNewAgentSummarizer_loadsConfig` row reworded, new
  `TestNewAgentSummarizer_announcesUpgrade`). The spec named
  `TestChatCommand_summarizeSub` as the home; the `chat` tree cannot see
  the constructor's output, so the test lives in `internal/summary`.
- **R2-09 (High), found by the gate.** The first full run after the
  fixes failed with `race detected during execution of test` in a skills
  e2e: an abandoned in-flight summarizer was still inside `NewQuerier`
  (`newMcpLogSink` reading `os.Stderr`) after `run()` returned, while the
  capture helper restored the stream. The phase-4 design ("cancelled and
  forgotten; the process exit reaps it") is kept; the querier simply
  stops referencing process streams: `Configurations.ErrOut` reaches the
  sink through `newMcpLogSinkTo`, and the cost manager's asynchronous
  error log now also uses `CostWarnf` (a second run failed on that
  goroutine interleaving `cost manager error` into a skills e2e's stdout).
  The manager's error send is non-blocking, so
  `TestNewQuerier_costManagerErrorUsesCostWarnf` asserts the stream
  contract (nothing on stdout, any arrival through the seam) rather than
  an arrival.
- `WriteFileAtomic` syncs the temp file before the rename; the cold-path
  cost is one fsync per created or price-updated model config, never on
  the warm path.
- `chat summarize -r` on zero rows prints the all-zero totals object;
  a declined confirmation prints `{"aborted":true}`.

Verification (all from the repository root, gates unedited):

- Red first: `go vet` on the six packages failed on the undefined
  `WriteFileAtomic` and the unknown `CostWarnf` field before the
  implementation.
- `go test ./internal/utils/ -run TestWriteFileAtomic -race -count=3` → ok.
- `go test ./internal/text/ -run 'TestSetupConfigFile_concurrentCold|TestNewCostEnricher_defaultWarnf|TestNewQuerier_errOut|TestNewQuerier_costManagerErrorUsesCostWarnf' -race -count=3` → ok (the last also at `-count=10`).
- `go test ./internal/cost/ -run TestStorePriceScheme_concurrent -race -count=3` → ok.
- `go test ./internal/summary/ -race -count=3 -cover` → ok, 94.7%.
- `go test ./internal/chat/ ./internal/models/ -run 'TestHandleSummarize_rawOutput|TestHandleSummarize_confirmation|TestSummaryApplyTo' -race -count=3` → ok.
- `go test . -run 'Test_e2e_chat_summarize_cold_model_config|Test_e2e_query_labels_in_flight_cold_price|Test_e2e_query_labels_in_flight$|Test_e2e_chat_summarize' -race -count=1` → ok; the two new rows take 0.12 s together.
- Race regression: `go test . -race -count=3 -timeout=30s` looped six times after the R2-09 fix → six `ok`, 21.6–23.3 s each (before the fix: one race in four loops, then one interleave in six).
- gofumpt (no file listed), `go vet ./...`, staticcheck, `go fix ./...` clean; `dupl -t 80 .` at the 31 baseline groups.
- Full `go test ./... -race -cover -count=3 -timeout=30s`: 44 packages ok every run; the root package hit its 30 s alarm twice under the parallel gate at load 13–14 (the recorded host-load flake; root alone stays at ~22 s, unchanged from before this phase). The retry from a quiet start (load 2.9 at start, 5.4 at end) exited 0: 45 packages `ok`, root 22.5 s, no timeout, no race; gofumpt, vet, staticcheck, fix and dupl were run clean on the same Go tree immediately before it.
- Coverage: `internal/summary` 94.7%, `internal/text` 84.4%, `internal/chat` 75.3%, `internal/cost` 57.4% (was 47.0%), `internal/utils` 81.6%, `internal/models` 100%.

### 2026-09-10 10:40–11:00 UTC — maintainer follow-ups (same session)

- **`make qa` root-package timeout (maintainer: "not present on main").**
  Measured sequentially on a quiet host: `go test . -race -count=3` takes
  20.7 s on a `git archive main` export and 22.2 s on this branch, so the
  branch spent ~1.5 s of the root package's margin under the 30 s alarm,
  and `make qa` (every package in parallel) tipped it over on this host
  where `main` still fits. The summarizer explains it: an A/B with the
  shared e2e fixture's `summarize-conversations` off versus on, skipping
  the summary rows, measured 21.1 s versus 22.3 s at count=3 — every one
  of the ~150 pre-existing query e2e rows was paying for a summarizer
  construction, a second `textConfig.json` load and a mock model call.
  Fix: `setupMainTestConfigDir` writes the shared fixture with
  `summarize-conversations: false` (the pre-existing rows now cost what
  they cost on main); `setupSummaryE2E` rewrites it to the default so the
  summary rows opt in; and the query command no longer constructs a
  summarizer when the feature is off (`internal/text/cmd.go`, pinned by
  `TestQueryCommand_summarizerNotBuiltWhenOff`, which also proves
  `-summarize` over a false config still builds one). The root package's
  own margin on `main` (~21 of 30 s at count=3) is pre-existing and
  outside this worklog. After the fix, `make qa` (lint, then the unedited parallel
  `go test ./... -race -count=3 -cover -timeout=30s`) exited 0 with 45
  packages `ok`; the host carried external load 6–17 during that run (the
  sandbox itself was idle), so the root package reported 31.0 s wall and
  `internal/audio` 26.6 s — inside the alarm but with no margin under
  that load. Sequential root timings taken under external load 8 in the
  same run were 23.2 s (branch) versus 21.6 s (`main`), within the ±1 s
  noise of the contended host; the quiet A/B above is the reliable
  measurement. Anyone re-running the gate should do so at load below
  five (see the README's phase-1 and phase-3 journal entries).
- **Test-process guard (maintainer: tests must never trigger a
  summarization that costs money).** The fixture default above is a
  convention, not a guarantee: a test that skips the fixture, or a
  developer `textConfig.json` with `summary-model` set, could still reach
  a real vendor through the summarizer while the main model is the mock.
  `NewAgentSummarizer` now refuses at its first line unless
  `summary.allowed()` passes: `CLAI_SUMMARIZER=off` refuses in any
  process, and under `go test` (`testing.Testing()`) it refuses unless
  `CLAI_SUMMARIZER=on`. The query path traces the refusal and stays
  unlabelled; `chat summarize` fails naming the switch and touches no
  file. Opt-in lives only in the summary fixtures (`setupSummaryE2E`,
  `seedConfigDir`, `TestNewAgentSummarizer_loadsConfig`); fake
  summarizers are unaffected. Pinned by `TestSummarizerGuard` and
  `Test_e2e_summarizer_guard` (query and batch rows for unset and `off`).
  Recorded as D32. Final `make qa` on this tree from load 3.6: exit 0,
  45 packages `ok`, root 23.1 s, `internal/summary` 94.3%; `go vet` and
  `dupl` (31 baseline groups) clean.
- **Guard redesigned (maintainer: the `testing.Testing()` sniff is a
  hack).** Production code no longer knows about tests. `main.go` wires
  the constructor through `var newSummarizer = summary.NewAgentSummarizer`;
  the root package's `TestMain` sets it to `refusingSummarizer` and
  `setupSummaryE2E` restores the real one under `t.Cleanup`. In prod the
  variable is never touched, so default-on behaviour is exactly the
  config's. `internal/summary/guard.go` keeps only `CLAI_SUMMARIZER=off`
  (operator kill switch); `seedConfigDir` needs no env. D32 reworded.
- **Positional window (D33).** `clai chat summarize 7d`; `-since` removed
  from `SummarizeFlags`, `summarizeSetup` reads `c.Args()[2]` and hands
  the handler the bare verb. The tree's parser follows stdlib flag order
  (probed: `c summarize -y -sm test 7d` works, `c summarize 7d -y` puts
  `-y` in the positionals), so the usage error names the order when it
  sees a flag after the window. Rows: `TestChatCommand_summarizeSub`
  (missing window, extra positional, flag after the window, unparsable),
  `TestSummarizeFlags_register` (no `-since`),
  `Test_e2e_chat_summarize_window_required`, every batch e2e row flags-first.
  Final `make qa` on this tree from load 1.8: exit 0, 45 packages `ok`,
  root 22.4 s, `internal/summary` 94.9%, `internal/chat` 75.4%; `go vet`
  and `dupl` (31 baseline groups) clean.
- **`chat list` column header renamed `Prompt` → `About`** (maintainer
  request): both header rows in `internal/chat/handler_list_chat.go`,
  `TestListChats_NarrowWidthShowsCostAndAbout` (renamed; rejects
  `Prompt`), and the two rendered tables in
  `architecture/continue-from-claudex.md`. `internal/chat` and the list
  e2e rows pass at `-race -count=3`.

## Review findings

### Review 3 — 2026-09-10 (`worklog-review`, holistic)

- [x] **R3-02 (Medium)** — The state-bearing toggle labels (maintainer
  follow-up recorded in the README journal) push the `chat list` prompt
  line past the terminal width, and the table clears one line too few.
  With a dirscope binding and a foreign source present the prompt reads
  `(select [d]irscoped convs: off, [f]oreign convs: shown, [p]rev, [n]ext,
  [q]uit, [/] filter, page 0/1): ` — one hundred and three visible
  columns, seventy-four before the labels. On an eighty-column terminal it
  wraps to two physical lines, but `table.selectNumbers` clears
  `amPrinted+1` lines (`go_away_boilerplate/pkg/table/table.go:352`), so
  every keypress leaves one stale line and the list drifts down the
  screen. The width helper measures row cells only; the prompt line is
  never measured (`internal/chat/handler_list_chat.go:658`). Fix: shorter
  labels (`[d]ir: off`, `[f]oreign: shown`) or measure the prompt against
  `utils.SessionDimensions` and drop to the short form; pin with an
  eighty-column render assertion.
- [x] **R3-03 (Medium)** — One writer reachable from the summarizer's
  querier still bypasses the phase-8 seams: `resolveModelPrice`
  (`internal/cost/manager.go:159`) logs a failed price store with
  `ancli.Errf`, which prints on stderr, from the manager goroutine.
  **Scenario:** `OPENROUTER_API_KEY` set, cold price cache, per-model
  config present but the config dir not writable (the fixture of
  `TestStorePriceScheme_concurrent`'s failed-store row): the summary
  succeeds and `error: failed to store price scheme …` reaches the host's
  stderr, possibly after the main run persisted and returned (the R2-09
  class). The invariant's "on its success path it writes nothing to
  stderr" is breached under that state. Fix: give `cost.Manager` a warn
  function (setter like `SetModelResolver`), pass `costWarnf` from
  `NewQuerier`, route this line and `Enrich`'s `ancli.Warnf`
  (`manager.go:209`) through it; one test on the manager.
- [x] **R3-05 (Low, D32 hardening)** — The opt-in fixtures' config floor
  is the paid default model. `setupSummaryE2E`
  (`main_summary_e2e_test.go:263`) writes `text.Default` verbatim, so the
  ladder's floor rung is `gpt-5.2`, root seeds carry no `Queries`, and the
  fixture blanks neither `OPENAI_API_KEY` nor `ANTHROPIC_API_KEY`; a future
  batch row that omits `-sm test` would resolve to the OpenAI vendor with
  the developer's key. Today every batch row passes `-sm test`, so the
  guard holds by argument discipline. The same fixture blanks
  `DEBUG_SUMMARY` but not `DEBUG`, which `debugflags` lets win, so
  `DEBUG=1` in the shell fails the six `stderr == ""` oracles. Fix: write
  the summary fixture's `textConfig.json` with `Model: "test"`, blank the
  vendor keys and `DEBUG` as `main_chat_e2e_test.go:370` and
  `main_dirscope_e2e_test.go:395` do; same for `seedConfigDir` in
  `internal/summary` (R3-04).
- [x] **R3-09 (Low)** — `WriteFileAtomic` (`internal/utils/file.go:110`)
  applies `perm` literally with `os.Chmod`, where the replaced
  `os.WriteFile` masked it by umask: a user with `umask 077` now gets a
  world-readable per-model config (which can hold vendor settings) instead
  of an owner-only one. Fix: create the temp with
  `os.OpenFile(O_CREATE|O_EXCL|O_WRONLY, perm)` on a random name so the
  umask applies, or document the exact-mode contract.
- **R3-19 (Note, maintainer decision)** — On upgrade the presence-based
  loader rewrites an existing `textConfig.json` with
  `"summarize-conversations": true` and announces it once. That is D12 and
  the phase-4 text says so, but it is a forced migration of a persisted
  config that turns a paid feature on for every existing user, against the
  maintainer's stated preference for opt-out without rewriting persisted
  configs. No code defect. Confirmed by the maintainer as a deliberate
  exception (D34): the attractiveness of a labelled history outweighs the
  extra tokens.
- **R3-20 (Note)** — `.claude/settings.local.json` is an untracked
  session artefact in the tree and should not ride the commit.
- **R3-21 (Note)** — The `ancli.Warnf("found OPENROUTER_API_KEY but failed
  to init catalog fetcher")` branch (`querier_setup.go:288`) is dead and
  unrouted. Concurrent cold construction is tear-free but lost-update
  prone on the price cache (acceptable per D31; one sentence in
  `summaries.md`). `hasForeign` is computed over `allRows`, so `[f]` is
  offered even when `[d]` already hid every foreign row.

Verified good (review 3): D31 in both writers with the concurrent tests;
the `CostWarnf`/`ErrOut` routing of the enricher, the manager error-log
goroutine and the MCP sink; D32 is a single seam (the real constructor is
referenced by production code only at `main.go:40`, `TestMain` installs
the refusing one before `m.Run`, only `setupSummaryE2E` restores it, no
`t.Parallel` in the root package); no `testing.Testing`, `flag.Lookup
("test.` or `os.Args[0]` sniff in production code; `CLAI_SUMMARIZER=off`
semantics on both paths; opted-out runs build nothing; the `About` header
everywhere; `[d]`/`[f]` toggles filter before grouping and index the
visible slice, so selection stays consistent; `toggleLabel` gated on
`ancli.UseColor` with attribute resets.
