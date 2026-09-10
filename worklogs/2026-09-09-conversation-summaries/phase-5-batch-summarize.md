# Phase 5 — `chat summarize <window>`

> Superseded detail (D33, phase 8): the window is the positional argument
> (`clai chat summarize 7d`) and flags go before it; every `-since` below
> reads as that positional window, and the cited tests run the positional form.

**Status:** Complete (reopened by review 2; R2-01 and R2-02 fixed in [phase 8](./phase-8-review-2-fixes.md))

[← README](./README.md)

## Goal

Label existing history on request: filter the index to a window, confirm
the cost, run a bounded worker pool over the summarizer, and write the
index once.

## Specification

### Command

- `internal/chat/cmd.go`: the tree gains `"summarize|s"` with its own
  setup closure, `summarizeSetup(deps, cf, sf)`, shaped like
  `fullChatSetup` (config-touching: `deps.ConfigPrep` first) but
  distinct from it, because the shared `setChatQuerier` sees neither the
  summarize flags nor the summarizer. `chat.CommandDeps` gains
  `NewSummarizer func(confDir string) (models.Summarizer, error)` and
  `ParseSince`, both invoked only inside `summarizeSetup` after
  `ConfigPrep`. The closure builds the handler through `New` as today,
  then sets `ChatHandler.summarizer` and `ChatHandler.summarizeOptions`
  (the parsed `since` instant, `force`, `yes`, `workers`, `model`) on it
  before `c.SetQuerier`. No other verb sets either field. `main.go` wires
  `summary.NewAgentSummarizer` and `summary.ParseSince` and adds a usage
  example.
- `internal/flags.go`: a `SummarizeFlags` group (`Since StringFlag`,
  `Force BoolFlag`, `Yes BoolFlag`, `Workers IntFlag`, `SummaryModel StringFlag`)
  registered on the summarize sub only, sharing `-r`, `-n`, `-p` with the
  tree. `-since` is required.
- `chatUsage` and the sub help list the verb and its flags.

### Handler (`internal/chat/handler_summarize.go`)

1. The `-since` value was parsed in `summarizeSetup` through the injected
   `chat.CommandDeps.ParseSince` (`internal/chat` must not import
   `internal/summary`, which imports `internal/text`); a parse error fails
   setup naming the accepted forms and the handler never runs. The handler
   reads the instant from `summarizeOptions`.
2. Read the index; select rows where `effectiveUpdated() >= since`,
   `ID != "globalScope"`, and (`Summary == ""` or `-force`). Foreign rows
   never appear (D15).
3. Confirmation: print the count and the token estimate
   (`count × summary-input-runes ÷ summary-estimate-runes-per-token`, a stated
   upper bound on input tokens) and read one line through
   `table.ReadUserInputFrom(cq.input)`; `y`/`yes` proceeds. `-y` skips the
   prompt. `-n` without `-y` errors before touching anything. Zero rows
   prints a notice and exits zero.
4. Worker pool of `-workers` goroutines over a job channel. Every job
   runs on its own context: `jobCtx, jobCancel := context.WithCancel(ctx)`
   where `ctx` is the command context (the root context from `main.go`),
   with `jobCancel` stored under `utils.ContextCancelKey` on `jobCtx` and
   deferred (D27). The job loads the file, calls `Summarize(jobCtx, …)`
   with `Model` = the `-sm` value (empty lets the ladder run), stamps the
   three fields, appends `Summary.Queries`, and writes through
   `chat.SaveWithoutIndex`. Results (chat or error) flow to the
   coordinator on one channel.
5. The coordinator prints one line per result (`<id>: <title>` or
   `<id>: ERROR <err>`; `-r` prints one JSON object per line with `id`,
   `title`, `summary`, `error`), collects labelled chats, and on completion
   or on context cancellation calls `ChatHandler.upsertIndexBatch` (nil →
   `chat.UpsertChatIndexBatch`) once with every labelled chat. It then
   prints a totals line and returns a non-nil error when any job failed
   (exit status non-zero) or when cancelled.

### Per-job context table

