# Survey calibration — long recordings find their speakers in one survey, not chunk by chunk

Status: **signed 2026-11-22, amended after H1 the same day (chained merge, join after labelling) and re-signed; interfaces 1–3 signed on V3 Ready. Amended 2026-11-23 after the Retro run in Penelope (the join's survey check, the retry's swap of shared clips) and re-signed.** Phases 1–3 complete and reviewed; H2 (the real Retro run) in progress.
Supersedes the bootstrap and discovery of
[`2026-09-01-adaptive-speaker-calibration`](../2026-09-01-adaptive-speaker-calibration/README.md)
(cited below as `asc`); its planner, assembler, mapper, budgets and measured provider facts stay.

## Goal

An oversized recording transcribed with an anchored diarizer gets one label per person across the whole
recording, in less than half of today's time. Today the first chunk runs alone and without clips, registers
whatever it happens to hear, and is never revisited. The new path hears every chunk first (survey), merges
the voices it found into one registry, and only then transcribes every chunk against that registry.

What must stay true: it works unattended (I1), identity comes only from audio inside one request (I2),
no speech is dropped (I7), and the effort ends with less code than it started with (I9).

Why now (evidence: notebook `codebases/imago/clai/2026-11-20T15-20-00Z_88-minute-run-log-why-unknown-and-slow.md`):
on an 88-minute meeting, chunks 2–5 mapped the same 6 speakers with 13 s unknown, but chunk 1 (bootstrap)
registered 2 voices and left 229 s unknown in 21 `unknown-N` labels. The run took 67.8 minutes for
12 requests, because the bootstrap is serial and each chunk's requests run one after another.

## Glossary

| Term           | Meaning                                                                                              |
| -------------- | ---------------------------------------------------------------------------------------------------- |
| Chunk          | one planned core of the recording (`ChunkPlanner`, unchanged)                                        |
| Material label | a request-local label with ≥ 3 s and ≥ 1 % of its chunk's speech (`Materiality`, unchanged)          |
| Survey         | every chunk transcribed once, with no prefix; its transcripts are used for candidates only           |
| Candidate      | the best 3–10 s clip of one material label of one survey chunk (`SampleExtractor`, unchanged)        |
| Merge link     | two requests (forward, reversed) made only of clips, no core: one clip per voice found so far plus one later chunk's candidates |
| Group          | clips that share a label in both orders of one link: one speaker                                     |
| Registry       | the groups as global speakers `speaker-N`, each with its clips ranked; fixed after the merge except for the join |
| Join           | two voices whose clips share a label in every labelling request that settles both (at least 2) become one voice, unless a chunk whose survey heard both under labels of their own leaves, in its labelling request, a material label no registry clip settled on; decided once, after the labelling pass |
| Labelling pass | every chunk transcribed with the registry's best clips as its prefix (today's calibrated pass); the output comes from it |
| Retry          | one more request for a chunk whose labelling pass went wrong, fixing all of it at once: the clips in reversed order if two shared a label, the speaker's next clip for a failed one and for each of two that shared a label, and a clip of each material voice that matched no one |
| `unknown-N`    | speech no registry speaker claimed; one name per unmapped voice of a chunk                           |

## Flows

One trigger: `clai audio transcribe <file>` (or `audio_transcribe`) on a recording over a request budget,
with an anchored diarizer. Every other route is untouched.

```text
Splitter ──Run(file)──▶ Coordinator
Coordinator ──Plan(file)──▶ ChunkPlanner                      ◀── cores (computed once, I5)

 survey     Coordinator ──RequestFunc(survey, no samples, core k)──▶ requests   × every chunk, at once
                        ◀── segments (request-local labels)
 candidates Coordinator ──Candidates(segments, label)──▶ SampleExtractor        × every material label
                        ◀── best clip per label
 merge      Coordinator ──Merge(candidates)──▶ Merger           chunk 1's candidates seed the voices
            Merger ──RequestFunc(link k forward, a clip per voice + chunk k's clips)──▶ requests  ┐ at once
            Merger ──RequestFunc(link k reversed, the same clips reversed)──▶ requests           ┘
                   ◀── segments ×2          × every later chunk, in series: each link sees the voices so far
            Coordinator ◀── registry (≤ 8 voices), MergeReport
 labelling  Coordinator ──RequestFunc(labelling, registry clips, core k)──▶ requests  × every chunk, at once
                        ◀── segments ──Map──▶ LabelMapper ◀── Mapping
 join       Coordinator: voices on one label in every labelling request that settles both ──▶ one voice
            (not when a chunk's survey heard them apart and its labelling request left a voice naming nobody)
 retry      Coordinator ──RequestFunc(retry, fixed clips + a clip per unmatched voice, core k)──▶ requests
                        ◀── Mapping            × every chunk whose labelling pass went wrong, at once
 stitch     Coordinator ──stitch(mappings)──▶ segments in source time, `unknown-N` per voice
Splitter ◀── segments ── Coordinator           (naming with known voices follows, unchanged and optional)
```

`requests` is today's `RequestFunc`: assemble, check both budgets, cache, transcribe, count. "At once" is
bounded by `parallelism` (Q1). Variants of the same trigger:

- **More than 8 voices:** a sign the merge failed to group someone, not a real meeting. The registry keeps
  the 8 groups with the most survey speech, the others' speech becomes `unknown-N`, and stderr names it
  as a likely fault (Q2).
- **A voice still unmatched after the retry:** it stays `unknown-N`. No run fails over labels (I7).
- **Two voices' clips share a label in some labelling requests but not all:** no join; those chunks are
  ambiguous and get their retry.

## Status board

**Line:** running

| #   | Node                          | Parent | Class       | Interface                                                                                                             | Status      |
| --- | ----------------------------- | ------ | ----------- | --------------------------------------------------------------------------------------------------------------------- | ----------- |
| 1   | Speaker merger                | root   | contract    | `internal/audio:Merger` — `Merge(ctx, candidates) (Snapshot, MergeReport, error)` (`internal/audio/merge.go`)          | Complete    |
| 2   | Survey calibration            | root   | composition | root: `Coordinator.Run` for an oversized anchored recording (signature and output unchanged); the `calibration` event log's request kinds and `merge` events; the board's phase lines and footer; `architecture/audio.md` | Complete    |
| 3   | Prune bootstrap and discovery | root   | prune       | `Removes` table: bootstrap, discovery's registry additions, the registry's mutation paths, the per-chunk request limit, the `max-speakers` flag and config field (8 becomes a constant) | Complete    |

Order: README sign-off → H1 (the merge spike) → phase files → validation → interface sign-off → 1 → 2 → 3.
A `no-go` from H1 returns to this README; no phase file is written before it.

### Imports

