# Phase 3: coordinator, cache, and stitching

**Status:** Complete. See [README](./README.md).

## Goal

Drive planning, assembly, mapping, and discovery into one source-ordered
transcript with bounded cost, best-effort and strict policies, and clean
cancellation.

## Specification

All values below are README parameters referenced by name.

`Coordinator` (`internal/audio/coordinator.go`) runs the first chunk
sequentially through bootstrap, then schedules remaining chunks with
`parallelism` workers against a registry snapshot. A chunk whose mapping
leaves unmapped material labels or a failed sample enters `Discover()`; all
other chunks continue. Accepted results are final and are never
retranscribed because the registry grew.

This phase introduces the config field `strict-speakers` on
`TranscribeConfig`, README default; the flag is Phase 4.

A run-scoped in-memory cache keyed by request hash, model, endpoint, and
request options returns raw segments for identical requests without calling
the transcriber. It is a map lookup; hits are reported, not counted as
requests. Stitching removes prefix regions, relabels the core, restores
source time from the manifest's core start, and appends in chunk order.

Policy: in best-effort mode unmapped speech keeps its text with label
`unknown-N`. N is assigned at stitch time, one number per contiguous run of
the same unmapped local label, counted in source order across the whole run.
The coordinator writes a stderr summary of unknown duration and reasons plus
request, retry, cache-hit, and uploaded-seconds totals. In strict mode an
unresolved material label is an error carrying the same diagnostics.

Cancellation stops scheduling, cancels in-flight requests, removes artifacts,
and returns the context error.

### Request accounting table

| Request kind                  | Counts | Test                                  |
| ----------------------------- | ------ | ------------------------------------- |
| Bootstrap uncalibrated pass   | yes    | `TestRequestAccountingCountsRetries`  |
| Bootstrap calibrated pass     | yes    | `TestRequestAccountingCountsRetries`  |
| Discovery iteration           | yes    | `TestRequestAccountingCountsRetries`  |
| Recovery retry (at most one per chunk): reversed prefix order on ambiguity, or sample replacement on a failed sample | yes | `TestRequestAccountingCountsRetries`, `TestAmbiguityRetriesWithReversedPrefix`, `TestSecondSampleFailureDoesNotRetry` |
| Cache hit                     | no; reported as a hit | `TestCacheHitSkipsTranscriber` |

A chunk has one recovery retry. An ambiguity report (two accepted samples on
one label) spends it on the same request with the prefix in the recovery
retry order and no registry change; a failed sample spends it on a
replacement followed by a plain retry. Discovery runs only after a chunk
shows no failed registry sample (D23): a failed speaker's speech is
unanchored and must never seed a candidate. An ambiguity that repeats on
the retry applies the Phase 2 replacement row for the next chunk that
carries those samples; a sample that fails on or after the retry is
likewise reported for replacement by the next carrier. In both cases the
current chunk's mapping proceeds with the affected speakers unmapped:
`unknown` in best-effort, an error in strict.

### Limits table

| Limit               | `Coordinator` field   | README parameter         | Test trigger                          | Test                     |
| ------------------- | --------------------- | ------------------------ | ------------------------------------- | ------------------------ |
| Requests per chunk  | `MaxRequestsPerChunk` | `max-requests-per-chunk` | Inject one; the second request errors | `TestBudgetStopsPerChunk` |

The longest production paths are: bootstrap, one uncalibrated pass, one
calibrated pass, one discovery iteration for candidates that were mixed,
and one replacement retry; later chunks, one pass plus the
discovery iterations plus one replacement retry. Both equal the default,
so the limit is a safety stop production never reaches and tests reach by
injection. Exhaustion is a structured error under both
policies naming chunk, source interval, unresolved labels, attempts, and
consumed requests. There is no run limit and no cost limit: uploaded audio
is bounded by the limit times the chunk count times `max-request-seconds`.

Timestamps: stitching restores source time by manifest arithmetic and is
exact to one PCM sample. The codec timestamp correction is the README
parameter (none, D22); the coordinator applies no offset.

## Integration contract

| Scenario                                  | Collaborators                              | Observable result                                    | Required side effects                       | Prohibited side effects             |
| ----------------------------------------- | ------------------------------------------ | ---------------------------------------------------- | ------------------------------------------- | ----------------------------------- |
| Four chunks, six permuted speakers        | scripted transcriber, planner, assembler   | Source-ordered segments with six global IDs          | Status lines and totals on stderr           | Prefix text in output               |
| Late speaker in chunk 4                   | lock                                       | Discovered; chunks 2–3 results untouched             | One extra request                           | Retranscription of accepted chunks  |
| Identical retry                           | cache                                      | Transcriber called once                              | Cache-hit counter advanced by one           | Second network call                 |
| Unmapped non-material speech, best-effort | mapper                                     | Segments rendered as `unknown-1`, text preserved     | Stderr summary with duration and reason     | Dropped segments; stdout warnings   |
| Unmapped material label, strict           | mapper                                     | Error with chunk, interval, attempts                 | Artifacts removed                           | Transcript on stdout                |
| `MaxRequestsPerChunk` injected as 1       | limits                                     | Second request refused with diagnostics              | Attempts recorded per chunk                 | Silent truncation                   |
| Cancellation mid-run                      | context                                    | `ctx.Err()`; queued chunks never start               | Artifacts removed                           | Leaked goroutines or temp files     |
| Two samples share a label in a chunk      | scripted transcriber keyed by prefix order | Retry with reversed prefix; both map; no mutation    | Retry counted; registry version unchanged   | Sample replacement; merged identity |