| Scenario                                                                                   | Result                                                                                   | Test                                     |
| ------------------------------------------------------------------------------------------ | ---------------------------------------------------------------------------------------- | ---------------------------------------- |
| A fake summarizer invokes the cancel func it finds under `utils.ContextCancelKey` on its context (as the runner does on `StopEvent`) | Only that job's context is done; sibling jobs complete; the command context is not done; the command's own cancel func is never called | `TestHandleSummarize_jobContextsIsolated` |
| The command context is cancelled while a blocking fake holds several jobs                  | Every job's context is done; the handler returns a non-nil error; completed results flushed | `TestHandleSummarize_flushesOnCancel`     |
| The command context carries no cancel func under the key                                   | Jobs run; each job still carries its own func under the key                              | `TestHandleSummarize_jobContextsIsolated` |
| Real summarizer, several eligible rows, command context built like `main.go` builds the root context | Every row labelled; exit zero                                                  | `Test_e2e_chat_summarize_window`          |

### Index helpers (`internal/chat/chat.go`, `index.go`)

- `SaveWithoutIndex(convDir, chat)`: the body of `Save` minus
  `upsertChatIndex` (GroupKey stamp and sidecar kept).
- `UpsertChatIndexBatch(convDir, chats []Chat)`: one read, N upserts in
  memory, one write; each row stamped `Updated` as in phase 3.

### Limits

| Limit                       | Injectable                                   | Parameter                     | Trigger in test                                                                  | Test                                   |
| --------------------------- | -------------------------------------------- | ----------------------------- | -------------------------------------------------------------------------------- | -------------------------------------- |
| Concurrent summarizer calls | `ChatHandler.summarizeOptions.workers` (from `-workers`) | `-workers`         | Blocking fake counts concurrent entries; never above the flag                    | `TestHandleSummarize_workerBound`      |
| Index writes                | `ChatHandler.upsertIndexBatch`               | —                             | A counting func injected in the field; called exactly once with every labelled chat | `TestHandleSummarize_singleIndexWrite` |

## Integration contract

