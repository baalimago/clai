# Phase 2: registry, mapping, and discovery

**Status:** Complete. See [README](./README.md).

## Goal

Map each request's local labels to append-only global speakers using only
in-request sample evidence, discovering new speakers as they appear.

## Specification

All values below are README parameters referenced by name.

`SampleExtractor` (`internal/audio/sample.go`) selects, for a local label in
one chunk's segments, the highest-ranked source clip that fits the sample
length: contiguous same-label speech ranked by duration then clearance from
neighboring labels. Labels below the material label threshold are
non-material and yield no candidate; the reason is recorded.

`SpeakerRegistry` (`internal/audio/registry.go`) holds immutable global IDs,
one active sample each, a replacement count, verified source ranges, and a
verification count. It is append-only, guarded by a mutex, and exposes
`Snapshot()` for frozen reads and `Discover()` for serialized mutation.
`Snapshot()` never mutates. The registry refuses to grow past `max-speakers`.

`LabelMapper` (`internal/audio/mapping.go`) takes a manifest and the returned
segments. For each sample interval it computes label duration shares; the
sample maps when one label reaches the dominant label threshold and no other
accepted sample holds the same label. Core segments are then relabeled:
mapped local labels become global IDs, unmapped labels are listed with
source interval, duration, materiality, and reason. Every request that
carries a sample counts as one verification of it. A sample that is mixed or
missing is reported as failed; two accepted samples sharing one label are
reported as ambiguous and neither identity is merged. The mapper only
reports; the registry mutation table below is the sole place those reports
change state, and the coordinator (Phase 3) decides whether an ambiguity
report becomes a reversed-order retry or a replacement.

Discovery (`internal/audio/discovery.go`) runs the sequence in
`architecture/audio.md`: bootstrap on the first chunk (uncalibrated, then one
calibrated pass with all candidates), and for later chunks up to the
discovery iterations of lock, snapshot re-check, candidate extraction for all
unmapped material labels, and one retranscription. Candidates that collapse
onto one label keep the better clip and become one speaker. A candidate that
collapses onto an already verified speaker is that speaker. A mixed
candidate is not promoted; it is re-picked from the next-ranked clip in the
following iteration. Bootstrap allows one such iteration after the
calibrated pass; later chunks allow the discovery iterations. Every
retranscription and retry goes through Phase 3 request accounting. The
scripted transcriber in tests keys responses by manifest region layout, not
call order, and permutes labels per request.

### Registry mutation table

Every state change enters through `Discover()` while holding the lock,
re-reads the snapshot version, and increments the version once per speaker
affected by an applied row. No other mutation exists; a new path is a new
row first.

| Mutation                        | Trigger                                   | Version re-check                                                  | Clip source                     | Tests                                                                                  |
| ------------------------------- | ----------------------------------------- | ----------------------------------------------------------------- | ------------------------------- | -------------------------------------------------------------------------------------- |
| Add speaker                     | Unmapped material label with a candidate  | Dropped when the label now maps in the refreshed snapshot         | Current chunk core              | `TestDiscoveryFindsTwoNewSpeakersInOneChunk`, `TestConcurrentWorkersShareOneNewVoice` |
| Replace sample                  | Mapper reports a sample failed            | Skipped when the active sample already changed since the snapshot | Speaker's verified source ranges | `TestFailedSampleTriggersReplacementAndRetry`, `TestConcurrentSampleFailureConsumesOneReplacement` |
| Replace both ambiguous samples  | Mapper reports two samples share a label again after the recovery retry (README, recovery retry order) | As replace; identities never merged | Verified source ranges | `TestMappingRejectsSampleCollapse`                                       |
| Freeze speaker                  | Replacements per speaker reached, or no verified source range | None; last good sample kept, error if none            | None                            | `TestRegistryReplacementLimit`                                                         |
| Refuse candidate                | Registry at `max-speakers`                | None; label unmapped, reason `prefix-reserve-exhausted`           | None                            | `TestRegistryRefusesBeyondMaxSpeakers`                                                 |
| Record verification (bookkeeping) | Coordinator accepts a mapping whose sample mapped | None; count and verified ranges appended, version unchanged | Accepted core ranges     | `TestRegistryVersionIncrementsPerMutation`                                             |

