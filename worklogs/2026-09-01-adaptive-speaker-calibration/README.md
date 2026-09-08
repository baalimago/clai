# Worklog: adaptive speaker calibration

Effort designed: 2026-09-01. Strategy revised: 2026-09-02 (V4, after Phase 0). Repository: `clai`.
Design reference: [`architecture/audio.md`](../../architecture/audio.md),
section "Adaptive Speaker Calibration (Planned)". That section is part of this
effort's diff and must be committed together with this worklog.

## Goal

Reconcile request-local diarization labels across oversized recordings by
prepending verified speaker samples to each ordinary diarization request. The
algorithm is vendor-agnostic: it needs only labeled diarization with
timestamps and does not use provider-native known-speaker fields.

## Status board

| Phase | File                                                                             | Status      | Outcome                                                          |
| ----- | -------------------------------------------------------------------------------- | ----------- | ---------------------------------------------------------------- |
| 0     | [phase-0-provider-spike.md](./phase-0-provider-spike.md)                         | Complete    | `proceed` under V4 (D19–D22): anchoring measured, gates re-based on the provider floor |
| 1     | [phase-1-planning-and-assembly.md](./phase-1-planning-and-assembly.md)           | Complete    | Byte and seconds budgets, silence-aligned cuts, encoded requests |
| 2     | [phase-2-registry-and-discovery.md](./phase-2-registry-and-discovery.md)         | Complete    | Candidate samples, mapping, discovery, continuous verification   |
| 3     | [phase-3-coordinator-and-stitching.md](./phase-3-coordinator-and-stitching.md)   | Complete    | Parallel coordinator, cache, stitching, best-effort and strict   |
| 4     | [phase-4-integration-and-acceptance.md](./phase-4-integration-and-acceptance.md) | In Progress | Wired, gates green, real run done; annotated acceptance is human required |

Router: complete phases in order. Phase 0 is a gate: Phases 1–4 start only
when its decision is `proceed`. A `redesign` decision returns to this README.
Phases 0 and 4 contain `Human required` steps; an agent stops at the point
those subsections name and records the handoff in the session journal.

## Strategy

### Provider facts (evidence, not assumptions)

The reference recording's original 21-label transcript, bucketed into its
eight 633 s chunks, shows the provider producing 4–20 labels per request for
six people (table below). Two consequences shape every phase: spurious labels
are normal input and must never become identities, and any policy that errors
on an unresolved label will fail on real recordings.

Phase 0 (2026-09-02, ten paid requests, evidence in the phase file) measured
the provider directly:

- A 1400 s request succeeds. The cap lies above `max-request-seconds` and
  below the 5065 s whole file that returned HTTP 400.
- Anchoring works: with one clean clip per voice, every sample was dominant,
  all five samples got distinct labels, and 99.8 % of core speech mapped, in
  two of two forward-order requests.
- The provider is not self-consistent without a prefix: two independent runs
  of the same chunk agree on only 67–78 % of speech (majority mapping).
  Anchored repeats agree on 93.8 %. That floor, not the anchoring, bounds
  every absolute agreement gate.
- Transcript labels over-split the loudest voice: the main speaker held three
  of the six largest chunk-local labels in the first two chunks. Identity
  comes only from clips collapsing in a request, never from label statistics.
- Two distinct samples can merge onto one label depending on prefix order
  (one of three orderings). The same clips in the other order were distinct.
- A speaker's core speech can be split away from that speaker's own clip
  (two of five requests). Discovery on the unanchored label collapses its
  candidate onto the known speaker and recovers it.
- The MP3 re-encode shifts timestamps by +0.06 to +0.08 s median, below
  the timestamp tolerance; per-segment jitter of ±0.2–0.5 s is the
  provider's own.

Provider-native `known_speaker_references` are capped at four clips of 2–10 s
and, in the spike, misassigned a whole speaker. They are not used by the
pipeline.

### Reference recording

The acceptance case is a local meeting recording that never enters the
repository. It is identified here by content, not path, so phases can refer
to it after `architecture/audio.md` drops its temporary section.

