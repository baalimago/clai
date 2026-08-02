# Phase 4 — Integration & e2e

**Status:** Complete (2026-08-02, worker session 2026-08-02-04)

[← README](./README.md)

## Goal

Prove the ban list end-to-end through the real executor and CLI surfaces —
flag, config file, profile, and agent API — and update the architecture docs.

## Specification

### Test infrastructure notes

The e2e suite drives real tool execution through the mock vendor
(`-cm mock_test`, `internal/vendors/mock.go`): the mock fabricates the tool
call and inputs (`CLAI_MOCK_CMD_COMMAND`, `CLAI_MOCK_ASYNC_CMD_RUN_COMMAND`,
`CLAI_MOCK_ASYNC_CMD_RUN_ARGS`), and the real `FreetextCmdTool` /
`asyncCmdRunTool` then execute through `tools.Invoke`. Banned-command
assertions therefore exercise the real spawn-point check.

### New tests (in `main_tooling_comprehensive_e2e_test.go` or a new
`main_cmd_ban_e2e_test.go`)

1. **Flag path** — `-cmd-ban=touch` with `CLAI_MOCK_CMD_COMMAND=touch <marker>`:
   run `-r -cm mock_test -t=cmd -cmd-ban=touch q tool_cmd`; assert output
   contains `banned by policy` and `touch`; assert `<marker>` does NOT exist;
   assert the run completes normally (exit 0, no hard stop).
2. **Config file path** — write `textConfig.json` with
   `"cmd-ban": ["touch"]` via the test config dir helper; same mock command;
   assert refusal and no marker.
3. **Profile path** — profile `"cmd-ban": ["git commit"]`; mock command
   `git commit -m x`; assert refusal; assert `git log` (mock command
   `git log`) succeeds. Additive check (R1-04 revision): with the file base
   also set to `["touch"]`, both `git commit -m x` and `touch <marker>` are
   refused — the profile merges onto, not replaces, the file base.
4. **Quoted bypass path (Q7: A)** — `-cmd-ban=git commit` with
   `CLAI_MOCK_CMD_COMMAND=sh -c 'git commit -m x'`; assert refusal; the
   flattening rule catches the nested phrase before execution. This test
   pins the Phase 1 single-sided quote-strip semantics (Review 1 R1-01):
   it fails under a both-sides literal reading of rule 2.
5. **Async path** — `-cmd-ban=sh` with
   `CLAI_MOCK_ASYNC_CMD_RUN_COMMAND=sh`; assert refusal and that no async cmd
   is registered (`AsyncCmdSnapshotForTests()` empty).
6. **Permissive default** — no ban config; mock command `printf ok`; assert
   normal output (regression guard for D4).

### No-hard-stop assertion

Every refusal scenario asserts the run completes with exit 0 and the refusal
appears in the transcript as a tool result — the agent is notified, the run is
not aborted (D14).

### Agent API test

MOVED to Phase 5 (2026-08-02): the real-path agent tests — single-agent
refusal, sequential per-run isolation, and the R2-01 concurrent-agents
`-race` test — live in [`phase-5-pkg-agent-e2e.md`](./phase-5-pkg-agent-e2e.md).
Phase 4 no longer owns any `pkg/agent` assertions.

### Documentation

- `architecture/tooling.md` — Security section: describe the command ban
  list, matching semantics (D2), scope (D1), and precedence (D5), plus the
  documented limits: literal-text-only (no variable expansion, R1-03),
  contiguity (interleaved args can evade a phrase, R1-06), deny-by-content
  (ANY command whose literal tokens contain the phrase is refused, even
  when nothing executes — `echo git commit` IS banned by `git commit`;
  Review 2 R2-03), and literal-spelling evasion (`/bin/rm -rf /` is NOT
  caught by entry `rm`).
- `architecture/config.md` — Tool-selection section: document `cmd-ban` /
  `cmd-ban` / `-cmd-ban`.

### What it does NOT do

- Does NOT add setup-wizard UI (D10) or config migration (D11).

## Integration contract

| Scenario | Input | Observable result |
|----------|-------|-------------------|
| `-cmd-ban=touch` + mock `touch <marker>` | CLI e2e | Tool result contains `banned by policy (matched entry "touch")`; marker absent; run exits 0 |
| textConfig.json `cmd-ban: ["touch"]` + mock `touch <marker>` | CLI e2e | Same refusal; marker absent |
| Profile `cmd-ban: ["git commit"]` + mock `git commit -m x` | CLI e2e | Refusal naming `git commit` |
| Profile `cmd-ban: ["git commit"]` + mock `git log` | CLI e2e | `git log` output passes through |
| File `["touch"]` + profile `["git commit"]` + mock `git commit -m x` | CLI e2e | Refusal naming `git commit`; `touch` from the file base also still banned (additive, R1-04 revision) |
| `-cmd-ban=git commit` + mock `sh -c 'git commit -m x'` | CLI e2e | Refusal (quote flattening, Q7: A); no spawn |
| `-cmd-ban=sh` + mock async `sh` | CLI e2e | Refusal; no async cmd registered |
| No ban config + mock `printf ok` | CLI e2e | Normal output (regression) |
| Agent `WithCmdBanList("touch")` + `cmd` call | covered by Phase 5 (pkg/agent e2e) | Refusal naming `touch` |
| Any refusal scenario | CLI e2e | Run completes (exit 0); refusal is a tool result; NO hard stop (D14) |

