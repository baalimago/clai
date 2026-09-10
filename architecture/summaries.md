# Conversation summaries: a generated title and summary per conversation

This document describes how clai labels a conversation with a model-generated
**title** and **summary**, how the label is produced without touching the
conversation's own model run, and where it is rendered. It is the
authoritative note for the fields `chat.md` lists on `Chat`, the
`chat summarize` verb, and the `title: summary` form of the lookback block
in `dirscope.md`. Decision ids (D-numbers) refer to
`worklogs/2026-09-09-conversation-summaries/README.md`.

Before this feature every surface labelled a conversation with its **first
user message**. That message stays the fallback everywhere; the label is an
additive layer on top of it.

Core implementation:

- `internal/summary/*` — the summarizer: `submit_summary` tool, transcript
  rendering, prompt, querier construction, model ladder, `ParseSince`
- `internal/models/summary.go` — the shared `Summarizer` interface,
  `SummaryRequest`, `Summary` and `Summary.ApplyTo`
- `internal/text/summary_launch.go`, `internal/text/finalizer.go` — the
  in-flight launch, join and abandon on the query path
- `internal/chat/handler_summarize.go` — the batch command
- `internal/chat/label.go`, `internal/chat/dirscope_lookback.go`,
  `internal/chat/handler_list_chat.go`, `internal/chat/handler_dir.go` —
  the display surfaces

## Data model

`pkg/text/models.Chat` carries three additive fields:

```go
Title     string    `json:"title,omitempty"`
Summary   string    `json:"summary,omitempty"`
SummaryAt time.Time `json:"summary_at,omitzero"`
```

`SummaryAt` is provenance only: it says when the label was generated and is
never used as a staleness index (D16). Conversations are edited in place, so
no message count or index into `Messages` is recorded either.

The chat index row (`internal/chat/index.go`, `chat_index.cache`) caches
`title`, `summary` and `updated`. `updated` is stamped at every upsert and
taken from the file mtime on a rebuild; rows cached before the field existed
decode as zero and `effectiveUpdated()` falls back to `created`. The index
version is **not** bumped (D14): a bump would force a full rebuild over the
corpus on the next `chat list`.

Usage accounting: every summarizer run appends one or more
`QueryCost` rows to `Chat.Queries` with `Purpose: "summary"` (D13, D19).
The cost and token columns therefore stay truthful, while
`chatIndexRowFromChat` skips summary rows when it resolves the row's model,
so a summary row appended last never masquerades as the conversation's
model.

## Shared interface (`internal/models`)

```go
type SummaryRequest struct {
    Chat  pub_models.Chat
    Model string // explicit model; empty lets the summarizer apply the ladder
}

type Summary struct {
    Title, Summary, Model string
    Queries               []pub_models.QueryCost // Purpose "summary"
    GeneratedAt           time.Time
}

type Summarizer interface {
    Summarize(ctx context.Context, req SummaryRequest) (Summary, error)
}

func (s Summary) ApplyTo(c *pub_models.Chat)
```

`ApplyTo` is the single label-stamping rule: it sets `Title`, `Summary` and
`SummaryAt` (a zero `GeneratedAt` becomes the time of the call) and appends
`Queries`. Both the in-flight join and the batch worker call it.

Nothing under `internal/text` or `internal/chat` imports
`internal/summary`. The production implementation,
`summary.NewAgentSummarizer(confDir)`, is injected from `main.go` as a lazy
constructor through `text.QueryCommandDeps.NewSummarizer` (query path) and
`chat.CommandDeps.NewSummarizer` plus `chat.CommandDeps.ParseSince` (batch
path, D7). The `chat` tree keeps its no-model invariant: only
`chat summarize` invokes the constructor, so `chat list`, `dir`, `dirv2` and
`continue` still need no API key.

## The summarizer querier

`NewAgentSummarizer` loads `textConfig.json` once (through
`utils.LoadConfigFromFileCollect`) for the ladder rungs `summary-model` and
`model`; when that load upgrades the file it announces the added fields
the way the interactive loader does, which only happens on the batch path
because the query path's `SetupQuerier` has already upgraded it. Each `Summarize` call builds a fresh querier through
`text.CreateQuerier` (never `agent.Setup`, D23) with a `Configurations`
holding:

| Field | Value | Why |
|---|---|---|
| `Model` | the resolved model | see the ladder below |
| `SystemPrompt` | the fixed labeller prompt (`internal/summary/prompt.go`) | |
| `UseTools: true`, `Tools: [submit_summary]`, no tool globs | exactly one tool, registered without the unknown-glob warning | |
| `SkipAmbientMcpServers: true` | no `mcpServers/*.json` server starts for a one-tool run (D18) | |
| `SaveReplyAsConv: false` | never persists a conversation, never writes `globalScope.json`, never touches a dirscope binding | |
| `MaxToolCalls: 4` | bounds the validate-and-retry loop: four accepted submissions; the stoploss then refuses three more before the hard stop, so a hostile run makes up to eight tool round trips, and every label costs at least two model calls because the querier ends on a text turn after `accepted` | |
| `Raw: true`, `Out: io.Discard`, `ErrOut: io.Discard` | nothing reaches the host's stdout or stderr, and an abandoned run never reads a process stream | |
| `CostWarnf` → `DEBUG_SUMMARY` trace | the cost manager's and enricher's warnings (`ancli.Warnf` prints on stdout) never reach the host | |
| `ConfigDir` verbatim | no `clai` segment appended | |
| empty `CmdBan` | the tool is not a shell tool; the policy is per run anyway (D29) | |
| `AgentSettings.UsageRecorder` | captures every model step for the synthesized usage row | |

The querier's input is one user message: the conversation's user messages
plus the last assistant message that has text, rendered as role-tagged
lines inside a `<transcript>` block (text parts only; tool, system and
tool-call-only messages are skipped), capped head-preserving at
`models.SummaryInputRunes` (2000) runes, followed by the submission
instruction.

`Summary.Queries` is the querier chat's enriched `Queries` when the cost
catalog produced them, else one row synthesized from the recorded usage with
zero cost; every row is stamped `Purpose: "summary"`.

### `submit_summary` and validation

The model returns its result by calling `submit_summary` (D9), a tool with
two required string inputs. The tool validates and stores the result in a
per-call holder; the last valid submission wins; a run that ends without a
valid submission is an error (`no summary submitted`).

| Input | Rule |
|---|---|
| `title` | required, non-empty after trimming, single line, whitespace collapsed, at most 60 runes |
| `summary` | required, non-empty after trimming, whitespace collapsed, at most 240 runes |

A rejected field returns an error naming the field (`title: at most 60
runes, got 71`). The tool loop folds that into the tool result as
`ERROR: failed to run tool: submit_summary, error: title: …` and feeds it
back to the model, which corrects the named field and calls again. This is
what makes validate-and-retry work on every vendor without structured
output; `ResponseFormat` is implemented by two vendors only and is not
used.

### Model ladder (D22)

One ladder, applied wherever a rung is empty:

```text
-sm flag → config summary-model → the conversation's last recorded model → config model
```

"Last recorded model" is the newest `Queries` row with a non-empty `Model`
and an empty `Purpose`, the same rule the index uses. The query path
resolves the first two rungs itself and passes the run's own model as the
third, so `SummaryRequest.Model` is always non-empty there. The batch path
passes the flag value and lets the summarizer apply the rest. Using the
conversation's own model by default (D11) never leaks a local-model chat to
a second vendor.

### Context ownership (D17, D27)

The session runner cancels the cancel func stored under
`utils.ContextCancelKey` on every normal completion (`StopEvent`), and
`main.go` stores the **root** cancel func there. A summarizer sharing the
run context would be cancelled the moment the main call finished, and its
own `StopEvent` would cancel the main call. `Summarize` therefore runs its
querier on `context.WithCancel(caller ctx)` with that child's cancel stored
under `utils.ContextCancelKey`: the runner's `StopEvent` reaches only the
child, while a cancel of the caller's context still reaches the querier.
Cancellation is detected by `Summarize` itself (the runner treats
`ctx.Done()` as a normal stop), which checks the caller's `ctx.Err()`
before building the querier and again after the run.

## In-flight generation (query path)

Generation is on by default with an opt-out (D12): config
`summarize-conversations` (`textConfig.json`, default `true`) and the
`-summarize` flag on `query`, which overrides the config for the run.
`summary-model` / `-sm` select the model.

Lifecycle (`internal/text/summary_launch.go`, `finalizer.go`):

1. **Launch.** `Querier.Query` launches the summarizer just before the
   session runner starts, when all hold: a summarizer is attached,
   `summarize-conversations` is on, the run persists (`SaveReplyAsConv`)
   and `InitialChat.Summary` is empty (D3). It receives a copy of the
   initial chat on a context derived from the run context with
   `context.WithoutCancel` (the root's cancel key is masked with a nil
   value, since `WithoutCancel` keeps values) and then `context.WithCancel`,
   whose cancel the launcher keeps. A panic inside `Summarize` is
   recovered into an error. Because the copy is taken before the model
   answers, the in-flight label describes the prompt, never the outcome;
   only the batch path summarizes a finished transcript.
