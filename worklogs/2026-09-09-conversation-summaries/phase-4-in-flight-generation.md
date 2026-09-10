# Phase 4 — In-flight generation

**Status:** Complete

[← README](./README.md)

## Goal

Label a new conversation during its first query: launch the summarizer
alongside the main call, print the answer, join with a bounded wait, and
persist once, with the failure and interrupt paths isolated.

## Specification

### Config and flags

- `internal/text/conf.go`: `Configurations` gains
  `SummarizeConversations bool` (`json:"summarize-conversations"`),
  `SummaryModel string` (`json:"summary-model" migrate:"true"`) and
  `SummaryJoinTimeout time.Duration` (`json:"-"`). `text.Default` sets
  `SummarizeConversations` to the README default; the presence-based loader
  announces both keys on upgraded files.
- `internal/flags.go`: `QueryTextFlags` gains `Summarize BoolFlag` (`-summarize`)
  and `SummaryModel StringFlag` (`-sm`, `--summary-model`).
- `internal/text/cmd.go` `ApplyFlagOverrides`: `-summarize` applies on
  `Explicit()` in both directions; `-sm` applies on `Changed()`.
- `internal/completion.go`: `-sm` completes from model history like `-cm`.
- `internal/debugflags`: `SUMMARY` switch; `traceSummaryf` in `internal/text`.

### Querier wiring

- `Querier` gains `summarizer models.Summarizer`, `summaryJoinTimeout`,
  `summaryInterrupt <-chan struct{}`, `summarizeConversations bool`,
  `summaryModel string`, and a `summaryRun *summaryRun` holding the
  in-flight state (result channel, cancel func).
- `NewQuerier` copies the two config fields and the join timeout (zero →
  README default).
- `Querier.SetSummarizer(models.Summarizer)` attaches the summarizer;
  `text.QueryCommandDeps.NewSummarizer` is invoked in the query command's
  `OnSetup` after `SetupQuerier` and the result attached through the setter.
  A nil constructor or a constructor error attaches nothing: the error is
  traced under `DEBUG_SUMMARY` and setup continues (D26).
- The launch goroutine wraps `Summarize` in a `recover`; a panic becomes an
  error on the result channel and is handled like any other failure (D26).
- `main.go` sets `NewSummarizer: summary.NewAgentSummarizer`.

### Launch (`internal/text/summary_launch.go`)

