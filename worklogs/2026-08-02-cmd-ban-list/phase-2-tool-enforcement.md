# Phase 2 — Tool enforcement

**Status:** Complete

[← README](./README.md)

## Goal

Refuse to spawn a command (freetext or direct) that matches a configured ban
entry, before any process is created, in `cmd` and `async_cmd` (plus the
`freetext_command` / `async_cmd_run` legacy aliases) — and notify the agent
about the rule in the refusal.

## Specification

### New components

- **`pkg/tools/cmd_ban.go`** (extended):
  - `var cmdBanList []string` — package-level per-run state.
  - `cmdBanMu sync.RWMutex` — guards `cmdBanList` (Review 2 R2-01,
    resolution of R1-05; mirrors the `asyncCmdManager.mu` RWMutex
    precedent): `SetCmdBanList` / `ResetCmdBanListForTests` take the write
    lock, `validateCmdNotBanned` takes the read lock. A "documented
    limitation" is NOT an option here: the public `pkg/agent` API (Phase 3)
    can run two agents with different ban lists concurrently, and a torn
    slice read is a real data race that `-race` only catches if a test
    exercises it (Phase 4 adds one).
  - `SetCmdBanList(entries []string)` — replace the active list with a
    snapshot copy of `entries` (write-locked; the caller's slice is never
    retained — review 6 R6-02).
  - `ResetCmdBanListForTests()` — restore the empty default for test
    isolation (write-locked; mirrors `ResetAsyncCmdManagerForTests`).
  - `validateCmdNotBanned(command string, args []string) error` — builds the
    effective token list (`[command] + args` for direct execution, the raw
    freetext string for shell execution), runs `matchCmdBan`, and returns an
    error naming the matched entry. Nil when not banned. For direct
    execution each element of `[command] + args` is tokenized with the same
    rules as freetext (quote strip and flatten apply per element), so
    `command=sh`, `args=[-c, 'git commit']` is caught by entry `git commit`
    (Review 1 R1-06).

### Modified components

- **`pkg/tools/bash_tool_freetext_command.go`** — both `Call` and
  `CallWithContext` call `validateCmdNotBanned(freetextCmd, nil)` immediately
  after the empty-string check, before `exec.Command`. Covers `cmd` and its
  `freetext_command` alias (one shared implementation).
- **`pkg/tools/async_cmds.go`** — `callAsyncCmdRun` calls
  `validateCmdNotBanned(command, args)` after input parsing, before
  `asyncCmdManager.Spawn`. Covers `async_cmd` (argv-based, no shell —
  see README D1/D12).
- **`pkg/tools/bash_tool_freetext_command.go`** — `cmdDescription` gains one
  sentence: some commands are refused by configured policy and must not be
  retried. The `async_cmd` description gains the same note.

### Enforcement point (D6)

The check happens at the spawn point inside `pkg/tools`, so any caller (tool
executor, agent embedding, future tools) is protected. The ban list is set per
run from `NewQuerier` (Phase 3); until then the default empty list keeps the
tools fully permissive (D4).

### Refusal behavior (D7, 2026-08-02 revision)

On a match the command is never spawned. The tool returns an error naming the
matched entry AND stating the rule, so the agent is notified and can adjust:

- Freetext: `run freetext command %q: command is banned by policy (matched entry %q). Do not run commands matching this rule.`
- Async: `start async command: command is banned by policy (matched entry %q). Do not run commands matching this rule.`

The error flows through the normal tool-result path: the model sees the
refusal as a tool result and continues the run. **No hard stop** — a hard stop
was considered and rejected (README "Rejected alternatives"); the agent is
trusted to heed the notification.

### What it does NOT do

- Does NOT apply the ban to structured tools, `clai_run`, or MCP tools (D9).
- Does NOT introduce an allow list (D3).
- Does NOT set the ban list from configuration (Phase 3).
- Does NOT trace execution or inspect process trees (rejected).

## Integration contract

| Scenario | Input | Observable result |
|----------|-------|-------------------|
| `cmd` (or `freetext_command`) with banned freetext command | `SetCmdBanList(["rm"])`, command `rm -rf <marker-dir>` | Error tool result names entry `rm` and the rule; marker file/dir NOT created |
| `cmd` with allowed command | `SetCmdBanList(["rm"])`, command `echo hi` | Normal output `hi` |
| `async_cmd` with banned command | `SetCmdBanList(["git commit"])`, `command=git`, `args=[commit, -m, x]` | Error names entry `git commit`; `AsyncCmdSnapshotForTests()` shows no new cmd |
| Default state (no `SetCmdBanList`) | any command | Existing behavior unchanged (permissive) |
| Quoted bypass attempt | `SetCmdBanList(["rm"])`, command `sh -c "rm -rf /"` | Banned (quote flattening, Q7: A) |
| Refusal mid-run | banned command via executor | Run continues; refusal appears as tool result; no hard stop |

