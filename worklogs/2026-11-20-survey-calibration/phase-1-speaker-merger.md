# Phase 1 — Speaker merger

**Parent:** root · **Class:** contract · [README](README.md)

## Goal

Turn the survey's candidate clips into one registry of at most 8 speakers, by the chained merge that H1
proved: chunk 1's candidates seed the voices, and each later chunk is one link of two clips-only requests
whose two orders must agree before two clips count as one speaker (I2).

## Interface

Written to `internal/audio/merge.go`. Two existing files gain one member each, declared here because the
merger cannot be called without them: `Speaker.Clips` in `internal/audio/registry.go`, and the clips-only
request in `internal/audio/assemble.go` (`Assemble.1`–`Assemble.3`). Nothing calls the merger yet; phase 2
wires it.

```go
package audio

import (
	"context"
	"time"
)

// maxVoices is the room the fixed prefix reserve holds: 8 slots of 11 s. (I5)
const maxVoices = 8

// The two requests of one merge link.
const (
	RequestMergeForward  RequestKind = "merge-forward"
	RequestMergeReversed RequestKind = "merge-reversed"
)

// SurveyCandidate is the best clip of one material label of one survey
// chunk.
type SurveyCandidate struct {
	// Chunk is the 0-based index of the planned core the label was heard in.
	Chunk int
	// Candidate is the clip as SampleExtractor ranked it: Label, Clip,
	// Speech, Clearance.
	Candidate
	// LabelSpeech is all the survey speech of Label in its chunk.
	LabelSpeech time.Duration
}

// ID names the clip in a merge request and in the report.
//
// - ID.1 — The ID is "c<Chunk+1>-<Label>", for example "c2-A" for label A
//   of the second chunk. Two candidates Merge accepts never share one
//   (Merge.10).
func (c SurveyCandidate) ID() string

// CandidateError refuses a candidate before any request is sent.
//
// - CandidateError.1 — Reason is exactly one of "empty clip" (Clip.End <=
//   Clip.Start), "negative chunk" and "duplicate label" (the chunk and
//   label of an earlier candidate), checked in that order, and Candidate is
//   the first candidate of the slice that fails.
// - CandidateError.2 — Error is
//   "candidate <ID> of chunk <Chunk+1>: <Reason>".
type CandidateError struct {
	Candidate SurveyCandidate
	Reason    string
}

func (e *CandidateError) Error() string

// ClipVerdict is what one clip received in one merge request.
type ClipVerdict struct {
	// Label is the request-local label holding most of the clip's labelled
	// speech; "" when nothing in the clip was labelled.
	Label string
	// Share is Label's part of the clip's labelled speech, 0 to 1.
	Share float64
	// Settled is Share >= 0.80 (the dominance threshold of LabelMapper).
	Settled bool
}

// LinkOutcome says what one link did with one clip.
type LinkOutcome string

const (
	// LinkHeld: a voice's clip, settled in both orders; the voice goes on.
	LinkHeld LinkOutcome = "held"
	// LinkMerged: a voice's clip that shared both labels with an earlier
	// voice's clip; the two voices are one from here on.
	LinkMerged LinkOutcome = "merged"
	// LinkJoined: a candidate that shared both labels with a voice's clip
	// and joined that voice.
	LinkJoined LinkOutcome = "joined"
	// LinkNew: a candidate that shared both labels with no voice's clip and
	// began a voice.
	LinkNew LinkOutcome = "new"
	// LinkUnsettled: a clip not settled in both orders. A candidate is then
	// in no voice; a voice is unchanged by the link.
	LinkUnsettled LinkOutcome = "unsettled"
)

// LinkClip is one clip of one link.
type LinkClip struct {
	// ID is the voice ("voice-3") for a voice's clip, SurveyCandidate.ID for
	// a candidate.
	ID       string
	Clip     SourceInterval
	Forward  ClipVerdict
	Reversed ClipVerdict
	Outcome  LinkOutcome
	// Voice is the voice the clip belongs to after the link; "" for an
	// unsettled candidate.
	Voice string
}

// MergeLink is one link: the voices so far against one chunk's candidates.
type MergeLink struct {
	// Chunk is the 0-based chunk whose candidates the link carried.
	Chunk int
	// Clips are in the forward request's order.
	Clips []LinkClip
}

// MergedVoice is one voice the merge found.
type MergedVoice struct {
	// Voice is "voice-N", N counting voices in the order they began.
	Voice string
	// Speaker is the registry ID, or "" for a voice over the cap of 8.
	Speaker string
	// Clips are the voice's candidates, best first.
	Clips []SurveyCandidate
	// SurveySpeech is the sum of the clips' LabelSpeech.
	SurveySpeech time.Duration
}

// MergeReport says what the merge heard and decided, without transcript
// text.
type MergeReport struct {
	// Links holds one entry per link sent, in chunk order.
	Links []MergeLink
	// Voices holds every voice found: the kept ones in speaker order, then
	// the ones over the cap, most survey speech first.
	Voices []MergedVoice
}

// Found counts the voices the merge found.
//
// - Found.1 — Found is len(Voices): the kept voices and the ones over the
//   cap of 8, so it is the count Run.5's line names. A voice merged into an
//   earlier one in a link is not counted, and neither is an unsettled
//   candidate.
func (r MergeReport) Found() int

// Merger builds the speaker registry of one recording from its survey
// candidates.
type Merger interface {
	// Merge returns the registry and the report of how it was reached.
	//
	// - Merge.1 — Seed. The candidates of the lowest chunk that has any
	//   each begin one voice, and no request is sent for that chunk: the
	//   survey already kept one chunk's voices apart.
	// - Merge.2 — Links. Every later chunk that has candidates is one link,
	//   in chunk order, each started only when the link before it is
	//   decided. A link is exactly two requests, both in flight at once:
	//   RequestMergeForward and RequestMergeReversed, each with a zero core
	//   and the same clips — the best clip of every voice so far and that
	//   chunk's candidates (SampleClip.Candidate true for the candidates
	//   only), ordered by Clip.Start and then ID in the forward request and
	//   in exactly the opposite order in the reversed one. A chunk without
	//   candidates sends nothing. Nothing else is ever sent. (I8)
	// - Merge.3 — Verdict. A clip is settled in a request when one label
	//   holds at least 80 % of its labelled speech; a clip with no labelled
	//   speech is not settled. A clip settled in both requests of a link has
	//   the key (forward label, reversed label). Labels of two requests are
	//   never compared with each other. (I3)
	// - Merge.4 — Decision, from the link's two answers only (I2):
	//   voices whose clips have the same key become one voice, which keeps
	//   the earlier voice's name and holds the clips of both; a candidate
	//   with the key of a voice's clip joins that voice; candidates sharing
	//   a key that no voice's clip has begin one voice together; a candidate
	//   without a key joins nothing and begins nothing; a voice whose clip
	//   has no key is unchanged. Nothing is grouped by time, text or count.
	// - Merge.5 — Ranking. A voice's clips are ranked by longer Speech,
	//   then larger Clearance, then earlier Clip.Start; its best clip is the
	//   first.
	// - Merge.6 — Registry. When more than 8 voices are found, the 8 with
	//   the most SurveySpeech are kept, ties going to the voice whose
	//   earliest clip starts first. The kept voices are numbered
	//   "speaker-1", "speaker-2", … by the start of each voice's earliest
	//   clip. Snapshot.Speakers holds them in that order; each Speaker has
	//   its ID, Clips (ranked, each SampleClip.ID the speaker's ID,
	//   Candidate false) and Sample equal to Clips[0]. Every other field of
	//   Speaker and Snapshot is zero. (I4, I10)
	// - Merge.7 — Report. Links lists every clip of every link with both
	//   verdicts and its outcome; Voices lists every voice found, kept or
	//   not. The report carries no segment text.
	// - Merge.8 — Deterministic. The same candidates and the same answers
	//   give an equal Snapshot and an equal report, whatever the order of
	//   the candidates slice and whichever request of a link answers first.
	//   Voices are named "voice-1", "voice-2", … in the order they begin:
	//   the seed's by Clip.Start and then ID, a link's new voices by the
	//   position in the forward request of each one's first clip. A name is
	//   never reused: a voice merged into an earlier one leaves its name
	//   unused. (I10)
	// - Merge.9 — No candidates give an empty Snapshot, an empty report and
	//   a nil error, with no request sent: a recording with nothing to
	//   label is not a failure. (I7)
	// - Merge.10 — A candidate with Clip.End <= Clip.Start, a negative
	//   Chunk, or the chunk and label of another candidate fails the merge
	//   with a *CandidateError before any request.
	// - Merge.11 — A request that fails ends the merge: the link's other
	//   request is cancelled, and the error wraps the cause (errors.Is and
	//   errors.As reach it) and names the link as "merge link for chunk N"
	//   with N counted from 1. A context that ends gives its error. The
	//   Snapshot is then zero.
	// - Merge.12 — The merger reads the candidates and the answers and
	//   nothing else: no voice library, no question, no state kept between
	//   calls. (I1, I4)
	Merge(ctx context.Context, candidates []SurveyCandidate) (Snapshot, MergeReport, error)
}

// ChainMerger is the Merger. Transcribe sends one request and is called
// with a zero core only.
type ChainMerger struct {
	Transcribe RequestFunc
}

func (m ChainMerger) Merge(ctx context.Context, candidates []SurveyCandidate) (Snapshot, MergeReport, error)
```

