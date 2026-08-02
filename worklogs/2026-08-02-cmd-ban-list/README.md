# Command Ban List

## Status board

| #   | Phase                                             | Status                | Summary                                                                                                             |
| --- | ------------------------------------------------- | --------------------- | ------------------------------------------------------------------------------------------------------------------- |
| 1   | [Matching engine](./phase-1-matching-engine.md)   | Complete              | Token/phrase matcher: quote strip, word flatten, metachar split                                                     |
| 2   | [Tool enforcement](./phase-2-tool-enforcement.md) | Complete              | Ban check at spawn in `cmd`, `async_cmd` (+ aliases); setter snapshots its input slice (R6-02 resolved) |
| 3   | [Config plumbing](./phase-3-config-plumbing.md)   | Complete              | textConfig.json, profile, `-cmd-ban` flag, agent API, per-run wiring                                                |
| 4   | [Integration & e2e](./phase-4-integration-e2e.md) | Complete              | Executor path, agent API, CLI e2e, docs                                                                             |
| 5   | [pkg/agent e2e](./phase-5-pkg-agent-e2e.md)       | Complete (2026-08-02) | Agent query contexts carry immutable per-agent policies; concurrent isolation is covered by e2e + race tests        |
| 6   | [Quality gates](./phase-6-quality-gates.md)       | Complete (2026-08-02) | Async cancellation race fixed and repeated QA gates pass                                                            |