## Acceptance criteria

- [x] `SetCmdBanList` + `ResetCmdBanListForTests` exist; default is empty — `TestCmdBanEnforcement_SetAndReset`
- [x] `SetCmdBanList` snapshots its input slice: caller mutation after the setter returns never alters the active list — `TestCmdBanEnforcement_SetterSnapshotsInput` (review 6 R6-02)
- [x] `cmd` (and its `freetext_command` alias) refuse banned commands before spawning — `TestCmdBanEnforcement_FreetextRefusesBannedBeforeSpawn` (all four `Call`/`CallWithContext` entry points), `TestCmdBanEnforcement_FreetextQuotedBypassBanned`
- [x] `async_cmd` refuses banned commands/args before spawn (no process group created) — `TestCmdBanEnforcement_AsyncRefusesBannedBeforeSpawn` asserts an empty `AsyncCmdSnapshotForTests()`
- [x] Refusal error names the matched entry and states the rule — `assertCmdBanRefusal` helper asserted in every refusal test
- [x] Refusal does NOT stop the run — it is a normal tool result (no `io.EOF`-style abort): the tool returns the refusal through the existing error path; the executor-level continuation (run keeps going, refusal appears as a tool result) is asserted in Phase 4's "Refusal mid-run" e2e
- [x] Allowed commands pass through untouched; default state permissive — `TestCmdBanEnforcement_AllowedPasses`, `TestCmdBanEnforcement_DefaultPermissive`
- [x] Tool descriptions mention the policy refusal — `TestCmdBanEnforcement_DescriptionsMentionRefusal`
- [x] `go test ./pkg/tools/ -timeout=30s` passes — ok 2.086s (full package, `-count=1`)

## Error coverage

| Failure | Expected outcome |
|---------|-----------------|
| Command matches entry | Error names entry and rule; no process spawned |
| Multiple entries match | First entry in list order reported (Phase 1 contract) |
| Non-string `command` input | Existing type error, unchanged |
| Empty `command` input | Existing empty-string error, unchanged (ban check runs after validation) |
| Async `command` matches but args don't | Banned (command token participates) |
| Async `command` clean, an arg matches | Banned (args tokens participate) |
| Async arg contains a phrase (`command=sh`, args `[-c, 'git commit']`) | Banned (arg flattened and matched, R1-06) |
| Non-contiguous argv (`git -C /path commit` vs entry `git commit`) | NOT banned — documented limitation (R1-06); contiguity is the contract |

## Implementation notes

Executing agent: clai (worker session 2026-08-02-02).

- The matcher was refactored for reuse: `matchCmdBan` now delegates to a
  shared `matchCmdBanTokens(tokens, entries)` core so `validateCmdNotBanned`
  reuses the entry loop instead of duplicating it. Phase 1's public behavior
  is unchanged (all 27 `TestCmdBanMatch` cases still pass).
- `validateCmdNotBanned` holds the read lock for the whole check, and
  `SetCmdBanList` / `ResetCmdBanListForTests` take the write lock (R2-01's
  mutex resolution of R1-05). The list is replaced wholesale, never mutated
  in place, mirroring the `asyncCmdManager` global-state pattern.
- The refusal message is built as an inner error in `validateCmdNotBanned`
  (`command is banned by policy (matched entry %q). Do not run commands
  matching this rule.`) and wrapped at the two call sites with the contract
  prefixes (`run freetext command %q: %w`, `start async command: %w`), so
  the full messages match the spec's refusal strings byte for byte. The
  trailing period trips staticcheck ST1005; suppressed with `//lint:ignore`
  because the message is a complete agent-facing sentence whose period is
  part of the contract.
- Freetext: the check sits after the empty-string check and before timeout
  parsing in both `Call` and `CallWithContext`, so the empty-command error
  is unchanged (error-coverage table) and no process is created for a banned
  command.
- Async: the check sits after input parsing and before
  `asyncCmdManager.Spawn` in `callAsyncCmdRun`. `SpawnAsyncCmdForTests`
  bypasses it by design — it is a test helper that talks to the manager
  directly, not the tool path.
- Test safety: the async refusal cases pass `cwd: t.TempDir()`, so even a
  regressed ban check could not create a real `git commit`; the freetext
  refusal cases target marker dirs inside `t.TempDir()`, which are harmless
  to remove.
