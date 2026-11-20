# Phase 5 — Speaker namer

**Parent:** root · **Class:** contract · [README](README.md)

## Goal

Give the speakers of one transcribed recording the names of the selected people, by sending the picked
clips and one candidate clip per transcript speaker through the transcriber in two orders and naming
only by agreement (L5.1), and say in a typed report who was named, by which clip, and why anyone was not.

## Interface

Written to `internal/audio/namer.go`.

```go
package audio

import (
	"context"
	"time"

	"github.com/baalimago/clai/internal/audio/voices"
)

// Order is the sequence of the picked clips in one naming request. (L5.1)
type Order string

const (
	// OrderForward: the picks in the selection's order, each person's picks
	// best first.
	OrderForward Order = "forward"
	// OrderReversed: the same picks in exactly the opposite sequence.
	OrderReversed Order = "reversed"
)

// NamingStatus is the outcome of one naming as a whole.
type NamingStatus string

const (
	// NamingNamed: both orders were answered and a speaker took a name.
	NamingNamed NamingStatus = "named"
	// NamingNoAgreement: both orders were answered and nobody took a name.
	NamingNoAgreement NamingStatus = "no-agreement"
	// NamingNotDiarized: the transcript has no speaker label. (F1)
	NamingNotDiarized NamingStatus = "not-diarized"
	// NamingNoCandidate: no transcript speaker has a candidate clip.
	NamingNoCandidate NamingStatus = "no-candidate"
	// NamingNoVoice: the selection holds no pick; every person named is
	// unknown to the library.
	NamingNoVoice NamingStatus = "no-voice"
	// NamingLibraryUnreadable: people were named and the library could not
	// be read (Selection.Unreadable). (F4)
	NamingLibraryUnreadable NamingStatus = "library-unreadable"
	// NamingRequestFailed: a naming request could not be built or was not
	// answered. (F4)
	NamingRequestFailed NamingStatus = "request-failed"
	// NamingNotAsked: the selection was Empty; nothing was done. (F3)
	NamingNotAsked NamingStatus = "not-asked"
)

// SpeakerReason says why a transcript speaker kept its label.
type SpeakerReason string

const (
	// ReasonNoCandidate: the speaker has no run of speech a candidate can
	// be cut from.
	ReasonNoCandidate SpeakerReason = "no-candidate"
	// ReasonNoMatch: in neither order did one person's landed clip share
	// the candidate's label.
	ReasonNoMatch SpeakerReason = "no-match"
	// ReasonAmbiguous: in an order, clips of two people carried the
	// candidate's label.
	ReasonAmbiguous SpeakerReason = "ambiguous"
	// ReasonDisagreed: the two orders gave different people, or a person in
	// one order and nobody in the other.
	ReasonDisagreed SpeakerReason = "disagreed"
	// ReasonNotCompared: the speaker's candidate was in no answered naming:
	// none was answered (see NamingReport.Status), or the request budget
	// left the candidate out.
	ReasonNotCompared SpeakerReason = "not-compared"
)

// VoiceResult says how one person of the selection fared.
type VoiceResult string

const (
	// VoiceNamed: the person's name went to at least one speaker.
	VoiceNamed VoiceResult = "named"
	// VoiceAbsent: a clip of the person landed and named nobody.
	VoiceAbsent VoiceResult = "absent"
	// VoiceNotPlaced: no clip of the person landed in either order.
	VoiceNotPlaced VoiceResult = "not-placed"
	// VoiceNotCompared: no naming was answered.
	VoiceNotCompared VoiceResult = "not-compared"
	// VoiceUnknown: the library does not hold the person. (F11)
	VoiceUnknown VoiceResult = "unknown-voice"
)

// Provenance is the clip that named a segment and that clip's status when it
// was picked. (F6)
type Provenance struct {
	Clip   voices.ClipRef
	Status voices.Status
}

// MarshalJSON is the segment's voice field.
//
// - Provenance.MarshalJSON.1 — The encoding is exactly
//   {"clip":"<slug>/<id>","status":"confirmed"|"unconfirmed"}: clip is the
//   ClipRef's text form, which ParseClipRef reads back. (F6)
func (p Provenance) MarshalJSON() ([]byte, error)

// NamedSegment is one transcript segment after naming.
type NamedSegment struct {
	// Segment is the segment as transcribed, except that Speaker is the
	// person's plain name when a clip named it.
	Segment
	// Voice is the clip that named the segment; nil when no clip did.
	Voice *Provenance
}

// SpeakerNaming is what became of one transcript speaker.
type SpeakerNaming struct {
	// Label is the speaker's label as transcribed.
	Label string
	// Name is the person whose name the speaker took; empty when it kept
	// its label.
	Name voices.Name
	// Voice is the clip the name came from; nil when Name is empty.
	Voice *Provenance
	// Reason says why the speaker kept its label; empty when Name is set.
	Reason SpeakerReason
}

// VoiceNaming is what became of one person the run was asked to name.
type VoiceNaming struct {
	// Name is the stored name, or for VoiceUnknown the name as it was given.
	Name   string
	Result VoiceResult
}

// NamingRequestStage is the step of a naming request that failed.
type NamingRequestStage string

const (
	// StageBuild: the request's audio could not be put together.
	StageBuild NamingRequestStage = "build"
	// StageSend: Transcriber.Transcribe returned an error for the request.
	StageSend NamingRequestStage = "send"
)

// NamingRequestError says a naming request failed: in which order, at which
// stage, and why. Its message never holds audio. (F8)
type NamingRequestError struct {
	Order Order
	Stage NamingRequestStage
	Err   error
}

func (e *NamingRequestError) Error() string
func (e *NamingRequestError) Unwrap() error

// NamingBudgetError says a naming request does not fit the request budget:
// Need is the length of the picked clips with one candidate, or the size of
// the built file; Limit is the budget it exceeds.
type NamingBudgetError struct {
	NeedDuration  time.Duration
	LimitDuration time.Duration
	NeedBytes     int64
	LimitBytes    int64
}

func (e *NamingBudgetError) Error() string

// NamingReport is the typed outcome of one naming, beside the segments.
type NamingReport struct {
	Status NamingStatus
	// Origin is the selection's: which rule of F11 chose the people.
	Origin voices.Origin
	// Speakers has one entry per transcript speaker label, in the order the
	// labels first appear in the transcript.
	Speakers []SpeakerNaming
	// Voices has one entry per selected person, in the selection's order,
	// then one per unknown person, in the order given.
	Voices []VoiceNaming
	// Err is why the naming was not answered: a *voices.LibraryReadError,
	// a *voices.MissingToolError or a *NamingRequestError; nil for every
	// other status.
	Err error
	// Unrecorded is set by the caller whose Tally of the outcomes failed;
	// Name always leaves it nil.
	Unrecorded error
}

// MarshalJSON is the naming object of the json output.
//
// - NamingReport.MarshalJSON.1 — The encoding is one object with the keys
//   status, origin, speakers, voices, in that order, then error when Err is
//   set and unrecorded when Unrecorded is set, each of those two the
//   error's message. speakers and voices are arrays, [] when empty.
// - NamingReport.MarshalJSON.2 — A speaker is {"label","name","voice"} when
//   named, voice as Provenance encodes it, and {"label","reason"} when not.
//   A voice is {"name","result"}. Every status, origin, reason and result is
//   the constant's text. For example:
//   {"status":"named","origin":"flag",
//    "speakers":[{"label":"A","name":"Lorentz","voice":{"clip":"lorentz/9f3c2a1b0d4e","status":"confirmed"}},
//                {"label":"B","reason":"no-match"}],
//    "voices":[{"name":"Lorentz","result":"named"},{"name":"Ada","result":"unknown-voice"}]}
// - NamingReport.MarshalJSON.3 — The encoding holds no audio and no path
//   other than what an error's message names. (F8)
func (r NamingReport) MarshalJSON() ([]byte, error)

// Naming is the result of naming one recording.
type Naming struct {
	// Segments are the transcript's segments, named where a clip named
	// them.
	Segments []NamedSegment
	Report   NamingReport
	// Outcomes is how each picked clip fared, for Tallier.Tally. (F6)
	Outcomes []voices.ClipOutcome
}

// Namer names the speakers of a transcribed recording after the people of a
// selection.
type Namer interface {
	// Name compares the selection's picked clips with one candidate clip of
	// each transcript speaker, in two naming requests, and returns the
	// segments with every agreed name applied, the report, and the picked
	// clips' outcomes. It has no error: whatever goes wrong is in the
	// report. (F4)
	//
	// - Name.1 — Segments has one entry per given segment, in the given
	//   order, each with its start, end and text untouched. Every segment
	//   whose label took a name has that person's stored name as its speaker
	//   and the naming clip as Voice; every other segment has the speaker it
	//   was given and a nil Voice. (F6, L5.1)
	// - Name.2 — The transcriber is reached only through
	//   Transcriber.Transcribe, with a file path, and of every segment, given
	//   or returned by a naming request, only the start, the end and the
	//   speaker label are read. The result is the same for any Transcriber
	//   that returns the same starts, ends and labels. (F1)
	// - Name.3 — When no given segment has a speaker label the status is
	//   NamingNotDiarized: Speakers is empty, nothing is run and nothing is
	//   sent. (F1, F4)
	// - Name.4 — Each transcript label has at most one candidate: the first
	//   clip SampleExtractor.Candidates returns for that label over the given
	//   segments. A label with none is ReasonNoCandidate. When no label has
	//   one the status is NamingNoCandidate, and nothing is run or sent.
	// - Name.5 — A naming request is one audio file whose sound is, in
	//   order: each picked clip, whole, from its Pick.Path; then each
	//   candidate, cut from the recording at its candidate's interval, in the
	//   order the labels first appear in the transcript, without those
	//   Name.20 leaves out. L5.3 of silence
	//   separates every two neighbours. There is no other audio: nothing
	//   before the first clip, nothing after the last candidate, nothing else
	//   of the recording. (L5.3, F8)
	// - Name.6 — Two requests are sent, one Transcribe call each: first
	//   OrderForward, the picks in Selection.Voices order with each person's
	//   picks as given; then OrderReversed, the same picks in exactly the
	//   opposite sequence. The candidates are the same, in the same order, in
	//   both. No other call is made to the transcriber. (L5.1)
	// - Name.7 — In a request's answer, a clip's or a candidate's region is
	//   its place in the request, counted from Pick.Seconds, the candidates'
	//   lengths and L5.3. Its label is the label that covers most of the
	//   region's labelled time, its share is that part of the labelled time,
	//   and a region with no labelled time, or with two labels tied for most,
	//   has no label. A picked clip landed in an order when its share there
	//   is at least L5.2; a share of exactly L5.2 lands. (L5.2)
	// - Name.8 — In one order a candidate points at a person when at least
	//   one of that person's clips landed with the candidate's label and no
	//   clip of another person, landed or not, has that label. A speaker
	//   takes a person's name only when its candidate points at that same
	//   person in both orders. (L5.1)
	// - Name.9 — Two clips of one person on the candidate's label are
	//   support: the person is still pointed at. Two transcript speakers
	//   whose candidates both point at one person both take the name. (L5.1)
	// - Name.10 — A speaker that takes no name keeps its label everywhere and
	//   is reported with one reason: ReasonAmbiguous when in either order
	//   clips of two people have its candidate's label; else ReasonDisagreed
	//   when the orders point at different people or only one points at
	//   anyone; else ReasonNoMatch. When the two orders were not both
	//   answered, a label is ReasonNoCandidate when it has no candidate and
	//   ReasonNotCompared otherwise. (L5.1, F4)
	// - Name.11 — A pick of the named person supports the name when it
	//   landed with the candidate's label in at least one order. The clip a
	//   name comes from is, of the supporting picks, the first in pick order
	//   whose Pick.Status is Confirmed; when no supporting pick is confirmed,
	//   the first supporting pick in pick order. Voice carries that clip's
	//   reference and its Pick.Status, so a name is reported as unconfirmed
	//   only when no confirmed clip supports it. (F6)
	// - Name.12 — When both orders were answered, Outcomes has exactly one
	//   entry per pick of the selection, in OrderForward sequence: Landed is
	//   true when the clip landed in at least one order, and Named lists, in
	//   Speakers order, the labels whose name came from this clip (Name.11),
	//   so an outcome never names without landing. With any other status
	//   Outcomes is empty: a naming that was not answered says nothing about
	//   a clip. (F6)
	// - Name.13 — Report.Voices says of every selected person VoiceNamed,
	//   VoiceAbsent or VoiceNotPlaced when both orders were answered and
	//   VoiceNotCompared otherwise, and lists every entry of
	//   Selection.Unknown as VoiceUnknown under the name as given, whatever
	//   the status. Report.Origin is the selection's. (F11, F4)
	// - Name.14 — The status is the first of these that applies:
	//   NamingNotAsked for an Empty selection; NamingLibraryUnreadable when
	//   Selection.Unreadable is set, with it as Err; NamingNoVoice when the
	//   selection holds no pick; NamingNotDiarized; NamingNoCandidate;
	//   NamingRequestFailed; NamingNoAgreement when both orders were answered
	//   and nobody took a name; NamingNamed. With every status but
	//   NamingNamed all segments come back as given, and with every status
	//   before NamingRequestFailed nothing is run and nothing is sent. (F4)
	// - Name.15 — A request that cannot be built, whose file is missing or
	//   empty after it was built, or whose Transcribe call returns an error
	//   gives NamingRequestFailed with a *NamingRequestError holding the
	//   order, the stage and the cause as Err. After a failed forward request
	//   the reversed one is not sent. (F4)
	// - Name.16 — The only program run is ffmpeg, through the CommandRunner,
	//   and every invocation names the file it writes as its last argument
	//   (L5.4). When ffmpeg does not resolve, the status is
	//   NamingRequestFailed with a *voices.MissingToolError naming it, and
	//   nothing is sent.
	// - Name.17 — A ctx that is cancelled before both orders are answered
	//   gives NamingRequestFailed with an Err that matches ctx.Err().
	// - Name.18 — However it ends, Name leaves no file behind, changes
	//   neither the recording nor anything under voices, and writes nothing
	//   under voices. A clip's audio goes nowhere but into the request file
	//   handed to Transcribe: no error and no part of the Naming holds audio.
	//   (F8)
	// - Name.19 — With an Empty selection the segments come back as given,
	//   Outcomes and Report.Speakers and Report.Voices are empty, and nothing
	//   is run or sent. (F3)
	// - Name.20 — A naming request stays within the request budget. Its
	//   length, the picks, the candidates and the silences between them, is
	//   at most Budgets.MaxRequestDuration. The picks always go in;
	//   candidates are taken most speech first, equal speech by the label
	//   that appears first, and the first candidate that would take the
	//   length over the budget is left out with every candidate after it. A
	//   label whose candidate is left out is ReasonNotCompared and keeps its
	//   label; the others are named as if it were not in the transcript.
	// - Name.21 — When the picks with the first candidate are longer than
	//   Budgets.MaxRequestDuration, or a built request file is larger than
	//   Budgets.MaxRequestBytes, the status is NamingRequestFailed with a
	//   *NamingRequestError of StageBuild wrapping a *NamingBudgetError that
	//   holds what was needed and the limit, and nothing is sent. (F4)
	Name(ctx context.Context, recording string, segments []Segment, selection voices.Selection) Naming
}

// NewNamer returns the Namer that sends its naming requests to transcriber,
// builds them through runner and keeps each within budgets, the resolved
// request budgets of the run (ResolveBudgets).
//
// - NewNamer.1 — The Namer holds no state besides its three arguments: calls
//   do not affect each other and may run at once.
func NewNamer(transcriber Transcriber, runner CommandRunner, budgets Budgets) Namer
```