## Acceptance criteria

| Criterion                                                                                                  | Test                                                    |
| ---------------------------------------------------------------------------------------------------------- | ------------------------------------------------------- |
| Output is source ordered and contains no prefix speech                                                     | `TestStitchRemovesPrefix`                               |
| Source timestamps are restored exactly by manifest arithmetic                                              | `TestStitchRestoresSourceTimestamps`                    |
| Parallel chunks respect the worker count while discovery holds the lock                                    | `TestFrozenSnapshotAllowsBoundedParallelism`            |
| A late speaker never invalidates accepted chunks                                                           | `TestLateSpeakerDoesNotInvalidateChunks`                |
| Cached requests do not call the transcriber; the key covers hash, model, endpoint, options                 | `TestCacheHitSkipsTranscriber`, `TestCacheKeyCoversRequestIdentity` |
| Best-effort output never drops speech and numbers `unknown-N` in source order at stitch time              | `TestBestEffortRendersUnknown`                          |
| Strict mode never writes a partial transcript                                                              | `TestStrictFailsWithDiagnostics`                        |
| Every row of the accounting table counts as stated; the limits table row stops work with a diagnostic      | `TestRequestAccountingCountsRetries`, `TestBudgetStopsPerChunk` |
| Ambiguity is retried once with the recovery retry order and no registry mutation                            | `TestAmbiguityRetriesWithReversedPrefix`                |
| Stderr totals report requests, retries, cache hits, uploaded seconds, and unknown duration                 | `TestCoordinatorReportsTotalsOnStderr`                  |
| Cancellation leaves no goroutines, artifacts, or in-flight requests                                        | `TestCoordinatorCancelsAndCleansUp`                     |
| `strict-speakers` zero value is best-effort                                                                | `TestStrictSpeakersDefaultsToBestEffort`                |

## Error coverage

| Condition                           | Expected outcome                                           | Test                                       |
| ----------------------------------- | ---------------------------------------------------------- | ------------------------------------------ |
| Transcriber HTTP error              | Error wrapped with chunk and request context               | `TestCoordinatorPropagatesTranscriberError`|
| Per-chunk request limit             | Error naming chunk, limit, and consumption                 | `TestBudgetStopsPerChunk`                  |
| Limit exhausted in best-effort      | Error, not `unknown` rendering                             | `TestBudgetErrorsUnderBestEffort`          |
| Unresolved material label, strict   | Error with diagnostics; no stdout                          | `TestStrictFailsWithDiagnostics`           |
| Unresolved label, best-effort       | `unknown-N` segments; stderr summary                       | `TestBestEffortRendersUnknown`             |
| Second sample failure in one chunk  | No retry; speaker unmapped in this chunk; replacement deferred to the next carrier | `TestSecondSampleFailureDoesNotRetry` |
| Ambiguity repeats on the retry      | No third request; both speakers unmapped in this chunk; replacement deferred to the next carrier | `TestAmbiguityRetriesWithReversedPrefix` |
| Cancellation                        | Context error; cleanup verified                            | `TestCoordinatorCancelsAndCleansUp`        |

Declared tests live in `internal/audio/coordinator_test.go`,
`cache_test.go`, and `stitch_test.go`. Run `go test ./internal/audio/...
-race -count=3 -timeout=30s` before marking complete.

## Implementation notes

**Session:** Claude Fable 5.1 via `worklog-work`, 2026-09-02 (UTC, ~15:00).

### Deltas from the specification

- **Recovery and discovery no longer travel in one request (D23).** The
  scripted diarizer reproduced a cascade: when a failed sample's replacement
  was verified inside the same request that carried discovery candidates,
  and the replacement failed too, the failed speaker's own speech was an
  unmapped material label, its candidate was promoted as a new identity,
  and every later chunk then saw two samples of one voice collapse
  (ambiguity retries, unknown runs). The coordinator now spends the recovery
  slot on a plain retry (reversed order for ambiguity, `Discovery.Recover`
  for a failed sample) and runs discovery only when the retry shows no
  failed registry sample. A chunk whose sample still fails after the retry
  is accepted with that speaker unmapped and no discovery, so the failed
  speaker's speech can never seed a duplicate identity. The longest path is
  unchanged: pass, recovery retry, two discovery iterations, four requests.
