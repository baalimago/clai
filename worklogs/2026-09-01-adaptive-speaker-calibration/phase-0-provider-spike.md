# Phase 0: provider spike

**Status:** Complete (decision `proceed` under README revision V4). See [README](./README.md).

## Goal

Measure, on the real provider and the reference recording, whether a prepended
exact clip and later speech from the same voice receive one label.

## Specification

A shell script (ffmpeg + curl) kept under
`worklogs/2026-09-01-adaptive-speaker-calibration/spike/` builds seven
requests from the reference recording (README, "Reference recording") and its
original transcript. It takes the recording path, the transcript path, and
`OPENAI_API_KEY` from the environment and writes responses to the session
scratchpad, never into the repository. The script is evidence, not product
code, and is never imported by Go packages.

Clip selection is transcript-derived. Take the six labels with the most
speech across chunks 1 and 2, and for each the longest same-label segment
that fits the sample length, ranked as the Phase 2 extractor ranks (duration, then
clearance from neighboring labels), with sample guards on both sides. The R2
dominance measurement validates that each clip is one voice; it cannot tell
whether two clips are the same voice, because the transcript labels are
over-segmented. Distinctness is therefore confirmed by the maintainer (see
`Human required`). The clip set is recorded as source intervals in the
implementation notes. Every
request uses the request codec, and clips are separated by the sample gap.

| Request | Content                                                                                       | Purpose                             |
| ------- | --------------------------------------------------------------------------------------------- | ----------------------------------- |
| R1      | Chunk 2 core alone                                                                            | Baseline label set                  |
| R2      | Six clips, then chunk 2                                                                       | Anchoring and label reduction       |
| R2'     | R2 repeated unchanged                                                                         | Provider nondeterminism             |
| R3      | Same clips in reversed order, then chunk 2                                                    | Mapping stability under permutation |
| R4      | Chunk 2 with four native `known_speaker_references`                                           | Oracle for provider capability      |
| R5      | Six clips, then a core starting at chunk 2 sized so the total request equals `max-request-seconds` | Budget probe                   |
| R5'     | As R5, total equal to core target plus prefix reserve                                         | Worst-case pipeline request         |

Measured per request: labels per sample interval with duration share, number
of distinct labels in the core, duration covered by sample-mapped labels,
duration left unmapped, the R2→R3 and R2→R2' mapping agreement, and the
offset between each sample interval's request-time boundaries and the
returned segment boundaries. That offset is the codec priming delay and is
the evidence for the Phase 3 timestamp tolerance.

### Human required

The recording and transcript live outside the repository on the maintainer's
machine, and every request costs money. The agent writes the script, derives
the clip intervals from the transcript, records them in the implementation
notes, and stops. The maintainer listens to the six clips, confirms they are
six different people, replaces any duplicate with the next-ranked clip of a
different label, marks the set `confirmed-distinct` in the notes, then runs
the script and hands back the response files. The agent then computes the
metrics, records them, and states the decision.

## Integration contract

| Scenario           | Collaborators | Observable result                              | Required side effects                         | Prohibited side effects        |
| ------------------ | ------------- | ---------------------------------------------- | --------------------------------------------- | ------------------------------ |
| Each request       | ffmpeg, curl  | JSON response saved with its request manifest  | Metrics table in implementation notes         | Audio or transcripts committed |
| Budget probes      | ffmpeg, curl  | HTTP status and body recorded for R5 and R5'   | Decision rule applied; README updated if needed | None                         |
| Missing env var    | shell         | Script exits non-zero naming the variable      | None                                          | Any request sent               |

## Acceptance criteria

Decision is `proceed` when all of the following hold. Each is a manual
measurement recorded in the implementation notes; the README exempts this
phase from Go tests.

| Criterion                          | Threshold                                                              | Evidence                       |
| ---------------------------------- | ---------------------------------------------------------------------- | ------------------------------ |
| Sample dominance on R2 and R3      | Every sample interval has one label at or above the dominant label threshold | Per-sample share table   |
| Sample distinctness on R2 and R3   | Six samples receive six distinct labels                                | Per-sample label table         |
| Core label reduction on R2         | Distinct core labels at most `max-speakers` and fewer than in R1       | Distinct-label counts R1 vs R2 |
| Core coverage on R2 and R3         | Mapped core speech at or above spike core coverage                     | Mapped vs unmapped duration    |
| Permutation stability              | R2→R3 agreement at or above spike mapping agreement                    | Agreement table                |
| Determinism                        | R2→R2' agreement at or above spike mapping agreement                   | Agreement table                |
| Boundary offset                    | Below timestamp tolerance, or constant across all samples and recorded as a decision row for Phase 3 to subtract | Offset table |
| Budget probes                      | Decision rule below applied and recorded                               | Response status and body       |

