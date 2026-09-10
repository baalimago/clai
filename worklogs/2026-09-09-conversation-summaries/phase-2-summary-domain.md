# Phase 2 — `internal/summary` domain

**Status:** Complete

[← README](./README.md)

## Goal

Provide the `Summarizer` interface and its production implementation: a
one-off querier that reads a rendered transcript and submits a validated
title and summary through the `submit_summary` tool.

## Specification

### Shared types (`internal/models`)

`internal/models/summary.go` holds `SummaryRequest`, `Summary` and
`Summarizer` exactly as the README's shared-interfaces block defines them.
No logic beyond the types.

### `submit_summary` tool (`internal/summary/tool.go`)

An `LLMTool` in the shape of `pkg/tools/bash_tool_date.go`: a
`Specification` with `Name` = the README tool name, a description that
states the limits by name and value taken from the package constants, and
two required string properties `title` and `summary`. The tool is
constructed per `Summarize` call around a holder (`*submission`, guarded by
a mutex) so concurrent summarizer runs never share state.

Validation table — every rejection returns an error whose text names the
field and the rule, which the tool loop folds into the tool result as
`ERROR: …` and returns to the model:

| Input                                                       | Result                                                        | Test                                     |
| ----------------------------------------------------------- | ------------------------------------------------------------- | ---------------------------------------- |
| `title` missing or not a string                             | error `title: required`                                        | `TestSubmitSummary_validation`           |
| `title` empty after trimming                                | error `title: empty`                                           | `TestSubmitSummary_validation`           |
| `title` contains a newline                                  | error `title: single line`                                     | `TestSubmitSummary_validation`           |
| `title` longer than `title-max-runes` after whitespace collapse | error naming the limit                                     | `TestSubmitSummary_validation`           |
| `summary` missing or not a string                           | error `summary: required`                                      | `TestSubmitSummary_validation`           |
| `summary` empty after trimming                              | error `summary: empty`                                         | `TestSubmitSummary_validation`           |
| `summary` longer than `summary-max-runes` after whitespace collapse | error naming the limit                                 | `TestSubmitSummary_validation`           |
| Both valid                                                  | stored (trimmed, internal whitespace collapsed); returns `accepted` | `TestSubmitSummary_storesLastValid` |
| Valid twice                                                 | the later submission replaces the earlier                      | `TestSubmitSummary_storesLastValid`      |

Whitespace collapse: runs of Unicode whitespace become one space; the
summary keeps sentence punctuation as given. Rune counts use
`utf8.RuneCountInString`.

### Transcript rendering (`internal/summary/render.go`)

`renderTranscript(chat) string` emits one `<transcript>` block: every
`user` message and, when one exists, the **last** `assistant` message, in
transcript order, each as `role: text` where text is `Message.String()`
for plain content and the concatenated `Text` parts otherwise. System,
tool and tool-call-only messages are skipped; image parts are skipped.
The block is capped head-preserving at `summary-input-runes` runes with a
single-rune ellipsis marker when truncated. The instruction paragraph after
the block tells the model to call the tool with the two fields and states
both limits by value.

### Summarizer prompt (`internal/summary/prompt.go`)

A fixed system prompt: the model is a conversation labeller; it must call
`submit_summary` exactly once with a short imperative title and a
two-sentence summary stating what was asked and, when visible, the
outcome; it must not answer the conversation; on a rejection it must
correct the named field and call again.

### `AgentSummarizer` (`internal/summary/summarizer.go`)

`NewAgentSummarizer(confDir string) (models.Summarizer, error)` loads
`textConfig.json` through `utils.LoadConfigFromFileCollect` (the variant
without the stdout upgrade announcement; the added-paths list is
discarded) with `text.MigrateOldChatConfig` and `text.Default`, and keeps
`summary-model` and `model` for the ladder. `Summarize`:

1. Resolves the model (README ladder, D22) and errors when every rung is
   empty.
2. Builds a `text.Configurations` per the README's "The summarizer querier"
   paragraph, with a usage recorder that appends every
   `CompletedModelCall`.
3. Calls `text.CreateQuerier`, asserts `models.ChatQuerier`, and runs
   `TextQuery` with a chat holding the single rendered user message.
