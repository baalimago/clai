# `clai loop` — autonomous multi-iteration goal loop

## Goal

Add the `clai loop` (alias `l`) command so clai can iterate across fresh
sessions until a user-defined goal is achieved, verified by an optional shell
check command, or halted by an explicit block or iteration limit. Each
iteration runs a standard text querier session equipped with the
`complete_task` tool; if an iteration ends without an accepted `complete_task`
call (for example after a token-stoploss handover or an intermediate reply),
its final assistant reply becomes the handover context for the next iteration.

What must stay true: `clai query` and every other command keep their exact
behaviour; each persisted loop iteration shares the first user message so
`clai chat list` groups all iterations of a loop run under one `GroupKey`; and
stopping an iteration on `models.StopEvent` never cancels the outer loop
context.

## Glossary

| Term                 | Meaning                                                                                                                      |
| -------------------- | ---------------------------------------------------------------------------------------------------------------------------- |
| Outer loop           | The iteration cycle owned by `LoopRunner` across one or more `ChatQuerier.TextQuery` sessions.                               |
| Inner loop           | One `ChatQuerier.TextQuery` run (`sessionRunner.Run`), which streams completions and executes tool calls for one iteration.  |
| Handover             | The final assistant text of an iteration that ended without a recorded `complete_task` submission.                           |
| Seed chat            | The initial `pub_models.Chat` built once during setup from the system prompt, any `-g`/`-re`/`-dre` context, and stdin/args. |
| Verification command | The optional shell command passed via `-check` that must exit 0 before an `"achieved"` submission is accepted.               |

## Flows

### Achieve goal across handover iterations

```text
┌────┐       ┌───────┐       ┌───────────┐       ┌────────────┐       ┌──────────┐       ┌──────────────────┐       ┌───────────┐
│user│       │cli:Run│       │LoopCommand│       │SetupQuerier│       │LoopRunner│       │CompletionRecorder│       │ChatQuerier│
└─┬──┘       └───┬───┘       └─────┬─────┘       └─────┬──────┘       └────┬─────┘       └────────┬─────────┘       └─────┬─────┘
  │              │                 │                   │                   │                      │                       │
  │ clai l ...   │                 │                   │                   │                      │                       │
  │─────────────▶│                 │                   │                   │                      │                       │
  │              │ OnSetup(cmd)    │                   │                   │                      │                       │
  │              │────────────────▶│                   │                   │                      │                       │
  │              │                 │ SetupQuerier(...) │                   │                      │                       │
  │              │                 │──────────────────▶│                   │                      │                       │
  │              │                 │◀ - - - - - - - - ─│                   │                      │                       │
  │              │                 │ (Querier, *Configurations)            │                      │                       │
  │              │                 │                   │                   │                      │                       │
  │              │                 │ NewCompletionRecorder(checkCmd)       │                      │                       │
  │              │                 │─────────────────────────────────────────────────────────────▶│                       │
  │              │                 │◀ - - - - - - - - - - - - - - - - - - - - - - - - - - - - - - │                       │
  │              │                 │                   │                   │                      │                       │
  │              │                 │ NewLoopRunner(q, recorder, cfg)       │                      │                       │
  │              │                 │──────────────────────────────────────▶│                      │                       │
  │              │                 │◀ - - - - - - - - - - - - - - - - - - ─│                      │                       │
  │              │◀ - - - - - - - ─│                   │                   │                      │                       │
  │              │                 │                   │                   │                      │                       │
  │              │ Query(ctx)      │                   │                   │                      │                       │
  │              │────────────────────────────────────────────────────────▶│                      │                       │
  │              │                 │                   │                   │                      │                       │
  │              │                 │             [Iteration 1]             │                      │                       │
  │              │                 │                   │                   │ Reset()              │                       │
  │              │                 │                   │                   │─────────────────────▶│                       │
  │              │                 │                   │                   │◀ - - - - - - - - - - │                       │
  │              │                 │                   │                   │ TextQuery(ctx, chat₁)│                       │
  │              │                 │                   │                   │─────────────────────────────────────────────▶│
  │              │                 │                   │                   │◀ - - chat₁ (handover reply, no tool call) - ─│
  │              │                 │                   │                   │ Submission()         │                       │
  │              │                 │                   │                   │─────────────────────▶│                       │
  │              │                 │                   │                   │◀ - - (zero, false) - │                       │
  │              │                 │                   │                   │                      │                       │
  │              │                 │             [Iteration 2]             │                      │                       │
  │              │                 │                   │                   │ Reset()              │                       │
  │              │                 │                   │                   │─────────────────────▶│                       │
  │              │                 │                   │                   │◀ - - - - - - - - - - │                       │
  │              │                 │                   │                   │ TextQuery(ctx, chat₂)│ (chatid:New+handover)│
  │              │                 │                   │                   │─────────────────────────────────────────────▶│
  │              │                 │                   │                   │                      │ CallWithContext       │
  │              │                 │                   │                   │                      │ (status="achieved")   │
  │              │                 │                   │                   │                      │◀──────────────────────│
  │              │                 │                   │                   │                      │ ─ "recorded" (check 0)▶
  │              │                 │                   │                   │◀ - - chat₂ (completed) - - - - - - - - - - - │
  │              │                 │                   │                   │ Submission()         │                       │
  │              │                 │                   │                   │─────────────────────▶│                       │
  │              │                 │                   │                   │◀ - (Achieved, true) ─│                       │
  │              │◀ - - nil - - - - - - - - - - - - - - - - - - - - - - - ─│                      │                       │
  │◀ - exit 0 - ─│                 │                   │                   │                      │                       │
```

