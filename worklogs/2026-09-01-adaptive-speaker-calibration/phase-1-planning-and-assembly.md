# Phase 1: planning and request assembly

**Status:** Complete. See [README](./README.md).

## Goal

Produce byte-safe, seconds-safe, silence-aligned request files with exact
request-to-source interval manifests.

## Specification

All values below are README parameters referenced by name.

`ChunkPlanner` (`internal/audio/plan.go`) probes the source duration, runs one
`silencedetect` pass, and partitions the source into contiguous, disjoint core
intervals. The plan is computed once per run. Each core targets the core
target: `max-request-seconds` minus the prefix reserve minus the core margin.
The live prefix is never consulted: the reserve guarantees that any prefix the
registry can produce fits every core. Each planned boundary moves to the
nearest silence midpoint inside the silence window, else stays. The plan
records the silence candidates it chose and why.

`AudioAssembler` (`internal/audio/assemble.go`) builds each request in two
stages. Stage one decodes samples, gaps, and the core into one canonical PCM
timeline with a sample guard on each side of every sample and a sample gap
between samples; every region boundary is a sample count. Stage two encodes
the timeline once with the request codec (`-map_metadata -1 -fflags
+bitexact`). The `RequestManifest` holds ordered regions with request-time and
source-time intervals, prefix duration, core start, codec, encoded bytes,
request seconds, source hash, and request hash.

Before upload the assembler stats the encoded file and applies one bytes
check, encoded bytes plus the multipart envelope reserve must fit
`max-request-bytes`, and one seconds check, request seconds must fit
`max-request-seconds`. Artifacts live in a run-scoped temporary directory.
`CommandRunner` stays injected; unit tests use a scripted runner and never
need ffmpeg.

This phase introduces the config fields `max-request-bytes`,
`max-request-seconds`, and `max-speakers` on `TranscribeConfig`, with README
defaults and the README zero-and-negative rule, resolved by
`ResolveBudgets(TranscribeConfig) (Budgets, error)` in `querier.go`, which
the planner and assembler consume. Flags are Phase 4. Existing sub-limit and
non-diarized paths are untouched in this phase.

### Artifact cleanup invariant

| Path                       | Mechanism                                   | Test                                    |
| -------------------------- | ------------------------------------------- | --------------------------------------- |
| Success                    | Deferred removal of the run directory       | `TestAssemblerCleansUpOnSuccessAndError` |
| Assembler error (budgets)  | Same deferred removal, error returned       | `TestAssemblerRejectsOverBudgetBytes`   |
| Runner failure mid-stage   | Same deferred removal, stderr tail in error | `TestAssemblerCleansUpOnSuccessAndError` |
| Context cancellation       | Same deferred removal, `ctx.Err()` returned | `TestAssemblerCleansUpOnCancel`         |

## Integration contract

| Scenario                                  | Collaborators           | Observable result                                        | Required side effects              | Prohibited side effects            |
| ----------------------------------------- | ----------------------- | -------------------------------------------------------- | ---------------------------------- | ---------------------------------- |
| Plan 5065 s source, default reserve       | scripted ffprobe/ffmpeg | 4 cores ≈ 1266 s, boundaries on silence midpoints        | Plan lists chosen silences         | Boundary outside the silence window |
| Plan with `max-speakers` 12               | scripted runner         | Reserve 132 s; every core plus 132 s ≤ 1400 s            | Plan records the reserve           | Core exceeding seconds budget      |
| Reserve alone reaches seconds budget      | scripted runner         | Error naming reserve and budget before any core          | None                               | ffmpeg invoked                     |
| Assemble prefix + core                    | scripted runner         | One encoded file, manifest with exact intervals          | Run directory removed              | Stream-copy concatenation          |
| Encoded file over bytes budget            | scripted runner         | Error naming bytes, budget, and envelope reserve         | Artifacts removed                  | Upload attempted                   |
| Runner failure mid-assembly               | scripted runner         | Error with ffmpeg stderr tail                            | Partial artifacts removed          | Leaked temp files                  |
| Config with zero fields                   | none                    | README defaults resolved                                 | None                               | Error                              |
| Config with a negative field              | none                    | Error naming the field                                   | None                               | Planner constructed                |
| Real ffmpeg (build tag `ffmpeg`)          | ffmpeg on PATH          | Same manifest as the scripted run within one sample on a fixture of at most 5 s in `internal/audio/testdata` | None | Network; the reference recording |

## Acceptance criteria

| Criterion                                                                                          | Test                                       |
| -------------------------------------------------------------------------------------------------- | ------------------------------------------ |
| Identical source bytes and plan produce identical PCM timeline bytes and manifest                  | `TestAssemblerIsDeterministic`             |
| Every request-time boundary resolves to its source-time boundary within one PCM sample             | `TestManifestBoundariesAreSampleExact`     |
| Every core plus the full prefix reserve fits `max-request-seconds`; the plan is computed once      | `TestPlannerReservesPrefix`                |
| Both budget checks run before any upload and use the README parameters                             | `TestAssemblerRejectsOverBudgetBytes`, `TestAssemblerRejectsOverBudgetSeconds` |
| Boundaries land on detected silence when one exists inside the window                              | `TestPlannerAlignsToSilence`               |
| Boundaries never split a segment of the scripted transcript (oracle only; the planner has no transcript at runtime) | `TestPlannerAlignsToSilence` |
| Artifact cleanup holds on every row of the invariant table                                         | `TestAssemblerCleansUpOnSuccessAndError`, `TestAssemblerCleansUpOnCancel` |
| Zero config fields resolve to defaults; negative fields error naming the field                     | `TestConfigZeroMeansDefault`, `TestConfigRejectsNegativeBudgets` |
| Sub-limit and non-diarized transcription behavior is unchanged                                     | `TestSplitterPreservesExistingPaths`       |
| Real ffmpeg agrees with the scripted runner on the fixture                                         | `TestAssemblerRealFFmpeg` (build tag `ffmpeg`) |