## Integration contract

| Scenario                                    | Collaborators                      | Observable result                            | Required side effects                 | Prohibited side effects                  |
| ------------------------------------------- | ---------------------------------- | -------------------------------------------- | ------------------------------------- | ---------------------------------------- |
| Bootstrap, three material labels, permuted  | scripted transcriber, assembler    | Three global IDs; two requests               | Version advanced by three             | Third request                            |
| Later chunk, all labels known               | snapshot                           | Segments relabeled to global IDs; one request| No registry mutation                  | Discovery lock taken                     |
| Later chunk, two new speakers               | lock, scripted transcriber         | Both promoted after one retranscription      | Version advanced by two               | Sequential one-per-iteration discovery   |
| Spurious label collapsing onto known voice  | scripted transcriber               | Mapped to existing ID; no new speaker        | Rejection reason recorded             | Seventh identity                         |
| Sample fails in a later request             | scripted transcriber               | Replacement from verified ranges, chunk retried | Replacement count advanced by one  | Earlier accepted chunks touched          |
| Two workers see the same new voice          | lock                               | Second worker maps via refreshed snapshot    | One new ID                            | Duplicate IDs for one voice              |
| Two workers see the same sample fail        | lock                               | One replacement applied, second skipped      | Replacement count advanced by one     | Second replacement consumed              |
| Ninth material voice with `max-speakers` 8  | lock                               | Label unmapped, reason `prefix-reserve-exhausted` | Registry unchanged               | Ninth ID; prefix over reserve            |

## Acceptance criteria

| Criterion                                                                                     | Test                                            |
| --------------------------------------------------------------------------------------------- | ----------------------------------------------- |
| A scripted transcriber may permute labels per request while global IDs stay stable            | `TestBootstrapPermutedLabels`                   |
| Bootstrap and later discovery both promote several speakers from one retranscription          | `TestBootstrapPermutedLabels`, `TestDiscoveryFindsTwoNewSpeakersInOneChunk` |
| No identity is created from non-material, mixed, or collapsed evidence                        | `TestExtractorSkipsNonMaterialLabels`, `TestMappingRejectsMixedSample`, `TestSpuriousLabelCollapsesOntoKnownVoice` |
| Every applied mutation row increments the version once per affected speaker and is invisible to earlier snapshots | `TestRegistryVersionIncrementsPerMutation`, `TestRegistrySnapshotIsImmutable` |
| Unmapped labels are returned with source interval, duration, materiality, and reason          | `TestMappingReportsUnmappedLabels`              |
| Every row of the mutation table is enforced under the lock with its re-check                  | Tests listed per row                            |
| Discovery stops at the discovery iterations and reports remaining labels with attempts        | `TestDiscoveryStopsAfterTwoIterations`          |
| Concurrent workers seeing one new voice produce one ID; seeing one failed sample consume one replacement | `TestConcurrentWorkersShareOneNewVoice`, `TestConcurrentSampleFailureConsumesOneReplacement` |

## Error coverage

| Condition                                       | Expected outcome                                          | Test                                            |
| ----------------------------------------------- | --------------------------------------------------------- | ----------------------------------------------- |
| Sample interval below the dominant threshold    | Sample reported failed; mapping reports it; no promotion  | `TestMappingRejectsMixedSample`                 |
| Two accepted samples share a label              | Both reported ambiguous; identities not merged; replaced only after the recovery retry | `TestMappingRejectsSampleCollapse` |
| Sample interval has no segments                 | Sample reported missing; replacement row applied          | `TestMappingRejectsMissingSample`               |
| Candidate below the material label threshold    | No candidate; reason `non-material`                       | `TestExtractorSkipsNonMaterialLabels`           |
| Replacements per speaker reached                | Speaker frozen with last good sample; error if none       | `TestRegistryReplacementLimit`                  |
| Registry at `max-speakers`                      | Candidate refused; reason `prefix-reserve-exhausted`      | `TestRegistryRefusesBeyondMaxSpeakers`          |
| Discovery iterations exhausted                  | Unmapped labels returned with attempts                    | `TestDiscoveryStopsAfterTwoIterations`          |
| Snapshot read during mutation                   | Old snapshot unchanged                                    | `TestRegistrySnapshotIsImmutable`               |
| Speaker has no verified range for a replacement | Freeze row applied; reason recorded                       | `TestRegistryReplacementLimit`                  |