| Property         | Value                                                              |
| ---------------- | ------------------------------------------------------------------ |
| SHA-256          | `5c28ff2c021f526644b8995f533f39476305baed7e8863af8f53be9f5b89e635` |
| Format           | WAV, pcm_s16le, 16 kHz, mono                                       |
| Duration         | 5065 s (84 min), 162 MB                                            |
| Participants     | six                                                                |
| Original split   | eight chunks of 633 s; chunk k spans (k−1)×633 s to k×633 s         |
| Original result  | 21 request-local labels, unreconciled                              |

Labels per original chunk, with the count having at least 2 s of speech:

| Chunk  |  1 |  2 |  3 |  4 |  5 |  6 |  7 |  8 |
| ------ | -: | -: | -: | -: | -: | -: | -: | -: |
| Labels |  4 |  9 |  8 | 13 |  7 |  6 | 20 | 12 |
| ≥ 2 s  |  3 |  6 |  6 |  8 |  5 |  6 |  6 |  7 |

Phase 0 established that chunk 2 holds four voices with material speech and
one small fifth voice, and that chunks 1–2 together hold at most five of the
six participants. The sixth is expected in later chunks.

### Non-negotiable invariants

Labels are request-local and are never compared across requests. Identity
comes from acoustic evidence inside one assembled request, never from text
similarity, temporal proximity, or a forced speaker count.

The calibration prefix is never rendered. Every request has a manifest mapping
request-time intervals to source-time intervals, derived from the canonical
PCM sample count. Every request is checked against both budgets before upload.

The plan is computed once. Cores are sized against a fixed prefix reserve, not
the live prefix, so a speaker discovered late never pushes a later core over
the seconds budget and nothing is ever replanned.

Two accepted samples sharing one label in a request is ambiguity, not
evidence of one voice. The chunk is retried once with the reversed prefix
order and no registry change; only a repeated ambiguity replaces samples.
The retry shares the per-chunk recovery slot with sample replacement.
Discovery runs only when no registry sample is failed in the chunk's
latest request (D23).

The registry is append-only and holds at most `max-speakers` identities.
Every registry mutation goes through one serialized entry point under the
discovery lock with a snapshot version re-check; Phase 2 lists the mutations
as a table and no other mutation exists. Adding a speaker never invalidates
an accepted chunk. All non-mutating work runs against a frozen snapshot.
Accepted results are final.

Best-effort is the default: unmapped speech renders as `unknown-N` and is
never dropped. `strict-speakers` turns unresolved material labels into an
error with no stdout transcript. Request-limit exhaustion is an error under
both policies; production paths are bounded so that the limit is a safety
stop, and tests reach it by injecting a lower value.

All external binaries and transcription calls are injected and mockable.
Vendor packages own protocol details; `internal/audio/` owns orchestration.
No new third-party dependency.

Config fields follow the repository convention: a zero value means "use the
default" (config migration fills missing keys from non-zero defaults, see
`architecture/config.md`), a negative value is a load-time error naming the
field. The same guard already exists for `parallelism` in
`internal/audio/create_querier.go`.

### Parameters and owners

This table is the only place a default, limit, threshold, or tunable is
written. Phase files refer to rows by name and never restate a value; the
only numerals in a phase file are oracle data inside scenario rows. The
Owner column names the single phase that introduces each item. Flags for
config fields are all introduced by Phase 4.