### Reject unverified completion, then stop on a blocked submission

```text
┌────┐       ┌───────┐       ┌──────────┐       ┌──────────────────┐       ┌───────────┐
│user│       │cli:Run│       │LoopRunner│       │CompletionRecorder│       │ChatQuerier│
└─┬──┘       └───┬───┘       └────┬─────┘       └────────┬─────────┘       └─────┬─────┘
  │              │                │                      │                       │
  │              │ Query(ctx)     │                      │                       │
  │              │───────────────▶│                      │                       │
  │              │                │ Reset()              │                       │
  │              │                │─────────────────────▶│                       │
  │              │                │◀ - - - - - - - - - - │                       │
  │              │                │ TextQuery(ctx, chatₖ)│                       │
  │              │                │─────────────────────────────────────────────▶│
  │              │                │                      │ CallWithContext       │
  │              │                │                      │ (status="achieved")   │
  │              │                │                      │◀──────────────────────│
  │              │                │                      │ ─ verification err ──▶│
  │              │                │                      │   (Submission unset)  │
  │              │                │                      │                       │
  │              │                │                      │ CallWithContext       │
  │              │                │                      │ (status="blocked")    │
  │              │                │                      │◀──────────────────────│
  │              │                │                      │ ─ "recorded" ────────▶│
  │              │                │◀ - - chatₖ (completed) - - - - - - - - - - - │
  │              │                │ Submission()         │                       │
  │              │                │─────────────────────▶│                       │
  │              │                │◀ - (Blocked, true) - │                       │
  │              │◀ - - - - - - - │                      │                       │
  │              │  *LoopBlockedError                    │                       │
  │◀ - exit 1 - ─│                │                      │                       │
```

## Status board

**Line:** complete

| #   | Node               | Parent | Class       | Interface                                                                      | Status   |
| --- | ------------------ | ------ | ----------- | ------------------------------------------------------------------------------ | -------- |
| 1   | CompletionRecorder | root   | contract    | `internal/text:CompletionRecorder` (`internal/text/loop_tool.go`)              | Complete |
| 2   | LoopRunner         | root   | contract    | `internal/text:LoopRunner` (`internal/text/loop.go`)                           | Complete |
| 3   | Loop command       | root   | composition | root CLI interface: `clai [flags] loop\|l [flags] <text>` (`internal/cli:Run`) | Complete |

### Imports

| Member                                                                                 | Kind                        | Used by phases |
| -------------------------------------------------------------------------------------- | --------------------------- | -------------- |
| `pkg/text/models:LLMTool`, `Input`, `Specification`, `InputSchema`, `ParameterObject`  | repository types            | 1, 2           |
| `pkg/text/models:Chat`, `Message`                                                      | repository types            | 2, 3           |
| `internal/models:Querier`, `ChatQuerier`, `ToolBox`, `Summarizer`                      | repository interfaces       | 2, 3           |
| `internal/chatid:New`                                                                  | repository function         | 2              |
| `internal/utils:ContextCancelKey`                                                      | repository context key      | 2              |
| `internal/text:SetupQuerier`, `QueryCommandDeps`, `Configurations`, `Querier`          | repository functions/types  | 2, 3           |
| `internal:Command`, `TextFlags`, `NewTextFlags`, `IntFlag`, `NewIntFlag`, `StringFlag` | repository command/flag API | 3              |
| `internal/tools:Names`                                                                 | repository function         | 3              |
| `internal/cli:Commands`, `Run`, `DefaultDeps`                                          | repository functions        | 3              |
| `e2e:runOne`, `e2e:setupMainTestConfigDir`                                             | repository test helpers     | 3              |

