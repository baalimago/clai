# Phase 2 — Survey calibration

**Parent:** root · **Class:** composition · [README](README.md)

## Goal

Make `Coordinator.Run` label an oversized anchored recording by the survey path — survey, candidates,
chained merge, labelling pass, join, one retry, stitch — as the only path, with its event log, board and
`architecture/audio.md` saying what it does.

## Interface

The root's interface is five things. `Coordinator.Run` is written to `internal/audio/coordinator.go`; the
`calibration` event log to `internal/audio/calibration_log.go`; the board's words to
`internal/audio/coordinator.go` and `internal/audio/eta.go`; the `parallelism` default to
`internal/audio/querier.go`, `internal/audio/split.go` and `internal/audio/cmd.go`; the document to
`architecture/audio.md`.

### `Coordinator.Run`

```go
package audio

// Request kinds of the survey path. The merge's two kinds are phase 1's.
const (
	RequestSurvey    RequestKind = "survey"
	RequestLabelling RequestKind = "labelling"
	RequestRetry     RequestKind = "retry"
)

type Coordinator struct {
	// … today's fields. Two change meaning, one is new:

	// Parallelism caps the requests in flight; zero or less means no cap.
	Parallelism int
	// Merger builds the registry; nil means ChainMerger over the run's
	// request function.
	Merger Merger
	// MaxRequestsPerChunk is no longer read (removed in phase 3).
}

// Run returns the stitched, labelled transcript in source time. Its
// signature, its segments' shape and Totals are today's.
//
// - Run.1 — Plan. The plan is computed once, with the prefix reserve of 8
//   slots (88 s) whatever Budgets.MaxSpeakers holds. Every request below,
//   the merge's included, is assembled by the run's assembler and so checked
//   against both budgets before upload. (I5)
// - Run.2 — Survey. Every chunk is sent once with kind "survey" and no
//   sample. Its labels are used to cut candidates and for nothing else.
//   (I3, I6)
// - Run.3 — Candidates. Each material label of each survey answer
//   (Materiality over the chunk's core speech) gives one SurveyCandidate:
//   the first clip SampleExtractor.Candidates ranks for it, with the
//   label's survey speech. A material label with no clip of 3–10 s gives
//   none.
// - Run.4 — Merge. Merger.Merge is called once with every candidate. Its
//   requests go through the run's request function: assembled, budget
//   checked, cached, counted and sent again like any other. The registry it
//   returns is the run's registry. (I4)
// - Run.5 — More than 8 voices. When the merge found more than 8, the run
//   goes on with the 8 it kept, and the board's finishing lines begin with
//   exactly
//   "found 10 voices; more than 8 usually means one person was not recognised across chunks"
//   with the count the merge found in place of 10. (Q2)
// - Run.6 — Labelling pass. Every chunk is sent once with kind "labelling"
//   and one clip per registry speaker, the speaker's Sample, ordered by the
//   clips' source start. With an empty registry the request equals the
//   chunk's survey request and is answered by the run's cache: the
//   provider is asked once for that audio.
// - Run.7 — Join. After every labelling request has answered, and once
//   only: two speakers agree when at least 2 labelling requests settle
//   both their clips (one label holding at least 80 % of each clip's
//   labelled speech) and every request that settles both gives both the
//   same label. Speakers are taken in registry order; a speaker joins the
//   earliest earlier speaker it agrees with, unless some request settled it
//   on a different label than a speaker already joined there. Two speakers
//   whose survey candidates of one chunk held labels of their own do not
//   agree when that chunk's labelling answer holds a material label no
//   registry clip settled on. The joined
//   speaker holds the clips of both, ranked as in Merge.5, and the registry
//   is numbered again by earliest clip. The registry changes in no other
//   way and at no other time. (I2, I4)
// - Run.8 — Mapping. In a labelling or retry answer, a label names a
//   speaker when the registry clips settled on it belong to one speaker
//   after the join. A label on which clips of two speakers settled names
//   nobody. Mapping is by LabelMapper's rules otherwise. (I2)
// - Run.9 — Retry. A chunk whose labelling answer leaves a material label
//   naming nobody is sent once more, with kind "retry" and everything fixed
//   at once: the registry clips in the opposite order when clips of two
//   speakers settled on one label; the next-ranked clip, not yet sent for
//   this chunk, of every speaker none of whose clips settled on a label of
//   its own (no clip settled, or its clip shared a label with another
//   speaker's); and, after
//   the registry clips, one candidate clip for each material label no
//   registry clip settled on, cut from that label's speech in the labelling
//   answer, most speech first, as many as keep the prefix at 8 clips. A
//   retry that would equal the chunk's labelling request is not sent. A
//   clip swapped in is used in that request only. (I4, I8)
// - Run.10 — Retry result. Speech under a label only a candidate clip
//   settled on names nobody: no speaker is added after the merge. The
//   retry's answer replaces the labelling answer when it maps more speech
//   to registry speakers; otherwise the labelling answer stays. (I4)
// - Run.10a — Retry failed. A retry request that fails for any reason —
//   not answered after the schedule, refused for the key or the account, a
//   failed assembly or budget check — does not fail the run: the chunk
//   keeps its labelling answer, the speech the retry would have fixed
//   stays "unknown-N", and the board's finishing lines hold one line for
//   it,
//   "chunk 3 (35:00–52:30) second try failed: <cause>; its labelling result is kept".
//   The other chunks' retries go on. The one exception is an ended
//   context, which ends the run (Run.16). (I7)
// - Run.11 — Stitch. The output is each chunk's kept answer in source
//   order, the prefix removed, labels replaced by "speaker-N", and speech
//   naming nobody kept as "unknown-N", one name per unmapped label of a
//   chunk, as today. Every output segment is read from the answer to a
//   labelling or retry request, and no label of a survey or merge answer
//   is in the output. With an empty registry (Run.6) the labelling answer
//   is the cache's answer to the identical request: it is read as the
//   labelling answer, names nobody, and all its speech is "unknown-N".
//   (I6, I7)
// - Run.12 — Labels never fail a run. Unsettled clips, ambiguity, a voice
//   matching nobody, more than 8 voices and an empty registry all end in a
//   nil error. (I7)
// - Run.13 — Parallelism. With Parallelism zero or less, every request of
//   a stage is in flight at once: all surveys, the two requests of a link,
//   all labelling requests, all retries. With Parallelism n, never more
//   than n requests are in flight, the merge's included. (Q1)
// - Run.14 — Length. A run sends at most 3 requests per chunk (survey,
//   labelling, retry) and 2 per chunk after the first (its merge link). No
//   stage runs twice and none depends on an answer to end. A request sent
//   again by the schedule is the same request. (I8)
// - Run.15 — Sent again. Every request kind is sent again on c.Retry, as
//   calibrated requests are today: a rate limit, a provider failure, no
//   answer. (Q1)
// - Run.16 — Real failures. A failed plan; a survey, merge or labelling
//   request that fails, whether at assembly or a budget check, refused for
//   the key or the account, or not answered after the schedule; and an
//   ended context at any stage, the retry included, fail the run: Run
//   returns the error wrapping the cause, in-flight requests are
//   cancelled, the assembler is closed, and the board is left readable
//   with its footer. Without a survey, a merge or a labelling answer there
//   is no output to keep. A failed retry request is not among them
//   (Run.10a).
// - Run.17 — Unattended. The run reads no voice library, asks nothing and
//   waits for nobody. (I1)
// - Run.18 — Totals. Requests counts uploads (cache hits are CacheHits),
//   Retries counts retry requests sent, Unknown is the unmapped speech of
//   the output; Resent, Uploaded and Cores are as today.
func (c *Coordinator) Run(ctx context.Context, filePath string) ([]Segment, error)
```

### `parallelism`

```text
transcribe.parallelism   integer; 0, absent or negative: unset
-parallelism <n>         "Cap the parallel transcription requests of a split audio file."

Default.Transcribe.Parallelism is 0 and NewSplitter sets none.

- Par.1 — Unset, the labelling path sends every chunk at once (Run.13). (Q1)
- Par.2 — Unset, the plain split path runs 3 chunks at once, as today.
- Par.3 — A positive value caps both paths at that many requests in flight.
- Par.4 — An audioConfig.json that holds a positive parallelism keeps it: it is a set value.
```

### The `calibration` event log

One line per event at `audio=debug`, `calibration <event> key=value …`, as today. A chunk's lines carry
`chunk=N attempt=M`; a merge request's lines carry `link=N`, N being the number of the chunk whose
candidates the link holds. No record holds transcript text.

| Event | Keys | Written |
| ----- | ---- | ------- |
| `plan` | `source= max_request_seconds= max_request_bytes= voices=8 prefix_reserve=88.0 core_target= silence_window= silences= cores= parallelism=all\|<n> requests_max=<3·cores + 2·(cores−1)>` | once |
| `plan-core` | unchanged | per core |
| `request` | unchanged keys; `kind=survey\|merge-forward\|merge-reversed\|labelling\|retry`; a merge request has `core=0.0-0.0` | per request, cache hits included |
| `request-retry` | unchanged | per resend |
| `response`, `sample`, `label` | unchanged | per survey, labelling and retry answer |
| `candidate` | a chunk line (its `chunk=N attempt=1` is the only `chunk=`): `label= id=c<chunk>-<label> clip= speech= clearance= label_speech=`, or `label= id=- reason=no-run-of-sample-length` | per material survey label |
| `merge` | per clip of a link: `link= clip=<id> source=<start-end> forward=<label>:<share> reversed=<label>:<share> outcome=held\|merged\|joined\|new\|unsettled voice=<voice-N\|->` | after the merge, from `MergeReport` |
| `merge` | per voice found: `voice=voice-N speaker=<speaker-N\|-> clips=<ids joined by +> sample=<start-end> survey_speech=` | after the merge |
| `merge` | `state=done links= found= kept=` | once |
| `join` | per join: `from=<speaker> into=<speaker> shared=<requests> apart=0`; per pair the survey check of Run.7 kept apart, directly or through a speaker already joined: `refused a=<speaker> b=<speaker> shared=<requests> chunks=<1-based chunks>`; then `state=done joined=<n> speakers=<n>` | once, after the labelling pass |
| `registry` | per speaker after the join: `id=speaker-N was=<ids before the join, joined by +> clips=<n> sample=<start-end>` | only when a join was made |
| `retry` | `state=start reversed=true\|false swapped=<speaker@old>new,…\|-> candidates=<label:clip,…\|-> pending=<label:speech,…>`; then `outcome=accepted\|kept reason=no-gain` with `mapped= unknown= material_unmapped=` as `before>after`; or `outcome=kept reason=request-failed cause="<one phrase>"`; or `state=skipped reason=nothing-to-change pending=` | per chunk with a material label naming nobody |
| `accepted`, `unmapped` | `accepted`: `requests= resent= retries= mapped= unknown= material_unmapped= map=` (`discovery_attempts` is gone); `unmapped` unchanged | once per chunk, when its answer is final |
| `unknown`, `stitched` | unchanged | at the stitch |

