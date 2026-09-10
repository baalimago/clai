# Conversation summaries — a generated title and summary per conversation

Every place clai labels a conversation today shows the **first user message**
and nothing else: the `chat list` row, the chat-info view, `chat dir`/`dirv2`,
the group row, and the `<recent_conversations>` block that lookback injects
into the agent prompt. The row renderer keeps the head and tail of that
message around a `...` infix; the lookback line keeps a fixed head of that
message (the literal the README names `lookback-preview-runes`). Neither says
what the conversation was about or how it ended.

This worklog adds a model-generated **title** and **summary** to each
conversation, produced by a one-off summarizer querier that submits its
result through a validating `submit_summary` tool. Generation runs **in
flight**, concurrently with the main model call on the first persist of a
conversation, and **in batch** through `clai chat summarize <window>`
for existing history. It closes the in-process gaps in `internal/text` that
would otherwise make a one-off querier inside the CLI process a regression.

## Status board

| #   | Phase                                                               | Status      | Summary                                                                                                                                                  |
| --- | ------------------------------------------------------------------- | ----------- | -------------------------------------------------------------------------------------------------------------------------------------------------------- |
| 1   | [In-process querier gaps](./phase-1-in-process-querier-gaps.md)     | Complete    | `SkipAmbientMcpServers`, cost enrichment decoupled from persistence, `QueryCost.Purpose`, the cmd-ban policy carried on the tool-call context (global deleted); a querier that persists nothing, starts no ambient server and alters no other run's policy |
| 2   | [`internal/summary` domain](./phase-2-summary-domain.md)            | Complete (review 2: R2-03, R2-04 fixed in phase 8) | `submit_summary` tool with validation and retry, transcript rendering, summarizer prompt, querier-backed `Summarizer`, model ladder, `ParseSince`, mock support |
| 3   | [Model and index fields](./phase-3-model-and-index.md)              | Complete    | `title`, `summary`, `summary_at` on `Chat`; `title`, `summary`, `updated` on the index row; the globalScope mirror and the `-re` path preserve them       |
| 4   | [In-flight generation](./phase-4-in-flight-generation.md)           | Complete    | Launch on an isolated context alongside the main call, display, bounded join, persist once; opt-out and model override; usage recorded; failure silent  |
| 5   | [`chat summarize <window>`](./phase-5-batch-summarize.md)             | Complete (reopened by review 2; fixed in [phase 8](./phase-8-review-2-fixes.md)) | Index-filtered window, confirmation with count and token estimate, bounded worker pool, single index writer, idempotent unless `-force`                  |
| 6   | [Surfaces](./phase-6-surfaces.md)                                   | Complete    | `labelFor` on every surface: `chat list` row and group row, chat info `title:`/`summary:` lines, `chat dir`/`dirv2` JSON keys and pretty lines, lookback `title: summary`; 5 e2e rows |
| 7   | [Docs and quality-gate sweep](./phase-7-docs-and-gates.md)          | Complete    | `architecture/summaries.md`, seven architecture docs updated, `Summary.ApplyTo` consolidation; every gate green unedited, 45 packages `ok`, 31 pre-existing dupl groups |
| 8   | [Review 2 fixes (addendum)](./phase-8-review-2-fixes.md)            | Complete (reopened by review 3: R3-02, R3-03; fixed in [phase 10](./phase-10-review-3-fixes.md)) | Atomic per-model config writes (R2-01), raw JSON on every batch path (R2-02), summarizer querier off stderr without seeded prices (R2-03), batch-path upgrade announcement (R2-04), one-line notes |
| 9   | [Summarize progress board](./phase-9-summarize-progress-board.md)   | Complete (reopened by review 3: R3-01; fixed in [phase 10](./phase-10-review-3-fixes.md)) | `internal/board` extracted from the audio transcribe board (`Log` added); audio flows adapted unchanged; `chat summarize` draws one row per worker on a terminal, logs results above, keeps plain/`-r` output for pipes |
| 10  | [Review 3 fixes (addendum)](./phase-10-review-3-fixes.md)           | Complete    | Board truncates before colouring (R3-01), `chat list` prompt fits the width (R3-02), cost manager warnings through the seam (R3-03), fixture hygiene, one-line Lows |

**Phase order.** Phases 1 and 3 are independent and can run in parallel.
Phase 2 depends on 1. Phases 4 and 5 depend on 2 and 3 and are independent
of each other. Phase 6 depends on 3 and reads nothing from 4 or 5. Phase 7
runs last; phase 8 is the review-2 addendum and runs after it; phase 9
(progress board) is the maintainer's UI request and depends on 5; phase
10 is the review-3 addendum and runs after 9.

**Next eligible work:** none — phases 1–10 are Complete. Review round
3 (`worklog-review`, holistic, 2026-09-10) found R3-01..R3-03 (Medium)
plus fourteen Low and four Note items; phase 10 fixed every Medium and
Low the same day with the unedited gates green (forty-six packages
`ok`). Residual: the table library never measures a wrapped prompt
line, so a paginated `chat list` below about ninety-one columns can
still drift (pre-feature). Next: `worklog-review` round 4 over phase 10,
then the maintainer commits and releases (agents never commit). README
signed off 2026-09-09 (review round 1 plus D26); revised the same day for
validation rounds 1–3; revised 2026-09-10 for review round 2 (D31), the
phase-8 execution, D32/D33, phase 9, review round 3 and phase 10.

### Invariants (non-negotiable)

| Invariant                                                                                                       | Mechanism                                                                                                                                   | Owner       | Test |
| --------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------- | ----------- | --- |
| `chat list`, `chat dir`, `chat dirv2`, `chat continue` need no API key                                          | The `chat` tree receives a lazy `NewSummarizer` constructor by injection and only `chat summarize` invokes it                               | Phase 5     | Existing list/dir/continue e2e; `Test_e2e_chat_summarize_vendor_error_leaves_list_working` |
| The summarizer never persists a conversation, never writes `globalScope.json`, never touches a dirscope binding | `Configurations.SaveReplyAsConv: false` on the summarizer querier                                                                           | Phase 2     | `TestAgentSummarizer_isSideEffectFree` |
| The summarizer never disables the chat index of the host process                                                | The querier is built through `text.CreateQuerier`, never `agent.Setup`; `chat.SkipIndex` is never written                                   | Phase 2     | `TestAgentSummarizer_isSideEffectFree` |
| The summarizer starts no ambient MCP server and calls exactly one tool, `submit_summary`                        | `Configurations.SkipAmbientMcpServers`, `Tools: [submit_summary]`, no tool globs                                                            | Phases 1, 2 | `TestSetupMcpManager_skipAmbient_startsNoConfigDirServer`, `TestAgentSummarizer_isSideEffectFree` |
| A run's command-ban policy lives on its tool-call context; constructing any querier alters no other run's policy | `Configurations.CmdBan` → `Querier.cmdBan` → `pkgtools.WithCmdBanContext` at the tool executor's single invoke site; the package global and its setter are deleted (D29) | Phase 1     | `TestToolExecutor_attachesCmdBanPolicy`, `TestToolExecutor_secondQuerierDoesNotAlterPolicy` |
| The summarizer never writes to the host's stdout; on its success path it writes nothing to stderr               | `Configurations.Out` and `ErrOut` are `io.Discard`, `CostWarnf` routes cost warnings to the trace (phase 8); e2e asserts clean streams with and without a seeded price | Phases 2, 4, 8 | `TestAgentSummarizer_isSideEffectFree`, `TestAgentSummarizer_coldPriceIsSilent`, `Test_e2e_query_labels_in_flight`, `Test_e2e_query_labels_in_flight_cold_price` |
| No in-flight summarizer failure of any kind (constructor error, model error, timeout, panic, interrupt) fails, delays beyond the join bound, or alters the answer or the persistence of the main query | Constructor failure attaches no summarizer; the goroutine recovers panics into errors; bounded join after display; every failure is dropped and only traced under `DEBUG_SUMMARY` (D26) | Phase 4     | `TestFinalize_join`, `TestQueryCommand_newSummarizerError` |
| The summarizer's own `StopEvent` never cancels the caller's context, on any path                                | `Summarize` runs its querier on `context.WithCancel(caller ctx)` with that child's cancel stored under `utils.ContextCancelKey` (D27)        | Phase 2     | `TestAgentSummarizer_stopEventDoesNotCancelCaller` |
| The summarizer can neither be cancelled by, nor cancel, the main call's normal completion                       | The launcher hands `Summarize` a `context.WithoutCancel(run ctx)` child; the key isolation above keeps the summarizer's stop local          | Phase 4     | `TestSummaryContext_isolation`, `TestQuery_stopEventDoesNotCancelSummary` |
| Every batch job runs on its own child of the command context; a job's completion cancels no sibling, a root cancel reaches every job | Per-job `context.WithCancel(command ctx)` with the job's cancel stored under `utils.ContextCancelKey` before `Summarize` (D27)  | Phase 5     | `TestHandleSummarize_jobContextsIsolated`, `TestHandleSummarize_flushesOnCancel` |
| An interrupt abandons the summarizer                                                                            | External cancel before completion (`!session.SawStopEvent`) skips the join; the join also selects on an injectable interrupt channel        | Phase 4     | `TestFinalize_join` (interrupt rows) |
| A generated title and summary are never regenerated on the query path                                           | Launch only when `InitialChat.Summary` is empty (D3); replies and forks preserve the stored fields (D20, D28)                                | Phases 3, 4 | `TestQuery_launchConditions`, `Test_e2e_dirreply_does_not_relabel` |
| A reply never drops a stored title or summary; a `-re` fork inherits its parent's                               | `globalScope.json` mirrors the fields; the `-re` branch of `SetupInitialChat` adopts them into the fork; `-dre` loads the full file          | Phase 3     | `TestSetupInitialChat_replyAdoptsFields`, `Test_e2e_reply_preserves_title_summary`, `Test_e2e_dirreply_preserves_title_summary` |
| Summary usage is visible in cost and token columns and never masquerades as the conversation's model            | Summary `QueryCost` rows carry `Purpose: "summary"`; index model resolution skips them                                                       | Phases 1, 4 | `TestChatIndexRowFromChat_modelSkipsSummaryRows`, `TestAgentSummarizer_queriesCarryUsage` |
| The batch command is idempotent                                                                                 | Rows with a non-empty `summary` are skipped unless `-force`                                                                                 | Phase 5     | `TestHandleSummarize_forceAndIdempotence`, `Test_e2e_chat_summarize_idempotent_and_force` |
| The batch command rewrites the index once, from one writer                                                      | Workers use `chat.SaveWithoutIndex`; the coordinator calls `chat.UpsertChatIndexBatch` on completion and on cancellation                    | Phase 5     | `TestHandleSummarize_singleIndexWrite` |
| The test suite never builds the real summarizer through the CLI unless a fixture opts in; `CLAI_SUMMARIZER=off` disables it in any process | Root `TestMain` swaps `main.go`'s `newSummarizer` for a refusing constructor, restored only by `setupSummaryE2E`; `summary.allowed()` honours `off` (D32) | Phase 8     | `Test_e2e_summarizer_guard`, `TestSummarizerGuard` |
| Concurrent in-process queriers never observe a torn per-model config file                                        | Every writer of `<vendor>_<model>_<version>.json` (`setupConfigFile`, `storePriceScheme`) writes through `utils.WriteFileAtomic` (temp + rename) (D31) | Phase 8     | `TestSetupConfigFile_concurrentCold`, `TestStorePriceScheme_concurrent`, `TestAgentSummarizer_concurrentColdModelConfig` |

