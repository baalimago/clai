# Phase 3 — remove the retry loop

**Status:** Complete
[Worklog README](./README.md)

## Goal

Delete `runStepWithRetry` and its machinery so a rate limit surfaces to the
caller on the first call, unretried (D3), without touching the token-count
seam that stoploss depends on.

## Specification

All sites were surveyed at the README's baseline ref (see "Survey
baseline"); re-anchor by symbol name, not line number. This phase is
independent of phase 2 (D15): it deletes code that *matches*
`models.ErrRateLimit`, and that name is stable whichever phase runs first.

### Removal table — production

| Symbol | Location | Action |
| --- | --- | --- |
| `runStepWithRetry` | `internal/text/session_runner.go` | delete |
| `waitForRateLimitReset` | `internal/text/session_runner.go` | delete (its swallowed-warning sites — phase 1, bucket 3 — die with it) |
| `sleepContext` | `internal/text/session_runner.go` | delete; its only callers are inside `waitForRateLimitReset` |
| `currentRetries` field | `internal/text/session_runner.go` | delete |
| `RateLimitRetries` | `internal/text/querier.go` | delete |
| `FallbackWaitDuration` | `internal/text/querier.go` | delete |
| `rateLimitLastAmTokens` field | `internal/text/querier.go` | delete |
| the `r.runStepWithRetry(ctx, session)` call in `Run` | `internal/text/session_runner.go` | rewrite to `r.executeModelStep(ctx, session)` |

### Keep table — the trap

| Symbol | Why it stays | Pinned by |
| --- | --- | --- |
| `models.InputTokenCounter` / `CountInputTokens` | `internal/text/stoploss.go` asserts the interface; the anthropic vendor calls it unconditionally in `StreamCompletions`. A dependency-following removal breaks token stoploss (README, "The retry loop is a relic") | existing stoploss suite, run in the sweep below |
| `models.ErrRateLimit` | absorbed, not deleted — the alias transition is owned elsewhere (README, D15) | the alias owner's acceptance criteria; `go build ./...` |

### Test changes

| Test | Action |
| --- | --- |
| `Test_sessionRunner_Run_RateLimitRetryIsIterative` (`internal/text/session_runner_test.go`) | replace with `Test_sessionRunner_Run_RateLimitSurfacesOnFirstCall`: the completer yields a rate-limit error; `Run` returns an error satisfying `errors.As` for `*models.ErrRateLimit`, and the completer records exactly one `StreamCompletions` call |
| `Test_sleepContext` (`internal/text/session_runner_test.go`) | delete with its symbol |
| `Test_waitForRateLimitReset_FallbackPath` (`internal/text/session_runner_test.go`) | delete |
| `Test_waitForRateLimitReset_FallbackHonorsCancel` (`internal/text/session_runner_test.go`) | delete |
| `Test_Querier_SavesConversation_WhenStreamSetupFailsDueToRateLimitTokenCount` (`internal/text/querier_test.go`) | keep, amend: the message-substring assertion referenced text produced by `waitForRateLimitReset`; the behavior under test (conversation persisted when stream setup fails) stays, with the assertion moved to `errors.As` for `*models.ErrRateLimit` on the returned error |
| `MockQuerierRateLimitTokenCountFail` (`internal/text/querier_test.go`) | keep; fixture for the amended test |

### Files

- `internal/text/session_runner.go`
- `internal/text/querier.go`
- `internal/text/session_runner_test.go`
- `internal/text/querier_test.go`

## Integration contract

| Trigger | Collaborators / fakes | Observable result | Required side effects | Prohibited side effects |
| --- | --- | --- | --- | --- |
| Completer yields a rate-limit error on the first step | `sessionRunner` with a mock completer | `Run` returns an error matching `*models.ErrRateLimit` via `errors.As` | none | a second `StreamCompletions` call; any sleep or wait before returning |
| Stream setup fails on the token-count path | querier with `MockQuerierRateLimitTokenCountFail` | the returned error matches `*models.ErrRateLimit`; the conversation is persisted | conversation saved | assertions on error message wording |

## Acceptance criteria