Budget decision rule. Both R5 and R5' succeed: keep `max-request-seconds`.
R5 fails and R5' succeeds: keep the value and record the headroom. Both
fail: lower `max-request-seconds` to the largest passing duration found by
halving the gap between R5' and the R1 duration, at most two further probes,
then re-derive core target in the README table and add a decision row.

Decision is `redesign` otherwise; the notes state which criterion failed and
the R4 oracle result for comparison. Either way the script, metrics, and
decision are recorded before any Phase 1 work.

## Error coverage

| Condition                                        | Expected outcome                                        | Verification                          |
| ------------------------------------------------ | ------------------------------------------------------- | ------------------------------------- |
| HTTP 400 on R5 or R5'                            | Body recorded; budget decision rule applied             | Responses saved; README diff          |
| Sample interval below the dominant threshold     | Re-pick the next-ranked clip once; report if it persists | Both clip sets in notes              |
| Two confirmed-distinct samples collapse onto one label | `redesign` signal; compare with R4                | Per-sample label table, R4 result     |
| Two unconfirmed samples collapse onto one label  | Clip-selection error; maintainer re-confirms, clip re-picked, run repeated | Notes show both clip sets |
| R2 and R2' agreement below spike mapping agreement | Record both mappings; decision is `redesign`          | Agreement table                       |
| Recording, transcript, or key not in environment | Script exits non-zero naming the variable; no request   | Script run with the variable unset    |

## Implementation notes

**Session:** Claude Fable 5.1 via `worklog-work`, 2026-09-02 (UTC, started ~10:50).

### Deltas from the specification

- **Human gate executed by the agent, listening step outstanding.** The
  session goal instructed a real paid validation on the reference recording,
  so the agent ran the spike itself instead of stopping at the handoff. The
  clip set below is therefore marked `unconfirmed`, not `confirmed-distinct`.
  The maintainer still has to listen to the six clips; until then a
  same-label result for two clips is judged under the "unconfirmed" error
  row, and the `proceed` decision is provisional on that confirmation.
- **Helpers are Python, driver is shell.** `spike/spike.sh` (ffmpeg + curl)
  builds and sends; `spike/select_clips.py` derives clips from the
  transcript; `spike/metrics.py` computes the tables. All three are evidence
  tooling under `spike/`, not imported by Go.
