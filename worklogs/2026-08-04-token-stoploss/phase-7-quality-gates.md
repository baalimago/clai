# Phase 7 — Quality gates

**Status:** Complete
**Back to:** [README](./README.md)

## Goal

Run the repository's full quality gate and re-establish the duplication
baseline after the change.

## Specification

Commands (from AGENTS.md and the Makefile):

```bash
go run github.com/mibk/dupl@latest -t 80 .
make qa
go run github.com/mibk/dupl@latest -t 80 .
```

`make qa` runs `lint` (staticcheck, gofumpt, go fix) and
`go test ./... -race -count=3 -cover -timeout=30s`.

Baseline before the change (2026-08-04): dupl reports 29 clone groups; the
README "Verified good" section records it. The post-change run must not add
needless clone groups beyond what the feature legitimately requires (the
ladder text duplication between `applyToolCallBudget` and any new warning
paths is the known acceptable case — prefer extracting shared warning strings
as constants).

## Acceptance criteria

1. `go build ./...` passes.
2. `go vet ./...` passes.
3. `make qa` passes (staticcheck + gofumpt + go fix + race tests ×3 + cover).
4. `go run github.com/mibk/dupl@latest -t 80 .` shows no new clone groups
   beyond the 29-group baseline (same count or a documented delta).
5. Every phase in the README status board is Complete, with each acceptance
   criterion citing its test.

## Implementation notes

Executed 2026-08-04 (imago + clai, worker session 7). Final gate sweep on
the finished branch; no production code changed in this phase.

Commands run (all from the repo root):

```bash
go build ./...
go vet ./...
go run mvdan.cc/gofumpt@latest -l .
go run honnef.co/go/tools/cmd/staticcheck@latest ./...
go fix ./...
make qa
go run github.com/mibk/dupl@latest -t 80 .
```

Results:

- `go build ./...` ✓ (exit 0)
- `go vet ./...` ✓ (exit 0)
- `go run mvdan.cc/gofumpt@latest -l .` ✓ — no diffs (the `make qa` `-w`
  run therefore rewrote nothing)
- `go run honnef.co/go/tools/cmd/staticcheck@latest ./...` ✓ (exit 0)
- `go fix ./...` ✓ — no changes
- `make qa` ✓ — lint target (staticcheck, gofumpt -w, go fix) then
  `go test ./... -race -count=3 -cover -timeout=30s`: all 38 packages ok,
  exit 0. Coverage highlights: internal/text 72.0%, internal/setup 79.6%,
  pkg/agent 93.8%, internal/text/generic 72.0%, internal/tools 81.9%.
  The full suite passed within the 30s per-package budget on the first
  attempt this session (no load-related retries needed).
- `go run github.com/mibk/dupl@latest -t 80 .` → **29 clone groups**, the
  documented baseline (README "Verified good") — no new clone groups were
  added by the feature. The known acceptable ladder-text case is already
  shared via the `ladderText` constant builder in `internal/text/stoploss.go`.

Verification of acceptance criteria:

1. `go build ./...` passes — ✓ (exit 0).
2. `go vet ./...` passes — ✓ (exit 0).
3. `make qa` passes — ✓ staticcheck + gofumpt + go fix + race tests ×3 +
   cover, exit 0 across all 38 packages.
4. dupl shows no new clone groups beyond the 29-group baseline — ✓ exactly
   29 clone groups.
5. Every phase in the README status board is Complete, with each acceptance
   criterion citing its test — ✓ all seven phases are Complete on the
   README status board; each phase file's "Verification of acceptance
   criteria" section cites its tests: Phase 1
   (`TestConfigurations_LegacyTokenWarnLimitKeyIgnored`,
   `Test_sessionRunner_Run_OversizedFirstQueryNoTokenPrecheck`), Phase 2
   (`internal/text/stoploss_test.go`, `TestAgent_WithStoploss`,
   `TestAgent_WithStoploss_ZeroValueDisabled`,
   `TestAgent_WithMaxToolCalls_Zero`), Phase 3
   (`internal/text/stoploss_controller_test.go`,
   `internal/text/stoploss_runner_test.go`, `Test_applyToolCallBudget`),
   Phase 4 (`TestSetupFlags`, `Test_resolveIntAlias`,
   `Test_applyFlagOverridesForText_Stoploss`,
   `Test_goldenFile_HELP_prints_usage`), Phase 5
   (`internal/setup/setup_stoploss_test.go`), Phase 6
   (`main_stoploss_e2e_test.go` cases 1–6, `rg` sunset search over
   `architecture/`), Phase 7 (this file).