## Scope

Out of scope, and no phase builds it:

- A separate judge or evaluator model call; completeness is signaled by the
  agent through `complete_task` and optionally gated by `-check`.
- New configuration files or new fields in `textConfig.json` or profile JSONs;
  loop settings are CLI flags (`-mi` / `-max-iterations`, `-check`)
  composed with existing `TextFlags`.
- Subagent spawning or parallel branch execution inside `clai loop`.
- New third-party libraries.

## Root facts

**F1 — Completion tool contract.** The loop completion tool is named
`complete_task`. It requires two string parameters: `status` (enum `"achieved"`
or `"blocked"`) and `summary` (non-empty after trimming whitespace). Calling
`complete_task` with missing, unknown, or whitespace-only values returns an
error and records no submission.

**F2 — Verification gate.** When `complete_task` is called with `status` equal
to `"achieved"` and a non-empty verification command is configured, the tool
executes that command with `sh -c <checkCmd>` bound to the call context. If the
command exits non-zero or fails to run, the tool returns an error containing
the command's combined stdout and stderr and does not record a submission. When
`status` is `"blocked"` or no verification command is configured, no shell
command is executed.

**F3 — Iteration termination and continuation.** An iteration ends with task
completion only when `CompletionRecorder.Submission` returns a recorded
`LoopSubmission` after `ChatQuerier.TextQuery` returns `nil`:

- `LoopStatusAchieved` ends the loop with `nil` (exit code 0).
- `LoopStatusBlocked` ends the loop with `*LoopBlockedError` matching
  `ErrLoopBlocked` via `errors.Is` and carrying `Summary` (exit code 1).
- No submission (`!ok`) means the task is not yet complete (for example after a
  `stoploss` handover or an intermediate plain reply); if further iterations
  remain, the outer loop starts the next iteration, otherwise it returns
  `*MaxIterationsError` matching `ErrMaxIterations` via `errors.Is` (exit code 1).

**F4 — Context isolation per iteration.** Each iteration runs `TextQuery` on a
child context derived from the caller's context with its own `context.WithCancel`
stored under `utils.ContextCancelKey`. A `models.StopEvent` inside an iteration
cancels only that iteration's child context, never the caller's context. If the
caller's own context is cancelled (`ctx.Err() != nil`), `LoopRunner.Query`
stops immediately and returns the error without starting another iteration.

**F5 — Seed preservation and `GroupKey` continuity.** Setup resolves stdin and
CLI prompt arguments once into the seed chat. Every iteration runs a chat
whose system message is the seed chat's system message with the loop
instructions appended. Iteration 1 runs the seed chat otherwise unchanged;
every subsequent iteration $k > 1$ constructs a chat with a fresh
`chatid.New()` ID and preserves the seed chat's messages up through its first
`user` message before appending the prior iteration's assistant handover
message and iteration $k$'s continuation `user` message (which includes the
goal text). Because the first `user` message is identical across all
iterations, every persisted iteration of a loop run receives the same
`GroupKey`. Every iteration chat also carries the seed chat's `Profile`, so a
loop run with `-p` persists the effective profile on each iteration.

**F6 — Additive tool registration.** The completion tool F1 names is registered
onto the querier via `RegisterExtraTool` so that it is available on every loop
run whether `-t` is unset, set to all tools, or set to a specific tool list,
without narrowing the user's selected tool catalog.

**F7 — Iteration bound.** `MaxIterations` bounds how many iterations one loop
run starts. A negative value is rejected with an error whose message contains
`max-iterations` before any model call runs; zero means no bound. A run that
starts the bound without an accepted completion ends as F3 specifies.

## Validation policy

### Gates

