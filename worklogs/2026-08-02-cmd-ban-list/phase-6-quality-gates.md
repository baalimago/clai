# Phase 6 — Quality gates

**Status:** Complete (2026-08-02, reopened review 6 resolved)

[← README](./README.md)

## Goal

Run the repository's full QA suite plus the duplication baseline, and record
the exact commands and outcomes.

## Specification

Per `AGENTS.md`, the duplication baseline must be established before
implementation and re-run after:

```bash
go run github.com/mibk/dupl@latest -t 80 .
```

The repository's QA gate is:

```bash
make qa
```

which runs `staticcheck`, `gofumpt -w -l`, `go fix`, and
`go test ./... -race -count=3 -cover -timeout=30s`.

This `-race` sweep exercises the Phase 5 concurrent-agents test — the
enforcement point for the R2-01 `cmdBanMu` resolution (added 2026-08-02,
when the quality-gate phase was renumbered 5 → 6).

Additional gates:

```bash
go build ./...
go vet ./...
```

Also verify, once for the whole change:

- No new third-party dependencies in `go.mod` (project rule).
- No vendor-specific workarounds leaked into generic code (`pkg/tools` is the
  tool layer, not a vendor package — ban logic belongs there per D6).
- `clai tools` output still lists `cmd` (alias `freetext_command`),
  `async_cmd` (alias `async_cmd_run`) with the updated descriptions.

## Integration contract

| Scenario | Observable result |
|----------|-------------------|
| `make qa` | All packages pass under `-race -count=3 -cover`, staticcheck clean, gofumpt no diff |
| `go build ./...` | Compiles cleanly |
| `go vet ./...` | Clean |
| dupl re-run | No needless duplication introduced vs baseline |
| `go.mod` | No new third-party requires |

## Acceptance criteria

- [x] Baseline dupl run recorded in implementation notes
- [x] `make qa` passes and is recorded
- [x] `go build ./...` and `go vet ./...` pass
- [x] Post-change dupl run shows no new clones
- [x] `go.mod` unchanged in dependency set
- [x] All phases 1–5 marked Complete on the README status board with evidence cited

## Error coverage

| Failure | Expected outcome |
|---------|-----------------|
| Test flake under `-count=3` | Investigate and fix before marking complete |
| Staticcheck finding | Fix in the owning phase; re-run full gate |
| gofumpt diff | Format; re-run full gate |
| dupl clone in new code | Refactor the duplicate; re-run |

## Implementation notes

Executing agent: clai (worker session 2026-08-02-06).

No code changes were needed: this phase is the final quality-gate sweep over
Phases 1–5, and every gate passed on the current tree. All commands were run
from the repo root.

Baseline dupl (recorded before the sweep, matches the review-1 baseline in
the session journal):

```bash
go run github.com/mibk/dupl@latest -t 80 .   # Found total 29 clone groups
```

Full QA gate (`make qa` runs staticcheck, `gofumpt -w -l`, `go fix`, then
`go test ./... -race -count=3 -cover -timeout=30s`):

```bash
make qa   # exit 0; all 37 packages in ./... (36 with tests, 1 no test files) ok under -race -count=3 -cover
```

The `-race -count=3` sweep exercises the Phase 5 concurrent-agents test
(`pkg/agent/cmd_ban_e2e_test.go`), the enforcement point for the R2-01
`cmdBanMu` resolution — green on every one of the 3 runs, no flakes.

Additional gates:

```bash
go build ./...   # clean
```

```bash
go vet ./...   # clean
```

Post-change dupl re-run:

```bash
go run github.com/mibk/dupl@latest -t 80 .   # Found total 29 clone groups — unchanged
```