### Shared interfaces

Defined in `internal/models` (phase 2 owns them; phases 4 and 5 consume
them):

```go
type SummaryRequest struct {
    Chat  pub_models.Chat
    // Model is the explicit model to use. Empty lets the summarizer resolve
    // it: config summary-model → the conversation's last recorded model →
    // the text config model (D22).
    Model string
}

type Summary struct {
    Title       string
    Summary     string
    Model       string                 // the model that produced the result
    Queries     []pub_models.QueryCost // one row per summarizer run, Purpose "summary", cost when the catalog was ready
    GeneratedAt time.Time              // stamped by the summarizer; provenance only, never a staleness index (D16)
}

type Summarizer interface {
    Summarize(ctx context.Context, req SummaryRequest) (Summary, error)
}
```

`internal/summary.NewAgentSummarizer(confDir string) (models.Summarizer, error)`
is the production implementation; it loads `textConfig.json` once at
construction for the model ladder. Tests in phases 4 and 5 use fakes that
return instantly, block until cancelled, or error.

Injection points, both set in `main.go`:

- `text.QueryCommandDeps.NewSummarizer func(confDir string) (models.Summarizer, error)` —
  invoked in the query command's `OnSetup` after `SetupQuerier`, and attached
  through `Querier.SetSummarizer(models.Summarizer)` (the `SetChatID` /
  `applyDirReplyChatID` pattern; `SetupQuerier` owns its `Configurations`, so
  the value cannot be placed there first).
- `chat.CommandDeps.NewSummarizer func(confDir string) (models.Summarizer, error)` —
  invoked only by `chat summarize`.

`text.Configurations` carries `SummarizeConversations` and `SummaryModel` as
JSON config fields and `SummaryJoinTimeout` as a `json:"-"` field (zero
means the README default), so `NewQuerier` copies them onto the querier the
way it copies `UseLookback`.

**Model resolution ladder (D22).** `-sm` flag → config `summary-model` →
the conversation's last recorded model (`Queries`, newest non-empty
`Model`, the rule `chatIndexRowFromChat` uses) → text config `model`. The
query path resolves the first two and passes the run's own model as the
third, so `SummaryRequest.Model` is always non-empty there. The batch path
passes the flag value and lets the summarizer apply the rest.

### The summarizer querier

Built per call in `internal/summary` through `text.CreateQuerier` with a
`Configurations` holding: the resolved model, the fixed summarizer system
prompt, `UseTools: true`, `Tools` holding only `submit_summary`, no tool
globs, `SkipAmbientMcpServers: true`, `SaveReplyAsConv: false`,
`MaxToolCalls: summary-max-tool-calls`, `Out: io.Discard`, `ConfigDir`
verbatim, an empty `CmdBan` (its only tool is not a shell tool, and since
D29 the policy is per run), and an `AgentSettings.UsageRecorder` that
captures every model step. The querier runs on a child of the caller's context created with
`context.WithCancel`, whose cancel func `Summarize` stores under
`utils.ContextCancelKey` on that child and calls on return: the runner's
`StopEvent` cancel reaches only the child, and a cancel of the caller's
context still reaches the querier (D27). Its input is the conversation's user messages plus the last assistant
message when one exists, rendered into **one user message** (role-tagged
lines inside a `<transcript>` block, text parts only, no tool or system
messages), capped at `summary-input-runes` runes head-preserving, followed
by the submission instruction. The tool validates and stores the result in a
per-call holder; the last valid submission wins; a missing submission after
the run is an error. `Summary.Queries` is the querier chat's enriched
`Queries` when present, else one row synthesized from the recorded usage
with zero cost; every row is stamped `Purpose: "summary"`.

### In-flight lifecycle (phase 4)

1. `Querier.Query` launches the summarizer before the runner starts when
   all hold: a summarizer is attached, `SummarizeConversations` is on,
   `ShouldSaveReply` is on, and `InitialChat.Summary` is empty. The
   summarizer receives a copy of the initial chat (system message excluded
   by the renderer) on a context derived from the run context with
   `context.WithoutCancel` and then `context.WithCancel`; the launcher
   keeps that cancel func to abandon the run. The cancel-key isolation is
   the summarizer's own (D27), so the run context's cancel func is never
   visible to the summarizer's runner.
2. The main call streams as today.
3. The finalizer: on a failed or empty run, or when the run context was
   cancelled without a `StopEvent` (interrupt), it cancels the summarizer,
   persists immediately and returns. Otherwise it prints the answer, then
   joins the summarizer for at most `summary-join-timeout`, also giving up
   on the injectable interrupt channel; a result stamps `Title`, `Summary`,
   `SummaryAt` and appends `Summary.Queries`; then it persists once.
4. A summarizer that is still running after the join is cancelled and
   forgotten; the process exit reaps it.

### Display precedence

Every surface renders `title` when non-empty, else the first user message
with today's truncation. The lookback block renders `title: summary` when a
title is present (the summary may be empty), else today's
`lookback-preview-runes` head of the first message. Foreign rows have no
title source and keep their first-message label.

### Severity taxonomy

| Severity | Meaning                                                              |
| -------- | -------------------------------------------------------------------- |
| High     | Breaks an invariant above or a repository QA gate                    |
| Medium   | Wrong behavior in a specified scenario without breaking an invariant |
| Low      | Naming, docs, or a test that proves less than its criterion claims   |
| Note     | Observation; no change required                                      |

High and Medium reopen the phase they concern (or route to an addendum
phase that the board row points at). Low is fixed in place or deferred by
the maintainer without changing the phase status. Note never reopens.

## Parameters and owners