Added to `internal/audio/registry.go`:

```go
type Speaker struct {
	// … today's fields, untouched in this phase …

	// Clips are the speaker's clips, best first; Sample is Clips[0]. Empty
	// for a speaker the old path registered.
	Clips []SampleClip
}
```

Added to `internal/audio/assemble.go` (the member the README's Imports table gives to this phase):

```go
// ErrEmptyRequest refuses a request with no sample and no core
// (Assemble.2); callers match it with errors.Is.
var ErrEmptyRequest = errors.New("request has no audio: no sample and no core")

// AssembleRequest.Core may be the zero SourceInterval:
//
// - Assemble.1 — With samples and a zero Core the request holds the samples
//   and their gaps only. No core audio is extracted. The manifest ends with
//   a core region of zero length at the end of the request, CoreStart and
//   CoreEnd are 0, and Duration equals PrefixDuration. Both budgets are
//   checked as for any request. (I5)
// - Assemble.2 — With no sample and a zero Core, Assemble returns
//   ErrEmptyRequest and leaves no file behind.
// - Assemble.3 — A request with a core is assembled exactly as today: the
//   same regions, bytes and RequestHash.
```

Consumes:

- `RequestFunc`, `RequestKind` (`internal/audio/discovery.go`, `internal/audio/calibration_log.go`; repository)
- `Candidate`, `better` (`internal/audio/sample.go`; repository)
- `LabelMapper.Map`, `SampleResult` (`Label`, `Share`, `Labeled`), `dominantShare` (`internal/audio/mapping.go`;
  repository, unchanged: the merger reads `Mapping.Samples` only)
- `RequestAssembler`, `AssembleRequest`, `RequestManifest`, `SampleClip`, `SourceInterval`
  (`internal/audio/assemble.go`, `internal/audio/plan.go`; repository)
- `Snapshot`, `Speaker` (`internal/audio/registry.go`; repository)
- Test doubles: the scripted diarizer `voiceProvider` and `arithmeticAssembler`
  (`internal/audio/discovery_fakes_test.go`), `manifestFor` (`internal/audio/mapping_test.go`), the fake
  runner `scriptedRunner` (`internal/audio/calibration_fakes_test.go`). No external system; the provider's
  behaviour in a clips-only request is the one H1 measured (the README's H1 result, and the H1 spike's
  `step_chain` (notebook note `codebases/imago/clai/2026-11-22T10-19-30Z_merge-spike-many-clips-in-one-request-merge-two-voices.md`)).

## Acceptance criteria

| #   | Criterion                                                                                                       | Proves                    | Proof                                                        |
| --- | --------------------------------------------------------------------------------------------------------------- | ------------------------- | ------------------------------------------------------------ |
| 1   | One person heard in every chunk under a different label each time is one speaker with one clip per chunk        | Merge.1–Merge.4, I2, I3   | `TestMergerContract/one_person_across_chunks`                |
| 2   | Two people who share a label in one order only stay two speakers                                                | Merge.3, Merge.4, I2      | `TestMergerContract/one_order_is_not_enough`                 |
| 3   | A link is two requests at once with the same clips in opposite orders, and links run in series                  | Merge.2, I8               | `TestMergerContract/link_shape`                              |
| 4   | Two voices whose clips share both labels in a later link become one voice                                       | Merge.4                   | `TestMergerContract/two_voices_become_one`                   |
| 5   | A clip that is mixed or unlabelled in either order joins nothing; a voice in that state is unchanged            | Merge.3, Merge.4          | `TestMergerContract/unsettled_clips`                         |
| 6   | Ten voices found keep the 8 with the most survey speech, numbered by earliest clip, and the report says 10      | Merge.6, Merge.7, Found.1, I10 | `TestMergerContract/more_than_eight`                         |
| 7   | The same answers give the same registry for any candidate order and any answer order                            | Merge.8, ID.1, I10        | `TestMergerContract/deterministic`                           |
| 8   | No candidates is an empty registry, no request and no error                                                     | Merge.9, I7               | `TestMergerContract/nothing_to_merge`                        |
| 9   | A bad candidate is refused before any request; a failed request ends the merge with its cause                   | Merge.10, Merge.11, CandidateError.1–2 | `TestMergerRefusesBadCandidates`, `TestMergerRequestFailure` |
| 10  | A clips-only request holds the clips and gaps only, is checked against both budgets and runs no core extraction | Assemble.1–Assemble.3, I5 | `TestAssemblerContract`                                      |
| 11  | The merger keeps nothing between calls and reads nothing but its input                                          | Merge.12, I1, I4          | `TestMergerContract/independent_calls`                       |
| 12  | The gate passes unedited, `merge.go` is covered ≥ 90 %, and no flag, config field or path is added              | I9, I11                   | `make qa`; `go test ./internal/audio -run 'Merger\|Assembler' -cover` |

## Test cases

`TestMergerContract` is one table run against every `Merger` (today `ChainMerger`), with the scripted
diarizer as its `RequestFunc`. `TestAssemblerContract` is one table run against every `RequestAssembler`:
`AudioAssembler` over the fake runner, and the `arithmeticAssembler` fake.

| Test | Level | Proves | Given | Then | Must not |
| ---- | ----- | ------ | ----- | ---- | -------- |
| `TestMergerContract/one_person_across_chunks` | contract suite | Merge.1–Merge.4, Merge.6, I2, I3 | 4 chunks, 3 people; each chunk's candidates carry that chunk's own letters (the same person is `A` in chunk 1 and `C` in chunk 2) | 3 speakers; each holds one clip per chunk it spoke in; 3 links, 6 requests | Group two clips because their survey labels are equal; send a request for chunk 1 |
| `TestMergerContract/one_order_is_not_enough` | contract suite | Merge.3, Merge.4, I2 | The diarizer gives two people one label in the forward request of link 2 and different labels in the reversed one | Two speakers; the candidate begins its own voice (`LinkNew`) | Join on one order |
| `TestMergerContract/one_order_is_not_enough/merge-reversed_only` | contract suite | Merge.3, Merge.4, I2 | The mirror of `one_order_is_not_enough`: two people on one label in the reversed request of link 2 only | Two speakers; `c2-A` reports `LinkNew` | Join on the reversed order alone |
| `TestMergerContract/link_shape` | contract suite | Merge.2, I8 | 3 chunks; the diarizer holds each request until both requests of the link are in flight | Per link: kinds `merge-forward` and `merge-reversed`, a zero core, the best clip of each voice so far plus that chunk's candidates, forward sorted by start, reversed exactly opposite; link 3 starts after link 2 answered; a chunk with no candidates sends nothing | Send a core; send link 3 before link 2 is decided; send more than 2 requests per link |
| `TestMergerContract/two_voices_become_one` | contract suite | Merge.4, Merge.5, Merge.7 | Chunk 1 holds two candidates of one person (the survey split her); link 2 gives both voices' clips one label in both orders | One speaker holding both clips and chunk 2's; the later voice's clip reports `LinkMerged`; link 3 carries one clip for her, the best | Keep two speakers; carry both clips into link 3 |
| `TestMergerContract/unsettled_clips` | contract suite | Merge.3, Merge.4, Merge.7 | Rows: a candidate 50/50 between two labels in the forward request; a candidate with no labelled speech; a candidate at 79 %; a voice's clip mixed in the reversed request | Each such candidate reports `LinkUnsettled` with `Voice ""` and is in no speaker; the voice with the mixed clip keeps its clips and gains none in that link; a candidate at exactly 80 % is settled | Begin a voice from an unsettled clip; drop the voice |
| `TestMergerContract/unsettled_clips/two_clips_covered_only_by_unlabelled_segments` | contract suite | Merge.3, Merge.4, I2, L1.7 | A voice's clip and another person's candidate are covered only by segments with an empty speaker, in both orders | Both report `LinkUnsettled` with zero verdicts; the candidate joins nothing; the two stay apart | Settle a clip on the empty label; make the two one speaker |
| `TestMergerContract/more_than_eight` | contract suite | Merge.6, Merge.7, Found.1, I10 | 10 people across 3 chunks with distinct survey speech; a second row with two voices tied on speech at the cut | `Found()` is 10; 8 speakers, the ones with the most survey speech; `speaker-N` ordered by earliest clip; the two dropped voices are in `Voices` with `Speaker ""`; the tie keeps the voice whose earliest clip starts first | Keep 10; number by speech; lose a dropped voice from the report |
| `TestMergerContract/deterministic` | contract suite | Merge.5, Merge.8, ID.1, I10 | The `one_person_across_chunks` script run with the candidates shuffled, and with the reversed request answering before the forward one | Equal `Snapshot` and equal `MergeReport` in every run, the `voice-N` names included: the seed's voices in clip order, a link's new voices in forward-request order, the name of a merged voice not reused; candidate IDs read `c<chunk>-<label>`; each speaker's `Sample` is its longest clip, then the one with most clearance, then the earliest | Depend on slice order or on which request answers first |
| `TestMergerContract/deterministic/names_follow_clip_order` | contract suite | Merge.8, I10 | Labels against clip order: `c1-A` at 50 s, `c1-B` at 10 s; the last link begins `c3-B` (230 s) before `c3-A` (260 s) | `c1-B` is `voice-1`, `c1-A` `voice-2`; `c3-B` is `voice-3`, `c3-A` `voice-4` | Name voices by ID |
| `TestMergerContract/nothing_to_merge` | contract suite | Merge.1, Merge.9, I7 | Rows: no candidates; candidates in one chunk only; chunk 1 without candidates and chunks 2 and 3 with | Row 1: empty registry, nil error, 0 requests. Row 2: one speaker per candidate, 0 requests. Row 3: chunk 2 seeds, one link for chunk 3 | Return an error; send a request for the seeding chunk |
| `TestMergerContract/independent_calls` | contract suite | Merge.12, I1, I4 | The same `ChainMerger` value merges two different recordings in turn, with `HOME` holding no voice library | The second result equals a fresh merger's result | Carry voices from the first call into the second |
| `TestMergerRefusesBadCandidates` | unit | Merge.10, CandidateError.1–2 | Rows: `Clip.End <= Clip.Start`; `Chunk` −1; two candidates with one chunk and label | `*CandidateError` with the reason and the first failing candidate; a candidate with two faults reports the first in the order of CandidateError.1; the text is `candidate c2-A of chunk 2: duplicate label`; 0 requests | Send a request first |
| `TestMergerRequestFailure` | unit | Merge.11 | Rows: the forward request of link 2 fails with a `claierr` transport error while the reversed one waits; the context is cancelled during link 2 | The error is the cause under `errors.Is`, its text holds `merge link for chunk 2`; the waiting request saw its context end; the Snapshot is zero. Row 2: the context's error | Return a partial registry; start link 3 |
| `TestAssemblerContract` | contract suite | Assemble.1–Assemble.3, I5 | Rows per assembler: 3 samples and a zero core; no sample and a zero core; 2 samples and a core (today's request); 3 samples over the seconds budget; (fake runner only) an encoded size over the byte budget | Row 1: regions are sample, gap ×3 then a zero-length core region; `Duration == PrefixDuration`; `CoreStart == CoreEnd == 0`; the fake runner saw no extraction for a core. Row 2: `ErrEmptyRequest`, run directory empty. Row 3: the manifest and `RequestHash` equal today's (`TestAssemblerIsDeterministic` stays green unedited). Rows 4–5: the existing budget errors | Run ffmpeg for an empty core; skip a budget check for a clips-only request |
| `go test ./internal/audio -run 'Merger\|Assembler' -coverprofile=c.out && go tool cover -func=c.out \| grep merge.go` | command | criterion 12 | The repository after the phase | `merge.go` at 90 % or more; the figure written in Implementation notes | Leave `c.out` in the tree |
| `git diff --stat` of the phase, and `grep -n 'Register(' internal/audio/cmd.go` against the start | command | criterion 12, I9 | The repository after the phase | No new flag, no new field in `TranscribeConfig`, no caller of `ChainMerger` outside tests | Wire the merger into `Run` (phase 2's) |
| `make qa` | command | I9, I11 | The repository after the phase | Exit 0, twice | Edit the gate, add a sleep, a skip or a real ffmpeg call |

## Error management

| Failure | Expected outcome | Test |
| ------- | ---------------- | ---- |
| A candidate with an empty clip, a negative chunk, or a chunk and label already given | `*CandidateError{Candidate, Reason}`; no request sent; zero Snapshot and zero `MergeReport` | `TestMergerRefusesBadCandidates` |
| A merge request fails (transport, auth, budget, any `RequestFunc` error) | Error wrapping the cause, text `merge link for chunk N: <cause>`; the link's other request is cancelled; zero Snapshot and zero `MergeReport`; no later link | `TestMergerRequestFailure` |
| The context ends | The context's error; zero Snapshot and zero `MergeReport` | `TestMergerRequestFailure` |
| A merge request answers with no error but no manifest (nil, or one without regions) | The link fails: `merge link for chunk N: <kind> request answered without a manifest`; zero Snapshot and zero `MergeReport`; no later link | `TestMergerRequestFailure/a_request_answers_without_a_manifest` |
| A clip mixed, missing or below 80 % in a request | Not a failure: `LinkUnsettled` in the report | `TestMergerContract/unsettled_clips` |
| More than 8 voices | Not a failure: 8 kept, the rest reported with `Speaker ""` | `TestMergerContract/more_than_eight` |
| No candidates | Not a failure: empty registry, nil error | `TestMergerContract/nothing_to_merge` |
| A request with no sample and no core | `ErrEmptyRequest`; no file left | `TestAssemblerContract` |
| A clips-only request over a budget | Today's budget error from `Assemble` (`exceeds max-request-seconds`, `exceeds max-request-bytes`) | `TestAssemblerContract` |

## Golden diff

None.

## Removes

None.

## Local facts

| ID   | Fact | Decided by |
| ---- | ---- | ---------- |
| L1.1 | The algorithm is the H1 spike's `step_chain` (notebook note `codebases/imago/clai/2026-11-22T10-19-30Z_merge-spike-many-clips-in-one-request-merge-two-voices.md`), the run H1 measured (4 links of 9–13 clips, 7 voices, one pair of voices merged in a link). Like the spike, a link is always exactly two requests (I8); the spike's `-m<merges>` suffix only renamed a request in its ledger. | README H1 result, I8 |
| L1.2 | The verdict is `LabelMapper.Map`'s per-sample `Label` and `Share` over the clip's region; `Mapping.Samples[i].Status`, `Core` and `Unmapped` of a clips-only request are not read. `Map` is unchanged. | README Imports |
| L1.3 | A link's two requests are in flight at once, so the scripted diarizer's faults for this phase are keyed by what a request holds (its kind, its clip order, a clip's source interval), never by its request number. The existing number-keyed faults stay for the tests that use them. | I11 |
| L1.4 | Before the first change, the worker writes the I9 baseline in Implementation notes: the output of `find internal/audio -name '*.go' ! -name '*_test.go' -print0 \| xargs -0 cat \| wc -l` (9,942 in the working tree at plan time, 2026-11-22). Phase 3 compares against it. | I9 |
| L1.6 | The `arithmeticAssembler` fake is brought under `TestAssemblerContract`: it returns `ErrEmptyRequest` for an empty request and checks an optional seconds budget, zero meaning none, so the coordinator tests that build `&arithmeticAssembler{}` are untouched. | Assemble.1–Assemble.3; V1 note |
| L1.5 | A voice clip in a link carries the ID `voice-N`; a candidate carries `SurveyCandidate.ID()`. `speaker-N` exists only in the returned registry. | this phase |
| L1.7 | A segment whose speaker is empty is not a label: the merger drops such segments before `LabelMapper.Map` (unchanged), so they settle no clip and make no two clips one speaker. A clip covered only by them has no labelled speech (`ClipVerdict{Label: "", Share: 0, Settled: false}`). | I2, `ClipVerdict` doc; orchestrator on review R1-04, 2026-11-22 |

## Human required

None.

## Implementation notes

| Row | Date | What | Gates |
| --- | ---- | ---- | ----- |
| fix-start | 2026-11-22 | V1 notes | not run: documents only, no code touched |
| fix-end | 2026-11-22 | V1 notes closed in this file | not run: documents only, no code touched |
| fix-start | 2026-11-22 | R1-01–R1-04 (review 1) | `make qa` exit 0, 108 packages ok · `go vet ./...` clean · dupl 49 groups |
| fix-end | 2026-11-22 | R1-01–R1-04 closed: mirrored `one_order_is_not_enough/merge-reversed_only`; `deterministic/labels_against_clip_order` and `names_follow_clip_order`; Error management rows for the zero report and the missing manifest; L1.7 (empty speaker is no label, filtered before `Map`) with `unsettled_clips/two_clips_covered_only_by_unlabelled_segments`; phase 2 L2.11. The reviewer's three probes (reversed-only key, seeds by ID, new voices by ID) each fail the new tests | twice with `GOFLAGS=-shuffle=on`: `make qa` exit 0, 108 packages ok (`internal/audio` 93.8 %) · `go vet ./...` clean · dupl 49 groups; `merge.go` 100 % (171 of 171) |

I9 baseline (L1.4), before the first change, 2026-11-22: `find internal/audio -name '*.go' ! -name '*_test.go' -print0 | xargs -0 cat | wc -l` → **9942**.

| Run   | Commit      | Command                                                                                     | Result |
| ----- | ----------- | ------------------------------------------------------------------------------------------- | ------ |
| start | 0ca840b+wip | `make qa` · `go vet ./...` · `go run github.com/mibk/dupl@latest -t 80 .` (API keys unset)  | qa exit 0, 108 packages ok, 0 failed (`internal/audio` 93.3 %) · vet clean · dupl 49 clone groups |
| end   | 0ca840b+wip | the same three, `GOFLAGS=-shuffle=on` (fresh random test order)                             | qa exit 0, 108 packages ok, 0 failed (`internal/audio` 93.8 %) · vet clean · dupl 49 clone groups (none in the new files) |
| end   | 0ca840b+wip | the same three, `GOFLAGS=-shuffle=on` (fresh random test order)                             | qa exit 0, 108 packages ok, 0 failed (`internal/audio` 93.8 %) · vet clean · dupl 49 clone groups (none in the new files) |

Criterion 12: `go test ./internal/audio -run 'Merger|Assembler' -coverprofile=<scratch>/c.out` → `merge.go` **100 %**
(169 of 169 statements; no `c.out` left in the tree). No caller of `ChainMerger` outside `merge.go` and tests; no
`Register(` line, flag or `TranscribeConfig` field added. Non-test lines in `internal/audio/` after the phase: 10,508
(baseline 9,942; `merge.go` is 530 of them, most of it the signed interface and its clauses).

What landed:

- `internal/audio/merge.go`: the Interface block verbatim, with `ChainMerger` (seed, chained links of two concurrent
  requests, Merge.4 decision, ranking, registry of at most 8).
- `internal/audio/registry.go`: `Speaker.Clips`. `internal/audio/assemble.go`: `ErrEmptyRequest`, Assemble.1–3 (a zero
  core extracts nothing), and `checkRequestSeconds`, shared with the fake so both give today's seconds-budget error.
- Tests: `TestMergerContract` (9 named subtests, each annotated with the clauses it proves), `TestMergerRefusesBadCandidates`,
  `TestMergerRequestFailure` in `merge_test.go`; `TestAssemblerContract` in `assemble_test.go` over `AudioAssembler` and
  `arithmeticAssembler` (L1.6: `ErrEmptyRequest`, optional `maxRequestDuration`, zero = none).
- `merge_fakes_test.go`: `mergeDiarizer`, the scripted diarizer for requests in flight together (L1.3). It labels from
  `voiceProvider`'s truth (unchanged; called with request number 0, so no number-keyed fault fires), shifts letters by
  an FNV hash of the request's kind and clip IDs, and keys faults (`share`, `sameLabel`, `fail`, `block`) by kind plus
  a source instant a clip holds. `peers` holds requests until both of a link are in flight; `reversedFirst` makes the
  reversed answer first.

Andon: none.
Surprises:

- `TestAssemblerContract` row 3 pins today's `RequestHash` (`2dfe3f46…c766e995`) and `Bytes` (39,936), captured from
  `Assemble` before the zero core was added.
- Readings of the clauses, each pinned by a test: CandidateError.1 checks candidates in slice order, each against the
  reasons in order; on any Merge.10/Merge.11 error the `MergeReport` is zero as well as the Snapshot; candidates that
  begin one voice together all report `LinkNew` with that voice.
- Not in the Error management table: a `RequestFunc` that answers with no manifest fails the link
  (`merge link for chunk N: merge-reversed request answered without a manifest`) instead of panicking in `LabelMapper.Map`.
- During the phase someone else staged the whole clai working tree (`git add`), this phase's files included, so the
  index holds a mid-phase snapshot of them. This agent ran no git write.

### Validation notes (V1, 2026-11-22)

- **V1 note · check 4.** `SurveyCandidate.ID`, `MergeReport.Found`, `CandidateError.Error` and `ErrEmptyRequest` carry
  their contract in plain comments, not numbered clauses; `ID`'s format is caller-observable (phase 2's `candidate`
  and `merge` records).
- **V1 note · checks 4, 9.** Merge.8 makes `voice-N` repeatable for one implementation, but nothing orders voices that
  begin together (the seed's voices, or two candidates with fresh keys in one link). Phase 2's
  `merge voice=voice-1 speaker=speaker-1` pattern depends on that order; "voices that begin together are numbered in
  forward-request order" would pin it.
- **V1 note · check 4.** `TestAssemblerContract` runs against `arithmeticAssembler`, which today returns a manifest for
  an empty request and checks no budget. It needs `ErrEmptyRequest` and a seconds-budget check where zero means no
  limit (every coordinator test builds `&arithmeticAssembler{}` without budgets).
- **V1 note · L1.1.** `step_chain` sends exactly two requests per link; its `-m<merges>` suffix only renames the request
  in the spike's ledger. The difference L1.1 describes does not exist. Harmless.
- **V1 note · check 7.** The phase holds two interfaces (`Merger` and the `RequestAssembler` extension Assemble.1–3);
  the README Imports table assigns both here, so not filed.
- **V1 note · check 4.** Criterion 12's coverage command and its "no flag, config field or path is added" have no Test
  cases row.
- **V1 note · root, I5/I7.** A merge link's size is bounded only by the voices and the chunk's candidates. With
  `max-request-seconds` below about (voices + candidates) × 11 s a link fails its budget check and the run fails
  (Run.16) although the plan accepted the budget. At the default 1,400 s this cannot happen (H1's links: 97–137 s).

### Validation notes (V2, 2026-11-22)

- **V2 note.** Local facts list L1.6 before L1.5; the IDs are unique, the order is cosmetic.
- **V2 note · root, I5/I7.** The V1 note on merge-link size against `max-request-seconds` still applies (not addressed;
  default budget unaffected).

## Review findings

### Review 1, 2026-11-22

Verdict: **Ready.** No blocker or major finding. Four minor findings are open: three for `fix` and one for `plan`.

Gates, run twice from `components/clai` with the three API keys unset and `GOFLAGS=-shuffle=on`:

- `make qa`: exit 0 both times (108 packages ok, `internal/audio` 93.8 %).
- `go vet ./...`: clean both times.
- dupl: 49 clone groups both times, the same as at the phase's start.
- The gates left the tree unchanged (`git diff HEAD` hash before and after).

Extra checks:

- `go test ./internal/audio -run 'Merger|AssemblerContract' -race -count=100 -shuffle=on`: ok.
- `-coverprofile` to the scratch directory: `merge.go` 100 %, and no `c.out` was left in the tree.
- Non-test lines in `internal/audio/`: 10,508 (baseline 9,942).

Mutation probes ran in a scratch copy of the tree. The repository was never edited.

- **R1-01 · minor · `internal/audio/merge_test.go:299`.** Covers Merge.3, Merge.4, I2 and acceptance criterion 2.
  - Gap: `one_order_is_not_enough` gives two people one label only in the forward request.
  - Probe: a merger that keys clips on the reversed label alone (`merge.go:382` changed to `linkKey{"", r.Label}`) passes every `Merger` test.
  - Failure scenario: two people who share label A only in `merge-reversed` would become one speaker, and the suite would stay green. Half of I2 ("they share a label in both") has no evidence.
  - The shipped code keys on both labels and is correct.
  - [ ] Add the mirrored row to `TestMergerContract/one_order_is_not_enough`: the same script with `sameLabel` keyed `{RequestMergeReversed, …}`, expecting two speakers and `c2-A` reporting `LinkNew`. Confirm that the reversed-only probe fails it.
- **R1-02 · minor · `internal/audio/merge_test.go:576`.** Covers Merge.8, I10 and the Test cases row `deterministic` ("the seed's voices in clip order, a link's new voices in forward-request order").
  - Gap: in every script a chunk's labels run in clip-start order, so clip order and ID order never differ.
  - Probes: two variants pass every `Merger` test. One names seed voices by ID (the sort at `merge.go:229` on `ID()` only). The other names a link's new voices by ascending ID instead of forward position (`decide`, the loop at `merge.go:422`).
  - Failure scenario: chunk 1's label A has its best clip at 50 s and label B at 10 s. Merge.8 names `c1-B` `voice-1`; the variant names `c1-A` `voice-1`. Phase 2's `merge voice=voice-1 speaker=speaker-1` record would then change with no failing test.
  - The shipped code is correct.
  - [ ] Add a script whose seed chunk and one link's two new voices have labels against clip order. For example, `c1-A` at 50 s and `c1-B` at 10 s, and `c3-A` at 260 s and `c3-B` at 230 s each beginning a voice. Assert the `voice-N` names, and confirm that both probes fail.
- **R1-03 · minor · `internal/audio/merge.go:489` (also `:222`, `:242`); Error management.** Merge.11 admits two readings. The code takes the stricter one each time, but neither is written down.
  - (a) Every error (Merge.10, Merge.11) zeroes the `MergeReport` as well as the Snapshot. Merge.11 and Error management rows 1–3 say only "zero Snapshot".
  - (b) A `RequestFunc` that answers `(nil, nil, nil)`, or a manifest without regions, fails its link with `merge link for chunk N: merge-reversed request answered without a manifest`. No Error management row lists this.
  - Both are consistent with the signed clauses. There is no partial result, and a failure in expectation is an error. Phase 2 reads no report after a failed merge. Tests pin both behaviours. A reader of this file or of the godoc sees neither.
  - [ ] Add both to Error management: the zero report beside the zero Snapshot in rows 1–3, and a row for an answer without a manifest (test `TestMergerRequestFailure/a_request_answers_without_a_manifest`). Alternatively, ask plan to word Merge.11 as "the Snapshot and the report are then zero".
- **R1-04 · minor · owner `plan` · `internal/audio/merge.go:494`.** Covers Merge.3, I2 and `ClipVerdict`.
  - Cause: `LabelMapper.Map` counts a segment with `Speaker ""` as a label. A clip covered only by unlabelled segments therefore gets `ClipVerdict{Label: "", Share: 1, Settled: true}`.
  - Failure scenario: two such clips share the key `("", "")` and become one speaker because neither has a label. This goes against ClipVerdict's "\"\" when nothing in the clip was labelled" and against the stricter reading of Merge.3 ("a clip with no labelled speech is not settled"). The repository's own convention elsewhere is "unlabelled time is no label's" (`namer_test.go:336`).
  - Reachable only if the anchored diarizer returns a segment without `speaker`. `internal/audio/generic/parse.go:51` passes such a segment through as `Speaker ""`. No answer on this route has shown it, and H1's spike behaves the same way.
  - The same question applies to phase 2's join (Run.7, "one label holding at least 80 % of each clip's labelled speech"), so it is a cross-phase rule.
  - The clauses conflict for this input: ClipVerdict says "Settled is Share >= 0.80", but its Label doc says "" means nothing was labelled.
  - [ ] Plan decides whether "" is a label for the merge verdict and for the join. If it is not, settle only on a non-empty label, and add an `unsettled_clips` row whose clip is covered by an unlabelled segment.

Verified-good notes:

- **Interface.** The first Interface block and `merge.go` from `maxVoices` to `ChainMerger.Merge` were compared with function bodies stripped and both run through gofumpt. They are identical. `Speaker.Clips`, `ErrEmptyRequest` and Assemble.1–3 match their blocks.
  - Only the declared public members are exported. `empty`, `hasCore`, `checkRequestSeconds` and the merge helpers are private.
  - Nothing outside `merge.go` and tests calls `ChainMerger`. No flag or `TranscribeConfig` field was added. `internal/audio/cmd.go` is not among this phase's files.
- **Algorithm against H1's `step_chain`.** The two agree on:
  - the seed: the lowest chunk, with no request;
  - the link: the best clip of each voice plus the chunk's candidates, sorted by start, then exactly reversed;
  - the verdict: `Map`'s Label and Share at ≥ 0.80, with ties going to the smaller label, as the spike's `max(sorted(held))` does;
  - the decision: voices that share a key merge into the earliest begun one; a candidate joins the key's owner, or else the key's fresh voice; a clip with no key changes nothing;
  - names are never reused (the spike's `g<i>` also kept emptied groups).

  The only differences are deterministic tie-breaks where the spike relied on list order: slots with equal starts are ordered by ID, and equal-ranked clips by the earlier start (Merge.5). Candidates also carry `SampleClip.Candidate`, which affects only `Map`'s statuses, and those are not read.
- **I2, I3.** A key is formed only for a clip settled in both requests (`merge.go:381`). Keys are compared whole, so forward labels are only ever compared with forward labels, and reversed with reversed. Survey labels appear only in the candidate ID and in the per-chunk duplicate check, which stays within one request. Four probes fail the suite as they should: settling on one order (`if f.Settled`, then `if r.Settled`), keying on the forward label only, and never merging voices.
- **Merge.11, traced.** `both` runs two goroutines on a derived context and cancels it on the first error. It drains both answers from a buffered channel, so no goroutine is left behind. If the parent context has ended, it returns that error bare; otherwise it wraps the first cause with `%w` through `diag.Errorf`. In the test, `errors.Is` and `errors.As` reach `*claierr.TransportError`. No later link starts.
- **Assemble.3, checked independently.** A scratch copy of the tree was given `0ca840b`'s `Assemble`, with only the phase's helpers appended. In it:
  - `TestAssemblerContract/a_request_with_a_core_is_assembled_as_today` passes for both assemblers, so the pinned `RequestHash` `2dfe3f46…` and 39,936 bytes are today's values;
  - the zero-core and empty-request rows fail for `AudioAssembler`, so those rows tell the two versions apart.

  `TestAssemblerIsDeterministic` is unedited.
- **The executor's readings.** The zero report on error and the failed link for an answer without a manifest are both consistent with the signed clauses; R1-03 only asks that they be written down. The other two readings follow the clause text and `LinkNew`'s doc: CandidateError.1 checks the slice in order, each candidate against the reasons in order; and candidates that begin one voice together all report `LinkNew` with that voice.
- **I10, I11.**
  - Voice names come from `voiceChain.began`, and speaker numbers from each voice's earliest clip. No map iteration order reaches an output.
  - The new tests have no sleep. The fakes key their faults by kind and source instant (L1.3).
  - 100 race runs in shuffled order were all green.
- **Caveat: voice order equals clip order.** When chunk indices are in time order, as phase 2's plan produces them, a voice's begin order always equals its earliest-clip order. Tests therefore cannot tell Merge.6's numbering and tie rule apart from begin order. This is harmless, but a caller that passed chunks out of time order would see the two diverge.
- **Caveat: untyped error for a missing manifest.** The cause in R1-03 (b) is a plain `fmt.Errorf`, so callers cannot match it. Adding a sentinel would add a public member the interface does not declare. The case cannot arise from phase 2's request function, which always returns a manifest when its error is nil.
- **Caveat: `better` is not reused.** Consumes lists `better`, but `chainVoice.add` repeats its ordering and adds Merge.5's start tie-break. dupl does not flag it.