4. Reads the holder. Empty → error `no summary submitted`. Otherwise
   returns `Summary{Title, Summary, Model, Queries, GeneratedAt: time.Now().UTC()}`
   where `Queries` follows the README rule (enriched rows when present,
   else one synthesized row from the recorded usage with zero cost), each
   stamped `Purpose: "summary"`.

The querier's context is a `context.WithCancel` child of the caller's
context; `Summarize` stores that child's cancel func under
`utils.ContextCancelKey` on the child and calls it on return (D27). The
runner's `StopEvent` cancel therefore reaches only the child, while a
cancel of the caller's context still reaches the querier. Lifetime
isolation from the run context (detaching from its cancellation) stays
the caller's responsibility (phase 4). The summarizer never writes
`chat.SkipIndex`.

### Context table

| Scenario                                                                               | Result                                                         | Test                                              |
| -------------------------------------------------------------------------------------- | -------------------------------------------------------------- | ------------------------------------------------- |
| Caller context carries a cancel func under `utils.ContextCancelKey`; mock run completes | Caller context not done after `Summarize` returns; caller's func never called | `TestAgentSummarizer_stopEventDoesNotCancelCaller` |
| Caller context cancelled while the mock run is in flight                               | `Summarize` returns an error wrapping `context.Canceled`        | `TestAgentSummarizer_cancelled`                   |
| Caller context carries no cancel func under the key                                    | Run completes; no panic; result as usual                        | `TestAgentSummarizer_submitsThroughMock`          |

### `ParseSince` (`internal/summary/since.go`)

`ParseSince(s string, now time.Time) (time.Time, error)` per D4:

| Input                          | Result                                                | Test                 |
| ------------------------------ | ----------------------------------------------------- | -------------------- |
| Go duration (`s`, `m`, `h`)    | `now - d`                                              | `TestParseSince`     |
| Extended duration with `d`, `w` | days and weeks as multiples of a day; a weeks-then-days value is allowed | `TestParseSince` |
| RFC 3339 timestamp             | that instant                                           | `TestParseSince`     |
| `YYYY-MM-DD`                   | local midnight of that date                            | `TestParseSince`     |
| Empty, negative, zero, unparsable | error naming the accepted forms                     | `TestParseSince`     |

### Mock vendor support (`internal/vendors/mock.go`)

`inputsForTool` gains a `submit_summary` case. It takes the call ordinal
(the count of prior `submit_summary` calls in the chat's assistant
messages, which `nextToolCall` already computes when it skips tokens
whose call was made; that loop is extended to hand the count on, not
duplicated) and returns the
nth entry of `CLAI_MOCK_SUMMARY_TITLES` and `CLAI_MOCK_SUMMARY_SUMMARIES`
(`|`-separated; the last entry repeats; the README defaults apply when the
variable is unset). This is the only mock change; existing cases keep
their signature through a thin wrapper.

## Integration contract

| Trigger                                                                                                      | Collaborators / fakes                                                    | Observable result                                                          | Required side effects | Prohibited side effects                                                                                   |
| ------------------------------------------------------------------------------------------------------------ | ------------------------------------------------------------------------ | -------------------------------------------------------------------------- | --------------------- | --------------------------------------------------------------------------------------------------------- |
| `Summarize` with `Model: "test"` over a chat whose user message contains `tool_submit_summary` once           | Mock vendor; temp config dir seeded like `setupMainTestConfigDir`        | `Title == "Mock title"`, `Summary == "Mock summary."`, one `Queries` row with `Purpose: "summary"` and non-zero usage | None | No file under `conversations/`, no `globalScope.json`, no `dirs/`, no `chat_index.cache`; `chat.SkipIndex` still false; stdout and stderr empty |
| Same, token present twice, `CLAI_MOCK_SUMMARY_TITLES` = a title of 70 runes then `Good title`                | Mock vendor                                                              | `Title == "Good title"`; the querier chat holds two tool results, the first starting with `ERROR: title`  | None                  | As above                                                                                                  |
| Same, token present once, `CLAI_MOCK_SUMMARY_TITLES` = a title of 70 runes                                   | Mock vendor                                                              | Error `no summary submitted`                                               | None                  | As above                                                                                                  |
| Same, temp config dir holds one `mcpServers/*.json` with command `false`                                     | Mock vendor                                                              | Result as row one                                                          | None                  | No MCP client spawned (no `failed to setup` on stderr)                                                    |
| Same, context cancelled before the call                                                                      | Mock vendor                                                              | Error wrapping `context.Canceled`                                          | None                  | As row one                                                                                                |
| Same as row one, caller context built like `main.go` builds the root context (a cancel func under `utils.ContextCancelKey`) | Mock vendor                                               | Result as row one; the caller context is not done afterwards               | None                  | The caller's cancel func is never invoked                                                                  |

