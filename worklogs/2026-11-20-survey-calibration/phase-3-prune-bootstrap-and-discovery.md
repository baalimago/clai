# Phase 3 — Prune bootstrap and discovery

**Parent:** root · **Class:** prune · [README](README.md)

## Goal

Remove what phase 2 left without a caller — bootstrap and discovery, the registry's mutation paths, the
per-chunk request limit and the `max-speakers` setting — so `internal/audio/` ends with at most 10,300
non-test lines (I9, amended 2026-11-22: the 9,942 before phase 1 plus about 3 %).

## Interface

Specified by the Removes table. No interface is added or changed: the output of every route, the
`calibration` event log, the board and every golden are what phase 2 left.

Consumes:

- `Coordinator.Run` and the survey path (phase 2): the only caller of what stays
- `maxVoices` (phase 1): what `max-speakers` is re-pointed to
- `Snapshot`, `Speaker`, `Speaker.Clips` (repository and phase 1): what the registry is reduced to
- The I9 baseline in phase 1's Implementation notes (L1.4)

## Acceptance criteria

| #   | Criterion | Proves | Proof |
| --- | --------- | ------ | ----- |
| 1   | No Go file names a removed member | the Removes table, I4, I8 | the four `grep` commands under Test cases |
| 2   | `-max-speakers` is no flag, `transcribe.max-speakers` is no field, and an old `audioConfig.json` that still sets it, or `strict-speakers`, loads | the `max-speakers` rows, I7, Q2 | `TestOldConfigSettingsLoad` |
| 3   | The plan still reserves 88 s, now from the constant | I5, the `Budgets.MaxSpeakers` row | `TestPlanReservesEightSlots` (phase 2; it drops only its `Budgets.MaxSpeakers` 3 setting and renames the row that named it, every assertion unchanged), the adapted rows of `plan_test.go` |
| 4   | The registry is two value types with no way to change one after it is built | I4 | `grep` command 1; `go doc ./internal/audio Speaker` shows `ID`, `Sample`, `Clips` only |
| 5   | Phase 2's scenarios pass with every assertion unchanged, edited only where the Removes table names them (`TestPlanReservesEightSlots` and `TestSurveyPathIsTheOnlyPath` drop the settings that no longer exist, and the one `TestPlanReservesEightSlots` row named after `max-speakers` is renamed), and every golden is unchanged | the class's proof | `go test ./internal/audio -run 'SurveyPath\|Join\|Retry\|Parallelism\|MoreThanEight\|PathLength\|NothingToLabel\|Unmatched\|RealFailure\|Board\|CalibrationLog'`; `go test ./e2e -run '^TestDiagnosticOutputGoldens$'` |
| 6   | `internal/audio/` has at most 10,300 non-test lines by L1.4's command (I9 as amended 2026-11-22) | I9 | the line-count command under Test cases |
| 7   | The gate passes unedited | I11 | `make qa`; `go vet ./...`; `go run github.com/mibk/dupl@latest -t 80 .` |

## Test cases