The events `recovery`, `discovery` and `revisit`, the `registry` records with `op=`, and the request kinds
`bootstrap`, `bootstrap-candidates`, `forward`, `reversed-retry`, `discovery`, `replacement-retry` and
`revisit` are no longer written.

### The board

Columns, marks, the JSON progress records and the status writer are today's. The words change (Q4 (a)):

| Element | Text | Shown when |
| ------- | ---- | ---------- |
| Header | `speaker labelling  <file>  <n> chunks × <core target>  ≤<3n + 2(n−1)> requests` | always |
| Phase | `finding voices · <n> chunks` | the survey |
| Phase | `matching voices · <c> clips`, c the candidates | the merge |
| Phase | `labelling · <n> chunks · <s> speakers`, s the registry after the merge | the labelling pass |
| Phase | `second try · <k> chunks with unmatched voices` | the retry, only when k > 0 |
| Phase | `stitching` | the stitch |
| Phase | `done · <segments> segments · <elapsed> elapsed` | success, as today |
| Row state, active | `finding voices · <request length>`, `labelling · <request length>`, `second try · <request length>`; a resend shows today's `retry 2/4 in 10s · 429` and `<state> · try 3` | a request of the chunk is in flight |
| Row state, waiting | `<v> voices found`, v the chunk's material survey labels | between its survey and its labelling request |
| Row state, done | `labelled · <s> speakers` with ✓, or `<u> unmatched · <s> speakers` with ✗, u the material labels naming nobody, s the registry after the join | the chunk's answer is final |
| Row state, failed | `request failed`, as today | a request of the chunk failed |
| `req` cell | `<requests of the chunk>/3` | from its first request |
| Live footer | `<estimate> · <elapsed> elapsed · requests <sent>/≤<3n + 2(n−1)> · resent <r> · in flight <f> · speakers <s> · unknown <u> · uploaded <d>`; `in flight` counts chunk rows with a request in flight, as today, so a merge request, which has no row, is not in it; the estimate's typical case is no retry, its worst case a retry for every chunk | while running |
| Final footer | `requests <sent>/≤<3n + 2(n−1)> · resent <r> · retries <k> · cache hits <h> · uploaded <d> · speakers <s> · unknown <u> · <elapsed> elapsed` | at the end, failures included |
| Row state, retry failed | the done state of its labelling answer (`<u> unmatched · <s> speakers` with ✗), not `request failed` | its retry request failed (Run.10a) |
| Finishing lines | the Run.5 line when more than 8 voices were found, then one Run.10a line per failed retry request, then today's unknown-speech summary | at the end |

### `architecture/audio.md`

The section `## Adaptive Speaker Calibration` keeps its heading and is rewritten to the survey path:
"Speaker sample registry" (built by the merge, changed once by the join), "Calibration prefix and mapping"
(the labelling pass, the join, the retry), "Discovery" replaced by "Survey and merge", "Stitching and
failure policy", "Investigating a run" (the event table above), "Required tests". Also: "Progress" and
"Request retries" (no per-chunk limit to spend; the kinds named), and the `parallelism` rows of
"Configuration", and the Key Files row that names "bootstrap and discovery" (it names `merge.go` instead). The intro cites this worklog and H1's measurement beside `asc`. The rows of
`max-speakers` stay until phase 3 and say the calibrated path no longer reads it.

Consumes:

- `Merger.Merge`, `ChainMerger`, `SurveyCandidate`, `MergeReport`, `MergedVoice`, `MergeLink`, `LinkClip`,
  `MergeReport.Found`, `CandidateError`, `maxVoices`, `RequestMergeForward`, `RequestMergeReversed`, `Speaker.Clips`,
  `Assemble.1` (phase 1)
- `ChunkPlanner.Plan`, `Plan`, `Budgets` (repository, unchanged)
- `RequestAssembler`, `AssembleRequest`, `RequestManifest`, `SampleClip`, `NewAssembler` (repository)
- `SampleExtractor.Candidates`, `Materiality`, `better` (repository, unchanged)
- `LabelMapper.Map`, `Mapping` (`MaterialUnmapped`, `MappedSpeech`, `UnknownSpeech`, `Failed`,
  `Ambiguous`, `resolve`), `SampleStatus` (repository, unchanged)
- `RequestFunc`, `requestCache`, `RequestTranscriber`, `FileTranscriber`, `resend.Do`, `resend.Schedule`
  (repository, unchanged; the request retries of Q1 landed on 2026-11-20 as `internal/resend`,
  `Coordinator.Retry` and `Splitter.Retry`)
- `Snapshot`, `Speaker` (repository)
- The revisit, in the working tree and not committed at plan time (branch
  `feat-keep-recurrent-voices-for-auto-labels`, on `0ca840b`): `Coordinator.revisit`,
  `Coordinator.revisitChunk`, `Coordinator.eachChunk`, `revisitOutcome` (`revisitAccepted`, `revisitKept`,
  `revisitSkipped`), `reasonNoGain`, `Discovery.Revisit`, `RequestRevisit`, `chunkResult.speakers`
  (`internal/audio/coordinator.go`, `internal/audio/discovery.go`, `internal/audio/calibration_log.go`),
  and its tests in `internal/audio/revisit_test.go`. The retry is the revisit made unconditional on the
  registry's growth: same scheduling, same "more mapped speech" rule, same record shape. One difference:
  a retry request that fails keeps the labelling answer (Run.10a), where a failed revisit
  request fails the run.
- `unknown-N` per voice, same state: `stitch`, `UnknownVoice`, `summarizeUnknown`, `logStitched`
  (`internal/audio/stitch.go`, `internal/audio/calibration_log.go`), unchanged
- `chunkBoard`, `newChunkBoard`, `etaModel` (`internal/audio/board.go`, `internal/audio/eta.go`;
  repository)
- Test doubles: `voiceProvider`, `manifestTranscriber`, `arithmeticAssembler`
  (`internal/audio/discovery_fakes_test.go`), `scriptedRunner` (`internal/audio/calibration_fakes_test.go`),
  `newCoordinator`, `calibrationLog`, `assertRecords`, `newCalibratedSplitter`, `recordedSchedule`
  (existing test helpers). Provider behaviour: the README's H1 result.

## Acceptance criteria