## Acceptance criteria

| Outcome                                                                                     | Test                                                                   |
| ------------------------------------------------------------------------------------------- | ---------------------------------------------------------------------- |
| Validation table holds row by row                                                           | `TestSubmitSummary_validation`, `TestSubmitSummary_storesLastValid`    |
| Rendering: user messages plus last assistant only, text parts only, cap head-preserving     | `TestRenderTranscript_selectsMessages`, `TestRenderTranscript_capsInput` |
| Model ladder resolves in README order and errors when empty                                 | `TestResolveModel_ladder`                                              |
| Mock success path through the real tool loop                                                | `TestAgentSummarizer_submitsThroughMock`                               |
| Rejection then acceptance through the real tool loop                                        | `TestAgentSummarizer_retriesAfterRejection`                            |
| Nothing submitted → error                                                                   | `TestAgentSummarizer_errorsWithoutSubmission`                          |
| Persists nothing, keeps the index enabled, starts no ambient server, silent on stdout/stderr | `TestAgentSummarizer_isSideEffectFree`                                 |
| Context table: the caller's cancel func is never invoked; caller cancel still reaches the run | `TestAgentSummarizer_stopEventDoesNotCancelCaller`, `TestAgentSummarizer_cancelled` |
| `Queries` rows stamped `Purpose: "summary"`, usage non-zero                                  | `TestAgentSummarizer_queriesCarryUsage`                                |
| Synthesized row when enrichment produced none                                               | `TestAgentSummarizer_synthesizesUsageRow`                              |
| `ParseSince` table                                                                          | `TestParseSince`                                                       |
| Mock sequence env selects the nth submission                                                | `TestMock_submitSummarySequence`                                       |
| Construction loads `textConfig.json` defaults and writes nothing to stdout, even for a file that predates a key | `TestNewAgentSummarizer_loadsConfig`                |

Files: `internal/models/summary.go`, `internal/summary/tool.go`,
`internal/summary/tool_test.go`, `internal/summary/render.go`,
`internal/summary/render_test.go`, `internal/summary/prompt.go`,
`internal/summary/summarizer.go`, `internal/summary/summarizer_test.go`,
`internal/summary/since.go`, `internal/summary/since_test.go`,
`internal/vendors/mock.go`, `internal/vendors/mock_test.go`.

## Error coverage

| Failure                                                      | Expected outcome                                                     | Test                                            |
| ------------------------------------------------------------ | -------------------------------------------------------------------- | ----------------------------------------------- |
| No model on any rung                                         | Error before any querier is built                                    | `TestResolveModel_ladder`                       |
| Unknown vendor for the resolved model                        | `CreateQuerier` error wrapped with the model name                    | `TestAgentSummarizer_unknownModel`              |
| Model never calls the tool                                   | `no summary submitted`                                               | `TestAgentSummarizer_errorsWithoutSubmission`   |
| Every submission rejected until `summary-max-tool-calls`     | Run ends through the refusal ladder; `no summary submitted`          | `TestAgentSummarizer_exhaustsAttempts`          |
| Context cancelled                                            | Error wrapping `context.Canceled`; nothing written                   | `TestAgentSummarizer_cancelled`                 |
| The runner cancels the context under `utils.ContextCancelKey` on `StopEvent` | Only the summarizer's own child is cancelled; the caller's context and cancel func untouched | `TestAgentSummarizer_stopEventDoesNotCancelCaller` |
| `textConfig.json` unreadable at construction                 | `NewAgentSummarizer` returns the load error                          | `TestNewAgentSummarizer_loadsConfig`            |
| `textConfig.json` predates `summary-model`                   | Loaded with the default filled; no announcement on stdout            | `TestNewAgentSummarizer_loadsConfig`            |
| Chat with no user message                                    | Rendered block empty; the run proceeds; whatever the model submits applies | `TestRenderTranscript_selectsMessages`     |

## Implementation notes