Consumes:

- `Selection`, `Selection.Empty`, `SelectedVoice`, `Pick`, `Origin`, `ClipOutcome` (phase 4)
- `ClipRef`, `ClipRef.String`, `ParseClipRef`, `Name`, `Slug`, `Status`, `LibraryReadError` (phase 1)
- `MissingToolError` (phase 3)
- `internal/audio:Transcriber` (`Transcribe`), `Segment` (`generic.Segment`) (import)
- `internal/audio:SampleExtractor` (`Candidates`, `Candidate`, `SourceInterval`) (import): the clip
  length parameter (3–10 s) and the run rule are its own.
- `internal/audio:CommandRunner` (import): the seam ffmpeg is run through; `ExecRunner` is passed in by
  the root.
- `internal/audio:Budgets` (`MaxRequestBytes`, `MaxRequestDuration`) (import): the run's resolved
  request budgets, passed in by the root.
- `Tallier.Tally`, `NewLibrarian`, `NewRanker` (phases 4, 2): only in `TestName_OutcomesAreTalliable`,
  which hands this phase's outcomes to the real Librarian.
- `internal/testenv:Main` (import)
- `TestPackage_ImportsNoAudio` (phase 1) keeps proving F9: this file imports `voices`, and `voices`
  imports nothing from `internal/audio`.