```sh
go run mvdan.cc/gofumpt@latest -w -l .
go run honnef.co/go/tools/cmd/staticcheck@latest ./...
go vet ./...
go test ./... -race -cover -count=3 -timeout=30s
go fix ./...
go run github.com/mibk/dupl@latest -t 80 .
```

### Human checks

None.

## Findings

| ID    | Severity | Node | Owner | Status      | Summary                                                           |
| ----- | -------- | ---- | ----- | ----------- | ----------------------------------------------------------------- |
| V1-01 | major    | root | plan  | Closed +wip | check 4 · root/phases 1-2 · add F7, move the mock scripting to L3 |

## Holistic review

Reviewed on 2026-10-21 after phases 1-3 reached complete (worker session
`clai-loop-review`).

| Fact | Evidence                                                                                                                            |
| ---- | ----------------------------------------------------------------------------------------------------------------------------------- |
| F1   | `NewCompletionRecorder` schema and validation; `TestCompletionRecorder_Validation`, `TestCompletionRecorder_AchievedWithoutCheck`   |
| F2   | `verify` gate on `achieved` only; `TestCompletionRecorder_CheckGate`, `TestCompletionRecorder_BlockedSkipsCheck`                    |
| F3   | submission switch and typed terminal errors; `TestLoopRunner_BlockedReturnsTypedError`, `TestLoopRunner_MaxIterationsAndValidation` |
| F4   | per-iteration child context under `utils.ContextCancelKey`; `TestLoopRunner_HandoverAndStopEventIsolation`                          |
| F5   | seed-preserving iteration chats; unit `GroupKey` assertion and `Test_e2e_loop_handover_and_check_gate_groups_chats`                 |
| F6   | `RegisterExtraTool` additive registration; `TestQuerier_RegisterExtraTool`, `Test_e2e_loop_achieves_goal` (no `-t`)                 |
| F7   | bound validation and exhaustion; `TestLoopRunner_MaxIterationsAndValidation`, `Test_e2e_loop_blocked_and_max_iterations`            |

Review decisions:

- D1 — The `-check` help text now says the gate applies only when `-check` is
  set; the previous wording implied that an unset command rejects `achieved`.
- D2 — `appendLoopInstructions` is idempotent: a seed chat resumed from an
  earlier loop run (for example with `-re`) already ends with the loop
  instructions, so appending them again would duplicate the block.

Gates on the final tree:

| Gate                                                                                                                  | Result                                                                                                                                                         |
| --------------------------------------------------------------------------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `go run mvdan.cc/gofumpt@latest -w -l .`                                                                              | passed                                                                                                                                                         |
| `go run honnef.co/go/tools/cmd/staticcheck@latest ./...`                                                              | passed                                                                                                                                                         |
| `go vet ./...`                                                                                                        | passed                                                                                                                                                         |
| `go fix ./...`                                                                                                        | passed                                                                                                                                                         |
| `go test ./... -race -cover -count=3 -timeout=30s`                                                                    | 30 s package alarm in `e2e`, `internal/audio`, `internal/text` and `internal/vendors` under host load (~10 on 8 cores); these packages pass alone and serially |
| `go test ./... -race -cover -count=3 -timeout=120s -p 1`                                                              | passed                                                                                                                                                         |
| `go test ./internal/text/ ./internal/vendors/ ./internal/cli/ ./internal/ ./e2e/ -race -cover -count=3 -timeout=120s` | passed after the review fixes                                                                                                                                  |
| `go run github.com/mibk/dupl@latest -t 80 internal/text internal/cli internal/vendors e2e internal`                   | no clones in the loop files; 36 pre-existing groups                                                                                                            |

Coverage: every function in `internal/text/loop_tool.go` is at 100%; every
function in `internal/text/loop.go` is at 100% except `iterationChat` (92.3%,
the `chatid.New` failure). `setupTextQuerier` keeps the pre-existing 66.7%
error branches.

### Second review pass

Reviewed on 2026-10-21 after the first pass (worker session
`clai-loop-review-2`).

Findings and fixes:

- D3 — The selected `-p` / `-prp` profile was not persisted on loop runs. The
  loop seed is `tConf.InitialChat`, while `NewQuerier` stamps the effective
  profile on its own chat copy. `setupTextQuerier` now mirrors that profile
  onto the seed, and `iterationChat` carries the seed profile into every later
  iteration. `Test_e2e_loop_persists_selected_profile` drives a two-iteration
  run with a profile and asserts both persisted chats.