2. **Stream.** The main call streams as today, unaffected.
3. **Finalize** (`sessionFinalizer.Finalize`), in this order:
   - a failed or empty run abandons the summarizer and persists at once;
   - an interrupt (root context cancelled without a `StopEvent`) abandons
     the summarizer, then displays the partial answer and persists;
   - otherwise the finalizer **displays** the answer, **joins** the
     summarizer for at most a fixed join bound (5 s, `Configurations.SummaryJoinTimeout`,
     `json:"-"`, no config key or flag; also giving up
     when the interrupt channel closes, which is wired to SIGINT/SIGTERM
     unless a test injects its own), stamps the result with
     `Summary.ApplyTo`, and **persists once** — conversation file, index
     and `globalScope.json` mirror stay consistent (D21).
4. A summarizer still running after the join is cancelled and forgotten;
   the process exit reaps it.

Display precedes the join so the answer is never delayed by the summary
wait; a short one-shot query still gets labelled because the summarizer
runs concurrently with the main call (D8).

**Isolation (D26).** No summarizer failure of any kind — constructor error,
model error, validation exhaustion, timeout, panic, interrupt — fails,
delays beyond the join bound, or alters the answer, the exit status or the
persistence of the main query. Every failure is dropped silently (D25);
`DEBUG_SUMMARY=1` traces launch, apply, failure and abandon lines. The
query command's `OnSetup` attaches the summarizer through
`Querier.SetSummarizer`; a constructor error attaches nothing and the query
proceeds.

### Replies and forks (D20, D28)

A label is generated once and never refreshed on the query path (D3). The
`globalScope.json` mirror is built by explicit field copy and carries
`title`, `summary` and `summary_at`. A plain `-re` **forks** a new file (the
mirror carries `ID: globalScope`, so a fresh id is generated); the `-re`
branch of `SetupInitialChat` adopts the three fields from the mirror when
its own chat has no summary, so the fork inherits its parent's label and
the launcher does not fire. A `-dre` reply continues the bound conversation
from the full file and keeps its label in place. Both paths append their
own cost row; neither adds a second `purpose: "summary"` row.

An opted-out run (`summarize-conversations: false` without `-summarize`)
constructs no summarizer at all: the query command skips
`NewAgentSummarizer`, so no second `textConfig.json` load happens.
`clai chat continue` runs in the `chat` tree, which attaches no
summarizer, so a pre-feature conversation continued there stays
unlabelled until `chat summarize` runs.

## Batch generation: `clai chat summarize <window>`

Existing history is labelled on request (D1, D2 — there is no `--all`;
the corpus can be hundreds of megabytes):

```bash
clai c s 7d
clai c summarize -y -workers 8 2026-09-01
clai -r c summarize -force 12h
```

| Flag | Default | Meaning |
|---|---|---|
| `<window>` (positional; flags go before it, stdlib order — `clai c s -y 7d`, a trailing flag yields a usage error naming the order) | required | window: an extended duration (`s`, `m`, `h`, `d`, `w`, e.g. `1w2d`), an RFC 3339 timestamp, or `YYYY-MM-DD` at local midnight (D4) |
| `-force` | off | regenerate rows that already have a summary |
| `-y`/`-yes` | off | skip the confirmation; required with `-n` |
| `-workers` | 4 | concurrent summarizer jobs |
| `-sm`/`-summary-model` | `""` | first rung of the ladder |

`summarizeSetup` (`internal/chat/cmd.go`) is the one config-touching path
of the verb: config prep, the positional window required, the injected `ParseSince`, the
injected `NewSummarizer`, then the handler. The handler
(`handler_summarize.go`):

1. filters **on the index only** — never opens a file outside the window —
   keeping native rows whose `effectiveUpdated()` is inside the window and
   whose `summary` is empty (unless `-force`), skipping the `globalScope`
   row. Un-cloned foreign (Claude Code, Codex, …) rows never enter the
   index and are not summarized; a cloned foreign chat is a native file and
   is eligible (D15);
2. prints the count and an upper-bound token estimate
   (`count × 2000 / 4`) and asks `[y/N]` unless `-y`;
3. runs a bounded worker pool. Every job runs on its own
   `context.WithCancel(command ctx)` child with the job's cancel stored
   under `utils.ContextCancelKey` before `Summarize` (D27): a job's
   completion cancels no sibling, and a root cancel (interrupt) reaches
   every job. Every job builds its own querier, so N workers construct N
   queriers of one model at once; both writers of the per-model config
   file (`setupConfigFile` creating the default, the cost manager storing
   a fetched price) go through `utils.WriteFileAtomic` (temp file plus
   rename, D31) so a sibling's read never sees a torn file;
4. each worker stamps the result with `Summary.ApplyTo` and writes the
   conversation through `chat.SaveWithoutIndex`; the coordinator collects
   every result and flushes the index **once** through
   `chat.UpsertChatIndexBatch`, on completion and on cancellation (D24 —
   `chat.Save` rewrites the whole `chat_index.cache` per call and
   concurrent upserts lose updates);