| Test | Level | Proves | Given | Then | Must not |
| ---- | ----- | ------ | ----- | ---- | -------- |
| `grep -rnE 'SpeakerRegistry\|RegistryTx\|RecordVerification\|AddSpeaker\|ReplaceSample\|DiscoveryReport\|CandidateRejection\|RequestLimitError\|IsRequestLimit\|MaxRequestsPerChunk\|MaxSpeakers\|RequestBootstrap\|RequestForward\|RequestReversedRetry\|RequestDiscovery\|RequestReplacementRetry\|RequestRevisit\|SampleVersion\|FrozenReason' --include='*.go' .` | command | criterion 1, I4, I8 | The repository after the phase | No output | — |
| `grep -rnE 'Discovery\|[Bb]ootstrap' --include='*.go' --exclude='*_test.go' internal/audio` | command | criterion 1 | The repository after the phase | No output (`eta.go` names no discovery since phase 2, L2.8) | — |
| `grep -rnE '\.byStatus\(\|\.Ambiguous\(\)\|\.RangesFor\(\|\.SourceTime\(\|\.RunDir\(\)\|[^.a-zA-Z]better\(\|Mapping\) Failed' --include='*.go' internal/audio` | command | criterion 1 | The repository after the phase | No output | — |
| `grep -rnE 'max-speakers\|max_speakers\|max-requests-per-chunk\|prefix-reserve-exhausted' --include='*.go' --include='*.md' . \| grep -vE '^\./(worklogs\|spike)/'` | command | criterion 1, Q2 | The repository after the phase | Only the Allowed hits of the Removes table: `TestOldConfigSettingsLoad` and the one sentence under "Stitching and failure policy" of `architecture/audio.md`; no hit in `survey_test.go` or under "Request budgets and encoding" | — |
| `go doc ./internal/audio Speaker` | command | criterion 4, I4 | The repository after the phase | The fields `ID`, `Sample`, `Clips` and no other; `go doc -u ./internal/audio Snapshot` shows `Speakers`, `Samples` and `find` | — |
| `go test ./internal/audio -run 'SurveyPath\|Join\|Retry\|Parallelism\|MoreThanEight\|PathLength\|NothingToLabel\|Unmatched\|RealFailure\|Board\|CalibrationLog\|PlanReserves'` | command | criterion 5 | The repository after the phase; `git diff` of phase 2's test files | Pass; the diff shows only the removed settings of the Removes table and the one renamed row of `TestPlanReservesEightSlots` | Change an assertion, a fixture or an expectation |
| `TestOldConfigSettingsLoad` | unit | criterion 2, I7, Q2 | An `audioConfig.json` with `"strict-speakers": true, "max-speakers": 3, "parallelism": 2`; the transcribe flag set | The configuration loads with `parallelism` 2; the flag set has no `max-speakers` and no `strict-speakers`; `clai audio transcribe -max-speakers 3 f.wav` is refused as any unknown flag is, before a request | Fail to load an old config; accept the flag silently |
| `plan_test.go`, rows adapted | unit | criterion 3, I5 | The planner with the default budgets; a seconds budget of 80 s | Reserve 88 s; with 80 s the error reads `prefix reserve 1m28s (8 voices × 11s) reaches max-request-seconds 1m20s; raise the budget` | Name `max-speakers` |
| `find internal/audio -name '*.go' ! -name '*_test.go' -print0 \| xargs -0 cat \| wc -l` | command | criterion 6, I9 | The repository after the phase, and the baseline in phase 1's Implementation notes | A number at most 10,300; it and the 9,942 baseline written in Implementation notes | Count by another rule than L1.4's |
| `go test ./e2e -run '^TestDiagnosticOutputGoldens$'` | command | criterion 5 | No `-update-diagnostic-goldens` | Pass; `git status e2e/testdata` clean | Regenerate a golden |
| `make qa`, `go vet ./...`, `go run github.com/mibk/dupl@latest -t 80 .` | command | criterion 7, I11 | The repository after the phase | Exit 0, twice | Edit the gate |

## Error management

None. The phase adds no failure. The one message it rewords is the planner's reserve error (Removes,
`Budgets.MaxSpeakers`); `RequestLimitError` goes with the limit that raised it, which phase 2 stopped
raising.

## Golden diff

None.

## Removes

"Tests" under Consumers are deleted with the member unless a re-pointing is named. Allowed hits are
outside `worklogs/`, which keeps its history.