| Trigger                                                                                                       | Collaborators / fakes                                                     | Observable result                                                                                                      | Required side effects                                            | Prohibited side effects                                        |
| ------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------- | ---------------------------------------------------------------- | -------------------------------------------------------------- |
| Seed three conversations (two inside a 7d window whose user message holds `tool_submit_summary`, one older); `clai c summarize -since 7d -y -sm test` through `run`, whose context carries the process cancel func under `utils.ContextCancelKey` | Mock vendor; real summarizer; e2e temp config dir | stdout lists the two inside ids with `Mock title`; both files gain the fields and a `purpose: "summary"` row (the first job's completion did not cancel the second); the older file unchanged; exit zero | Index rewritten once, rows carry `title` | The older file untouched; `globalScope.json` untouched; no `dirs/` write |
| Rerun the same command                                                                                       | As above                                                                  | `0` selected notice; exit zero                                                                                          | None                                                             | No file rewritten                                              |
| Rerun with `-force`                                                                                          | As above                                                                  | Both inside ids listed again; `summary_at` newer than before                                                           | Files and index rewritten                                        | None                                                           |
| `clai -n c summarize 7d`                                                                              | As above                                                                  | Error naming `-y`; exit non-zero                                                                                        | None                                                             | No file touched                                                |
| `clai c summarize -y -sm gpt-does-not-exist 7d` with no vendor key in the environment                 | Real summarizer                                                           | Each job reports an error line; totals line; exit non-zero                                                             | A per-model config file for the unknown model may be created by `NewQuerier` (today's behavior) | No conversation file, index or mirror touched; `clai c l` still succeeds in the same dir |
| `clai c summarize -y` (no window)                                                                          | —                                                                         | Usage error naming `-since`; exit non-zero                                                                              | None                                                             | None                                                           |

## Acceptance criteria

| Outcome                                                              | Test                                                                                       |
| -------------------------------------------------------------------- | ------------------------------------------------------------------------------------------ |
| Flags register on the sub only; `-since` required                    | `TestSummarizeFlags_register` (`internal/flags_test.go`), `TestChatCommand_summarizeSub` (`internal/chat/cmd_test.go`) |
| Window filter from the index; globalScope and labelled rows excluded  | `TestHandleSummarize_selectsWindow`                                                        |
| Confirmation table (`y`, `n`, `-y`, `-n` without `-y`, zero rows)     | `TestHandleSummarize_confirmation`                                                         |
| Token estimate formula                                               | `TestHandleSummarize_tokenEstimate`                                                        |
| Worker bound and single index write                                  | `TestHandleSummarize_workerBound`, `TestHandleSummarize_singleIndexWrite`                  |
| Per-job context table                                                | `TestHandleSummarize_jobContextsIsolated`, `TestHandleSummarize_flushesOnCancel`, `Test_e2e_chat_summarize_window` |
| `summarizeSetup` parses `-since`, attaches the summarizer and options for this verb only | `TestChatCommand_summarizeSub`                                                       |
| Index flushed on cancellation with completed results                 | `TestHandleSummarize_flushesOnCancel`                                                      |
| Per-job errors reported; exit status non-zero; other jobs complete   | `TestHandleSummarize_reportsErrors`                                                        |
| `-force` regenerates; rerun is a no-op                               | `TestHandleSummarize_forceAndIdempotence`                                                  |
| Raw output is one JSON object per line                               | `TestHandleSummarize_rawOutput`                                                            |
| Helpers                                                              | `TestSaveWithoutIndex`, `TestUpsertChatIndexBatch` (`internal/chat/index_test.go`)         |
| Integration rows                                                     | `Test_e2e_chat_summarize_window`, `Test_e2e_chat_summarize_idempotent_and_force`, `Test_e2e_chat_summarize_noninteractive_needs_yes`, `Test_e2e_chat_summarize_vendor_error_leaves_list_working`, `Test_e2e_chat_summarize_since_required` (`main_summary_e2e_test.go`) |

Files: `internal/chat/cmd.go`, `internal/chat/cmd_test.go`,
`internal/chat/handler.go`, `internal/chat/handler_summarize.go`,
`internal/chat/handler_summarize_test.go`, `internal/chat/chat.go`,
`internal/chat/index.go`, `internal/chat/index_test.go`,
`internal/flags.go`, `internal/flags_test.go`, `main.go`,
`main_summary_e2e_test.go`.

## Error coverage

| Failure                                           | Expected outcome                                                           | Test                                          |
| ------------------------------------------------- | -------------------------------------------------------------------------- | --------------------------------------------- |
| Unparsable `-since`                               | Setup fails naming the accepted forms; the handler never runs; nothing touched | `TestChatCommand_summarizeSub`             |
| `NewSummarizer` errors                            | Command setup fails with the wrapped error                                  | `TestChatCommand_summarizeSub`                |
| A job's summarizer cancels the func under `utils.ContextCancelKey` | Only that job's context is affected; siblings and the command complete | `TestHandleSummarize_jobContextsIsolated`  |
| A conversation file fails to load                  | That job reports an error; others continue                                  | `TestHandleSummarize_reportsErrors`           |
| `Summarize` errors for one job                     | Reported; file untouched; others continue                                   | `TestHandleSummarize_reportsErrors`           |
| `SaveWithoutIndex` fails                          | Reported; the row is not included in the index flush                        | `TestHandleSummarize_reportsErrors`           |
| Context cancelled mid-run                          | In-flight jobs cancelled; completed results flushed; non-nil error returned | `TestHandleSummarize_flushesOnCancel`         |
| Index flush fails                                  | Warning; command error mentions the index; files already written stand      | `TestHandleSummarize_singleIndexWrite`        |

## Implementation notes

Session `session_01KXDFdab6MmuuKetC1EBowJ` (phase-5 worker), 2026-09-09.

Deviations and decisions:

- `summary-input-runes` now lives as `models.SummaryInputRunes`
  (`internal/models/summary.go`); `summary.InputMaxRunes` aliases it
  (one-line edit in the phase-2 file `internal/summary/tool.go`). The
  token estimate needs the cap and `internal/chat` cannot import
  `internal/summary`; the parameters table keeps phase 2 as the owner of
  the value, `internal/models` is the shared-interface home the README
  already assigns to cross-phase types.
- `-n` is read from `utils.Live`, the session global `Command.Setup`
  derives from `-n`; `summarizeOptions` carries exactly the five fields
  the parameters table names. Handler tests set `utils.Live` explicitly.
- Raw mode prints the totals as a JSON object
  (`{"selected","labelled","failed"}`) so `-r` stays one JSON object per
  line; the result lines carry `id`, `title`, `summary`, `error`.
- The coordinator skips the index flush when no job labelled anything,
  and `UpsertChatIndexBatch` is a no-op for an empty batch, so the
  vendor-error row's "index untouched" holds without a second writer.
- `summarizeOne` returns `ctx.Err()` before loading the file when the
  command context is already done; a cancelled run never opens files
  for jobs the feeder handed over in the same instant as the cancel.
- `summarizeSetup` runs `ConfigPrep` first (spec), then checks that
  `-since` is present (error names `-since` and the accepted forms),
  then `ParseSince`, then `NewSummarizer`, then `New`.
- `internal.IntFlag` gained `NewIntFlag` (mirror of `NewStringFlag`) for
  the `-workers` default. `go fix` rewrote the worker loop to `wg.Go`.
- E2e window row: `seedConv` stamps every index row at now, so the
  older row is moved outside the window through its file mtime and a
  cache rebuild (the pre-existing "Building cache index" line appears on
  stderr; the row asserts stdout and files, not stderr). The vendor-error
  row isolates `HOME` (the foreign claude-code scan otherwise added
  twenty seconds and rows) and asserts the native list row by its
  `| clai ` source cell, because the non-TTY raw list truncates the
  prompt column. The row's totals oracle reads `1 failed`.
- `chat`'s `Desc`, help examples, `chatUsage` and the `main.go` usage
  list the verb; the sub's help shows its flags.

Verification (repository root, host load 1.6–5.5):

- `go test ./internal/chat/ -race -count=1 -run 'TestHandleSummarize|TestSaveWithoutIndex|TestUpsertChatIndexBatch|TestChatCommand_summarizeSub|Test_Command_tree' -timeout=30s` → ok
- `go test ./internal/ -race -count=1 -run 'TestSummarizeFlags_register|Test_chatFlagsRegistration|Test_textFlagsRegistration' -timeout=30s` → ok
- `go test ./internal/summary/ -race -count=1 -timeout=30s` → ok
- `go test . -race -count=1 -run 'Test_e2e_chat_summarize' -timeout=30s` → ok (after two oracle edits recorded above)
- `go run mvdan.cc/gofumpt@latest -w -l .` → no files listed
- `go run honnef.co/go/tools/cmd/staticcheck@latest ./...` → exit 0
- `go vet ./...` → exit 0; `go fix ./...` → exit 0
- `go run github.com/mibk/dupl@latest -t 80 .` → 31 groups, none in
  `handler_summarize*.go`, `cmd*.go`, `chat.go`, `index.go`, `flags.go`
  or `main_summary_e2e_test.go`
- `internal/chat` at `-count=1 -cover`: 74.4% package-wide; per function
  `handleSummarize` 91.2%, `runSummarizeJobs` 100%, `summarizeOne`
  88.9%, `selectSummarizeRows` 100%, `confirmSummarize` 85.7%,
  `summarizeSetup` 89.5%, `UpsertChatIndexBatch` 81.8%,
  `SaveWithoutIndex` 100%
- `go test ./... -race -cover -count=3 -timeout=30s` (unedited,
  parallel) run one: exit 0, no FAIL or panic line. Run two (captured):
  exit 0, 45 packages `ok`, no FAIL or panic line; `clai` 22.112s
  56.7%, `internal` 80.6%, `internal/chat` 74.4%, `internal/summary`
  95.2%, `internal/text` 84.4%. No `-p 1` rerun was needed.

## Review findings

### Review 2 — 2026-09-10 (`worklog-review`, post-implementation)

- [x] **R2-01 (Medium)** — Concurrent workers race on the per-model
  vendor config file. Every job builds its own querier through
  `text.CreateQuerier`; `setupConfigFile`
  (`internal/text/querier_setup.go:157`) writes the default config with a
  plain `os.WriteFile` when the file is missing, and the cost manager's
  `storePriceScheme` (`internal/cost/manager.go:87`) rewrites the same
  file on a price-cache miss once the catalog answers. A sibling worker's
  `ReadAndUnmarshal` can observe the truncated file. **Reproduced:**
  sixteen concurrent `Summarize` calls on a config dir whose
  `mock_test_test.json` was removed failed in two of ten runs with
  `failed to setup config file: … failed to unmarshal file: unexpected end
  of JSON input` (also seen on the re-read after the default write:
  `failed to read default model`). **Scenario:** the first
  `clai c summarize -since … -sm <model never used before>` on the default
  four workers, or any run with `OPENROUTER_API_KEY` set and a cold price
  cache, reports spurious per-job errors, exits non-zero and leaves those
  rows unlabelled until a rerun. The phase's error table covers "a
  conversation file fails to load" and integration row five acknowledges
  the config-file write, but no row states what N concurrent constructions
  of the same model do; the contract missed the branch. Fix in phase 8:
  atomic write (temp file plus rename) in both writers, which are generic
  code, plus a regression test that runs several `Summarize` calls
  concurrently on a cold model config. Fixed in phase 8 (D31):
  `utils.WriteFileAtomic` in both writers; `TestSetupConfigFile_concurrentCold`,
  `TestStorePriceScheme_concurrent`, `TestAgentSummarizer_concurrentColdModelConfig`
  and `Test_e2e_chat_summarize_cold_model_config` pass under `-race`.
- [x] **R2-02 (Low)** — Raw mode is not one JSON object per line on every
  path: the zero-row notice (`handler_summarize.go:51`) and `aborted`
  (`handler_summarize.go:60`) print plain text under `-r`. A script that
  reruns `clai -r c summarize -since …` and parses stdout as JSON lines
  breaks on the idempotent rerun. Fix in phase 8: emit
  `{"selected":0,"labelled":0,"failed":0}` and an `{"aborted":true}` line
  under `-r`, covered by `TestHandleSummarize_rawOutput`. Fixed in
  phase 8 (`handler_summarize.go`, two raw branches; two new rows in
  `TestHandleSummarize_rawOutput`).
- **R2-07 (Note)** — `updated` is stamped at every upsert, so a label-only
  rewrite by the batch moves every labelled row to "updated now". This is
  what the parameters row specifies and it only affects later `-since`
  windows, which skip labelled rows anyway. No change.

Verified good:

- One worker pool, per-job `WithCancel` child owning the key, one result
  channel drained by the coordinator, index flushed once on completion
  and on cancellation, `globalScope` and labelled rows skipped,
  `-y`/`utils.Live` gate before any read, idempotent rerun, `-force`
  regenerates and appends a fresh `purpose: "summary"` row.
- `SaveWithoutIndex` and `Save` share one body; `UpsertChatIndexBatch`
  and `upsertChatIndex` share `upsertIndexRow`.
- The e2e window row runs the real summarizer with two concurrent jobs
  through the mock under `-race` and passes; it does not hit R2-01 because
  the fixture seeds the mock's config file.

### Review 3 — 2026-09-10 (`worklog-review`, holistic)

- [x] **R3-10 (Low)** — `writeChatIndex` (`internal/chat/index.go:300`)
  still writes with plain `os.WriteFile`. A Ctrl-C landing inside the
  batch flush truncates `chat_index.cache`, and the next `chat list` takes
  the "corrupted cache" full rebuild over the corpus, the cost D14 exists
  to avoid. D31 covered the per-model config, not this writer. Fix:
  `utils.WriteFileAtomic`.
- [x] **R3-11 (Low)** — `-workers 0` and negative values are silently
  clamped to one (`handler_summarize.go:150`), untested and undocumented;
  `-workers abc` is a flag error but `-workers -4` runs single-threaded.
  Fix: reject values below one in `summarizeSetup` with a usage error and
  one test row.
- [x] **R3-12 (Low)** — `-r` without `-y` writes the confirmation prompt
  (`handler_summarize.go:133`) on stdout ahead of the JSON lines, so
  R2-02's "one JSON object per line on every path" holds only with `-y`
  (the raw test works around it with `LastIndex("{")`). Fix: route the
  prompt to stderr under `-r`, or state that `-r` implies `-y`.
- **R3-21 (Note)** — Jobs pre-empted by a root cancel return
  `context.Canceled` and are counted and printed as failed in the totals;
  cancelled is not failed, but the exit status is non-zero either way.
  `ConfigPrep()` runs before the window is validated, so a bare
  `clai c s` still creates or migrates config before printing usage. The
  specification and integration rows above still spell the removed
  `-since` flag (fourteen mentions) although the cited tests run the
  positional form; tracked under R3-14.

Verified good (review 3): the window filter (`effectiveUpdated`,
`globalScope` skipped, boundary included), D15 by construction of the
index, the confirmation and its estimate, D27 per-job contexts, D24 single
writer with the cancel flush, every per-job error path excluded from the
flush with a non-zero exit, the worker bound, `-force` semantics and the
rerun no-op, raw JSON on the result, totals, zero-row and aborted paths,
`summarizeSetup` (exactly one window, the order hint, `NewSummarizer`
invoked only here). `runSummarizeJobs` 100%, `handleSummarize` 92.3%.
