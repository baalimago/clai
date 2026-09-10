# Phase 1 — In-process querier gaps

**Status:** Complete

[← README](./README.md)

## Goal

Make a one-off `text.Querier` built inside the CLI process safe to run
without persisting, without starting ambient MCP servers, without altering
any other run's command-ban policy, and with usage accounting that survives
the absence of persistence.

## Specification

Four changes in `internal/text`, one in `pkg/tools`, one in `pkg/agent`,
one in `pkg/text/models`, one in `internal/chat`. Defaults are unchanged
for every existing path (D18, D19, D23, D29); the only observable change
for a library caller is that `pkgtools.SetCmdBanList` no longer exists.

### `Configurations.SkipAmbientMcpServers`

- `internal/text/conf.go`: add `SkipAmbientMcpServers bool` with
  `json:"-"` next to `McpServers`. Documented as: when set, the run
  discovers no server from `<configDir>/mcpServers/*.json`; servers passed
  in `McpServers` still start and keep their explicit/ambient posture.
- `internal/text/querier_setup_tools.go`: `setupMcpManager` takes the flag
  into account before globbing the directory. When set, the config-dir file
  list is empty and only `userConf.McpServers` reach the manager; when that
  list is also empty the function returns the empty tool map without
  starting the manager goroutine, exactly as the no-servers branch does
  today. The "MCP servers directory not found" error is not raised when the
  flag is set (a summarizer must not depend on that directory existing).

### Cost enrichment decoupled from persistence

- `internal/text/finalizer.go`: `costEnricher.enrich` runs whenever
  `session.Chat.TokenUsage` is non-nil, before the `ShouldSaveReply` branch,
  so a non-persisting querier returns a chat whose `Queries` carry the run's
  usage and cost. The persistence branch keeps origin stamping,
  `SaveAsPreviousQuery` and the dirscope write unchanged.
- Behavior change for CLI configs with `save-reply-as-prompt: false`: the
  returned chat gains cost rows in memory; nothing is written. The bounded
  readiness wait inside `enrich` applies to that path as it does to every
  persisting run today.

### Command-ban policy on the tool-call context (D29)

The package-global ban list is removed; the policy travels from the
configuration to the one place tools are invoked.