## Acceptance criteria

| #   | Criterion                                                                                                             | Proves                                        | Proof                                                        |
| --- | --------------------------------------------------------------------------------------------------------------------- | --------------------------------------------- | ------------------------------------------------------------ |
| 1   | A speaker whose candidate shares a label with a person's landed clip in both orders gets that person's name on every one of its segments, with the clip and its status; of several supporting clips a confirmed one is the source, then pick order | `Name.1`, `Name.8`, `Name.11`, L5.1, F6       | `TestName_NamesByAgreement`, `TestName_SupportNotAmbiguity`  |
| 2   | Agreement in one order only, or clips of two people on one label, names nobody                                        | `Name.8`, `Name.10`, L5.1                     | `TestName_OneOrderIsNotEnough`, `TestName_TwoPeopleOnOneLabel` |
| 3   | Two clips of one person on one label still name; a clip under the dominant share does not                             | `Name.7`, `Name.9`, L5.1, L5.2                | `TestName_SupportNotAmbiguity`, `TestName_DominantShare`     |
| 4   | Each request holds the picked clips, then one candidate per label, 0.5 s apart, and nothing else, forward then reversed | `Name.4`, `Name.5`, `Name.6`, L5.3            | `TestName_RequestLayout`                                     |
| 5   | Only start, end and label are read, through `Transcribe` alone; a transcript with no labels is `not-diarized`          | `Name.2`, `Name.3`, F1                        | `TestName_ReadsOnlyStartEndLabel`, `TestName_NotDiarized`    |
| 6   | Every picked clip of an answered naming has one outcome, landed or not, that the Librarian accepts                    | `Name.12`, F6                                 | `TestName_Outcomes`, `TestName_OutcomesAreTalliable`         |
| 7   | Nothing that goes wrong fails: the segments come back as given with the reason in the report                          | `Name.14`–`Name.17`, `Name.19`, F4, F3        | `TestName_NeverFails`, `TestName_MissingFFmpeg`, `TestName_Cancelled`, `TestName_EmptySelection`, `TestName_NoCandidate` |
| 8   | The report lists every speaker and every person asked for, unknown people included, and encodes as the json output's `naming` | `Name.10`, `Name.13`, `NamingReport.MarshalJSON.1`–`.3`, `Provenance.MarshalJSON.1`, F11 | `TestName_ReportVoices`, `TestNamingReport_JSON` |
| 9   | No audio leaves but in the request file, and no file is left                                                          | `Name.18`, F8                                 | `TestName_LeavesNothing`                                     |
| 10  | Namers share nothing                                                                                                  | `NewNamer.1`                                  | `TestNamer_Concurrent`                                       |
| 11  | A naming request never exceeds the request budget: picks first, then candidates by most speech, the rest `not-compared` | `Name.20`, `Name.21`, F4                      | `TestName_CandidatesWithinBudget`, `TestName_BudgetTooSmall` |
| 12  | Every ffmpeg invocation names the file it writes as its last argument, so a stand-in that writes that file serves a whole naming | `Name.16`, L5.4                               | `TestName_FFmpegOutputIsLastArgument`                        |