| Member | Consumers | Re-pointed to | Allowed hits |
| ------ | --------- | ------------- | ------------ |
| `Discovery` and its methods `Pass`, `PassWith`, `Revisit`, `Bootstrap`, `Resolve`, `Recover`, `Replace`, `RecordAccepted` (and the unexported `request`, `discover`, `candidates`, `promote`, `add`, `logRegistry`, `logReplace`, `replacementClip`), `DiscoveryReport`, `CandidateRejection`, `candidate`, `hadMixed`, `overlapsAny`, `discoveryIterations` (`internal/audio/discovery.go`) | None in production after phase 2. Tests: `discovery_test.go` (whole file: `newDiscovery`, `assertOneIDPerVoice`, 7 tests) | Nothing. `RequestFunc`, declared in the same file, moves to `internal/audio/merge.go` unchanged | None |
| The discovery reasons `reasonCollapsedOntoKnown`, `reasonSameVoice`, `reasonMixed`, `reasonMissing`, `reasonAmbiguousKnown`, `reasonClipsTried` (`discovery.go`); `reasonNoClip`, `reasonReserveExhausted`, `reasonFrozen`, `reasonStale` (`internal/audio/sample.go`); `rejectedText` (`internal/audio/calibration_log.go`) | `discovery.go`, `registry.go`; `summarizeUnknown` lists `reasonReserveExhausted` among the reasons it prints (`internal/audio/stitch.go`) | `summarizeUnknown` prints `no-sample` and `non-material` only; no speech has the removed reason after phase 2. `reasonNoRun`, declared in `discovery.go` today, stays and moves to `sample.go` | None |
| `SpeakerRegistry`, `NewSpeakerRegistry`, `SpeakerRegistry.Snapshot`, `snapshotLocked`, `RecordVerification`, `Discover`, `RegistryTx`, `RegistryTx.Snapshot`, `AddSpeaker`, `ReplaceSample`, `maxReplacements` (`internal/audio/registry.go`) | `discovery.go`. Tests: `registry_test.go` (5 tests) | Nothing. The helper `clip` of `registry_test.go` moves to `mapping_test.go`, which uses it | None |
| `Speaker.SampleVersion`, `Replacements`, `Verified`, `Ranges`, `Frozen`, `FrozenReason`; `Snapshot.Version` (`registry.go`) | `registry.go`, `discovery.go` and their tests; `merge_test.go`'s registry check of Merge.6 reads `snap.Version != 0` | Nothing: `Speaker` is `ID`, `Sample`, `Clips`; `Snapshot` is `Speakers` with `Samples()` and `find`. `Snapshot.find` stays where it is: phase 2's `planRetry` calls it to take a speaker's next clip. The Merge.6 check drops its `Version` term and keeps the rest | None |
| Request kinds `RequestBootstrap`, `RequestBootstrapCandidates`, `RequestForward`, `RequestReversedRetry`, `RequestDiscovery`, `RequestReplacementRetry`, `RequestRevisit` (`calibration_log.go`) | `discovery.go` only (grep 2026-11-22: no test names one any more) | `RequestSurvey`, `RequestLabelling`, `RequestRetry` (phase 2) and the merge's two (phase 1) are the kinds | None |
| `RequestLimitError`, `IsRequestLimit`, `Coordinator.MaxRequestsPerChunk` (`internal/audio/coordinator.go`); `Splitter.MaxRequestsPerChunk` and its hand-over in `Splitter.calibrate` (`internal/audio/split.go`) | No production reader after phase 2. Tests: `TestToolBridgePropagatesCalibrationError` (`calibration_integration_test.go`) builds a `RequestLimitError` as its sample error; `TestSurveyPathIsTheOnlyPath` sets the field | The tool-bridge test propagates a transport error instead, assertions otherwise unchanged; `TestSurveyPathIsTheOnlyPath` drops the `MaxRequestsPerChunk` setting (and `Budgets.MaxSpeakers`, see that row), assertions unchanged | None |
| `-max-speakers`: `Flags.MaxSpeakers`, its registration, its override row and its usage line (`internal/audio/cmd.go`: the usage text, `Flags`, `Flags.Register`, the budget overrides) | `TestFlagOverridesBudget`, `TestFlagRejectsNegativeBudget` (`calibration_integration_test.go`) | The two tests keep their `max-request-bytes` and `max-request-seconds` rows | `TestOldConfigSettingsLoad` (asserts the flag is absent) |
| `transcribe.max-speakers`: `TranscribeConfig.MaxSpeakers`, `Default.Transcribe.MaxSpeakers`, the negative check and the override in `ResolveBudgets` (`internal/audio/querier.go`) | the budget tests of `querier_test.go`; `calibration_integration_test.go` (`smallBudgetConf`, `TestCalibrationToolBridgeUsesSameEngine`, `TestNoStrictSpeakers`) | An old config that sets the field loads, as unknown fields do. `TestNoStrictSpeakers` becomes `TestOldConfigSettingsLoad`. `smallBudgetConf` loses its `MaxSpeakers` line and nothing else: its 80 s budget stays, since the tests that plan with it set their own budget since phase 2 (L2.10). `TestCalibrationToolBridgeUsesSameEngine` drops `s.Budgets.MaxSpeakers != 3` from its condition and keeps the rest, the 80 s included; its config-error case sets a negative `max-request-seconds` and looks for that name. The budget tests of `querier_test.go` keep its byte and seconds assertions | `TestOldConfigSettingsLoad` (the literal in its old config) |
| `Budgets.MaxSpeakers` (`querier.go`: the field and its default in `ResolveBudgets`) | `ChunkPlanner.Plan` (`internal/audio/plan.go`); phase 2's pin in `Coordinator.Run` (`budgets.MaxSpeakers = maxVoices`); `plan_test.go` (the `larger max-speakers` row and the reserve-error row); phase 2's `TestPlanReservesEightSlots` (its row named `max-speakers 3 is not read`) and `TestSurveyPathIsTheOnlyPath`, which set it to 3 to show it is not read | `maxVoices` (phase 1): the reserve is `maxVoices × prefixSlot`. The planner's error becomes `prefix reserve %v (8 voices × 11s) reaches max-request-seconds %v; raise the budget`. The pin in `Run` is deleted (it is the only trace left in `coordinator.go`). The `larger max-speakers` plan row is deleted. `TestPlanReservesEightSlots` and `TestSurveyPathIsTheOnlyPath` drop that one setting and keep every assertion; the row `max-speakers 3 is not read` of `TestPlanReservesEightSlots` is renamed `the reserve is 88 s`, its fixture and expectations unchanged | None |
| `architecture/audio.md`: `"max-speakers": 8` in the example, the `transcribe.max-speakers` row, the `-max-speakers` row; the words "whatever `max-speakers` holds" under "Request budgets and encoding" | Readers | "Request budgets and encoding" says the reserve is fixed at 8 slots of 11 s, without naming the setting. One sentence under "Stitching and failure policy": `max-speakers` was removed with this effort, room for 8 voices is fixed, and an old config that still sets it loads | That one sentence |
| `Mapping.byStatus`, `Mapping.Failed`, `Mapping.Ambiguous` (`internal/audio/mapping.go`) | No production caller after phase 2 (the retry reads `Samples[i].Status`); `discovery.go`. Tests: `TestMappingRejectsMixedSample`, the two-samples-on-one-label test and the missing-sample test of `mapping_test.go`; `discovery_test.go` | The three mapping tests read `mp.Samples[i].Status` (`SampleMixed`, `SampleMissing`, `SampleAmbiguous`) for the same samples and counts; `LabelMapper.Map` itself is not touched | None |
| `Mapping.RangesFor` (`mapping.go`) | `Discovery.RecordAccepted` only | Nothing | None |
| `better` (`internal/audio/sample.go`) | `Discovery.promote` only; the merge ranks by Merge.5 in `merge.go` | Nothing | None |
| `RequestManifest.SourceTime` (`internal/audio/assemble.go`) | No production caller. Tests: two assertions of `TestManifestBoundariesAreSampleExact` (`assemble_test.go`) | The two assertions go with the member; the test's region-boundary assertions (`ReqStart`, `ReqEnd`, `SrcStart`, `SrcEnd`), which hold the same arithmetic, stay | None |
| `AudioAssembler.RunDir` (`assemble.go`) | No production caller. Tests: `assemble_test.go` (the clean-up tests, `assertRunDirEmpty`, the contract suite's probe) | The tests read the field `runDir` directly, being in the package; every assertion unchanged | None |
| Comments that still describe the old path, reworded to what the code does now, code unchanged: the doc of `Speaker` ("cut from verified speech", "a speaker the old path registered") and of `Snapshot` ("at Version") in `registry.go`; the doc of `LabelMapper` ("registry state changes happen in Discover") and the comment on the several-candidates case inside `LabelMapper.Map` ("discovery keeps the better clip") in `mapping.go`; the doc of `logResponse` ("before discovery promotes anything") in `calibration_log.go`; the field comments of `Region.Candidate` and `SampleClip.Candidate` ("a discovery clip") in `assemble.go` | Readers | A candidate is a clip that is not a registry speaker's: a merge link's candidate or a retry's | None |
| Any unexported member the rows above leave without a caller (staticcheck names it; with the rows above applied to the tree of 2026-11-22 it names none in the calibrated path) | The removed code only | Nothing | None |

## Local facts

| ID   | Fact | Decided by |
| ---- | ---- | ---------- |
| L3.1 | Nothing is removed that a route other than the calibrated one reads: `SampleExtractor`, `Materiality`, `LabelMapper`, the assembler, the planner, the cache, `stitch`, the known-voices naming and the plain and request-local split paths are untouched. | README Scope |
| L3.2 | `-max-speakers` on the command line is refused like any flag clai does not define. No alias and no warning is kept for it. An `audioConfig.json` that sets `max-speakers` loads and the value is ignored. | Q2; the `strict-speakers` precedent of 2026-11-22 |
| L3.3 | The test doubles (`voiceProvider`, `arithmeticAssembler`, `scriptedRunner`, `manifestFor`) stay; only faults that nothing uses any more go (`mixCandidateCores`, `mixCandidateVoice`, `mixAllCandidates`, if unused). | I11 |
| L3.4 | The measurement of I9 is L1.4's command, run on the tree after this phase and compared with the number phase 1 recorded. If it is not lower, the phase is not complete: the worker looks for more dead code under the Removes rows, and pulls a design andon rather than touching live code. | I9 |
| L3.5 | Re-planned on R2-05 (2026-11-22). Every Removes row was checked by grep against the tree of that day and applied to a scratch copy: `go build ./...` passes, and `staticcheck -tests=false ./internal/audio/...` names nothing of the calibrated path. L1.4's count goes from 11,085 to 10,458, so this table frees about 627 lines (discovery.go 392, registry.go 136, the limit, the kinds, the reasons, `max-speakers`, and R2-05's members 44). The baseline is 9,942: criterion 6 is not met by this table alone and needs the reductions R2-02 tracks in phase 2's code (R2-03, R2-04, R2-06, R2-07). L3.4 stands. | R2-05, R2-02 |