`Querier.Query` calls `launchSummary(ctx)` before `runner.Run` when every
condition in the README's in-flight lifecycle holds. The launch resolves
the model (flag/config already folded into `summaryModel`, else the run's
model), derives the summary context as `context.WithCancel` of
`context.WithoutCancel(run ctx)` and keeps that cancel func in
`summaryRun` (D17; the cancel-key isolation is `Summarize`'s own, D27),
and starts a goroutine that calls `Summarize` and delivers
`(Summary, error)` on a buffered channel.
`summaryInterrupt` nil at launch time is replaced by a channel fed from
`signal.Notify(SIGINT, SIGTERM)`; the registration is released after the
join.

### Finalizer

`sessionFinalizer.Finalize` is restructured (D21):

1. Append the assistant message and usage as today; enrich (phase 1 order).
2. If `session.Failed`, `FinalAssistantText` is empty, or
   `ctx.Err() != nil && !session.SawStopEvent`: cancel the summary run,
   persist as today, return.
3. Print the answer (`postProcessOutput` or the structured print).
4. `joinSummary`: select on the result channel, a timer of
   `summaryJoinTimeout`, and `summaryInterrupt`. A result stamps `Title`,
   `Summary`, `SummaryAt` onto `session.Chat` and appends `Summary.Queries`
   after the main cost rows. Timeout, interrupt or error cancel the run and
   leave the chat unlabelled; errors and timeouts trace under
   `DEBUG_SUMMARY` only.
5. Persist as today (`EnsureOriginDir`, `SaveAsPreviousQuery`, dirscope
   write); a `SaveAsPreviousQuery` failure is returned as the finalizer's
   error exactly as today, and the join outcome never contributes to it.

The legacy display-only call site in `internal/text/querier.go`
(`Finalize(context.Background(), session)` with `Finalized: q.hasPrinted`)
stays as it is: it returns early when the runner already finalized, and
otherwise falls into the "nothing launched" row because no `summaryRun`
exists on that path. The interrupt gate in step two must never treat it as
a normal completion.

### Join table

| Scenario                                                        | Fake / setup                                             | Result                                                    | Test                                    |
| --------------------------------------------------------------- | -------------------------------------------------------- | --------------------------------------------------------- | --------------------------------------- |
| Nothing launched (no summarizer, opt-out, or `Summary` already set) | No `summaryRun`                                       | No wait, no timer, no signal registration; persisted as today (covers the summarizer's own querier and `pkg/agent`) | `TestFinalize_join` |
| Instant fake                                                     | Returns at once                                          | Chat labelled; `Queries` gains the summary row after the main row; persisted once | `TestFinalize_join`   |
| Fake slower than the bound                                       | Blocks until its context is cancelled; timeout injected small | Unlabelled; no error; the fake's context is cancelled; elapsed below the injected bound plus test slack | `TestFinalize_join` |
| Erroring fake                                                    | Returns an error                                         | Unlabelled; no error; nothing on stderr                   | `TestFinalize_join`                     |
| Main run failed                                                  | Runner error                                             | Not joined; summary context cancelled; persisted           | `TestFinalize_join`                     |
| Interrupt before completion                                      | Run context cancelled, `SawStopEvent` false              | Not joined; summary context cancelled; persisted           | `TestFinalize_join`                     |
| Interrupt during the join                                        | Injected `summaryInterrupt` closed while the fake blocks | Join returns at once; unlabelled; persisted                | `TestFinalize_join`                     |
| Answer printed before the join                                   | Blocking fake; output writer records order               | Answer bytes precede the join wait                         | `TestFinalize_printsAnswerBeforeJoin`   |
| Structured output                                                | `ResponseFormat` set; instant fake                       | Labelled; final JSON printed once                          | `TestFinalize_join`                     |
| Panicking fake                                                   | `Summarize` panics                                       | Unlabelled; no error; answer printed; persisted; process alive | `TestFinalize_join`                 |
| Every join outcome                                               | All rows above                                           | Run's returned error is nil unless persist failed; exit status unchanged | `TestFinalize_join`       |

### Launch table

| Condition                                       | Launched? | Test                                   |
| ----------------------------------------------- | --------- | -------------------------------------- |
| All conditions hold, new chat                   | yes       | `TestQuery_launchConditions`           |
| `InitialChat.Summary` non-empty (`-dre` continuation of a labelled chat) | no | `TestQuery_launchConditions`     |
| `-dre` continuation of an unlabelled pre-feature chat | yes  | `TestQuery_launchConditions`           |
| `summarize-conversations` false                  | no        | `TestQuery_launchConditions`           |
| `-summarize=false` over a true config            | no        | `TestQuery_launchConditions`           |
| `-summarize` over a false config                 | yes       | `TestQuery_launchConditions`           |
| `ShouldSaveReply` false                          | no        | `TestQuery_launchConditions`           |
| No summarizer attached (`pkg/agent`, tests)      | no        | `TestQuery_launchConditions`           |
| Model: `-sm` > config `summary-model` > run model | as listed | `TestQuery_summaryModelResolution`     |

### Context table

| Scenario                                                              | Result                                                            | Test                                        |
| --------------------------------------------------------------------- | ----------------------------------------------------------------- | ------------------------------------------- |
| Root cancel func invoked (as the runner does on `StopEvent`)          | Summary context not done                                          | `TestSummaryContext_isolation`              |
| `summaryRun` cancel invoked                                           | Summary context done; run context not done                        | `TestSummaryContext_isolation`              |
| Summary context carries no cancel func under `utils.ContextCancelKey` (the key is installed by `Summarize`, D27) | The run's cancel func is not reachable from the summary context | `TestSummaryContext_isolation`  |
| Values of the run context visible on the summary context                | Present                                                           | `TestSummaryContext_isolation`              |
| Summarizer launched while the main run has a `cmd-ban` entry              | The main run's `cmd` call is still refused after the launch (D29) | `Test_e2e_query_summary_keeps_cmd_ban`      |
| Real mock main call completes with `StopEvent`; blocking fake         | Fake still running when the finalizer starts the join             | `TestQuery_stopEventDoesNotCancelSummary`   |

## Integration contract

| Trigger                                                                                                  | Collaborators / fakes                                         | Observable result                                                                                  | Required side effects                                                       | Prohibited side effects                                       |
| -------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------- | -------------------------------------------------------------------------------------------------- | --------------------------------------------------------------------------- | ------------------------------------------------------------- |
| `clai -cm test -t ls q "hello tool_submit_summary"` in the e2e temp config dir                           | Mock vendor for both runs; real `summary.NewAgentSummarizer`   | stdout holds the answer `hello tool_submit_summary`; the persisted conversation has `title: "Mock title"`, `summary: "Mock summary."`, a `summary_at`, and exactly two `queries` rows, the second with `purpose: "summary"`; the index row and `globalScope.json` carry the title | Binding written as today | stderr empty; no MCP client; no second conversation file |
| Same with `-summarize=false`                                                                             | As above                                                      | No `title`; one `queries` row                                                                       | As above                                                                    | stderr empty                                                  |
| `clai -cm test -t ls q "hello"` (no token: the mock never submits)                                       | As above                                                      | Answer printed; no `title`; exit status zero                                                        | As above                                                                    | stderr empty                                                  |
| Same as row one with `-sm test` and config `summary-model` unset                                         | As above                                                      | Summary `queries` row has `model` = the mock's reported model name                                  | As above                                                                    | None                                                          |
| Row one, then `clai -cm test -dre q "again"`                                                             | As above                                                      | Title unchanged; `summary_at` unchanged; exactly one summary row in `queries`                       | As above                                                                    | No second summarizer run (no third `queries` row)             |
| `clai -cm test -t=cmd -cmd-ban=touch q "tool_cmd tool_submit_summary"` with `CLAI_MOCK_CMD_COMMAND` = `touch <marker>` | As above; real `cmd` tool                      | The persisted conversation holds a tool result starting with `ERROR:` naming `touch`, and `title: "Mock title"`; the marker file does not exist | As above | No spawn of `touch`; stderr empty |

## Acceptance criteria

| Outcome                                                        | Test                                                                                   |
| -------------------------------------------------------------- | -------------------------------------------------------------------------------------- |
| Flags apply per the override rules                             | `TestApplyFlagOverrides_summary` (`internal/text/cmd_test.go`)                          |
| Config migration announces the two keys                         | `TestTextConfigMigration_addsSummaryKeys` (`internal/text/config_migration_test.go`)    |
| `NewQuerier` copies fields and defaults the join timeout        | `TestNewQuerier_summaryDefaults` (`internal/text/querier_setup_test.go`)                |
| Launch table                                                    | `TestQuery_launchConditions`, `TestQuery_summaryModelResolution` (`internal/text/summary_launch_test.go`) |
| Context table                                                   | `TestSummaryContext_isolation`, `TestQuery_stopEventDoesNotCancelSummary`               |
| Join table                                                      | `TestFinalize_join`, `TestFinalize_printsAnswerBeforeJoin` (`internal/text/finalizer_test.go`) |
| `-sm` completion                                                | `TestCompletion_summaryModelFlag` (`internal/completion_test.go`)                        |
| Integration rows                                                | `Test_e2e_query_labels_in_flight`, `Test_e2e_query_summarize_opt_out`, `Test_e2e_query_summary_failure_silent`, `Test_e2e_query_summary_model_flag`, `Test_e2e_dirreply_does_not_relabel`, `Test_e2e_query_summary_keeps_cmd_ban` (`main_summary_e2e_test.go`) |

Files: `internal/text/conf.go`, `internal/text/cmd.go`,
`internal/text/cmd_test.go`, `internal/text/config_migration_test.go`,
`internal/text/querier.go`, `internal/text/querier_setup.go`,
`internal/text/querier_setup_test.go`, `internal/text/summary_launch.go`,
`internal/text/summary_launch_test.go`, `internal/text/finalizer.go`,
`internal/text/finalizer_test.go`, `internal/text/debug_trace.go`,
`internal/flags.go`, `internal/flags_test.go`, `internal/completion.go`,
`internal/completion_test.go`, `internal/debugflags/*.go`, `main.go`,
`main_summary_e2e_test.go`.

## Error coverage

| Failure                                                         | Expected outcome                                                          | Test                                    |
| --------------------------------------------------------------- | ------------------------------------------------------------------------- | --------------------------------------- |
| Summarizer returns an error                                     | Unlabelled; run succeeds; `DEBUG_SUMMARY` line only                       | `TestFinalize_join`                     |
| Summarizer exceeds the bound                                    | Unlabelled; cancelled; run succeeds                                       | `TestFinalize_join`                     |
| `NewSummarizer` constructor errors in `OnSetup`                 | Nothing attached; `DEBUG_SUMMARY` trace only; the query runs and succeeds (D26) | `TestQueryCommand_newSummarizerError` (`internal/text/query_cmd_test.go`) |
| `Summarize` panics                                              | Recovered into an error; unlabelled; run succeeds (D26)                   | `TestFinalize_join`                     |
| Persist fails after a successful join                           | Persist error returned as today; the answer was already printed           | `TestFinalize_join`                     |
| Summary rows present but main enrichment failed                 | Summary rows appended alone; index model resolution skips them (phase 1)  | `TestFinalize_join`                     |

## Implementation notes

### 2026-09-09 — phase-4 worker (clai, session 5753ae90)

Deltas from the specification, surprises and verification only.

- **Interrupt path keeps today's display.** Finalizer step two says an
  interrupt (`ctx.Err() != nil && !SawStopEvent`) "persists as today,
  returns". Today's finalizer prints the (partial) answer on that path, so
  returning before the display would drop it in non-raw modes. Implemented
  as: abandon the summarizer, then display, then persist; the join is
  skipped because no `summaryRun` remains. The join-table row's observables
  (not joined, summary context cancelled, persisted) are unchanged and
  proven by `TestFinalize_join/interrupt_before_completion`. A failed or
  empty run persists immediately without display, exactly as today.
- **Cancel key masked, not only absent.** `context.WithoutCancel` keeps
  values, so the run's cancel func under `utils.ContextCancelKey` would
  still be reachable from the summary context. The launcher shadows the
  key with a nil value (`context.WithValue(..., ContextCancelKey, nil)`);
  `TestSummaryContext_isolation` row three asserts `Value(key) == nil`.
- **`Querier.runModel`.** The querier had no copy of the configured model
  name (only the completer, whose `ModelNamer` the mock does not
  implement), so `NewQuerier` now stores `userConf.Model` as the ladder's
  last rung on the query path (parameters row "`-sm` > config > run model").
- **Mock vendor race under cancellation** (`internal/vendors/mock.go`, not
  in the phase file list). Abandoning the summarizer cancels its querier
  mid-stream; the runner's `ctx.Done()` branch reads `Mock.TokenUsage()`
  while the mock's streaming goroutine writes `usage`, which the race
  detector reported in the root e2e suite
  (`Test_e2e_skills_opt_in_enablement_and_precedence`, where the main run
  fails fast). `usage` is now guarded by a mutex; vendor-local change.
- **Integration row five oracle.** The row's prohibited effect reads "no
  third `queries` row", but the `-dre` continuation is itself a persisting
  run that appends its own main cost row (D13, D19). `Test_e2e_dirreply_does_not_relabel`
  asserts exactly one `purpose: "summary"` row and three rows in total.
- **`DEBUG_SUMMARY` trace stream.** `traceSummaryf` reuses the
  `traceChatf` shape, `ancli.Noticef`, which writes to stdout; the
  `TestQueryCommand_newSummarizerError` DEBUG row asserts on stdout+stderr.
- **"Building cache index" chatter.** A fresh config dir prints the
  pre-existing index-build progress on stderr on the first save. The
  empty-stderr oracles seed `{"version":2,"rows":[]}` into
  `chat_index.cache` (`seedEmptyChatIndex` in `internal/text`, inline in
  `setupSummaryE2E`) so the assertion is exact rather than filtered.
- **Session globals.** `Command.Setup` derives `utils.ReadonlyConfig` from
  `-r` process-wide; the new command-boundary test restores the globals in
  `t.Cleanup`, otherwise two pre-existing stoploss migration tests fail
  later in the package.
- **Extra tests beyond the tables.** `TestSignalInterrupt` (nil-channel
  default: SIGINT closes the channel, release leaves it open) and a
  `TestFinalize_join` row for a summary without `GeneratedAt`
  (`SummaryAt` stamped now). `internal/debugflags` gained only a
  `DEBUG_SUMMARY` row in `TestEnabled`; `Enabled` needed no change.
- **Not touched.** `architecture/*.md` (phase 7); `internal/summary`
  (phase 2's `Summarize` already owns the D27 child context).

Verification (repository root, final tree):

- `go run mvdan.cc/gofumpt@latest -w -l .` — clean after formatting.
- `go run honnef.co/go/tools/cmd/staticcheck@latest ./...` — exit 0 (one
  ST1008 in a test helper fixed during the session).
- `go vet ./...`, `go fix ./...` — clean.
- `go run github.com/mibk/dupl@latest -t 80 .` — 31 clone groups, the
  pre-existing set; none in files this phase added or edited.
- `go test ./... -race -cover -count=3 -timeout=30s` — first run (load
  11.6): root package failed with the mock `usage` data race above plus a
  30 s alarm; after the mutex fix the unedited parallel gate exited 0 with
  every package `ok` (load 10; root 30.4 s wall, `internal/text` 84.3%,
  `internal` 80.1%). Final tree (two test additions later): the parallel
  gate at load 6.6 → 18.9 failed purely with `panic: test timed out after
  30s` in the root package (`Test_e2e_dirscope_lookback_on_tools_work`
  running at 0 s) and `internal/audio`
  (`TestCalibrationEndToEndDiscoversLateSpeakers` at 0 s), no `--- FAIL`,
  no data race; the same command with `-p 1` exited 0 with all 45
  packages `ok` (root 22.9 s, `internal/audio` 13.7 s, `internal/text`
  5.6 s at 84.4%). Root alone at `-count=3` on a quiet host: 22.2 s.
- Root package alone at `-count=1`: 8.6 s; the six new e2e tests take
  0.01–0.02 s each. With `NewSummarizer` temporarily nil the e2e subset
  ran in 7.0 s versus 8.2 s wired, so the in-flight summarizer adds about
  a second per count across the whole root suite.
- `internal/text` per-function coverage of the new code: `launchSummary`,
  `abandonSummary`, `signalInterrupt`, `shouldLaunchSummary` 100%,
  `joinSummary` 94%, `Finalize` 96%, `persist` 93%, `attachSummarizer`
  90%; package 84.4% (was 83.6%).

## Review findings

### Review 2 — 2026-09-10 (`worklog-review`, post-implementation)

- **R2-06 (Note)** — The in-flight launcher fires only on the query
  command: `attachSummarizer` lives in `internal/text/cmd.go` and the
  `chat` tree attaches no summarizer, so a pre-feature conversation
  continued through `clai chat continue` stays unlabelled until a batch
  run. Consistent with the README's "query path" wording, but neither the
  README nor `architecture/summaries.md` says so. Phase 8 adds one
  sentence to the architecture doc. No code change.

Verified good (every branch of the finalizer traced,
`internal/text/finalizer.go:108-136`):

- Failed or empty run: `abandonSummary` then persist, no join.
- Interrupt (root cancelled, `SawStopEvent` false): abandon before the
  display, then display and persist. The runner's `ctx.Done()` branch
  returns without an error and without `SawStopEvent`
  (`session_runner.go:279`), so the gate is exact; a normal completion
  also cancels the root context but sets `SawStopEvent`, so the join runs.
- Success: display, join bounded by the timer, the injected interrupt
  channel or the result, `ApplyTo`, one persist. The structured-output
  branch takes the same path.
- `launchSummary` masks the run's cancel func with a `nil` value under the
  key on a `WithoutCancel` child; the only reader of the key
  (`session_runner.go:246`) type-asserts with an ok check, so the masked
  value cannot panic and the run's cancel is unreachable. The launcher's
  own cancel is called by `abandonSummary` on every exit of the join.
- The goroutine sends exactly once on a buffered channel: the normal send
  or the recovered panic, never both.
- `signalInterrupt` registers SIGINT and SIGTERM; `shutdown.Monitor`
  already registers both (`go_away_boilerplate/pkg/shutdown`), so the
  extra `Notify` alters no process behaviour and `release` stops it on
  every abandon path. Hypothesis "SIGTERM is swallowed while the watch is
  registered" disproved.
- Launch conditions, the model ladder on the query path (`-sm` → config →
  run model, the explicit value short-circuits the summarizer's ladder as
  the README states), and the e2e `cmd-ban` row all hold.

### Review 3 — 2026-09-10 (`worklog-review`, holistic)

- [x] **R3-08 (Low)** — `joinSummary` (`internal/text/summary_launch.go:116`)
  applies any nil-error result, including a `Summary` with an empty title
  and summary; `ApplyTo` then stamps `SummaryAt` and appends usage rows
  onto an unlabelled chat, and the launch gate (`chat.Summary == ""`)
  relaunches on the next `-dre`. The "never regenerated" invariant holds
  only because `submit_summary` rejects empty fields. Fix: treat an empty
  `Summary` as a failure in the join (trace, no `ApplyTo`) so the contract
  does not rest on every `Summarizer` implementation validating.
- [x] **R3-17 (Low)** — `traceSummaryf` (`internal/text/debug_trace.go:19`)
  and `traceCostWarnf` (`internal/summary/summarizer.go:114`) go through
  `ancli.Noticef`, which prints on stdout: with `DEBUG_SUMMARY=1` (or plain
  `DEBUG`) under `-r`, the summarizer's traces land in the answer stream.
  Opt-in, but the same pitfall as R2-03. Fix: write the traces to stderr,
  or document that `DEBUG_SUMMARY` is incompatible with piped `-r` output.
- **R3-18 (Note)** — The in-flight summarizer receives the initial chat,
  so its label describes the prompt and never the outcome; only the batch
  path sees the full transcript. The lifecycle section states it, the
  user-facing `architecture/summaries.md` does not say it in one plain
  sentence. Worth one line, since the worklog's motivation names "how it
  ended".
- **R3-21 (Note)** — `launchSummary` overwrites `q.summaryRun` without
  abandoning a previous run; unreachable today because every `Query` ends
  in the deferred `Finalize`, which joins or abandons. A guard at the top
  would make the leak-freedom local. The `timeout <= 0` fallback in
  `joinSummary` duplicates `NewQuerier`'s default and is reached only by
  fixtures that bypass it.

Verified good (review 3): the launch gate and every row of
`TestQuery_launchConditions`; exactly one send on the buffered result
channel (normal or recovered panic); `signal.Stop` reached on every path
(`joinSummary` defers the abandon, the failed/empty and interrupt branches
call it, `Run` defers `Finalize` on every exit); the interrupt gate
`ctx.Err() != nil && !SawStopEvent` with `SawStopEvent` set only by a
tool-free `StopEvent`; D17/D27 isolation in both directions
(`TestSummaryContext_isolation`, `TestQuery_stopEventDoesNotCancelSummary`);
display → join → persist on the plain and structured paths; an abandoned
goroutine touches only its cloned request, its own querier, the atomic
per-model config write and the routed seams. `joinSummary` 94.1%,
`Finalize` 96.3%.