| Member                                                                              | Kind                                   | Used by |
| ----------------------------------------------------------------------------------- | -------------------------------------- | ------- |
| `ChunkPlanner.Plan`, `Plan`, `Budgets`                                              | repository, unchanged                  | 2       |
| `RequestAssembler`, `AssembleRequest`, `RequestManifest`, `SampleClip`              | repository; phase 1 adds a request with samples and no core | 1, 2 |
| `SampleExtractor.Candidates`, `Materiality`, `better`                               | repository, unchanged                  | 1, 2    |
| `LabelMapper.Map`, `Mapping`, `SampleStatus`                                        | repository, unchanged                  | 1, 2    |
| `RequestFunc`, `requestCache`, `RequestTranscriber`                                 | repository, unchanged                  | 1, 2    |
| `Snapshot`, `Speaker`                                                               | repository; phase 3 reduces the registry to these | 1, 2, 3 |
| the revisit of a chunk with the final registry (the retry's base); `unknown-N` per voice in `stitch` | repository, **landing 2026-11-20** in the current path; phase 2's file names the members once committed | 2 |
| scripted diarizer and fake runner (`calibration_fakes_test.go`, `discovery_fakes_test.go`) | repository test doubles         | 1, 2    |

## Scope

Not built:

- No change to planning, encoding, budgets, the cache, the plain split path, the request-local
  (OpenRouter) path, naming, the voice library, or the output formats.
- No flag or config field that selects the old path. There is one path (I9).
- No speaker added after the merge. A voice the survey found in no chunk stays `unknown-N` (I4).
- No retry by clips alone (resolving an unmatched voice from a merge-style request instead of re-running
  its chunk). It would save about 9 minutes on the 88-minute recording; decide after acceptance.
- No strict mode, ever (Lorentz, 2026-11-22: "Having unknown labels that can be labeled is a lot better
  than failing. The transcript round is very expensive"). `strict-speakers` was removed from the current
  path on 2026-11-22, before this effort; an old config that still sets it loads.
- No streaming or `heard mm:ss` on this route (unchanged).
- No Minutes (GUI) work. Human-in-the-loop belongs there, and nothing here waits for it.

Human authored: the export of the recording and its transcript from Minutes, the spend approval (H1), and
the annotation of chunk 1 (H2).

## Root facts

### Invariants

| ID  | Statement                                                                                                                                                                                       | Binds   |
| --- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ------- |
| I1  | **Unattended.** No step reads the voice library, asks a question, or needs human input. Naming runs after the stitch, as today.                                                                 | 1, 2    |
| I2  | **Never by guesswork.** Identity comes only from audio inside one request. Nothing is merged by time, text or a forced count (`architecture/audio.md`, kept). Two clips are one speaker only when each is dominant in both orders of one merge link and they share a label in both. Two voices are joined only when their clips share a label in every labelling request that settles both, at least 2, and no chunk whose survey heard both under labels of their own leaves, in its labelling request, a material label no registry clip settled on: there the survey heard two people and the labelling request heard one clip as the other's, so a clip is bad, not the voices one (the Retro, 2026-11-23). | 1, 2 |
| I3  | Labels are request-local and never compared across requests. Survey labels are never compared with labelling-pass labels.                                                                            | 1, 2    |
| I4  | The registry is built by the merge and changes at most once more, by the join (I2), which only joins voices. No add, no global sample change. A failed sample, and each of two samples that settled on one label, is swapped for the speaker's next clip in that chunk's retry only. | 1, 2, 3 |
| I5  | The plan is computed once. Every request, merge requests included, is checked against both budgets before upload. The prefix reserve is fixed at 8 × 11 s = 88 s.                               | 1, 2    |
| I6  | No survey segment reaches the output. Output segments come from labelling and retry requests only.                                                                                             | 2       |
| I7  | Unmapped speech is kept as `unknown-N`, never dropped and never an error. There is no strict mode.                                                                                             | 2, 3    |
| I8  | The path has a fixed length: at most 3 requests per chunk (survey, labelling, one retry) plus 2 per merge link (one link per chunk after the first). No loop depends on a provider answer.                                          | 2, 3    |
| I9  | **Lean.** One path, no fallback to the old one. The effort ends with at most 10,400 non-test lines in `internal/audio/`, the 9,942 at the start of phase 1 plus about 4.6 % (measured by L1.4). Amended 2026-11-22: "fewer" could not be met after every safe cut the reviewers found. Amended 2026-11-23 from 10,300 for the H2 run 2 changes (10,361 after them). | 1, 2, 3 |
| I10 | Deterministic for the same answers: `speaker-N` is numbered by each group's earliest clip in source time; on overflow the groups with the most survey speech are kept.                          | 1       |
| I11 | Tests stay fast: the scripted diarizer and the fake runner only, no sleep, no real ffmpeg beyond today's. The gate runs unedited.                                                               | 1, 2, 3 |

### Parameters

Existing defaults are `asc`'s table and are not restated. New or changed:

| Parameter                     | Default                                                        | Decided by                        |
| ----------------------------- | -------------------------------------------------------------- | --------------------------------- |
| candidates per material label | 1, the best clip (longest speech, then clearance)              | this README                       |
| merge orders                  | 2: forward and reversed                                        | Lorentz, 2026-11-20 (direction)   |
| merge link size               | one clip per voice so far plus one chunk's candidates (9–13 clips on the Retro) | H1, 2026-11-22: 25 clips in one request gave two people one label |
| dominance in a merge request  | ≥ 80 % of the clip's labeled speech (the existing threshold)   | `asc`                             |
| retries per chunk             | 1, fixing every problem of the labelling pass at once          | this README (keeps 3 per chunk)   |
| `parallelism`                 | unset: every chunk at once; a set value caps it                | Maintainer, 2026-11-20 (Q1)       |
| voices labelled               | 8, fixed (no setting); more found is warned as a likely fault  | Lorentz, 2026-11-22 (Q2)          |
| spike thresholds S1–S4        | see H1                                                         | this README, from `asc` gates     |
| acceptance targets            | see H2                                                         | `asc` gates (≥ 95 %, < 2 %)       |

### Time and cost, 88-minute recording (measured in H1, 2026-11-22)

Before (2026-11-20): 12 requests, 13,230 s uploaded (2.5× the 5,256 s source), 67.8 minutes.

| Stage                         | Requests | Uploaded | Wall time, measured                                  |
| ----------------------------- | -------: | -------: | ---------------------------------------------------- |
| plan, silence, encode         |        0 |        0 | about 0.7 min (0:39 to the first request, 2026-11-20) |
| survey, 5 chunks at once      |        5 |  5,256 s | 6.3 min (5.2–6.3), no 429                            |
| merge, 4 links of 2 at once   |        8 |    980 s | 2.9 min (34–48 s per link)                           |
| labelling pass, 5 at once     |        5 |  5,634 s | 6.8 min (6.2–6.8)                                    |
| join                          |        0 |        0 | 0                                                    |
| retry                         |        0 |        0 | 0 (none needed on the Retro)                         |
| **Total**                     |   **18** | **2.26×** | **about 17 min**                                    |

- A retry round, when a chunk needs one, adds about 6.5 minutes.
- Hard bound (I8): 23 requests, about 17,500 s (3.3×). Today's bound is 20 requests, 28,000 s.

### The Retro in Penelope (2026-11-23): why the join and the retry changed

Run log without text: notebook `codebases/imago/clai/2026-11-23T05-40-02Z_retro-penelope-run-the-unmapped-voice-was-lorentz.md`.

- The merge found 6 voices, one clip per chunk each for `voice-5` (survey speech per chunk 123.8, 102.8,
  243.8, 132.9 and 122.8 s).
- In all 5 labelling requests, `voice-5`'s clip (68:12) and `voice-4`'s (54:13) settled on one label, so the
  join made them one speaker. Every labelling request then held one material label no clip settled on:
  122.5, 96.3, 227.2, 131.6 and 100.0 s, `voice-5`'s survey speech chunk by chunk. The library named that
  speech Lorentz, and `speaker-3` Filip.
- The surveys of chunks 1 and 4 had heard `voice-4` and `voice-5` under labels of their own. A survey that
  splits one person leaves nothing over once the clips share a label. A bad clip leaves the other person's
  speech on a label nobody claims. The join now tells the two apart (I2).
- Without the join the two clips are ambiguous, and the retry already reverses them. It now also swaps both
  for their next clips (I4), since one of them does not sound like its speaker.
- Not done: a retry candidate that holds its own label does not become a speaker (Scope stays). The voice
  this run lost had been found by the survey.

## Validation policy

### Gates

```sh
make qa
go vet ./...
go run github.com/mibk/dupl@latest -t 80 .
```

`make qa` runs staticcheck, gofumpt, `go fix`, and `go test ./... -race -count=3 -cover -timeout=30s` with
`-p` at the physical core count. Goldens: `go test ./e2e -run '^TestDiagnosticOutputGoldens$' -args -update-diagnostic-goldens`.

### Human checks

Neither is a gate. Both spend money and need the 88-minute recording, which lives in Minutes' IndexedDB.

#### H1 — merge spike (certifies root; before any phase file and before the interface sign-off)

The design's main risk: does a request made only of clips group clips of one person from different chunks?

**Human required (Lorentz):** download the recording from Minutes, and export the speakers of the good
2026-11-22 transcript with a browser-console snippet kept outside the repository (start, end and speaker per segment, no text). The
cost bound was approved with the README sign-off; the key is the OpenAI key in the agent's shell. The agent
stops until the two files are in `~/.sandbox-safe/`.

Inputs: the recording (5,255.7 s); the 2026-11-22 transcript's speakers (the reference: who spoke when,
the run that labelled well); the 12:50Z run's `calibration` records (5 core cut times, 7 registry clips of
6 speakers).

