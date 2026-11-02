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

## Review findings

None.