## Test cases

Every test builds its `Selection` by hand from `Pick` values whose `Path` is a file under the `testenv`
home and whose `Seconds` is stated in the row. The `Transcriber` is a test fake that records the path it
is given and answers from a table of regions: the test lays the regions out itself from the picks'
`Seconds`, the candidates' lengths and 0.5 s, and says which label covers how much of each region in
each order. The `CommandRunner` is a test fake that records every invocation and writes a file at the
path its last argument names (L5.4), reading nothing else of the arguments; no row needs ffmpeg
installed. The budgets are the defaults (25 MiB, 1400 s) unless
a row states others. Unless a row says otherwise, the transcript is
speaker `A` for 0–8 s and speaker `B` for 9–15 s, and the selection is Lorentz with two picks (8.4 s
confirmed, 6.1 s confirmed) and Syrus with one (7.0 s unconfirmed).

| Test                              | Level          | Proves                                                     | Given                                                                                                                                                         | Then                                                                                                                                                                     | Must not                                             |
| --------------------------------- | -------------- | ---------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------ | ---------------------------------------------------- |
| `TestName_NamesByAgreement`       | contract suite | `Name.1`, `Name.8`, `Name.11`, `Name.14`, L5.1, F6, AC 1   | Both orders: Lorentz's first clip and candidate `A` on one label, Syrus's clip and candidate `B` on another, every share 1.0; the transcript has three `A` segments and two `B` | Status `named`; every `A` segment has speaker `Lorentz` and `Voice` `lorentz/<first clip>` `confirmed`; every `B` segment `Syrus` with status `unconfirmed`; starts, ends and texts as given | Rename only the candidate's segment, or change a text |
| `TestName_OneOrderIsNotEnough`    | contract suite | `Name.8`, `Name.10`, L5.1, AC 2                            | Forward: Lorentz's clip shares `A`'s label. Reversed: `A`'s label is on no clip; then a second run where reversed puts `A` with Syrus's clip                   | `A` keeps its label both times, reason `disagreed`; status `no-agreement`; the segments equal the given ones                                                              | Name from the forward order alone                    |
| `TestName_TwoPeopleOnOneLabel`    | contract suite | `Name.8`, `Name.10`, L5.1, AC 2                            | Both orders: Lorentz's clip, Syrus's clip and candidate `A` on one label; then the same with Syrus's clip at share 0.5 (not landed) on that label              | `A` keeps its label, reason `ambiguous`, both times                                                                                                                      | Let an unlanded clip of another person go unnoticed  |
| `TestName_SupportNotAmbiguity`    | contract suite | `Name.9`, `Name.11`, L5.1, AC 3                            | Both orders: both of Lorentz's clips and candidate `A` on one label; then a transcript where `A` and `B` both share that label; then (3) a run where the first clip does not land in the reversed order; then (4) a run where the first clip lands in the forward order only and the second in the reversed order only; then, with Lorentz's first pick unconfirmed and his second confirmed: (5) both land with `A`'s label in both orders; (6) the first lands in both orders and the second in the reversed order only; (7) the second lands in neither order; then (8) both picks unconfirmed, both landing in both orders | `A` is `Lorentz` from the first clip; then `A` and `B` are both `Lorentz`; then (3) and (4) `A` is `Lorentz` and `Voice` is the first clip, the first supporting pick in pick order, both being confirmed; (5) and (6) `Voice` is the second clip with status `confirmed`; (7) `Voice` is the first clip with status `unconfirmed`; (8) `Voice` is the first clip; in every case the outcome whose `Named` holds `A` is the source clip's, and it has `Landed` true | Report `ambiguous` for two clips of one person, report a name as `unconfirmed` while a confirmed clip supports it, or take a clip that landed with another label as the source |
| `TestName_DominantShare`          | contract suite | `Name.7`, L5.2, AC 3                                       | Lorentz's clip with candidate `A`'s label at share 0.80 in both orders; then at 0.79 in the forward order; then candidate `A`'s region with two labels at 0.5 each; then candidate `A`'s region with no labelled time | Named at 0.80; `disagreed` at 0.79; `no-match` for the tied and for the empty region; `Landed` follows                                                                    | Round a share, or count unlabelled time as a label's |
| `TestName_RequestLayout`          | contract suite | `Name.4`, `Name.5`, `Name.6`, L5.3, AC 4                   | A transcript with labels `B` (first), `A`, and `C` whose longest run is 2 s; the recorded runner invocations and the paths given to `Transcribe`               | Two `Transcribe` calls with two different files; the forward request's inputs are Lorentz's two clips, Syrus's clip, then the recording cut at `B`'s and at `A`'s first `SampleExtractor` candidate, 0.5 s of silence between neighbours; the reversed request has Syrus's clip, then Lorentz's second, then his first, then the same two cuts; `C` is `no-candidate` | Send the recording's other audio, lead or trail with silence, reorder the candidates, or call `Transcribe` a third time |
| `TestName_ReadsOnlyStartEndLabel` | contract suite | `Name.2`, F1, AC 5                                         | The agreement of the first row answered by two different fake transcribers, one of which returns empty texts and the other long random texts and extra segments outside every region | The two `Naming` values are equal; the given segments' texts are untouched                                                                                                 | Type-assert the transcriber, or read a text          |
| `TestName_NotDiarized`            | contract suite | `Name.3`, `Name.14`, F1, F4, AC 5                          | Segments that all have an empty speaker                                                                                                                       | Status `not-diarized`; `Speakers` empty; every selected person `not-compared`; segments as given; no runner invocation and no `Transcribe` call                           | Send a request                                       |
| `TestName_NoCandidate`            | contract suite | `Name.4`, `Name.14`, AC 7                                  | A diarized transcript whose every run is under 3 s                                                                                                            | Status `no-candidate`; each label `no-candidate`; `Outcomes` empty; nothing run or sent                                                                                   | Count the run against a clip                         |
| `TestName_Outcomes`               | contract suite | `Name.12`, F6, AC 6                                        | Lorentz's first clip names `A` in both orders; his second lands in the reversed order only and names nobody; Syrus's clip lands in neither                     | Three outcomes in forward sequence: first clip `Landed` true, `Named` `["A"]`; second `Landed` true, `Named` empty; Syrus's `Landed` false, `Named` empty                  | Omit the clip that did not land                      |
| `TestName_OutcomesAreTalliable`   | contract suite | `Name.12`, F6, AC 6                                        | A library under the `testenv` home built with `NewLibrarian(dir, NewRanker(), clock)` holding the three clips; the outcomes of the row above given to `Tally`   | A nil error, no `OutcomeError`, `Tallied.Events` 4 (three `placed`, one `named`)                                                                                          | Produce an outcome that names without landing        |
| `TestName_ReportVoices`           | contract suite | `Name.10`, `Name.13`, F11, F4, AC 8                        | A selection with Lorentz (names `A`), Syrus (lands, names nobody), Christoph (lands in neither order), `Unknown` `["Ada"]`, `Origin` `OriginFlag`; then the same selection with a failing transcriber | `Voices` is Lorentz `named`, Syrus `absent`, Christoph `not-placed`, Ada `unknown-voice`; `Origin` is `flag`; `Speakers` is `A` named and `B` `no-match`, in transcript order; then the three people are `not-compared` and Ada is still `unknown-voice` | Drop the unknown person, or fail on it               |
| `TestName_NeverFails`             | contract suite | `Name.10`, `Name.14`, `Name.15`, F4, AC 7                             | In turn: `Selection.Unreadable` set; a selection with only `Unknown`; the runner failing on the forward build; the runner succeeding without writing a file; `Transcribe` failing on the forward request; `Transcribe` failing on the reversed request | `library-unreadable` with the `LibraryReadError`; `no-voice`; `request-failed` with `NamingRequestError{forward, build}` twice; `{forward, send}` and one `Transcribe` call; `{reversed, send}`; every time the segments equal the given ones, `Outcomes` is empty and each speaker with a candidate is `not-compared` | Panic, return a name, or send the reversed request after a failed forward one |
| `TestName_MissingFFmpeg`          | contract suite | `Name.16`, AC 7                                            | A runner whose `LookPath("ffmpeg")` fails; then a successful run's recorded program names                                                                     | `request-failed` with a `*voices.MissingToolError` whose `Tool` is `ffmpeg`, and no `Transcribe` call; the only program ever run is `ffmpeg`                              | Run another program, or send without ffmpeg          |
| `TestName_FFmpegOutputIsLastArgument` | contract suite | `Name.16`, L5.4, AC 12                                 | The named run of the first row, a run whose budget leaves candidates out (`Name.20`) and a run whose forward send fails, over a runner fake that writes a few bytes to the file its last argument names and does nothing else; every recorded invocation | Every invocation is `ffmpeg`, and its last argument is a path that is neither the recording nor a file under `voices`; each request file given to `Transcribe` exists and is not empty when it is called; the three `Naming` values equal those of the same runs over the suite's usual fake | Write its output to a path that is not the last argument, or run ffmpeg only to print or to probe |
| `TestName_Cancelled`              | contract suite | `Name.17`, AC 7                                            | A ctx cancelled while the fake transcriber holds the forward request                                                                                          | `request-failed`; `errors.Is(report.Err, context.Canceled)`; segments as given                                                                                            | Block, or send the reversed request                  |
| `TestName_LeavesNothing`          | contract suite | `Name.18`, F8, AC 9                                        | Pick files and a recording that hold a marker string; a listing of the temporary directory, the library and the recording before and after a named run, a failed build and a failed send; every error and the `Naming` rendered as text and as JSON | The listings are identical; the request files given to `Transcribe` no longer exist; the marker is in no error, report or segment                                         | Leave a request file, or write under `voices`        |
| `TestName_EmptySelection`         | contract suite | `Name.19`, `Name.14`, F3, AC 7                             | `voices.Selection{}`                                                                                                                                          | Status `not-asked`; segments as given with nil `Voice`; `Outcomes`, `Speakers` and `Voices` empty; nothing run or sent                                                    | Touch the runner or the transcriber                  |
| `TestName_CandidatesWithinBudget` | contract suite | `Name.20`, `Name.5`, AC 11                                 | A split transcript with nine labels (`1-A` … `3-C`) whose best runs are 10, 9, … 3 s and two of equal speech; Lorentz's two picks and Syrus's one; `MaxRequestDuration` 60 s; naming answers that agree on `1-A` = Lorentz | Both requests are at most 60 s by the layout: the three picks, then the candidates with most speech that fit, in first-appearance order, the earlier label of the equal pair before the later; every label left out is `not-compared` and keeps its label; `1-A` is `Lorentz`; with the default budgets all nine go in | Drop a pick to make room, send a request over the budget, or report a left-out label as `no-match` |
| `TestName_BudgetTooSmall`         | contract suite | `Name.21`, F4, AC 11                                       | `MaxRequestDuration` 20 s with picks of 21.5 s; then default duration with `MaxRequestBytes` 1 KiB and a runner that writes a 4 KiB request file                 | `request-failed` both times with `NamingRequestError{forward, build}` wrapping a `*NamingBudgetError` holding the need and the limit; no `Transcribe` call; segments as given; `Outcomes` empty | Send an oversized request, or fail the call          |
| `TestNamingReport_JSON`           | unit           | `NamingReport.MarshalJSON.1`–`.3`, `Provenance.MarshalJSON.1`, AC 8 | The report of `TestName_ReportVoices`; a `request-failed` report with `Unrecorded` set; an empty `not-asked` report; each through `encoding/json`       | The first equals the example of `NamingReport.MarshalJSON.2` extended by its entries, keys in the stated order; the second has `error` and `unrecorded` as messages; the third has `"speakers":[]` and `"voices":[]`; each `clip` reads back through `ParseClipRef` | Encode `null` for an empty list, or a `ClipRef` as an object |
| `TestNamer_Concurrent`            | contract suite | `NewNamer.1`, AC 10                                        | One Namer naming eight recordings at once under `-race`, each with its own transcript and regions                                                             | Each result equals the result of the same call made alone                                                                                                                | Share a request file or a result between calls       |