## Human required

None.

## Implementation notes

| Row | Date | What | Gates |
| --- | ---- | ---- | ----- |
| fix-start | 2026-11-22 | V1-01 | not run: documents only, no code touched |
| fix-end | 2026-11-22 | V1-01 closed in this file | not run: documents only, no code touched |
| replan | 2026-11-22 | R2-05: Removes gains `Mapping.byStatus`/`Failed`/`Ambiguous`, `Mapping.RangesFor`, `better`, `RequestManifest.SourceTime`, `AudioAssembler.RunDir` and the stale comments; `Snapshot.find` stays (phase 2's `planRetry` calls it); rows cite members by name; L3.5 | scratch copy: `go build ./...` ok, `staticcheck -tests=false ./internal/audio/...` clean for the path; real tree untouched |
| fix-start | 2026-11-22 | V4-01 | not run: documents only, no code touched |
| fix-end | 2026-11-22 | V4-01 closed: the renamed `TestPlanReservesEightSlots` row and the "Request budgets and encoding" sentence are in Removes and in what criterion 5 allows; the stale doc-comment consumer is gone | not run: documents only; grep 4 run on the tree to list today's hits |

| Run   | Commit      | Command | Result |
| ----- | ----------- | ------- | ------ |
| start | 0ca840b+wip | `env -u OPENAI_API_KEY -u ANTHROPIC_API_KEY -u OPENROUTER_API_KEY make qa`; `go vet ./...`; `go run github.com/mibk/dupl@latest -t 80 .` | `make qa` exit 0, 108 packages ok (`internal/audio` 93.4 %); `go vet` clean; dupl 49 clone groups; L1.4 count 10,891 |
| end   | 0ca840b+wip | the same, `GOFLAGS=-shuffle=on` | `make qa` exit 0, 108 packages ok (`internal/audio` 95.7 %); `go vet` clean; dupl 49 clone groups |
| end   | 0ca840b+wip | the same, `GOFLAGS=-shuffle=on` | `make qa` exit 0, 108 packages ok (`internal/audio` 95.7 %); `go vet` clean; dupl 49 clone groups |

Criteria (2026-11-22, all with `/usr/bin/grep`, V5):

- 1: greps 1–3 print nothing; grep 4 prints only `TestOldConfigSettingsLoad` (`calibration_integration_test.go`) and
  the one sentence under "Stitching and failure policy" of `architecture/audio.md`.
- 2: `TestOldConfigSettingsLoad` written first and seen failing (the flag existed and the run reached `ConfigPrep`),
  then passing; `-max-speakers 3 f.wav` and `-no-such-flag 3 f.wav` both end in `flag provided but not defined`,
  status 1, before `ConfigPrep`.
- 3: `TestPlannerRejectsOversizedReserve` (80 s, the whole message) seen failing, then passing; `TestPlannerReservesPrefix`
  keeps its 88 s row; `TestPlanReservesEightSlots` passes.
- 4: `go doc ./internal/audio Speaker` shows `ID`, `Sample`, `Clips`; `go doc -u ./internal/audio Snapshot` shows
  `Speakers`, `Samples`, `find` (still called by `planRetry`).
- 5: criterion 5's `go test -run` (with `PlanReserves`) ok. Against a copy of the tree taken before the phase, phase 2's
  test files differ only in `survey_test.go`: the row renamed `the reserve is 88 s` and the three removed settings
  lines; `TestDiagnosticOutputGoldens` ok and `e2e/testdata` byte-identical to that copy.
- 6: I9 by L1.4's command: **10,253** non-test lines (baseline 9,942 at the start of phase 1; 10,891 at this phase's start;
  bound 10,300).