| Parameter                    | Default                                          | Owner                       |
| ---------------------------- | ------------------------------------------------ | --------------------------- |
| `max-request-bytes`          | 25 MiB (`25 << 20`, the current constant)        | Phase 1 (config field)      |
| `max-request-seconds`        | 1400 s                                           | Phase 1 (config field)      |
| `max-speakers`               | 8                                                | Phase 1 (config field)      |
| `strict-speakers`            | false                                            | Phase 3 (config field)      |
| `parallelism`                | 3 (existing config field)                        | existing                    |
| multipart envelope reserve   | 16 KiB                                           | Phase 1                     |
| prefix reserve               | `max-speakers` × 11 s = 88 s                     | Phase 1                     |
| core margin                  | 2 %                                              | Phase 1                     |
| core target                  | (1400 − 88) × 0.98 ≈ 1286 s                      | Phase 1 (derived)           |
| request codec                | MP3 CBR 64 kbps mono 16 kHz                      | Phase 1                     |
| canonical PCM                | s16le mono 16 kHz                                | Phase 1                     |
| silence detection            | −30 dB, ≥ 400 ms                                 | Phase 1                     |
| silence window               | ±10 % of core target                             | Phase 1                     |
| sample length                | 3–10 s                                           | Phase 2                     |
| sample guard                 | 250 ms each side                                 | Phase 2                     |
| sample gap                   | 500 ms                                           | Phase 2                     |
| material label               | ≥ 3 s speech and ≥ 1 % of chunk speech           | Phase 2                     |
| dominant label threshold     | ≥ 80 % of sample speech                          | Phase 2                     |
| replacements per speaker     | 2                                                | Phase 2                     |
| discovery iterations         | 2 per chunk                                      | Phase 2                     |
| `max-requests-per-chunk`     | 4 (`Coordinator` field, not user config)         | Phase 3                     |
| recovery retry order         | reversed prefix order, once per chunk            | Phase 3                     |
| timestamp tolerance          | 100 ms end to end                                | Phase 3 (measured Phase 0)  |
| spike core coverage          | ≥ 90 % of core speech mapped, forward order      | Phase 0                     |
| spike mapping agreement      | ≥ prefix-free repeat agreement (measured 67–78 %) | Phase 0                    |
| codec timestamp correction   | none (measured below tolerance)                  | Phase 0 (D22)               |
| non-diarized chunk ratio     | 80 % of `max-request-bytes`                      | Phase 4                     |
| attribution gate             | ≥ 95 % of annotated non-crosstalk speech         | Phase 4                     |
| unknown gate                 | < 2 % of annotated speech                        | Phase 4                     |

The prefix reserve is one worst-case slot per speaker: the sample length
upper bound plus two sample guards plus one sample gap. With the defaults the
reference recording plans as four cores of about 1266 s; each core plus the
full reserve stays under `max-request-seconds`. The worst-case pipeline
request is core target plus prefix reserve, 1374 s.

`max-requests-per-chunk` equals the longest production path (Phase 3 lists
the paths), so a run limit adds nothing and there is none. The recovery
retry (reversed order on ambiguity, or a replacement on a failed sample) is
one slot per chunk, so D19 adds no path length. Uploaded audio is
bounded by `max-requests-per-chunk` × cores × `max-request-seconds`.

Phase 0 may revise the sample length, silence, and dominance values from
measured data; record any change here and in the decisions log.

### Shared interfaces

Phase 1 defines `RequestManifest` (ordered regions with request-time and
source-time intervals, prefix duration, core start, codec, bytes, seconds,
request hash). Phase 2 consumes manifests and produces `LabelMapping`
(local label → global ID, plus unresolved labels with reasons). Phase 3 owns
the coordinator that drives both and emits `[]Segment`. Phase 4 wires the
coordinator into `Splitter`, flags, and the tool bridge.

### Severity taxonomy

`blocker`: an invariant above is violated or an acceptance criterion is
unmet. `major`: a specified behavior or error row lacks a passing test.
`minor`: naming, comments, or non-behavioral cleanup. Blocker and major
findings reopen a phase; minor findings do not.

## Readiness checklist

The author runs every line before requesting validation and records the
outcome in the session journal. Validators check this list first; findings
outside it are notes unless an invariant is broken. Run from this directory.

1. No numerals in phase files outside oracle rows. Expected output: only
   rows inside an `Integration contract` table. Values after `=` are
   command arguments, not parameters, and are excluded.

   ```bash
   grep -nE '(^|[^=])\b[0-9]+([.,][0-9]+)? ?(s|ms|MB|MiB|KiB|kbps|kHz|dB|%)\b' phase-*.md
   ```