## Error management

`Name` returns no error (F4). Each failure is a status, with its typed cause in `Report.Err` where one
exists.

| Failure                                                        | Expected outcome                                                                                              | Test                          |
| -------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------- | ----------------------------- |
| The transcript has no speaker label                            | Not an error: status `not-diarized`, segments as given, nothing sent                                          | `TestName_NotDiarized`        |
| No transcript speaker has a candidate                          | Not an error: status `no-candidate`, each label `no-candidate`                                                | `TestName_NoCandidate`        |
| `Selection.Unreadable` is set                                  | Not an error: status `library-unreadable`, `Report.Err` the `*voices.LibraryReadError`                        | `TestName_NeverFails`         |
| The selection holds only unknown people                        | Not an error: status `no-voice`, each listed `unknown-voice`                                                  | `TestName_NeverFails`         |
| A named person the library does not hold, beside known ones    | Not an error: listed `unknown-voice`; the others are named                                                    | `TestName_ReportVoices`       |
| ffmpeg does not resolve                                        | Not an error: status `request-failed`, `Report.Err` a `*voices.MissingToolError{Tool: "ffmpeg"}`              | `TestName_MissingFFmpeg`      |
| A request cannot be built, or its file is missing or empty     | Not an error: status `request-failed`, `*NamingRequestError` with the order and `StageBuild`                  | `TestName_NeverFails`         |
| `Transcribe` fails on a naming request                         | Not an error: status `request-failed`, `*NamingRequestError` with the order and `StageSend`, wrapping the cause | `TestName_NeverFails`       |
| The ctx is cancelled                                           | Not an error: status `request-failed`, `Report.Err` matches `ctx.Err()`                                       | `TestName_Cancelled`          |
| A candidate does not fit the request budget                    | Not an error: the label is `not-compared`; the others are named                                                | `TestName_CandidatesWithinBudget` |
| The picks with one candidate exceed `MaxRequestDuration`, or the built file exceeds `MaxRequestBytes` | Not an error: status `request-failed`, `*NamingRequestError` of `StageBuild` wrapping `*NamingBudgetError` | `TestName_BudgetTooSmall`     |
| Both orders answered, nobody agreed on                         | Not an error: status `no-agreement`, each speaker `no-match`, `ambiguous` or `disagreed`                      | `TestName_OneOrderIsNotEnough`, `TestName_TwoPeopleOnOneLabel` |
| An Empty selection                                             | Not an error: status `not-asked`, nothing done                                                                | `TestName_EmptySelection`     |