Andon: none.
Surprises:

- `voiceProvider`'s per-request faults (`splitVoice`, `mixSample`, `dropSample`, `mergeVoices`) were set only by
  `discovery_test.go`; by L3.3 they went with the three it names, and `voiceProvider.transcribe`, `threeVoiceMeeting`,
  `withLaterVoices` and `voiceAt` with them (staticcheck U1000 once the file was gone). Labels are unchanged.
- The reserve-error row proves "names no `max-speakers`" by comparing the whole message: a `max-speakers` literal in
  `plan_test.go` would be a grep 4 hit outside the Allowed hits.
- `Run`'s local `budgets` existed only for the pin; with the pin gone it passes `c.Budgets`.
- `staticcheck -tests=false ./internal/audio/...` names only `setupTranscribeQuerier` (`setup_transcribe.go`), test-only
  before this phase and outside the calibrated path; left.

### Validation notes (V1, 2026-11-22)

- **V1 note · grep 2.** `Discovery|[Bb]ootstrap` over non-test `internal/audio` also hits `eta.go` (`worstDiscovery`
  and the `etaModel` comment) unless phase 2's estimate rewrite (L2.8) renames them; neither phase says so.
- **V1 note · check 4.** Criterion 4's `go doc` and criterion 5's `go test -run '…'` have no Test cases row.
- **V1 note · Removes row 2.** `reasonNoRun` is declared in `discovery.go` today; "stays, in `sample.go`" means it
  moves there when `discovery.go` goes.