- D4 — F7's `MaxIterations: 0` (unlimited) branch had no test. The
  `zero limit is unlimited` case in `TestLoopRunner_MaxIterationsAndValidation`
  now runs three iterations and ends on an accepted completion.
- D5 — The `tooling.run` comment claimed the map is never mutated after
  `NewQuerier`; `RegisterExtraTool` mutates it between queries, so the comment
  now says so. `TextFlagValues` now names `loop` too.
- D6 — `examples.md` documents the loop command; previously it appeared only
  in the architecture docs and `-h`.

Gates on the reviewed tree:

| Gate                                                                                                                  | Result                                                                                                                                                                                                                                                                                                                                                                  |
| --------------------------------------------------------------------------------------------------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `go run mvdan.cc/gofumpt@latest -w -l .`                                                                              | passed                                                                                                                                                                                                                                                                                                                                                                  |
| `go vet ./...`                                                                                                        | passed                                                                                                                                                                                                                                                                                                                                                                  |
| `go run honnef.co/go/tools/cmd/staticcheck@latest ./...`                                                              | passed                                                                                                                                                                                                                                                                                                                                                                  |
| `go fix ./...`                                                                                                        | passed                                                                                                                                                                                                                                                                                                                                                                  |
| `go run github.com/mibk/dupl@latest -t 80 internal/text internal/cli internal/vendors e2e internal`                   | passed; no clones in the loop files                                                                                                                                                                                                                                                                                                                                     |
| `go test ./internal/text/ ./internal/vendors/ ./internal/cli/ ./internal/ ./e2e/ -race -cover -count=3 -timeout=120s` | passed                                                                                                                                                                                                                                                                                                                                                                  |
| `go test ./... -race -cover -count=3 -timeout=30s`                                                                    | 30 s package alarm in `e2e` (`Test_e2e_setup_announcement_survives_interactive_wizard` in the final run) and `internal/audio` (`TestCalibrationStrictFailureHasNoStdout`) at host load 2.7 on 8 cores; an earlier run named `Test_e2e_chat_summarize_noninteractive_needs_yes` and `TestCoordinatorLiveBoardReadsStatesSafely`; both packages pass with `-timeout=120s` |

### Third review pass

Reviewed on 2026-10-21 after the second pass (worker session
`clai-loop-review-3`).

Findings and fixes:

- D7 — F6 named `-t` set to all tools and to a specific tool list, but only the
  unset case had an end-to-end test. `Test_e2e_loop_completion_tool_is_additive`
  now runs the loop with `-t *` and `-t ls` and asserts the persisted
  `complete_task` call.
- D8 — The `complete_task` description and the loop instructions said "exactly
  once", which conflicts with F2: a failed `-check` rejects the achieved call
  and the model must retry. Both now tell the model to continue and call again
  after the command passes. `TestCompletionRecorder_CheckGate` gained a
  `rejected achieved call can be retried` case.
- D9 — D2's idempotent instruction append had only the helper-level test;
  `TestLoopRunner_IterationChatDoesNotDuplicateLoopInstructions` now asserts
  `iterationChat` leaves one instruction block on a seed that already carries
  it.
- D10 — Documentation drift: the README flow said `"accepted"` while the tool
  returns `"recorded"`; `internal/flags.go`, `internal/text/querier.go`,
  `internal/audio/tool_overrides.go`, `architecture/cmd-dispatch.md`,
  `architecture/query.md`, `architecture/audio.md` and
  `architecture/summaries.md` still described loop as query-only or omitted it
  from shared flag, completion and summarizer lists.

Gates on the reviewed tree:

| Gate                                                                                                                  | Result                                                                                                                                                                                                                                 |
| --------------------------------------------------------------------------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `go run mvdan.cc/gofumpt@latest -w -l .`                                                                              | passed                                                                                                                                                                                                                                 |
| `go vet ./...`                                                                                                        | passed                                                                                                                                                                                                                                 |
| `go run honnef.co/go/tools/cmd/staticcheck@latest ./...`                                                              | passed                                                                                                                                                                                                                                 |
| `go fix ./...`                                                                                                        | passed                                                                                                                                                                                                                                 |
| `go run github.com/mibk/dupl@latest -t 80 internal/text internal/cli internal/vendors e2e internal`                   | passed; 36 pre-existing groups, no loop clones                                                                                                                                                                                         |
| `go test ./internal/text/ ./internal/vendors/ ./internal/cli/ ./internal/ ./e2e/ -race -cover -count=3 -timeout=120s` | passed: text 23.6 s / 85.9%, vendors 9.5 s / 83.8%, cli 1.1 s / 73.2%, internal 1.1 s / 80.9%, e2e 25.1 s                                                                                                                              |
| `go test ./e2e/ -run Test_e2e_loop -race -count=3 -timeout=120s`                                                      | passed: 1.4 s                                                                                                                                                                                                                          |
| `go test ./internal/audio/ -race -cover -count=3 -timeout=120s`                                                       | passed: 17.5 s / 88.8%                                                                                                                                                                                                                 |
| `go test ./... -race -cover -count=3 -timeout=30s`                                                                    | failed twice with the 30 s package alarm in `e2e` (`Test_e2e_stoploss_flag_overrides_file`) and `internal/audio` (`TestCalibrationStrictFailureHasNoStdout`) at host load 4.4-6.3 on 8 cores; both packages pass the scoped runs above |

The strict alarm is the known host-load pattern recorded by the first two
reviews; no test was skipped or edited.

### Fourth review pass

Reviewed on 2026-10-23 on the loop display tree (worker session
`clai-loop-review-4`). The display layer added after the third pass
(`internal/text/loop_display.go`, span-coloured headers, the summarizing
placeholder) was audited against its call order.

Findings and fixes:

- D11 — The pending placeholder was decided in `SetLoopProgress`, which the
  runner calls before `iterationChat` builds the iteration's chat. The decision
  therefore read the previous iteration's chat: a resumed seed that already
  carried a summary showed `summarizing…` although `shouldLaunchSummary` refuses
  it, and iteration 2+ launched a summarizer without ever showing the
  placeholder. `SetLoopProgress` now stores progress unchanged and the new
  `markLoopPhasePending` copies `shouldLaunchSummary` on the iteration's own
  chat; `Query` calls it before the first frame, next to `launchSummary`.
  `TestQuerier_MarkLoopPhasePendingMatchesTheSummaryLaunch` pins the four
  cases and `TestQuerier_QueryDecidesThePendingPhaseFromTheIterationChat` pins
  the ordering through `TextQuery` for a fresh iteration and a resumed seed.
- D12 — The bounded join was written twice: `awaitLoopPhaseBeforeSettle` and
  `joinSummary` each resolved the bound and selected over result, timer and
  interrupt. Both now use `waitForSummary`, which returns a typed `summaryWait`,
  and `summaryJoinBound`. Each caller keeps its own result handling and trace
  line.
- D13 — Documentation drift: `architecture/colours.md` still called `loopTitle`
  the colour of the whole progress row, while the code colours only the
  iteration marker; `architecture/query.md` now states that the placeholder
  appears exactly when a summarizer launches for that iteration.
- D14 — Accepted, not changed: `w := q.out; if w == nil { w = os.Stdout }`
  repeats eleven times across `internal/text`, and `dupl -t 80` reports no group
  for it. A loop-only helper would make the package inconsistent, so the
  pattern waits for one package-wide cleanup.

Gates on the reviewed tree:

| Gate                                                                     | Result                                                                    |
| ------------------------------------------------------------------------ | ------------------------------------------------------------------------- |
| `go run mvdan.cc/gofumpt@latest -w -l .`                                 | passed                                                                    |
| `go vet ./...`                                                           | passed                                                                    |
| `go run honnef.co/go/tools/cmd/staticcheck@latest ./...`                 | passed                                                                    |
| `go fix ./...`                                                           | passed; no working-tree change                                            |
| `go run github.com/mibk/dupl@latest -t 80 .`                             | passed; no group touches `loop_display.go`, `summary_launch.go` or `loop.go` |
| `go test ./... -race -count=3 -timeout=30s`                              | passed                                                                    |
| `go test ./... -race -cover -count=3 -timeout=30s`                       | the 30 s package alarm hit `e2e` and `internal/audio`; no test logic failed |
| `go test ./e2e/ ./internal/audio/ -race -cover -count=3 -timeout=120s`   | passed: e2e 23.4 s, audio 17.5 s / 88.8%                                   |
| `go test ./internal/text/ ./internal/utils/ ./internal/summary/ -race -count=3 -timeout=30s` | passed: text 23.5 s, utils 2.0 s, summary 1.4 s |