2. Every test name is declared in exactly one phase. Expected output: none.

   ```bash
   for t in $(grep -ohE '\bTest[A-Za-z0-9]+' phase-*.md | sort -u); do
     n=$(grep -lE "\b$t\b" phase-*.md | wc -l); [ "$n" = 1 ] || echo "$t in $n phases"
   done
   ```

3. Every config field, flag, and injectable field has one owner in the
   parameters table, and no phase other than the owner introduces it.
   Check by reading the table against each phase's Specification.

4. Every invariant and limit is a table with a test per row: Phase 1
   artifact cleanup, Phase 2 registry mutations, Phase 3 request accounting
   and limits. A prose "always" or "never" without a row is a defect.

5. Every phase mentioning listening, manual, or paid has a `Human required`
   subsection. Expected: both commands list the same files.

   ```bash
   grep -liE 'listen|manual|paid' phase-*.md; grep -l 'Human required' phase-*.md
   ```

6. No phase references text scheduled for deletion. Expected output: only
   the Phase 4 removal instruction.

   ```bash
   grep -n 'Temporary Investigation' phase-*.md
   ```

7. New conventions do not contradict existing code. Checked against
   `internal/audio/create_querier.go` (zero means default) and
   `architecture/config.md` (migration fills non-zero defaults).

## Decisions log

| ID  | Date       | Decision                                                                                   | Rationale                                                                                                       | Replaces                                          |
| --- | ---------- | ------------------------------------------------------------------------------------------ | --------------------------------------------------------------------------------------------------------------- | ------------------------------------------------- |
| D1  | 2026-09-01 | In-request acoustic anchoring with exact source clips, vendor-neutral                       | Labels are request-local; native references cap at four clips                                                   | Temporal-neighbor merging, forced six-label pass  |
| D2  | 2026-09-02 | Phase 0 spike is a gate                                                                    | Anchoring behavior is unverified; one paid measurement is cheaper than a wrong build                            | Assumed provider capability                       |
| D3  | 2026-09-02 | Seconds budget plus deterministic MP3 re-encode                                            | The 1400–1500 s cap caused the HTTP 400; PCM would force eight requests, MP3 needs four                          | PCM mandate, eight 633 s chunks                   |
| D4  | 2026-09-02 | Silence-aligned cut points                                                                 | Fixed-time cuts split utterances at every boundary                                                              | Fixed-time cuts                                   |
| D5  | 2026-09-02 | Continuous verification                                                                    | Every request carrying a sample already verifies it; dedicated requests wasted budget                            | Dedicated verification requests                   |
| D6  | 2026-09-02 | All-at-once discovery, at most two iterations per chunk                                    | 4–20 labels per request; one candidate per iteration could not converge inside the budget                        | Three requests per candidate, sequential          |
| D7  | 2026-09-02 | Append-only registry, accepted results final                                               | Retranscribing accepted chunks after each discovery cascades cost with no identity gain                          | Stale-result retranscription                      |
| D8  | 2026-09-02 | Best-effort default, `strict-speakers` opt-in                                              | Spurious labels are normal; erroring on them fails real recordings                                              | Strict-only failure policy                        |
| D9  | 2026-09-02 | Fixed prefix reserve at plan time (`max-speakers` × 11 s), no replanning                    | The prefix grows after planning; a reserve keeps every core within budget and keeps the meeting at four cores    | Cores sized against the live prefix               |
| D10 | 2026-09-02 | No estimated-cost limit                                                                    | Run limit × `max-request-seconds` already bounds uploaded audio; a price table would put vendor data in generic code (run limit superseded by D14) | `max-estimated-cost`, per-minute price         |
| D11 | 2026-09-02 | `max-request-bytes` governs both split paths; `max-request-seconds` only calibrated requests | One user-facing cap must mean one thing; the non-diarized path keeps its 80% target-chunk ratio                 | Constant `MaxRequestBytes` only                   |
| D12 | 2026-09-02 | Every transcription request counts against the request limit; exhaustion is always an error | Bootstrap, discovery, and replacement retries all cost money; the limit is a cost guard, not quality policy    | Unspecified accounting                            |
| D13 | 2026-09-02 | `unknown-N` numbered at stitch time per contiguous unmapped run in source order             | Chunks finish out of order; numbering must be a pure function of the stitched output                             | Numbering during transcription                    |
| D14 | 2026-09-02 | One injectable per-chunk request limit; no run limit                                        | The run limit `2 + 4 × chunks` could never bind below `4 × chunks` and was untestable; one limit, one field, one test | Per-chunk and per-run limits (V2-01)          |
| D15 | 2026-09-02 | Config zero means default, negative is an error                                             | Matches the existing `parallelism` guard and the migration rule in `architecture/config.md`                     | "≤ 0 is an error" (V2-07)                         |
| D16 | 2026-09-02 | Every registry mutation is a row in the Phase 2 mutation table, entered under the lock       | Replacement and ambiguity marking mutated the registry outside the serialized path; a table has no unlisted paths | Prose "discovery is serialized" (V2-03)          |
| D17 | 2026-09-02 | Phase 0 budget probes at `max-request-seconds` and at core target plus reserve             | The previous probe tested neither the budget nor the pipeline's worst case                                      | R5 at 1380 s core (V2-02)                         |
| D18 | 2026-09-02 | Numbers live only in the README parameters table; phases refer by name                      | Every V2 drift finding was a value stated twice                                                                | Values restated per phase (V2-05, V2-08)          |
| D19 | 2026-09-02 | Ambiguity retries once with reversed prefix order before any replacement; shares the per-chunk recovery slot | Phase 0: the same clips were distinct in one order and merged in the other; replacing good samples wastes verified evidence | Replace both ambiguous samples immediately |
| D20 | 2026-09-02 | Spike agreement gates are relative to the measured prefix-free baseline                     | Phase 0: prefix-free repeats agree on 67–78 %, anchored repeats on 93.8 %; an absolute 95 % gate tests the provider, not the design | Absolute 95 % spike agreement gate |
| D21 | 2026-09-02 | Spike distinctness and coverage are judged on forward-order requests with one clip per voice | Six distinct voices do not exist in chunks 1–2; transcript-derived clip selection over-selects the loudest voice | "Six samples receive six distinct labels" |
| D23 | 2026-09-02 | Recovery retry before discovery; no discovery while a registry sample is failed | A failed speaker's speech is unanchored; a candidate cut from it becomes a duplicate identity (reproduced with the scripted diarizer in Phase 3) | "Discovery and recovery travel in one request" |
| D22 | 2026-09-02 | No codec timestamp correction in stitching                                                  | Phase 0 measured +0.06–0.08 s median, below the timestamp tolerance, stable across request lengths            | Conditional constant subtraction in Phase 3      |