- R6-02 fix (reopen, worker session 2026-08-02-09): `SetCmdBanList` now
  copies its input at the setter boundary
  (`cmdBanList = append([]string(nil), entries...)`), so a caller mutating
  its slice after the call can neither race a concurrent spawn check through
  the aliased backing array nor alter policy mid-run. The regression test
  `TestCmdBanEnforcement_SetterSnapshotsInput` mutates `entries[0]` after
  `SetCmdBanList` and asserts the installed snapshot still bans `rm` while
  the mutated entry (`sudo`) does not leak in. Red-first: the test failed
  against the pre-fix direct assignment; negative verification (revert to
  direct assignment) reproduced the failure. The README "Ban-list ownership"
  strategy paragraph was already the contract; D17 records it in the
  decision log.

Verification (all run from the repo root):

```bash
go test ./pkg/tools/ -run 'TestCmdBan' -count=1 -timeout=30s   # ok 0.012s (phase 1 + phase 2)
```

```bash
go test ./pkg/tools/ -count=1 -timeout=60s   # ok 2.086s (full package, incl. pre-existing tests)
```

```bash
go test ./pkg/tools/ -race -count=1 -timeout=60s   # ok 3.159s (mutex holds under -race)
```

```bash
go test ./... -timeout=120s   # all packages ok
```

```bash
go build ./...   # clean
```

```bash
go vet ./pkg/tools/   # clean
```

```bash
go run mvdan.cc/gofumpt@latest -l pkg/tools/   # no output
```

```bash
go run honnef.co/go/tools/cmd/staticcheck@latest ./pkg/tools/   # clean (ST1005 intentionally suppressed)
```

```bash
go run github.com/mibk/dupl@latest -t 80 .   # 29 clone groups — unchanged from baseline, no new clones
```

R6-02 re-verification (2026-08-02, worker session 2026-08-02-09; all from the
repo root):

```bash
go test ./pkg/tools/ -run 'TestCmdBan' -count=1 -timeout=30s   # ok 0.009s (incl. TestCmdBanEnforcement_SetterSnapshotsInput)
```

```bash
go test ./pkg/tools/ -count=1 -timeout=60s   # ok 2.142s (full package, incl. pre-existing tests)
```

```bash
go test ./pkg/tools/ -race -count=1 -timeout=60s   # ok (snapshot + mutex hold under -race)
```

```bash
go test -race ./pkg/agent/ -run 'TestAgentCmdBan' -count=1 -timeout=120s   # ok (concurrent-agents race gate)
```

```bash
go test . -run 'Test_e2e_cmd_ban' -count=1 -timeout=90s   # ok (all e2e refusal/no-spawn tests)
```

```bash
go test ./... -count=1 -timeout=180s   # all packages ok
```

```bash
go build ./...   # clean
```

```bash
go vet ./...   # clean
```

```bash
go run mvdan.cc/gofumpt@latest -l pkg/tools/   # no output
```

```bash
go run honnef.co/go/tools/cmd/staticcheck@latest ./pkg/tools/   # clean (ST1005 intentionally suppressed)
```

```bash
go run github.com/mibk/dupl@latest -t 80 .   # 29 clone groups — unchanged from baseline, no new clones
```

## Review findings (review 1, 2026-08-02)

Reviewer: imago. The phase was Not Started; this review amends the contract
in place. Severity taxonomy and the full findings index live in the README.

- **R1-05 (Medium) — D6 race rationale is wrong about the e2e; concurrent
  agent embeddings unaddressed.** The package-level `cmdBanList` global is
  written once per run in `NewQuerier` and read at spawn. Within the CLI and
  the in-process e2e suite (which is NOT subprocess-based —
  `main_*_e2e_test.go` calls `run(...)` in-process; `AsyncCmdSnapshotForTests`
  only works in-process) this is race-free in practice: tests are sequential
  per package and one run's setter precedes its readers. But the public
  `pkg/agent` API (Phase 3) is the plausible concurrent user: two agents
  with different ban lists running concurrently would cross-talk and race
  on the global. Decide explicitly: document the limitation, or guard the
  var with a mutex (matches the `asyncCmdManager` RWMutex precedent, ~5
  lines). Recommendation: mutex guard. README D6 corrected accordingly.