## Session journal

- 2026-10-21 · plan · README drafted
- 2026-10-21 · validate · V1 Not ready · `clai -p subagent q`: 1 major open, 6 withdrawn · notes: 8
- 2026-10-21 · validate · V2 Not ready · `clai -p subagent-investigation q`: V1-01 open, 6 withdrawn, 8 notes · notes: 8
- 2026-10-21 · plan · README amended: Root facts, Flows
- 2026-10-21 · plan · phase 1 re-planned, phase 2 re-planned, phase 3 re-planned
- 2026-10-21 · validate · V3 Ready · `go build ./internal/text/` (repo copy, assembled interfaces 1-3 plus `var _ LoopQuerier = &Querier[*vendors.Mock]{}`): ok · notes: 0
- 2026-10-21 · validate · V4 Ready · `go build ./internal/text/` (repo copy, assembled interfaces 1-3 plus `var _ LoopQuerier = &Querier[*vendors.Mock]{}`): ok · notes: 0 · ran at maintainer request after a Ready verdict
- 2026-10-21 · implement · phase 1 Complete · `go test ./internal/text/ ./internal/vendors/ -race -count=3 -cover -timeout=30s` passed; full `make qa` test phase hit host-load timeouts, `-p 1` passed · worker session `clai-loop-phase-1`
- 2026-10-21 · implement · phase 2 Complete · `go test ./internal/text/ ./internal/vendors/ -race -count=3 -cover -timeout=30s` and `make qa` passed; one earlier exact run hit a transient host-load e2e `captureStdoutStderr` race · worker session `clai-loop-phase-2`
- 2026-10-21 · implement · phase 3 Complete · first strict run caught a `-cm` regression in the shared text-flag refactor (parsed into a value copy, fixed with a pointer); after the fix the exact `go test ./... -race -cover -count=3 -timeout=30s` passed once, later attempts hit the 30 s package alarm in five heavy packages under host load, which pass `-count=3 -cover -race -p 1 -timeout=120s` · worker session `clai-loop-phase-3`
- 2026-10-21 · review · holistic review · F1-F7 traced to code and tests; two review fixes (`-check` help wording, idempotent loop instructions); `go test ./... -race -cover -count=3 -timeout=120s -p 1` and the scoped `-count=3` suite passed, the exact 30 s command hit host-load alarms · worker session `clai-loop-review`
- 2026-10-21 · review · second holistic review · D3 profile persistence fix (seed stamp in `setupTextQuerier` plus `iterationChat` profile carry), D4 `-mi 0` test, D5 comment drift, D6 `examples.md`; scoped `-count=3` suite, gofumpt, vet, staticcheck, fix and dupl passed; the exact 30 s suite hit the e2e/audio alarms · worker session `clai-loop-review-2`
- 2026-10-21 · review · third holistic review · D7 `-t *` / `-t ls` F6 e2e test, D8 retry wording for rejected `-check`, D9 D2 iteration-chat test, D10 command-list and flow documentation drift; scoped `-count=3` suite, loop e2e, audio, gofumpt, vet, staticcheck, fix and dupl passed; the exact 30 s suite hit the same e2e/audio alarms · worker session `clai-loop-review-3`
- 2026-10-22 · validate · V5 Ready · python3 inline tree/section audit: passed; `GO111MODULE=off go build -o /tmp/clai-loop-v5-validation/check /tmp/clai-loop-v5-validation/interfaces.go`: passed; `rg`/`go doc`/source inspection (consumed signatures, clauses, flows, provenance, human requirements): passed; `go test ./internal/text ./internal/cli -race -count=1 -timeout=120s`: passed; `go test ./e2e -run '^Test_e2e_loop' -race -count=1 -timeout=120s`: passed; prior V1-01 resolved · notes: 0
- 2026-10-23 · review · fourth holistic review · D11 pending placeholder decided on the iteration's own chat in `Query` (two stray-placeholder directions closed), D12 shared `waitForSummary`/`summaryWait`, D13 `loopTitle` and placeholder documentation drift, D14 accepted `q.out` repetition; gofumpt, vet, staticcheck, fix and dupl passed, `go test ./... -race -count=3 -timeout=30s` passed, the `-cover` variant hit the known e2e/audio 30 s alarm and both packages pass at 120 s · worker session `clai-loop-review-4`