### 2026-09-09T15:44Z — phase-2 worker (Claude session_01KXDFdab6MmuuKetC1EBowJ)

Executed test-first on top of the phase 1 and 3 diffs. Deltas from the
specification, in order of weight:

1. **`Configurations.SummaryModel` declared here, not in phase 4.** The
   ladder rung `config summary-model` is read through
   `utils.LoadConfigFromFileCollect(..., &text.Default)`, which needs the
   field on `text.Configurations`. The field is added to
   `internal/text/conf.go` as `json:"summary-model"` with the README default
   (empty) and **without** `migrate:"true"`: a zero default with no migrate
   tag is never filled into an existing file, so no textConfig.json is
   rewritten and no announcement fires on this phase's diff (the root e2e
   suites that pin file contents and announcements stay untouched). Phase 4
   still owns the tag, the `-sm` flag, `SummarizeConversations`,
   `SummaryJoinTimeout` and the `NewQuerier` copies; it must add
   `migrate:"true"` and adjust the announcement e2e rows when it does.
2. **Tool-result oracle for the retry row.** The tool loop folds a tool
   error as `ERROR: failed to run tool: submit_summary, error: title: …`
   and prefixes every within-budget result with `[ Tool calls remaining: N ] `,
   so the literal "starting with `ERROR: title`" cannot hold on the real
   path; `TestAgentSummarizer_retriesAfterRejection` asserts the first
   result contains `ERROR:` and `title:` and the second ends in `accepted`.
3. **Mock `inputsForTool` takes the ordinal directly** (`inputsForTool(name,
   ordinal)`) instead of a wrapper: `StreamCompletions` is its only caller,
   so a signature-preserving wrapper would be dead code under staticcheck.
   `nextToolCall` returns `(name, ordinal, ok)`; the ordinal is the prior
   call count it already builds, the skip loop runs on a `maps.Clone` of it.
4. **`Raw: true` on the summarizer `Configurations`** in addition to the
   README list: it keeps the finalizer off the pretty-print and rolling
   viewport paths, which is what makes the empty-stdout/stderr assertion
   deterministic with `Out: io.Discard`.
5. **Cancellation is detected by `Summarize`, not by the runner.** The
   session runner treats `ctx.Done()` as a normal stop (`StopRequested`,
   no error), so `TextQuery` returns nil on a cancelled context. `Summarize`
   checks the caller's `ctx.Err()` before building the querier and again
   after `TextQuery` and returns `summarize: context canceled`. The
   in-flight row of the context table runs against a blocking
   `ChatQuerier` injected through the `newQuerier` seam (the mock returns
   instantly, so there is no real in-flight window); the pre-cancelled row
   runs the real mock path.
6. **`newQuerier` seam.** `agentSummarizer.newQuerier` defaults to
   `text.CreateQuerier`; tests wrap it (`chatCapture`) to read the querier
   chat while the whole real path runs, and swap it for the blocking
   querier above.
7. **Last assistant message = last assistant message with text.** A
   trailing tool-call-only assistant message is skipped and the previous
   textual one is rendered, per the "tool-call-only messages are skipped"
   rule.
8. `TestAgentSummarizer_synthesizesUsageRow` runs the real path with the
   price files removed: the catalog never resolves, enrichment warns and
   returns the chat unchanged, and the summarizer synthesizes the row.

Verification (all from the repository root):

```
go test ./internal/summary/ -race -count=3 -timeout=30s -cover
  ok  internal/summary  coverage: 95.2% of statements
go test ./internal/vendors/ ./internal/models/ ./internal/text/ -race -count=3 -timeout=30s
  ok (all three)
go run mvdan.cc/gofumpt@latest -w -l .        -> no output
go run honnef.co/go/tools/cmd/staticcheck@latest ./...  -> exit 0
go vet ./...                                  -> exit 0
go fix ./...                                  -> exit 0 (rewrote one test loop to `for range`)
go run github.com/mibk/dupl@latest -t 80 .    -> 31 clone groups, all pre-existing
  (a first run reported 32: the config-dir walk helper cloned
  internal/vendors/pi/source_reader.go; rewritten on fs.WalkDir)
go test ./... -race -cover -count=3 -timeout=30s   (load average 5-7)
  exit 0; every package ok; root 26.8s, internal/text 12.7s
```