## Acceptance criteria

- [x] Flag, file, and profile e2e tests each prove refusal AND absence of side effect
- [x] Quoted-bypass e2e proves `sh -c 'git commit'` is refused (Q7: A)
- [x] Async e2e proves no process spawn on ban
- [x] Every refusal scenario proves the run completes with exit 0 (no hard stop, D14)
- [x] Permissive-default e2e regression passes
- [x] Agent API test proves `WithCmdBanList` reaches the tool (moved to Phase 5, 2026-08-02)
- [x] `architecture/tooling.md` and `architecture/config.md` document the feature
- [x] `go test ./... -timeout=30s` passes (full suite, no `-race` needed here; Phase 6 runs the race gate)

## Error coverage

| Failure | Expected outcome |
|---------|-----------------|
| Flag + file both set | Flag appends; refusal matches either entry (D5) |
| Mock env missing (e.g. `CLAI_MOCK_CMD_COMMAND` unset) | Mock default command runs; e2e asserts on the default only when intended |
| Profile active + flag active | Profile base + flag append (covered by Phase 3 unit tests; e2e asserts a single source at a time) |
| Banned entry matched but command would have succeeded | Refusal still wins; no execution (D7) |

## Implementation notes

Executed 2026-08-02 by clai (worker session 2026-08-02-04). All new tests
live in `main_cmd_ban_e2e_test.go` (root package), driving the real CLI
through `run()` with the mock vendor (`-cm mock_test`), so every assertion
exercises the real spawn-point check behind `tools.Invoke`.

Test → acceptance-criteria mapping:

- `Test_e2e_cmd_ban_flag_path` — AC 1 (flag): `-cmd-ban=touch` + mock
  `touch <marker>`; refusal names `touch`; marker absent; exit 0.
- `Test_e2e_cmd_ban_config_file_path` — AC 1 (file): `textConfig.json` with
  `"cmd-ban": ["touch"]` (rest filled from defaults by
  `LoadConfigFromFile`).
- `Test_e2e_cmd_ban_profile_path` — AC 1 (profile): profile
  `"cmd-ban": ["git commit"]`; `git commit -m x` refused, `git log` passes
  through. The pass-through subtest runs inside a real git repo
  (`initGitRepo` helper: `git init` + one commit via direct `exec.Command`,
  CWD switched with `os.Chdir` + cleanup, mirroring the skills e2e pattern)
  because `git log` needs a repo to produce output.
- `Test_e2e_cmd_ban_profile_merges_onto_file_base` — AC 1 (additive,
  R1-04 revision): file `["touch"]` + profile `["git commit"]`; both
  commands refused, each naming its own entry.
- `Test_e2e_cmd_ban_quoted_bypass_refused` — AC 2 (Q7: A, R1-01):
  `-cmd-ban=git commit` + mock `sh -c 'git commit -m x'`; refused by the
  flattened tokens. Fails under a both-sides literal reading of Phase 1
  rule 2.
- `Test_e2e_cmd_ban_async_no_spawn` — AC 3: `-cmd-ban=sh` + mock async
  `sh`; refusal named `sh` and `AsyncCmdSnapshotForTests()` stays empty.
- Every refusal test asserts `gotStatus == 0` (AC 4, D14) — implemented in
  the shared `assertCmdBanE2E` helper, which also asserts the refusal names
  the matched entry and that a supplied marker path was never created.
- `Test_e2e_cmd_ban_permissive_default` — AC 5 (D4): no ban config; mock
  `printf ok` passes through and no refusal text appears.
- AC 6 (agent API) is covered by Phase 5 (pkg/agent e2e, moved 2026-08-02).

Multi-word flag values (`-cmd-ban=git commit`) are passed as single argv
slice elements, not via `strings.Split`, because the space must stay inside
the flag value. The per-run list is self-contained (`NewQuerier` calls
`SetCmdBanList`); each test still resets the global list on cleanup for
isolation in case `run()` errors before the setter.

Documentation:

- `architecture/tooling.md` — new "Command ban list" subsection in the
  Security and safety considerations section: matching semantics (D2),
  scope (D1), additive cascade (D5), spawn-point enforcement and
  no-hard-stop (D14), and the four documented matching limits
  (deny-by-content R2-03, no variable expansion R1-03, contiguity R1-06,
  literal-spelling evasion R2-03).
- `architecture/config.md` — new "Command ban configuration" subsection in
  the Tool selection configuration section: `cmd-ban` / `cmd-ban` /
  `-cmd-ban` / `WithCmdBanList`, purely additive semantics, and a pointer
  to the matching limits.

Gates: `go test ./... -timeout=30s` ok; `go test ./... -count=1
-timeout=180s` ok (one transient flake in the pre-existing
`TestAsyncCmdRun_BindsAsyncCmdToSessionContext` under parallel load —
passes in isolation and on re-run, unrelated to this phase); `go vet
./...` clean; `go build ./...` clean; gofumpt clean; staticcheck clean;
dupl unchanged at 29 clone groups (matches the recorded baseline).

## Review findings (review 1, 2026-08-02)

Reviewer: imago. The phase was Not Started; this review amends the contract
in place. Severity taxonomy and the full findings index live in the README.

- **R1-01 dependency (High) — e2e test 4 hinges on Phase 1 rule 2.**
  `CLAI_MOCK_CMD_COMMAND=sh -c 'git commit -m x'` with `-cmd-ban=git commit`
  only refuses if the tokenizer strips the single-sided quotes per Phase 1
  rule 2 as amended. Test 4 now states this dependency explicitly so a
  Phase 1 regression is caught here too.
- **R1-06 dependency (Low) — docs must state the limits.** The
  `architecture/tooling.md` Security section must document literal-text-only
  matching and the contiguity evasion; amended the spec above. The async
  e2e (test 5) uses `-cmd-ban=sh` with argv `sh` — unaffected by
  flattening; a follow-up unit case in Phase 2 covers phrase-in-arg.
- **R1-04 revision (2026-08-02) — profile-path e2e asserts the union.** The
  profile test now also proves a file-base ban survives when a profile with
  `cmd-ban` is active: file `["touch"]` + profile `["git commit"]` refuse
  both commands. This is the e2e guard for the purely additive cascade.

Verified good: the mock env vars exist (internal/vendors/mock.go:188–196:
`CLAI_MOCK_CMD_COMMAND`, `CLAI_MOCK_ASYNC_CMD_RUN_COMMAND`,
`CLAI_MOCK_ASYNC_CMD_RUN_ARGS`). The in-process e2e harness (`run()`,
`captureStdoutStderr`, `setupMainTestConfigDir`, `ResetAsyncCmdManagerForTests`,
`AsyncCmdSnapshotForTests`) supports every planned assertion, including
"no async cmd registered" and "marker file absent". The mock continues a
run after a tool error (`hasToolMessage` → `finalMockResponse`), so "exit 0,
no hard stop" is testable as specified. `architecture/tooling.md` has a
"Security and safety considerations" section (line 198) and
`architecture/config.md` has a "Tool selection configuration" section
(line 162) — both doc seams exist. Note: the suite is in-process, so the
async refusal assertion via `AsyncCmdSnapshotForTests()` works (see R1-05).

## Review findings (review 2, 2026-08-02)

Reviewer: clai. The phase was Not Started; this review amends the contract
in place. Full findings index in README.

- **R2-01 dependency (Medium) — concurrent-agents race test.** The R2-01
  mutex resolution in Phase 2 needs an enforcement test here: two
  concurrent agents with different ban lists under `go test -race
  ./pkg/agent/...`, each refusal naming its own entry. Amended the Agent
  API test section above. Without it, the mutex is dead code as far as the
  gates are concerned. **Location updated 2026-08-02:** the test moved to
  the new Phase 5 (pkg/agent e2e); the section above now points there.
- **R2-03 (Low) — deny-by-content and literal-spelling evasion
  undocumented.** The docs must state that banning is by literal content
  (a command is refused whenever the phrase's tokens occur in it, even when
  nothing executes — `echo git commit` is banned by `git commit`) and that
  alternate spellings evade (`/bin/rm -rf /` is not caught by entry `rm`).
  Amended the `architecture/tooling.md` bullet above; README Strategy
  matching-limits paragraph updated.

Verified good: all six planned e2e tests plus the agent API test remain
achievable with the amendments; the R1-04 union e2e (file `["touch"]` +
profile `["git commit"]`) is unchanged by review 2.

## Review findings (review 8, 2026-08-02)

Independent re-audit found no new findings. The real CLI e2e tests cover flag,
file, profile, quoted, async, permissive, additive, no-spawn, and no-hard-stop
boundaries, and the architecture documentation matches the implementation.
Phase remains Complete.