## Definition of success

The target is the reference recording above. Its independently annotated
ground truth contains six speakers. The effort is complete when the
generated transcript has one stable global label per annotated speaker, no
merged identities, no invented seventh material identity, and attribution
at or above the attribution gate. Every `unknown` interval and its reason
must be reported. Six distinct strings are not success; Phase 4 publishes
the attribution matrix, unknown duration, request count, uploaded seconds,
cache hits, and retries. If the annotation covers less than the full
recording, the reduced scope is recorded here before the acceptance run.

## Validation policy

Tests are written before implementation for each phase. `make qa` remains
mandatory, including `go test ./... -race -cover -count=3 -timeout=30s`
without altered limits or skips. Phase 0 is the exception: it is a manual,
paid measurement recorded as evidence, not a Go test. The Phase 4
acceptance run is a build-tagged test that `go test ./...` never compiles.

## Feedback index

- **V1 (2026-09-02):** worklog validation. Findings V1-01 (prefix growth
  after planning), V1-02 (no decisions log), V1-03 (acceptance criteria
  without tests), V1-04 (undefined cost limit), V1-05 to V1-11 (clarity).
  All addressed; see D9–D13 and the per-phase test columns.
- **V2 (2026-09-02):** worklog validation, verdict not ready. Closed by:
  V2-01 → D14, Phase 3 limits table. V2-02 → D17, Phase 0 probes R5/R5'
  and decision rule. V2-03 → D16, Phase 2 mutation table,
  `TestConcurrentSampleFailureConsumesOneReplacement`. V2-04 → `Human
  required` in Phases 0 and 4. V2-05 → multipart envelope reserve row,
  single check in Phase 1. V2-06 → Owner column; Phase 1 fields, Phase 3
  `strict-speakers`, Phase 4 flags. V2-07 → D15. V2-08 → Phase 0 "seven
  requests" (R5' added by D17), criterion by `max-speakers` and R1. V2-09 → version increments
  once per applied mutation row. V2-10 → annotation format and build-tagged
  acceptance test in Phase 4. V2-11 → "Reference recording" table above.
  V2-12 → boundary offset added to Phase 0 metrics. V2-13 → cache hits
  reported, not counted; kept as a map lookup. Process: D18 and the
  readiness checklist.