## Review findings (review 10, 2026-08-05)

- [x] **R10-01 — High:** Reproduce and resolve the mandated race gate failure
  before marking this phase Complete. `go test ./... -race -cover -count=3
  -timeout=30s` and the focused `go test ./internal/text -race -cover
  -count=3 -timeout=30s` both timed out in `internal/text` at approximately
  30 seconds. The timeout stacks show feature tests blocked in
  `utils.AttemptPrettyPrint` while waiting for child processes during tool-call
  and `load_skill` emission. A focused non-race test passes, but that is not a
  substitute for the repository gate. Under the current implementation, a
  clean checkout cannot be signed off as satisfying Phase 7 acceptance
  criterion 3. Fix the process/output path or otherwise make the exact gate
  reproducibly pass, then record the command and result here.

### Fix round (imago + clai, worker session 2, 2026-08-05)

Root cause: `internal/utils.AttemptPrettyPrint` spawned the `glow`
subprocess for every printed message whenever glow was installed and
`NO_COLOR` was unset. The `internal/text` feature tests emit tool calls and
`load_skill` loads through the real executor into `strings.Builder`
writers; with glow installed each emission spawned two subprocesses
(version probe + render, ~63 ms each). Under `-race -count=3` the
accumulated spawn cost blew the 30 s package budget — a load-dependent
flake (review 9 passed, review 10 timed out twice; the glow binary
predates both reviews).

Fix (D25): glow is an interactive terminal renderer, so the print path now
spawns it only when the destination writer is a character device
(`isTerminalWriter`, the same heuristic `internal/utils/prompt.go` uses
for stdin) AND the renderer is installed (`glowAvailable`, probed once per
process via `sync.OnceValue`). Captured output (pipes, files, test
buffers) and machines without glow share the plain ANSI fallback — no
subprocess per message. The glow width math moved into the pure
`glowRenderArgs` helper. New tests pin the gate
(`TestAttemptPrettyPrint_SkipsGlowForCapturedWriters` — fake glow on PATH
must never spawn for a buffer), the terminal path
(`TestAttemptPrettyPrint_UsesGlowForTerminalWriters` — fake glow records
`-w 95` against a character-device writer), the width math
(`Test_glowRenderArgs`), and the writer heuristic (`Test_isTerminalWriter`).

Gates re-run from the repo root (2026-08-05):

- `go test ./internal/text -race -cover -count=3 -timeout=30s` ✓ — 7 s
  (previously timed out at 30.084 s)
- `go test ./... -race -cover -count=3 -timeout=30s` ✓ — all 38 packages;
  internal/text 71.7%, internal/utils 70.9%, internal/setup 79.6%,
  pkg/agent 93.8%
- `go build ./...` ✓; `go vet ./...` ✓; gofumpt ✓ (no diffs);
  staticcheck ✓; `go fix ./...` ✓ (no changes)
- `go run github.com/mibk/dupl@latest -t 80 .` → 29 clone groups
  (baseline unchanged)
- `go test . -run Test_e2e_stoploss -count=1 -timeout=120s` ✓ (6/6)

Docs: `architecture/colours.md` decision tree, `architecture/query.md`
output modes, and `architecture/replay.md` raw-vs-pretty now state that
glow rendering applies to terminal output only. R10-01 resolved; Phase 7
Complete.

Verified good in this review: the focused non-race regression
`go test ./internal/text -run
Test_toolExecutor_ExecuteBatch_RefusedCallHasNoSideEffect -count=1
-timeout=60s` passes. This confirms the timeout is not, by itself, evidence
that the refusal assertion is wrong; it is evidence that the required race
gate remains unresolved.

