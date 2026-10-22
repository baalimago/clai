# Phase 3 — Loop command

**Parent:** root · **Class:** composition · [README](README.md)

## Goal

Expose `clai [flags] loop|l [flags] <text>` on the CLI with `-mi` /
`-max-iterations` and `-check` flags, wired end-to-end through
`internal/text` and `internal/cli`.

## Interface

Written to `internal/flags.go`, `internal/text/cmd.go`, and `internal/cli/cli.go`.

```text
loop.1  clai [flags] loop|l [-mi|-max-iterations N] [-check CMD] <goal>
        runs LoopRunner over the configured text querier and exits 0 when
        complete_task records achieved (with CMD exiting 0 when set)
        (F1, F2, F3, F4, F5, F6, L1, L2)

loop.2  clai [flags] loop|l -check CMD <goal>
        when CMD exits non-zero on iteration 1 and exits 0 on iteration 2,
        continues across iterations, persists each iteration with an identical
        GroupKey, and exits 0 (F2, F3, F4, F5, L1)

loop.3  clai [flags] loop|l [-mi N] <goal>
        when the model records blocked or exhausts N iterations without
        achieving the goal, or when -mi is negative, prints the error to
        stderr and exits 1 (F3, F7, L1)

loop.4  clai loop -h  /  clai l -h  /  clai (no args)
        lists loop|l in the top-level command table and prints loop's help
        text, examples, TextFlags, and LoopFlags (-mi/-max-iterations,
        -check) on -h; exits 0 on -h (L1, L2, L3)
```

Consumes: `internal/text:NewCompletionRecorder`,
`internal/text:CompletionRecorder` (phase 1); `internal/text:NewLoopRunner`,
`internal/text:LoopConfig`, `internal/text:LoopRunner`,
`internal/text:ErrLoopBlocked`, `internal/text:ErrMaxIterations`,
`internal/text:LoopQuerier` (phase 2);
`internal/text:SetupQuerier`, `internal/text:QueryCommandDeps`,
`internal/text:Configurations` (import);
`internal:Command`, `internal:TextFlags`, `internal:NewTextFlags`,
`internal:IntFlag`, `internal:NewIntFlag`, `internal:StringFlag` (import);
`internal/tools:Names` (import); `internal/cli:Commands`, `internal/cli:Run`,
`internal/cli:DefaultDeps` (import); `e2e:runOne`, `e2e:run`,
`e2e:captureStdoutStderr`, `e2e:setupMainTestConfigDir` (import);
`internal/vendors:Mock` `complete_task` scripting (phase 1, L3; recorded
fixture).

## Acceptance criteria

| #   | Criterion                                                                                                            | Proves                                                                                            | Proof                                                |
| --- | -------------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------- | ---------------------------------------------------- |
| 1   | `clai l` achieves a goal in one iteration when `complete_task` succeeds and exits 0                                  | `loop.1`, `F1`, `F3`, `F6`                                                                        | `Test_e2e_loop_achieves_goal`                        |
| 2   | `clai loop -check` rejects a failing verification in iteration 1, succeeds in iteration 2, and shares one `GroupKey` | `loop.2`, `F2`, `F4`, `F5`                                                                        | `Test_e2e_loop_handover_and_check_gate_groups_chats` |
| 3   | `clai loop` exits 1 with a descriptive error when blocked, when `-mi` is exhausted, or when `-mi` is negative        | `loop.3`, `F3`, `F7`, `L1`                                                                        | `Test_e2e_loop_blocked_and_max_iterations`           |
| 4   | `loop\|l` is registered in `cli.Commands`, appears in bare `clai` usage, and prints its flags on `clai l -h`        | `loop.4`, `L1`, `L2`, `L3` | `Test_e2e_loop_help_and_dispatch`                    |

## Test cases