## Review findings

### Review 2 — 2026-09-10 (`worklog-review`, post-implementation)

- [x] **R2-03 (High; filed as Low, re-rated in phase 8)** — The
  summarizer's querier inherits the cost enricher's warnings.
  `costEnricher.enrich` (`internal/text/cost_enricher.go`) warns through
  `ancli.Warnf` when the catalog is not ready within its bound or when the
  price is missing; with no `OPENROUTER_API_KEY` and a per-model config
  file without a `price` field, every successful in-flight run therefore
  prints `warning: failed to enrich chat with cost estimate: … missing
  pricing` a second time (the main run already prints it once).
  **Correction from phase 8:** `ancli.Warnf` is `PrintWarn`, which writes
  to **stdout**, not stderr (the ancli stdout pitfall). The cold-price
  warning therefore broke the README invariant "the summarizer never
  writes to the host's stdout", a High, and under `-r` landed inside the
  machine-readable answer stream. Reproduced while running the review's
  concurrency test in this package. The e2e rows never saw it because
  every fixture seeds price files (`setupSummaryE2E`). Fixed in phase 8:
  `Configurations.CostWarnf` routes the summarizer querier's enricher
  warnings to a `DEBUG_SUMMARY` trace; `TestAgentSummarizer_coldPriceIsSilent`
  and `Test_e2e_query_labels_in_flight_cold_price` prove both streams
  clean without seeded prices.
- [x] **R2-04 (Low)** — `NewAgentSummarizer`
  (`internal/summary/summarizer.go:30`) loads `textConfig.json` through
  `LoadConfigFromFileCollect` and discards the announcements. On the query
  path the announcing load in `SetupQuerier` has already run, so nothing
  is lost; on the batch path `summarizeSetup` makes this the first load in
  the process, so an upgrade rewrite (the `summary-model` fill the README's
  release note promises "on the next interactive run") happens silently
  when `chat summarize` is the first command after upgrading. Fix in
  phase 8: print the collected announcements on the batch path (the
  `chat` tree's `summarizeSetup` can announce them the way the announcing
  loader does) or state in `architecture/summaries.md` that the batch
  path upgrades silently. Fixed in phase 8: the constructor announces the
  collected additions through `ancli.PrintOK` (the announcing loader's
  own message), which fires only on the batch path because the query
  path's `SetupQuerier` has already upgraded the file; the V2-03 row
  "loads silently" is superseded by "announces the upgrade once"
  (`TestNewAgentSummarizer_loadsConfig`, `TestNewAgentSummarizer_announcesUpgrade`).

Verified good:

- `Summarize` owns `utils.ContextCancelKey` on a `WithCancel` child and
  checks the caller's `ctx.Err()` after the run, so a caller cancel wins
  over a normal stop and the runner's `StopEvent` cancel reaches only the
  child (`internal/summary/summarizer.go:52-83`).
- The querier is built with `SaveReplyAsConv: false`, `SkipAmbientMcpServers`,
  one injected tool, no globs, `Out: io.Discard`, an empty `CmdBan`; the
  side-effect test walks the config dir before and after and asserts no
  conversation, mirror, index or binding write and no ambient spawn.
- `submit_summary` validation matches the phase table (required, empty,
  single line, rune cap after whitespace collapse); the holder keeps the
  last valid submission; a missing submission is an error after the run.
- `ParseSince` rejects zero and negative durations, `-` inside the tail,
  and falls through duration → RFC 3339 → local date as D4 states.
- `renderTranscript` keeps user messages and the last assistant message
  with text, skips system and tool roles, caps head-preserving.

### Review 3 — 2026-09-10 (`worklog-review`, holistic)

- [x] **R3-04 (Low)** — The package's silence tests are host-sensitive.
  `seedConfigDir` (`internal/summary/summarizer_test.go:29`) blanks
  `OPENROUTER_API_KEY` but neither `DEBUG`/`DEBUG_*` nor `CLAI_SUMMARIZER`.
  **Reproduced:** `DEBUG=1 go test ./internal/summary/ -run
  TestAgentSummarizer_isSideEffectFree` fails with the querier's
  `[DEBUG_CHAT]`/`ok:` chatter on stdout; a shell with `CLAI_SUMMARIZER=off`
  fails every constructor test. The repository convention
  (`internal/text/query_cmd_test.go`, `main_dirscope_e2e_test.go`) is to
  blank `DEBUG` in the fixture. Fix: blank `DEBUG`, `DEBUG_SUMMARY`,
  `DEBUG_CHAT`, `DEBUG_STOPLOSS` and `CLAI_SUMMARIZER` in `seedConfigDir`.