| #   | Criterion | Proves | Proof |
| --- | --------- | ------ | ----- |
| 1   | A recording whose first chunk holds two of six people gets six speakers in every chunk, with no unknown speech, in 2 requests per chunk plus 2 per link | Run.2–Run.4, Run.6, Run.11, the Goal | `TestSurveyPathLabelsEveryChunk` |
| 2   | Survey requests carry no clip and none of their segments is in the output | Run.2, I3, I6 | `TestSurveyNeverReachesTheOutput` |
| 3   | The plan is made once with an 88 s reserve whatever `max-speakers` says, and every request passes the assembler | Run.1, I5 | `TestPlanReservesEightSlots` |
| 3a  | No test outside the ones this phase lists changes its result because of the fixed reserve, the new header or the `parallelism` default | L2.10 | the two `grep` commands of L2.10, and `make qa` |
| 4   | Two speakers who share a label in every labelling request that settles both become one; one request apart, or one shared request only, joins nothing; nor does a chunk whose survey heard them under labels of their own and whose labelling answer leaves a material label naming nobody | Run.7, Run.8, I2, I4 | `TestJoinAfterLabelling` |
| 5   | A chunk with a material label naming nobody gets exactly one retry that reverses, swaps (the failed speaker's clip and both clips that shared a label) and adds candidates at once; the registry is the same after it | Run.9, I4, I8 | `TestRetryFixesEverythingAtOnce` |
| 6   | The retry's answer is kept only when it maps more speech; an identical retry is not sent | Run.9, Run.10 | `TestRetryResult` |
| 7   | A voice matching nobody after the retry is `unknown-N`, one name per voice, and the run succeeds | Run.10–Run.12, I4, I7 | `TestUnmatchedVoiceStaysUnknown` |
| 8   | Ten voices found: 8 labelled, the rest `unknown-N`, and stderr says exactly `found 10 voices; more than 8 usually means one person was not recognised across chunks` | Run.5, Run.12, Q2 | `TestMoreThanEightVoices` |
| 9   | Unset `parallelism` sends every chunk at once; a set value caps every stage, the merge's pair included; the plain path stays at 3 | Run.13, Par.1–Par.4, Q1 | `TestParallelism`, `TestParallelismDefault` |
| 10  | No script makes a run send more than 3 per chunk plus 2 per link | Run.14, I8 | `TestPathLengthIsFixed` |
| 11  | A rate-limited request of any kind is sent again and the run succeeds | Run.15, Q1 | `TestEveryRequestKindIsSentAgain` |
| 12  | A real failure fails the run with its cause and leaves nothing behind: the plan, a survey, merge or labelling request that fails for any reason (budget, key, account, no answer), a cancel at any stage | Run.16 | `TestRealFailureFailsTheRun` |
| 12a | A retry request that fails for any reason but a cancel does not fail the run: the chunk keeps its labelling answer, its unmatched speech is `unknown-N`, and stderr names the chunk and the cause in one line | Run.10a, I7 | `TestFailedRetryKeepsTheLabellingResult` |
| 13  | The event log has the records of the table and no transcript text | the event log | `TestCalibrationLogOfTheSurveyPath` |
| 14  | The board shows Q4 (a)'s words in order, the `≤` bound of I8 and the footers | the board, Q4 | `TestBoardOfTheSurveyPath` |
| 15  | The path through the `Splitter` with the real assembler over the fake runner gives the same labels, and a clips-only request runs no core extraction | Run.1, Run.4, I11 | `TestSurveyPathEndToEnd` |
| 16  | A recording with no material label uploads each chunk once and is all `unknown-N` | Run.6, Run.12, Merge.9 | `TestNothingToLabel` |
| 17  | The run reads no voice library and no setting selects another path | Run.17, I1, I9 | `TestSurveyPathIsTheOnlyPath` |
| 18  | `architecture/audio.md` describes the survey path and no longer the bootstrap, the discovery lock or the revisit | the document | `grep` commands under Test cases |
| 19  | The gate passes unedited; new code in `coordinator.go` is covered ≥ 90 % | I11 | `make qa`; `go vet ./...`; `go run github.com/mibk/dupl@latest -t 80 .` |

## Test cases

Every scenario runs `Coordinator.Run` with the real `ChainMerger`, `LabelMapper`, `SampleExtractor`,
planner and stitch wired in, over the scripted diarizer (`manifestTranscriber`) and the
`arithmeticAssembler`, unless it says otherwise. "The diarizer" below is the scripted one; its faults are
keyed by what a request holds (L1.3).

| Test | Level | Proves | Given | Then | Must not |
| ---- | ----- | ------ | ----- | ---- | -------- |
| `TestSurveyPathLabelsEveryChunk` | integration scenario | Run.2–Run.4, Run.6, Run.11, Run.18, I10 | A six-person meeting over 4 chunks with two of the six in chunk 1 (a variant of `sixVoiceMeeting`, whose chunk 1 holds three) | Six `speaker-N`, each one person in all 4 chunks, numbered by earliest clip; 0 s unknown; `Totals.Requests` 14 (4 + 6 + 4), `Retries` 0; timestamps in source time | Leave chunk 1's late voices unknown; send a retry |
| `TestSurveyNeverReachesTheOutput` | integration scenario | Run.2, I3, I6 | The diarizer writes into every segment's text what its request held, which is all a `RequestTranscriber` sees: `survey` (a core, no sample), `merge` (samples, no core) or `prefixed` (samples and a core); it gives each survey its own letters | Every output segment's text says `prefixed`; survey requests hold no sample; a survey label equal to a labelling label of another person changes nothing | Emit a survey or merge segment; compare labels of two requests |
| `TestPlanReservesEightSlots` | integration scenario | Run.1, I5 | `Budgets.MaxSpeakers` 3 and a 700 s budget; a second row whose registry has 8 speakers | One ffprobe and one silence pass; every core plus 88 s fits the budget; every request, merge links included, went through `Assemble` | Plan with 33 s; replan; upload a request the assembler did not check |
| `TestJoinAfterLabelling` | integration scenario | Run.7, Run.8, I2, I4 | Rows, each with the merge leaving one person as two speakers: (a) their clips share a label in all 4 labelling requests; (b) in 3, and one request settles them on different labels; (c) both settled in one request only; (d) three speakers where A agrees with B, B with C, and one request settles A and C apart; (e) as (a), and the survey of one chunk held a candidate of each under its own label, and that chunk's labelling answer holds a material label no clip settled on (the Retro of 2026-11-23); (f) as (e) with nothing left over in that chunk | (a) one speaker with both clips, numbered again without a gap, no retry, 0 s unknown, `join from= into=` and `registry` records. (b) no join; the 3 chunks get a retry. (c) no join. (d) B joins A, C stays. (e) no join, a `join refused` record; each chunk where either speaks gets a retry, and every voice is named. (f) one speaker, as (a); (g) as (f), with a material label no clip settled on in a chunk whose survey did not hear the pair: still one speaker; (h) the merge splits A, the split joins A, and a third speaker C shares A's label in every request while the survey of a chunk heard C apart from the split: C joins nothing, a `join refused` record names the split and C | Join on fewer than 2 requests; join speakers a request kept apart; join twice in a run; join two voices the survey heard apart where a voice is left naming nobody |
| `TestRetryFixesEverythingAtOnce` | integration scenario | Run.9, I4, I8 | One chunk whose labelling answer puts two speakers' clips on one label, mixes a third speaker's clip, and holds a material label no clip settled on; the other chunks are clean | That chunk gets 1 retry: the registry clips reversed, the third speaker's next-ranked clip in place (the two speakers on one label have no other clip here; `TestRetrySwapsBothClipsOnASharedLabel` and `TestRetryBuildsItsPrefix`'s shared-label row prove that both get their next clip when they have one), one candidate clip after them; the other chunks get none; after the run the registry's samples are the merge's | Send two retries; swap the clip in another chunk's request; exceed 8 clips |
| `TestRetryResult` | integration scenario | Run.9, Run.10 | Rows: the retry maps more speech; the retry maps the same; the retry maps less; the chunk's only material label naming nobody has no run of 3–10 s to cut a candidate from, no two speakers' clips share a label, and every speaker has a settled clip | Row 1: the retry's answer is the chunk's, `retry outcome=accepted`. Rows 2–3: the labelling answer stays, `outcome=kept reason=no-gain`. Row 4: Run.9 changes nothing, so no request, `retry state=skipped reason=nothing-to-change pending=<that label>`, and the label's speech is `unknown-N` | Keep an answer that maps less; upload an identical request |
| `TestUnmatchedVoiceStaysUnknown` | integration scenario | Run.10–Run.12, I4, I7 | A seventh person speaks 60 s in chunk 3 only and the survey of chunk 3 misses her (her speech under another label there), so the merge never hears her | Chunk 3 gets its retry with her candidate; the candidate holds its own label; her speech is one `unknown-N` in every run of hers; the registry has six speakers; nil error | Add a speaker; drop her speech; return an error; give her two names in one chunk |
| `TestMoreThanEightVoices` | integration scenario | Run.5, Run.12, Q2 | Rows: 10 people; 9 people; 8 people | 10: the status output holds the line `found 10 voices; more than 8 usually means one person was not recognised across chunks`, 8 speakers, the two with the least survey speech are `unknown-N`, nil error. 9: `found 9 voices; …`. 8: no such line | Fail the run; label 10; print the line for 8 |
| `TestParallelism` | integration scenario | Run.13, Q1 | 4 chunks; the diarizer holds requests and counts those in flight. Rows: `Parallelism` 0; 2; 1 | 0: 4 surveys in flight together, then 2 per link, then 4 labelling requests. 2: never more than 2, in any stage. 1: never more than 1, the link's pair one after the other | Exceed a set cap; hold back a chunk when unset |
| `TestParallelismDefault` | unit | Par.1–Par.4 | `createSplitter` with the default configuration; with `parallelism` 5 in the file; with `-parallelism 2` | Default: `Splitter.Parallelism` 0, the plain path's workers 3, the coordinator uncapped. 5 and 2: both paths capped at that | Change the plain path's default |
| `TestPathLengthIsFixed` | integration scenario | Run.14, I8 | The worst script: every chunk leaves a material label naming nobody in both its labelling and retry answers; 4 chunks. A second row: a clean run | Worst: 4 + 6 + 4 + 4 = 18 requests = 3·4 + 2·3. Clean: 14. `req` cells never above `3/3` | Send a fourth request for a chunk; repeat a stage |
| `TestEveryRequestKindIsSentAgain` | integration scenario | Run.15, Q1 | Rows per kind (`survey`, `merge-forward`, `labelling`, `retry`): its first send is answered 429, with `recordedSchedule` | The run succeeds with the same labels; `Totals.Resent` 1; a `request-retry` record with that kind; `Totals.Requests` unchanged by the resend | Count a resend as a request of the chunk; sleep |
| `TestRealFailureFailsTheRun` | integration scenario | Run.16 | Rows: ffprobe fails; a survey request fails with a transport error after the schedule; a merge request does; a labelling request does; a labelling request is refused with a rejected key (401); the assembler refuses a labelling request over the byte budget; the context is cancelled during the labelling pass; the context is cancelled while a retry request is in flight | Each: `Run` returns nil segments and an error whose cause is reachable with `errors.Is`; requests in flight saw their context end; the assembler is closed; the status output ends with the footer and a `request failed` row where a request failed | Return a partial transcript; leave a request file; hang |
| `TestFailedRetryKeepsTheLabellingResult` | integration scenario | Run.10a, Run.12, Run.18, I7 | Two chunks need a retry; chunk 2's is answered. Rows for chunk 3's retry: answered 503 on every send of `recordedSchedule`; no answer at all (transport error); refused with a rejected key (401, never sent again); refused by the assembler's budget check | Each row: nil error; the output's chunk 3 equals its labelling answer, with the unmatched voice as one `unknown-N`; chunk 2 has its retry's answer; the status output holds exactly one line `chunk 3 (…) second try failed: <cause>; its labelling result is kept`, the cause being `provider unavailable (503)`, the transport error, the 401 and the budget error in turn; chunk 3's row reads `<u> unmatched · <s> speakers`; the record `retry chunk=3 … outcome=kept reason=request-failed cause="…"`; `Totals.Retries` counts the retry requests uploaded (1 in the budget row, 2 in the others) | Return an error; drop chunk 3's speech; cancel chunk 2's retry; mark the row `request failed`; sleep; swallow a cancelled context (that row is `TestRealFailureFailsTheRun`'s) |
| `TestCalibrationLogOfTheSurveyPath` | integration scenario | the event log | The `TestJoinAfterLabelling` (a) script and the `TestRetryFixesEverythingAtOnce` script at `audio=debug` | `assertRecords` finds one record per row of the event table with its keys (`plan … voices=8 … parallelism=all requests_max=18`, `request … kind=survey`, `request link=2 kind=merge-forward core=0\.0-0\.0`, `candidate chunk=1 attempt=1 label=\w id=c1-`, `merge link=2 clip=`, `merge voice=voice-1 speaker=speaker-1`, `merge state=done`, `join from=`, `join state=done`, `registry id=speaker-1 was=`, `retry state=start reversed=true`, `retry outcome=accepted`, `accepted … retries=1`, `stitched`); no line holds a segment's text; the output equals a run without the log | Write `bootstrap`, `discovery`, `revisit` or `op=`; change the result by logging |
| `TestBoardOfTheSurveyPath` | integration scenario | the board, Q4 | The `TestRetryFixesEverythingAtOnce` script with a status buffer; a second run under `-json` with no status writer | Header `speaker labelling  meeting.wav  4 chunks × … ≤18 requests`; the phases in order `finding voices · 4 chunks`, `matching voices · <c> clips`, `labelling · 4 chunks · <s> speakers`, `second try · 1 chunks with unmatched voices`, `stitching`, `done ·`; row states `finding voices`, `<v> voices found`, `labelling`, `second try`, `labelled · <s> speakers`; `req` `3/3` for the retried chunk and `2/3` for the others; the final footer's fields in order. Under `-json`: the same phases as `progress` records | Show `bootstrap`, `calibrated`, `discovery`, `revisit`, `workers` or `reserve`; print `second try` when no chunk needs one |
| `TestSurveyPathEndToEnd` | integration scenario | Run.1, Run.4, I11 | `newCalibratedSplitter`: the `Splitter`, the real `AudioAssembler` and planner over `scriptedRunner`, a model with anchored labels, 4 chunks, 4 people | One `speaker-N` per person across chunks; the fake runner saw no `atrim` of length 0 and one encode per uploaded request; the non-diarized and under-the-cap routes still take their own paths (`TestCalibrationPreservesNonDiarizedAndSubLimitPaths` unedited) | Need ffmpeg; touch the plain or request-local route |
| `TestNothingToLabel` | integration scenario | Run.6, Run.11, Run.12, Merge.9, I6 | Every chunk's speech is under labels below the material threshold | No merge request; each labelling request is a cache hit (`Totals.Requests` 4, `CacheHits` 4); all speech `unknown-N`, no survey label in the output; nil error | Upload a chunk twice; fail; emit a survey label |
| `TestSurveyPathIsTheOnlyPath` | integration scenario | Run.17, I1, I9 | A voice library with enrolled people in the test home; `MaxRequestsPerChunk` 1; `Budgets.MaxSpeakers` 3 | The requests and the output equal a run without any of the three; no file of the library is opened by the coordinator (the naming step is not part of `Run`) | Read the library; take another path for any setting |
| `grep -nE 'bootstrap\|discovery lock\|[Rr]evisit\|max-requests-per-chunk\|uncalibrated pass' architecture/audio.md` | command | the document | The repository after the phase | No output | Leave the old path described as current |
| `grep -cE 'survey\|merge link\|labelling pass\|second try\|found 10 voices' architecture/audio.md` | command | the document | The repository after the phase | A count of at least 5 | — |
| `go test ./internal/audio -coverprofile=c.out && go tool cover -func=c.out \| grep coordinator.go` | command | criterion 19 | The repository after the phase | Every function this phase wrote in `coordinator.go` at 90 % or more on average; the figure written in Implementation notes | Leave `c.out` in the tree |
| `grep -rn 'smallBudgetConf\|calibrated diarization' --include='*_test.go' .` and `grep -rnE 'MaxRequestSeconds = \|MaxRequestDuration = ' --include='*_test.go' internal/audio` | command | criterion 3a, L2.10 | The repository after the phase | The users L2.10 lists and no other; every test that reaches the planner through `Run` has a budget above 88 s | Edit `smallBudgetConf` |
| `make qa`, `go vet ./...`, `go run github.com/mibk/dupl@latest -t 80 .` | command | I11 | The repository after the phase | Exit 0, twice | Edit the gate, add a sleep, a skip or a real ffmpeg call |

Existing tests that drive the old path through `Run` or the `Splitter` are rewritten against the clauses
above or deleted with the behaviour they proved: in `coordinator_test.go` (`TestLateSpeakerDoesNotInvalidateChunks`,
`TestFrozenSnapshotAllowsBoundedParallelism`, `TestBestEffortRendersUnknown`, `TestRequestAccountingCountsRetries`,
`TestSecondSampleFailureDoesNotRetry`, `TestAmbiguityRetriesWithReversedPrefix`, `TestBudgetStopsPerChunk`,
`TestBudgetErrorsUnderBestEffort`, `TestCoordinatorReportsTotalsOnStderr`, the live-board tests, `TestCoordinatorLiveErrorExitKeepsFooter` among them), all of
`revisit_test.go`, `calibration_log_test.go` (all but `TestNamingLogSaysWhatBecameOfEachSpeaker`),
`calibration_integration_test.go` (`TestCalibrationEndToEndWithPermutedLabels`,
`TestCalibrationEndToEndDiscoversLateSpeakers`), `request_retry_test.go` (the calibrated rows),
`eta_test.go` and `board_json_test.go` where they name old states. `TestStitchRemovesPrefix`,
`TestStitchRestoresSourceTimestamps`, `TestCoordinatorPropagatesTranscriberError` and
`TestCoordinatorCancelsAndCleansUp` keep their assertions (the last with `Parallelism` 1, since unset now starts every survey at once).

The fixed reserve of Run.1 also reaches three tests of other files, adapted in this phase (L2.10):
`TestMaxRequestBytesAppliesToBothPaths` (`calibration_integration_test.go`), whose `smallBudgetConf` budget of
80 s the planner now refuses, and `TestSplitter_AnchoredDiarizationCalibrates` and
`TestSplitter_DiarizeRoutesToCalibration`, which use the same small budget and look for the old header
`▸ calibrated diarization`. `smallBudgetConf` itself is not touched: each of the three sets
`conf.Transcribe.MaxRequestSeconds` on its own copy of the configuration to a budget that holds the 88 s
reserve (200 s gives two chunks of the 180 s source), and the two splitter tests look for
`▸ speaker labelling`. None of the three asserts a chunk count, and their other assertions are unchanged.
`TestSurveyPathEndToEnd`, which replaces the two `TestCalibrationEndToEnd…` tests, sets its budget the same
way. `TestCalibrationToolBridgeUsesSameEngine` reads `smallBudgetConf` through `createSplitter` only, never
plans, and passes unedited with its 80 s assertion. Tests of `Discovery` and `SpeakerRegistry`
themselves (`discovery_test.go`, `registry_test.go`) stay until phase 3.

## Error management

| Failure | Expected outcome | Test |
| ------- | ---------------- | ---- |
| ffprobe or the silence pass fails | `Run` returns the planner's error; no request sent | `TestRealFailureFailsTheRun` |
| A survey, merge or labelling request is over a budget at assembly | `Run` returns `chunk N: <assembler error>` (a merge request: the merger's `merge link for chunk N: …`); nothing uploaded for it | `TestRealFailureFailsTheRun` |
| A survey or labelling request fails: not answered after the schedule, or refused for the key or the account | `Run` returns `chunk N request M (…): <cause>`; the row reads `request failed`; other requests cancelled | `TestRealFailureFailsTheRun` |
| A retry request fails for any reason but an ended context: not answered after the schedule, refused for the key or the account, refused at assembly or a budget check | Not a failure: nil error; the chunk keeps its labelling answer; finishing line `chunk N (<span>) second try failed: <cause>; its labelling result is kept`; record `retry … outcome=kept reason=request-failed cause=…`; other retries go on | `TestFailedRetryKeepsTheLabellingResult` |
| A merge request fails: not answered after the schedule, or refused for the key or the account | `Run` returns the merger's error (`merge link for chunk N: <cause>`); the board's phase stays `matching voices` and the footer is written | `TestRealFailureFailsTheRun` |
| A rate limit, a provider failure or no answer, within the schedule | Not a failure: sent again, counted in `resent` | `TestEveryRequestKindIsSentAgain` |
| The context ends, at any stage, a retry in flight included | `Run` returns the context's error; the assembler is closed | `TestRealFailureFailsTheRun` |
| A clip unsettled, two speakers on one label, a voice matching nobody, after the retry | Not a failure: the speech is `unknown-N` | `TestUnmatchedVoiceStaysUnknown`, `TestRetryResult` |
| More than 8 voices | Not a failure: the Run.5 line, 8 labelled | `TestMoreThanEightVoices` |
| No material label in the recording | Not a failure: everything `unknown-N` | `TestNothingToLabel` |
| The merger returns a `*CandidateError` | Cannot happen from Run.3's candidates; if it does, `Run` returns it (stub merger) | `TestRealFailureFailsTheRun` (the merge row uses a stub `Merger` that returns an error) |

## Golden diff

None. No existing golden records the calibrated route: `e2e/testdata/diagnostics/*.golden` hold no
`calibration` record, no board and no `speaker-N` (`help.golden` line 16, the `audio` summary, does not
change). The run is pinned by the scenarios above. `go test ./e2e -run '^TestDiagnosticOutputGoldens$'`
passes without `-update-diagnostic-goldens`.

## Removes

None.

## Local facts

| ID   | Fact | Decided by |
| ---- | ---- | ---------- |
| L2.1 | "A chunk whose labelling pass went wrong" (README, Glossary) is a chunk whose labelling answer, after the join, leaves a material label naming nobody. A mixed clip of a speaker who is silent in that chunk costs nothing in the output and triggers no request. | this phase, from the revisit's trigger |
| L2.2 | A retry's prefix never exceeds 8 clips, the reserve the plan made (I5): the registry clips first, then candidates by speech until 8. With 8 speakers a retry carries no candidate. | I5 |
| L2.3 | After a join the registry is numbered again by I10's rule, so the output has `speaker-1 … speaker-N` without a gap. The labelling requests' records carry the IDs before the join; the `join` and `registry` records give the mapping. | I10 |
| L2.4 | A retry request that fails for any reason — not answered once the resends are spent, refused for the key or the account, a failed assembly or budget check — does not fail the run: the labelling answer is kept and stderr says so in one line (Run.10a). Only an ended context ends the run during the retry. This departs from the imported revisit, whose failed request fails the run. A survey, merge or labelling request that fails still fails the run, for any cause. | Orchestrator, 2026-11-22, on Lorentz's rule that no run fails over labels (I7): the retry only improves labels, and an account running out of credit is likeliest at the end of a long run |
| L2.5 | The labelling prefix is ordered by the clips' source start, the order H1's labelling pass used (`step_label`); the retry's reversed order is its exact opposite, candidates last in both. | H1 |
| L2.6 | From this phase the calibrated path reads neither `max-speakers` nor `MaxRequestsPerChunk`; both are inert until phase 3 removes them. | I5, I8, Q2 |
| L2.7 | The gate's staticcheck refuses an unexported member with no caller. The rewrite of `coordinator.go` therefore deletes, in this phase, the unexported members it orphans (`bootstrap`, `transcribeChunk`, `revisit`, `revisitChunk`, `limit`, `requestPurpose`, `defaultMaxRequestsPerChunk`, `revisitOutcome` and its constants, `reasonRequestLimit`, `chunkResult.speakers`, `Mapping.registrySamples`, and what else staticcheck names). Exported members and units that still build with their own tests (`Discovery`, `SpeakerRegistry`, `RequestLimitError`, the old request kinds, `max-speakers`) are phase 3's. | the gate |
| L2.8 | The estimate counts, per unfinished chunk, the requests left of survey and labelling, plus the merge's 2 per link at the measured rate; the worst case adds one retry per chunk. `eta.go`'s names and comments speak of stages, not of discovery (phase 3's grep). | this phase |
| L2.10 | The fixed 88 s reserve refuses a seconds budget at or below it. Checked by grep over every `_test.go` on 2026-11-22 (V2-01). **`smallBudgetConf`** (80 s, left untouched) has six users: `TestCalibrationEndToEndWithPermutedLabels` and `TestCalibrationEndToEndDiscoversLateSpeakers` (replaced by `TestSurveyPathEndToEnd`, own budget), `TestMaxRequestBytesAppliesToBothPaths`, `TestSplitter_AnchoredDiarizationCalibrates` (`split_speakers_test.go:204`) and `TestSplitter_DiarizeRoutesToCalibration` (`split_test.go:301`) (each raises the budget on its own copy), and `TestCalibrationToolBridgeUsesSameEngine` (never plans; unedited). **Other budgets:** `testBudgets` (700 s) and `defaultBudgets` (1,400 s) hold the reserve; the 60 s row of `plan_test.go:143`, the 30 s row of `assemble_test.go:135`, the namer's 62 s and 20 s rows and `newDurationSplitter` call the planner, the assembler, the namer or the plain path directly, not `Run`, so this phase's pin does not reach them. **The header:** `▸ calibrated diarization` is asserted through `Run` only in the tests this phase lists (`coordinator_test.go:393`, `:530`, `calibration_integration_test.go:99`, the two splitter tests); `board_test.go`, `board_json_test.go:106` and `internal/board/board_test.go` pass it as a literal title to the board and do not move. **`parallelism`:** no test asserts the default 3 (`cmd_test.go`, `create_querier_test.go` and the e2e files set a value; `TestCalibrationToolBridgeUsesSameEngine` asserts the 1 its fixture sets), and the plain path's workers stay 3. | Run.1, I5; V1-02, V2-01 |
| L2.9 | A stub `Merger` is used only for the failure rows of `TestRealFailureFailsTheRun`; every other scenario wires `ChainMerger`. | worklog-plan, composition failure clauses |
| L2.11 | The join (Run.7) treats a segment whose speaker is empty as phase 1's L1.7 does: it is not a label, settles no clip and joins no two speakers. | I2, phase 1 L1.7 (review R1-04) |

## Human required

None.

## Implementation notes

| Row | Date | What | Gates |
| --- | ---- | ---- | ----- |
| fix-start | 2026-11-22 | V1-02, V1-03 | not run: documents only, no code touched |
| fix-end | 2026-11-22 | V1-02, V1-03 closed in this file | not run: documents only, no code touched |
| fix-start | 2026-11-22 | V2-01 | not run: documents only, no code touched |
| fix-end | 2026-11-22 | V2-01 closed in this file (and phase 3's `transcribe.max-speakers` row kept in step) | not run: documents only, no code touched |
| fix-start | 2026-11-22 | R2-01, R2-03, R2-06–R2-13 (review 2) | `make qa` exit 0, 108 packages ok (`internal/audio` 93.4 %) · `go vet ./...` clean · dupl 49 groups |
| fix-end | 2026-11-22 | R2-01, R2-03, R2-06–R2-13 closed (below); R2-02, R2-04, R2-05 left to plan and the maintainer | twice with `GOFLAGS=-shuffle=on`: `make qa` exit 0, 108 packages ok (`internal/audio` 93.4 %) · `go vet ./...` clean · dupl 49 groups |
| replan | 2026-11-23 | Run.7 (the join's survey check) and Run.9 (both clips that shared a label are swapped), criteria 4–5, `TestJoinAfterLabelling` rows (e)–(f), `TestRetryFixesEverythingAtOnce`; after H2 run 2 (README, the Retro in Penelope) | not run: documents only |

| Run   | Commit      | Command                                                                                     | Result |
| ----- | ----------- | ------------------------------------------------------------------------------------------- | ------ |
| start | 0ca840b+wip | `make qa` · `go vet ./...` · `go run github.com/mibk/dupl@latest -t 80 .` (API keys unset)  | qa exit 0, 108 packages ok, 0 failed (`internal/audio` 93.8 %) · vet clean · dupl 49 clone groups |
| end   | 0ca840b+wip | the same three, `GOFLAGS=-shuffle=on` (fresh random test order)                             | qa exit 0, 108 packages ok, 0 failed (`internal/audio` 93.5 %) · vet clean · dupl 49 clone groups (none in the new files) |
| end   | 0ca840b+wip | the same three, `GOFLAGS=-shuffle=on` (fresh random test order)                             | qa exit 0, 108 packages ok, 0 failed (`internal/audio` 93.5 %) · vet clean · dupl 49 clone groups (none in the new files) |

Criterion 19: `go test ./internal/audio -coverprofile=<scratch>/c.out` → the 35 functions this phase wrote in
`coordinator.go` (all but `FileTranscriber.TranscribeRequest`, `RequestLimitError.Error`, `Totals`, `IsRequestLimit`,
which are today's) average **99.5 %**; their statements **99.2 %** (509 of 513). No `c.out` in the tree.
Criterion 3a: the two L2.10 greps list only `smallBudgetConf`'s users L2.10 names (`TestSurveyPathEndToEnd` replacing
the two end-to-end tests, own budget 134 s; `TestMaxRequestBytesAppliesToBothPaths` and the two splitter tests, own
budget 200 s; `TestCalibrationToolBridgeUsesSameEngine`, unedited) and the board literals that do not move; every test
that plans through `Run` has a budget above 88 s. Criterion 18: the `bootstrap|discovery lock|[Rr]evisit|…` grep
prints nothing; the `survey|merge link|…` count is 42. Golden diff: none (`TestDiagnosticOutputGoldens` passes in
`make qa`). Non-test lines in `internal/audio/` after the phase: **11,085** (baseline 9,942; 10,508 after phase 1).

What landed:

- `internal/audio/coordinator.go`: the Interface block (kinds, the changed fields, `Merger`, `Run` with Run.1–18) and
  the survey path: `surveyAll`, `candidates`, `merge` (the run's request function for the links, `mergeRequest`,
  records `link=N`), `labelAll`, `joinVoices` (Run.7, reusing `voiceChain.registry` for the renumbering),
  `answer.mapping` (Run.8: `LabelMapper.Map` over a core-only manifest, then `resolve` per label that names one
  speaker), `planRetry`/`retryChunk` (Run.9–10a), `finish` (Run.5 and Run.10a lines, then the unknown summary).
  `Parallelism` is a semaphore in `request` (nil when unset) plus `min(cap, chunks)` workers per stage.
- `merge.go`: `labelledSamples` and `settled`, shared by the merger and the join (L1.7, L2.11); behaviour unchanged.
- `calibration_log.go`: `plan` (`voices=8 … parallelism= requests_max=`), `accepted` without `discovery_attempts`,
  `linkLog`, `logMerge`, `logJoin` (`join`, `registry`). `eta.go`: stages and links (L2.8). `querier.go`, `split.go`,
  `cmd.go`: `parallelism` unset by default (Par.1–4). `architecture/audio.md`: the sections the phase names.
- Deleted as orphaned (L2.7; staticcheck clean): `defaultMaxRequestsPerChunk`, `chunkResult`, `revisitOutcome` and its
  constants, `reasonRequestLimit`, `revisit`, `revisitChunk`, `workers`, `limit`, `requestPurpose`, `sampleIDs`,
  `bootstrap`, `transcribeChunk`, `reversed`, `Mapping.registrySamples`, `verdictText` (listed in phase 3's last
  Removes row; staticcheck named it here).
- Tests: `survey_test.go` (every Test cases row but the two below, plus `TestRetrySwapsAJoinedSpeakersFirstFailedClip`),
  `TestCalibrationLogOfTheSurveyPath` (`calibration_log_test.go`), `TestSurveyPathEndToEnd`
  (`calibration_integration_test.go`), `TestEtaEstimateCountsStagesAndLinks`; the scripted diarizer `chunkDiarizer`
  (`survey_fakes_test.go`): letters from the request's content, faults keyed by class, core, turn and clip (L1.3),
  `peers` gating first sends only; `arithmeticAssembler` records its requests and takes a `refuse` hook. Deleted with
  the behaviour they proved: `revisit_test.go`, the old-path tests of `coordinator_test.go` and
  `calibration_log_test.go`, the two `TestCalibrationEndToEnd…`. Adapted: `request_retry_test.go`'s calibrated rows
  (new kinds and words), the three L2.10 tests, `TestCoordinatorCancelsAndCleansUp` (`Parallelism` 1), the live-board
  tests (new header, no sleep), and V3's `TestCacheHitSkipsTranscriber` (`RequestLabelling`; a cache hit is a request of
  the chunk, not an upload). `TestSurveyPathEndToEnd` needs 134 s, not 200 s, for its four chunks (V3).

Review 2 fixes (2026-11-22), tests first where behaviour changed:

- R2-01: `finalFooter` writes the board's last line on every exit, failures included; `TestRealFailureFailsTheRun`
  asserts its fields, `TestCoordinatorLiveErrorExitKeepsFooter` became `…WritesTheFinalFooter` (the live frame cuts the
  footer at the board's width, so it asserts the visible fields and no `in flight`).
- R2-03 (orchestrator's decision): `Run`'s and `merge.go`'s doc comments are short and point to `architecture/audio.md`
  and the phase files, which keep the clauses.
- R2-06: the join ranks from `report.Voices` only; a report without a speaker's clips joins nothing (no fallback,
  no panic), and the `reportless` subtest asserts that.
- R2-07: `requestFor` gone (`ask` calls `request`); `prefixedLog` serves `logFor` and `linkLog`; the row word is a map
  lookup in `request`; `logPlan` takes the parallelism; `chunkState`'s counters are atomics; `logJoin` derives `was`
  from `after`; the manifest check is in `request` for every kind (a region-less manifest now marks its row).
- R2-08–R2-10: `TestRetryBuildsItsPrefix`, `TestLabellingOrderJoinAndNaming`, `TestParallelismSlots`; the fake gained a
  `share` fault. In a scratch copy each listed probe now fails the suite (12 of 12, the `apart` probe written so it
  still compiles).
- R2-11: `unmatched` (material labels naming nobody, `""` excluded) drives the retry trigger, the ✗ count and the
  retry and `accepted` records; `TestSpeechWithoutASpeakerIsNoLabel`. The fake keeps an empty speaker empty.
- R2-12: `retryChunk` returns `*retryFailure`, `retryAll` `[]retryFailure`; `line()` renders the finishing line.
- R2-13: `voiceProvider.held` signals the first held request; the test waits on it, no sleep.

I9 by L1.4's command after the fixes: **10,891** non-test lines (11,085 before; baseline 9,942). R2-02 stays open.

Andon: none.
Surprises:

- A failed retry must not mark its row even briefly: off a terminal every row update prints a line, so
  `rowFailed` skips a retry unless the context ended.
- A merge request's error goes back unwrapped, so the run's error is exactly `merge link for chunk N: <cause>`.
- I9 is at risk for phase 3: the tree holds 11,085 non-test lines against the 9,942 baseline, and phase 3's Removes
  rows free roughly 600 (`discovery.go` 392, most of `registry.go`, the old kinds, `RequestLimitError`, `max-speakers`).

### Validation notes (V1, 2026-11-22)

- **V1 note · I6.** With an empty registry, Run.6 has the cache answer the labelling request with the survey's segments,
  which reach the output (`TestNothingToLabel`), while Run.11 says no survey segment is in it (I6). The output is the
  same either way (all `unknown-N`); name the case in Run.11 so the two clauses agree.
- **V1 note · check 4.** `TestSurveyPathLabelsEveryChunk` (and criterion 1) says `sixVoiceMeeting` has two of six people
  in chunk 1; its chunk 1 holds three (`v1 v2 v3`). The row needs a variant fixture.
- **V1 note · check 4.** `TestSurveyNeverReachesTheOutput`: `RequestTranscriber.TranscribeRequest(ctx, m)` receives no
  kind; the scripted diarizer can tell only a survey (no sample) from the rest by the manifest.
- **V1 note.** `TestCoordinatorCancelsAndCleansUp` keeps its assertions only with `c.Parallelism = 1`: at 3, Run.13
  starts three surveys at once and `p.requests() != 1` fails.
- **V1 note · the document.** The `bootstrap` grep also hits the Key Files row of `architecture/audio.md` (line 43,
  "bootstrap and discovery"), outside the sections this phase lists; reword it and add `merge.go` there.
- **V1 note · event log.** `candidate` lists `chunk=` among its keys while every chunk line already carries
  `chunk=N attempt=M`; say which.
- **V1 note · board.** The footer's `in flight <f>` counts board rows; merge requests have none.
- **V1 note · check 3.** Consumes omits `CandidateError` (last Error management row) and `MergeReport.Found` (Run.5).
- **V1 note · check 3.** The revisit members exist only uncommitted in the working tree on `0ca840b`, beside many
  unrelated modified files; commit them before phase 1 so no phase commit or re-plan revert sweeps them.
- **V1 note · check 4.** Criterion 19's coverage (≥ 90 % of new code in `coordinator.go`) has no command row.
- **V1 note · root.** The status board's phase 2 Interface cell omits `parallelism` (Par.1–4, decided by Q1); the README
  status line still says "Phase files being written"; H1's S2 is still open (the 41:42 check) and no
  `human check root` journal line records H1.
- **V1 note · root, check 5.** Root holds facts that bind one phase: I6, "candidates per material label", "retries per
  chunk", `parallelism` (phase 2); "merge orders", "merge link size" (phase 1). Binds omits phase 1 for I7 and I8
  (Merge.9, Merge.2 cite them) and phase 2 for I10 (Run.7, L2.3). Not filed: the brief binds the signed Root facts
  and Parameters.

### Validation notes (V2, 2026-11-22)

- **V2 note.** `TestCoordinatorCancelsAndCleansUp` "with `Parallelism` 1, since unset now starts every survey at once":
  the test sets `Parallelism` 3, not unset; at 3 three surveys start at once. The instruction is right, the reason is
  not.
- **V2 note.** "with the source lengthened so the chunk counts they assert stay": none of the three adapted tests
  asserts a chunk count on the calibrated route; harmless.
- **V2 note · criterion 19.** The coverage row's "every function … at 90 % or more on average" is not the criterion's
  "new code … covered ≥ 90 %"; say which figure counts.
- **V2 note.** Local facts list L2.10 before L2.9; cosmetic.
- **V2 note · check 3.** The revisit members are still uncommitted in the working tree (V1 note); commit before phase 1.
- **V2 note · root.** The V1 root notes still apply: README status line, the board cell without `parallelism`, H1's
  open S2, and fact placement and Binds (not filed, binding per the brief).

### Validation notes (V3, 2026-11-22)

- **V3 note.** "`TestSurveyPathEndToEnd` … sets its budget the same way": its Given needs 4 chunks of the 180 s source,
  which the three tests' 200 s (two chunks) does not give; about 134 s (cores near 45 s) or a longer source does.
- **V3 note.** Criterion 3a's Proof cites "the two `grep` commands of L2.10"; they are in the Test cases row, not in
  L2.10.
- **V3 note.** `TestCacheHitSkipsTranscriber` (`cache_test.go`) calls `c.requestFor(asm, st, …)` with
  `RequestForward` and asserts that cache hits count in `st.consumed` ("against the chunk limit"). Phase 2 does not
  list it: keep `requestFor`'s signature and per-chunk count, or adapt the test with the rewrite.
- **V3 note.** Still open from V1/V2: the revisit is uncommitted (`coordinator.go`, `discovery.go`,
  `calibration_log.go` modified, `revisit_test.go` untracked, on `0ca840b`); commit it before phase 1. The root notes
  still apply.

## Review findings

### Review 2, 2026-11-22

First review of this node; numbered round 2 after phase 1's round 1. Verdict: **Not ready** (2 major, 11 minor).

Gates, run twice from `components/clai` with the API keys unset and `GOFLAGS=-shuffle=on`: `make qa` exit 0 both times
(108 packages ok, `internal/audio` 93.4 %); `go vet ./...` clean both times; dupl 49 clone groups both times, none in
`coordinator.go`, `merge.go` or the new test files (also none at `-t 30` in the package's non-test files). The gates
left the tree byte-identical (sha256 of every tracked and untracked file before and after). New tests repeated:
`go test ./internal/audio -race -count=40 -shuffle=on -run 'Survey|Join|Retry|Parallelism|MoreThanEight|PathLength|NothingToLabel|Unmatched|RealFailure|Board|CalibrationLog|PlanReserves|Eta|Merger|CacheHit|MaxRequestBytes|Splitter_|Coordinator'`:
ok (68.6 s); 12 race binaries of the same set at once, `-test.count=15 -test.shuffle=on` each: 12 × PASS. No flake.
`go test ./e2e -run '^TestDiagnosticOutputGoldens$'`: ok, no golden changed by this phase. Criterion 18's two greps:
no output; 42. Criterion 3a's greps: only L2.10's users. Coverage: every function this phase wrote in `coordinator.go`
at 90 % or more (`slot` 83.3 %, the only one below 100 % besides `Run` 97.1 %, `planRetry` 97.8 %, `clipsOf` 90 %,
`request` 98.1 %); no `c.out` in the tree.

I9, measured with L1.4's command: **11,085** non-test lines now (baseline 9,942; 10,508 after phase 1). Phase 3's
Removes applied in a scratch copy (`discovery.go` gone with `RequestFunc` and `reasonNoRun` moved, the registry's
mutation paths and the old `Speaker` and `Snapshot` fields, the seven old kinds, `rejectedText`, `RequestLimitError`,
`IsRequestLimit`, both `MaxRequestsPerChunk`, `-max-speakers`, `transcribe.max-speakers`, `Budgets.MaxSpeakers`
re-pointed to `maxVoices`, the four reasons; `go build ./...` ok): **10,504**, and staticcheck then names `better`
(about −6): about **10,498, 556 over the baseline**. R2-02 has the judgement.

Mutation probes in a scratch copy of the tree (each against the whole `internal/audio` suite): caught — retry kept on
equal speech, join on one shared request, join without the kept-apart check, the `found` line at 8, no cancel on the
first error, a failed retry marking its row, no identical-retry check, no reserve pin, swapping a clip already sent,
no reversal, every upload counted as a retry, no renumbering after a join, ignoring the context between chunks, the
cache off, no ambiguity in naming, the candidate's last clip, a retry for every chunk, the join counting unsettled
clips, the link number, the ✗ mark, a smaller candidate cap, double-counted shared requests, one worker per stage,
the outer error as the cause, no `found` line, no `clipsOf` fallback (26 caught of 39). Survived: R2-08, R2-09, R2-10
and one of R2-11 (13 in all); the cancelled
retry's own `ctx.Err()` check (`coordinator.go:526`) is an equivalent mutant (`eachChunk` returns `ctx.Err()`), and so
are the retry's clip IDs (post-join, seen only in records). `st.resent` feeds only the `accepted` record's `resent=`,
which no row asserts above 0 (left as a note).

| ID | Severity | Where | Clause | Finding | Action |
| -- | -------- | ----- | ------ | ------- | ------ |
| R2-01 | major | `internal/audio/coordinator.go:268` | the board's "Final footer … at the end, failures included"; Run.16 "the board is left readable with its footer"; `architecture/audio.md` Progress, "final footer … failures included" | Every failure exit writes `c.board.Footer()`, the live footer, so the last line keeps an estimate and `in flight`. Probe: a labelling request not answered after the schedule ends the status with `finishing · 0s elapsed · requests 12/≤18 · resent 4 · in flight 1 · speakers 6 · unknown 0s · uploaded 55m`, not `requests 12/≤18 · resent 4 · retries 0 · cache hits 0 · uploaded … · speakers 6 · unknown 0s · … elapsed`. `TestRealFailureFailsTheRun` checks only `requests ` and ` elapsed` (`survey_test.go:952`), and `TestCoordinatorLiveErrorExitKeepsFooter` (`coordinator_test.go:217`) asserts the opposite (`in flight` in the error-exit frame) | - [x] Write the final footer on every exit path from one function shared with `finish`, assert its fields in a failure row of `TestRealFailureFailsTheRun`, and change `TestCoordinatorLiveErrorExitKeepsFooter` with it (no line cost) |
| R2-02 | major | README I9; phase 3 Removes | I9 (binds 1, 2, 3) | I9 is not reachable as planned. Phase 3 as written ends near 10,498, 556 over the baseline (measurement above). Every reduction this review found totals about 352: R2-03 about 205, R2-04 about 40, R2-05 about 45, R2-06 about 17, R2-07 about 45. That leaves about 200. The only other block of non-test code with no production caller is `eval.go` (196 lines, H2's scorer, called by `eval_test.go` only). It lies outside the effort's scope, and moving it would relocate code, not reduce it | - [ ] Re-plan phase 3 to take R2-03–R2-05, and phase 2's fix to take R2-06–R2-07. Find about 200 more lines (for example the request accounting and board plumbing of `coordinator.go`, or a narrower `merge.go`). If the signed scope cannot give them, raise a design andon so the maintainer decides on I9 (and on `eval.go`) |
| R2-03 | minor | `internal/audio/coordinator.go:149`–`251`; `internal/audio/merge.go:1`–`218` | I9; CLAUDE.md "Do not leave bloaty redundant comments … Public functions should only describe non-intuitive functionality" | The signed interface lives in Go doc comments and repeats `architecture/audio.md` ("Survey and merge", "Calibration prefix and mapping", "Stitching and failure policy") nearly sentence for sentence: Run.1–18 is 103 comment lines, `merge.go`'s interface comments 126 | - [x] Plan: decide whether a composition or contract Interface block may keep its clause text in the phase file (tests already name the clause IDs), with Go docs of one or two lines pointing to `architecture/audio.md`; then shorten both blocks (about −205) |
| R2-04 | minor | `internal/audio/mapping.go:143`–`171`; `internal/audio/coordinator.go:633`–`681` | I9; Imports ("`LabelMapper.Map` … unchanged"), L3.1 | `LabelMapper.Map`'s holders block (known and candidate samples per label → `LabelToID`, `SampleAmbiguous`, `SampleCollapsed`/`Onto`) is the old path's rule, and the survey path discards it. `answer.mapping` runs `Map` over a core-only manifest so nothing is named, runs it again over the full manifest through `labelledSamples`, then names labels by Run.8 and rewrites every settled sample's status. The merger reads only `Label`, `Share` and `Labeled`. After phase 3 nothing else calls `Map` | - [ ] Plan: let the mapper take the owners and apply Run.8 itself; delete the holders block and `answer.mapping`'s rewrite (about −40) |
| R2-05 | minor | `internal/audio/mapping.go:58`–`73`, `:98`; `internal/audio/assemble.go:68`, `:151`; `internal/audio/registry.go:23`, `:45` | I9; phase 3 Removes (the `Speaker` row lists `Snapshot.find`) | Phase 3's Removes misses production-dead members: `Mapping.byStatus`, `Failed` and `Ambiguous` (`Failed` called by `discovery.go` only, `Ambiguous` by nobody), `Mapping.RangesFor` (`discovery.go` only), `RequestManifest.SourceTime` and `AudioAssembler.RunDir` (tests only, already today). Together about −45. The `Speaker` row removes `Snapshot.find`, which `planRetry` now calls (`coordinator.go:593`), so phase 3 as written does not build. Stale comments: `registry.go:23` ("Empty for a speaker the old path registered"), `mapping.go:109` ("registry state changes happen in Discover"), `mapping.go:166`, `calibration_log.go:124` ("before discovery promotes anything") | - [ ] Plan: add those members and their tests, and the comments, to phase 3's Removes; keep `Snapshot.find` or re-point `planRetry` |
| R2-06 | minor | `internal/audio/coordinator.go:791`–`807`, `:763` | I9; Merge.6, Merge.7 ("Voices lists every voice found", clips ranked) | `clipsOf`'s fallback serves a `MergeReport` without `Voices`, which the `Merger` contract rules out. Only the test double `reportless` reaches it (`survey_test.go:808`, the subtest at `:369`) | - [x] Rank the joined speaker's clips from `report.Voices` only; delete the fallback and the `reportless` subtest (about −17) |
| R2-07 | minor | `internal/audio/coordinator.go:857`, `:947`, `:340`, `:132`, `:685`; `internal/audio/calibration_log.go:41`, `:80` | I9; CLAUDE.md "If some piece of code is written twice, it should be abstracted" | Small reductions that keep behaviour. `requestFor` only wraps `c.request` for `ask` (pass the assembler and state; −5). `logFor` and `linkLog` build the same `EventLog` closure behind different prefixes (−6). `purposeOf` can be a map lookup (−5). `parallelismText` can fold into `logPlan`'s call (−4). `chunkState`'s `consumed`, `resent` and `retries` can be atomics instead of `mu`-guarded ints across `request`, `chunkDone`, `etaChunks`, `countUpload`, `countResent`, `logFor` and `logAccepted` (−15). `joining.was` can be derived from `after` in `logJoin` (−4). `ask` and `ChainMerger.request` both check for a manifest without regions; one check in `c.request` covers every kind of the run (−3). About −45 in all | - [x] Apply them with the gates green |
| R2-08 | minor | `internal/audio/coordinator.go:609`, `:607`, `:575`, `:606`; `internal/audio/survey_test.go:414`, `:486` | Run.9, L2.2 | Four probes pass the whole suite. (a) Without the 8-clip cap: "exceed 8 clips" is a Must not of `TestRetryFixesEverythingAtOnce`, but no fixture has 8 speakers and a pending label. (b) Without the most-speech-first sort: every fixture has one candidate. (c) Taking only `SampleMapped` as settled for the swap: the fixture's two speakers on one label have no next clip. (d) Cutting candidates for a label a registry clip settled on: the fixture's shared label holds no core speech. Scenario (a): 8 speakers and a material label naming nobody give a 9-clip retry over the 88 s reserve, which the budget check refuses and Run.10a keeps silently, unseen. The shipped code is right | - [x] Add rows: 8 speakers and a pending label (no candidate sent); two pending labels (the larger first, the cap cutting the smaller); an ambiguous speaker with a next clip (not swapped); a shared label with core speech (no candidate). Confirm that each probe fails |
| R2-09 | minor | `internal/audio/coordinator.go:465`, `:740`, `:753`, `:640` | Run.6, L2.5; Run.7; Run.8 | Probes that pass the whole suite. (a) Labelling clips in registry order instead of source start: in every fixture the samples' start order equals registry order. (b) Dropping `apart > 0` from the first join test: the kept-apart loop then refuses, and no fixture has a speaker apart from its earliest agreeing speaker but agreeing with a later one. (c) Trying a later speaker after the earliest is blocked, instead of `break`. (d) Naming a label from an unsettled clip: the mix fault's top label never occurs in a core, so the 80 % rule of Run.8 is not pinned at the coordinator. The shipped code is right | - [x] Add a speaker whose best clip starts after a later-numbered speaker's; three-speaker join rows for (b) and (c); a 60/40 mixed clip whose top label is a core label (names nobody). Confirm the probes fail |
| R2-10 | minor | `internal/audio/survey_test.go:629`; `internal/audio/coordinator.go:935` | Run.13 ("never more than n requests are in flight, the merge's included") | With the semaphore removed (`slot` always free), `TestParallelism` passes 20 times and so does the whole suite. At `Parallelism` 1 the link's pair is never forced to overlap (`peers` nil, the fake answers at once), and `stageWorkers` caps the chunk stages on its own. A coordinator that sends both requests of a link at once under `-parallelism 1` passes | - [x] Make the row hold the first merge request until the test has seen whether the second arrives, or unit-test `slot` with `Parallelism` 1 (a second take blocks until the first is released). Confirm the probe fails |
| R2-11 | minor | `internal/audio/coordinator.go:481`, `:989` | L2.11; `architecture/audio.md` "A segment without a speaker is no label for any decision below" (before Second try) | The retry trigger (`MaterialUnmapped()`) and the row's unmatched count include the speech of segments with no speaker. `materialLabels` (`:398`) and `planRetry`'s pending list (`:606`) skip it, and no row pins that skip: a probe dropping `label != ""` from `materialLabels` passes the whole suite. Reachable only if the anchored diarizer returns segments without a speaker (not seen on this route). Scenario: a labelling answer whose only material unmapped speech has no speaker makes the chunk due. The phase then reads `second try · 1 chunks with unmatched voices` and the row ends `✗ 1 unmatched`. If any clip went unsettled, a retry is uploaded for speech that no retry can name | - [x] Skip `""` in the trigger and the count (the doc's reading, the stricter one) and add a row, or reword the doc |
| R2-12 | minor | `internal/audio/coordinator.go:514`, `:473` | CLAUDE.md "Return types are self-describing … If a return value needs a comment to explain it, it needs a type" | `retryChunk` returns `(Mapping, string, error)` and `retryAll` returns `([]string, []Mapping, error)`. The bare strings are "the finishing line of a failed retry", which only the doc comment explains | - [x] Return a named type (for example a failed-retry record with chunk, span and cause that renders the line) |
| R2-13 | minor | `internal/audio/coordinator_test.go:175` | I11 ("no sleep"); CLAUDE.md | `TestCoordinatorCancelsAndCleansUp`, edited in this phase (`Parallelism` 1), still polls `p.requests()` with `time.Sleep(time.Millisecond)` for up to 5 s. The Implementation notes say the live-board tests lost their sleeps | - [x] Signal the first held request on a channel from `voiceProvider` and wait on it |

Verified good (notes):

- **Interface.** The `Run` doc comment equals the Interface block token for token, and the three kinds, `Parallelism`,
  `Merger` and `MaxRequestsPerChunk` (unread) are as declared.
- **I5, Run.1.** `Run` pins `MaxSpeakers` to `maxVoices` before the one `Plan`. Every request of every kind, including
  the merge's through `mergeRequest`, goes through `c.request`, so the run's assembler checks both budgets before any
  upload. A retry stops adding candidates at 8 clips (`:609`); a labelling request carries one clip per speaker, 8 at
  most.
- **I6, Run.11.** The survey's mappings feed `candidates` only. `stitch` reads the labelling or retry mapping. With an
  empty registry the labelling request equals the survey's (`labellingClips` empty) and the cache answers it.
- **I2, I3, Run.7–8.** The join compares two labels only inside one labelling request (`settledOn` per answer), counts
  only settled clips over labelled segments (`labelledSamples`), and is computed once. A label names a speaker only
  when every settled registry clip on it has one owner after the join; candidates never name.
- **I4.** `planRetry` swaps copies; the registry after the run holds the merge's samples (asserted).
- **I7, Run.10a, Run.12.** A failed retry with the context alive keeps the labelling mapping, writes one record and
  one finishing line, and leaves the row on its labelling result. Labels never return an error.
- **I8, Run.14.** At most survey, labelling and one retry per chunk, and at most n−1 links of 2. Resends happen inside
  `resend.Do` on the same manifest and do not move `consumed`.
- **Concurrency.** `eachChunk` sends the failing worker's error before it cancels, buffers `len(states)` errors and
  waits for every worker. `both` waits for both answers. `Animate` ends with the run's context. No goroutine outlives
  `Run` but `Animate`'s return. `-race` is clean in every run above.
- **Q1, Par.1–4.** `Default.Transcribe.Parallelism` is 0 and `NewSplitter` sets none; the plain path's workers are 3;
  a file value or the flag caps both paths. Caveat for H2: an `audioConfig.json` that clai wrote before this phase
  holds `"parallelism": 3`, which Par.4 keeps (the maintainer's own file does), so the 88-minute run goes in two waves
  unless it passes `-parallelism 5` or the file is edited.
- **Q2, Run.5.** The line is exact and comes first among the finishing lines; `Found` is `len(Voices)`.
- **Docs.** `architecture/audio.md` describes the survey path. The Progress, Request retries, Configuration and Key
  Files rows are updated, and the `max-speakers` rows say the path no longer reads it.

### Review 3, 2026-11-22

Verdict: **Ready**. Every fix-owned finding of review 2 holds and none regressed. R2-05 holds by phase 3's re-plan.
R2-02 was closed by the maintainer while this round ran: I9 now reads "at most 10,300". R2-04 is open by choice (it
would change an unchanged import). One new minor: R3-01. No blocker or major is open on this node.

Gates, run twice from `components/clai` with the API keys unset and `GOFLAGS=-shuffle=on`:
- `make qa`: exit 0 both times (108 packages ok, `internal/audio` 93.4 %).
- `go vet ./...`: clean both times.
- dupl: 49 clone groups both times.
- The tree was byte-identical before and after (sha256 of every tracked and untracked file).

Repeated: phase 2's tests in one race binary, `-test.count=40 -test.shuffle=on`: PASS. The same set in 12 race binaries
at once, `-test.count=15` each: 12 × PASS. `TestDiagnosticOutputGoldens`: ok. Criterion 18's grep: no output.
Coverage: every function this phase wrote in `coordinator.go` is at 97 % or more (`slot` is now 100 %), and no
`c.out` is in the tree.

I9 by L1.4's command: **10,891** (11,085 at review 2; baseline 9,942). The amended bound is at most 10,300. Phase 3's
re-planned Removes, applied to a scratch copy of today's tree, give **10,253** (`go build ./...` ok). That meets the
bound with 47 lines to spare, before the stale comments are reworded.

Review 2's findings, checked again:
- **R2-01 holds.** Every exit writes `finalFooter()` (`coordinator.go:175`, `:903`). Probe: putting the live footer
  back fails `TestCoordinatorLiveErrorExitWritesTheFinalFooter` and four rows of `TestRealFailureFailsTheRun`.
  Without its cache-hit count the footer fails `TestNothingToLabel`.
- **R2-03 holds in the code.** The `Run` doc is 7 lines and `Merger.Merge`'s 6; both point to `architecture/audio.md`
  and to the phase files. The record of that change is R3-01.
- **R2-06 holds.** The join ranks from `report.Voices` only, and a report without a speaker's clips joins nothing
  (`:681`). Probe: dropping that guard fails `…a_merger_whose_report_breaks_Merge.7_joins_nothing`.
- **R2-07 holds.** `requestFor` is gone and `prefixedLog` serves both logs. The row word is a map lookup, `logPlan`
  takes the parallelism, the counters are atomics, `logJoin` derives `was` from `after`, and the manifest check is
  in `request` (`:765`). Probe: dropping that check makes the region-less row panic.
- **R2-08, R2-09, R2-10 hold.** All nine surviving probes of review 2, written against the current code, now fail
  the suite. Each is caught by `TestRetryBuildsItsPrefix`, `TestLabellingOrderJoinAndNaming` or
  `TestParallelismSlots`.
- **R2-11 holds.** `unmatched` (`:405`) leaves out `""` from the trigger, the ✗ count and the `retry`/`accepted`
  records. Probes: putting `""` back into `unmatched` or `materialLabels` fails `TestSpeechWithoutASpeakerIsNoLabel`.
  Also, a trigger that needs two labels fails four tests.
- **R2-12 holds.** `retryChunk` returns `*retryFailure` and `retryAll` returns `[]retryFailure`, with `line()`
  rendering the finishing line. Probe: dropping the failure fails all four rows of
  `TestFailedRetryKeepsTheLabellingResult`.
- **R2-13 holds.** `voiceProvider.held` signals without blocking. No `time.Sleep` is left in phase 2's test files.
- The 18 probes of this round: 17 fail the suite. The one that survives is review 2's known equivalent mutant (the
  retry's own `ctx.Err()` check; `eachChunk` returns `ctx.Err()`).

New in the fixes (typed returns, final footer, empty-speaker rule, trimmed docs): nothing wrong in behaviour. The
deferred `finalFooter` also runs after a successful `finish`, but it only rewrites the same cache-hit count, and the
board ignores a second `Finish`.

| ID | Severity | Where | Clause | Finding | Action |
| -- | -------- | ----- | ------ | ------- | ------ |
| R3-01 | minor | `internal/audio/coordinator.go:147`; `internal/audio/merge.go:134`; `phase-2-survey-calibration.md` Interface; `phase-1-speaker-merger.md` Interface | worklog-review, contract phase: "the written interface matches the phase's Interface block token-for-token after formatting"; R2-03 | R2-03's outcome lives only in the code and in phase 2's fix notes ("orchestrator's decision"). Phase 1's and phase 2's Interface blocks still carry Merge.1–12 and Run.1–18 inside their Go blocks, and no plan or maintainer line records that the clauses no longer need to be in the Go docs. Scenario: the root review compares `merge.go` with phase 1's Interface block token for token and finds about 110 comment lines missing, so the signed contract phase 1 was reviewed Ready on no longer matches its code | - [ ] Plan: state in both Interface sections that the clause text stays in the phase file and the Go docs only point to it (or have the maintainer re-sign the shortened blocks), with a journal line |