Procedure (a spike script kept outside the repository, one step per run, with ffmpeg and the `clai` binary;
provider answers stay outside the repository; results in the notebook `codebases/imago/clai/2026-11-22T10-19-30Z_merge-spike-many-clips-in-one-request-merge-two-voices.md`):

1. **Known clips.** Join the 7 logged registry clips (6 speakers) with 0.5 s of silence; send them
   forward and reversed. 2 requests of about 2.5 minutes.
2. **Survey.** Cut the 5 cores at the logged times, encode as the pipeline does, transcribe all 5 at once
   with no prefix. Record each latency and any 429. 5 requests, 87.6 minutes.
3. **Merge at scale.** Cut the best clip of every material label of every chunk, join them, send forward
   and reversed. A clip's reference speaker is the `speaker-N` holding most of its interval in the
   reference transcript; a chunk-1 clip under an `unknown-N` is identified by Lorentz listening
   (at most 10 clips). 2 requests of about 6 minutes.
4. **Anchor chunk 1.** Send chunk 1 once with the best clip of every group as its prefix.
   1 request of about 19 minutes.

Cost bound: 10 requests planned, at most 13; at most 150 minutes of uploaded audio; about 25 minutes of
wall time. A step is repeated at most once, and only for a transport failure.

| ID  | Go when                                                                                                        | Otherwise                 |
| --- | -------------------------------------------------------------------------------------------------------------- | ------------------------- |
| S1  | no group holds clips of two reference speakers, in step 1 and in step 3                                        | no-go                     |
| S2  | step 3 gives at most 8 groups, and each of the 6 speakers has ≥ 80 % of its clips in one group | no-go                     |
| S3  | step 4 maps ≥ 90 % of chunk 1's speech to a registry speaker (`asc`'s spike gate; today 70.6 %)                | no-go                     |
| S4  | 5 requests at once all answer without a 429, the slowest within 10 minutes                                     | go, with Q1's default kept at 3 |

`go` needs S1–S3. A `no-go` reopens the direction with Lorentz; the measurements go in the notebook either way.

#### H1 result (2026-11-22, the notebook note above; 22 requests, about 209 minutes of audio)

- **S4 yes:** 5 survey requests at once, 5.2–6.3 min, no 429.
- **One merge request of all 25 candidates: S1 no-go.** It used 5 labels for about 6 people and gave
  `speaker-4` and `speaker-5` of the 2026-11-20 run one label in both orders; 7 clips in one request kept
  all 6 apart. Hence the chained merge.
- **Chained merge, 4 links of 9–13 clips:** 7 voices. The 2026-11-20 run's 6 speakers (7 clips) land on
  6 different voices. One link joined two voices that shared a label in both orders (`speaker-3` had been
  split). The seventh voice was a single chunk-3 clip.
- **Labelling pass:** the seventh voice and `speaker-2` shared label A in all 5 requests, so as an
  ambiguity it left 37.4 % of the speech unknown. With the join: 0 % unknown, 6 voices, every chunk 100 %
  mapped, S3 go (chunk 1: 100 %), and the 2026-11-20 run's 7 clips on 6 distinct voices.
- **Open:** whether the 137 s of the joined chunk-3 voice are `speaker-2`. Lorentz checks one moment
  (41:42 of the Retro) or exports the reference; S2 needs the reference.

#### H2 — acceptance (certifies root; after phase 3)

`clai audio transcribe -am gpt-4o-transcribe-diarize -af json <the 88-minute recording>` with
`LOG_LEVEL=warn,audio=debug`, scored with the existing `Evaluate` (`internal/audio/eval.go`).

**Human required (Lorentz):** annotate chunk 1 (0–1,048.8 s; who speaks when, tab-separated, crosstalk
marked), the chunk that failed. `asc`'s acceptance was never run; its gates are kept (Q5 on the scope).