Declared tests live in `internal/audio/sample_test.go`, `registry_test.go`,
`mapping_test.go`, and `discovery_test.go`. Run `go test ./internal/audio/...
-race -count=3 -timeout=30s` before marking complete.

## Implementation notes

**Session:** Claude Fable 5.1 via `worklog-work`, 2026-09-02 (UTC, ~14:00).

### Deltas from the specification

- **New mutation-table row: record verification.** Verified counts and
  verified source ranges are state the replacement row depends on, so they
  needed an entry point. `RecordVerification` runs under the registry mutex,
  never under the discovery lock, and does not advance the version (it
  changes no identity or sample). Added as the last table row; the
  coordinator calls it through `Discovery.RecordAccepted` when a chunk's
  mapping is accepted. Promotion counts as the first verification.
- **Candidate collapse is a mapper status, not ambiguity.** `SampleClip`
  and `Region` carry a `Candidate` flag. When several accepted samples share
  a label the mapper distinguishes: two or more registry samples →
  `ambiguous` for all (never merged); one registry sample plus candidates →
  candidates `collapsed` onto that speaker; candidates only → `collapsed`
  and discovery keeps the better clip. Without the flag the mapper could
  not tell a same-voice collapse from an ambiguity.
- **Discovery requests run under the discovery lock**, including the
  transcription itself, so a second worker's iteration always carries the
  first worker's newly added sample and its candidate collapses onto it
  (`TestConcurrentWorkersShareOneNewVoice`). The Phase 3 coordinator keeps
  other chunks running on their frozen snapshots meanwhile.
- **`Recover` is Phase 2's replacement path**: replacement rows under the
  lock (stale versions skipped), then one retry request against the
  refreshed snapshot outside the lock. Whether the retry is allowed (one
  recovery slot per chunk) is the coordinator's decision.
- **Extractor details.** A single utterance longer than the sample length
  upper bound is cut to the bound rather than skipped. Same-label runs break
  on a pause longer than one second, so disjoint verified ranges yield
  separate replacement candidates. Re-picks after a mixed candidate skip
  clips overlapping any clip already tried in this chunk.
- **Global IDs** are `speaker-N` in promotion order; candidate IDs are
  `cand-<i>-<label>` and never survive a mapping (provisional relabels are
  undone when promotion is refused).
- **Scripted diarizer** (`voiceProvider` in `discovery_fakes_test.go`) labels
  regions from a ground-truth timeline with letters permuted per request and
  injects the faults Phase 0 measured: split voice, mixed sample, dropped
  sample, merged voices. It builds manifests by arithmetic (`manifestFor`)
  so these tests need neither ffmpeg nor the scripted runner.

### Verification

```bash
go test ./internal/audio/ -race -count=3 -timeout=30s -cover   # ok, 83.7 %
go run mvdan.cc/gofumpt@latest -w -l internal/audio ; go vet ; staticcheck ; dupl -t 80
go test ./... -race -cover -count=3 -timeout=30s
```

All declared tests exist and pass: extractor (`sample_test.go`, plus
`TestExtractorRanksByDurationThenClearance`), registry (`registry_test.go`,
plus `TestRegistryDiscoverHonoursContext`), mapper (`mapping_test.go`),
discovery (`discovery_test.go`).

## Review findings

None.