- **R1-06 (Low) — contiguity/flattening limits undocumented; async argv
  tokenization unspecified.** `git -C /path commit` (argv) and freetext
  `git --config x commit` are NOT banned by entry `git commit`, though both
  really commit — contiguity is the contract, and the docs (Phase 4
  tooling.md) must state it. Also: this phase now pins that each async argv
  element goes through the same tokenizer, so an arg containing a space
  (`touch "my file"` vs entry `my file`) IS banned by flattening — that is
  intended and now specified in the error-coverage table.

Verified good: both `Call` and `CallWithContext` in
`bash_tool_freetext_command.go` validate input (empty-string check) before
`exec.Command("sh", "-c", ...)` — the ban-check slot exists (lines ~59–64,
~125–130). `callAsyncCmdRun` parses input before `asyncCmdManager.Spawn` —
the slot exists there too. `cmdDescription` is a const shared by `cmd` and
`freetext_command`; the async description is the inline `Specification()` of
`asyncCmdRunTool` — both doc seams exist. `ResetAsyncCmdManagerForTests` /
`AsyncCmdSnapshotForTests` exist. `tools.Invoke` (internal/tools/handler.go:
83,89) wraps tool errors as `ERROR: failed to run tool: <name>, error:
<err>`, so the refusal string surfaces in the transcript; `applyToolCallBudget`
returns `io.EOF` only on `maxToolCalls` exhaustion, so a refusal does not
hard-stop a default run — the "no hard stop" AC is achievable.

## Review findings (review 2, 2026-08-02)

Reviewer: clai. The phase was Not Started; this review amends the contract
in place. Full findings index in README.

- **R2-01 (Medium) — the R1-05 race decision was never pinned.** R1-05
  recommended a mutex but left "mutex or documented limitation" open, and
  the Phase 2 spec carried no mutex. Consequence: an executor picking the
  "documented limitation" branch ships a public `pkg/agent` API whose
  package-level `cmdBanList` races when two agents with different ban lists
  run concurrently — and every gate (`make qa` runs `-race -count=3`) stays
  green because no test exercises that interleaving. Amended: the spec now
  carries `cmdBanMu sync.RWMutex` (the `asyncCmdManager.mu` precedent,
  async_cmds.go:441), and the concurrent-agents `-race` test lives in
  Phase 5 (pkg/agent e2e, added 2026-08-02; see the README feedback index).
  The maintainer may still choose "documented limitation" instead, but it
  must be a deliberate override of this resolution, recorded here.

Verified good: with the mutex the per-run setter/write + spawn/read pattern
is race-free for both the CLI and the in-process e2e (sequential per run,
setter precedes readers), and the `-race` gate in `make qa` can now actually
enforce it via the Phase 4 test.

## Review findings (review 6, 2026-08-02)

Reviewer: clai. The phase is reopened for R6-02; severity taxonomy and the
complete index live in the README.

- **R6-02 (Medium) — `SetCmdBanList` does not snapshot caller-owned state.** At
  `pkg/tools/cmd_ban.go:21-25`, the setter assigns `cmdBanList = entries` while
  claiming the mutex guards the active list. The mutex only guards the slice
  header; a caller can retain `entries` and mutate `entries[i]` after the
  setter returns, outside `cmdBanMu`. During a concurrent spawn check this is
  a data race, and without `-race` it can silently add/remove a ban in the
  middle of a run. For example, an embedding caller passes `entries :=
  []string{"rm"}`, calls `SetCmdBanList(entries)`, then changes
  `entries[0] = "sudo"` while a tool validates `rm -rf /`. Fix by copying the
  slice under the setter boundary (and add a regression test that mutates the
  original after `SetCmdBanList`); the spawn reader should observe the
  installed snapshot only.

**Resolution (2026-08-02, worker session 2026-08-02-09):** fixed in this
phase. `SetCmdBanList` copies its input slice at the setter boundary
(`append([]string(nil), entries...)`) and the doc comment states the
snapshot contract. Regression test
`TestCmdBanEnforcement_SetterSnapshotsInput` mutates the caller's slice
after the setter returns and asserts the spawn reader observes the installed
snapshot only; the test failed red against the pre-fix direct assignment
(negative verification by reverting reproduced the failure). All gates
re-ran green; see the implementation notes and the verification block.

Verified good: internal setter/read interleavings are protected by the
`RWMutex`, and the targeted package and agent race tests pass. That does not
cover mutation through an aliased input slice, which is why this finding is
not closed by the existing race gate.

## Review findings (review 8, 2026-08-02)

Independent re-audit found no new findings. The setter snapshot is taken before
the active list is published; the freetext and async checks occur after input
validation and before process creation; and refusal errors preserve the
matched-entry/rule contract. Targeted tests and the full QA race sweep passed.
R6-02 remains resolved; Phase remains Complete.