### Validation notes (V2, 2026-11-22)

- **V2 note.** The `Budgets.MaxSpeakers` row cites `calibration_log_test.go:97`, which phase 2 rewrites; the line
  reference will be stale (the consumer itself is gone or adapted by then). Criterion 5's `-run` regex lacks the
  `PlanReserves` its Test cases row adds; harmless.

### Validation notes (V3, 2026-11-22)

- **V3 note · Removes, request kinds.** The row lists `discovery.go` as the only consumer, but `cache_test.go:22`, `:25`
  (`TestCacheHitSkipsTranscriber`) pass `RequestForward`, so grep 1 hits it; re-point the test to `RequestLabelling`.
  Text unchanged since V1, so recorded as a note (round 3).

### Validation notes (V4, 2026-11-22; phase 3 only, after the R2-05 re-plan)

- **V4 note · `Budgets.MaxSpeakers` row.** Phase 2's fixer has already cut `Run`'s doc comment to a summary without
  clause text (`coordinator.go`, above `Run`), so "the words 'whatever Budgets.MaxSpeakers holds' in Run's doc comment"
  no longer exist; only the pin `budgets.MaxSpeakers = maxVoices` remains. Do not re-add a Run.1 sentence to the
  comment; the row's doc part should follow R2-03's outcome.