| Target                                         | Today (2026-11-20)                | Required                         |
| ---------------------------------------------- | --------------------------------- | -------------------------------- |
| attribution, annotated non-crosstalk speech    | not measured                      | ≥ 95 % on chunk 1 (`asc` gate)   |
| unknown, share of all speech                   | 5.3 % (241.9 s of 4,545 s)        | < 2 % (`asc` gate)               |
| labels                                         | 6 speakers + 21 `unknown-N`       | 6 speakers, no material `unknown-N` |
| agreement with today's labels, chunks 2–5      | —                                 | ≥ 90 % (repeated labelling passes agree on 91.5–93.9 %) |
| wall time, `parallelism` ≥ 5                   | 67.8 min                          | ≤ 40 min                         |
| uploaded audio                                 | 2.5×                              | ≤ 3.3× (the hard bound)          |

## Findings

| ID  | Severity | Node | Owner | Status | Summary |
| --- | -------- | ---- | ----- | ------ | ------- |
| V1-01 | major | 3 | fix | Closed +wip | check 4 · Acceptance criteria 3 and 5, Removes rows `Budgets.MaxSpeakers` and `RequestLimitError` · criterion 3 cites `TestPlanReservesEightSlots` "(phase 2, unedited)" and criterion 5 says phase 2's scenarios pass unedited (its `-run` matches `SurveyPath`), yet `TestPlanReservesEightSlots` sets `Budgets.MaxSpeakers` 3 and the Removes table itself edits `TestSurveyPathIsTheOnlyPath`; neither compiles unedited once the fields go, and the `Budgets.MaxSpeakers` row lists neither test · name both tests as consumers that drop only the removed settings, and word criteria 3 and 5 to allow exactly that edit |
| V1-02 | major | 2 | fix | Closed +wip | check 3 · Run.1 and the list of rewritten tests; phase 3 Removes row `transcribe.max-speakers` · the fixed 88 s reserve makes the planner refuse `smallBudgetConf`'s 80 s budget ("prefix reserve 1m28s … reaches max-request-seconds 1m20s"), so `TestMaxRequestBytesAppliesToBothPaths`, `TestSplitter_AnchoredDiarizationCalibrates` and `TestSplitter_DiarizeRoutesToCalibration` (the last two also match the old header `▸ calibrated diarization`) fail phase 2's gate; phase 2 lists none of them and phase 3 schedules the fixture change ("small-budget fixtures … hold the 88 s reserve") one phase late · move the fixture change into phase 2, name the three tests among those phase 2 adapts, and leave phase 3 only the `MaxSpeakers` lines |
| V1-03 | major | 2 | fix | Closed +wip | check 4 · Test cases `TestRetryResult` row 4 · "the chunk's only fault is a mixed clip of a speaker with no other clip" expects no request and `retry state=skipped reason=nothing-to-change`, but by L2.1 a silent speaker's mixed clip writes no `retry` record, and a speaking one leaves her label material with no registry clip settled on it, so Run.9 adds a candidate clip cut from it and sends the retry · give the row a Given where Run.9 changes nothing (e.g. that label has no 3–10 s run and there is nothing to reverse or swap), or expect the retry with the candidate |
| V2-01 | major | 2 | fix | Closed +wip | check 3 · the paragraph under Test cases ("`smallBudgetConf` and their fixtures get a seconds budget that holds the 88 s reserve … Their other assertions are unchanged") and L2.10 · raising `smallBudgetConf`'s 80 s budget breaks `TestCalibrationToolBridgeUsesSameEngine` (`calibration_integration_test.go:213` asserts `s.Budgets.MaxRequestDuration` is `80*time.Second`), which phase 2 does not list, so phase 2's gate fails · name that test among those adapted (its 80 s literal follows the new budget), or raise the budget only in the three tests' own confs and keep phase 3's `transcribe.max-speakers` row in step |
| V4-01 | major | 3 | fix | Closed +wip | check 6 · Removes rows `Budgets.MaxSpeakers` and `architecture/audio.md`, criterion 5 and its Test cases row, grep 4 · on the current tree grep 4 also hits `internal/audio/survey_test.go:238` (`TestPlanReservesEightSlots`'s row named "max-speakers 3 is not read") and `architecture/audio.md:414` ("whatever `max-speakers` holds", under "Request budgets and encoding"); neither is an Allowed hit or in a Removes row, and criterion 5's diff check ("only the removed settings") forbids renaming that row, so criteria 1 and 5 cannot both pass · add the row's rename and the doc sentence to the Removes table and to what criterion 5's diff allows, or list them as Allowed hits |
| R1-01 | minor | 1 | fix | Closed +wip | Merge.3, Merge.4, I2 · criterion 2 · `merge_test.go:299`: `one_order_is_not_enough` gives two people one label in the forward request only; a merger keying clips on the reversed label alone (`merge.go:382` → `linkKey{"", r.Label}`) passes every `Merger` test, so two people sharing one label in `merge-reversed` only would become one speaker unseen (the shipped code is right) · add the mirrored row (`sameLabel` keyed `RequestMergeReversed`) expecting two speakers and `c2-A` `LinkNew`, and confirm that probe fails it |
| R1-02 | minor | 1 | fix | Closed +wip | Merge.8, I10 · Test cases row `deterministic` · `merge_test.go:576`: every script's labels run in clip-start order, so naming seed voices by ID (`merge.go:229`) or a link's new voices by ID instead of forward position (`merge.go:422`) passes every `Merger` test; with `c1-A`'s clip at 50 s and `c1-B`'s at 10 s that variant names `c1-A` `voice-1` and phase 2's `merge voice=voice-1 speaker=speaker-1` record changes unseen (the shipped code is right) · add a script whose seed and one link's two new voices have labels against clip order, assert the `voice-N` names, and confirm both probes fail |
| R1-03 | minor | 1 | fix | Closed +wip | Merge.11 · Error management · `merge.go:489` (and `:222`, `:242`): two readings the code takes the stricter way are not written down: every error also zeroes the `MergeReport` (rows 1–3 say "zero Snapshot"), and a `RequestFunc` answering without a manifest fails its link (`merge link for chunk N: merge-reversed request answered without a manifest`), which no row lists; both are consistent with the clauses and pinned by tests · add both to Error management (the zero report in rows 1–3; a row for the missing manifest, test `TestMergerRequestFailure/a_request_answers_without_a_manifest`) |
| R1-04 | minor | 1 | plan | Closed +wip | Merge.3, I2 · `ClipVerdict` · `merge.go:494`: `LabelMapper.Map` counts `Speaker ""` as a label, so a clip covered only by unlabelled segments gets `{Label: "", Share: 1, Settled: true}`, and two such clips share the key `("", "")` and become one speaker, against ClipVerdict's "\"\" when nothing in the clip was labelled"; reachable only if the anchored diarizer returns a segment without `speaker` (`generic/parse.go:51` passes it through; not seen on this route; the H1 spike does the same); phase 2's join (Run.7) has the same question · decide whether "" is a label for the merge verdict and the join; if not, settle only a non-empty label and add an `unsettled_clips` row for it |
| R2-01 | major | 2 | fix | Closed +wip | the board's Final footer "at the end, failures included", Run.16 · `coordinator.go:268`: every failure exit finishes with `c.board.Footer()`, the live footer; a labelling request not answered ends the status with `finishing · 0s elapsed · requests 12/≤18 · resent 4 · in flight 1 · …` instead of `requests … · retries … · cache hits … · … elapsed`; `TestRealFailureFailsTheRun` checks only `requests `/` elapsed`, and `TestCoordinatorLiveErrorExitKeepsFooter` asserts the live footer · write the final footer from one function on every exit path, assert its fields in a failure row, and change that test with it |
| R2-02 | major | 2 | plan | Closed (maintainer: I9 amended) | I9 · L1.4's count: 11,085 now (baseline 9,942); phase 3's Removes applied in a scratch copy give 10,504 (≈ 10,498 with `better`), 556 over · the reductions of R2-03–R2-07 total ≈ 352, leaving ≈ 200; `eval.go` (196 lines, no production caller) lies outside the effort · re-plan phase 3 with R2-03–R2-05 and phase 2's fix with R2-06–R2-07, find ≈ 200 more, else raise a design andon so the maintainer decides on I9 |
| R2-03 | minor | 2 | plan | Closed +wip | I9, CLAUDE.md comments · `coordinator.go:149–251`, `merge.go:1–218`: the signed clause text sits in Go doc comments (103 + 126 lines) and repeats `architecture/audio.md` nearly sentence for sentence · decide whether Interface blocks may keep clause text in the phase file with a short Go doc pointing to the architecture doc (≈ −205) |
| R2-04 | minor | 2 | plan | Closed (plan: not done) | I9, Imports (`LabelMapper` unchanged), L3.1 · `mapping.go:143–171`, `coordinator.go:633–681`: the mapper's holders block is the old path's rule; `answer.mapping` maps twice and rewrites every settled status by Run.8, the merger reads only label and share · let the mapper take Run.8's owners and delete the holders block and the rewrite (≈ −40) |
| R2-05 | minor | 3 | plan | Closed +wip | I9, phase 3 Removes · production-dead members it misses: `Mapping.byStatus`/`Failed`/`Ambiguous`/`RangesFor` (`mapping.go:58–73`, `:98`), `RequestManifest.SourceTime` and `AudioAssembler.RunDir` (tests only, `assemble.go:68`, `:151`), ≈ −45; its `Speaker` row removes `Snapshot.find`, which `planRetry` calls (`coordinator.go:593`); stale old-path comments (`registry.go:23`, `mapping.go:109`, `:166`, `calibration_log.go:124`) · add them to Removes; keep or re-point `find` |
| R2-06 | minor | 2 | fix | Closed +wip | I9, Merge.6–7 · `coordinator.go:791–807`: `clipsOf`'s fallback serves a report without `Voices`, which the `Merger` contract rules out; only the `reportless` double reaches it · rank from `report.Voices` only, delete the fallback and its subtest (≈ −17) |
| R2-07 | minor | 2 | fix | Closed +wip | I9, CLAUDE.md "written twice" · `coordinator.go:857`, `:947`, `:340`, `:132`, `:685`; `calibration_log.go:41`, `:80`: `requestFor` wraps `c.request` (−5), `logFor`/`linkLog` duplicate one closure (−6), `purposeOf` as a map (−5), `parallelismText` (−4), `chunkState` counters as atomics (−15), `joining.was` from `after` (−4), one manifest check in `c.request` (−3) · apply (≈ −40) |
| R2-08 | minor | 2 | fix | Closed +wip | Run.9, L2.2 · `coordinator.go:609`, `:607`, `:575`, `:606`: probes pass the whole suite without the 8-clip cap, without the most-speech-first order, with only `SampleMapped` counted as settled for the swap, and with candidates for labels a registry clip settled on (the shipped code is right) · add rows for 8 speakers plus a pending label, two pending labels, an ambiguous speaker with a next clip and a shared label with core speech; confirm each probe fails |
| R2-09 | minor | 2 | fix | Closed +wip | Run.6/L2.5, Run.7, Run.8 · `coordinator.go:465`, `:740`, `:753`, `:640`: probes pass the whole suite with labelling clips in registry order, without `apart > 0` in the first join test, trying a later speaker after a blocked earliest one, and naming a label from an unsettled clip (the shipped code is right) · add fixtures whose sample order differs from registry order, three-speaker join rows, and a 60/40 mixed clip on a core label; confirm the probes fail |
| R2-10 | minor | 2 | fix | Closed +wip | Run.13 "the merge's included" · `survey_test.go:629`, `coordinator.go:935`: with `slot` always free `TestParallelism` passes 20 times; at `Parallelism` 1 the link's pair is never forced to overlap · hold the first merge request until the test sees whether the second arrives, or unit-test `slot`; confirm the probe fails |
| R2-11 | minor | 2 | fix | Closed +wip | L2.11, `architecture/audio.md` "no label for any decision below" · `coordinator.go:481`, `:989`: the retry trigger and the row's unmatched count include speech with no speaker, which `materialLabels` and `planRetry` skip (a probe dropping that skip in `materialLabels` passes the whole suite); a chunk whose only material unmapped speech is speakerless is due, reads `✗ 1 unmatched`, and may upload a retry · skip `""` in both and add a row, or reword the doc |
| R2-12 | minor | 2 | fix | Closed +wip | CLAUDE.md typed returns · `coordinator.go:514`, `:473`: `retryChunk` returns `(Mapping, string, error)` and `retryAll` `([]string, []Mapping, error)`, the strings explained only by a comment · return a named failed-retry type |
| R2-13 | minor | 2 | fix | Closed +wip | I11 "no sleep" · `coordinator_test.go:175`: `TestCoordinatorCancelsAndCleansUp`, edited in this phase, still polls with `time.Sleep(time.Millisecond)` for up to 5 s · signal the first held request on a channel and wait on it |
| R3-01 | minor | 1, 2 | plan | Closed +wip | worklog-review contract check (Interface token for token), R2-03 · `coordinator.go:147`, `merge.go:134`: R2-03 shortened the `Run` and `Merger.Merge` docs, but phase 1's and phase 2's Interface blocks still carry Merge.1–12 and Run.1–18 in their Go blocks, and no plan or maintainer line records the change (only "orchestrator's decision" in phase 2's notes), so the root review's token-for-token check of phase 1 fails · state in both Interface sections that the clauses stay in the phase file and the Go docs point to them (or re-sign the shortened blocks), with a journal line |
| R4-01 | minor | 3 | fix | Closed +wip | documentation of changed state updated in the same phase · `README.md:3`: the Status line still reads "Phases 1–2 complete; phase 3 in progress." while board row 3 and the journal say Complete; the next agent reading the header resumes a finished phase instead of going to H2 · update the Status line (phase 3 complete and reviewed, H2 next) |

## Session journal

- 2026-11-20 · plan · README drafted
- 2026-11-20 · plan · README amended: Q1 answered (parallelism unset = every chunk at once; transcription requests retried)
- 2026-11-22 · plan · README amended: `strict-speakers` removed (Lorentz: "I don't think we should have this", then "Let's never have strict-speakers"; removed from the current path the same day, `make qa` green), Q3 dropped with it; Q5 answered (a); Q2 answered (8 fixed, `max-speakers` removed, more is a warned fault); Q4 answered (a); recovery retry and mop-up merged into one retry per chunk (3 requests per chunk, was 4); "anchored pass" renamed "labelling pass"; Q4 re-worded
- 2026-11-22 · maintainer · README signed
- 2026-11-22 · plan · README amended: H1 inputs (the reference is the good 2026-11-22 transcript, exported without text; the 12:50Z log gives cores and 7 clips); spike script and export snippet written, dry-run with a fake clai
- 2026-11-22 · plan · H1 steps 1–3 run (9 requests, 98.9 min of audio; spike/results.md): S4 yes (5 at once, slowest 6.3 min, no 429); step 1 kept 6 voices apart from 7 clips; S1 no-go at step 3 (25 clips in one request put speaker-4 and speaker-5 in one group in both orders); step 4 not run; direction back to Lorentz
- 2026-11-22 · plan · H1 continued under "iterate until we can reliably and rapidly transcribe the full retro" (Lorentz): chained merge and labelling pass run (13 more requests); README amended: Glossary (merge link, join), Flows (chained merge, join), I2, I4, I8, merge link size, measured time (about 17 min), H1 result; re-sign needed
- 2026-11-22 · maintainer · README signed (re-signed after the H1 amendment: chained merge, join after labelling; Lorentz: "Approve, start now")
- 2026-11-22 · maintainer · interfaces count as signed once validation says Ready (Lorentz, for this effort: "Yes, sign on Ready"); the interface sign-off line is written by the orchestrator on that verdict
- 2026-11-22 · plan · phases 1–3 written (Fable planner); README amended: Q4's header example bound ≤17 → ≤23 requests, as I8 gives for 5 chunks (planner's finding; no fact changed)
- 2026-11-22 · validate · V1 Not ready · checks 1–10 over README and phases 1–3; check 2: phase 1 and 2 Interface blocks (stub bodies) added to a scratch copy of the working tree (`merge.go`, `Speaker.Clips`, `ErrEmptyRequest`, `Coordinator.Merger`, the survey kinds) with signature probes for every consumed member, `go build ./internal/audio/ && go vet ./internal/audio/`: OK; repository `go build ./...`: OK, `go vet ./internal/audio/`: OK; check 3: every consumed member confirmed by inspection (revisit members uncommitted); L1.4 baseline command: 9942 · findings: 3 major (V1-01–V1-03) · notes: 22
- 2026-11-22 · fix · V1-01, V1-02, V1-03 closed · phase files only (documents); gates not run: no code touched, `make qa`, `go vet`, dupl unaffected · also taken from the V1 notes: test rows for coverage and go doc, fixture and wording corrections in phases 1–3
- 2026-11-22 · plan · phases 1–2 clauses tightened after V1 notes: Merge.8 (voice-N order, names never reused), ID.1, CandidateError.1–2, Found.1 (numbered clauses for the plain-comment members; ErrEmptyRequest points to Assemble.2), Run.6 and Run.11 (the empty-registry labelling answer comes from the cache and is read as a labelling answer, I6)
- 2026-11-22 · validate · V2 Not ready · checks 1–10 over README and phases 1–3; V1-01, V1-02, V1-03 resolved, none regressed; check 2: current Interface blocks extracted verbatim (bodyless funcs stubbed) into a scratch copy of the working tree with `Speaker.Clips`, `ErrEmptyRequest`, the phase 2 kinds, `Coordinator.Merger`, a `Run` signature probe and probes for every consumed member, `go build ./internal/audio/ && go vet ./internal/audio/`: OK; check 3: consumed members confirmed by inspection (revisit members still uncommitted) · findings: 1 major (V2-01) · notes: 9
- 2026-11-22 · fix · V2-01 closed · phase files only (documents); gates not run: no code touched · `smallBudgetConf` left untouched, the three adapted tests raise their own budget; every user of the fixtures, budgets, header and parallelism default grepped and listed in L2.10
- 2026-11-22 · validate · V3 Ready · checks 1–10 over README and phases 1–3; V1-01, V1-02, V1-03 and V2-01 resolved, none regressed; check 2: current Interface blocks extracted verbatim (bodyless funcs stubbed) into a fresh scratch copy of the working tree with `Speaker.Clips`, `ErrEmptyRequest`, the phase 2 kinds, `Coordinator.Merger`, a `Run` signature probe and probes for every consumed member, `go build ./internal/audio/ && go vet ./internal/audio/`: OK; repository `go build ./...`: OK; check 3: consumed members confirmed by inspection; L2.10's fixture, header and parallelism claims re-grepped: hold · findings: none · notes: 5
- 2026-11-22 · maintainer · interfaces 1–3 signed (on validation V3 Ready, as Lorentz approved for this effort: "Yes, sign on Ready")
- 2026-11-22 · plan · README amended: H1 references point to the notebook note; `spike/` was removed from the repository (Lorentz: "no js in the clai codebase"); phase files 2–3 repointed, phase 1 by its executor
- 2026-11-22 · work · phase 1 Complete · start and both end gates green (`make qa` 108 packages ok, `go vet` clean, dupl 49 groups unchanged; end runs with `GOFLAGS=-shuffle=on`); `merge.go` 100 % covered; I9 baseline 9,942 written in the phase file; andon: none
- 2026-11-22 · review · 1 round 1 Ready · `make qa` ×2 (`GOFLAGS=-shuffle=on`, API keys unset): exit 0, 108 packages ok, `internal/audio` 93.8 %; `go vet ./...` ×2: clean; dupl ×2: 49 clone groups; the gates left the tree unchanged; `go test ./internal/audio -run 'Merger|AssemblerContract' -race -count=100 -shuffle=on`: ok; `merge.go` coverage 100 %; Interface block and `merge.go` declarations identical after gofumpt; Assemble.3's pinned hash checked against `0ca840b`'s `Assemble` in a scratch copy; mutation probes in the scratch copy (five caught, three survived: R1-01, R1-02; one equivalent) · findings: 4 minor (R1-01–R1-03 fix, R1-04 plan) · notes: 10
- 2026-11-22 · fix · R1-01, R1-02, R1-03, R1-04 closed · tests first (mirrored reversed-only row, labels against clip order, unlabelled-segment row; the three review probes fail them), L1.7 in the merger (empty speaker filtered before `Map`), Error management rows, phase 2 L2.11 · `make qa`: exit 0 at start and twice at the end (108 packages ok, `GOFLAGS=-shuffle=on`); `go vet ./...`: clean ×3; dupl: 49 groups ×3; `merge.go` 100 %
- 2026-11-22 · work · phase 2 Complete · start and both end gates green (`make qa` 108 packages ok, `internal/audio` 93.5 %; `go vet` clean; dupl 49 groups unchanged; end runs with `GOFLAGS=-shuffle=on`); functions this phase wrote in `coordinator.go` 99.5 % covered; V3 acted on (`TestCacheHitSkipsTranscriber` on `RequestLabelling`, `TestSurveyPathEndToEnd` at 134 s); L2.7 deletions incl. `verdictText`; non-test lines 11,085 (I9 baseline 9,942: phase 3 is at risk, see the phase file's Surprises); andon: none
- 2026-11-22 · maintainer · H2 spend approved: the agent runs the real Retro transcription itself after phase 3 and its review, at most two runs (about 18 requests and 200 min of audio each), with the OpenAI key in its shell (Lorentz: "Yes, run it")
- 2026-11-22 · review · 2 round 2 Not ready · `make qa` ×2 (`GOFLAGS=-shuffle=on`, API keys unset): exit 0, 108 packages ok, `internal/audio` 93.4 %; `go vet ./...` ×2: clean; dupl ×2: 49 clone groups; the gates left the tree unchanged (sha256 of every file); phase 2's tests `-race -count=40 -shuffle=on`: ok, and 12 race binaries at once ×15: all PASS; `TestDiagnosticOutputGoldens`: ok; Run doc comment identical to the Interface block; I9 by L1.4: 11,085, phase 3's Removes simulated in a scratch copy: 10,504 (baseline 9,942); 39 mutation probes in a scratch copy (13 survived: 10 filed in R2-08–R2-11, 2 equivalent, 1 noted) · findings: 2 major (R2-01 fix, R2-02 plan), 11 minor (R2-03–R2-05 plan, R2-06–R2-13 fix) · notes: 11
- 2026-11-22 · plan · phase 3 re-planned (R2-05: Removes gains the production-dead `Mapping` and assembler members, `better` and the stale comments; `Snapshot.find` stays; scratch-copy build ok; the table frees about 627 lines, 11,085 → 10,458 against a baseline of 9,942)
- 2026-11-22 · validate · V4 Not ready · phase 3 only (re-planned for R2-05), checks 1–10; V1-01 resolved, none regressed (V1-02, V1-03, V2-01 are phase 2's, outside this round); check 2: prune, no Interface block to compile; check 3: Consumes (`Coordinator.Run`, `maxVoices`, `Snapshot`, `Speaker`, `Speaker.Clips`, the L1.4 baseline 9942 in phase 1's notes) present in the working tree, `go build ./internal/audio/`: OK; every Removes row checked by member name with grep against the working tree while phase 2's fixer edits `coordinator.go` (L1.4 count 10891 at the time): rows hold; the four greps as the worker will run them: grep 4 has two hits outside its Allowed hits · findings: 1 major (V4-01) · notes: 4
- 2026-11-22 · fix · V4-01 closed · phase 3 file only (documents); gates not run: no code touched; grep 4 run on the tree: every hit outside worklogs is now a Removes consumer or an Allowed hit
- 2026-11-22 · validate · V5 Ready · phase 3 only, checks 1–10; V4-01 and V1-01 resolved, none regressed; check 2: prune, no Interface block; check 3: Consumes (`Coordinator.Run`, `maxVoices`, `Snapshot`, `Speaker`, `Speaker.Clips`, the L1.4 baseline 9942) present, `go build ./internal/audio/`: OK; the four greps run with `/usr/bin/grep` against the working tree (L1.4 count 10891): every hit maps onto a Removes row, re-pointing or Allowed hit, `Snapshot.find` still called by `planRetry`; criterion 6 left to R2-02 as instructed · findings: none · notes: 2
- 2026-11-22 · maintainer · interface 3 signed after its re-plan (on validation V5 Ready, as Lorentz approved for this effort); R2-05 closed by that re-plan
- 2026-11-22 · fix · R2-01, R2-03, R2-06, R2-07, R2-08, R2-09, R2-10, R2-11, R2-12, R2-13 closed · tests first (final footer on failure, speakerless speech no label; mutant-killing rows for Run.6–9 and Run.13, 12 of 12 probes now fail in a scratch copy) · `make qa` exit 0 at start and twice at the end (108 packages ok, `internal/audio` 93.4 %, `GOFLAGS=-shuffle=on`); `go vet ./...` clean ×3; dupl 49 groups ×3 · I9 by L1.4: 10,891 (R2-02 open; R2-04, R2-05 not done, as directed)
- 2026-11-22 · maintainer · R2-02 decided: I9 amended from "fewer non-test lines than at the start" to "at most 10,300 (9,942 plus about 3 %)" (Lorentz: "Accept, go to the Retro run"; after phase 2's fixes 10,891, phase 3 frees about 627); phase 3's criterion 6 follows
- 2026-11-22 · review · 2 round 3 Ready · `make qa` ×2 (`GOFLAGS=-shuffle=on`, API keys unset): exit 0, 108 packages ok, `internal/audio` 93.4 %; `go vet ./...` ×2: clean; dupl ×2: 49 clone groups; the gates left the tree unchanged (sha256); phase 2's tests in one race binary ×40 shuffled: PASS, 12 race binaries at once ×15: PASS; `TestDiagnosticOutputGoldens`: ok; I9 by L1.4: 10,891; phase 3's re-planned Removes on a scratch copy of today's tree: 10,253 (`go build ./...` ok) against the amended bound of 10,300 · R2-01, R2-03, R2-06–R2-13 hold, none regressed (18 probes in a scratch copy: 17 fail the suite, every survivor of review 2 among them; 1 equivalent); R2-05 holds by phase 3's re-plan; R2-02 closed by the maintainer's I9 amendment during this round; R2-04 open by choice; no blocker or major open on node 2 · findings: 1 minor (R3-01 plan) · notes: 0
- 2026-11-22 · plan · R3-01 closed: the signed clauses (Merge.1–12, Run.1–18 and the rest) live in the phase files' Interface blocks; the code's doc comments state what a member does and what is not obvious, and point to `architecture/audio.md` (R2-03, clai's CLAUDE.md: no redundant comments). The root review compares signatures with the Interface blocks, and behaviour with the clauses, not comment text token for token
- 2026-11-22 · plan · R2-04 closed as not done: it would change `LabelMapper.Map`, which the Imports table keeps unchanged, for about 40 lines; with I9 amended and phase 3 reaching 10,253 in a scratch copy, it is not needed
- 2026-11-22 · work · phase 3 Complete · start and both end gates green (`make qa` 108 packages ok, `internal/audio` 93.4 % at start, 95.7 % at the end; `go vet` clean; dupl 49 groups unchanged; end runs with `GOFLAGS=-shuffle=on`, API keys unset); the four greps (`/usr/bin/grep`): 1–3 empty, 4 only the Allowed hits; goldens unchanged; I9 by L1.4: 10,253 (baseline 9,942, bound 10,300); `discovery.go`, `discovery_test.go`, `registry_test.go` deleted, `RequestFunc` in `merge.go`, `reasonNoRun` in `sample.go`; `TestNoStrictSpeakers` is `TestOldConfigSettingsLoad`; andon: none
- 2026-11-22 · review · 3 round 4 Ready · `make qa` ×2 (`GOFLAGS=-shuffle=on`, API keys unset): exit 0, 108 packages ok, `internal/audio` 95.7 %; `go vet ./...` ×2: clean; dupl ×2: 49 clone groups; the gates left the tree unchanged (sha256); the four greps (`/usr/bin/grep`): 1–3 empty, 4 only the Allowed hits (`architecture/audio.md:559`, `TestOldConfigSettingsLoad`); I9 by L1.4: 10,253 (baseline 9,942, bound 10,300); `TestDiagnosticOutputGoldens` ok, `e2e/testdata` byte-identical to the phase-start copy; criterion 5's run ok, phase 2's test files differ from that copy only in `survey_test.go`'s renamed row and 3 setting lines; touched tests `-race -count=20 -shuffle=on`: ok; built binary: `-max-speakers` refused as `-no-such-flag` is, status 1, before a request, and an old `audioConfig.json` with `max-speakers` and `strict-speakers` loads; per-function coverage, phase-start copy against now: unchanged for every surviving function (no merge, labelling, join or retry coverage lost with the removed test-diarizer faults); V1-01, V4-01, R2-05 hold, none regressed · findings: 1 minor (R4-01 fix) · notes: 7
- 2026-11-22 · fix · R4-01 closed: the Status line says phases 1–3 complete and H2 in progress · records only, no gate needed
- 2026-11-22 · maintainer · human check root · H2 run 1 (agent, native): 25 min 17 s, 22 requests, 3.09×, 26.1 % unknown — fails < 2 %
- 2026-11-23 · maintainer · human check root · H2 run 2 (Lorentz, in Penelope with 7 library voices): 26 min 10 s, 23 requests, 3.34×, 11.2 % unknown before naming, 2.4 % after it — fails < 2 %; the join lost a voice
- 2026-11-23 · plan · README amended after H2 run 2 (Lorentz: "do the final improvements"): Glossary (join, retry), Flows (join), I2 (the join's survey check), I4 (shared clips swapped in the retry), the Retro evidence; phase 2's Run.7 and Run.9 re-planned in place; re-sign needed
- 2026-11-23 · maintainer · README signed (re-signed after the H2 run 2 amendment; phase 2's Run.7 and Run.9 re-plan signed with it; Lorentz: "Sign, build now")
- 2026-11-23 · maintainer · I9 amended to at most 10,400 non-test lines (10,361 after the H2 run 2 changes; Lorentz: "Raise I9 to 10,400")
- 2026-11-23 · review · root (the H2 run 2 changes) Ready · 8 minor findings, none blocking: the `join refused` record missing from phase 2's log table and from the transitive refusal; no test for the transitive survey check or for a leftover in another chunk; rows (e) and the retry row out of step with their fixtures; the naming error choice untested against a cancelled order's own error; the mock pairing an order asked twice with reply 0; a transcriber's own `context.Canceled` naming the wrong order; Glossary and I2 said "naming nobody" where Run.7 says "no registry clip settled on" · 16 mutation probes, 4 survived · `go test ./internal/audio ./e2e/... -race -count=3 -shuffle=on`: ok ×2; I9 10,361
- 2026-11-23 · fix · review findings closed: the transitive refusal writes its record; tests rows (g) leftover elsewhere and (h) transitive; the cancelled order fails with its own ctx error in `TestNamer_AFailedOrderCancelsTheOther`, which also covers a transcriber's own `context.Canceled`; an order asked twice starts the next pair in the mock; phase 2's log table, rows (e)–(h) and the retry row; known-voices' `TestName_NeverFails` row
- 2026-11-23 · plan · README clarified, no decision changed: Glossary "Join" and I2 say "a material label no registry clip settled on" as Run.7 does (the review's R-10: read literally, "naming nobody" would count the shared label itself)

## Open questions (for Lorentz, before the README sign-off)

Each is one case of visible behaviour. Internals are decided above as facts.

**Q1 — How many chunk requests run at once by default?**
Case: the 88-minute recording has 5 chunks; `parallelism` defaults to 3.
- (a) Raise the default to 8: one wave up to about 2.8 hours; about 30 minutes for this recording.
- (b) Keep 3: two waves; 43–58 minutes.
- (c) The calibrated path ignores `parallelism` and always sends every chunk at once.
- Recommended: (a), if H1's S4 shows no 429 at 5. The field also governs the plain split path.
- **Answered 2026-11-20 (Lorentz): max it** ("Why not max it?"). Every chunk goes at once by default; `parallelism`
  is only an optional cap. Transcription requests get the retries with growing delays that chat steps already have
  (`internal/text/step_retry.go`), so a 429 waits and retries instead of failing the run. H1's S4 records 429s, and no longer sets a default.

**Q2 — The merge finds 10 voices. What does the user get?**
- **Answered 2026-11-22 (Lorentz):** no limit to design around ("When will there ever be more than 8 people
  who needs transcribing in a meeting..? … let's not design in any arbitrary limits which complicate things
  needlessly. But, if more than 8 speakers are detected, something is likely not working right"). So room
  for 8 voices is fixed and the `max-speakers` setting goes (phase 3). More than 8 found is reported as a
  likely fault, not as a limit to raise: the 8 with the most speech are labelled, the rest stay `unknown-N`,
  and stderr says `found 10 voices; more than 8 usually means one person was not recognised across chunks`.

**Q3 — dropped 2026-11-22.** `strict-speakers` is removed, so no run fails over labels.

**Q4 — The progress board's phase line. Which words?**
Case: the 88-minute run, in order. The first draft's `anchoring` and `mop-up` were unclear (Lorentz, 2026-11-22:
"What is anchoring and mop-up?").
- (a) `finding voices · 5 chunks` → `matching voices · 31 clips` → `labelling · 5 chunks · 6 speakers` →
  `second try · 2 chunks with unmatched voices` → `stitching`. Row states: `finding voices`, `labelling`,
  `second try`.
- (b) Today's words where they exist: `uncalibrated pass`, `calibrated pass`, `discovery`.
- Recommended: (a). The header becomes `speaker labelling  <file>  5 chunks × 17m31s  ≤23 requests` (I8's bound; it read ≤17 before the H1 amendment).
- **Answered 2026-11-22 (Lorentz): (a)** ("Q4 is OK").

**Q5 — How much of the recording do you annotate for H2?**
- (a) Chunk 1 only (17.5 minutes of audio), plus the agreement check on chunks 2–5.
- (b) The whole 88 minutes, as `asc` first asked.
- **Answered 2026-11-22 (Lorentz): (a)** ("Q5 sounds good").