| Test                                                 | Level                | Proves                                 | Given                                                                   | Then                                                                                         | Must not                                   |
| ---------------------------------------------------- | -------------------- | -------------------------------------- | ----------------------------------------------------------------------- | -------------------------------------------------------------------------------------------- | ------------------------------------------ |
| `Test_e2e_loop_achieves_goal`                        | integration scenario | `loop.1`, `F1`, `F3`, `F6`, AC 1       | `-r -cm test l run tool_complete_task`                                  | Exits 0 and persists the completed conversation                                              | Require `-t` to be set for `complete_task` |
| `Test_e2e_loop_handover_and_check_gate_groups_chats` | integration scenario | `loop.2`, `F2`, `F4`, `F5`, `L1`, AC 2 | `-r -cm test -check <fail-once-script> l run tool_complete_task`        | Runs 2 iterations, exits 0, and both saved conversations have identical non-empty `GroupKey` | Cancel iteration 2 when iteration 1 stops  |
| `Test_e2e_loop_persists_selected_profile`             | integration scenario | `loop.2`, `F5`, AC 2                   | `-r -cm test -p <profile> -check <fail-once-script> l run tool_complete_task` | Runs 2 iterations, exits 0, and both saved conversations carry the selected profile             | Lose the profile in later iterations       |
| `Test_e2e_loop_blocked_and_max_iterations`           | integration scenario | `loop.3`, `F3`, `F7`, `L1`, AC 3       | Blocked mock status, `-mi 2` without `tool_complete_task`, and `-mi -1` | Each exits 1 and writes the corresponding error to stderr                                    | Exit 0 on unachieved or blocked runs       |
| `Test_e2e_loop_help_and_dispatch`                    | integration scenario | `loop.4`, `L1`, `L2`, `L3`, AC 4       | `clai l -h`, `clai loop -h`, and `cli.Commands(cli.DefaultDeps())`      | Usage and `-h` list `loop\|l`, `-mi`, `-max-iterations`, `-check`                            | Omit `TextFlags` or `LoopFlags` from `-h`  |

## Error management

| Failure                                    | Expected outcome                                              | Test                                       |
| ------------------------------------------ | ------------------------------------------------------------- | ------------------------------------------ |
| `-mi` / `-max-iterations` is negative      | Error printed to stderr naming `-max-iterations`; exit code 1 | `Test_e2e_loop_blocked_and_max_iterations` |
| Model calls `complete_task` with `blocked` | `*LoopBlockedError` printed to stderr; exit code 1            | `Test_e2e_loop_blocked_and_max_iterations` |
| Iterations reach `-mi` without achievement | `*MaxIterationsError` printed to stderr; exit code 1          | `Test_e2e_loop_blocked_and_max_iterations` |

## Golden diff

| Golden                                                           | Change                                                       |
| ---------------------------------------------------------------- | ------------------------------------------------------------ |
| `internal/cli/cli_test.go:TestCommands_buildsEveryDocumentedKey` | Adds `"loop\|l"` to the expected command map keys            |
| `e2e/help_e2e_test.go:Test_goldenFile_usage_on_no_args`          | Asserts `"loop\|l"` in top-level usage output                |
| `e2e/dispatch_e2e_test.go:Test_e2e_command_help`                 | Adds `{"l -h", "loop <text>"}` to the per-command `-h` table |
| `e2e/loop_e2e_test.go` (new)                                     | End-to-end scenarios for `loop.1`–`loop.4`                   |

## Removes

None.

## Local facts

**L1 — `LoopFlags` group.** `internal/flags.go` defines `LoopFlags` with
`MaxIterations IntFlag` (default `10`, registered under `"mi"` and
`"max-iterations"`) and `Check StringFlag` (default `""`, registered under
`"check"`).

**L2 — `LoopCommand` wiring.** `text.LoopCommand(deps QueryCommandDeps)` in
`internal/text/cmd.go` registers `TextFlags` plus `LoopFlags`, runs the same
config/media/summarizer setup as `QueryCommand`, and wraps the querier in
`NewLoopRunner`, which owns completion-tool registration. The seed chat is
`tConf.InitialChat`; the goal text is `tConf.PostProccessedPrompt`. The
composition reaches its `LoopQuerier` through a checked type assertion of the
`models.Querier` that `SetupQuerier` returns; a failed assertion returns an
error naming the concrete type, a path the CLI never reaches because every
`CreateQuerier` branch returns `*Querier[C]`. `internal/cli/cli.go` registers
`"loop|l"` in `Commands`.

**L3 — Documentation.** `architecture/cmd-dispatch.md` and `architecture/query.md`
document the `loop|l` command and `LoopFlags` group.

## Human required

None.

## Implementation notes