- `pkg/tools/cmd_ban.go`: delete `cmdBanList`, `cmdBanMu`, `SetCmdBanList`
  and `ResetCmdBanListForTests`. `validateCmdNotBannedWithContext` reads
  only the context policy installed by `WithCmdBanContext`; a context
  without a policy is permissive. `validateCmdNotBanned` (no context) is
  deleted with its callers. The doc comment on `FreetextCmdTool.Call`
  states that the context-free entry point enforces no policy and that
  enforcement requires `CallWithContext`; `WithCmdBanContext` keeps its
  snapshot-copy contract (the old worklog's "Ban-list ownership").
- `internal/text/querier_setup.go`: `NewQuerier` no longer calls
  `SetCmdBanList`; it copies `userConf.CmdBan` onto `Querier.cmdBan`
  (snapshot copy, same rule as the old setter).
- `internal/text/tool_executor.go`: the single `tools.InvokeWith` site
  invokes on `pkgtools.WithCmdBanContext(ctx, q.cmdBan)`. Lookback tools
  and the recorder are untouched.
- `pkg/agent/run.go`: `Agent.Query` stops attaching `a.cmdBan`; the list
  already reaches the querier through `asInternalConfig` (`CmdBan:
  a.cmdBan`). The `WithCmdBanList` doc comment drops the sentence about a
  package-level fallback.
- `internal/audio/tool_overrides.go`: the doc comment on
  `transcribeOverride` stops citing `pkgtools.SetCmdBanList` as its
  pattern (the override itself is out of scope, see the README).
- Documentation changes with the code, in this phase:
  `architecture/tooling.md` "Command ban list" describes the context-carried
  policy and the permissive context-free `Call`; `architecture/config.md`
  (Security, "Command ban list" cross-reference) loses any mention of a
  process default; `pkg/agent` option docs as above.
- Existing tests move to the context path in this phase:
  `pkg/tools/cmd_ban_enforcement_test.go` (`TestCmdBanEnforcement_SetAndReset`,
  `TestCmdBanEnforcement_SetterSnapshotsInput` and
  `TestCmdBanEnforcement_ContextPolicyOverridesGlobalPolicy` are replaced by
  the rows below; `TestCmdBanEnforcement_ValidateCmdNotBanned` re-targets
  `validateCmdNotBannedWithContext` with a policy context installed by
  `WithCmdBanContext`; `TestCmdBanEnforcement_DefaultPermissive` is folded
  into `TestCmdBanEnforcement_NoPolicyPermissive`; the remaining
  `TestCmdBanEnforcement_*` tests install the policy with
  `WithCmdBanContext`), `internal/text/querier_setup_cmd_ban_test.go`
  (`Test_NewQuerier_AppliesCmdBanList`, `Test_NewQuerier_NoCmdBanStaysPermissive`
  assert `Querier.cmdBan`), `main_cmd_ban_e2e_test.go` (the seven
  `Test_e2e_cmd_ban_*` tests drop the reset calls; their assertions are
  unchanged), `pkg/agent/cmd_ban_e2e_test.go` (the four `TestAgentCmdBan_*`
  tests drop the reset calls; the concurrency comments describe per-run
  contexts, not overlapping setter writes) and `pkg/agent/recorder_test.go`
  (`TestAgent_WithToolCallRecorder_Error` drops its reset cleanup).

#### Policy table

| Actor                                                    | Mechanism                                                                       | Test                                                   |
| -------------------------------------------------------- | ------------------------------------------------------------------------------- | ------------------------------------------------------ |
| CLI query (`-cmd-ban`, config, profile)                  | `Configurations.CmdBan` → `Querier.cmdBan` → executor context                   | `Test_e2e_cmd_ban_flag_path`, `Test_e2e_cmd_ban_config_file_path`, `Test_e2e_cmd_ban_profile_path`, `Test_e2e_cmd_ban_profile_merges_onto_file_base` |
| Embedded agent (`agent.WithCmdBanList`)                  | `asInternalConfig` → `Configurations.CmdBan` → same path; no attach in `run.go` | `TestAgentCmdBan_SingleAgentRefusal`, `TestAgentCmdBan_SequentialPerRunIsolation` |
| Concurrent runs with distinct lists                      | Each executor attaches its own querier's list                                   | `TestAgentCmdBan_ConcurrentDistinctLists`, `TestAgentCmdBan_ConcurrentPermissiveAndBanned` |
| A second querier constructed mid-run with an empty list  | Construction writes no shared state                                             | `TestToolExecutor_secondQuerierDoesNotAlterPolicy`     |
| `cmd` and `async_cmd` spawn points                       | `validateCmdNotBannedWithContext` reads the context policy only                 | `TestCmdBanEnforcement_FreetextRefusesBannedBeforeSpawn`, `TestCmdBanEnforcement_AsyncRefusesBannedBeforeSpawn` |
| Context without a policy, and context-free `Call`        | Permissive; documented                                                          | `TestCmdBanEnforcement_NoPolicyPermissive`             |
| `WithCmdBanContext` snapshot                             | Copies its input slice                                                          | `TestCmdBanEnforcement_ContextPolicySnapshotsInput`    |

### `QueryCost.Purpose`

- `pkg/text/models/chat.go`: add `Purpose string` with
  `json:"purpose,omitempty"` to `QueryCost`. Empty for every row produced by
  the cost manager; `"summary"` for rows a summarizer contributes (stamped
  by phase 2, appended by phases 4 and 5).
- `internal/chat/index.go`: `chatIndexRowFromChat` resolves `row.Model`
  from the newest `Queries` entry whose `Model` is non-empty **and whose
  `Purpose` is empty**. Token and cost aggregates keep summing every row.

### Invariants of this phase

| Invariant                                                                 | Mechanism                                                       | Test                                                        |
| ------------------------------------------------------------------------- | --------------------------------------------------------------- | ----------------------------------------------------------- |
| A querier with `SkipAmbientMcpServers` starts no config-dir server         | Directory glob skipped in `setupMcpManager`                     | `TestSetupMcpManager_skipAmbient_startsNoConfigDirServer`   |
| Explicit `McpServers` still start under the flag                          | `userConf.McpServers` appended regardless                        | `TestSetupMcpManager_skipAmbient_keepsExplicitServers`      |
| A missing `mcpServers` directory is not an error under the flag           | Stat check bypassed when the flag is set                         | `TestSetupMcpManager_skipAmbient_toleratesMissingDir`       |
| Injected `Tools` register without a stderr warning                        | Existing `userConf.Tools` loop; no globs                          | `TestSetupTooling_injectedToolsRegisterWithoutWarning`      |
| Enrichment runs without persistence                                       | `enrich` hoisted above the persistence branch                    | `TestFinalize_enrichesWithoutPersistence`                   |
| Nothing is written without persistence                                    | Persistence branch untouched                                     | `TestFinalize_withoutPersistence_writesNothing`             |
| Persisting runs still enrich exactly once                                 | Single `enrich` call site                                        | `TestFinalize_persistingRunEnrichesOnce`                    |
| `Purpose` round-trips and is omitted when empty                           | `omitempty` tag                                                  | `TestQueryCost_purposeRoundTrip`                            |
| Index model resolution skips summary rows                                 | `Purpose == ""` guard in `chatIndexRowFromChat`                  | `TestChatIndexRowFromChat_modelSkipsSummaryRows`            |
| The executor attaches the querier's own ban list to every tool call      | `WithCmdBanContext(ctx, q.cmdBan)` at the single invoke site     | `TestToolExecutor_attachesCmdBanPolicy`                     |
| Constructing a querier alters no other run's policy                      | No package-level ban state exists                                | `TestToolExecutor_secondQuerierDoesNotAlterPolicy`          |

## Integration contract

| Trigger                                                                                   | Collaborators / fakes                                          | Observable result                                                | Required side effects                       | Prohibited side effects                                                     |
| ----------------------------------------------------------------------------------------- | -------------------------------------------------------------- | ---------------------------------------------------------------- | ------------------------------------------- | --------------------------------------------------------------------------- |
| `NewQuerier` with `Model: "test"`, `SkipAmbientMcpServers`, one injected tool, a temp config dir holding one `mcpServers/*.json` whose command is `false` | Mock vendor; real `setupTooling`             | Querier builds; the injected tool is the only registered tool     | None                                        | No MCP client spawned; no `failed to setup` warning on stderr               |
| Same querier runs `TextQuery` with `SaveReplyAsConv: false`                               | Mock vendor; temp config dir with a price file for `mock_test_test` | Returned chat has exactly one `Queries` row with usage and cost | None                                        | No `conversations/*.json`, no `globalScope.json`, no `dirs/*.json`, no `chat_index.cache` |
| Querier A built with `CmdBan: ["touch"]` and the `cmd` tool; querier B then built with an empty `CmdBan`; A runs `TextQuery` over a prompt holding `tool_cmd` with `CLAI_MOCK_CMD_COMMAND` = `touch <marker>` | Mock vendor for both; real `cmd` tool | A's tool result starts with `ERROR:` and names `touch`; the marker file does not exist | None | No shared state written by B's construction; B's own `tool_cmd` run creates its marker (permissive) |

## Acceptance criteria

| Outcome                                                                                       | Test                                                                                    |
| --------------------------------------------------------------------------------------------- | --------------------------------------------------------------------------------------- |
| Flag off: `setupMcpManager` behaves exactly as today (existing tests unchanged)               | Existing `internal/text` MCP startup tests                                              |
| Flag on: no config-dir server started, explicit ones started, missing dir tolerated           | `TestSetupMcpManager_skipAmbient_startsNoConfigDirServer`, `TestSetupMcpManager_skipAmbient_keepsExplicitServers`, `TestSetupMcpManager_skipAmbient_toleratesMissingDir` |
| Injected tools register silently                                                              | `TestSetupTooling_injectedToolsRegisterWithoutWarning`                                  |
| Non-persisting finalize enriches and writes nothing                                           | `TestFinalize_enrichesWithoutPersistence`, `TestFinalize_withoutPersistence_writesNothing` |
| Persisting finalize unchanged: one enrichment, one save, one binding write                    | `TestFinalize_persistingRunEnrichesOnce`, existing finalizer tests                      |
| `Purpose` serializes only when set                                                            | `TestQueryCost_purposeRoundTrip`                                                        |
| A chat whose last cost row is a summary row indexes the main model                            | `TestChatIndexRowFromChat_modelSkipsSummaryRows`                                        |
| Policy table, row by row                                                                      | The tests named in the policy table                                                     |
| `NewQuerier` keeps the list on the querier and touches no global                              | `Test_NewQuerier_AppliesCmdBanList`, `Test_NewQuerier_NoCmdBanStaysPermissive` (rewritten) |
| Executor attaches the policy                                                                  | `TestToolExecutor_attachesCmdBanPolicy` (`internal/text/tool_executor_test.go`)         |
| No symbol named `SetCmdBanList`, `ResetCmdBanListForTests` or `cmdBanList` remains            | `grep -rn 'SetCmdBanList\|ResetCmdBanListForTests\|cmdBanList' --include='*.go' .` prints nothing |
| End-to-end: integration rows one and two                                                      | `TestNewQuerier_oneOffQuerier_isSideEffectFree` (`internal/text/querier_setup_test.go`) |
| End-to-end: integration row three                                                             | `TestToolExecutor_secondQuerierDoesNotAlterPolicy` (`internal/text/tool_executor_test.go`) |

Files: `internal/text/conf.go`, `internal/text/querier_setup_tools.go`,
`internal/text/querier_setup_tools_test.go`, `internal/text/finalizer.go`,
`internal/text/finalizer_test.go`, `internal/text/querier_setup.go`,
`internal/text/querier_setup_test.go`,
`internal/text/querier_setup_cmd_ban_test.go`, `internal/text/querier.go`,
`internal/text/tool_executor.go`, `internal/text/tool_executor_test.go`,
`pkg/tools/cmd_ban.go`, `pkg/tools/cmd_ban_enforcement_test.go`,
`pkg/tools/bash_tool_freetext_command.go`, `pkg/agent/run.go`,
`pkg/agent/agent.go`, `pkg/agent/cmd_ban_e2e_test.go`,
`pkg/agent/recorder_test.go`, `main_cmd_ban_e2e_test.go`,
`internal/audio/tool_overrides.go`, `architecture/tooling.md`, `architecture/config.md`,
`pkg/text/models/chat.go`, `pkg/text/models/chat_test.go`,
`internal/chat/index.go`, `internal/chat/index_test.go`.

## Error coverage

| Failure                                                                  | Expected outcome                                                                     | Test                                                     |
| ------------------------------------------------------------------------ | ------------------------------------------------------------------------------------ | -------------------------------------------------------- |
| Flag on and an explicit server fails to spawn under `StrictMcpStartup`   | Typed `ErrMcpServerStartup` still returned (explicit posture unchanged)              | `TestSetupMcpManager_skipAmbient_keepsExplicitServers`   |
| Enrichment fails (catalog not ready) on a non-persisting run             | Chat returned without cost rows; one warning as today; run succeeds                  | `TestFinalize_enrichesWithoutPersistence` (not-ready case) |
| `TokenUsage` nil on a non-persisting run                                 | No enrichment attempted; no warning                                                  | `TestFinalize_withoutPersistence_writesNothing`          |
| A tool is invoked on a context carrying no policy (library caller, context-free `Call`) | Permissive; no panic; documented                                        | `TestCmdBanEnforcement_NoPolicyPermissive`               |
| Caller mutates the slice after `WithCmdBanContext`                        | The installed policy is unchanged                                                    | `TestCmdBanEnforcement_ContextPolicySnapshotsInput`      |
| A banned command reaches the spawn point through the executor             | Refusal folded into the tool result as `ERROR: …` naming the entry; nothing spawned | `TestToolExecutor_attachesCmdBanPolicy`                  |

## Implementation notes

- 2026-09-09 — phase-1 worker (Claude session
  `session_01KXDFdab6MmuuKetC1EBowJ`), started 17:40Z. Deltas from the
  specification:
  - `FreetextCmdTool.Call` no longer calls the validator at all (it was
    validating against `context.Background()`, a no-op once the global is
    gone); the doc comment states that only `CallWithContext` enforces.
  - The enrichment gate is `session.Chat.TokenUsage != nil` as specified,
    which also changes the persisting path for a run without usage: it no
    longer calls `enrich` (previously the cost manager errored with "token
    usage is not yet set" and warned). Two existing fixtures in
    `internal/text/querier_test.go` relied on that call with a nil usage
    (`Test_Querier_postProcess_OnlyOuterCallEnrichesChat`,
    `Test_Querier_postProcess_SkipsCostEnrichIfManagerNotReady`); both now
    carry a `TokenUsage` and still prove their own claims. That file is not
    on the phase file list; the edit is fixture-only.
  - Integration row one names an ambient server whose command is `false`;
    the test uses `sh -c "touch <marker>"` instead, a strictly stronger
    oracle (the spawn itself is observable on disk) alongside the stderr
    assertion the row asks for. The control sub-test with the flag off
    proves the marker oracle is live.
  - `TestSetupMcpManager_skipAmbient_keepsExplicitServers` carries both a
    positive row (explicit testserver starts and registers `mcp_echo_echo`)
    and the error-coverage row (strict spawn failure stays
    `ErrMcpServerStartup`), each asserting the ambient marker absent.
  - `TestCmdBanEnforcement_FreetextRefusesBannedBeforeSpawn` previously ran
    the same two rows twice and included `Cmd.Call`; it now exercises
    `CallWithContext` only, since `Call` is permissive by contract (covered
    by `TestCmdBanEnforcement_NoPolicyPermissive`).
  - `architecture/tooling.md` also gained one sentence in the MCP lifecycle
    section for `SkipAmbientMcpServers` (the phase only named the
    command-ban section, but the field is part of this phase's contract).
  - Untouched: `Test_e2e_cmd_ban_*` assertions, `TestAgentCmdBan_*`
    assertions; only the reset calls and the concurrency comments changed.
  Surprises: `setupTooling` returns before registering any tool when the
  `mcpServers` directory is missing (the warn-and-degrade branch), so an
  injected tool on a bare config dir registers only with
  `SkipAmbientMcpServers`; this is exactly the summarizer's shape and is
  covered by `TestNewQuerier_oneOffQuerier_isSideEffectFree`. The machine
  ran at load average ~29 on 22 cores while another phase executed in
  parallel; the first two full-gate runs saw 30 s panics in packages this
  phase never touched (`internal/audio`, `internal/tools/mcp`, root) and the
  root package passed alone in 21.5 s at `-count=3`.
  Verification (all from the repository root):
  - `grep -rn 'SetCmdBanList\|ResetCmdBanListForTests\|cmdBanList' --include='*.go' .` → no output.
  - `go run mvdan.cc/gofumpt@latest -w -l .` → exit 0, no files listed on the final pass.
  - `go run honnef.co/go/tools/cmd/staticcheck@latest ./...` → exit 0.
  - `go vet ./...` → exit 0.
  - `go fix ./...` → exit 0.
  - `go run github.com/mibk/dupl@latest -t 80 .` → 31 clone groups, all
    pre-existing; the only group touching a phase file pairs
    `startupErrorNames` (`internal/text`, pre-existing) with
    `mcpStartupServerNames` (`pkg/agent`), two test packages that cannot
    share a helper. No clone in code added by this phase.
  - `go test ./internal/text/ -race -count=3 -timeout=30s -cover` → ok, 83.6%.
  - `go test ./pkg/tools/ ./pkg/text/models/ ./internal/chat/ ./pkg/agent/ -race -count=1` → ok.
  - `go test . -race -cover -count=3 -timeout=30s` (root alone) → ok 21.5 s, 56.7%.
  - `make qa` (staticcheck, gofumpt, then `go test ./... -race -count=3 -cover -timeout=30s`) at load average 18.8 →
    staticcheck and gofumpt clean; 42 packages `ok`; two panics `test timed out after 30s`:
    root package (running `Test_e2e_sync_plus_async`) and `internal/audio` (running
    `TestSplitter_DiarizeRoutesToCalibration`). Neither package is touched by this phase.
    `internal/text` took 22.0 s in that run against 6.8 s alone.
  - `go test ./internal/audio/ -race -cover -count=3 -timeout=30s` (alone) → ok 14.5 s, 89.1%.
  - `go test . -race -cover -count=3 -timeout=30s` (alone) → ok 21.5 s, 56.7%.
  - `go vet ./...` (final) → exit 0; `go run github.com/mibk/dupl@latest -t 80 .` (final) → 31 pre-existing groups, unchanged.
  - `make qa` rerun after the 1-minute load average fell below 8 (it rose
    back to 18.6 during the run itself) → 41 packages `ok`; root package
    `panic: test timed out after 30s` (running
    `Test_e2e_dirscope_recording_is_unconditional`), `internal/audio`
    timed out (running `TestMaxRequestBytesAppliesToBothPaths`),
    `internal/tools/mcp` FAIL (`TestManager`: "tool not registered").
  - `go test ./internal/tools/mcp/ -race -cover -count=3 -timeout=30s` (alone) → ok 6.0 s, 77.6%.
  Gate verdict: four full `make qa` / `go test ./...` runs in this session
  failed only with 30 s timeouts or timing assertions in packages this phase
  does not modify (root, `internal/audio`, `internal/tools/mcp`; the sole
  edit under those paths is the doc comment in
  `internal/audio/tool_overrides.go` named by the specification). Each of
  those packages passes standalone at `-race -count=3`. This is the
  full-suite load-flake class the repository has recorded before:
  `worklogs/2026-08-26-audio-transcription-design/phase-2-generic-transcriber.md`
  (`anthropic/Test_context` timed out under full-suite load, passes
  isolated), `worklogs/2026-08-28-cmd-flag-system-refactor/phase-11-drop-glob-command.md`
  ("tripped the main e2e package's 30s test timeout under full-suite
  parallel load … 20.4s standalone at `-race -count=3`") and
  `worklogs/2026-09-05-error-propagation/phase-9-errors-doc-and-gates.md`
  (R3-03: the root pty-e2e suite "does not exit zero in this sandbox …
  under `-count=3`", accepted with qualified wording). Every phase-owned
  criterion has passing evidence and every other gate is clean. The
  orchestrator reran the full unedited gate at load average 9 (README
  journal, "Phase 1 gate rerun and sign-off"): exit 0, every package `ok`;
  the phase was set **Complete** on that run, with no code change.

## Review findings

### Review 2 — 2026-09-10 (`worklog-review`, post-implementation)

No findings.

Verified good:

- D29 holds end to end. `pkg/tools/cmd_ban.go` carries no package state;
  `validateCmdNotBannedWithContext` reads only the context policy and its
  three callers (`bash_tool_freetext_command.go:128`, `async_cmds.go:536`,
  the deleted `Call` path) are all context paths. The executor attaches
  `q.cmdBan` at its single invoke site (`internal/text/tool_executor.go:149`)
  and `pkg/agent` hands its list to `Configurations.CmdBan`
  (`pkg/agent/agent.go:250`), so an embedded agent and the CLI share one
  mechanism. No non-test caller of `tools.Invoke` remains.
- `SkipAmbientMcpServers` skips the directory stat, the glob and the
  missing-directory error; explicit `McpServers` still reach the manager
  (`internal/text/querier_setup_tools.go`).
- Enrichment is gated on `TokenUsage != nil` above the persistence branch
  (`internal/text/finalizer.go:103`), so the summarizer's non-persisting
  querier carries usage; `Purpose` is skipped by both model resolvers
  (`internal/chat/index.go`, `internal/summary/summarizer.go:lastRecordedModel`).