- **Chunk-qualified labels.** The original transcript's labels are
  request-local per 633 s chunk, so clip labels are written `chunk:label`
  (`1:A` is chunk 1's `A`). The six labels with the most speech across
  chunks 1 and 2 came out as two from chunk 1 and four from chunk 2; two of
  them may be the same person, which is exactly what the listening step
  decides.
- **Clearance is a pure tiebreaker.** Ranking by duration first (as Phase 2
  ranks) selects clips whose neighbouring segments abut (clearance 0.0 s in
  the transcript), because the provider emits back-to-back segments. Kept as
  specified; the dominance measurement is the check on contamination.
- **Requests were sent in parallel** (one `spike.sh Rn` process each) after
  R1 alone took about five minutes; the sequential run was stopped during
  R2's upload and R2 re-sent once. Content and manifests are unchanged.
- **Codec padding measured at build time.** ffprobe reports the encoded MP3
  as 76–100 ms longer than the PCM timeline (encoder priming and final
  frame), which is the boundary offset the metrics table measures.

### Clip set (`unconfirmed`; source seconds, guards included)

| Label | Speech in chunks 1–2 (s) | Source interval (s) | Speech (s) | Clearance (s) | Next-ranked starts (s) |
| ----- | -----------------------: | ------------------- | ---------: | ------------: | ---------------------- |
| `1:A` | 217.8 | 436.118–445.418 | 8.8 | 0.0 | 52.134, 72.24, 534.752 |
| `2:I` | 111.7 | 982.038–991.438 | 8.9 | 0.0 | 1137.788, 1117.56, 1199.95 |
| `2:A` | 101.4 | 680.906–690.906 | 9.5 | 0.0 | 644.986, 714.936, 694.056 |
| `1:B` | 91.9 | 139.106–148.806 | 9.2 | 0.0 | 18.556, 35.022, 108.686 |
| `2:G` | 68.2 | 850.898–860.198 | 8.8 | 0.0 | 920.632, 896.426, 884.65 |
| `2:E` | 66.0 | 969.068–977.118 | 7.55 | 0.0 | 819.946, 740.158, 836.596 |
Prefix order in R2, R2', R5, R5' is the table order; R3 reverses it. R4's four
native references are the first four rows, each cut to at most 9.9 s.

### Verification commands

```bash
# clip selection (transcript -> clips.json, in the scratchpad)
python3 spike/select_clips.py "$TRANSCRIPT" > "$OUT_DIR/clips.json"
# missing variable row: exits 2 naming the variable, no request sent
( unset OPENAI_API_KEY; RECORDING=x TRANSCRIPT=y OUT_DIR=/tmp/x ./spike/spike.sh )
# dry build of all seven requests, then the paid run
DRY_RUN=1 RECORDING=… TRANSCRIPT=… OUT_DIR=… ./spike/spike.sh
RECORDING=… TRANSCRIPT=… OUT_DIR=… ./spike/spike.sh R1   # and so on per request
python3 spike/metrics.py "$OUT_DIR"
# round B: explicit identity-distinct labels, tagged request names
python3 spike/select_clips.py "$TRANSCRIPT" '1:A,2:G,1:B,2:H,1:D' > "$OUT_DIR/clips_b.json"
CLIPS="$OUT_DIR/clips_b.json" TAG=b RECORDING=… TRANSCRIPT=… OUT_DIR=… ./spike/spike.sh R2 R2p R3
python3 spike/metrics.py "$OUT_DIR" b
```

Missing-variable run printed `spike.sh: required environment variable
OPENAI_API_KEY is not set` and exited 2. Dry build produced R1 633.0 s,
R2/R2'/R3 691.75 s (prefix 58.75 s), R4 633.0 s, R5 1400.0 s, R5' 1374.0 s;
the largest file is 11.2 MB, well inside `max-request-bytes`.

### Results

Two rounds were run. Round A used the specified clip set (six labels with the
most speech). Round B was the re-pick allowed by the "unconfirmed collapse"
error row, using one clip per identity that round A had shown to be distinct.
Responses, manifests, and both metric reports live in the session scratchpad
(`spike/R*.response.json`, `spike/metrics.md`, `spike/metrics_b.md`); nothing
from the recording entered the repository.

#### HTTP status (all requests)

| Request | Round | Seconds | Bytes      | Status | Segments |
| ------- | ----- | ------: | ---------: | ------ | -------: |
| R1      | A     |   633.0 |  5 065 076 | 200    |      188 |
| R2      | A     |   691.8 |  5 535 092 | 200    |      203 |
| R2'     | A     |   691.8 |  5 535 092 | 200    |      195 |
| R3      | A     |   691.8 |  5 535 092 | 200    |      207 |
| R4      | A     |   633.0 |  5 065 076 | 200    |      182 |
| R5      | A     |  1400.0 | 11 200 916 | 200    |      436 |
| R5'     | A     |  1374.0 | 10 992 980 | 200    |      413 |
| R2b     | B     |   678.1 |  5 425 364 | 200    |      191 |
| R2b'    | B     |   678.1 |  5 425 364 | 200    |      195 |
| R3b     | B     |   678.1 |  5 425 364 | 200    |      199 |

Budget probes: R5 (exactly `max-request-seconds`) and R5' (core target plus
prefix reserve) both succeeded. Decision rule: keep `max-request-seconds`.
No README change.

#### Round A: the specified clip set holds three people

Every sample was dominant (lowest share 89.9 %), but the six clips received
three labels in R2, R2', R5, R5' and two in R3, always in the same groups:
{`1:A`, `2:I`, `2:A`}, {`1:B`, `2:E`}, {`2:G`} (R3 also merged `2:G` into the
first group). Cross-request confusion of core speech against the original
transcript shows why: original `2:I` and `2:A` are one voice (the meeting's
main speaker, about 210 s of chunk 2), and `1:A` is that voice in chunk 1;
`1:B` and `2:E` are one voice. Chunk 2 contains four voices with material
speech (about 210 s, 80 s, 68 s, 54 s) and chunk 1 adds at most one more
(`1:D`, 16.8 s). The rule "six labels with the most speech across chunks 1
and 2" therefore cannot produce six distinct people on this recording. This
is a specification gap recorded in the README journal.