- **V4 note · `Snapshot.find`.** The row keeping `find` and the `go doc -u … Snapshot` check rest on `planRetry`
  calling `registry.find` (it does today), and `coordinator.go` is being edited; if the fix drops that call, `find`
  dies with `discovery.go` and both must follow.
- **V4 note · criterion 6.** L3.5 says this table alone leaves L1.4's count above the baseline (10,891 at this round,
  the tree still moving), so phase 3 can complete only after R2-02's reductions in phase 2 land; started earlier it
  ends in L3.4's design andon. Tracked by R2-02.
- **V4 note.** Grep 4 still filters `spike/`, which is no longer in the repository; harmless.

### Validation notes (V5, 2026-11-22; phase 3 only)

- **V5 note · the greps.** They assume GNU grep. In the agents' shell `grep` is a function for ugrep, which prints
  paths without `./`, so grep 4's `^\./(worklogs|spike)/` filter keeps every worklog hit (52 lines at this round).
  Run them with `/usr/bin/grep`, or filter `^(\./)?(worklogs|spike)/`; with GNU grep the hits map onto the Removes
  rows exactly.
- **V5 note · `Snapshot.find`.** Still called by `planRetry` (`registry.find`) at this round, so the row keeping it and
  the `go doc -u … Snapshot` check hold; they follow if phase 2's fix drops that call.

## Review findings

### Review 4, 2026-11-22

Round 4 (the first review of this node; rounds 1–3 reviewed nodes 1 and 2). **Ready**: no blocker or major.

