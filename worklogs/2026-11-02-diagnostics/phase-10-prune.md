# Phase 10 — Prune

**Parent:** root · **Class:** prune · [README](README.md)

## Goal

Remove the obsolete skills diagnostic path after the CLI uses the shared policy.

## Interface

None. This phase removes only the names in the Removes table.

Consumes: `LevelResolver.Resolve` (phase 3), `Reporter.Report` (phase 4),
`CLI.Run` (phase 9), and `internal/skills:Discover` (import).

## Acceptance criteria

| #   | Criterion                                                                           | Proves      | Proof                               |
| --- | ----------------------------------------------------------------------------------- | ----------- | ----------------------------------- |
| 1   | The old skills logger and parser have no references outside the allowed hits.       | Removes     | `TestPrunedSkillsDiagnostics`       |
| 2   | Skills events and legacy level controls retain the behavior established by phase 9. | F9, Removes | `TestSkillsDiagnosticCompatibility` |
| 3   | No golden output changes.                                                           | Removes     | `TestDiagnosticOutputGoldens`       |

## Test cases

| Test                                | Level                | Proves      | Given                                               | Then                                        | Must not                                |
| ----------------------------------- | -------------------- | ----------- | --------------------------------------------------- | ------------------------------------------- | --------------------------------------- |
| `TestPrunedSkillsDiagnostics`       | repeatable command   | Criterion 1 | The production and test source tree                 | Removed names have no unapproved references | Ignore generated source or tests        |
| `TestSkillsDiagnosticCompatibility` | integration scenario | Criterion 2 | Skills discovery with legacy and new level controls | Events and levels match phase 9             | Restore the old logger or change output |
| `TestDiagnosticOutputGoldens`       | integration scenario | Criterion 3 | The phase 9 fixtures                                | All snapshots match                         | Regenerate or change a golden           |

## Error management

None. This phase adds no error path.

## Golden diff

None.

## Removes

| Member                                                            | Consumers                             | Re-pointed to                           | Allowed hits                               |
| ----------------------------------------------------------------- | ------------------------------------- | --------------------------------------- | ------------------------------------------ |
| `internal/skills.logger` and `newLogger`                          | Skills discovery and activation       | Phase 4 `Reporter` through phase 9      | This Removes table and historical worklogs |
| `internal/skills.parseLogLevel` and `ParseLogLevelFromEnv`        | Skills configuration and parser tests | Phase 3 `LevelResolver` through phase 9 | This Removes table and historical worklogs |
| `internal/skills.LogLevel`, its constants, and `Options.LogLevel` | Skills discovery and its callers      | Phase 3 `Level` and phase 4 `Type`      | This Removes table and historical worklogs |

## Local facts

`LOG_LEVEL_SKILLS` remains a supported legacy input through the Legacy
controls table (README). Removing the old parser does not remove that
configuration behavior.

## Human required

None.

## Implementation notes

| Run    | Commit      | Command | Result                                                                                         |
| ------ | ----------- | ------- | ---------------------------------------------------------------------------------------------- |
| replan | 2c9c4ff+wip | —       | V1-11: re-pointed names follow the renames. V1-07: Local facts cite the Legacy controls table. |
| start  | 0ea7c95+wip | `go run mvdan.cc/gofumpt@latest -w -l . ; go run honnef.co/go/tools/cmd/staticcheck@latest ./... ; go vet ./... ; go test ./... -race -cover -count=3 -timeout=30s ; go fix ./... ; go run github.com/mibk/dupl@latest -t 80 .` | all rc 0, 92 packages ok, 0 FAIL, dupl 41 clone groups |
| end    | 0ea7c95+wip | same six gates, reverse order, `go test` with `-shuffle=on` | all rc 0, 92 packages ok, 0 FAIL, dupl 41 clone groups |
| end    | 0ea7c95+wip | same six gates, another order, fresh shuffle seed | all rc 0, 92 packages ok, 0 FAIL, dupl 41 clone groups |
| end    | 0ea7c95+wip | `TestPrunedSkillsDiagnostics`: `grep -rn -E '\b(newLogger\|parseLogLevel\|ParseLogLevelFromEnv\|LogLevelInfo\|LogLevelWarn\|LogLevelError)\b\|skills\.LogLevel\|skills\.logger\|Options\.LogLevel\|internal/skills/logger\.go' . --exclude-dir=.git` | hits only in this Removes table and the historical worklog 2026-09-05-error-propagation |
| end    | 0ea7c95+wip | `go test ./e2e -run '^TestDiagnosticOutputGoldens$'` and `go test ./internal/cli -run TestSkillsDiagnosticCompatibility` | ok, no golden file changed |
| fix-start | 0ea7c95+wip | `go run mvdan.cc/gofumpt@latest -w -l . ; go run honnef.co/go/tools/cmd/staticcheck@latest ./... ; go vet ./... ; go test ./... -race -cover -count=3 -timeout=30s ; go fix ./... ; go run github.com/mibk/dupl@latest -t 80 .` | all rc 0, 92 packages ok, 0 FAIL, dupl 41 clone groups |
| fix-end   | 0ea7c95+wip | same six gates, `go test` with `-shuffle=on` | all rc 0, 93 packages ok, 0 FAIL, dupl 43 clone groups |
| fix-end   | 0ea7c95+wip | same six gates, fresh shuffle seed | all rc 0, 93 packages ok, 0 FAIL, dupl 43 clone groups |

Andon: none.
Surprises:
- Phase 9 forced the skills level to error under `-response-format`; kept by giving discovery a reporter-less run Env in that case (`internal/text/setup_querier.go`).
- `LOG_LEVEL=warn` keeps the `DEBUG_SKILLS` selector, so discovery notices still show; the compatibility test pins that phase 9 behaviour.
- `TestDebugSkills` stays in `internal/skills/logging_test.go`; only `TestParseLogLevel` went with the parser.

## Review findings

### Review 1, 2026-11-09 (root)

**R1-07 · minor · owner fix.** Criterion 1 and its Test cases row cite `TestPrunedSkillsDiagnostics`, but no Go test of that name exists. The grep in Implementation notes was run once, so the gates do not stop a removed name from returning.
- [x] Add the test (e.g. in `e2e/diagnostic_static_test.go`), or name the exact command in the Proof column.

Verified good: re-ran the recorded grep, and the only hits are this Removes table and the 2026-09-05 worklog. `TestSkillsDiagnosticCompatibility` pins phase 9's behaviour, as criterion 2 asks; whether that behaviour is right is R1-04.