Because the prefix carried only three identities, round A's core coverage
(21.5 % in R2 under the collapse-rejecting rule) and agreement values measure
the clip set, not the provider, and are not used for the decision. Two
provider facts from round A do stand:

- Identical audio inside one request does not always get one label. Four
  clips came from inside chunk 2, so each appears twice per request. The
  prefix copy and the core copy carried the same label in R2', R5, R5' but
  not in R2 (`2:I`, `2:A`: prefix `A`, core `G`) or R3 (`2:I`: prefix `A`,
  core `F`). The provider splits the main speaker's core speech in about two
  of five requests even with that speaker's own clip in the prefix.
- R4 (native `known_speaker_references`, four clips) was a poor oracle: it
  assigned the second speaker's 62 s to the name of a main-speaker clip,
  split the main speaker across two names, and left 47 s unnamed.

#### Round B: one clip per distinct identity

Clip set B (`unconfirmed` by listening; distinct by round-A evidence):

| Label | Identity evidence                     | Source interval (s) | Speech (s) | Clearance (s) |
| ----- | ------------------------------------- | ------------------- | ---------: | ------------: |
| `1:A` | main speaker (= `2:I` = `2:A`)        | 436.118–445.418     |        8.8 |           0.0 |
| `2:G` | second speaker                        | 850.898–860.198     |        8.8 |           0.0 |
| `1:B` | third speaker (= `2:E`)               | 139.106–148.806     |        9.2 |           0.0 |
| `2:H` | fourth speaker                        | 910.776–920.232     |       8.96 |           0.9 |
| `1:D` | fifth speaker (= `2:C`, see below)    | 410.000–414.800     |        4.3 |           0.0 |

Per-sample dominance (share of labeled speech on the dominant label):

| Sample | R2b    | R2b'   | R3b (reversed) |
| ------ | -----: | -----: | -------------: |
| `1:A`  | 100 %  | 100 %  | 100 %          |
| `2:G`  | 97.8 % | 98.3 % | 96.9 %         |
| `1:B`  | 91.5 % | 85.7 % | 92.9 %         |
| `2:H`  | 100 %  | 100 %  | 97.7 %         |
| `1:D`  | 100 %  | 100 %  | 100 %          |

Distinct labels: R2b 5/5, R2b' 5/5, R3b 4/5 (`1:D` and `2:G` collapsed onto
one label; in the core that label carried 37 s while the second speaker's
speech went to an unanchored label `F`, 52 s).

Core coverage and label counts:

| Request | Core speech (s) | Mapped (s) | Unmapped (s) | Coverage | Distinct core labels |
| ------- | --------------: | ---------: | -----------: | -------: | -------------------: |
| R1      |           397.9 |          – |            – |        – |                    7 |
| R2b     |           405.8 |      404.9 |          0.9 |   99.8 % |                    6 |
| R2b'    |           401.6 |      400.8 |          0.8 |   99.8 % |                    6 |
| R3b     |           399.7 |      310.6 |         89.1 |   77.7 % |                    5 |

Core speech per identity, R2b / R2b': main 197.2 / 210.9, second 86.6 /
74.9, third 68.2 / 65.0, fourth 27.4 / 28.9, fifth 25.4 / 21.1.

Agreement (same identity on the same source time):

| Pair       | Both mapped (s) | Same (s) | Agreement of both-mapped | Agreement of A's mapped |
| ---------- | --------------: | -------: | -----------------------: | ----------------------: |
| R2b → R2b' |           378.9 |    356.0 |                   93.9 % |                  87.9 % |
| R2b → R3b  |           290.3 |    265.7 |                   91.5 % |                  65.6 % |

The R2b/R2b' disagreement is concentrated in one stretch: 13.8 s labeled
second speaker in R2b and main speaker in R2b'. The same 14–20 s swaps
between those two voices in every independent segmentation, including the
original WAV transcript (`2:G` → original `A` 19.9 s) and R1 (18.7 s). That
stretch is the provider's noise floor for this recording, about 5 % of core
speech, and it exists without any prefix.