| Run   | Commit      | Command                                                                                                                                                                                                                                                      | Result                                                                                                           |
| ----- | ----------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ | ---------------------------------------------------------------------------------------------------------------- |
| start | c95cf6c+wip | `go test ./internal/text/ ./internal/vendors/ ./internal/cli/ -race -count=1 -timeout=120s`                                                                                                                                                                  | passed                                                                                                           |
| red   | c95cf6c+wip | `go test ./e2e/ -run Test_e2e_loop -race -count=1 -timeout=120s`                                                                                                                                                                                             | failed: `loop` was not a command                                                                                 |
| end   | c95cf6c+wip | `go test ./internal/text/ ./internal/cli/ ./e2e/ -run 'Test_newLoopRunner_rejectsQuerierWithoutLoopSupport\|TestLoopCommand_registersTextAndLoopFlags\|Test_e2e_loop\|TestCommands_buildsEveryDocumentedKey\|Test_loopFlagsRegistration' -race -count=3 -timeout=120s` | passed                                                                                                           |
| end   | c95cf6c+wip | `go test ./... -race -cover -count=3 -timeout=30s`                                                                                                                                                                                                           | passed at 19:05 EEST; later identical runs timed out under host load, see Retry note                            |
| end   | c95cf6c+wip | `go test ./e2e/ ./internal/audio/ -race -cover -count=3 -timeout=120s`                                                                                                                                                                                       | passed; 23.4 s and 17.8 s                                                                                        |
| end   | c95cf6c+wip | `go run mvdan.cc/gofumpt@latest -w -l .`                                                                                                                                                                                                                     | passed                                                                                                           |
| end   | c95cf6c+wip | `go run honnef.co/go/tools/cmd/staticcheck@latest ./...`                                                                                                                                                                                                      | passed                                                                                                           |
| end   | c95cf6c+wip | `go vet ./...`                                                                                                                                                                                                                                               | passed                                                                                                           |
| end   | c95cf6c+wip | `go fix ./...`                                                                                                                                                                                                                                               | passed                                                                                                           |
| end   | c95cf6c+wip | `go run github.com/mibk/dupl@latest -t 80 .`                                                                                                                                                                                                                 | no clones in the changed files; the remaining 228 groups are pre-existing, mostly the `.claude/worktrees` copy |

`go tool cover -func` over `internal/text`, `internal/cli` and `internal`
reports 100% for `LoopFlags`, `newTextCommand` and every function in
`internal/text/loop.go` except `iterationChat` (92.3%, the `chatid.New`
failure). `LoopCommand` shows 38.5% because its success closure runs in the
e2e binary; `setupTextQuerier` keeps the pre-existing 66.7% error branches.
The three-package total is 85.3%.

Decisions:

- D1 — 2026-10-21, worker session `clai-loop-phase-3`: query and loop share
  `newTextCommand` and `setupTextQuerier`, so both commands expose the same
  TextFlags composition and the query path is unchanged. `newTextCommand`
  takes `*internal.TextFlags` because the register closure and the caller's
  setup closure must read one parsed value set.
- D2 — `newLoopRunner` performs the checked `LoopQuerier` assertion and
  returns an error naming the concrete type (`%T`). Every `CreateQuerier`
  branch returns `*Querier[C]`, so the CLI never takes the error path.
- D3 — `LoopFlags` defaults `-mi` to 10 and `-check` to empty, matching L1.
  The verification script in the e2e suite fails once and then passes, which
  proves the check gate rejects iteration 1 without cancelling iteration 2.
- D4 — The e2e rows assert persisted conversations and their `GroupKey`
  instead of stdout, so they pin F5 and F2 without depending on display mode.

Retry note: the first strict run caught a real regression, not host load: the
initial shared-constructor refactor passed `internal.TextFlags` by value, so
`Register` wrote parsed flags into a copy while `OnSetup` read the caller's
defaults. Every flag silently fell back to the config file, and tests that
expected the mock answered with real `gpt-5.2` output. `newTextCommand` now
takes a pointer. After the fix the exact strict command passed once. Later
attempts, including the final tree, exceeded the 30 s per-package alarm in
`e2e`, `internal/audio`, `internal/text`, `internal/tools/mcp` and
`internal/vendors` while the suite drove the host load average to 18–22 on
8 cores. Every alarm named an unrelated test; one attempt also failed the
pre-existing `TestStdioConnReapKillsGrandchildOnSelfExit` under that load.
On the final tree those five packages pass
`go test ./e2e/ ./internal/audio/ ./internal/text/ ./internal/tools/mcp/ ./internal/vendors/ -race -cover -count=3 -timeout=120s -p 1`
(e2e 26.2 s, audio 21.7 s, text 27.8 s, mcp 19.1 s, vendors 14.5 s), and the
loop rows pass `-count=3` in 1.3 s. The host, not the change, is the
bottleneck: `internal/text` alone already needs 27.8 s under the strict flags.

Surprises: none beyond the value-copy regression above.

## Review findings

None.