Cross-checks:

- Sunset search (Phase 1 criterion 1):
  `rg -n "TokenWarnLimit|tokenWarnLimit|countTokens|TokenCountFactor|tokenLengthWarning" --glob '!worklogs/**' --glob '!**/*_test.go' --glob '!architecture/**' .`
  returns only the intentionally retained `heuristicTokenCountFactor`
  implementations (generic + anthropic `InputTokenCounter`, R1-07/D12).
- Phase 6 criterion 2:
  `rg -n "token-warn|tokenWarn|TokenWarn|tokenLengthWarning" architecture/`
  returns no hits (exit 1).
- Holistic review (runbook step 4, all phases complete): the full branch
  diff was audited — the Phase 1 deletions left no orphaned imports or
  fields (`bufio`/`errors`/`path` removed with `countTokens`); the
  controller is stateless and rebuilt per run from the querier config
  (D18); the alias resolver returns errors from `parseFlags` with process
  exit only at the top-level caller (R3-03/D19); `main.go` usage and
  `printHelp` format args are in sync (15 `%v` args, help e2e green); the
  architecture docs updated in Phase 6 match the implemented behavior
  (batch preflight, handover after the tool batch, 0 = unlimited). No
  new decision recorded: the phase changed no production code, only the
  worklog status. Worklog complete.

## Review findings (review 12, 2026-08-05)

None. Re-ran the mandated race/coverage suite and all build, vet, formatter,
staticcheck, fix, e2e, and duplication checks. The 30 clone groups are the
documented 29-group baseline plus the accepted cross-package test-helper clone.

## Review findings (review 13, 2026-08-06)

The exact gates were re-run from the repo root on the working tree
(commands and results in the README's Review 13 entry); all pass. Three
findings concern this phase's territory (the follow-up work is journal-only,
so Phase 7 carries the reopened signoff):

- [ ] **R13-01 — Medium:** The config-migration announcement is printed to
      stdout on `-rf` (response-format) runs. `internal.Setup` sets
      `utils.ReadonlyConfig` from `PrintRaw` only (`internal/setup.go:510`),
      so a `-rf` scripting run whose configs need upgrading prints
      `added new field(s) to textConfig.json: ...` before the structured
      response (reproduced with the built binary; `-r` runs are protected).
      This violates the `-rf` contract "print only the final structured
      response". Fix direction: extend the machine-mode gate to
      `ResponseFormatPath` (or route announcements to stderr), and pin with
      a `-rf` + migration e2e test asserting the announcement never appears
      in the structured output.
- [ ] **R13-03 — Low:** The "dupl no new clone groups (30 baseline)" claim
      (session journal, 2026-08-05) is stale on the finished state: the
      working tree reports 31 clone groups vs 30 at HEAD. The delta is a
      dupl re-pairing artifact — the appended migration e2e tests in
      `main_confdir_e2e_test.go` shift the greedy matching so the
      pre-existing pair `Test_goldenFile_HELP_mentions_confdir_command` /
      `Test_goldenFile_TOOLS_lists_tools_and_footer` clears the threshold
      (reverting the confdir file to HEAD restores 30). Document the delta
      in the phase notes; no production code is duplicated.
- [ ] **R13-04 — Low:** `LoadTheme`'s upgrade announcement is not deferred
      for SETUP mode (the deferral closure covers only mode configs +
      profiles), so a theme.json upgrade prints before the wizard header
      (reproduced) and is at risk of erasure by the interactive TUI
      redraw — the documented root cause of the deferral. Fix direction:
      route the theme announcement through the same deferred-announcement
      path for SETUP, or accept and document the divergence.

Verified good in this review: `go build`, `go vet`, gofumpt, staticcheck,
`go fix`, the exact mandated race gate (`-race -cover -count=3
-timeout=30s`, all 38 packages, exit 0), `make qa` (exit 0), the six-case
stoploss e2e, and both sunset searches.