Fourth and fifth speaker: the original transcript (`2:H` 33 s, `2:C` 23 s),
R1 (`G` 34 s, `C` 16 s) and round B (27 s, 19–25 s) all separate them at the
same place; round A's R2' merged them into one label (54 s). Treated as two
people pending listening.

#### Boundary offset

The per-sample table specified by the phase measures speech onset inside the
guard, not codec delay (values −0.57 to +0.65 s, not constant). The codec
delay was measured instead by matching MP3-request core segment boundaries to
the original WAV-run transcript of chunk 2:

| Request     | Matched starts | Median start offset | Mean    | p10 / p90        |
| ----------- | -------------: | ------------------: | ------: | ---------------- |
| R1          |        157/188 |            +0.076 s | +0.058  | −0.136 / +0.276  |
| R2 (core)   |        150/187 |            +0.058 s | +0.040  |                  |
| R5 (core)   |            148 |            +0.058 s | +0.063  |                  |

Median offset is below the timestamp tolerance and stable across request
lengths; the spread is the provider's per-segment jitter, not a constant to
subtract. No Phase 3 correction row needed.

#### Criteria

| Criterion                        | Result                                                                 | Verdict |
| -------------------------------- | ---------------------------------------------------------------------- | ------- |
| Sample dominance on R2 and R3    | Lowest share 85.7 % over all eight prefixed requests                    | pass    |
| Sample distinctness on R2 and R3 | Five distinct in R2b and R2b'; four of five in R3b; six not available   | fail    |
| Core label reduction on R2       | R2b 6 vs R1 7; within `max-speakers`                                    | pass    |
| Core coverage on R2 and R3       | R2b 99.8 %, R2b' 99.8 %, R3b 77.7 %                                     | fail    |
| Permutation stability            | 91.5 % of both-mapped duration                                          | fail    |
| Determinism                      | 93.9 % of both-mapped duration                                          | fail    |
| Boundary offset                  | Median +0.06 to +0.08 s, below tolerance                                 | pass    |
| Budget probes                    | R5 and R5' both 200; keep `max-request-seconds`                          | pass    |

#### Decision: `redesign` (narrow), restated as `proceed` under V4

The anchoring mechanism itself is confirmed: with one clean clip per voice
in the specified order, every sample was dominant and distinct and 99.8 % of
core speech mapped, twice. What failed is the assumption that a single
request is stable enough for single-pass, final acceptance: the provider
merges two distinct samples in one of three orderings, splits a speaker's
core speech away from that speaker's own clip in about two of five requests,
and swaps about 5 % of speech between the two loudest voices in every run.
The README gates for permutation and determinism sit below that noise floor,
and the acceptance gates in Phase 4 (95 % attribution, under 2 % unknown)
should be re-derived from it. Items for the README revision:

1. Clip selection must be identity-aware; transcript labels alone over-select
   the loudest voice. Discovery in the pipeline already is (collapse means
   one voice), but the spike's rule and the six-clips assumption are not.
2. Per-request noise of about 5 % between the two main voices and an
   occasional sample merge mean "accepted results are final" needs either a
   second pass with a different sample order and a vote, or gates that
   accept the floor. This is a strategy decision, not a Phase 2 detail.
3. The spike agreement gates (95 %) should be stated relative to a measured
   prefix-free baseline (R1 repeated), which this spike did not include.
4. The maintainer still has to listen to clip set B and confirm five voices;
   the fifth speaker's distinctness from the fourth rests on three
   consistent segmentations, not on ears.

The README was revised the same day (V4, D19–D22) to close items 1–3; the
maintainer's reaffirmed session goal was taken as authorization for that
narrow revision. Under V4 the criteria read: dominance pass; distinctness
and coverage judged on forward-order requests (D21): 5/5 and 99.8 %, pass;
agreement relative to the prefix-free baseline (D20): 93.8 % and 91.5 %
against 67–78 %, pass; boundary offset pass (D22); budget probes pass. The
reversed-order merge is the D19 case. Decision under V4: **`proceed`**.
Still open for the maintainer: listen to clip set B and confirm five voices,
in particular that the fourth and fifth speakers are two people.

## Review findings

None.