## Golden diff

None.

## Removes

None.

## Local facts

L5.1–L5.3 are moved unchanged from the README's Phase-local facts; L5.4 is this phase's own.

**L5.1 — Names only by agreement.** A transcript speaker takes a person's name only when, in the
forward and in the reversed order, the transcriber gives its candidate the same label as at least one of
that person's picked clips, each such clip holding at least the dominant share of its own region, and
gives that label to no clip of another person. Two clips of one person on one label are support, not
ambiguity. Every other speaker keeps the label it was transcribed with.

| Parameter                  | Default | Decided by                                                       |
| -------------------------- | ------- | ---------------------------------------------------------------- |
| L5.2 dominant share        | 0.80    | `worklogs/2026-09-01-adaptive-speaker-calibration` phase 0       |
| L5.3 silence between clips | 0.5 s   | same                                                             |

**L5.4 — ffmpeg's output is its last argument.** Every ffmpeg invocation the Namer makes through
`CommandRunner` writes a file and names it as its last argument; the Namer never runs ffmpeg only to
print or to probe. A stand-in that writes a non-empty file at the path of its last argument and exits 0
therefore serves a whole naming: this phase's fake `CommandRunner` relies on it, and so does the `ffmpeg`
stub phase 9's end-to-end rows and goldens put on `PATH`. Binds `Namer.Name` (`Name.16`).
(Phase-authored; signed with this phase's interface.)

## Human required

None. Whether naming is right often enough on real voices is the root's measurement (phase 9, Human
required); this phase proves the rule on stated labels.