| Parameter                                                                          | Default                                                                            | Owner   |
| ---------------------------------------------------------------------------------- | ---------------------------------------------------------------------------------- | ------- |
| `Configurations.SkipAmbientMcpServers` (`json:"-"`)                                | false                                                                              | Phase 1 |
| `QueryCost.Purpose` (`json:"purpose,omitempty"`; `"summary"` for summarizer rows)  | ""                                                                                 | Phase 1 |
| `Querier.cmdBan` (copied from `Configurations.CmdBan`; attached with `pkgtools.WithCmdBanContext` at the executor) | inherits `Configurations.CmdBan` (empty)                              | Phase 1 |
| `title-max-runes`                                                                  | 60                                                                                 | Phase 2 |
| `summary-max-runes`                                                                | 240                                                                                | Phase 2 |
| `summary-input-runes` (input cap)                                                  | 2000                                                                               | Phase 2 |
| `summary-max-tool-calls` (accepted submissions; up to eight tool round trips, at least two model calls per label, R3-07) | 4                                                                                  | Phase 2 |
| `since` accepted forms                                                             | extended duration (`s`, `m`, `h`, `d`, `w`), RFC 3339, `YYYY-MM-DD` local midnight | Phase 2 |
| `submit_summary` tool name                                                         | `submit_summary`                                                                   | Phase 2 |
| Env `CLAI_MOCK_SUMMARY_TITLES` / `CLAI_MOCK_SUMMARY_SUMMARIES` (mock vendor, `\|`-separated sequence; nth call uses nth entry, last repeats) | `Mock title` / `Mock summary.`                        | Phase 2 |
| `models.Summarizer`, `models.SummaryRequest`, `models.Summary`                     | —                                                                                  | Phase 2 |
| `summary.NewAgentSummarizer`, `summary.ParseSince`                                 | —                                                                                  | Phase 2 |
| `Chat.Title`, `Chat.Summary`, `Chat.SummaryAt` (`time.Time`, `omitzero`)           | —                                                                                  | Phase 3 |
| Index row `title`, `summary`, `updated` (stamped at upsert; rebuild uses file mtime; zero falls back to `created`) | —                                                                 | Phase 3 |
| Config `summarize-conversations` (text config, bool)                               | true                                                                               | Phase 4 |
| Flag `-summarize` (bool, overrides config for the run)                             | inherits config                                                                    | Phase 4 |
| Config `summary-model` (text config, string; empty = ladder, `migrate:"true"`, no `omitempty` so the filled empty value survives the rewrite) | ""                                                          | Phase 4 |
| Flag `-sm` / `--summary-model` on `query` (completes from model history like `-cm`) | inherits config                                                                   | Phase 4 |
| `summary-join-timeout` (`Configurations.SummaryJoinTimeout`, `json:"-"`)           | 5s                                                                                 | Phase 4 |
| `Querier.SetSummarizer`, `Querier.summaryInterrupt` (injectable `<-chan struct{}`; nil wires SIGINT/SIGTERM) | —                                                        | Phase 4 |
| `text.QueryCommandDeps.NewSummarizer`                                              | —                                                                                  | Phase 4 |
| `DEBUG_SUMMARY` (debugflags switch; also on under plain `DEBUG`)                   | off                                                                                | Phase 4 |
| Positional `<window>` on `chat summarize` (required; flags go before it, stdlib flag order) — replaced flag `-since` (D33) | none                                                    | Phase 8 |
| Flag `-force` on `chat summarize`                                                  | false                                                                              | Phase 5 |
| Flag `-y` on `chat summarize` (skip confirmation)                                  | false                                                                              | Phase 5 |
| Flag `-workers` on `chat summarize`                                                | 4                                                                                  | Phase 5 |
| Flag `-sm` / `--summary-model` on `chat summarize`                                 | "" (ladder)                                                                        | Phase 5 |
| `summary-estimate-runes-per-token` (token estimate divisor for the confirmation)   | 4                                                                                  | Phase 5 |
| `chat.CommandDeps.NewSummarizer`, `chat.CommandDeps.ParseSince` (the `chat` tree cannot import `internal/summary`) | —                                                                  | Phase 5 |
| `ChatHandler.summarizer` (set only for the summarize verb)                         | nil                                                                                | Phase 5 |
| `ChatHandler.summarizeOptions` (`since`, `force`, `yes`, `workers`, `model`; set only for the summarize verb) | zero                                                    | Phase 5 |
| `ChatHandler.upsertIndexBatch func(convDir string, chats []pub_models.Chat) error` (nil → `chat.UpsertChatIndexBatch`) | nil                                            | Phase 5 |
| `chat.SaveWithoutIndex`, `chat.UpsertChatIndexBatch`                               | —                                                                                  | Phase 5 |
| `lookback-preview-runes` (existing fallback)                                       | 80                                                                                 | Phase 6 |
| `utils.WriteFileAtomic`                                                            | —                                                                                  | Phase 8 |
| `Configurations.CostWarnf` (`json:"-"`; nil → `ancli.Warnf`; cost manager and enricher warnings) | nil                                                                   | Phase 8 |
| `Configurations.ErrOut` (`json:"-"`; nil → `os.Stderr`; the MCP log sink's stream)   | nil                                                                                | Phase 8 |
| Env `CLAI_SUMMARIZER` (`off` refuses the real summarizer in any process; operator kill switch)   | unset                                                                              | Phase 8 |
| `main.go` `newSummarizer` (package var wired into both trees; the root `TestMain` swaps in a refusing constructor, `setupSummaryE2E` restores it) | `summary.NewAgentSummarizer` | Phase 8 |
| `ChatHandler.forceLive`, `ChatHandler.boardWidth`, `ChatHandler.now` (summarize board test seams)     | false, 0, nil (terminal probe, real clock)                                         | Phase 9 |

## Readiness checklist

Run by the author before requesting validation; outcome recorded in the
session journal.

1. No numerals in phase files outside integration-contract oracle rows:
   `grep -nE '(^|[^0-9])[0-9]+ ?(s|ms|runes|workers|calls|%)\b' phase-*.md`
2. Every test name is declared in exactly one phase and one file list.
3. Every config field, flag, and injectable field has one owner in the
   parameters table.
4. Every invariant and limit is a table with a test per row (the README
   invariants table carries a Test column; the validation table in phase
   2; the join table in phase 4).
5. Any phase mentioning listening, manual, or paid steps has a
   `Human required` subsection. (Target: none — see Validation policy.)
6. No phase references text scheduled for deletion.
7. New conventions do not contradict existing code conventions — checked
   against `internal/text/setup_querier.go` (`applyDirReplyChatID` setter
   pattern), `internal/chat/cmd.go` and `internal/text/cmd.go` (deps
   injection), `pkg/tools/bash_tool_date.go` (tool shape),
   `internal/flags.go` (flag registration), `internal/vendors/mock.go`
   (env-scripted tool inputs), `internal/debugflags` (debug switches),
   `pkg/tools/cmd_ban.go` and `internal/tools/handler.go` (context-carried
   policy, `contextualTool`), and `pkg/agent/run.go` (the one remaining
   `WithCmdBanContext` caller, removed by D29).

## Decisions

| ID  | Date       | Decision                                                                                                                       | Rationale                                                                                                                                                                                                       | Replaces                         |
| --- | ---------- | ------------------------------------------------------------------------------------------------------------------------------ | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | -------------------------------- |
| D1  | 2026-09-09 | Both in-flight and batch generation (option C)                                                                                 | A label only on request is absent when `chat list` is opened; batch is the only path for history                                                                                                                | —                                |
| D2  | 2026-09-09 | Batch takes `-since`, never `--all`                                                                                            | The corpus is hundreds of megabytes; a window bounds cost and time                                                                                                                                              | —                                |
| D3  | 2026-09-09 | Generate once, never refresh on the query path                                                                                 | The first query characterizes the conversation; divergence is the user's problem; `-force` regenerates on demand                                                                                                | —                                |
| D4  | 2026-09-09 | `-since` parses an extended duration first, then RFC 3339, then a date                                                         | Idiomatic Go durations plus `d`/`w`; absolute forms for "since Monday"                                                                                                                                          | —                                |
| D5  | 2026-09-09 | Output is a title and a two-sentence summary (option B)                                                                        | Rows need a title; lookback and chat info benefit from the outcome                                                                                                                                              | —                                |
| D6  | 2026-09-09 | Surfaces include `chat dir` and `dirv2` JSON with optional keys, no version bump                                               | Additive `omitempty` fields do not break scripts on the v2 record                                                                                                                                                | —                                |
| D7  | 2026-09-09 | Summarizer injected through `CommandDeps` from `main.go`                                                                       | Keeps the `chat` tree vendor-free and avoids the `internal/text` → `internal/chat` import direction                                                                                                              | —                                |
| D8  | 2026-09-09 | In-flight: launch with the main call, join in the finalizer with a bounded wait (Q5 option A)                                  | Near-zero tail in practice; short one-shot queries still get labelled; a hung vendor cannot hold the prompt                                                                                                     | —                                |
| D9  | 2026-09-09 | Summarizer built on clai's tool loop, result submitted through a validating `submit_summary` tool                              | Tool loop gives multiple rounds and vendor-agnostic constraint enforcement; `ResponseFormat` covers two vendors only                                                                                             | typed JSON extraction (rejected) |
| D10 | 2026-09-09 | Close the `pkg/agent` gaps with options, defaults unchanged for library consumers                                              | `SaveReplyAsConv` and `SkipIndex` are correct for embedded use and wrong inside the CLI                                                                                                                         | superseded by D23                |
| D11 | 2026-09-09 | Summary model defaults to the conversation's model; override by config key and flag                                            | Never leaks a local-model chat to a second vendor; follows the config-field-plus-flag rule                                                                                                                      | —                                |
| D12 | 2026-09-09 | In-flight generation on by default with an opt-out                                                                             | A feature nobody enables leaves `chat list` as it is; each new conversation costs one extra small call                                                                                                          | —                                |
| D13 | 2026-09-09 | Summary usage appended to `Queries`                                                                                            | Cost and token columns stay truthful                                                                                                                                                                            | —                                |
| D14 | 2026-09-09 | Index gains `updated` without a version bump; zero falls back to `created`                                                     | Avoids a full rebuild of the corpus; only pre-feature chats continued before the feature can be misplaced                                                                                                       | —                                |
| D15 | 2026-09-09 | Batch covers native conversations only; un-cloned foreign (Claude Code) rows are skipped                                       | Un-cloned foreign rows are not clai files and never enter the index; a cloned foreign chat is a native file with `Source` set and is eligible                                                                     | —                                |
| D16 | 2026-09-09 | No message-count or index field records what a summary covered; `summary_at` (`time.Time`) is provenance only                  | Conversations are edited in place, so any index into `Messages` drifts (`QueryCost.MessageTrigger` has the same weakness); a future refresh policy would use a content hash like `GroupKey`                     | `summary_covers` (int)           |
| D17 | 2026-09-09 | The summarizer runs on an isolated context; the finalizer distinguishes interrupt from normal completion by `SawStopEvent` and the join watches an injectable interrupt channel | The runner cancels the root context on every `StopEvent`; sharing it would cancel the summarizer on success and let the summarizer cancel the main call                                | "shared run context" (F2)        |
| D18 | 2026-09-09 | `Configurations.SkipAmbientMcpServers` skips config-dir MCP discovery; explicit `McpServers` still start                       | `setupTooling` otherwise spawns every configured server for a run that may call one tool                                                                                                                        | —                                |
| D19 | 2026-09-09 | Cost enrichment runs whenever the chat has usage; persistence is a separate gate. Summary rows carry `QueryCost.Purpose = "summary"` and index model resolution skips them | A non-persisting querier must still account; a summary row appended last must not become the row's model                                                             | —                                |
| D20 | 2026-09-09 | `globalScope.json` mirrors `title`/`summary`/`summary_at`; the `-re` branch of `SetupInitialChat` adopts them alongside `Created` | Otherwise the `-re` fork starts without them and the launcher regenerates a label for the parent's conversation plus one turn                                                                              | —                                |
| D21 | 2026-09-09 | Finalizer success path becomes display → join → persist; failure and interrupt paths persist immediately without a join        | The definition of success requires the answer before any summary wait; one persist keeps the file, the index and the mirror consistent                                                                          | —                                |
| D22 | 2026-09-09 | One model ladder: flag → config `summary-model` → conversation's last recorded model → text config `model`; the summarizer applies it when `SummaryRequest.Model` is empty | The `chat` tree cannot read the text config; the query path already holds every rung                                                                                                     | —                                |
| D23 | 2026-09-09 | The summarizer builds its querier directly through `text.CreateQuerier`; `pkg/agent` is untouched                              | `pkg/agent` has four in-process gaps, not two; closing them would add four public options for one internal consumer; the tool loop D9 wants is the querier itself                                               | D10                              |
| D24 | 2026-09-09 | Batch workers save through `chat.SaveWithoutIndex`; the coordinator is the single index writer and flushes on completion and on cancellation | `Save` rewrites the whole index per call and concurrent upserts lose updates                                                                                                                   | —                                |
| D25 | 2026-09-09 | An in-flight summary failure is silent; `DEBUG_SUMMARY` traces it; only `chat summarize` reports errors                        | Models without tool calling would otherwise warn on every query; the batch run is the user's explicit request                                                                                                   | "warned once through ancli"      |
| D26 | 2026-09-09 | Absolute isolation of the in-flight summarizer: no failure class may reach the main run's answer, exit status or persistence; logging is the only permitted effect | Maintainer instruction at sign-off; a labelling convenience must never cost a query                                                                                                          | —                                |
| D27 | 2026-09-09 | Context ownership on every path: `Summarize` installs its own cancel func under `utils.ContextCancelKey` on a `WithCancel` child of the caller's context; the in-flight launcher detaches with `WithoutCancel`; every batch job runs on its own `WithCancel` child of the command context, stemming from the root context | The command context is the root context and carries the process cancel func; a batch worker's `StopEvent` would otherwise cancel every sibling (V1-01). Maintainer instruction: every batch job isolated, still rooted so an interrupt reaches all | D17 (scope widened) |
| D28 | 2026-09-09 | A plain `-re` fork inherits its parent's `title`, `summary` and `summary_at` from the mirror; the launcher does not fire for it | `-re` forks a new file (the mirror carries `ID: globalScope`); the fork is the parent plus one turn, the first query already characterizes it (D3), and `GroupKey` groups the two rows in `chat list` | —                                |
| D29 | 2026-09-09 | The command-ban policy is carried only on the tool-call context: `NewQuerier` keeps `Configurations.CmdBan` on the querier, the tool executor attaches it with `pkgtools.WithCmdBanContext` at its single invoke site, `pkg/agent/run.go` stops attaching its own copy, and `cmdBanList`, `SetCmdBanList` and `ResetCmdBanListForTests` are deleted. A context without a policy is permissive, so `FreetextCmdTool.Call` (no context) enforces nothing and says so | `NewQuerier` wrote a process global on every construction, so any second in-process querier cleared the main run's bans (V2-01). Maintainer instruction: remove the package-scoped state rather than add a switch that skips writing it. The context path already exists end to end; deleting the global leaves one owner (`Configurations.CmdBan`) and one attachment point. Removing the exported setter is a compile-time break for library callers, which is preferable to a silently permissive no-op | "process-global ban list" (worklog 2026-08-02-cmd-ban-list, D17) |
| D30 | 2026-09-09 | Phase 3 integration row three and error row two corrected to observed behavior: `chat list` is structurally read-only (`readOnlyChatSetup` sets `utils.NoCreateConfig`) and never persists a rebuilt cache, so the row's persisted rebuild is proven through the next `chat.Save`; `LoadGlobalScope` normalizes any mirror id to `globalScope`, so a hand-edited mirror id still forks | Both were planning errors found by the phase-3 worker; the observables the rows care about (title/summary mirrored, `updated` from mtime, fields adopted on `-re`) are unchanged and proven by the same tests | — |
| D31 | 2026-09-10 | Every writer of a per-model config file writes atomically (temp file plus rename): `setupConfigFile` and the cost manager's `storePriceScheme`. No mutex in the summarizer; querier construction stays concurrent | Review 2 reproduced R2-01: with N batch workers building N queriers, `os.WriteFile` truncates the file a sibling is reading, and the job fails with a JSON error. The file is the only shared state; an atomic rename removes the tear for every present and future in-process querier, whereas a summarizer-local mutex would not cover the cost manager's later asynchronous write | — |
| D32 | 2026-09-10 | Running the test suite never builds the real summarizer through the CLI unless a fixture opts in: `main.go` wires the constructor through a package variable, the root test binary's `TestMain` replaces it with a refusing constructor and only `setupSummaryE2E` restores the real one. `CLAI_SUMMARIZER=off` is a separate operator kill switch honoured at the constructor. No production code knows about tests | Maintainer instruction: tests must never trigger a summarization that costs money, and a `testing.Testing()` sniff in production code (the first cut) is a hack. The real constructor reaches the CLI only by injection from `main.go`, so the test binary can swap it at that single seam; `internal/summary`'s own tests use a temp config dir and the explicit mock model | first cut: `testing.Testing()` guard |
| D33 | 2026-09-10 | The batch window is the positional argument of `chat summarize` (`clai chat summarize 7d`); the mandatory `-since` flag is removed. Flags keep the CLI's stdlib order and go before the window; a flag seen after it yields a usage error naming the order | Maintainer: a mandatory flag that could be an argument should be one. The tree's parser follows stdlib flag semantics everywhere, so trailing flags are a general property, not a summarize one; the hint keeps the mistake cheap | `-since` (phase 5) |
| D34 | 2026-09-10 | Default-on by design: the upgrade rewrite that turns `summarize-conversations` on for existing configs (D12) is a deliberate exception to the conservative-rollout preference; the opt-out stays | Maintainer decision on R3-19: labelled conversations make clai more attractive, which is worth the few extra tokens per new conversation | — |

## Rejected alternatives

- **Lazy generation inside `chat list`.** Breaks the no-API-key invariant of
  the `chat` tree.
- **Two agents, one for title and one for summary.** Doubles requests per
  conversation; one tool submission carries both fields with the same
  robustness.
- **Fixed two-line plaintext protocol.** Parsing on the client instead of
  validation the model can react to; superseded by D9.
- **`chatIndexVersion` bump for `updated`.** Forces a full rebuild over the
  corpus on the next `chat list`.
- **Closing the four `pkg/agent` gaps with options** (`WithoutPersistence`,
  `WithChatIndex`, `WithoutAmbientMcpServers`, `WithConfigDirExact`). Public
  API growth for one internal consumer; superseded by D23.
- **Persist, display, then re-save with the summary.** Two writes of the
  conversation, the index and the mirror per query; superseded by D21.
- **Foreign session title as a display tier.** No source reader produces
  one (F1).
- **A `Configurations` switch that skips `SetCmdBanList`.** Closes the
  summarizer's hole but keeps a process global that every future in-process
  querier must remember to opt out of; superseded by D29.
- **Keeping `SetCmdBanList` as a deprecated no-op.** A library caller that
  set a ban list would silently run permissive; a compile error is the
  honest failure (D29).

## Out of scope

- Refreshing summaries as a conversation grows (D3, D16: a content hash would be the signal if ever needed).
- Repairing `QueryCost.MessageTrigger`, which shares the in-place-edit weakness named in D16.
- Summarizing foreign sessions in place (D15).
- Any change to `GroupKey`, which stays the hash of the first user message.
- Changing the `StopEvent` root-context cancel in the session runner; the summarizer isolates itself from it (D17).
- Silencing the cost manager's own stderr warnings (missing catalog, cancelled fetch); they are pre-existing and shared with the main run.
- The `audio_transcribe` run-scoped override (`internal/audio/tool_overrides.go`), which has the same package-global shape as the old ban list. It is written only by the query command's `OnSetup` (`ApplyMediaOverrides`), never by querier construction, so a one-off querier cannot clear it; moving it onto the context is a separate cleanup.
- Threading a context through `LLMTool.Call` itself. The `contextualTool` optional interface already gives every shell-spawning tool a context path; changing the public interface is not needed for D29.

## Definition of success

| Outcome                                                                                                                                                                         | Evidence                                                                              |
| ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------- |
| A new conversation carries `title` and `summary` after its first run, in the conversation file, the index and the `globalScope.json` mirror                                     | Phase 4 integration test with an instant fake; phase 4 e2e through the mock vendor    |
| A `-dre` reply keeps the stored `title` and `summary` in place; a plain `-re` fork inherits them into its new file; neither relaunches the summarizer                          | Phase 3 e2e; phase 4 launch tests                                                     |
| `clai chat summarize` over several rows completes every job; no job's completion cancels a sibling, and an interrupt cancels them all                                          | Phase 5 context tests and e2e; phase 2 caller-context test                            |
| The main answer is printed before any summary wait, and a fake slower than the join bound leaves the conversation unlabelled without error                                      | Phase 4 join table tests                                                              |
| The main call's normal completion does not cancel the summarizer, and the summarizer's completion does not cancel the main call                                                 | Phase 4 context tests                                                                 |
| `chat list`, chat info, `chat dir`, `dirv2 -r` and the lookback block render the title, and the summary where specified, with fallback to today's behavior for unlabelled chats | Phase 6 tests and e2e                                                                 |
| `chat list`, `chat dir`, `chat dirv2`, `chat continue` succeed with no vendor key in the environment                                                                            | Existing e2e kept green; phase 5 adds the `summarize` counter-example                 |
| `clai chat summarize 7d` summarizes only the window, skips labelled rows, writes the index once, and reruns as a no-op                                                    | Phase 5 tests                                                                         |
| Running the summarizer leaves `globalScope.json`, the dirscope binding and the chat index unchanged, and starts no MCP server                                                   | Phase 2 tests                                                                         |
| Building a second querier in the process leaves the main run's command-ban policy intact; a `cmd-ban` entry still refuses a shell command on the query path and the embedded agent path | Phase 1 context-policy tests; existing cmd-ban e2e suites rewritten to the context |
| `make qa` passes unedited; new packages at or above the repository coverage floor                                                                                               | Phase 7 sweep                                                                         |

## Validation policy

The repository gates apply unchanged: `gofumpt`, `staticcheck`, `go vet`,
`go test ./... -race -cover -count=3 -timeout=30s`, `go fix`, `dupl`. No
phase requires a live vendor call; every model interaction in tests goes
through a fake `Summarizer` or the mock vendor (`Model: "test"`). No
`Human required` steps.

**Fixture hygiene (review 3).** Every test that builds a querier or
asserts a silent stream blanks `DEBUG`, `DEBUG_*`, `CLAI_SUMMARIZER`,
`CLAI_CONFIG_DIR` and the vendor keys (`OPENROUTER_API_KEY`,
`OPENAI_API_KEY`, `ANTHROPIC_API_KEY`) with `t.Setenv`, and writes a text
config whose `model` is the mock, so the D22 ladder's floor can never
reach a paid vendor. A silence oracle that passes only because the
developer's shell is clean proves nothing.

## Feedback index

### Review round 1 — 2026-09-09 (architecture and code audit before phases)

| ID  | Severity | Finding                                                                                                          | Closed by                                                        |
| --- | -------- | ---------------------------------------------------------------------------------------------------------------- | ---------------------------------------------------------------- |
| F1  | High     | "Foreign rows substitute a session title" is false; no reader produces one                                       | Evidence bullet, display precedence, rejected alternatives       |
| F2  | High     | `StopEvent` cancels the root run context; a shared context kills the summarizer on success and vice versa       | D17, invariant rows, in-flight lifecycle                         |
| F3  | High     | `pkg/agent` has four in-process gaps (persistence, SkipIndex, ambient MCP, config-dir rewrite), not two          | D18, D23, phase 1 rescoped                                       |
| F4  | High     | Cost enrichment is gated on persistence; a non-persisting summarizer carries no usage                            | D19, `Summary.Queries`, `QueryCost.Purpose`                      |
| F5  | High     | `-re` rebuilds the chat from the mirror and drops non-message fields; the launcher would regenerate              | D20, invariant row, phase 3 scope                                |
| F6  | Medium   | Finalizer persists before display; "answer before any summary wait" needs display → join → persist               | D21                                                              |
| F7  | Medium   | `Save` rewrites the whole index per call; batch workers would race                                               | D24, phase 5 scope                                               |
| F8  | Medium   | The `chat` tree cannot read `summary-model`; model resolution had no single owner                                | D22, model ladder                                                |
| F9  | Medium   | `SetupQuerier` owns its `Configurations`; the summarizer cannot be placed there by the command                    | `Querier.SetSummarizer`, lazy `NewSummarizer` constructors       |
| F10 | Low      | `globalScope.json` is an index row                                                                               | Evidence bullet, phase 5 filter                                  |
| F11 | Low      | Index `updated` had no stated source                                                                             | Parameters row (upsert time; rebuild uses file mtime)            |
| F12 | Low      | `SummaryAt` zero value would serialize on every unlabelled chat                                                  | `omitzero` (Go 1.24+; module is 1.26)                            |
| F13 | Low      | Validate-and-retry had no end-to-end proof path                                                                  | Mock vendor `submit_summary` sequence env, phase 2               |
| F14 | Medium   | "Warned once" per failed summary would nag on every query for models without tool calling                        | D25, `DEBUG_SUMMARY`                                             |
| F15 | Low      | The stdout/stderr invariant could not be met verbatim (cost manager warnings are ambient)                        | Invariant reworded; out-of-scope entry                           |

### Validation round 1 — 2026-09-09 (`worklog-validate`, verdict Not ready)

| ID    | Severity | Finding                                                                                                              | Closed by                                                                                                                       |
| ----- | -------- | -------------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------- |
| V1-01 | Major    | Batch workers ran on the root context; the first `StopEvent` would cancel every sibling                              | D27; `StopEvent` evidence bullet; two invariant rows; phase 2 context rule and test; phase 5 per-job context table and test     |
| V1-02 | Major    | Plain `-re` forks a new file; the README and phase 3 described an in-place overwrite                                 | D28; `SetupInitialChat` evidence bullet; D20 reworded; phase 3 persist-path row, integration row one, error row                |
| V1-03 | Major    | The single-index-write limit named an injectable that no field owned                                                 | Parameters row `ChatHandler.upsertIndexBatch`; phase 5 limits row                                                              |
| V1-04 | Minor    | The join table had no "nothing launched" row                                                                         | Phase 4 join table row                                                                                                          |
| V1-05 | Minor    | `summarize` was said to use the shared `fullChatSetup`, which cannot see its flags or attach the summarizer          | Phase 5 dedicated `summarizeSetup`; parameters row `ChatHandler.summarizeOptions`                                              |
| V1-06 | Minor    | Phase 4 cited `S8`, a decision id from another worklog                                                               | Phase 4 finalizer step five states the rule                                                                                     |
| V1-07 | Minor    | `summary-model` needs `migrate:"true"` without `omitempty` for the upgrade fill to survive                           | Parameters row                                                                                                                  |
| V1-08 | Note     | The `/` filter lives in the `table` package; phase 6 implied a clai change                                           | Phase 6 filtering paragraph                                                                                                     |
| V1-09 | Note     | `1w2d` was a numeral outside an oracle row                                                                           | Phase 2 `ParseSince` row reworded                                                                                               |
| V1-10 | Note     | D15 did not distinguish cloned from un-cloned foreign rows                                                           | D15 reworded                                                                                                                    |

### Validation round 2 — 2026-09-09 (`worklog-validate`, verdict Not ready)

| ID    | Severity | Finding                                                                                                              | Closed by                                                                                                                       |
| ----- | -------- | -------------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------- |
| V2-01 | Major    | `NewQuerier` installs `CmdBan` into a process global; a second in-process querier clears the main run's ban list     | D29; evidence bullet (five gaps); invariant row; parameters row `Querier.cmdBan`; definition-of-success row; phase 1 context-policy section and tests |
| V2-02 | Minor    | Integration rows omitted `-cm test`, so the commands would not select the mock                                       | Phase 3, 4 and 6 integration rows carry `-cm test`                                                                              |
| V2-03 | Minor    | `NewAgentSummarizer` named the announcing config loader                                                              | Phase 2 names `utils.LoadConfigFromFileCollect`; `TestNewAgentSummarizer_loadsConfig` asserts empty stdout                      |
| V2-04 | Note     | `lookback-preview-runes` read as a config key; `previewOf` cited in the wrong file                                   | Intro paragraph and evidence bullet reworded                                                                                    |
| V2-05 | Note     | The legacy `Finalize(context.Background(), …)` call site was not mentioned for the interrupt gate                    | Phase 4 finalizer section sentence                                                                                              |
| V2-06 | Note     | Phase 5 row five said "no file touched" although `NewQuerier` writes a per-model config file                          | Phase 5 row narrowed to conversation files, index and mirror                                                                    |

### Validation round 3 — 2026-09-09 (`worklog-validate`, verdict Conditionally ready)

| ID    | Severity | Finding                                                                                                              | Closed by                                                                                                                       |
| ----- | -------- | -------------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------- |
| V3-01 | Minor    | The phase 1 symbol grep would hit a doc comment in `internal/audio/tool_overrides.go`, a file the phase did not list  | Phase 1 spec bullet and file list name the comment edit                                                                         |
| V3-02 | Minor    | `TestCmdBanEnforcement_ValidateCmdNotBanned` targets a deleted function and `TestCmdBanEnforcement_DefaultPermissive` duplicates a new row; neither was named in the rewrite list | Phase 1 rewrite list names both: re-target to `validateCmdNotBannedWithContext`; fold into `NoPolicyPermissive` |
| V3-03 | Note     | The README invariants table had no Test column                                                                        | Test column added, one named test per row; checklist item 4 reworded                                                           |
| V3-04 | Note     | Phase 2 asked the caller to compute the `submit_summary` ordinal that `nextToolCall` already counts                   | Phase 2 mock paragraph points at the existing loop                                                                              |

### Review round 2 — 2026-09-10 (`worklog-review`, post-implementation code review)

Round-1 findings F1–F15 and validation findings V1–V3 were re-checked
against the shipped code: none regressed. New findings:

| ID    | Severity | Phase                                                   | Finding                                                                                                                         | Tracked in |
| ----- | -------- | ------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------- | ---------- |
| R2-01 | Medium   | [5](./phase-5-batch-summarize.md#review-findings)       | Concurrent batch workers tear the per-model config file (`setupConfigFile`, `storePriceScheme` use plain `os.WriteFile`); reproduced, two of ten runs fail with `unexpected end of JSON input` | Phase 8 (fixed, D31) |
| R2-02 | Low      | [5](./phase-5-batch-summarize.md#review-findings)       | `-r` prints plain-text lines for the zero-row and aborted paths                                                                 | Phase 8 (fixed) |
| R2-03 | High (filed Low; re-rated in phase 8: `ancli.Warnf` prints on stdout, breaking the stdout invariant) | [2](./phase-2-summary-domain.md#review-findings) | The summarizer's querier repeats the cost enricher's warning on a cold price cache; the invariant held only with seeded prices | Phase 8 (fixed, `CostWarnf`) |
| R2-04 | Low      | [2](./phase-2-summary-domain.md#review-findings)        | `NewAgentSummarizer` discards config-upgrade announcements; the batch path upgrades `textConfig.json` silently                   | Phase 8 (fixed; supersedes the V2-03 silence row) |
| R2-05 | Note     | [6](./phase-6-surfaces.md#review-findings)              | Group row takes the newest member's label even when an older member is the only labelled one (contract met)                     | — |
| R2-06 | Note     | [4](./phase-4-in-flight-generation.md#review-findings)  | `chat continue` never launches the in-flight summarizer; undocumented                                                           | Phase 8 (doc line) |
| R2-07 | Note     | [5](./phase-5-batch-summarize.md#review-findings)       | A label-only batch rewrite bumps `updated` to now (as specified)                                                                | — |
| R2-08 | Note     | [7](./phase-7-docs-and-gates.md#review-findings)        | `Summary.ApplyTo` fallback stamps local time, every other stamp is UTC                                                          | Phase 8 (one line) |
| R2-09 | High     | [8](./phase-8-review-2-fixes.md)                        | Found by the phase-8 gate: an abandoned in-flight summarizer read `os.Stderr` in `newMcpLogSink` after `run()` returned (race with the e2e capture helper); the manager error log was a second async stdout writer | Phase 8 (fixed: `ErrOut`, `CostWarnf` widened) |

### Review round 3 — 2026-09-10 (`worklog-review`, holistic review of the whole feature)

Prior findings F1–F15, V1–V3 and R2-01..R2-09 re-checked against the
shipped code: none regressed. New findings (R3-21 groups eight
observations, listed in the phase files):

| ID    | Severity | Phase                                                     | Finding                                                                                                                         | Tracked in |
| ----- | -------- | --------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------- | ---------- |
| R3-01 | Medium   | [9](./phase-9-summarize-progress-board.md#review-findings) | Board colours before truncating and counts escape runes: with colour on at eighty columns the header loses `model tokens time` and its reset; `✗` rows bleed colour | Phase 10 (fixed) |
| R3-02 | Medium   | [8](./phase-8-review-2-fixes.md#review-findings)          | Toggle labels make the `chat list` prompt one hundred and three columns; it wraps at eighty and the table clears one line too few per keypress | Phase 10 (fixed) |
| R3-03 | Medium   | [8](./phase-8-review-2-fixes.md#review-findings)          | `resolveModelPrice` logs a failed price store with `ancli.Errf` (stderr) from the manager goroutine, bypassing `CostWarnf`      | Phase 10 (fixed) |
| R3-04 | Low      | [2](./phase-2-summary-domain.md#review-findings)          | `internal/summary` silence tests fail under `DEBUG=1` or `CLAI_SUMMARIZER=off` in the shell (reproduced)                        | Phase 10 (fixed) |
| R3-05 | Low      | [8](./phase-8-review-2-fixes.md#review-findings)          | Summary fixtures' ladder floor is the paid default model; vendor keys and `DEBUG` not blanked; guard holds by `-sm test` discipline | Phase 10 (fixed) |
| R3-06 | Low      | [2](./phase-2-summary-domain.md#review-findings)          | A valid submission is discarded when the closing turn errors                                                                     | Phase 10 (fixed) |
| R3-07 | Low      | [2](./phase-2-summary-domain.md#review-findings)          | `summary-max-tool-calls` is not the call bound (eight round trips, at least two model calls per label)                            | Phase 10 (fixed) |
| R3-08 | Low      | [4](./phase-4-in-flight-generation.md#review-findings)    | `joinSummary` applies an empty result; the no-regeneration invariant rests on the tool's validation                              | Phase 10 (fixed) |
| R3-09 | Low      | [8](./phase-8-review-2-fixes.md#review-findings)          | `WriteFileAtomic` applies `perm` literally, ignoring the umask                                                                   | Phase 10 (fixed) |
| R3-10 | Low      | [5](./phase-5-batch-summarize.md#review-findings)         | `writeChatIndex` is not atomic; an interrupted flush forces a full rebuild                                                        | Phase 10 (fixed) |
| R3-11 | Low      | [5](./phase-5-batch-summarize.md#review-findings)         | `-workers` below one silently clamped                                                                                            | Phase 10 (fixed) |
| R3-12 | Low      | [5](./phase-5-batch-summarize.md#review-findings)         | `-r` without `-y` prints the confirmation prompt on stdout ahead of the JSON                                                     | Phase 10 (fixed) |
| R3-13 | Low      | [9](./phase-9-summarize-progress-board.md#review-findings) | Board mutators ignore `closed`; shrink branch untested though it is the audio error-exit frame; rune vs display width; `Tick` name collision; undocumented marks | Phase 10 (fixed) |
| R3-14 | Low      | [7](./phase-7-docs-and-gates.md#review-findings)          | Stale `-since`, `summary-join-timeout` as a key, column arithmetic and phase-spec text after D33 and phase 9                       | Phase 10 (fixed) |
| R3-15 | Low      | [2](./phase-2-summary-domain.md#review-findings)          | `NewAgentSummarizer` absent/malformed config branches untested; dead `ChatQuerier` assertion                                       | Phase 10 (fixed) |
| R3-16 | Low      | [2](./phase-2-summary-domain.md#review-findings)          | `isSideEffectFree` proves silence with a warm per-model config and the manager goroutine disabled                                 | Phase 10 (fixed) |
| R3-17 | Low      | [4](./phase-4-in-flight-generation.md#review-findings)    | `DEBUG_SUMMARY` traces go through `ancli.Noticef` (stdout) and land in the `-r` answer stream                                    | Phase 10 (fixed) |
| R3-18 | Note     | [4](./phase-4-in-flight-generation.md#review-findings)    | The in-flight label describes the prompt only; the batch label the transcript; one plain sentence missing in `summaries.md`      | Phase 10 (fixed) |
| R3-19 | Note     | [8](./phase-8-review-2-fixes.md#review-findings)          | D12 rewrites persisted configs with `summarize-conversations: true` on upgrade; maintainer to confirm consciously                 | Confirmed by design (D34) |
| R3-20 | Note     | [8](./phase-8-review-2-fixes.md#review-findings)          | `.claude/settings.local.json` untracked artefact in the tree                                                                     | Phase 10 (fixed) |
| R3-21 | Note     | 2, 4, 5, 8, 9                                              | Eight observations: unescaped transcript, empty transcript costs a call, dead `ParseSince` guard, `launchSummary` overwrite, cancelled jobs counted as failed, `ConfigPrep` before window validation, dead catalog-fetcher warning, `[f]` offered over hidden rows, `Log` height invariant | — |

## Session journal

### 2026-09-09 — Design settled, README authored (clai)

Read the label paths, the `chat` tree's no-model constraint, the
`pkg/agent` config mapping and `Setup`, the tool-error feedback path, the
promotion copy in `SaveAsPreviousQuery`, and the vendor `ResponseFormat`
coverage. Q1–Q6 answered by the maintainer; decisions D1–D15 recorded. Maintainer challenged the message-count field at sign-off; replaced by `summary_at` (D16).
README written for sign-off; phases not yet authored.

### 2026-09-09 — Review round 1 and phases authored (clai)

Audited the README against `architecture/*.md` and the code it cites:
`session_runner.go` (StopEvent root cancel), `finalizer.go` (persist before
display; enrichment inside the persistence branch), `querier_setup_tools.go`
(ambient MCP startup), `conf.go` (`-re` adoption), `reply.go` (mirror copy),
`index.go` (per-save index rewrite; globalScope row), `mock.go` (scripted
tool calls), the anthropic source reader (no title). Findings F1–F15 recorded
in the feedback index and closed by D17–D25; D10 superseded by D23. Phases 1–7
written from the revised README; all `Not Started`.

Readiness checklist run (2026-09-09): (1) numerals grep hits only the two
oracle rows in the phase-2 integration contract; (2) every test name is
declared in exactly one phase file; (3) every config field, flag and
injectable has one owner row (the `ParseSince` injection and the
`ChatHandler.summarizer` field were added to the table while writing
phase 5); (4) invariants, limits, validation, launch, join and context
tables carry a test per row; (5) no phase mentions listening, manual or
paid steps; (6) no phase references text scheduled for deletion; (7)
conventions checked against the files named in the checklist.

### 2026-09-09 — Sign-off (maintainer)

Maintainer accepted review round 1 including D23, with one instruction:
errors in the in-flight summarizer must never affect the main run; logging
is the only permitted effect. Recorded as D26; the isolation invariant now
names every failure class, and phase 4 no longer lets a summarizer
constructor error fail the query command. Next: `worklog-validate`.

### 2026-09-09 — Validation round 1 and revision (clai)

`worklog-validate` returned Not ready with V1-01 to V1-10. The maintainer
confirmed the findings and instructed that every batch job runs on an
isolated context stemming from the root context. Closed by class: context
ownership moved into the summarizer (D27) with a per-job child context in
the batch handler; the `-re` fork documented and the fork's label
inheritance decided (D28); the index-write seam and the summarize options
given owner rows; the remaining minors and notes edited in place. Feedback
index maps every id to its edit. Readiness checklist rerun: numerals grep
hits only oracle rows and the `dupl` gate command; no test name spans two
phases; every injectable has an owner; no `Human required` steps. Next:
`worklog-validate` round 2.

### 2026-09-09 — Validation round 2 and revision (clai)

`worklog-validate` returned Not ready with V2-01 to V2-06. V1-01 to V1-10
were verified against the code and the cited edits; all resolved. V2-01
found a fifth in-process querier gap: `NewQuerier` writes the command-ban
list into a process global, which a summarizer built with an empty
`CmdBan` would clear during the main run. The maintainer rejected a
skip-switch and instructed that the package-scoped state be removed.
Recorded as D29: the policy rides the tool-call context from
`Configurations.CmdBan` through `Querier.cmdBan` to the executor's single
invoke site; the global, its setter and the test reset helper are deleted;
`pkg/agent` stops attaching its own copy. Folded into phase 1, whose goal
already is the in-process safety of a one-off querier. README revised for
sign-off; phase edits (phase 1 rewrite, V2-02, V2-03, V2-05, V2-06) follow
the sign-off.

### 2026-09-09 — Sign-off of D29 (maintainer) and phase edits (clai)

Maintainer approved D29 as written: global and exported setter deleted,
policy carried on the tool-call context, folded into phase 1. Phase 1
gained the context-policy section, a policy table with a test per actor,
an integration row for a second querier constructed mid-run, and the
rewrite list for the existing cmd-ban suites. Phase 4 gained an e2e row
proving the in-flight launch leaves a `cmd-ban` entry effective. V2-02
(`-cm test` on every query row), V2-03 (`LoadConfigFromFileCollect`),
V2-05 (legacy finalize call site) and V2-06 (row narrowed) edited in
place. Readiness checklist rerun: numerals grep hits only oracle rows and
the `dupl` gate command; no test name spans two phases; every new
injectable (`Querier.cmdBan`) has an owner row; no `Human required`
steps. Next: `worklog-validate` round 3.

### 2026-09-09 — Validation round 3 and revision (clai)

`worklog-validate` returned Conditionally ready with V3-01 to V3-04.
V1-01 to V1-10 and V2-01 to V2-06 were re-verified against the cited
edits and the code; none regressed. The four findings were closed in
place on the maintainer's instruction: phase 1 lists the audio override
comment edit and names the two leftover `TestCmdBanEnforcement_*` tests,
the README invariants table gained a Test column, and phase 2 points at
the mock's existing ordinal loop. Readiness checklist rerun: numerals
grep hits only the two phase-2 oracle rows; no test name spans two
phases; every injectable has an owner row; no `Human required` steps.
Next: `worklog-work` on phases 1 and 3.

### 2026-09-09 — Phase 1 executed (clai, phase-1 worker)

Phase 1 implemented test-first: `SkipAmbientMcpServers` in
`setupMcpManager`, enrichment hoisted above the persistence branch on a
`TokenUsage != nil` gate, `QueryCost.Purpose` with the index-model guard,
and D29 in full — `cmdBanList`, `cmdBanMu`, `SetCmdBanList`,
`ResetCmdBanListForTests` and `validateCmdNotBanned` deleted,
`Querier.cmdBan` attached at the executor's single invoke site,
`pkg/agent/run.go` no longer attaches its own copy, every cmd-ban suite
moved to the context path. Docs updated in `architecture/tooling.md` and
`architecture/config.md`. Two pre-existing `querier_test.go` fixtures
needed a `TokenUsage` under the new gate (recorded in the phase notes).
Four full-gate runs (host load 8–29 on 22 cores) each failed only with
30 s timeouts or timing assertions in packages this phase does not modify
(root, `internal/audio`, `internal/tools/mcp`); each passes alone at
`-race -count=3`, and the class is the recorded pre-existing full-suite
load flake (2026-08-26 phase 2, 2026-08-28 phase 11, 2026-09-05 R3-03).
Every other gate is clean and every phase criterion has passing evidence;
the orchestrator's rerun at load 9 (next entry) exited 0 and closed the
phase.

### 2026-09-09 — Phase 1 gate rerun and sign-off (clai, orchestrator)

Full unedited gate rerun at load average 9: exit 0, every package `ok`;
lint, vet, symbol grep and `dupl` clean. Phase 1 source diff reviewed
against its specification; set to Complete. Phases run sequentially in
one tree (1 → 3 → 2 → 4 → 5 → 6 → 7) so no two workers edit shared files
or run the race gate concurrently. Next: phase 3.

### 2026-09-09 — Phase 3 executed (clai, phase-3 worker)

Phase 3 implemented test-first: `Title`/`Summary`/`SummaryAt` on
`Chat`, `title`/`summary`/`updated` on the index row with
`effectiveUpdated` and no version bump (D14), `Updated` stamped at
upsert and from file mtime on rebuild (`entryModTime` seam for the
unstat-able row), the mirror and promotion copies carry the three
fields, the `-re` branch adopts them from the mirror (D20, D28). Unit
tests in `pkg/text/models`, `internal/chat`, `internal/text`; e2e in
`main_summary_e2e_test.go` (all three integration rows pass at the real
binary path through the mock vendor). Two specification gaps found, both
recorded in the phase notes: (1) error row two says today's guard adopts
a hand-edited mirror id — `LoadGlobalScope` normalizes every mirror id
to `globalScope`, so the guard never fires and the reply always forks
under a fresh id (the README evidence bullet is right, the row is not);
the fields are adopted the same way, which the test proves. (2)
Integration row three names `clai -n -r c l q` as the trigger and "Cache
written" as a side effect, but `chat list` is structurally read-only
(`readOnlyChatSetup` → `NoCreateConfig`, pinned by
`TestReadChatIndex_ReadonlySilentAndNoPersist`), so it can never persist
a rebuilt cache; `Test_e2e_chat_list_rebuild_mirrors_title` runs the
spec's trigger and asserts no cache, then uses the first query's
`chat.Save` as the persisted-rebuild trigger and asserts the row's
observables. Lint, vet, dupl (31 pre-existing groups) clean. Three full
`go test ./... -race -cover -count=3 -timeout=30s` runs at load 12–30 saw
30 s alarms only in the root and `internal/audio` packages (untouched;
each `ok` alone at `-count=3`), the phase-1 signature. Phase left In
Progress. Next: rerun the full gate on a quiet machine, decide row
three's trigger (accept the query trigger or amend the row), then set
Complete and start phase 2.

### 2026-09-09 — Phase 3 sign-off and D30 (clai, orchestrator)

Phase 3 diff reviewed against its specification; two planning errors the
worker reported are corrected as D30 (read-only `chat list` never
persists a rebuilt cache; `LoadGlobalScope` normalizes the mirror id).
Full suite green at `-race -count=3 -timeout=30s -p 1`; the parallel
unedited gate times out in root and `internal/audio` under external host
load (swap in use), having passed after phase 1. Phase 3 set to Complete;
the parallel gate is retried at each boundary and gates phase 7. Next:
phase 2.

### 2026-09-09 — Phase 2 executed (clai, phase-2 worker)

Phase 2 implemented test-first: `models.SummaryRequest/Summary/Summarizer`,
`internal/summary` (`submit_summary` tool with the validation table,
transcript rendering with the head-preserving cap, the labeller prompt,
`NewAgentSummarizer` over `text.CreateQuerier` with the D22 ladder and the
D27 child-context ownership, `ParseSince`), and the mock vendor's
`submit_summary` sequence env (`nextToolCall` now hands the ordinal on).
Every validation, context, `ParseSince`, integration, acceptance and error
row has its named passing test; `internal/summary` at 95.2% coverage.
One cross-phase dependency surfaced and was resolved in place, recorded
in the phase notes: reading the `summary-model` rung through
`text.Default` requires `Configurations.SummaryModel`, which the
parameters table assigns to phase 4. The field is declared now without
`migrate:"true"` (zero default, no file rewrite, no announcement); phase 4
adds the tag, the flag and the querier copies. Two oracle deviations are
recorded (the real tool-result fold shape for the retry row; in-flight
cancellation proven through a blocking querier seam because the runner
treats `ctx.Done()` as a normal stop and the mock has no in-flight
window). Lint, vet, fix, gofumpt clean; `dupl` at the 31 pre-existing
groups; the unedited parallel gate `go test ./... -race -cover -count=3
-timeout=30s` exited 0 at load 5-7 (no `-p 1` rerun needed). Phase 2 set
to Complete. Next: phase 4.

### 2026-09-09 — Phase 2 sign-off (clai, orchestrator)

Phase 2 diff reviewed: `internal/summary` in five files with package
constants, the querier built through `text.CreateQuerier`, the mock's
existing ordinal loop extended rather than duplicated. Orchestrator
rerun of the unedited parallel gate at load 5: exit 0, every package
`ok`, `internal/summary` 95.2%; lint and vet clean. Deviation noted for
phase 4: `Configurations.SummaryModel` already exists without
`migrate:"true"`; phase 4 adds the tag and the remaining fields. Next:
phase 4, then 5, then 6, then 7.

### 2026-09-09 — Phase 4 executed (clai, phase-4 worker)

Phase 4 implemented test-first: `summarize-conversations` (default on),
`summary-model` with the `migrate` tag, `SummaryJoinTimeout`, the
`-summarize` and `-sm`/`--summary-model` flags with `-cm`-style
completion, `Querier.SetSummarizer` attached from the query command's
`OnSetup` through `QueryCommandDeps.NewSummarizer` (`main.go` wires
`summary.NewAgentSummarizer`), the launcher in
`internal/text/summary_launch.go` (`WithoutCancel` child with the cancel
key masked, `WithCancel` kept by the launcher, panic recovered, nil
interrupt wired to SIGINT/SIGTERM and released after the join), and the
finalizer reordered to display → join → persist with the failure and
interrupt paths abandoning the summarizer first (D21, D26). Every join,
launch, context, integration, acceptance and error row has its named
passing test; the six e2e rows run the real `run` path with the real
summarizer through the mock vendor. Two findings outside the phase's file
list, both recorded in the phase notes: the mock vendor's `usage` raced
with a cancelled runner's `ctx.Done()` read once the summarizer's querier
was abandoned mid-stream (mutex added, vendor-local), and the interrupt
path keeps today's answer display before persisting (the spec's "return"
would have dropped it). One oracle deviation: integration row five's
"no third `queries` row" contradicts the `-dre` run's own cost row; the
test asserts exactly one summary row and three in total. Gates: gofumpt,
staticcheck, vet, fix clean; dupl at the 31 pre-existing groups; the
unedited parallel gate exited 0 once (load 10) and, on the final tree,
timed out only in root and `internal/audio` at load 19 with no real
failure, after which `-p 1` exited 0 with all 45 packages `ok`;
`internal/text` 84.4%. Phase 4 set to Complete. Next: phase 5.

### 2026-09-09 — Phase 4 sign-off (clai, orchestrator)

Phase 4 diff reviewed: launch, join and abandon in
`internal/text/summary_launch.go`; the finalizer is display → join →
persist with `persist` split out; the interrupt path keeps today's
partial-answer display (recorded deviation, observables unchanged); a
real data race in the mock vendor's usage field was caught by the gate
and fixed vendor-locally. Orchestrator rerun of the unedited parallel
gate at load 3: exit 0, every package `ok`, `internal/text` 84.4%; lint
and vet clean. Next: phase 5.

### 2026-09-09 — Phase 5 executed (clai, phase-5 worker)

Phase 5 implemented test-first: `SummarizeFlags` on the summarize sub
only, `summarizeSetup` (ConfigPrep → `-since` required → injected
`ParseSince` → injected `NewSummarizer` → handler with `summarizer` and
`summarizeOptions`), the handler in `handler_summarize.go` (index window
filter skipping `globalScope` and labelled rows, confirmation with count
and token estimate through `table.ReadUserInputFrom`, one worker pool on
per-job `WithCancel` children owning `ContextCancelKey` (D27), one result
channel, one index flush through the injectable `upsertIndexBatch`),
`SaveWithoutIndex` factored from `Save`, `UpsertChatIndexBatch` sharing
`upsertIndexRow` with the single upsert, and the `main.go` wiring plus
usage example. Every per-job context, limits, integration, acceptance and
error row has its named passing test; the six e2e rows run through `run`
with the real summarizer and the mock vendor. One cross-package move
recorded in the phase notes: `summary-input-runes` is declared as
`models.SummaryInputRunes` and aliased by `summary.InputMaxRunes`, since
the `chat` tree cannot import `internal/summary`. Gates: gofumpt,
staticcheck, vet, fix clean; dupl at the 31 pre-existing groups; the
unedited parallel gate exited 0 at load 2–6; `internal/chat` 74.4%.
Phase 5 set to Complete. Next: phase 6.

### 2026-09-09 — Phase 5 sign-off (clai, orchestrator)

Phase 5 diff reviewed: `Save`/`SaveWithoutIndex` share one body,
`upsertChatIndex` and `UpsertChatIndexBatch` share `upsertIndexRow`, one
worker pool with per-job contexts, one index flush; `-n` read from
`utils.Live` (recorded deviation). Orchestrator rerun of the unedited
parallel gate at load 1: exit 0, every package `ok`, `internal/chat`
74.4%; lint and vet clean. Carried to phase 7: the label stamping in
`summarizeOne` and `applySummary` (`internal/text/summary_launch.go`) is
the same four lines in two packages and belongs in one helper on the
shared `models.Summary` type. Next: phase 6.

### 2026-09-09 — Phase 6 executed (clai, phase-6 worker)

Phase 6 implemented test-first: `labelFor` in `internal/chat/label.go`;
`chatListRow` carries `Title`/`Summary` from the index row, the group row
copies the newest member's, and both table widths render `labelFor`
through the existing truncation; chat info prints `title:` and
`summary:` (width-truncated) for a labelled chat and today's quoted line
otherwise, with `chatInfoHeight` keeping the clear-height exact; `chat
dir`/`dirv2` gain `title`/`summary` (`omitempty`) and a `labelOutput`
fragment after `prompt:`, `version` untouched; the lookback element is
`lookbackLabel(row)` over the named `lookbackPreviewRunes` (80). Every
surface, integration, acceptance and error row has its named passing
test; the five e2e rows run through `run` with the mock vendor, the
lookback row asserting the decoded system message (files are
HTML-escaped JSON) and the list row widening the no-TTY dimension
fallback. Gates: gofumpt, staticcheck, vet, fix clean; dupl at the 31
pre-existing groups; the unedited parallel gate exited 0 with 45 `ok`;
`internal/chat` 75.2%. Phase 6 set to Complete. Next: phase 7.

### 2026-09-09 — Phase 6 sign-off (clai, orchestrator)

Phase 6 diff reviewed: `labelFor` is the single precedence rule; the
list row, group row, info view, dir/dirv2 records and the lookback label
each render it once; the dirv2 `version` is unchanged; `chatInfoHeight`
keeps the screen clear exact for the extra line (recorded deviation).
Orchestrator rerun of the unedited parallel gate at load 3: exit 0,
every package `ok`, `internal/chat` 75.2%; lint and vet clean. Next:
phase 7, which also folds the duplicated label stamping into one helper
on `models.Summary`.

### 2026-09-09 — Phase 7 executed (clai, phase-7 worker)

Phase 7 implemented: the label stamping carried over from the phase-5
sign-off is one method, `models.Summary.ApplyTo`, with its own unit test,
called by the in-flight join and the batch worker (`applySummary`
deleted). `architecture/summaries.md` written from the README and the
shipped code (data model, `submit_summary` validation, the summarizer
querier table, the D22 ladder, D27 context ownership, the in-flight
lifecycle display → join → persist with the interrupt path keeping the
display, replies and forks, the batch command, display precedence,
`DEBUG_SUMMARY`, invariants); `README.md`, `chat.md`, `dirscope.md`,
`config.md`, `cmd-dispatch.md`, `query.md` and `tooling.md` updated in
their own style; the phase-5 `main.go` usage line verified. Gates, all
unedited from the repository root: gofumpt, staticcheck, vet, fix clean;
`dupl` at the 31 pre-existing groups, none inside worklog-added code
(triage table in the phase notes); `go test ./... -race -cover -count=3
-timeout=30s` exit 0 with 45 `ok` at load 5, and `make qa` exit 0 at load
4–8. Coverage: `internal/summary` 95.2%, `internal/text` 84.4%,
`internal/chat` 75.2%, `internal/models` 100%. Phase 7 and the whole
board set to Complete.

**Next (maintainer):** review the uncommitted diff (`git status` shows
the modified files, the new `internal/summary/`, `internal/models/summary*.go`,
`internal/text/summary_launch*.go`, `internal/chat/handler_summarize*.go`,
`internal/chat/label*.go`, `main_summary_e2e_test.go`,
`architecture/summaries.md` and this worklog directory), commit, and
release as a minor version: `summary-model` is filled into existing
`textConfig.json` files on the next interactive run (`migrate:"true"`),
in-flight labelling is on by default and costs one small extra model call
per new conversation (`summarize-conversations: false` or `-summarize=false`
opts out), and the exported `pkgtools.SetCmdBanList` /
`ResetCmdBanListForTests` are gone (D29, compile-time break for library
callers that used them). Agents never commit.

### 2026-09-10 — Review round 2 (clai, `worklog-review`)

Independent post-implementation review of the uncommitted diff against
the README contract. Gates re-run unedited from the repository root at
load average about two on twenty-two cores: `gofumpt -l .` listed no
file; `go vet ./...`, `staticcheck ./...` clean; `go test ./... -race
-cover -count=3 -timeout=30s` exit 0, 45 packages `ok`, no timeout;
`dupl -t 80 .` at the 31 baseline groups, none in worklog-added files.
Every test the invariants table names exists in the file the table
implies. Invariants traced branch by branch: D29 (no package ban state,
one attachment point, `pkg/agent` on the same path), the `StopEvent`
isolation on the query and batch paths (the only reader of
`ContextCancelKey` type-asserts with an ok check, so the launcher's `nil`
mask is safe), every finalizer exit (failed, empty, interrupt, success,
structured), the mirror/promotion/`-re` field carry, the index model
resolver, and every surface. Two hypotheses tested: "the launcher's
`signal.Notify` swallows SIGTERM" — disproved, `shutdown.Monitor` already
registers SIGINT and SIGTERM; "N batch workers race on the per-model
config file" — confirmed by a throwaway test in `internal/summary`
(sixteen concurrent `Summarize` calls on a removed `mock_test_test.json`,
`-race -count=10`: two runs failed with `unexpected end of JSON input`;
test deleted after the run). Verdict: the diff ships clean through the
gates but is **not ready**: R2-01 (Medium) makes the first
`chat summarize` against an unused model, or any run with a cold price
cache and `OPENROUTER_API_KEY`, fail jobs spuriously. Phase 5 reopened;
fixes consolidated in the phase-8 addendum with D31 (atomic writes in the
two generic writers) plus the three Low findings and two one-line notes.
Findings recorded in the phase files, the feedback index and the board.
Next: `worklog-work` on phase 8, then review round 3.

### 2026-09-10 — Phase 8 executed (clai, reviewer as worker)

Maintainer instruction "fix it in place". Phase 8 implemented test-first:
`utils.WriteFileAtomic` in `setupConfigFile` and `storePriceScheme` (D31),
raw JSON on the zero-row and aborted batch paths, `Configurations.CostWarnf`
routing the summarizer querier's cost warnings to the `DEBUG_SUMMARY`
trace, the constructor announcing a config upgrade it performs, the
`chat continue` sentence in `architecture/summaries.md`, `ApplyTo` in UTC.
Two corrections recorded in the phase notes: R2-03 is High, not Low —
`ancli.Warnf` prints on **stdout**, so the cold-price warning broke the
stdout invariant and under `-r` polluted the answer stream; and R2-04's
announcing option supersedes the V2-03 "loads silently" row (the query
path stays silent because `SetupQuerier` upgrades first). The first full
gate then surfaced R2-09 (High): an abandoned in-flight summarizer read
`os.Stderr` in `newMcpLogSink` after `run()` returned, racing the e2e
capture helper, and the cost manager's error-log goroutine was a second
asynchronous stdout writer. Fixed without changing the phase-4 abandon
design: `Configurations.ErrOut` (nil → `os.Stderr`) reaches the sink,
`CostWarnf` also covers the manager log; the summarizer's querier now
references no process stream. Evidence: six clean root-package loops at
`-race -count=3` (21.6–23.3 s), then the unedited parallel gate exit 0
with 45 `ok` from a quiet start (two earlier parallel runs hit the
recorded root 30 s load alarm at load 13–14 and nothing else). gofumpt,
vet, staticcheck, fix clean; dupl at the 31 baseline groups; coverage
`internal/summary` 94.7%, `internal/text` 84.4%, `internal/chat` 75.3%,
`internal/cost` 57.4%, `internal/utils` 81.6%, `internal/models` 100%.
Docs updated: `architecture/summaries.md` (seams, D31, raw lines,
announcement), `architecture/config.md` (atomic vendor config write).
Phases 5 and 8 set to Complete. Next: `worklog-review` round 3 over
phase 8, then the maintainer's commit and minor release.

### 2026-09-10 — Maintainer follow-ups after phase 8 (clai)

Two maintainer requests handled in the same tree. (1) `make qa` timed out
in the root package on this branch but not on `main`: measured
sequentially, `main` runs the root package in 20.7 s at count=3 and the
branch in 22.2 s, and the in-flight summarizer launched inside every
pre-existing query e2e row accounted for ~1.2 s of that (A/B in the
phase-8 notes). Fixed by running the shared e2e fixture with
`summarize-conversations` off (the summary rows opt in through their own
fixture) and by skipping summarizer construction on opted-out runs
(`TestQueryCommand_summarizerNotBuiltWhenOff`). (2) The `chat list`
column header `Prompt` is now `About`, since the column renders the title
when one exists. Evidence and the final `make qa` outcome are in the
phase-8 notes. Next: `worklog-review` round 3, then the maintainer's
commit and release.

### 2026-09-10 — Summarizer test guard, D32 (clai)

Maintainer instruction: the summarizer must never run in tests unless a
test wants it, since a summarization costs money. Added D32:
`NewAgentSummarizer` refuses unless `summary.allowed()` passes —
`CLAI_SUMMARIZER=off` refuses anywhere, and under `go test` it refuses
unless `CLAI_SUMMARIZER=on`; the summary fixtures opt in, fakes are
unaffected. `TestSummarizerGuard` and `Test_e2e_summarizer_guard` (query
stays unlabelled, `chat summarize` fails naming the switch, no file
touched) pass; `internal/summary` 94.3%. Documented in
`architecture/summaries.md` (Debugging table, invariants) and
`architecture/config.md`. Gate outcome in the phase-8 notes.

### 2026-09-10 — D32 redesigned, positional window (D33), header rename (clai)

Maintainer review of the guard: a `testing.Testing()` sniff in production
code is a hack, and the mandatory `-since` should be an argument. Both
changed. The guard is now pure injection: `main.go` wires the summarizer
constructor through the package variable `newSummarizer`, the root test
binary's `TestMain` replaces it with a refusing constructor, and only
`setupSummaryE2E` restores the real one; `internal/summary` drops the
`testing` import and keeps `CLAI_SUMMARIZER=off` as an operator kill
switch. `Test_e2e_summarizer_guard` proves both modes on the query and
batch paths. The window is positional (`clai chat summarize 7d`); flags
go before it as everywhere in the tree (stdlib order), and a trailing
flag yields a usage error that says so. Docs, help texts and the `About`
column follow. Gate outcome in the phase-8 notes.

### 2026-09-10 — Phase 9: summarize progress board (clai)

Maintainer request for a summarize UI modelled on the audio transcribe
parallel flow. The audio board (`internal/audio/board.go`) is extracted
into `internal/board` (title, phase, marked rows with live elapsed,
footer, in-place redraw on a terminal, static lines elsewhere) with one
addition, `Log`, for lines that stay above the table; the diarization and
ffmpeg-split flows now go through it with their rendered-string tests
unchanged. `chat summarize` draws it on an interactive terminal: one row
per worker slot (conversation, state, model, tokens, time), every result
logged above as `✓ <id>  <title>` / `✗ <id>  <error>`, a live footer with
totals and an estimate; pipes, `-r` and `-n` keep today's lines.
`architecture/board.md` written; index, `audio.md` and `summaries.md`
updated. Gate outcome in the phase-9 notes.

### 2026-09-10 — `[f]oreign convs` toggle in `chat list` (clai)

Maintainer request outside the summaries scope, handled in the same tree:
`chat list` gains a `[f]oreign convs` table action next to `[d]irscoped
convs`, offered only when a source reader produced rows; it hides or
shows every external conversation for the list session (foreign rows are
dropped before grouping). `prepareListRows` takes a `showForeign` flag;
`TestPrepareListRows_ForeignToggle` and
`TestListChats_ForeignToggleThroughListChats` (real table, stub source
reader, three renders) pin it. Docs: `architecture/chat.md` list toggles,
`continue-from-claudex.md` dirscope-filter rules. Follow-up: both toggle
labels now carry their state in words (`: off|on`, `: shown|hidden`) and
are bold plus underlined in the non-default position, closed with
attribute resets so the table's prompt colour survives (`toggleLabel`,
`TestToggleLabel`; the two toggle tests count the labels per render).

### 2026-09-10 — Review round 3 (clai, `worklog-review`, holistic)

Maintainer instruction: a holistic review of the whole feature, the
scope-crept areas (`About`, the `[f]` toggle and its labels, the
`internal/board` extraction, the positional window, the D32 guard) held
to the same bar as the core. Gates re-run unedited from the repository
root: `gofumpt -l .` empty, `staticcheck`, `go vet`, `go build` clean,
`dupl -t 80 .` at the thirty-one baseline groups; `go test ./... -race
-cover -count=3 -timeout=30s` started at load 4.8 on twenty-two cores,
exit 0, forty-six packages `ok`, root package 22.8 s, `internal/board`
97.9%, `internal/summary` 94.9%, `internal/text` 84.4%, `internal/chat`
76.7%, `internal/models` 100%. Five independent area reads (summary
domain; `internal/text` and `pkg`; `internal/chat`; board and audio;
wiring, e2e and docs) traced every invariant branch by branch; the lead
re-verified each Medium in the code and reproduced R3-04 (`DEBUG=1`
fails `TestAgentSummarizer_isSideEffectFree`). Verdict: the diff ships
clean through the gates but is **not ready**: R3-01 makes the summarize
and audio boards lose their last columns and leak colour on any
eighty-column terminal with colour on (every board test runs with colour
off, so the gate cannot see it); R3-02 makes `chat list` drift on an
eighty-column terminal whenever both toggles are offered; R3-03 leaves
one asynchronous stderr writer reachable from the summarizer's querier.
Fourteen Low and four Note items recorded in the phase files; fixture
hygiene promoted to the validation policy; fixes consolidated in the
phase-10 addendum. Next: `worklog-work` on phase 10, then review round
4, then the maintainer's commit and release.

### 2026-09-10 — Phase 10 executed (clai, reviewer as lead, four workers)

Maintainer instruction "Fix it!". Phase 10 executed test-first by four
parallel workers on disjoint packages (board and audio; `internal/chat`;
`internal/cost`, `internal/text`, `internal/utils`; `internal/summary`
and the root fixtures) with the lead on docs, the worklog and the gate.
Every Medium and Low from review 3 is fixed and pinned: `Truncate` is
escape- and display-width-aware and closes a cut coloured line with a
reset; `chat list` chooses a label tier that fits the width
(`listPromptTier`); `cost.Manager.SetWarnf` routes the last unrouted
writer through `CostWarnf`; fixture hygiene blanks `DEBUG`, `DEBUG_*`,
`CLAI_SUMMARIZER` and the vendor keys and pins the mock as the ladder
floor; a held submission survives a failing closing turn; an empty
in-flight result is a failure; `WriteFileAtomic` honours the umask; the
chat index is written atomically; `-workers` below one is a usage
error; the raw confirmation prompt goes to stderr; traces go to stderr;
mutators after `Finish` are no-ops and `Footer()` reflects the footer
function; stale docs and worklog text corrected; `.claude/settings.local.json`
ignored. The first unedited gate failed two pre-existing root rows that
counted the literal `[d]irscoped` label (the e2e process sees the
eighty-column fallback, which selects the terse tier); both now assert
the toggle by key and state word. Final gate: static checks clean, dupl
at baseline, forty-six packages `ok` (root 22.7 s at count=3 from a
quiet host). Board and notes updated. Next: `worklog-review` round 4
over phase 10, then the maintainer's commit and minor release.

### 2026-09-10 — D34: default-on confirmed (maintainer)

R3-19 closed by decision: the default-on rollout, including the upgrade
rewrite of existing text configs, is deliberate — a labelled history
makes clai more attractive, and that is worth a few extra tokens per new
conversation. The `summarize-conversations` opt-out and `-summarize`
override are unchanged. No code change.