- **Chunks are scheduled in source order** through an ordered queue that
  the workers drain, so with `parallelism` one the request sequence is
  deterministic and progress lines arrive in order. Parallelism is still
  bounded by the worker count (`TestFrozenSnapshotAllowsBoundedParallelism`
  measures the peak).
- **Discovery after a repeated ambiguity still runs.** Both known speakers
  stay unmapped in that chunk, but genuinely new voices in the chunk are
  still discovered; a candidate cut from the merged label collapses onto the
  known sample in a clean request or is rejected as `ambiguous-known-samples`
  when the merge persists, so identities never merge
  (`TestAmbiguityRetriesWithReversedPrefix`, second case).
- **Strict diagnostics include candidate rejections** (label, clip start,
  reason) in addition to chunk, interval, unresolved labels, attempts, and
  requests, so a strict failure explains why discovery could not anchor the
  speech.
- **Assembler and transcriber seams.** `RequestAssembler` and
  `RequestTranscriber` are the coordinator's two injected boundaries;
  `FileTranscriber` adapts the existing vendor `Transcriber`, and Phase 4
  wires the real assembler through `Coordinator.NewAssembler` (nil means
  real). Coordinator tests use an arithmetic assembler and the Phase 2
  scripted diarizer, which keeps the whole package under fifteen seconds
  at `-race -count=3`.
- **Cache hits count against the per-chunk limit** (they are attempts of
  that chunk) but not as requests or uploaded seconds; totals report them.
- Totals field is `Uploaded` (a `time.Duration`; staticcheck ST1011).

### Post-completion delta (2026-09-02, after the real run)

Progress display: the coordinator and the plain split path report through
a chunk board (`board.go`) in the rolling-view style of the text querier's
activity viewport: a `▸` header in the theme's primary colour, a two-space
body with one row per core (span, state, requests used against the chunk
limit, samples verified, unresolved material labels, unknown speech,
elapsed), ✓/✗ markers, and a totals footer. On a terminal
(`utils.IsTerminalWriter`) the board redraws in place at 200 ms with a
spinner on active rows; otherwise each row change prints as a static line
and no cursor control is emitted. Warning rows use the tool role colour. A phase line under the header
(bootstrap, calibrating, stitching, done, or failed) is advanced by the
coordinator, and the footer is a live function (requests against the
ceiling, rows in flight, uploaded audio, registry size, unknown speech so
far, elapsed) evaluated at every redraw, so counters and elapsed times tick
with the spinner. The footer leads with a remaining-time estimate
(`eta.go`): the provider rate is measured as wall seconds per uploaded
audio second over completed requests (Phase 0's ~0.3 before the first
completes); the typical figure assumes one more request per unfinished
chunk after its pass, the worst case the full per-chunk limit; passes are
divided by the worker count, discovery is serialized. The footer function
runs under the board lock with a row snapshot and must not call back into
the board; `TestCoordinatorLiveBoardRendersEstimate` drives the live path
against a buffer to catch that deadlock. The unknown-speech report after
the board lists only runs of at least three seconds (source order, span,
speech, label, reason) and counts the shorter fragments in one line; every
run is still its own `unknown-N` label in the transcript, so nothing is
dropped from the data (README: every unknown interval reported). The
request purpose (uncalibrated pass, calibrated pass, discovery with
candidate count, recovery retry) is derived from chunk state. An earlier
attempt that used ancli's timestamped `status: message` lines was rejected
as debug-like and removed. Covered by `TestBoardPlainModePrintsRowsOnChange`,
`TestBoardLiveModeRedrawsInPlace`, `TestBoardColorsWarningRowsOnly`,
`TestCoordinatorReportsTotalsOnStderr`, `TestSecondSampleFailureDoesNotRetry`.

### Verification

```bash
go test ./internal/audio/ -race -count=3 -timeout=30s -cover   # ok, 86.0 %
go run mvdan.cc/gofumpt@latest -w -l internal/audio ; go vet ; staticcheck ; dupl -t 80 (0 clones)
go test ./... -race -cover -count=3 -timeout=30s
```

Declared tests live in `coordinator_test.go` (all `TestCoordinator*`,
`TestLateSpeakerDoesNotInvalidateChunks`, `TestStitchRemovesPrefix`,
`TestStitchRestoresSourceTimestamps`, `TestFrozenSnapshotAllowsBoundedParallelism`,
`TestBestEffortRendersUnknown`, `TestStrictFailsWithDiagnostics`,
`TestStrictSpeakersDefaultsToBestEffort`, `TestRequestAccountingCountsRetries`,
`TestSecondSampleFailureDoesNotRetry`, `TestAmbiguityRetriesWithReversedPrefix`,
`TestBudgetStopsPerChunk`, `TestBudgetErrorsUnderBestEffort`), `cache_test.go`,
and `stitch_test.go` (plus `TestStitchNumbersUnknownRunsInSourceOrder`).

## Review findings

None.