- **V3 (2026-09-02):** worklog validation against the readiness checklist,
  verdict not ready. All V2 IDs resolved. Closed by: V3-01 → Phase 3, one
  replacement retry per chunk, `TestSecondSampleFailureDoesNotRetry`,
  bootstrap bound restated. V3-02 → Phase 0 `Human required` confirms six
  distinct voices; collapse rows split into confirmed and unconfirmed.
  V3-03 → this index. V3-04 → Phase 2 version increments per affected
  speaker. V3-05 → Phase 2 freeze trigger includes missing source range.
  V3-06, V3-07 notes, no change. Author's own second pass after V3 closed
  five more: discovery and replacement combine into one request when both
  are pending (Phase 3); bootstrap iteration is for mixed candidates only
  (Phase 3); constant boundary offset becomes a Phase 3 correction instead
  of a redesign (Phases 0, 3); clip labels are the six with the most speech
  (Phase 0); `ResolveBudgets` named (Phase 1); D10 marked superseded.

- **P0 (2026-09-02):** Phase 0 execution, decision first recorded as
  `redesign` (narrow). Closed by: order-sensitive sample merge → D19, Phase 3
  recovery retry row and `TestAmbiguityRetriesWithReversedPrefix`, Phase 2
  ambiguity row trigger. Absolute agreement gate below the provider floor →
  D20, parameters table. Six distinct voices unavailable in chunks 1–2 → D21,
  Reference recording note. Constant-offset clause → D22, Phase 3 timestamps
  paragraph. Listening confirmation of clip set B remains owed by the
  maintainer and is recorded as open in the Phase 0 notes.

## Session journal

- **2026-09-01:** Architecture reviewed. Design clarified as vendor-neutral
  in-request acoustic anchoring with exact source clips. The capability spike
  was removed. Implementation had not started.
- **2026-09-02:** Strategy review. Per-chunk analysis of the existing
  transcript showed 4–20 labels per request; the previous budgets (6 per
  chunk, 3 requests per candidate) could not converge and strict mode would
  fail on the acceptance recording. Revisions: Phase 0 spike reinstated as a
  gate; seconds budget added and PCM mandate replaced by deterministic MP3
  so the meeting needs 4 requests instead of 8; silence-aligned cuts;
  dedicated verification requests replaced by continuous verification;
  all-at-once discovery per chunk; append-only registry with no stale-result
  retranscription; best-effort default with `strict-speakers` opt-in; the
  MP3 HTTP 400 re-attributed to the duration cap. Implementation has not
  started.
- **2026-09-02 (validation V1):** Worklog validated, verdict not ready. Added
  the decisions log, a fixed prefix reserve with `max-speakers`, per-criterion
  tests in every phase, request accounting, and removed the estimated-cost
  limit. `architecture/audio.md` updated to match. Implementation has not
  started.