¹ Reviews 1–2 (2026-08-02) amended the contracts before implementation
started — each phase's `## Review findings (review N, 2026-08-02)`
sections and the [Review feedback](#review-feedback-review-1-2026-08-02) /
[Review feedback (review 2)](#review-feedback-review-2-2026-08-02) indexes
record them. High/Medium findings must be resolved before a phase can be
marked Complete.

Phase order: Phases 1–4 build the feature; Phase 5 (pkg/agent e2e, added
2026-08-02) depends on Phases 2–3 and runs before Phase 6; Phase 6 is the
final quality-gate sweep and always runs last (renumbered from 5 when
Phase 5 was added).

## Motivation

The `cmd` / `freetext_command` tools (one shared implementation) execute
arbitrary shell via `sh -c`, and `async_cmd` executes arbitrary programs
directly. A user may want to
prevent the model from running destructive or privileged commands (`rm -rf`,
`sudo`, `git commit`, ...) while still allowing everything else.

The default must remain permissive: with an empty ban list, ad-hoc command
execution works exactly as today. The ban list is an opt-in policy.

## Strategy

**Deny-only, word-boundary phrase matching.** One ban list, no allow list.
Each entry is a whitespace-separated phrase of one or more tokens, split on
whitespace ONLY — quotes and metachars are never processed inside entries
(Review 2 R2-02). A command is banned when the entry's tokens appear as a
contiguous, in-order run in the command's token list. Tokens are
whitespace-split with one layer of quotes stripped per token (single-sided,
per Phase 1 rule 2 — Review 1 R1-01),
quoted words flattened into inner tokens, and shell
metacharacters (`; | & ( ) < >` and backtick) split apart — so `sh -c "rm -rf /"`
is caught by entry `rm`, and `sh -c 'git commit'` is caught by entry `git commit`.
Matching is exact and case-sensitive. No regex, no globs in v1.

**Enforcement at the spawn point.** The check lives in `pkg/tools`, the
package that owns shell execution, so every caller (tool executor, agent
embedding, future tools) is protected — not only the LLM path. The ban list is
per-run state injected through a package-level setter, following the existing
global-state pattern (`defaultCmdTimeout`, `asyncCmdManager`,
`ResetAsyncCmdManagerForTests`).

**Refusal notifies the agent.** A banned command is never spawned. The tool
returns an error naming the matched entry and stating the rule; the model sees
it as a normal tool result and continues. No hard stop: a stop-entirely
behavior was considered (for post-hoc detection) and rejected together with
the detection approach itself.

**Scope.** Applies to the three freetext/direct command-execution tools:
`cmd`, `async_cmd` (plus legacy aliases `freetext_command`, `async_cmd_run`).
Structured tools with fixed binaries (`go`, `git`, `sed`, `ls`, ...),
`clai_run`, and MCP tools are out of scope: the policy targets freetext
command execution only.

**Full config cascade (purely additive).** Effective list = default(empty) +
`textConfig.json` (`cmd-ban`) + profile (`cmd-ban`, only when explicitly
set) + flag (`-cmd-ban`, append). There is no precedence: sources only add,
and no source may remove another source's bans — a ban is removed by editing
the source that added it (2026-08-02 revision of R1-04). The profile merges
its list onto the file base (nil-guarded: a profile that omits `cmd-ban`
contributes nothing; an explicit `cmd-ban: []` also contributes nothing).
The flag `-cmd-ban=a,b,c` appends, matching how `-t` appends to
`RequestedToolGlobs`. The public agent API gets `WithCmdBanList`. The profile
JSON key is `cmd-ban` in both files — the former `cmd_ban` profile key was
removed (decision D18).

**Known matching limits (Review 1 R1-03, R1-06; Review 2 R2-03).** The
matcher sees literal text only: it does not expand variable assignments
(`x=git; $x commit` is NOT caught by `git commit`) or command substitutions,
and contiguity means interleaved arguments evade a phrase (`git -C /path
commit` is NOT caught by `git commit`). Banning is by literal content: a
command is refused whenever the phrase's tokens occur in it, even when
nothing executes (`echo git commit` IS banned by `git commit`), and
alternate spellings that change the literal tokens evade (`/bin/rm -rf /`
is NOT caught by entry `rm`). Claims about what the engine bans — in this
README, the phase specs, or the docs — must not exceed these limits.

**Profile `cmd-ban` is additive (Review 1 R1-04 revision, 2026-08-02).** A
profile that omits `cmd-ban` contributes nothing; a profile with an explicit
`cmd-ban` merges its bans onto the file base. Nothing in the cascade removes
bans, so an unrelated profile can never silently disable the configured
bans.

**Ban-list ownership.** The setter must take ownership of an immutable snapshot
of its input slice. Locking the package variable does not protect a caller that
mutates the slice after `SetCmdBanList` returns; all per-run state must therefore
be copied at the setter boundary before spawn-point readers use it.

## Decisions

| #   | Decision                                                                                                                                          | Rationale                                                                                                                                                                                                                                                           |
| --- | ------------------------------------------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| D1  | Scope = `cmd`, `async_cmd` (+ legacy aliases `freetext_command`, `async_cmd_run`)                                                               | Prevents trivial bypass via `async_cmd`; one config knob (Q1: A)                                                                                                                                                                                                    |
| D2  | Word-boundary token phrase matching, contiguous in-order, exact, case-sensitive; quotes stripped, quoted words flattened, metachars split (Q7: A) | `rm` bans `rm -rf /` and `echo x \| rm -rf`, not `rmdir`; `git commit` bans `git commit -m x` and `sh -c 'git commit'`, not `git log` (Q2: A + user refinement)                                                                                                     |
| D3  | Deny-only; no allow list                                                                                                                          | "Ban list" ask; allow list can be added later without breaking (Q3: A)                                                                                                                                                                                              |
| D4  | Default list empty (permissive)                                                                                                                   | Ad-hoc command execution is the tool's purpose; bans are opt-in                                                                                                                                                                                                     |
| D5  | Purely additive cascade: file + profile (merge) + flag (append) + agent API; no source removes another's bans (2026-08-02 revision of R1-04)      | Restrictions only accumulate; flag append matches tool-selection pattern (Q4: A + user revision)                                                                                                                                                                    |
| D6  | Enforcement at spawn point in `pkg/tools` via package-level setter, set per run in `NewQuerier`                                                   | Strongest guarantee; matches existing global-state pattern; `-race` safe for the CLI and the in-process e2e suite (sequential per run; the e2e is NOT subprocess-based); concurrent `pkg/agent` embeddings need a mutex or a documented limitation (Review 1 R1-05) |
| D7  | Banned command never spawned; refusal error names entry + rule and notifies the agent; NO hard stop (D14)                                         | Model adjusts behavior; command never spawns                                                                                                                                                                                                                        |
| D8  | No regex / glob entries in v1                                                                                                                     | Predictable semantics; defer if needed                                                                                                                                                                                                                              |
| D9  | Out of scope: structured tools (`go`, `git`, `sed`, ...), `clai_run`, MCP tools                                                                   | Policy targets freetext command execution only                                                                                                                                                                                                                      |
| D10 | No setup-wizard changes                                                                                                                           | `clai setup` interactive editor already handles new JSON keys generically (`editSlice`)                                                                                                                                                                             |
| D11 | No config migration needed                                                                                                                        | New fields unmarshal to zero value (empty) when absent                                                                                                                                                                                                              |
| D12 | Async `async_cmd` stays argv-based (no shell), pre-check-only                                                                                     | Tooling-async.md safety posture: tokenized args, no implicit shell; a shell wrapper traces nothing extra (pre-check already sees argv)                                                                                                                              |
| D13 | No shared-core refactor in this effort                                                                                                            | Extracting a shared spawn/capture core is orthogonal and regression-prone; deferred to a future worklog                                                                                                                                                             |
| D14 | Refusal does not hard-stop the run                                                                                                                | 2026-08-02 revision: notify the agent about the rule instead of aborting                                                                                                                                                                                            |
| D15 | `Agent.Setup` sets `chat.SkipIndex` via a package-level `sync.Once`                                                                               | Concurrent `pkg/agent` Setups (the R2-01 enforcement tests) raced on the unsynchronized global write; `sync.Once` makes the public API `-race`-clean (phase 5, 2026-08-02)                                                                                          |
| D16 | `WithCmdBanList` doc comment warns that agents sharing a process share one active ban list (R3-01, resolved review 4, 2026-08-02)                 | `cmdBanMu` fixes the data race (R2-01), not the policy cross-talk of the package-global list; public-API users must not rely on distinct lists for concurrent agents                                                                                                |
| D17 | `SetCmdBanList` copies its input slice at the setter boundary (R6-02, resolved phase 2 reopen, 2026-08-02)                                        | The mutex only guards the slice header; an aliased input slice could race readers or silently add/remove a ban mid-run — the setter owns an immutable snapshot (README "Ban-list ownership" strategy)                                                               |
| D18 | Profile key unified to `cmd-ban`; `async_cmd` rename with `async_cmd_run` legacy alias (2026-08-02, pre-release cleanup)                         | One JSON key across file and profile (previously `cmd-ban` vs `cmd_ban`); one freetext spawn tool name, while production callers on the old `async_cmd_run` name keep working (registry alias + mock-vendor alias)                                                  |

## Rejected alternatives

| Idea                                                     | Reason rejected                                                                                                                                                      |
| -------------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Post-hoc trace audit (`sh -x`)                           | Easily circumvented (`set +x`, stderr redirect; dash script files are not traced); low marginal value over the hardened pre-check under the cooperative threat model |
| Process-tree watch (`/proc` polling)                     | Racy (fast processes missed), side effects can precede detection, Linux-only                                                                                         |
| ptrace / strace-level exec tracing                       | High complexity, Linux-only, tracee-detectable, deadlock risk with the timeout/kill machinery                                                                        |
| Sandboxing (Landlock / seccomp / bubblewrap / container) | Sound prevention, but changes "run any command" semantics; deferred as opt-in "restricted mode" future direction                                                     |
| Allow list (Claude Code parity)                          | Not needed for the ban-list ask; additive later without breaking                                                                                                     |
| Shell-wrapping `async_cmd`                               | Buys no audit value (pre-check already sees argv) and changes the documented no-shell contract                                                                       |
| Hard stop on refusal                                     | Rejected 2026-08-02: refusal notifies the agent instead; aborting hides the context the agent needs to adjust                                                        |
| Shared spawn/capture core refactor                       | Orthogonal to the ban feature; regression risk to the async manager; separate future worklog                                                                         |

## Out of scope

- Allow list / ask list (Claude Code parity deferred)
- Regex and glob entries
- Per-directory or per-chat ban lists
- Ban enforcement for structured tools, MCP tools, or `clai_run`

## Review feedback (review 1, 2026-08-02)

Reviewer: imago. Scope: planning contracts vs the codebase — no
implementation exists yet (every phase Not Started), so this review amends
the contracts in place instead of auditing code. Review rounds are numbered;
a later round revisits these findings with new `R{n}-*` IDs.

### Severity taxonomy

| Severity | Meaning                                                                        |
| -------- | ------------------------------------------------------------------------------ |
| Blocker  | Contract cannot be implemented as written; execution must not start            |
| High     | Implemented literally, produces failing acceptance criteria or unsafe behavior |
| Medium   | Incorrect rationale or unaddressed edge that bites in realistic use            |
| Low      | Documentation/consistency nit; non-blocking                                    |

Reopen rule: High and Medium must be resolved before their phase can be
marked Complete; Low findings are non-blocking. With all phases Not
Started, the fixes are recorded as contract amendments inside the phase
files.

### Findings index

| ID    | Severity | Phase                                                                 | Summary                                                                                                                                                                                                                                                                        |
| ----- | -------- | --------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| R1-01 | High     | [1](./phase-1-matching-engine.md), [4](./phase-4-integration-e2e.md)  | Tokenization rule 2 ("surrounding quotes") is ambiguous and contradicts rule 3's example; the flagship `sh -c 'git commit'` AC fails under a both-sides literal reading. Pinned single-sided strip semantics.                                                                  |
| R1-02 | Low      | [1](./phase-1-matching-engine.md)                                     | Rule 4 example wrong: `$(git log)` yields `$`, `git`, `log` — the `$` survives.                                                                                                                                                                                                |
| R1-03 | Medium   | [1](./phase-1-matching-engine.md)                                     | Matching semantics #4 overclaims: `x=git; $x commit` is NOT banned by `git commit` under the phase's own tokenizer. Elevated into Strategy as a matching limit.                                                                                                                |
| R1-04 | High     | [3](./phase-3-config-plumbing.md)                                     | `ProfileOverrides` unconditionally replacing `CmdBan` silently clears the file list for any profile without `cmd_ban` (i.e. every existing profile). Resolved 2026-08-02: purely additive — profile merges onto the file base (nil-guarded); no source removes another's bans. |
| R1-05 | Medium   | [2](./phase-2-tool-enforcement.md), README D6                         | D6's race rationale cites "subprocess e2e" — false; the e2e suite runs in-process. Concurrent `pkg/agent` embeddings cross-talk on the global. Decided: mutex or documented limitation (recommended: mutex).                                                                   |
| R1-06 | Low      | [2](./phase-2-tool-enforcement.md), [4](./phase-4-integration-e2e.md) | Contiguity/flattening limits undocumented (`git -C /path commit` evades `git commit`); async argv tokenization (flatten per element) unspecified. Pinned + doc requirement added.                                                                                              |

### Verified good

All Phase 2 seams exist (`Call`/`CallWithContext` validate before
`exec.Command("sh", "-c", ...)`; `callAsyncCmdRun` parses before
`asyncCmdManager.Spawn`; `cmdDescription` shared by `cmd` and
`freetext_command`; `tools.Invoke` surfaces tool errors as normal results
and `applyToolCallBudget` only hard-stops on `maxToolCalls` exhaustion). All
Phase 3 seams exist (`setupToolConfig` append precedent; `ProfileOverrides`
precedence; `NewQuerier` → `setupTooling` call site; `pkg/agent` `Option`/
`asInternalConfig`; `editSlice` generic editor D10; `main.go` usage). Phase 4
mock env vars and the in-process e2e harness support every planned
assertion. Gates re-run: `go build ./...` ✓, `go vet ./...` ✓,
`go test ./pkg/tools/ -timeout=60s` ✓, dupl baseline → 29 clone groups
(matches the session journal).

### Verdict

Not ready to execute as written: R1-01 and R1-04 would produce failing
acceptance criteria or silently dropped restrictions if implemented
literally. All fixes are small contract amendments already applied in the
phase files; with those, the worklog is ready to start at Phase 1.

## Review feedback (review 2, 2026-08-02)

Reviewer: clai (review agent). Scope: plan-quality review of the README and
phase contracts against the worklog-system template and the codebase — no
implementation exists yet (every phase Not Started), so this review amends
contracts in place, exactly like review 1. Severity taxonomy: same as
review 1.

### Findings index

| ID    | Severity | Phase                                                               | Summary                                                                                                                                                                                                                                                                                                                                                                                                                                 |
| ----- | -------- | ------------------------------------------------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| R2-01 | Medium   | [2](./phase-2-tool-enforcement.md), [5](./phase-5-pkg-agent-e2e.md) | R1-05's "mutex or documented limitation" was never pinned into a phase contract; as written the plan can pass every gate (including `make qa`'s `-race`) while shipping a data race on the public `pkg/agent` API. Resolved: Phase 2 spec now carries a `sync.RWMutex` (the `asyncCmdManager.mu` precedent); the concurrent-agents `-race` test lives in Phase 5 (pkg/agent e2e, added 2026-08-02).                                     |
| R2-02 | Medium   | [1](./phase-1-matching-engine.md)                                   | Entry tokenization unspecified in Phase 1: the spec tokenizes "the raw command" but never states how an ENTRY string becomes tokens. A quoted or metachar-containing entry behaves differently under a whitespace-split reading vs the full tokenizer — silently ineffective or over-broad bans. Resolved: entries are whitespace-split only (drop empties); rules 1–5 apply to the command string only. Elevated into README Strategy. |
| R2-03 | Low      | [4](./phase-4-integration-e2e.md)                                   | Deny-by-content corollary and literal-spelling evasion undocumented: any command whose literal tokens contain the phrase is refused even when nothing executes (`echo git commit`), and `/bin/rm -rf /` is NOT caught by entry `rm`. Phase 4 docs contract now states both.                                                                                                                                                             |
| R2-04 | Low      | [1](./phase-1-matching-engine.md)                                   | Phase 1 rule 3 ("Flatten: split each quoted word on inner whitespace") is vacuous as an independent step — after rule 1 no token contains inner whitespace and rule 2 stripped the quotes. Flattening is the emergent result of rules 1–2. Rule reworded to prevent a redundant or misread pass.                                                                                                                                        |
| R2-05 | Low      | [3](./phase-3-config-plumbing.md)                                   | Stale citations: `setupTooling` is called at `querier_setup.go:194`, not `:143`; `ProfileOverrides` lives in `conf_profile.go`, not `conf.go` (the struct fields are in `conf.go`). Spec corrected.                                                                                                                                                                                                                                     |

### Verified good

Template compliance: directory layout, phase-file structure (Goal /
Specification / Integration contract / Acceptance criteria / Error coverage /
Implementation notes / Review findings), README status board + Strategy +
Decisions + Rejected alternatives + Session journal, severity taxonomy and
reopen rule, and the final quality-gate phase all match the worklog-system
template; naming matches the existing `2026-07-23-macro-mode` convention.
The reading contract holds: shared invariants (cascade, matching semantics,
limits) live in README Strategy and each phase is self-contained. Scoping
discipline (D9, D13) keeps the change contained with no new dependencies.

Codebase claims re-verified against the tree: `cmd` / `freetext_command` are
two instances of one struct (`FreetextCmdTool`) and both `Call` /
`CallWithContext` validate before `exec.Command("sh", "-c", ...)`
(bash_tool_freetext_command.go:76/84, 141/150); `callAsyncCmdRun` parses
before `asyncCmdManager.Spawn` (async_cmds.go:511–530); the `asyncCmdManager.mu`
RWMutex precedent exists (async_cmds.go:441); `cmdDescription` is one const
shared by both tools (line 25); `tools.Invoke` wraps tool errors as normal
results (handler.go:83,89) and `applyToolCallBudget` returns `io.EOF` only
past `persistence > 2` (tool_executor.go:194); the mock env vars
(mock.go:188–193) and continue-after-tool-error behavior (mock.go:66–67)
exist; the e2e harness runs in-process (`run`, `captureStdoutStderr`,
`setupMainTestConfigDir`); `setupToolConfig` appends (setup.go:141) and the
`ProfileOverrides` → `setupToolConfig` → `applyProfileOverridesForText` order
is setup.go:236/241/245; `editSlice` (setup_actions.go:816); `pkg/agent`
`Option` / `asInternalConfig` (agent.go:45/122); doc seams
(tooling.md:198, config.md:162); `-t/-tools` usage row in main.go:36.
Gates re-run: `go build ./...` ✓, `go vet ./...` ✓,
`go test ./pkg/tools/ -timeout=60s` ✓, and the dupl baseline → 29 clone
groups (matches the journal). `make qa` was not re-run (it is Phase 6's own
deliverable — same caveat as review 1).

### Verdict

Clear, template-compliant, high quality. Review 1 fixed the two defects that
would have produced failing or unsafe behavior; review 2 found no blocker —
two Medium contract gaps (race pinning R2-01, entry tokenization R2-02) and
three Low spec/doc nits, all amended in place. The worklog is ready to start
at Phase 1.

## Review feedback (review 3, 2026-08-02)

Reviewer: clai (holistic review, worker session 2026-08-02-06). Scope:
post-implementation audit of ALL phases (1–6, all marked Complete) against
the README strategy and each phase contract — the first review round that
audits shipped code rather than contracts. Severity taxonomy: same as
review 1; High/Medium reopen, Low is non-blocking.

### Findings index

| ID    | Severity | Phase                                        | Summary                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                     |
| ----- | -------- | -------------------------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| R3-01 | Low      | [5](./phase-5-pkg-agent-e2e.md), `pkg/agent` | The residual logical cross-talk of the package-global list (D6) is documented only in phase-5 test design notes. `WithCmdBanList`'s doc comment (agent.go:122) does not warn public-API users that two CONCURRENT agents with distinct lists may enforce each other's lists — the `cmdBanMu` mutex fixes the data race (R2-01), not the policy cross-talk. Recommend one sentence in the doc comment ("agents sharing a process share one active list; do not rely on distinct lists for concurrent agents"). Non-blocking. |

### Verified good

Traced the core invariant — "a banned command is never spawned; the refusal
names the entry and the rule; the run does not hard-stop" — through every
in-scope branch:

- All three spawn paths check before spawning: `bash_tool_freetext_command.go`
  `Call` (validate :79 → `exec.Command` :95) and `CallWithContext` (:147 →
  :162), covering both `cmd` and `freetext_command` (one shared
  `FreetextCmdTool`); `async_cmds.go` `callAsyncCmdRun` (:530 → `Spawn` :533),
  covering `async_cmd_run` (`Call`/`CallWithContext` both delegate).
  `rg 'exec\.Command|Spawn\(' pkg/tools` shows no other freetext spawn point;
  structured tools, `clai_run`, and MCP are out of scope per D9.
- Branch audit: the empty-command error precedes the ban check in both
  freetext entry points (`TestCmdBanEnforcement_EmptyCommandErrorUnchanged`);
  `callAsyncCmdRun` parses input before checking (validation-first, fine);
  timeout/interrupt paths are unreachable for banned commands (pre-spawn
  check). The refusal message names the entry via %q and states the rule
  (D7); the `//lint:ignore ST1005` is scoped to the one sentence.
- Matcher: `containsRun` enforces contiguous, in-order, exact,
  case-sensitive matching; `tokenizeCommand` implements rules 1–5
  (whitespace split → single-sided quote strip → metachar split → drop
  empties; R1-01/R2-04); entries are whitespace-split only (R2-02);
  first-match-in-list-order; nil/empty entries and empty commands never ban.
  Exercised by `TestCmdBanTokenizeCommand` (16 cases) and `TestCmdBanMatch`
  (27 cases), including the deny-by-content and literal-spelling limits
  (R2-03).
- Cascade (D5/R1-04): file `cmd-ban` loads at setup.go:219; `ProfileOverrides`
  appends profile `cmd-ban` nil-guarded (conf*profile.go); `setupCmdBanConfig`
  appends the `-cmd-ban` flag (split/trim/drop-empty, after `setupToolConfig`);
  `NewQuerier` calls `pkgtools.SetCmdBanList(userConf.CmdBan)` unconditionally
  before `setupTooling` (querier_setup.go). `applyProfileOverridesForText`
  re-applies only ChatModel — no double-append. `WithCmdBanList` →
  `asInternalConfig` → `CmdBan` → the same setter; per-run isolation proven by
  `Test_NewQuerier*\*` and the phase-5 sequential test.
- Concurrency (R2-01): `cmdBanMu` RWMutex guards list writes/reads;
  `skipIndexOnce` is the only production writer of `chat.SkipIndex`
  (agent.go:161; `internal/chat` tests write it only in their own test
  binary). Both concurrent-agents tests ran green under `make qa`'s
  `-race -count=3`.
- Docs/surface: `clai tools` lists all three tools and `clai tools <name>`
  shows the full "refused by configured policy and must not be retried"
  description; `architecture/tooling.md` and `config.md` match the code;
  `main.go` usage row present; README claims (including the four matching
  limits) do not exceed the engine's actual behavior.

Gates re-run by the reviewer in this session: dupl baseline
`go run github.com/mibk/dupl@latest -t 80 .` → 29 clone groups; `make qa`
exit 0 (staticcheck, gofumpt, `go fix`, `go test ./... -race -count=3 -cover
-timeout=30s` — all 36 packages with tests ok, 1 no-test-files package);
`go build ./...` clean; `go vet ./...` clean; post-change dupl → 29 groups,
identical set; `git diff go.mod` empty.

### Verdict

Ships clean through every gate. All six phase contracts and the README
strategy are met by the shipped code; the phase-5 concurrent test genuinely
exercises the R2-01 mutex under `-race` (the phase-5 negative verification
proved both the mutex and `skipIndexOnce` are load-bearing). One Low,
non-blocking documentation finding (R3-01) recorded; no High/Medium findings
— no phase reopens, the status board stays all-Complete, and the worklog is
complete.

## Review feedback (review 4, 2026-08-02)

Reviewer: clai (holistic review, worker session 2026-08-02-07). Scope:
post-implementation re-audit of ALL phases (1–6, all Complete) against the
README strategy and every phase contract — the second review round that
audits shipped code — plus the R3-01 follow-up from review 3. Severity
taxonomy: same as review 1; High/Medium reopen, Low is non-blocking.

### Findings index

| ID    | Severity                              | Phase                                        | Summary                                                                                                                                                                                                                                                                                                                                           |
| ----- | ------------------------------------- | -------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| R3-01 | Low — RESOLVED (review 4, 2026-08-02) | [5](./phase-5-pkg-agent-e2e.md), `pkg/agent` | `WithCmdBanList`'s doc comment now warns that agents sharing a process share one active list (package-global state, D6) and that distinct lists must not be relied on for concurrent agents in one process (new decision D16). The `cmdBanMu` mutex (R2-01) fixes the data race; the doc sentence closes the policy cross-talk documentation gap. |

### Verified good (re-audit)

Re-traced the core invariant — "a banned command is never spawned; the
refusal names the entry and the rule; the run does not hard-stop" — through
every in-scope branch and the full cascade:

- Spawn paths: `bash_tool_freetext_command.go` `Call` (validate before
  `exec.Command`) and `CallWithContext`, plus `async_cmds.go`
  `callAsyncCmdRun` (validate before `asyncCmdManager.Spawn`); the
  empty-command error precedes the ban check in both freetext entry points;
  `SpawnAsyncCmdForTests` bypasses the check by design (test helper, not the
  tool path).
- Matcher: phase-1 rules 1–5 hold (16 tokenizer + 27 matcher unit cases);
  entries are whitespace-split only (R2-02); the `$` from `$(...)` survives
  (R1-02); single-sided quote strip (R1-01); the four documented matching
  limits are pinned by unit cases (`echo git commit` banned, `x=git; $x
commit` NOT banned, `git x commit` NOT banned, `/bin/rm -rf /` NOT
  banned).
- Cascade: file (`textConfig.json` `cmd-ban`) → profile (`cmd_ban`
  nil-guarded merge, R1-04 revision) → flag (`-cmd-ban` append, split/trim/
  drop-empties) → agent API (`WithCmdBanList` → `asInternalConfig`) →
  `NewQuerier` → `SetCmdBanList` before `setupTooling`. The late
  `applyProfileOverridesForText` touches only `Model`, so no double-append
  follows `setupCmdBanConfig`.
- Concurrency: `cmdBanMu` (R2-01) and `skipIndexOnce` (D15) both in place;
  the concurrent-agents `-race` tests in `pkg/agent/cmd_ban_e2e_test.go`
  still enforce them (green under `make qa`'s `-race -count=3`).
- Surfaces: `main.go` usage row; the three tool descriptions; the
  `architecture/tooling.md` and `config.md` sections match the code;
  no ban logic in `internal/text/generic` or `internal/vendors` (D6);
  `go.mod` dependency set unchanged.

### R3-01 resolution (2026-08-02)

Applied the review-3 recommendation: `pkg/agent/agent.go` `WithCmdBanList`
doc comment now carries the concurrent-agents warning (D16). Doc-comment-only
change — no behavior or test changes needed; the full gate suite was re-run
after the change and stays green (see the Session journal entry below).

### Verdict

No new findings. The single Low finding from review 3 is resolved; no
High/Medium findings — no phase reopens, the status board stays
all-Complete, and the worklog remains complete.

## Review feedback (review 5, 2026-08-02)

Reviewer: clai (holistic review, worker session 2026-08-02-08). Scope:
post-implementation re-audit of ALL phases (1–6, all Complete) against the
README strategy and every phase contract — the third review round that
audits shipped code. Severity taxonomy: same as review 1; High/Medium
reopen, Low is non-blocking. This round re-verified the code directly
instead of trusting the implementation notes: every gate was re-run
independently, the no-spawn invariant was re-traced through every in-scope
branch, and the cascade was re-followed end-to-end to the spawn point.

### Findings index

No new findings. R1-01…R3-01 stay resolved; no regressions observed.

### Verified good (re-audit)

- Spawn paths re-traced in code: `bash_tool_freetext_command.go` `Call` and
  `CallWithContext` both call `validateCmdNotBanned(freetextCmd, nil)`
  immediately after the empty-string check and before `exec.Command`;
  `async_cmds.go` `callAsyncCmdRun` calls `validateCmdNotBanned(command,
args)` after input parsing and before `asyncCmdManager.Spawn`.
  `rg 'exec\.Command|Spawn\(' pkg/tools` shows no other freetext spawn
  point (structured tools, `clai_run`, MCP out of scope per D9;
  `SpawnAsyncCmdForTests` bypasses by design — it is a test helper, not the
  tool path).
- Refusal message traced byte-for-byte: the inner `validateCmdNotBanned`
  error plus the `run freetext command %q: %w` / `start async command: %w`
  wrappers reproduce the phase-2 contract strings exactly.
- Matcher re-verified: `containsRun` enforces contiguous, in-order, exact,
  case-sensitive matching; `tokenizeCommand` implements rules 1–5
  (whitespace split → single-sided quote strip R1-01 → metachar split →
  drop empties; `$` from `$(...)` survives R1-02; flattening is emergent,
  R2-04); entries are whitespace-split only (R2-02); first-match in list
  order; nil/empty entries and empty commands never ban. The four
  documented matching limits are pinned by unit cases (deny-by-content,
  no variable expansion, contiguity, literal spelling — R2-03).
- Cascade re-traced: `applyFlagOverridesForText` /
  `applyProfileOverridesForText` (setup_flags.go:224/252) touch only
  Model/Profile/Reply/etc. — never `CmdBan` — so file (`cmd-ban`) →
  profile (nil-guarded merge, conf_profile.go:83) → flag
  (`setupCmdBanConfig`, split/trim/drop-empty/append after
  `setupToolConfig`) → agent API (`WithCmdBanList` → `asInternalConfig`) →
  `NewQuerier` setter before `setupTooling` (querier_setup.go:198) is
  union-only with no double-append and no clear.
- Concurrency: `cmdBanMu` RWMutex guards the list (write on
  `SetCmdBanList`/`ResetCmdBanListForTests`, read in `validateCmdNotBanned`;
  the slice is replaced wholesale, never mutated in place); `skipIndexOnce`
  is the only production writer of `chat.SkipIndex` (agent.go:164), and no
  `pkg/agent` test depends on it being false. Both concurrent-agents tests
  stayed green under `make qa`'s `-race -count=3` and a dedicated `-race`
  run.
- Surfaces: `main.go` usage row present; the real binary's `clai tools`
  lists all three tools with "Some commands are refused by configured
  policy and must not be retried."; the setup wizard handles the new
  `cmd-ban` JSON key generically (D10 — `handleValue` dispatches any
  `[]any` value to `editSlice`, setup_actions.go:752–753; no hard-coded
  key list); no ban logic in `internal/text/generic` or `internal/vendors`
  (D6); `go.mod` dependency set unchanged.

Gates re-run by the reviewer in this session (all from the repo root):

```bash
go run github.com/mibk/dupl@latest -t 80 .   # Found total 29 clone groups — matches the recorded baseline set
```

```bash
make qa   # exit 0: staticcheck, gofumpt, go fix clean; all 37 packages ok under go test ./... -race -count=3 -cover -timeout=30s
```

```bash
go build ./...   # clean
```

```bash
go vet ./...   # clean
```

```bash
go test ./pkg/tools/ -run 'TestCmdBan' -count=1 -timeout=60s   # ok
```

```bash
go test -race ./pkg/agent/ -run 'TestAgentCmdBan' -count=1 -timeout=120s   # ok
```

```bash
go test . -run 'Test_e2e_cmd_ban' -count=1 -timeout=90s   # ok (all e2e refusal/no-spawn/permissive-default tests)
```

```bash
git diff go.mod   # empty — no new third-party dependencies
```

### Verdict

No new findings; R1-01…R3-01 stay resolved. All six phase contracts and
the README strategy remain met by the shipped code, and every gate re-ran
green under independent execution — "ships clean through the gates" and
"correct" are both supported by the code trace, not just the green runs.
No phase reopens — the status board stays all-Complete and the worklog
remains complete.

## Review feedback (review 6, 2026-08-02)

Reviewer: clai. Scope: independent post-implementation audit of the shipped
code, including the public setter boundary and a fresh QA run. Severity
taxonomy: High/Medium reopen a phase; Low is non-blocking.

### Findings index

| ID    | Severity | Phase                              | Summary                                                                                                                                                                                           |
| ----- | -------- | ---------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| R6-01 | Medium   | [6](./phase-6-quality-gates.md)    | The first independently re-run `make qa` failed in `TestAsyncCmdRun_BindsAsyncCmdToSessionContext`; resolved by serializing cancellation state and removing the competing CommandContext watcher. |
| R6-02 | Medium   | [2](./phase-2-tool-enforcement.md) | `SetCmdBanList` stores the caller's slice without copying, so caller mutation after return bypasses `cmdBanMu` and can race readers or alter policy mid-run.                                      |

### Verified good

The full ordinary suite passed with `go test ./... -count=1 -timeout=120s`.
The targeted agent race suite passed with
`go test -race ./pkg/agent/ -run 'TestAgentCmdBan' -count=1 -timeout=120s`.
The dupl run found 29 clone groups, matching the recorded baseline. A second
`make qa` passed, so the failure in R6-01 is intermittent rather than a
deterministic feature regression. The spawn-point checks, matcher semantics,
configuration cascade, and no-hard-stop behavior remain as verified in review 5.

### Verdict

Not ready to close as reviewed. R6-02 is a real public API ownership/race gap
despite the internal mutex and must be fixed in Phase 2. R6-01 must be
investigated and either made deterministic or explicitly isolated as a known
pre-existing flaky test before Phase 6 can return to Complete. The second QA
run is evidence, not proof that the first failure was harmless.

## Review feedback (review 7, 2026-08-02)

Reviewer: clai. Scope: independent implementation audit after the R6-02 fix,
including the public-agent concurrency contract and a fresh QA run. Severity
taxonomy: High/Medium reopen a phase; Low is non-blocking.

### Findings index

| ID    | Severity | Phase                           | Summary                                                                                                                                 |
| ----- | -------- | ------------------------------- | --------------------------------------------------------------------------------------------------------------------------------------- |
| R7-01 | Medium   | [5](./phase-5-pkg-agent-e2e.md) | `WithCmdBanList` previously relied only on process-global state; resolved by carrying a copied policy through each agent query context. |

### Verified good

The matcher, spawn-point checks, additive configuration cascade, setter
snapshot, refusal/no-hard-stop behavior, and the `skipIndexOnce` race fix were
re-verified against the implementation. `go test ./... -count=1
-timeout=180s`, `go test -race ./pkg/agent/ -run 'TestAgentCmdBan' -count=1
-timeout=120s`, and `make qa` all passed in this review. The latter includes
staticcheck, gofumpt, `go fix`, and the repository race suite. The dupl run
reported 29 clone groups, matching the recorded baseline.

### Verdict

Not ready to close. R7-01 reopens Phase 5: the public agent API's documented
process-global limitation is internally consistent, but it does not satisfy
the phase's stated concurrent-agent isolation contract. R6-01 also remains
open until the intermittent async cancellation failure is explained or
isolated as required by Phase 6.

## Session journal

- **2026-08-02 (planning — imago):** Design interview Q1–Q4 completed:
  scope A, matching semantics A with per-argument word boundaries, deny-only,
  full cascade with append. Worklog scaffolded; phases 1–5 defined. No
  implementation started.
- **2026-08-02 (planning — imago):** Duplication baseline recorded for the
  quality-gate phase (renumbered 5 → 6 on 2026-08-02):
  `go run github.com/mibk/dupl@latest -t 80 .` → 29 clone groups; the 2 listed
  clones are pre-existing vendor code
  (`internal/vendors/anthropic/source_reader.go` vs
  `internal/vendors/pi/source_reader.go`), unrelated to this effort.
- **2026-08-02 (planning — imago):** Q5–Q7 resolved. Post-hoc trace audit
  (`sh -x`) explored and rejected (circumventable; see Rejected alternatives).
  Q7: A — tokenizer flattens quoted words and splits metachars. Refusal
  behavior revised: no hard stop; the refusal error notifies the agent about
  the rule (D14). Async stays argv-based, pre-check-only (D12); no shared-core
  refactor in this effort (D13).
- **2026-08-02 (review 1 — imago):** Contract review against the codebase
  (no implementation yet). Commands re-run: `go build ./...` ✓,
  `go vet ./...` ✓, `go test ./pkg/tools/ -timeout=60s` ✓,
  `go run github.com/mibk/dupl@latest -t 80 .` → 29 clone groups (matches
  the recorded baseline). Findings R1-01…R1-06 recorded; High/Medium amend
  Phase 1 (tokenizer semantics), Phase 3 (profile override nil-guard), and
  Phase 2/README D6 (race rationale). Verdict: not ready to execute as
  written until the amendments are absorbed; see the Review feedback index.
- **2026-08-02 (review 1 follow-up — imago):** R1-04 resolution revised per
  user decision: the cascade is purely additive. `ProfileOverrides` appends
  the profile's `cmd_ban` onto the file base (nil-guarded) instead of
  replacing it; `cmd_ban: []` contributes nothing (no clear switch); no
  source removes another source's bans. Precedence removed from README
  Strategy/D5 and Phase 3; Phase 4 gains a file+profile union e2e. The tool-
  selection analogy now holds for the flag only (profile still replaces for
  `RequestedToolGlobs`).
- **2026-08-02 (review 2 — clai):** Plan-quality review of README + phases
  against the worklog template and the codebase (no implementation yet).
  Gates re-run: `go build ./...` ✓, `go vet ./...` ✓,
  `go test ./pkg/tools/ -timeout=60s` ✓, dupl → 29 clone groups (matches
  the baseline). Findings R2-01…R2-05: R2-01 pins the R1-05 mutex decision
  into Phase 2 and adds a concurrent-agents `-race` test in Phase 4;
  R2-02 pins entry tokenization (whitespace-split only) into Phase 1 and
  README Strategy; R2-03 adds deny-by-content and literal-spelling limits to
  the Phase 4 docs contract; R2-04 rewrites the vacuous rule 3; R2-05 fixes
  stale file/line citations. Verdict: plan is clear and template-compliant;
  ready to start at Phase 1.
- **2026-08-02 (review 2 follow-up — imago):** Added Phase 5 "pkg/agent
  e2e" (`phase-5-pkg-agent-e2e.md`) to bolster the agent-API tests:
  real-path refusal through `internal.CreateTextQuerier` + the mock vendor,
  sequential per-run isolation (banned → permissive → banned), and
  concurrent agents with distinct ban lists under `-race` (enforcement for
  the R2-01 `cmdBanMu` resolution). Phase 4's agent-API section now points
  at Phase 5. The quality-gate phase was renumbered 5 → 6
  (`phase-6-quality-gates.md`) so the final sweep stays last and its `-race`
  gate re-enforces the new concurrent test.
- **2026-08-02 (phase 1 — clai, worker session 2026-08-02-01):** Phase 1
  implemented: `pkg/tools/cmd_ban.go` (`tokenizeCommand`, `matchCmdBan`) +
  `pkg/tools/cmd_ban_test.go` (table-driven; 16 tokenizer cases, 27 matcher
  cases). All 9 acceptance criteria met with cited tests; implementation
  notes written in the phase file. Gates: `go test ./pkg/tools/ -run
'TestCmdBan' -timeout=30s` ok 0.005s; `go test ./pkg/tools/ -timeout=60s`
  ok 2.392s; `go build ./...`, `go vet ./pkg/tools/`, gofumpt,
  staticcheck all clean; dupl unchanged at 29 clone groups. No deviations
  from the phase contract; next eligible phase is Phase 2.
- **2026-08-02 (phase 2 — clai, worker session 2026-08-02-02):** Phase 2
  implemented: `pkg/tools/cmd_ban.go` extended (`cmdBanList` +
  `cmdBanMu sync.RWMutex`, `SetCmdBanList`, `ResetCmdBanListForTests`,
  `validateCmdNotBanned`; matcher refactored into shared
  `matchCmdBanTokens` core); enforcement inserted at the spawn point in
  `bash_tool_freetext_command.go` (both `Call`/`CallWithContext`, after the
  empty-string check) and `async_cmds.go` (`callAsyncCmdRun`, before
  `Spawn`); `cmdDescription` and the `async_cmd_run` description note that
  some commands are refused by policy. New `cmd_ban_enforcement_test.go`:
  set/reset lifecycle, table-driven `validateCmdNotBanned` (freetext,
  argv tokenization, first-match order, non-contiguous not banned),
  refusal before spawn for all four freetext entry points + quoted bypass,
  async no-spawn (empty snapshot), non-contiguous argv allowed, allowed +
  default-permissive pass-through, empty-command error unchanged, and
  description notes. All 8 acceptance criteria met with cited tests. The
  refusal message ends with a period (agent-facing sentence, part of the
  contract); ST1005 suppressed with `//lint:ignore`. Gates: `go test
./pkg/tools/ -run 'TestCmdBan' -count=1 -timeout=30s` ok 0.012s; `go
test ./pkg/tools/ -count=1 -timeout=60s` ok 2.086s; `-race` ok 3.159s;
  `go test ./... -timeout=120s` ok; `go build ./...`, `go vet
./pkg/tools/`, gofumpt, staticcheck all clean; dupl unchanged at 29
  clone groups. No deviations from the phase contract; next eligible phase
  is Phase 3.
- **2026-08-02 (phase 3 — clai, worker session 2026-08-02-03):** Phase 3
  implemented (config plumbing): `Configurations.CmdBan`
  (`json:"cmd-ban"`) + `Profile.CmdBan` (`json:"cmd-ban,omitempty"`);
  `ProfileOverrides` nil-guarded additive append (R1-04 revision);
  flag-side `CmdBan` + `-cmd-ban` registration in `parseFlags`;
  `setupCmdBanConfig` (split/trim/drop-empty/append) called after
  `setupToolConfig`; `NewQuerier` calls `tools.SetCmdBanList` before
  `setupTooling` (unconditional, D6); `pkg/agent` `WithCmdBanList` +
  `asInternalConfig` passthrough; `main.go` usage row. Tests (TDD, red
  first): `conf_profile_test.go` cascade table (explicit merge / omitted /
  explicit empty) + profile JSON persistence, `conf_test.go` textConfig
  `cmd-ban` persistence, `setup_flags_test.go` two `-cmd-ban` parse cases,
  new `setup_cmd_ban_test.go` (5 append/trim/drop cases), new
  `querier_setup_cmd_ban_test.go` (real `NewQuerier` → refusal +
  permissive default), `agent_test.go` option + internal-config
  passthrough. All 7 acceptance criteria met with cited tests. Gates: `go
test ./internal/... ./pkg/agent/... -timeout=30s` 32/32 ok; `-race` on
  internal/text/internal/pkg/agent/pkg/tools ok; `go test ./...
-timeout=120s` ok; `go build ./...`, `go vet`, gofumpt, staticcheck all
  clean; dupl unchanged at 29 clone groups. No deviations from the phase
  contract; next eligible phase is Phase 4.
- **2026-08-02 (phase 4 — clai, worker session 2026-08-02-04):** Phase 4
  implemented (integration & e2e). New `main_cmd_ban_e2e_test.go` (root
  package) drives the real CLI through `run()` with the mock vendor: flag
  path (`-cmd-ban=touch`), config-file path (`textConfig.json`
  `"cmd-ban"`), profile path (`cmd_ban: ["git commit"]` — refusal plus a
  `git log` pass-through inside a real git repo via the `initGitRepo`
  helper), additive file+profile union (R1-04 revision), quoted bypass
  (`sh -c 'git commit -m x'` refused, R1-01/Q7: A), async no-spawn
  (`AsyncCmdSnapshotForTests()` empty), and the permissive-default
  regression (D4). Shared `assertCmdBanE2E` helper asserts every refusal
  names the entry AND exits 0 (no hard stop, D14). Docs: new "Command ban
  list" subsection in `architecture/tooling.md` Security (D1/D2/D5/D14 +
  the four matching limits) and new "Command ban configuration" subsection
  in `architecture/config.md` Tool selection (`cmd-ban`/`cmd_ban`/
  `-cmd-ban`/`WithCmdBanList`). All 8 acceptance criteria met with cited
  tests. Gates: `go test ./... -timeout=30s` ok; `go test ./... -count=1
-timeout=180s` ok (one transient flake in the pre-existing
  `TestAsyncCmdRun_BindsAsyncCmdToSessionContext` under parallel load,
  green in isolation and on re-run — unrelated to this phase); `go build
./...`, `go vet ./...`, gofumpt, staticcheck all clean; dupl unchanged
  at 29 clone groups. No deviations from the phase contract; next eligible
  phase is Phase 5.
- **2026-08-02 (phase 5 — clai, worker session 2026-08-02-05):** Phase 5
  implemented (pkg/agent e2e). New `pkg/agent/cmd_ban_e2e_test.go` drives the
  real agent path (`CreateTextQuerier` + mock vendor): single-agent refusal
  (names entry, marker absent, run completes — the test moved from Phase 4),
  sequential per-run isolation (banned → permissive → banned), concurrent
  distinct lists (`["touch"]` vs `["rm"]`, cmd + async_cmd_run, 3 cycles
  each, every refusal names the observer's own entry, ≥1 refusal guaranteed,
  no markers), and concurrent permissive + banned (permissive marker created
  every iteration, banned refused every iteration). Surprise: `Agent.Setup`'s
  unsynchronized `chat.SkipIndex = true` write raced under the phase's own
  concurrent `-race` gate — fixed with `skipIndexOnce sync.Once` (new
  decision D15); negative verification proved both the new fix and the Phase
  2 `cmdBanMu` are enforced by the tests (`-race` reported each race when
  reverted). Deterministic design deviations (recorded in the phase file):
  distinct commands need distinct tools (mock freetext input comes from one
  global env var); test-3 markers live under a non-existent parent (a
  cross-talked execution must fail harmlessly — D6 global state prevents
  logical cross-talk, the mutex only prevents data races); test-4 permissive
  command is `printf ok > <marker>` instead of a literal `touch`; Windows
  skip on the POSIX-spawning test. Gates: `go test ./pkg/agent/ -run
'TestAgentCmdBan' -count=1 -timeout=60s` ok 1.7s; `-race` ok 2.6s; full
  package and `-race` ok; `go test ./... -count=1 -timeout=180s` all ok;
  `go build ./...`, `go vet ./...`, gofumpt, staticcheck all clean; dupl
  unchanged at 29 clone groups. No remaining deviations from the phase
  contract; next eligible phase is Phase 6.
- **2026-08-02 (phase 6 — clai, worker session 2026-08-02-06):** Phase 6
  implemented (quality gates, final sweep — no code changes needed, all
  gates green on the Phases 1–5 tree). Baseline dupl
  `go run github.com/mibk/dupl@latest -t 80 .` → 29 clone groups (matches
  the recorded baseline); `make qa` exit 0 — staticcheck, gofumpt, `go fix`
  clean and all 37 packages in `./...` (36 with tests, 1 no test files)
  pass under `go test ./... -race -count=3 -cover
-timeout=30s` (the Phase 5 concurrent-agents `cmdBanMu` test green on all
  3 runs, no flakes); `go build ./...` and `go vet ./...` clean; post-change
  dupl → 29 clone groups, identical set; `go.mod` diff empty (no new
  third-party requires); `clai tools` lists `cmd`, `freetext_command`,
  `async_cmd_run` with the updated policy-refusal descriptions (verified
  against the real binary); no ban logic in `internal/text/generic` or
  `internal/vendors` (D6). All 6 acceptance criteria met with evidence
  cited in the phase file; status board fully Complete. No deviations from
  the phase contract; this was the last phase — the worklog is complete.
- **2026-08-02 (review 3 — clai, worker session 2026-08-02-06):** Holistic
  post-implementation review of all phases (1–6) against the README strategy
  and every phase contract. Traced the no-spawn invariant through all three
  spawn paths (`bash_tool_freetext_command.go` Call/CallWithContext,
  `async_cmds.go` callAsyncCmdRun), audited the branch order (empty-command
  error first), the matcher rules, the additive cascade end-to-end
  (file → profile → flag → agent API → `SetCmdBanList`), the `cmdBanMu`/
  `skipIndexOnce` concurrency fixes, and the docs. Re-ran every gate:
  dupl → 29 groups (baseline and post-change, identical); `make qa` exit 0;
  `go build ./...` and `go vet ./...` clean; `git diff go.mod` empty;
  `clai tools` shows all three tools with the policy-refusal description.
  One Low, non-blocking finding (R3-01: concurrent-agent logical cross-talk
  undocumented at the `WithCmdBanList` API boundary) — no phase reopens;
  status board stays all-Complete; worklog complete.
- **2026-08-02 (review 4 — clai, worker session 2026-08-02-07):** Holistic
  re-review of the completed worklog (phases 1–6) plus the R3-01 follow-up
  from review 3. Re-audited the no-spawn invariant through all three spawn
  paths, the branch order, the matcher rules, the additive cascade
  end-to-end, the `cmdBanMu`/`skipIndexOnce` concurrency fixes, and the
  docs — no new findings. Resolved R3-01 (Low) with a doc-comment-only
  change: `pkg/agent/agent.go` `WithCmdBanList` now warns that agents
  sharing a process share one active list and distinct lists must not be
  relied on for concurrent agents (new decision D16). Gates re-run after
  the change: `go test ./pkg/agent/ ./pkg/tools/ -count=1 -timeout=120s`
  ok; `go test -race ./pkg/agent/ -run 'TestAgentCmdBan' -count=1
-timeout=120s` ok 2.7s; `make qa` exit 0 (staticcheck, gofumpt, `go fix`,
  `go test ./... -race -count=3 -cover -timeout=30s` — all 37 packages ok);
  `go build ./...` and `go vet ./...` clean; dupl → 29 clone groups,
  identical set; `git diff go.mod` empty. Status board stays all-Complete;
  worklog complete.
- **2026-08-02 (review 5 — clai, worker session 2026-08-02-08):** Holistic
  re-review of the completed worklog (phases 1–6) — the third audit round
  of shipped code; the code was read directly rather than trusted from the
  implementation notes. Re-traced the no-spawn invariant through all three
  spawn paths (`bash_tool_freetext_command.go` Call/CallWithContext,
  `async_cmds.go` callAsyncCmdRun; `rg 'exec\.Command|Spawn\('` confirms no
  other freetext spawn point), the refusal-message contract byte-for-byte,
  the matcher rules, the additive cascade end-to-end (file → profile
  nil-guarded merge → flag append → agent API → `NewQuerier` setter before
  `setupTooling`; `applyFlagOverridesForText`/`applyProfileOverridesForText`
  never touch `CmdBan`), the `cmdBanMu`/`skipIndexOnce` concurrency fixes,
  the D10 generic setup-wizard dispatch (`handleValue` → `editSlice` for
  any `[]any`), and the docs — no new findings; R1-01…R3-01 stay resolved.
  Gates re-run: dupl → 29 clone groups (baseline set); `make qa` exit 0
  (staticcheck, gofumpt, `go fix`, `go test ./... -race -count=3 -cover
-timeout=30s` — all 37 packages ok); `go build ./...` and `go vet ./...`
  clean; `go test ./pkg/tools/ -run 'TestCmdBan' -count=1 -timeout=60s`
  ok; `go test -race ./pkg/agent/ -run 'TestAgentCmdBan' -count=1
-timeout=120s` ok; `go test . -run 'Test_e2e_cmd_ban' -count=1
-timeout=90s` ok; `git diff go.mod` empty; the real binary's `clai tools`
  lists all three tools with the policy-refusal description. Status board
  stays all-Complete; worklog complete.
- **2026-08-02 (phase 2 reopen — clai, worker session 2026-08-02-09):**
  R6-02 resolved in Phase 2. `SetCmdBanList` now snapshots its input slice
  at the setter boundary (`cmdBanList = append([]string(nil), entries...)`),
  so caller mutation after the setter returns can neither race a concurrent
  spawn check through the aliased backing array nor silently alter policy
  mid-run; the doc comment states the snapshot contract (new decision D17;
  the README "Ban-list ownership" strategy paragraph was already the
  contract). Regression test
  `TestCmdBanEnforcement_SetterSnapshotsInput` mutates `entries[0]` after
  `SetCmdBanList` and asserts the spawn reader observes the installed
  snapshot only (`rm` still banned, mutated `sudo` not leaked in) — red
  against the pre-fix direct assignment, green with the fix, and negative
  verification (temporary revert) reproduced the failure, proving the test
  guards the regression. Gates re-run: `go test ./pkg/tools/ -run
'TestCmdBan' -count=1 -timeout=30s` ok 0.009s; `go test ./pkg/tools/
-count=1 -timeout=60s` ok 2.142s; `go test ./pkg/tools/ -race -count=1
-timeout=60s` ok; `go test -race ./pkg/agent/ -run 'TestAgentCmdBan'
-count=1 -timeout=120s` ok; `go test . -run 'Test_e2e_cmd_ban' -count=1
-timeout=90s` ok; `go test ./... -count=1 -timeout=180s` ok; `go build
./...` and `go vet ./...` clean; gofumpt and staticcheck clean; dupl
  unchanged at 29 clone groups. Status board: Phase 2 back to Complete; the
  only remaining reopen is Phase 6 (R6-01 flaky-test investigation).
- **2026-08-02 (reopened phases fixed — clai):** R7-01 was resolved by adding
  an immutable command-ban policy to the agent query context; tool spawn checks
  prefer that per-agent policy while preserving the synchronized global
  fallback for CLI/direct callers. The concurrent agent e2e now requires every
  iteration to refuse under its own entry. R6-01 was resolved by the async
  cancellation implementation already present in the tree: independent
  process context, serialized cancellation state, and post-wait terminal
  status. Targeted cancellation tests passed 5 times; the concurrent agent
  race tests passed 3 times; `make qa`, `go build ./...`, `go vet ./...`, and
  post-change dupl all passed (29 groups, unchanged). Phases 5 and 6 are back
  to Complete.

- **2026-08-02 (review 8 — clai):** Independent post-implementation audit of
  all six phases, including the R7-01 per-agent context-policy fix, the R6-02
  setter snapshot, and the R6-01 async cancellation fix. Re-traced every
  in-scope spawn path and every cascade branch; no new findings and no prior
  regressions. Gates re-run from the repository root: `make qa` passed
  (staticcheck, gofumpt, `go fix`, and the full `-race -count=3 -cover`
  suite), `go build ./...` passed, `go vet ./...` passed, and
  `go run github.com/mibk/dupl@latest -t 80 .` reported 29 clone groups,
  unchanged from baseline. Targeted `pkg/tools` and `pkg/agent` command-ban
  tests also passed with timeouts. Status board remains all-Complete; the
  worklog is ready to close.

- **2026-08-02 (pre-release naming cleanup — clai):** Unified the profile
  JSON key to `cmd-ban` (removed `cmd_ban`) and renamed the async spawn tool
  to `async_cmd`. Because `async_cmd_run` is in production, it is retained as
  a registry alias (same tool instance) and as a mock-vendor alias; new
  callers use `async_cmd` (decision D18). Gates re-run: `make qa` passed,
  `go build ./...` / `go vet ./...` clean, targeted async/registry/mock
  packages pass, and dupl remains at 29 clone groups.

- **2026-08-02 (alias system — clai):** `clai tools` now renders aliases via
  a registry alias map (`SetAlias`/`Aliases`): the listing shows
  `- async_cmd (alias: async_cmd_run): ...` once instead of two duplicate
  entries, and `clai tools async_cmd_run` resolves to the canonical spec.
  Covered by `TestRegistry_SetAlias`, `TestSubCmd_ListsAliasUnderCanonicalTool`,
  and `TestSubCmd_DetailResolvesAlias`.

- **2026-08-02 (cmd/freetext_command unification — clai):** `cmd` and
  `freetext_command` are the same freetext shell tool; the duplicate
  `FreetextCmd` instance was removed and `freetext_command` is registered as
  a legacy alias of `cmd` (registry `SetAlias`, mock-vendor alias). The
  listing now shows `- cmd (alias: freetext_command): ...` once.

## Review feedback (review 8, 2026-08-02)

Reviewer: clai. Scope: independent post-implementation audit of all phases
after the review-7 and review-6 reopen fixes. Severity taxonomy: same as
review 1; High/Medium reopen, Low is non-blocking.

### Findings index

No new findings. R1-01…R3-01, R6-01, R6-02, and R7-01 remain resolved; no
regressions were observed.

### Verified good

The matcher still enforces the specified token rules and additive cascade. The
freetext `Call`/`CallWithContext` paths and `callAsyncCmdRun` all validate before
their spawn operations; refusal remains a normal tool error and does not hard
stop the run. `SetCmdBanList` copies caller input under its lock. Agent queries
carry an immutable copied policy in context, while CLI/direct callers retain
the synchronized global fallback. Async cancellation routes parent teardown
through the serialized cancellation path and finalizes status after wait.

### Verdict

Ships clean through the gates and remains correct against the worklog contract.
No phase reopens; the status board remains all-Complete.

## Post-release regression — duplicate tool schemas (2026-08-02)

The alias cleanup exposed a provider boundary that was not covered by the
original e2e tests. `Registry.All` and wildcard selection include lookup
aliases, but aliases resolve to the same specification name. Registering both
entries sent duplicate schemas to providers such as OpenAI, which rejected the
request with `Tool names must be unique.`

`internal/text/querier_setup_tools.go` now deduplicates selected tools by their
specification name before registration, for the all-tools, wildcard, exact,
and user-supplied tool paths. `Test_uniqueToolsDeduplicatesAliasesBySpecificationName`
covers canonical/alias and repeated selections. The registry still resolves
aliases for invocation; only the model-facing schema list is deduplicated.

- **2026-08-02 (review 8 — clai):** Independent post-implementation audit of
  all six phases, including the R7-01 per-agent context-policy fix, the R6-02
  setter snapshot, and the R6-01 async cancellation fix. Re-traced every
  in-scope spawn path and every cascade branch; no new findings and no prior
  regressions. Gates re-run from the repository root: `make qa` passed
  (staticcheck, gofumpt, `go fix`, and the full `-race -count=3 -cover`
  suite), `go build ./...` passed, `go vet ./...` passed, and
  `go run github.com/mibk/dupl@latest -t 80 .` reported 29 clone groups,
  unchanged from baseline. Targeted `pkg/tools` and `pkg/agent` command-ban
  tests also passed with timeouts. Status board remains all-Complete; the
  worklog is ready to close.

## Review feedback (review 8, 2026-08-02)

Reviewer: clai. Scope: independent post-implementation audit of all phases
after the review-7 and review-6 reopen fixes. Severity taxonomy: same as
review 1; High/Medium reopen, Low is non-blocking.

### Findings index

No new findings. R1-01…R3-01, R6-01, R6-02, and R7-01 remain resolved; no
regressions were observed.

### Verified good

The matcher still enforces the specified token rules and additive cascade. The
freetext `Call`/`CallWithContext` paths and `callAsyncCmdRun` all validate before
their spawn operations; refusal remains a normal tool error and does not hard
stop the run. `SetCmdBanList` copies caller input under its lock. Agent queries
carry an immutable copied policy in context, while CLI/direct callers retain
the synchronized global fallback. Async cancellation routes parent teardown
through the serialized cancellation path and finalizes status after wait.

### Verdict

Ships clean through the gates and remains correct against the worklog contract.
No phase reopens; the status board remains all-Complete.