Baseline: the copy of `internal/audio/`, `architecture/audio.md` and `e2e/testdata` the executor took at the phase
start (identical to round 3's scratch copy of the tree), rebuilt into a full scratch tree for the comparisons below.

Prior findings on this node: V1-01 and V4-01 resolved, not regressed; R2-05 holds (every member it lists is gone,
the stale comments it cites are reworded, `Snapshot.find` stays and `planRetry` calls it, `coordinator.go:490`).

Evidence:

- Gates ×2 (`GOFLAGS=-shuffle=on`, API keys unset): `make qa` exit 0, 108 packages ok, `internal/audio` 95.7 %;
  `go vet ./...` clean; dupl 49 clone groups; the gates left the tree unchanged (sha256 of every file).
- Criterion 1: the four greps with `/usr/bin/grep`: 1–3 print nothing; 4 prints only the Allowed hits,
  `architecture/audio.md:559` (under "Stitching and failure policy", `:538`) and `TestOldConfigSettingsLoad`
  (`calibration_integration_test.go:268`, `:273`, `:277`, `:282`).
- Criterion 2: `TestOldConfigSettingsLoad` passes. With a binary built from the tree and a scratch config dir,
  `clai audio transcribe -max-speakers 3 f.wav` and `-no-such-flag 3 f.wav` both end in `flag provided but not
  defined`, status 1, before any request; an `audioConfig.json` setting `max-speakers` and `strict-speakers` loads
  (the run goes on to "failed to find audio file").
- Criterion 3: the planner's error is `prefix reserve 1m28s (8 voices × 11s) reaches max-request-seconds 1m20s;
  raise the budget`, compared whole by `TestPlannerRejectsOversizedReserve`; the reserve is `maxVoices × prefixSlot`
  (`plan.go:86`), and the planner has no other caller than `Run`.
- Criterion 4: `go doc ./internal/audio Speaker` shows `ID`, `Sample`, `Clips`; `go doc -u … Snapshot` shows
  `Speakers`, `Samples`, `find`.
- Criterion 5: its `go test -run` passes; against the baseline, phase 2's test files differ only in `survey_test.go`
  (the renamed row and three setting lines); `TestDiagnosticOutputGoldens` ok and `e2e/testdata` byte-identical to the
  baseline (`git status e2e/testdata` shows only the staged change of earlier work).
- Criterion 6: I9 by L1.4's command: 10,253 (baseline 9,942, bound 10,300; 10,891 at the phase start).
- The touched tests `-race -count=20 -shuffle=on`: ok.
- Removes: every row matches the diff from the baseline, consumers re-pointed as written. Outside the table: the
  test-double removals below, a `samplesWith` test helper in `mapping_test.go`, `TestFlagRejectsNegativeBudget`'s two
  remaining cases folded into one switch (same rows, same assertions), and `Run`'s local `budgets`, which only held
  the pin.

Findings:

- **R4-01 · minor · `README.md:3`.** Clause: documentation of changed state is updated in the same phase. The
  README's Status line still reads "Phases 1–2 complete; phase 3 in progress." while board row 3 and the journal say
  Complete. Scenario: the next agent reads the header and resumes a finished phase instead of going to H2.
  - [ ] Update the Status line: phase 3 complete and reviewed, H2 next.

Notes:

- **note · test doubles (`discovery_fakes_test.go`).** No coverage was lost with them. In the baseline the faults
  `splitVoice`, `mixSample`, `dropSample`, `mergeVoices`, the three L3.3 names, `voiceProvider.transcribe`,
  `threeVoiceMeeting` and `withLaterVoices` were set or called only by `discovery_test.go` (grep over every test file).
  `voiceAt` served only `mixCandidateVoice`. The merge's tests use `mergeDiarizer` (`merge_fakes_test.go`), the survey
  path's the chunk diarizer (`survey_fakes_test.go`), each with its own faults; `voiceProvider` keeps `label`, `hold`,
  `held`, `err`, `failCore` and `delay` for the coordinator, cache and resend tests. Per-function coverage of
  `internal/audio` (`-coverprofile`, baseline against now, keyed by file and function) is identical for every
  surviving function except `Coordinator.Run`, 97.1 → 96.9 % because its two pin statements went. The uncovered
  blocks are the same, but one varies between runs in the baseline too: `coordinator.go:752` (a merge request
  failing at assembly) against the slot's error return; three baseline runs show either. L3.3 lists three faults
  "if unused"; the other four fall under its rule, "only faults that nothing uses any more go".
- **note · `merge.go:23`.** `RequestFunc` moved unchanged, as its row says, so its doc still reads "Phase 3 injects
  the accounting, cache, and provider behind it": a phase of the 2026-09-01 worklog. `Coordinator.request` is what
  does that. Reword when the file is next touched.
- **note · `survey_test.go:1408`.** `TestSurveyPathIsTheOnlyPath`'s second run now passes an empty tweak, so it
  compares runs with and without a locked voice library only; its message still says "a setting or a voice library".
  This is what the row asks (assertions unchanged); that no setting selects a path is now proven by compilation and
  grep 1.
- **note · old configs.** Both load paths decode with `json.Unmarshal`, which ignores unknown keys
  (`utils.loadConfigFromFile`; `decodeConfigWithDefaults` for read-only runs). When a file is upgraded for missing
  fields, the rewrite drops `max-speakers` and `strict-speakers` from it, as for any unknown key (seen in the
  scratch run); a complete file is not rewritten. A negative `max-speakers`, a load error before, now loads.
- **note · no outside users.** No Go code outside `components/clai` and no file of the parent repository (Minutes
  `web/`, its profiles) names `max-speakers`, `RequestLimitError`, `IsRequestLimit`, `MaxRequestsPerChunk` or the
  registry types; no golden holds the audio usage text.
- **note · `staticcheck -tests=false ./internal/audio/...`** names only `setupTranscribeQuerier`, as in the baseline.
  Every member that `discovery.go` and `registry.go` called still has a production caller or was removed by a row.
- **note · `architecture/audio.md:411`.** The reworded sentence left one 124-character line in a paragraph wrapped at
  about 78 characters (the paragraph had one long line before); cosmetic.