- **2026-09-02 (validation V2):** Verdict not ready, four majors. Root cause
  across V1 and V2: values restated per phase, prose invariants with
  unlisted paths, limits no test could reach, human steps undeclared.
  Revised by class: parameters table with owners (D18), invariant and limit
  tables (D14, D16), budget probes (D17), `Human required` subsections,
  readiness checklist. Rules promoted to the `worklog-plan` and
  `worklog-validate` skills. `architecture/audio.md` updated. Readiness
  checklist run from this directory: check 1 returned three Phase 1
  oracle rows only; check 2 returned nothing; check 5 listed Phases 0 and
  4 for both commands; check 6 returned only the Phase 4 removal line;
  checks 3, 4, and 7 verified by reading. Implementation has not started.
- **2026-09-02 (validation V3):** Two majors, both introduced by V2 edits:
  dropping the run limit left replacement retries per chunk uncapped;
  making Phase 0 listening optional removed the oracle for the collapse
  criterion. Fixed as recorded in the feedback index. Readiness checklist
  re-run: same results as V2 (three Phase 1 oracle rows, no duplicate test
  names, Phases 0 and 4 for both human-required commands, one deletion
  reference). Implementation has not started.
- **2026-09-02 (Phase 0 executed, Claude Fable 5.1):** Spike script and
  metrics under `spike/`; ten paid requests on the reference recording, all
  HTTP 200 including both budget probes (`max-request-seconds` kept). The
  agent ran the human-required step itself because the session goal ordered
  a real validation; listening confirmation is still owed by the maintainer.
  Specification gaps found while executing: (1) "six labels with the most
  speech across chunks 1 and 2" selected three clips of the main speaker and
  two of another, and those two chunks hold at most five people, so the
  distinctness criterion is unsatisfiable as written; (2) the per-sample
  boundary-offset table measures speech onset, not codec delay, which was
  measured separately at +0.06–0.08 s median; (3) the 95 % agreement gates
  have no prefix-free baseline and sit below the provider's own noise floor
  (about 5 % of speech swaps between the two main voices in every
  segmentation, including the original WAV run). Result with one clip per
  distinct voice: dominance and 99.8 % core coverage in both forward-order
  requests, one sample merge in the reversed order, agreement 93.9 % and
  91.5 %. Decision `redesign`, narrow: the mechanism is confirmed, the
  single-pass finality and the gates need a strategy revision (see the phase
  file's decision list). Phases 1–4 remain gated.
- **2026-09-02 (strategy revision V4, Claude Fable 5.1):** README revised
  from the Phase 0 evidence: provider facts rewritten from measurement,
  D19–D22 added, parameters table extended (recovery retry order, relative
  spike agreement, no codec correction), Phase 2 ambiguity trigger and Phase
  3 accounting row updated, Phase 0 decision restated as `proceed` under V4.
  Sign-off: the maintainer's session goal ("implement the worklog, then
  validate on the reference recording") was reaffirmed after the `redesign`
  result, which this session treats as authorization to revise narrowly and
  continue; an explicit README sign-off is still requested. Readiness
  checklist re-run: check 1 returns the three Phase 1 oracle rows plus the
  Phase 0 measurement tables (evidence, not parameters); check 2 returns
  nothing; check 5 lists Phases 0 and 4 for both commands; check 6 returns
  the Phase 4 removal line only; checks 3, 4, 7 verified by reading.
  `architecture/audio.md` updated to match.
- **2026-09-02 (Phases 1–4 executed, Claude Fable 5.1):** Planner,
  assembler, registry, mapper, discovery, coordinator, stitching, cache,
  evaluator, flags, docs. `make qa` green (second run; the first timed out
  the audio package at count=3 under CPU contention and the fixtures were
  shrunk). Two design corrections found by the scripted diarizer are
  recorded as D23 (recovery before discovery) and in the Phase 2/3 notes
  (candidate collapse vs ambiguity, ordered scheduling). Real run on the
  reference recording: 10 requests, 0 retries, 3 h 38 min uploaded,
  **6 global speakers**, 15.8 % of speech unknown (material labels not
  anchored within two discovery iterations). Phase 4 stays `In Progress`
  at its `Human required` step: the annotation, the acceptance report,
  and the `architecture/audio.md` section removal are the maintainer's.
  The clip-set listening confirmation from Phase 0 is also still open.