- [x] **R3-06 (Low)** — A paid, valid submission is discarded when the
  turn after it errors. `Summarize` (`internal/summary/summarizer.go:96`)
  returns the `TextQuery` error before consulting `holder`; a model that
  called `submit_summary` validly and then hit a vendor error on the
  closing text turn leaves the in-flight run unlabelled and the batch job
  reported failed although the label exists. The branch is uncovered. Fix:
  after the caller-cancel check, return the held label whenever
  `holder.get()` succeeds, and pin it through the `newQuerier` seam with a
  querier that submits then errors.
- [x] **R3-07 (Low)** — `summary-max-tool-calls` (four) is not the cost
  bound. The stoploss allows four submissions, refuses three more and then
  hard-stops, so a hostile run makes eight tool round trips
  (`TestAgentSummarizer_exhaustsAttempts` asserts exactly that), and every
  successful run costs at least two model calls because the querier does
  not stop on `accepted`. The README parameter row and
  `architecture/summaries.md` present the four as the attempt budget. Fix:
  state the real bound in both places, or stop the run on `accepted`.
- [x] **R3-15 (Low)** — `NewAgentSummarizer` branches without a test: an
  absent `textConfig.json` is created silently (`added` is nil for a fresh
  file) and the ladder's floor becomes `text.Default.Model`, so a fresh
  install's `chat summarize` targets OpenAI without any announcement;
  malformed JSON returns the unmarshal error (the "unreadable file" row
  uses a directory, a read error). The `ChatQuerier` assertion
  (`summarizer.go:88`) is unreachable and uncovered. Fix: add both rows to
  `TestNewAgentSummarizer_loadsConfig`; type the `newQuerier` seam as
  returning `models.ChatQuerier` or add a `Querier`-only fake.
- [x] **R3-16 (Low)** — `TestAgentSummarizer_isSideEffectFree` proves less
  than "persists nothing": `writePriceFiles` pre-seeds the per-model config
  that `NewQuerier` would otherwise write (the first use of a new `-sm`
  model does write to the config dir), and the fixture sets
  `CLAI_DISABLE_COST_ERR_LOG_GOROUTINE=1`, so the manager's error goroutine
  (routed through `CostWarnf` in production) never runs under the test.
  Fix: drop the env in this test (the goroutine's writer is now the trace,
  so the stdout swap no longer races) and word the phase text "nothing
  beyond the per-model config bootstrap". Fixed in phase 10; the phase's
  "persists nothing" reads as "nothing beyond the per-model config
  bootstrap".
- **R3-21 (Note)** — Observations, no change required: the transcript
  body is not escaped, so a user message containing `</transcript>` closes
  the block early (bounded by the validating tool); an empty transcript
  (image-only chat) still costs a model call, by the error table's design,
  and the `text == ""` skip for a user message is uncovered; the regex
  guard in `ParseSince` (`since.go:40`) is dead because the pattern always
  matches, and fractions are accepted only in standard units without the
  error text saying so.

Verified good (review 3): D27 ownership on every exit of `Summarize`
(the deferred cancel covers success, error, pre-cancel and panic
unwinding; `TestAgentSummarizer_stopEventDoesNotCancelCaller` is a real
proof because the runner reads the key from the child); the validator's
rows, last-valid-wins and the deterministic per-call holder; the head
preserving cap at exactly the input limit; the ladder order and the
`Purpose` skip in `lastRecordedModel`; `TestAgentSummarizer_querierConfig`
pins `Out`/`ErrOut` = `io.Discard`, `CostWarnf`, `SaveReplyAsConv`,
`SkipAmbientMcpServers` and the single tool; the mock's ordinal and
sequence handling; no test in the package can select a real vendor
(`Model: "test"` everywhere a querier is built, `CLAI_CONFIG_DIR` and
`OPENROUTER_API_KEY` blanked, `unknownModel` fails in `vendorType` before
any vendor `Setup`). Coverage `internal/summary` 94.9%, `internal/models`
100%.