## Implementation notes

| Run   | Commit      | Command                      | Result                |
| ----- | ----------- | ---------------------------- | --------------------- |
| start | 1550dec+wip | `go test ./internal/audio/... ./pkg/diagnostic/... -race -count=3 -cover -timeout=30s -p 4` (provider keys unset); `go vet ./internal/audio/...`; staticcheck, `gofumpt -l internal/audio`; `dupl -t 80 .` | 5 packages ok; vet, staticcheck, gofumpt clean; dupl 49 groups |
| end   | 1550dec+wip | the same                     | 5 packages ok (`internal/audio` 91.8 %), 22 phase-5 tests; vet and gofumpt clean; dupl 49 groups, none in `namer*`; staticcheck exit 1: U1000 only in phase 7's in-progress `voices_cmd_support_test.go`, none in phase 5 files |
| end   | 1550dec+wip | the same, `go test … -shuffle=on` | 5 packages ok; vet and gofumpt clean; dupl 49 groups; staticcheck exit 1, same cause (21 findings, 4 after two retries 90 s and 120 s apart) |
| fix-start | 1550dec+wip | `env -u OPENAI_API_KEY -u ANTHROPIC_API_KEY -u OPENROUTER_API_KEY go test ./internal/audio/ ./pkg/tools/... -race -count=3 -cover -timeout=30s -p 4`; `go vet ./internal/audio/... ./pkg/tools/...`; staticcheck and `gofumpt -l` on `internal/audio`, `pkg/tools` | 2 packages ok (`internal/audio` 17.6 s, 92.4 %; `pkg/tools` 13.0 s, 92.3 %); vet, staticcheck, gofumpt clean |
| fix-end   | 1550dec+wip | the same | 2 packages ok (`internal/audio` 18.0 s, 92.4 %; `pkg/tools` 13.9 s, 92.3 %); vet, staticcheck, gofumpt clean (R1-16) |
| fix-end   | 1550dec+wip | the same, `go test … -shuffle=on` | 2 packages ok (`internal/audio` 18.1 s; `pkg/tools` 13.4 s); vet, staticcheck, gofumpt clean |