## Error coverage

| Condition                              | Expected outcome                                     | Test                                          |
| -------------------------------------- | ---------------------------------------------------- | --------------------------------------------- |
| ffprobe fails or returns no duration   | Actionable error with stderr tail                    | `TestPlannerProbeFailure`                     |
| silencedetect finds nothing            | Planned boundaries used, plan says `no-silence`      | `TestPlannerFallsBackWithoutSilence`          |
| Reserve alone reaches seconds budget   | Error before any core is planned                     | `TestPlannerRejectsOversizedReserve`          |
| Encoded bytes over budget              | Error naming bytes, budget, reserve; no upload       | `TestAssemblerRejectsOverBudgetBytes`         |
| Request seconds over budget            | Error naming seconds and budget                      | `TestAssemblerRejectsOverBudgetSeconds`       |
| Context cancelled during assembly      | `ctx.Err()` returned, artifacts removed              | `TestAssemblerCleansUpOnCancel`               |
| Runner fails mid-assembly              | Error with stderr tail, partial artifacts removed    | `TestAssemblerCleansUpOnSuccessAndError`      |
| Source clip at start or end of file    | Guard clipped, manifest marks the clip               | `TestAssemblerClipsAtSourceBounds`            |
| Negative config field                  | Error naming the field                               | `TestConfigRejectsNegativeBudgets`            |

Declared tests live in `internal/audio/plan_test.go`,
`internal/audio/assemble_test.go`, `internal/audio/querier_test.go` (config
tests), and `TestSplitterPreservesExistingPaths` in `split_test.go`.
`TestAssemblerRealFFmpeg` lives in `assemble_ffmpeg_test.go` behind
`//go:build ffmpeg`. Run `go test ./internal/audio/... -race -count=3
-timeout=30s` before marking complete.

## Implementation notes

**Session:** Claude Fable 5.1 via `worklog-work`, 2026-09-02 (UTC, ~13:00).

### Deltas from the specification

- **Duration fields are named `Duration`, not `Seconds`.** staticcheck ST1011
  rejects a `time.Duration` with a unit suffix, so `Budgets.MaxRequestDuration`
  and `RequestManifest.Duration` carry the seconds budget and request length.
  The config key stays `max-request-seconds` (an `int` of seconds).
- **Seconds check runs before the encode.** The timeline length is known once
  the PCM stage finishes, so the seconds budget is applied there and an
  over-budget request never pays for an encode. The bytes check runs after
  the encode as specified. Both run before any upload.
- **Real-ffmpeg fixture is synthesized, not committed.** `TestAssemblerRealFFmpeg`
  (`//go:build ffmpeg`) generates a 4 s sine WAV with `ffmpeg -f lavfi` in a
  temp dir instead of adding a binary file to `internal/audio/testdata`; the
  build tag already requires ffmpeg. It checks the real manifest against the
  scripted runner's within one PCM sample and the encoded duration against
  the timeline (encoder padding measured 0–150 ms).
- **Boundary window is also budget-constrained.** Moving a boundary inside
  the silence window could push a neighbouring core past the seconds budget
  when cores are near the target, so each boundary's window is intersected
  with `[source_end − remaining_cores × max_core, previous_end + max_core]`.
  The reference plan is unaffected (cores of ~1266 s against a 1312 s cap).
- **Scripted runner emulates ffmpeg's file side effects.** `scriptedRunner`
  (`calibration_fakes_test.go`) parses `atrim=start:end` and writes exactly
  the expected sample count, writes a sized "encoded" file, and can fail or
  block on a matching argument. It is shared with later phases.
- **`probeDuration` extracted** from `Splitter` into a package function used
  by both the existing split path and the planner (dupl stays at zero).
- **Config migration side effect.** Adding non-zero defaults for the three
  fields makes existing `audioConfig.json` files gain the keys on the next
  run with the repository's standard "added new field(s)" notice. The e2e
  cascade fixture in `main_audio_e2e_test.go` now carries the full schema so
  the golden stdout stays clean; this is the repository's normal behaviour
  for new config keys, not a change to the migration rule.
- Request hash is SHA-256 over the canonical PCM timeline plus the codec
  string, so it is independent of the encoder build.

### Verification

```bash
go test ./internal/audio/ -race -count=3 -timeout=30s -cover   # ok, 83.5 %
go test -tags ffmpeg -run RealFFmpeg -race -count=1 -v ./internal/audio/   # PASS 0.40 s
go test ./... -race -cover -count=3 -timeout=30s               # ok after fixture update
go run mvdan.cc/gofumpt@latest -w -l . ; go vet ./... ; staticcheck ./... (also -tags ffmpeg)
go run github.com/mibk/dupl@latest -t 80 internal/audio        # 0 clone groups
```

Acceptance criteria and error rows all map to passing tests as declared;
the invariant table rows are covered by `TestAssemblerCleansUpOnSuccessAndError`
(success, runner failure, encode failure) and `TestAssemblerCleansUpOnCancel`.

## Review findings

None.