The 29 clone groups are identical to the baseline; no new clones were
introduced by Phases 1–5. `gofumpt -l .` reports no unformatted files
(no diff left by `make qa`'s `-w` pass).

Whole-change verification (phase contract):

- `go.mod` unchanged in dependency set — `git diff go.mod` is empty; the
  require set is `go_away_boilerplate`, `golang.org/x/exp`, `golang.org/x/net`
  (plus indirect `golang.org/x/text`), exactly as before this effort.
- No vendor-specific workarounds leaked into generic code: `pkg/tools` owns
  the ban logic (`cmd_ban.go` per D6); `rg 'ban|Ban' internal/text/generic`
  and `rg 'CmdBan|cmdBan|banned by policy' internal/vendors` both return no
  hits.
- `clai tools` still lists `cmd` (alias `freetext_command`) and `async_cmd`
  (alias `async_cmd_run`) with the updated descriptions. Verified against
  the real binary (built with `go build -o /tmp/clai-gate ./`): the listing
  shows both canonical rows with their alias annotations, and
  `clai tools cmd|freetext_command|async_cmd` each emit the policy note
  "Some commands are refused by configured policy and must not be retried."
  (the `TestCmdBanEnforcement_DescriptionsMentionRefusal` unit test pins the
  same contract).

Acceptance criteria evidence:

- Baseline dupl run → 29 clone groups (above; also recorded in README
  Session journal, planning entries).
- `make qa` exit 0 — full `-race -count=3 -cover` sweep of all 37 packages
  (36 with tests, all ok; `internal/models/completion` has no test files)
  plus staticcheck, gofumpt, `go fix` clean.
- `go build ./...` and `go vet ./...` exit 0.
- Post-change dupl → 29 clone groups, identical set to baseline.
- `go.mod` diff empty.
- README status board: Phases 1–5 marked Complete with per-phase evidence
  cited in each phase file's implementation notes and in the Session
  journal; Phase 6 now Complete.

No deviations from the phase contract; no findings to fix (the phase's only
error-coverage row that could fire — test flake under `-count=3` — did not;
all 3 counted runs were green).

## Review findings (review 6, 2026-08-02)

Reviewer: clai. The phase is reopened for R6-01; severity taxonomy and the
complete index live in the README.

- **R6-01 (Medium) — the independent QA gate flaked.** The first reviewer run
  of `make qa` failed in the pre-existing
  `TestAsyncCmdRun_BindsAsyncCmdToSessionContext` (`pkg/tools/async_cmds_test.go:199`):
  `expected cancelled terminal status after session cancel`. The same test
  passed when run three times under `-race`, and a second full `make qa` passed,
  but that only demonstrates intermittency. The phase contract explicitly says
  a test flake under `-count=3` must be investigated and fixed before marking
  the phase complete. Reproduce under the QA command, identify whether the
  cancellation/terminal-status assertion has a scheduling race, and either fix
  it or record a justified, deterministic quarantine with an owner and follow-up
  test; do not treat a passing rerun as resolution.

Verification: `go test ./... -count=1 -timeout=120s` passed; the first
`make qa` failed as above; the immediate second `make qa` passed. Phase 6 stays
reopened until the intermittent failure is explained or eliminated.

## Implementation notes (review 6 resolution, 2026-08-02)

The cancellation race was resolved in `pkg/tools/async_cmds.go`: async
commands now use an independent execution context, cancellation flags and the
SIGINT/SIGKILL sequence are serialized under the command mutex, and terminal
status is finalized only after the process wait. This removes the competing
`CommandContext` cancellation watcher that could report `failed` before the
cancel flags were visible. The regression test
`TestAsyncCmdRun_SessionCancelNeverLosesToSignalExit` exercises repeated
cancelled exits.

Verification:

```bash
go test ./pkg/tools -run 'TestAsyncCmdRun_(BindsAsyncCmdToSessionContext|SessionCancelNeverLosesToSignalExit)' -count=5 -timeout=180s  # pass
make qa  # pass: race/count=3 suite, staticcheck, gofumpt, go fix
go build ./... && go vet ./...  # pass
go run github.com/mibk/dupl@latest -t 80 .  # 29 clone groups, unchanged
```

## Review findings (review 1, 2026-08-02)

Reviewer: imago. No findings for this phase — it defines gates only. The
following were re-run by the reviewer against the current tree (all phases
Not Started): `go build ./...` ✓, `go vet ./...` ✓,
`go test ./pkg/tools/ -timeout=60s` ✓, and the dupl baseline
`go run github.com/mibk/dupl@latest -t 80 .` → 29 clone groups, matching
the session journal; the listed pair is the pre-existing
`internal/vendors/anthropic/source_reader.go` vs
`internal/vendors/pi/source_reader.go` clone. The ACs stay as written
(this phase was renumbered 5 → 6 when Phase 5 "pkg/agent e2e" was added,
2026-08-02); note that the full `make qa` gate (staticcheck, gofumpt, `-race
-count=3`) was not re-run in review 1 — it is the phase's own deliverable.

## Review findings (review 7, 2026-08-02)

Reviewer: clai. Phase remains reopened for the earlier R6-01 requirement; the
full index and severity taxonomy are in the README.

Verified good: `make qa` passed in this review, as did
`go test ./... -count=1 -timeout=180s`, the targeted agent race suite, and the
post-change dupl run (29 clone groups). This fresh green result does not
resolve R6-01: the phase contract requires explaining or isolating the
previously observed intermittent `TestAsyncCmdRun_BindsAsyncCmdToSessionContext`
failure rather than relying on a passing rerun.

## Review findings (review 8, 2026-08-02)

Independent re-audit re-ran `make qa`, `go build ./...`, `go vet ./...`, and
the post-change dupl command with timeouts; all passed and dupl remained at 29
clone groups. The async cancellation regression and full race sweep remain
green. R6-01 remains resolved; Phase remains Complete.