| Outcome | Evidence |
| --- | --- |
| No removed symbol remains anywhere in the module | `grep -rn "runStepWithRetry\|waitForRateLimitReset\|sleepContext\|RateLimitRetries\|FallbackWaitDuration\|rateLimitLastAmTokens\|currentRetries" --include='*.go' internal/ pkg/` returns nothing |
| A rate limit surfaces on the first call, unretried | `Test_sessionRunner_Run_RateLimitSurfacesOnFirstCall` |
| Conversation persistence on failed stream setup survives the removal | amended `Test_Querier_SavesConversation_WhenStreamSetupFailsDueToRateLimitTokenCount` |
| Token stoploss is untouched | existing stoploss and vendor suites green in `go test ./...` per Validation policy |

## Error coverage

| Failure | Expected outcome | Test |
| --- | --- | --- |
| Rate limit reported by the vendor mid-run | typed error to the caller on the first call; step ends | `Test_sessionRunner_Run_RateLimitSurfacesOnFirstCall` |
| Token-count failure during stream setup | error surfaces; conversation persisted for replay | amended `Test_Querier_SavesConversation_WhenStreamSetupFailsDueToRateLimitTokenCount` |

## Implementation notes

phase-3 worker subagent, 2026-09-05.

Executed as specified; no spec gaps. Deltas and surprises only:

- Import fallout beyond the removal table (expected consequence, not
  deviation): `time` dropped from `internal/text/querier.go`'s imports
  (its only uses were `FallbackWaitDuration` — nothing else in the file
  touches `time`). `session_runner.go` keeps all its imports;
  `models.InputTokenCounter`'s use there died with
  `waitForRateLimitReset` but the interface itself lives in
  `internal/models` and stays consumed by `stoploss.go:163` and
  anthropic's `claude_stream.go:30` — keep-table verified by grep and by
  the full suite.
- The replacement test sets `ResetAt: time.Now().Add(time.Hour)`, so a
  surviving sleep-until-reset path would blow the suite's timeout
  instead of passing silently — the prohibited-side-effect row (no sleep
  or wait) is enforced structurally, without asserting on wall-clock
  numerals.
- The amended querier test's mock (`MockQuerierRateLimitTokenCountFail`)
  builds `&models.ErrRateLimit{…}` as a literal without facts; it passes
  because phase 2 made `Error()` nil-receiver-safe on the facts pointer
  (README journal, phase 2 entry). No edit to the mock was needed, per
  the keep row.
- **Dupl baseline discrepancy (recording defect in phase 1, not a
  regression here).** `dupl -t 80` over the repo's own Go files reports
  **31** clone groups — before this phase's edits (measured via
  `git stash push` on the four touched files), after them, and **at the
  exact phase-1 baseline commit `59999b9`** (measured on a
  `git archive` copy). The recorded 28 is not reproducible with the
  configured command at its own measurement point; this phase's delta is
  exactly zero groups. Separately, a naive `dupl -t 80 .` today prints
  33 because a stray agent worktree copy sits under
  `.claude/worktrees/determined-lamport-b10f30/` and dupl scans it —
  an environment artifact, not code; left untouched (another session's
  worktree).

Verification commands run (all from the repo root):

- `go build ./...` ✓; `go vet ./...` ✓
- Acceptance grep from the criteria table: exit 1, zero matches over
  `internal/` and `pkg/`
- `go test ./internal/text/... -race -count=1 -timeout=30s -run
  'Test_sessionRunner_Run_RateLimitSurfacesOnFirstCall|Test_Querier_SavesConversation_WhenStreamSetupFailsDueToRateLimitTokenCount'`
  ✓ both pass
- `go run mvdan.cc/gofumpt@latest -w -l .` ✓ (reflowed
  `session_runner_test.go` once; clean after)
- `go run honnef.co/go/tools/cmd/staticcheck@latest ./...` ✓
- `go test ./... -race -cover -count=3 -timeout=30s` ✓ exit 0, 45
  packages ok, `internal/text` at 81.6% coverage
- `go fix ./...` ✓
- dupl: 31 groups (repo files, worktree-artifact excluded), identical
  before and after this phase — see delta note above
- `make qa` ✓ exit 0, end to end

## Review findings

None.