5. reports progress. On an interactive terminal (stdout is a TTY, not
   `-r`, not `-n`) it draws the shared progress board
   ([board.md](./board.md)): one row per worker slot showing the job's
   index in the selected set, the conversation id (the column takes the
   width the terminal leaves, a full UUID from 94 columns), state,
   model, tokens and elapsed; every finished conversation logged
   above it as `✓ <id>  <title>` or `✗ <id>  <error>`, and a live footer
   `labelled a/n · failed f · in flight k · elapsed · eta`. Otherwise it
   prints one line per conversation (`<id>: <title>` or
   `<id>: ERROR …`) and a totals line; with `-r` each is a JSON object
   (`{"id","title","summary","error"}` and
   `{"selected","labelled","failed"}`; a zero-row run prints the all-zero
   totals object and a declined confirmation `{"aborted":true}`). A failed job, a failed index flush
   or a cancelled run makes the command return an error; the batch run is
   the user's explicit request, so errors are reported here and nowhere
   else (D25).

The command is idempotent: a rerun over the same window is a no-op unless
`-force`.

## Display precedence

`labelFor(title, firstUserMessage)` (`internal/chat/label.go`) is the one
rule: the title when non-empty, else the first user message with today's
truncation. Foreign rows have no title source and keep their first-message
label.

| Surface | Labelled | Unlabelled |
|---|---|---|
| `chat list` row, group row (newest member's label) | `title` through the existing width truncation; the `/` filter matches it | first user message |
| chat info view | `title: …` and, when present, `summary: …` (both width-truncated; the screen clear height grows by the extra line) | `summary: "<first message>"` |
| `chat dir` / `dirv2` (`-r`) | `title` / `summary` JSON keys (`omitempty`); pretty output adds `title:` / `summary:` lines after `prompt:` | keys absent; `version` unchanged (D6) |
| lookback `<recent_conversations>` element | `title: summary` (the title alone when the summary is empty) | the first 80 runes of the first user message |

## Debugging

| Env var | Effect |
|---|---|
| `DEBUG_SUMMARY=1` (also on under plain `DEBUG=1`) | `[DEBUG_SUMMARY]` lines: summarizer unavailable, launch, applied, failed, abandoned after timeout / on interrupt |
| `CLAI_SUMMARIZER=off` | Operator kill switch: refuses to build the real summarizer in any process; the query path stays unlabelled (traced), `chat summarize` fails naming the switch |

The cost manager's own stderr warnings (missing catalog, cancelled fetch)
are pre-existing and shared with the main run; they are not silenced.

## Invariants

- The `chat` tree needs no API key; only `chat summarize` builds a
  summarizer.
- The test suite never builds the real summarizer through the CLI unless
  a fixture opts in: `main.go` wires it through the package variable
  `newSummarizer`, and the root test binary's `TestMain` replaces it with
  a refusing constructor that only `setupSummaryE2E` restores. No
  production code knows about tests. `CLAI_SUMMARIZER=off` disables the
  summarizer for any process.
- The summarizer never persists a conversation, never writes
  `globalScope.json`, never touches a dirscope binding, never sets
  `chat.SkipIndex`, starts no ambient MCP server, calls exactly one tool,
  writes nothing to the host's stdout, and alters no other run's
  command-ban policy.
- The summarizer's own `StopEvent` never cancels the caller's context; the
  main call's normal completion never cancels the in-flight summarizer.
- No in-flight failure reaches the main run's answer, exit status or
  persistence.
- A label is never regenerated on the query path; replies keep it, `-re`
  forks inherit it.
- Summary usage rows carry `purpose: "summary"` and never become the
  conversation's model.
- The batch command is idempotent and writes the index once, from one
  writer.

Tests: `internal/summary` (validation table, retry through the real tool
loop via the mock vendor's `CLAI_MOCK_SUMMARY_TITLES` /
`CLAI_MOCK_SUMMARY_SUMMARIES` sequences, context ownership, ladder,
`ParseSince`), `internal/text` (`TestFinalize_join`, `TestSummaryContext_isolation`,
`TestQuery_launchConditions`), `internal/chat` (`TestHandleSummarize_*`,
surface tests), and the root `main_summary_e2e_test.go` suites through the
mock vendor (`-cm test`).

## Out of scope

- Refreshing a label as a conversation grows (D3, D16); a content hash like
  `GroupKey` would be the signal if ever needed.
- Summarizing foreign sessions in place (D15).
- Any change to `GroupKey`, which stays the hash of the first user message.
- Changing the `StopEvent` root-context cancel in the session runner; the
  summarizer isolates itself from it.