Andon: none.
Surprises:
- Real ffmpeg 8.0.1 (scratch, not a test) builds the `namerArgs` request as 37.3 s with 0.5 s silences at 8.4, 15, 22.5 and 31 s: the layout exactly.
- An empty request file is the unexported `namerEmptyRequestError` inside `NamingRequestError{StageBuild}`; no exported member is added.
- `TestNamer_Concurrent` routes each call through a ctx value, since one Namer's request files have random names.
- In the request-failed branch a cancelled ctx wins over a budget or missing-ffmpeg cause, so `Err` matches `ctx.Err()` (Name.17).
- R1-16: `namerSource` walks the person's picks in pick order and takes the first confirmed supporting pick, else the first supporting one; "supporting" is `namerSupports` (landed with the candidate's label in either order). `namer.go`'s `Name.11` comment now carries the signed text.
- R1-16: `TestName_SupportNotAmbiguity` case 3 now expects the first clip; cases 5–8 set Lorentz's pick statuses on the usual fixture. Case 7 runs twice, the confirmed second clip once below the dominant share on the candidate's label and once landed on another label, so both halves of the Must-not "take a clip that landed with another label" are covered. Every case checks that the one outcome naming `A` is the source clip's and has `Landed`.

## Review findings

### Review 2, 2026-11-19

Root review round 2 at `1550dec+wip`. No finding.

- **R1-16 resolved.** `namerSource` (`internal/audio/namer_decide.go:135-149`) returns the first confirmed supporting pick, else the first supporting pick (`Name.11`); an agreed name always has one, since each order's pointing needs a landed pick of that person under the candidate's label. `TestName_SupportNotAmbiguity` passes.
- **note** With the real ffmpeg and the scripted mock transcriber, two requests are built and sent, a speaker is named only where both orders point at one person, and swapping the order of `-voices` names the same speaker after the other person when the scripted answers say so in both orders.
