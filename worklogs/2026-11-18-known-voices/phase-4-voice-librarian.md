# Phase 4 — Voice librarian

**Parent:** root · **Class:** contract · [README](README.md)

## Goal

Keep the voice library: enrol a clip, list the people and their clips, select and pick the clips a run
names with, tally how each picked clip fared, record a keep or an overrule, and remove a clip or a
person, all through the phase 1 format and safe for concurrent writers.

## Interface

Written to `internal/audio/voices/librarian.go`.

```go
package voices

import "time"

// Clock is the time an appended event carries, unless Librarian.3 moves it
// later.
type Clock func() time.Time

// Enrolment is one clip to add to one person.
type Enrolment struct {
	// Name is the person, as typed; Enrol applies the name rule.
	Name       string
	Inspection Inspection
	Status     Status
	Source     Source
	// Replaces is the person's clip this one stands in for (a trim), or
	// empty. That clip does not count against the cap and is never pruned;
	// removing it stays the caller's Remove.
	Replaces ClipID
}

// ClipSummary is one clip as `voices list -json` prints it.
type ClipSummary struct {
	ID        ClipID     `json:"id"`
	Status    Status     `json:"status"`
	Source    Source     `json:"source"`
	Seconds   float64    `json:"seconds"`
	Added     time.Time  `json:"added"`
	Named     int        `json:"named"`
	Overruled int        `json:"overruled"`
	LastNamed *time.Time `json:"last_named"`
	// Path is the clip's file, absolute.
	Path string `json:"path"`
}

// VoiceSummary is one person as `voices list -json` prints them: clips best
// first.
type VoiceSummary struct {
	Name      Name          `json:"name"`
	Slug      Slug          `json:"slug"`
	LastNamed *time.Time    `json:"last_named"`
	Clips     []ClipSummary `json:"clips"`
}

// Asked is what a run says about whom to name. (F11)
type Asked struct {
	// Tool is the audio_transcribe call's voices argument; empty when the
	// model gave none.
	Tool []string
	// Flag is the run's -voices; empty when it was not given.
	Flag []string
	// MaxVoices caps the people selected when nobody is named
	// (transcribe.max-voices).
	MaxVoices int
}

// Origin says which rule of F11 produced a selection.
type Origin string

const (
	OriginNone    Origin = ""
	OriginTool    Origin = "tool"
	OriginFlag    Origin = "flag"
	OriginLibrary Origin = "library"
)

// Pick is one clip that goes into a naming request.
type Pick struct {
	Ref     ClipRef
	Status  Status
	Seconds float64
	// Path is the clip's file, absolute.
	Path string
}

// SelectedVoice is one person a run names, with the picked clips, best first.
type SelectedVoice struct {
	Name  Name
	Slug  Slug
	Picks []Pick
}

// Selection is the people a run names. (F11)
type Selection struct {
	Origin Origin
	Voices []SelectedVoice
	// Unknown are the named people the library does not hold, as they were
	// given; the naming report lists each as unknown-voice.
	Unknown []string
	// Unreadable is set when people were named and the library could not be
	// read; the naming report says so. (F4)
	Unreadable *LibraryReadError
}

// Empty reports the empty selection of F3.
//
// - Empty.1 — True exactly when the selection has no voice, no unknown
//   person and no Unreadable. A run with an empty selection does today's
//   work and nothing else. (F3)
func (s Selection) Empty() bool

// ClipOutcome is how one picked clip fared in the naming of one recording.
// The namer produces one per picked clip. (F6)
type ClipOutcome struct {
	Clip ClipRef
	// Landed: the clip held the dominant share of its own region in at least
	// one of the two naming requests (L5.2). The namer gives outcomes only for
	// a naming both of whose requests were answered.
	Landed bool
	// Named are the transcript speakers, by their labels as transcribed,
	// that took the person's name from this clip; empty when it named nobody.
	Named []string
}

// Tallied is what one Tally appended.
type Tallied struct {
	// Events is the number of event lines appended.
	Events int
	// Dropped are the outcomes' clips the library no longer held.
	Dropped []ClipRef
}

// Verdict is the user's or the agent's judgement of a clip.
type Verdict string

const (
	Keep     Verdict = "keep"
	Overrule Verdict = "overrule"
)

// ReviewOutcome says what a review did.
type ReviewOutcome string

const (
	// ReviewConfirmed: the keep confirmed the clip.
	ReviewConfirmed ReviewOutcome = "confirmed"
	// ReviewAlreadyConfirmed: the clip was confirmed already; nothing was
	// appended.
	ReviewAlreadyConfirmed ReviewOutcome = "already-confirmed"
	// ReviewOverruled: the overrule was appended.
	ReviewOverruled ReviewOutcome = "overruled"
)

// Reviewed is the result of one review: what it did, and the clip as List
// now shows it.
type Reviewed struct {
	Outcome ReviewOutcome
	Clip    ClipSummary
}

// EnrolmentError refuses an enrolment whose Field ("status", "source" or
// "inspection") is missing or not a declared value.
type EnrolmentError struct{ Field string }

func (e *EnrolmentError) Error() string

// ClipExistsError: the person already holds this very clip.
type ClipExistsError struct{ Ref ClipRef }

func (e *ClipExistsError) Error() string

// VoiceFullError: the person holds Stored clips, the cap, and every one is
// confirmed, so none may go. (F5)
type VoiceFullError struct {
	Name   Name
	Slug   Slug
	Stored int
}

func (e *VoiceFullError) Error() string

// VoiceNotFoundError: the library holds no such person.
type VoiceNotFoundError struct{ Slug Slug }

func (e *VoiceNotFoundError) Error() string

// ClipNotFoundError: the library does not hold this clip.
type ClipNotFoundError struct{ Ref ClipRef }

func (e *ClipNotFoundError) Error() string

// VerdictError: the verdict is neither Keep nor Overrule.
type VerdictError struct{ Verdict Verdict }

func (e *VerdictError) Error() string

// OutcomeError: the outcome says a clip named someone without landing.
type OutcomeError struct{ Clip ClipRef }

func (e *OutcomeError) Error() string

// LibraryWriteError says a write failed: Path is the entry, Err why.
type LibraryWriteError struct {
	Path string
	Err  error
}

func (e *LibraryWriteError) Error() string
func (e *LibraryWriteError) Unwrap() error

// Enroller adds clips.
type Enroller interface {
	// Enrol adds one inspected clip to one person and returns its reference.
	//
	// - Enrol.1 — The name goes through ParseName; a refused name is its
	//   *VoiceNameError. A status or source that is not a declared value, or
	//   an inspection with no audio, is an *EnrolmentError naming the field.
	//   A refusal of any kind writes nothing.
	// - Enrol.2 — After a nil error the clip is in the library:
	//   voices/<slug>/<id>.flac holds the inspection's FLAC bytes, <id> is
	//   ClipIDOf of them, and one added event carries the status, the source
	//   and the inspection's seconds. The reference names that slug and id.
	//   The added event is stamped as Librarian.3 says, so a nil error
	//   always means the clip is held, also when the Clock stepped back or
	//   two calls fall on one Clock time. (L4.3)
	// - Enrol.3 — The first clip of a person creates the voice with the name
	//   as given. A later enrolment whose name has the same slug adds to that
	//   person and keeps the stored name, whatever its own spelling. (F10)
	// - Enrol.4 — A clip the person already holds is a *ClipExistsError and
	//   changes nothing. A clip that was removed is enrolled again, with no
	//   namings.
	// - Enrol.5 — A person holding L4.2 clips, not counting Replaces, gets
	//   room first: the lowest-ranked unconfirmed clip by the Ranker, never
	//   Replaces, is removed as Remove removes it, and then the clip is
	//   enrolled. The enrolment's source and status make no difference: a
	//   clip audio_remember_voice adds prunes, and is pruned, like any other.
	//   (F5, F7, L4.2)
	// - Enrol.6 — At that cap, when every counted clip is confirmed, the
	//   enrolment is refused with a *VoiceFullError and nothing is removed.
	//   (F5)
	// - Enrol.7 — A reader never sees a clip file that is not complete: the
	//   file is written as .<id>.flac.tmp beside its place and renamed, an
	//   existing clip file is never rewritten, voice.json is created
	//   exclusively and never rewritten, and no .tmp name outlives the call.
	//   (F10)
	// - Enrol.8 — Two Librarians enrolling into one directory at once lose
	//   nothing: every clip whose Enrol returned nil is held afterwards, and
	//   a person both created has one voice.json, the name of whichever came
	//   first. Enrolments that race at the cap may leave a person above it.
	//   (F10)
	// - Enrol.9 — A write that fails is a *LibraryWriteError naming the
	//   path, beside the zero ClipRef, and the library reads as it did
	//   before the call. One failure comes after the clip is enrolled: when
	//   the file of the clip pruned for it (Enrol.5) cannot be deleted,
	//   Enrol returns the new clip's reference together with a
	//   *LibraryWriteError naming that file. The new clip is then held as
	//   Enrol.2 says and the pruned clip is out of the library as Remove.5
	//   says; only its file is left. A reference that is not zero beside an
	//   error means this and nothing else. (F5)
	Enrol(e Enrolment) (ClipRef, error)
}

// Lister lists the library.
type Lister interface {
	// List returns every person who holds a clip.
	//
	// - List.1 — People come in name order, compared without case; each
	//   person's clips come best first by the Ranker. A person whose clips
	//   are all removed is not listed. (F7)
	// - List.2 — Per clip: its id, status, source, seconds and added time,
	//   the number of its named events, the number of its overruled events,
	//   the time of its latest named event (nil when it never named anyone)
	//   and the absolute path <configDir>/voices/<slug>/<id>.flac. Per
	//   person: the stored name, the slug, and the latest of the clips'
	//   LastNamed.
	// - List.3 — Encoded as JSON the result has exactly the keys name, slug,
	//   last_named, clips and, per clip, id, status, source, seconds, added,
	//   named, overruled, last_named, path. A library with nobody, and a
	//   config directory with no voices directory, encode as []; neither is
	//   an error.
	// - List.4 — A library that cannot be read is the *LibraryReadError of
	//   ReadLibrary.
	// - List.5 — Listing writes nothing.
	List() ([]VoiceSummary, error)
}

// Selector resolves whom a run names.
type Selector interface {
	// Select resolves the selection and picks its clips. It has no error:
	// whatever goes wrong is in the Selection. (F4)
	//
	// - Select.1 — The people are asked.Tool when it is not empty, else
	//   asked.Flag when it is not empty, else the library's own default;
	//   Origin says which, and a non-empty Tool hides Flag entirely. (F11)
	// - Select.2 — A named person matches a voice by name or by slug,
	//   without case. Matches are selected in the order asked, once each,
	//   whether or not any of their clips is confirmed. (F11)
	// - Select.3 — A named person the library does not hold is left out and
	//   listed in Unknown as given. The others are still selected. (F11, F4)
	// - Select.4 — With nobody named, the selection is every person who
	//   holds a confirmed clip, in the Ranker's order of each person's best
	//   clip, cut after asked.MaxVoices people; a MaxVoices under 1 selects
	//   nobody. (F11, F7)
	// - Select.5 — Each selected person carries their L4.1 best-ranked
	//   clips, best first, or all of them when they hold fewer: each Pick
	//   has the clip's reference, status, seconds and absolute path. (L4.1,
	//   F7)
	// - Select.6 — With nobody named, the selection is Empty when the
	//   library holds no confirmed clip, has no voices directory, or cannot
	//   be read. (F3)
	// - Select.7 — With people named and a library that cannot be read, the
	//   selection has no voice, no unknown person, and Unreadable set to the
	//   *LibraryReadError. (F4)
	// - Select.8 — Selecting reads the library and writes nothing. (F3)
	Select(asked Asked) Selection
}

// Tallier records how picked clips fared.
type Tallier interface {
	// Tally appends, for each picked clip of one naming, whether it was
	// placed and whom it named. (F6)
	//
	// - Tally.1 — Every outcome whose clip the library holds gets one placed
	//   event whose landed is the outcome's Landed, and a clip that named
	//   someone gets one named event with those speakers besides. The Clock
	//   is read once per call; every event carries that time, except those
	//   of a clip that Librarian.3 stamps later, and a clip's placed and
	//   named event of one call carry one time. After a nil error every
	//   appended event counts in the clip's history. (F6, L4.3)
	// - Tally.2 — A picked clip that did not land leaves its record too: a
	//   placed event with landed false, which the Ranker counts against it.
	//   (F6, F7)
	// - Tally.3 — An outcome that names someone without landing is an
	//   *OutcomeError, and nothing is appended for any outcome of the call.
	// - Tally.4 — An outcome whose clip the library no longer holds appends
	//   nothing and is listed in Dropped; it is not an error, and the other
	//   outcomes are appended.
	// - Tally.5 — No outcomes append nothing and create nothing. (F3)
	// - Tally.6 — Events is the number of lines appended. Concurrent Tally,
	//   Review, Enrol and Remove calls on one person leave every appended
	//   line whole and counted. (F10)
	// - Tally.7 — A library that cannot be read is its *LibraryReadError; an
	//   append that fails is a *LibraryWriteError naming the path.
	Tally(outcomes []ClipOutcome) (Tallied, error)
}

// Reviewer records a judgement of a clip.
type Reviewer interface {
	// Review appends a keep or an overrule to the clip it names and returns
	// what it did with the clip as List would now show it. (F6)
	//
	// - Review.1 — Keep on an unconfirmed clip appends one confirmed event
	//   and returns ReviewConfirmed; the clip is confirmed from then on, in
	//   every listing and selection, whatever the Clock said (Librarian.3).
	//   (F5, F6, L4.3)
	// - Review.2 — Keep on a confirmed clip succeeds with
	//   ReviewAlreadyConfirmed and appends nothing.
	// - Review.3 — Overrule appends one overruled event on every call,
	//   whatever the clip's status, and returns ReviewOverruled; the clip's
	//   Overruled grows by one, whatever the Clock said (Librarian.3), and
	//   its status does not change. (F6, L4.3)
	// - Review.4 — A clip the library does not hold is a *ClipNotFoundError;
	//   a verdict that is neither Keep nor Overrule is a *VerdictError.
	//   Neither appends anything.
	// - Review.5 — A review removes no clip and rewrites no file. (F5)
	// - Review.6 — A library that cannot be read is its *LibraryReadError; an
	//   append that fails is a *LibraryWriteError naming the path.
	Review(ref ClipRef, verdict Verdict) (Reviewed, error)
}

// Remover removes clips and people.
type Remover interface {
	// Remove removes one clip of a person, or with an empty clip the whole
	// person, and returns what it removed.
	//
	// - Remove.1 — The clip gets one removed event and its file is deleted;
	//   it is in no listing or selection afterwards, whatever the Clock
	//   said (Librarian.3, L4.3). Confirmed and
	//   unconfirmed clips are removed alike: Remove, and the pruning of
	//   Enrol.5 for unconfirmed clips, are the only removals there are. (F5)
	// - Remove.2 — With an empty clip every clip the person holds is removed
	//   that way and the person is no longer listed. voice.json and
	//   events.jsonl stay, so that a merge with an older copy brings nothing
	//   back. (F10)
	// - Remove.3 — A person the library does not hold is a
	//   *VoiceNotFoundError; a clip the person does not hold is a
	//   *ClipNotFoundError. Neither writes anything.
	// - Remove.4 — No other clip, and no other person, is touched.
	// - Remove.5 — A library that cannot be read is its *LibraryReadError; a
	//   write or a deletion that fails is a *LibraryWriteError naming the
	//   path. A clip whose removed event was appended is out of the library
	//   even if its file could not be deleted.
	Remove(person Slug, clip ClipID) ([]ClipRef, error)
}

// Librarian keeps the voice library.
//
// - Librarian.1 — Every line it appends is EncodeEvent's line with an id no
//   other event has, and every file it leaves follows the phase 1 layout, so
//   ReadLibrary and any copy of the files read what it wrote. (F10)
// - Librarian.2 — It runs no program and opens no connection, and no error,
//   summary or selection it returns carries audio: a clip's audio is only in
//   its file. (F8)
// - Librarian.3 — Every event it appends, of every kind and by every
//   member (the added and the pruned clip's removed of Enrol, the placed and
//   named of Tally, the confirmed and overruled of Review, the removed of
//   Remove), is stamped by one rule: the Clock's time, or, when the voice's
//   log as the call read it holds an event of the same clip at or after
//   that time, one nanosecond (the finest step an event's time has) after
//   the latest of them. So after a nil error the event takes effect when
//   the log is folded (ReadLibrary.4–.6): the clip is held, removed,
//   confirmed, or counted once more, as the member says, also when the
//   Clock stepped back, stands still, or runs behind an event already in
//   the log. Events of other clips are not looked at, and no line already
//   in the log is changed. (L4.3, F10)
type Librarian interface {
	Enroller
	Lister
	Selector
	Tallier
	Reviewer
	Remover
}

// NewLibrarian returns the Librarian of the library under
// <configDir>/voices.
//
// - NewLibrarian.1 — Construction reads and writes nothing. The Librarian
//   keeps nothing between calls but its three arguments, so several over one
//   directory, in one process or in several, see each other's writes. (F10)
func NewLibrarian(configDir string, ranker Ranker, now Clock) Librarian
```

Consumes:

- `ReadLibrary`, `EncodeEvent`, `ParseName`, `SlugOf`, `ClipIDOf`, `Event`, `EventKind`, `ClipHistory`,
  `StoredVoice`, `ClipRef`, `ClipID`, `Name`, `Slug`, `Status`, `Source`, `VoiceRecord`,
  `VoiceNameError`, `LibraryReadError`, and the layout `store.1`–`store.8` with both record schemas
  (phase 1)
- `Ranker`, `NewRanker` (phase 2)
- `Inspection` (phase 3)
- `internal/utils:GetClaiConfigDir` (import): the directory the root passes as `configDir`; the
  contract suite builds its libraries under the one it returns in the `testenv` home.
- `internal/testenv:Main` (import)

## Acceptance criteria

| #   | Criterion                                                                                                        | Proves                                              | Proof                                                     |
| --- | ---------------------------------------------------------------------------------------------------------------- | --------------------------------------------------- | --------------------------------------------------------- |
| 1   | An enrolled clip is on disk in the phase 1 format and reads back through `ReadLibrary` and `List`                 | `Enrol.2`, `Enrol.3`, `Librarian.1`, `List.2`       | `TestEnrol_WritesTheFormat`                               |
| 2   | Past six stored clips the lowest-ranked unconfirmed clip goes, whoever enrols; with six confirmed clips the new clip is refused and nothing goes | `Enrol.5`, `Enrol.6`, F5, F7, L4.2 | `TestEnrol_PrunesLowestUnconfirmed`, `TestEnrol_FullVoiceRefuses`, `TestEnrol_ReplacesIsNotCounted` |
| 3   | Two writers on one directory lose no clip and no event, and leave one `voice.json`                               | `Enrol.7`, `Enrol.8`, `Tally.6`, `NewLibrarian.1`, F10 | `TestLibrarian_ConcurrentWriters`, `TestEnrol_NeverPartial` |
| 4   | `List` returns people in name order with clips best first, and its JSON is what `voices list -json` prints       | `List.1`, `List.2`, `List.3`                        | `TestList_OrderAndFields`, `TestList_JSON`                |
| 5   | The tool's `voices` beat the run's `-voices`, which beat the default of confirmed people up to max-voices         | `Select.1`, `Select.2`, `Select.4`, F11             | `TestSelect_Precedence`, `TestSelect_Default`             |
| 6   | An unknown named person is reported and never fails the selection                                                | `Select.3`, F11, F4                                 | `TestSelect_UnknownPerson`                                |
| 7   | Nobody named and no confirmed clip, no `voices/`, or an unreadable library is the empty selection, and nothing is written | `Select.6`, `Select.8`, `Empty.1`, F3        | `TestSelect_EmptyWithoutVoices`                           |
| 8   | People named over an unreadable library is a selection that says so                                              | `Select.7`, F4                                      | `TestSelect_NamedButUnreadable`                           |
| 9   | Each selected person brings their two best-ranked clips, by the Ranker and nothing else                          | `Select.5`, L4.1, F7                                | `TestSelect_PicksTwoBest`                                 |
| 10  | A tally appends, per picked clip, a `placed` event saying whether it landed and a `named` event when it named someone, and shows up as the clip's namings | `Tally.1`–`Tally.5`, F6                             | `TestTally_AppendsOutcomes`, `TestTally_Refusals`         |
| 11  | A keep confirms once and is a success with nothing appended after that; an overrule always counts against the clip | `Review.1`–`Review.5`, F5, F6                       | `TestReview_Keep`, `TestReview_Overrule`, `TestReview_Refusals` |
| 12  | Removing a clip or a person takes it out of every listing, deletes the audio, and survives a merge with an older copy | `Remove.1`–`Remove.4`, F5, F10                 | `TestRemove_Clip`, `TestRemove_Person`, `TestRemove_Refusals` |
| 13  | Every failure is a typed error, and none carries audio                                                           | `Enrol.1`, `Enrol.4`, `Enrol.9`, `List.4`, `Tally.7`, `Review.6`, `Remove.5`, `Librarian.2`, F8 | `TestEnrol_Refusals`, `TestLibrarian_ReadAndWriteFailures`, `TestLibrarian_NoAudioLeaves` |
| 14  | A nil error from `Enrol`, `Tally`, `Review` or `Remove` means the event took effect, whatever the clock did       | `Librarian.3`, `Enrol.2`, `Tally.1`, `Review.1`, `Review.3`, `Remove.1`, L4.3 | `TestEnrol_HeldWhateverTheClock`, `TestLibrarian_EventsTakeEffectWhateverTheClock` |
| 15  | A prune whose file cannot be deleted still adds the new clip, and says which file is left                        | `Enrol.9`, `Enrol.5`, `Remove.5`, F5                | `TestEnrol_PruneLeavesAFile`                              |

## Test cases

Every test builds its library under the `testenv` home with `NewLibrarian(dir, NewRanker(), clock)`,
where `clock` is a test clock that steps one second per call. Inspections are built in the test from
distinct byte strings; the Librarian never decodes audio. A stated rank order is asserted against
`NewRanker().Rank` over the same histories, so no row fakes the Ranker.

| Test                                  | Level          | Proves                                                        | Given                                                                                                                                  | Then                                                                                                                                         | Must not                                              |
| ------------------------------------- | -------------- | ------------------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------- | ----------------------------------------------------- |
| `TestEnrol_WritesTheFormat`           | contract suite | `Enrol.2`, `Enrol.3`, `Librarian.1`, `List.2`, AC 1           | `Enrol` of a confirmed clip for `Lorentz`, then of an unconfirmed one for `LORENTZ`                                                     | `voices/lorentz/voice.json` is `{"name":"Lorentz"}`; two `.flac` files named by `ClipIDOf` hold the bytes; two `added` lines pass `event.schema.json` with distinct ids; `ReadLibrary` and `List` show one person, `Lorentz`, with both clips | Create `voices/LORENTZ`, or rewrite `voice.json`      |
| `TestEnrol_Refusals`                  | contract suite | `Enrol.1`, `Enrol.4`, AC 13                                   | Names `speaker-2` and `2pac`; status `"kept"`; source `"cli"`; an inspection with no bytes; the same clip twice for one person; then that clip removed and enrolled again | `VoiceNameError` twice, `EnrolmentError` with `status`, `source`, `inspection`; `ClipExistsError` with the ref; the re-enrolment succeeds with `Named` 0 | Create a directory or a line for a refused enrolment |
| `TestEnrol_PrunesLowestUnconfirmed`   | contract suite | `Enrol.5`, F5, F7, L4.2, AC 2                                 | A person with four confirmed and two unconfirmed clips with tallied histories; a seventh clip by `voices add`; the same library again with a seventh, unconfirmed clip by `audio_remember_voice` | Both times six clips remain; the one gone is the last unconfirmed clip of `NewRanker().Rank` over the six; it has a `removed` event and no file | Remove a confirmed clip, or more than one clip        |
| `TestEnrol_FullVoiceRefuses`          | contract suite | `Enrol.6`, F5, AC 2                                           | A person with six confirmed clips; a seventh by `voices add`, then a seventh by `audio_remember_voice`                                   | `VoiceFullError` with `Stored` 6 both times; the library reads as before                                                                     | Remove or replace any clip                            |
| `TestEnrol_ReplacesIsNotCounted`      | contract suite | `Enrol.5`, `Enrol.6`, F5, AC 2                                | A person with six confirmed clips; an enrolment with `Replaces` set to one of them; then a person with five confirmed and one unconfirmed, replacing a confirmed one | Both succeed with seven clips stored and nothing removed, the unconfirmed clip included                                                      | Prune the replaced clip, or prune at all              |
| `TestLibrarian_EventsTakeEffectWhateverTheClock` | contract suite | `Librarian.3`, `Tally.1`, `Review.1`, `Review.3`, `Remove.1`, `Remove.2`, `Enrol.5`, L4.3, AC 14 | Each case on a clock that stands still at one instant, on one that steps back an hour before the call, and over a clip whose `added` line is dated a year after the clock (a fixture written by hand). In turn, on fresh copies: `Tally` of the clip as landed and naming `["A"]`, together with a second clip whose events are all before the clock; `Review` with `Keep` on an unconfirmed clip; `Review` with `Overrule`, twice; `Remove` of the clip; `Remove` of the whole person; an `Enrol` at the cap (L4.2) that prunes that clip | Every call returns a nil error and its effect shows in `List` and `ReadLibrary`: `Named` 1 with `LastNamed` set and the `placed` counted; the clip `confirmed`, and its person in the default selection; `Overruled` 2; the clip in no listing; the person in no listing; the pruned clip in no listing and the new one held. Each appended line's `at` is exactly one nanosecond after the latest earlier line of its clip where that line is at or after the clock, and the clock's own time otherwise (the second tallied clip); the clip's `placed` and `named` lines of one `Tally` carry one time; every line passes `event.schema.json`, and every earlier line is byte for byte as before | Return a nil error for an event the fold ignores, stamp an event before the `added` it belongs to, stamp a clip's event by another clip's latest event, or rewrite a line |
| `TestEnrol_HeldWhateverTheClock`      | contract suite | `Enrol.2`, `Enrol.4`, `Librarian.3`, L4.3, AC 14              | In turn, each with its own clock: a clip enrolled, removed and enrolled again on a clock that returns one same instant for every call; the same on a clock that, for the second enrolment, returns an hour before the removal; a clip enrolled, tallied and overruled at instants after the clock's next value, then removed and enrolled again; a first enrolment on any clock | Every `Enrol` returns a nil error and `List` and `ReadLibrary` hold the clip afterwards, with no namings; the second `added` line's `at` is exactly one nanosecond after the latest earlier line of that clip in the first three, and the clock's own time in the fourth; every line passes `event.schema.json` | Return a nil error for a clip that is not listed, stamp `added` at or before an earlier event of the clip, or move the time of any other event |
| `TestEnrol_PruneLeavesAFile`          | contract suite | `Enrol.9`, `Enrol.5`, `Remove.5`, F5, AC 15                   | A person at the cap (L4.2) with one unconfirmed clip, ranked last; the deletion of that clip's file made to fail through the suite's file seam; a seventh clip enrolled, by `voices add` and again, on a fresh copy, by `audio_remember_voice` | Both times `Enrol` returns the new clip's ref, not zero, with a `*LibraryWriteError` whose `Path` is the pruned clip's file; `List` holds the new clip and not the pruned one, six clips; the pruned clip has its `removed` line; its file is still there and no reader counts it | Return the zero ref, report the new clip as not added, keep the pruned clip in a listing, or remove a confirmed clip |
| `TestEnrol_NeverPartial`              | contract suite | `Enrol.7`, F10, AC 3                                          | A reader goroutine calling `ReadLibrary` and hashing every `.flac` it is shown, during 50 enrolments; then an enrolment of bytes whose file already exists with no `added` event | Every file seen hashes to its name; no name ending in `.tmp` remains; the existing file keeps its modification time                           | Show a partial file, or rewrite an existing clip file |
| `TestLibrarian_ConcurrentWriters`     | contract suite | `Enrol.8`, `Tally.6`, `Librarian.1`, `NewLibrarian.1`, F10, AC 3 | Two Librarians over one directory; six goroutines each enrol one distinct clip for one new person (the cap is not passed), under two spellings, and each then tallies its clip as landed and overrules it ten times | All six clips are held; one `voice.json` with one of the two spellings; every line of `events.jsonl` is a whole event, `Skipped` is 0, and the lines number 6 plus the sum of `Tallied.Events` plus 60 | Lose a line, interleave two lines, or fail under `-race` |
| `TestList_OrderAndFields`             | contract suite | `List.1`, `List.2`, F7, AC 4                                  | `syrus`, `Christoph` and `lorentz` with the clips and events of "The voices menu, by example", and a person whose only clip was removed   | Christoph, lorentz, syrus; Lorentz's clips `9f3c…`, `e81d…`, `1a7d…` with `Named` 14, 3, 0, `Overruled` 1, 2, 0, `LastNamed` nil for the third; each `Path` is absolute and exists; the removed person is absent | List a person with no clip                            |
| `TestList_JSON`                       | contract suite | `List.3`, `List.5`, AC 4                                      | The same library encoded with `encoding/json`; then a library with nobody; then no `voices/`; a listing of every file before and after     | The keys are exactly `name`, `slug`, `last_named`, `clips` and `id`, `status`, `source`, `seconds`, `added`, `named`, `overruled`, `last_named`, `path`; the empty cases encode as `[]`; no file changed | Encode `null` for an empty library, or create `voices/` |
| `TestSelect_Precedence`               | contract suite | `Select.1`, `Select.2`, F11, AC 5                             | Three people with confirmed clips and one with only an unconfirmed clip; `Asked{Tool: ["SYRUS"], Flag: ["Lorentz"]}`; `Asked{Flag: ["lorentz", "Christoph", "Lorentz", <the unconfirmed person>]}`; `Asked{}` with `MaxVoices` 12 | `OriginTool` with Syrus only; `OriginFlag` with Lorentz, Christoph and the unconfirmed person, once each, in that order; `OriginLibrary` with the three confirmed people | Merge the tool's list with the flag's                 |
| `TestSelect_Default`                  | contract suite | `Select.4`, F11, F7, AC 5                                     | Five people with confirmed clips of different histories, one person with only unconfirmed clips; `MaxVoices` 12, 3 and 0                 | All five in the order `NewRanker().Rank` gives their best clips; the first three of that order; nobody (an `Empty` selection)                 | Select the unconfirmed-only person by default         |
| `TestSelect_UnknownPerson`            | contract suite | `Select.3`, F11, F4, AC 6                                     | `Asked{Flag: ["Lorentz", "Ada", "speaker-1"]}`; then `Asked{Tool: ["Ada"]}`                                                              | Lorentz selected, `Unknown` is `["Ada", "speaker-1"]`; then no voice, `Unknown` `["Ada"]`, `Empty` false                                      | Panic, drop the known person, or report `Empty`       |
| `TestSelect_EmptyWithoutVoices`       | contract suite | `Select.6`, `Select.8`, `Empty.1`, F3, AC 7                   | `Asked{MaxVoices: 12}` over: no `voices/`; a library whose clips are all unconfirmed; `voices` that is a regular file; a file listing before and after | `Empty` is true each time, `Origin` is `OriginNone`, `Unreadable` is nil; no file changed                                                    | Create `voices/`, or report the unreadable library    |
| `TestSelect_NamedButUnreadable`       | contract suite | `Select.7`, F4, AC 8                                          | `Asked{Flag: ["Lorentz"]}` over `voices` that is a regular file                                                                          | No voice, no unknown person, `Unreadable` holds the `LibraryReadError` with that path, `Empty` false                                          | List Lorentz as unknown                               |
| `TestSelect_PicksTwoBest`             | contract suite | `Select.5`, L4.1, F7, AC 9                                    | Lorentz with the three clips of the menu example, Christoph with one unconfirmed clip, both named                                        | Lorentz's picks are `9f3c…` then `e81d…`, the head of `NewRanker().Rank`; Christoph's is his one clip with status `unconfirmed`; each `Path` exists | Pick a third clip, or order picks by anything but rank |
| `TestTally_AppendsOutcomes`           | contract suite | `Tally.1`, `Tally.2`, `Tally.4`, `Tally.5`, F6, F7, F3, AC 10 | Outcomes: clip A landed and naming `["A", "C"]`, clip B landed naming nobody, clip C not landed, clip D removed before the call; then an empty call on a config directory with no `voices/` | `Tallied{Events: 4, Dropped: [D]}`; A has a `placed` line with `landed` true and a `named` line with those speakers, B a `placed` line with `landed` true, C a `placed` line with `landed` false, all at one time; `List` shows A `Named` 1 with `LastNamed` set, and C now ranks below an untallied twin; the empty call returns `Tallied{}` and creates nothing | Skip the line for C, write a line for D, or create `voices/` |
| `TestTally_Refusals`                  | contract suite | `Tally.3`, AC 10                                              | Two valid outcomes and one with `Named: ["A"]` and `Landed` false                                                                        | `OutcomeError` with that clip; `events.jsonl` is byte for byte as before                                                                     | Append the valid outcomes                             |
| `TestReview_Keep`                     | contract suite | `Review.1`, `Review.2`, F5, F6, AC 11                         | An unconfirmed clip reviewed with `Keep`, then again                                                                                    | `ReviewConfirmed` and one `confirmed` line; the returned clip and `List` say `confirmed`; a person with only that clip is now in the default selection; the second call returns `ReviewAlreadyConfirmed`, a nil error and the same clip, with `events.jsonl` byte for byte as before | Append a second `confirmed` event, or return an error |
| `TestReview_Overrule`                 | contract suite | `Review.3`, `Review.5`, F5, F6, AC 11                         | A confirmed clip with four namings, overruled three times                                                                               | `ReviewOverruled` three times and three `overruled` lines; the last returned clip has `Named` 4, `Overruled` 3 and status `confirmed`; the file is still there | Remove the clip or change its status                  |
| `TestReview_Refusals`                 | contract suite | `Review.4`, AC 11                                             | A ref to a removed clip, a ref to an unknown person, verdict `"maybe"`                                                                   | `ClipNotFoundError`, `ClipNotFoundError`, `VerdictError`; no line appended                                                                   | Create a directory for the unknown person             |
| `TestRemove_Clip`                     | contract suite | `Remove.1`, `Remove.4`, F5, AC 12                             | Lorentz with three clips and Syrus with one; `Remove("lorentz", <a confirmed clip>)`                                                      | That ref returned; one `removed` line; its file is gone; `List` and `Select` show the other two; Syrus's directory is byte for byte as before | Touch another clip or person                          |
| `TestRemove_Person`                   | contract suite | `Remove.2`, F10, AC 12                                        | `Remove("lorentz", "")`; then the directory merged with a copy taken before the removal (files and lines of both)                        | Three refs returned; Lorentz is in no listing; `voice.json` and `events.jsonl` remain; after the merge Lorentz is still not listed            | Delete the directory or the event log                 |
| `TestRemove_Refusals`                 | contract suite | `Remove.3`, AC 12                                             | `Remove("ada", "")`; `Remove("lorentz", "ffffffffffff")`                                                                                 | `VoiceNotFoundError`; `ClipNotFoundError` with the ref; nothing written                                                                      | Append a `removed` line for a clip that is not held   |
| `TestLibrarian_ReadAndWriteFailures`  | contract suite | `Enrol.9`, `List.4`, `Tally.7`, `Review.6`, `Remove.5`, AC 13 | `voices` that is a regular file, for all six members; then a voice directory made read-only, for `Enrol`, and a voice whose `events.jsonl` is read-only, for `Tally`, `Review` and `Remove` | `LibraryReadError` from `Enrol`, `List`, `Tally`, `Review`, `Remove`, and in `Select`'s `Unreadable` when a person is named; `LibraryWriteError` naming the path from the four writers, `Enrol`'s beside the zero ref; the library reads as before | Return a nil error with nothing written               |
| `TestLibrarian_NoAudioLeaves`         | contract suite | `Librarian.2`, F8, AC 13                                      | A clip whose bytes hold a marker string; every error of the rows above, the JSON of `List`, and a `Selection`, rendered as text; a Librarian built with no `Runner` | The marker appears in none of them; `NewLibrarian` takes no runner and no network client                                                     | Put clip bytes in an error, a summary or a selection  |

## Error management

| Failure                                                         | Expected outcome                                                              | Test                                 |
| --------------------------------------------------------------- | ----------------------------------------------------------------------------- | ------------------------------------ |
| A name the name rule refuses                                    | `*VoiceNameError` from `ParseName`; nothing written                           | `TestEnrol_Refusals`                 |
| A status or source that is not declared, or no audio            | `*EnrolmentError` naming `status`, `source` or `inspection`                   | `TestEnrol_Refusals`                 |
| The person already holds the clip                               | `*ClipExistsError` with the ref                                               | `TestEnrol_Refusals`                 |
| Six clips, all confirmed                                        | `*VoiceFullError` with the stored count; nothing removed                      | `TestEnrol_FullVoiceRefuses`         |
| A named person the library does not hold (`Select`)             | Not an error: left out, listed in `Selection.Unknown`                         | `TestSelect_UnknownPerson`           |
| Library unreadable, nobody named (`Select`)                     | Not an error: the `Empty` selection                                           | `TestSelect_EmptyWithoutVoices`      |
| Library unreadable, people named (`Select`)                     | Not an error: `Selection.Unreadable` holds the `*LibraryReadError`            | `TestSelect_NamedButUnreadable`      |
| An outcome naming someone without landing                       | `*OutcomeError`; nothing appended for the call                                | `TestTally_Refusals`                 |
| An outcome for a clip removed since it was picked               | Not an error: nothing appended, the ref in `Tallied.Dropped`                  | `TestTally_AppendsOutcomes`          |
| `Keep` on a confirmed clip                                      | Not an error: `ReviewAlreadyConfirmed`, nothing appended                      | `TestReview_Keep`                    |
| A review of a clip the library does not hold                    | `*ClipNotFoundError`                                                          | `TestReview_Refusals`                |
| A verdict that is neither `keep` nor `overrule`                 | `*VerdictError`                                                               | `TestReview_Refusals`                |
| Removing from a person the library does not hold                | `*VoiceNotFoundError`                                                         | `TestRemove_Refusals`                |
| Removing a clip the person does not hold                        | `*ClipNotFoundError`                                                          | `TestRemove_Refusals`                |
| The library cannot be read (`Enrol`, `List`, `Tally`, `Review`, `Remove`) | `*LibraryReadError` naming the path                                 | `TestLibrarian_ReadAndWriteFailures` |
| A write, an append or a deletion fails                          | `*LibraryWriteError` naming the path; the library reads as before, except that a clip whose `removed` line was appended is out | `TestLibrarian_ReadAndWriteFailures` |
| `Enrol` pruned a clip and cannot delete its file                | The new clip's ref with a `*LibraryWriteError` naming the leftover file; the new clip is held, the pruned clip is out | `TestEnrol_PruneLeavesAFile`         |
| The clock stepped back, stands still, or runs behind an event in the log (`Enrol`, `Tally`, `Review`, `Remove`) | Not an error: the event is stamped one nanosecond after the clip's latest event, and it takes effect | `TestEnrol_HeldWhateverTheClock`, `TestLibrarian_EventsTakeEffectWhateverTheClock` |

## Golden diff

None.

## Removes

None.

## Local facts

Moved unchanged from the README's Phase-local facts:

| Parameter                                | Default            | Decided by                     |
| ---------------------------------------- | ------------------ | ------------------------------ |
| L4.1 clips per person per naming request | 2, a code constant | maintainer, 2026-11-18 (Q5)    |
| L4.2 stored clips per person             | 6, a code constant | maintainer, 2026-11-18 (Q10')  |

**L4.3 — An event outlasts the clock.** The library folds a clip's events by time (phase 1,
`ReadLibrary.4`–`.6`): an `added` event starts the clip anew and only events at or after it count. An
`added` stamped at or before a `removed` of the same clip would therefore leave the clip out although
`Enrol` returned nil, and a `removed`, `confirmed`, `overruled`, `placed` or `named` stamped before the
clip's `added` would be ignored although its call returned nil. So no event is stamped at or before an
event of the same clip that the log already holds: every appended event takes the later of the Clock's
time and one nanosecond after that clip's latest event. One nanosecond is the finest step of an event's
`at` (phase 1, `event.schema.json`). The rule is stated in `Librarian.3`. Its one owner is the step
through which every member of the Librarian appends: no member stamps on its own, the Librarian is the
only writer of events, and phase 1's `EncodeEvent` stays a codec that stamps nothing. It orders a call after the lines it read; two calls that race on one clip are ordered
only as `Enrol.8` and `Tally.6` say. Binds `Librarian` (`Librarian.3`), `Enroller.Enrol`,
`Tallier.Tally`, `Reviewer.Review`, `Remover.Remove`. (Maintainer, 2026-11-19, review-1 design question
"Enrol.2 stamping", for `Enrol`; widened to every event writer by the orchestrator, 2026-11-19.)

## Human required

None.

## Implementation notes

| Run   | Commit      | Command                                                                                                                                                                  | Result                                                                                                          |
| ----- | ----------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------ | --------------------------------------------------------------------------------------------------------------- |
| start | 1550dec+wip | `go test ./internal/audio/voices/... ./pkg/diagnostic/... -race -count=3 -cover -timeout=30s -p 4`; `go vet`, `staticcheck`, `gofumpt -l` on `internal/audio/voices`; `dupl -t 80 .` | 162 passed, 0 failed (54 tests ×3); vet, staticcheck, gofumpt clean; dupl 49 groups                              |
| end   | 1550dec+wip | the same                                                                                                                                                                 | 237 passed, 0 failed (79 tests ×3); voices 92.6 % (phase 4 files 89.5 %); vet, staticcheck, gofumpt clean; dupl 49 groups, none new |
| end   | 1550dec+wip | the same with `-shuffle=on`                                                                                                                                              | 237 passed, 0 failed; voices 92.6 %; vet, staticcheck, gofumpt clean; dupl 49 groups, none new                   |
| fix-start | 1550dec+wip | `env -u OPENAI_API_KEY -u ANTHROPIC_API_KEY -u OPENROUTER_API_KEY go test ./internal/audio/voices/... ./internal/menu/... ./pkg/diagnostic/... -race -count=3 -cover -timeout=30s -p 4`; `go vet`, staticcheck and `gofumpt -l` on `internal/audio/voices/...`, `internal/menu/...`; `dupl -t 80 .` (R1-01, R1-03, R1-04, R1-05, R1-06) | all ok: voices 6.0 s 92.4 %, voicestest 2.2 s 94.4 %, menu 1.4 s 96.0 %, diagnostic 2.9 s 93.3 %; vet, staticcheck, gofumpt clean; dupl 49 groups |
| fix-end | 1550dec+wip | the same | all ok (load average 34): voices 8.4 s 95.4 %, voicestest 2.6 s 94.4 %, menu 1.6 s 96.0 %, diagnostic 3.1 s 93.3 %; voices 63 tests, 248 with subtests, menu 33 and 166, per count; vet, staticcheck, gofumpt clean; dupl 49 groups, none in voices or menu |
| fix-end | 1550dec+wip | the same with `-shuffle=on` | all ok (load average 21): voices 8.4 s 95.4 %, voicestest 2.4 s 94.4 %, menu 1.5 s 96.0 %, diagnostic 3.0 s 93.3 %; vet, staticcheck, gofumpt clean; dupl 49 groups |
| fix-start | 1550dec+wip | none (V5-01: this document only, no code changed) | gates not run |
| fix-end | 1550dec+wip | none (V5-01: `Librarian.3` ends at the observable rule; the ownership sentence is in L4.3 alone) | gates not run |
| fix-start | 1550dec+wip | `env -u OPENAI_API_KEY -u ANTHROPIC_API_KEY -u OPENROUTER_API_KEY go test ./internal/audio/voices/... -race -count=3 -cover -timeout=30s -p 4`; `go vet`, staticcheck, `gofumpt -l` on `internal/audio/voices` (R1-05, R1-13) | all ok: voices 7.3 s 95.4 %, voicestest 2.1 s 94.4 %; vet, staticcheck, gofumpt clean |
| fix-end | 1550dec+wip | the same | all ok: voices 9.2 s 95.4 %, voicestest 2.1 s 94.4 %; vet, staticcheck, gofumpt clean |
| fix-end | 1550dec+wip | the same with `-shuffle=on` | all ok: voices 9.2 s 95.4 %, voicestest 2.1 s 94.4 %; vet, staticcheck, gofumpt clean |

Andon: none.
Surprises:
- `voice.json` is written to `.voice.json.<random>.tmp` and hard-linked into place (`os.Link` fails on an existing file), so a concurrent `Tally` or `Review` never reads a half-written record; an `O_EXCL` create is the fallback where links fail (WASIX), and it is untested.
- A mutant that renames `voice.json` into place instead of linking it survives: the create race of `Enrol.8` cannot be forced through the interface, so only `TestLibrarian_ConcurrentWriters` probes it.
- An append first closes a last line a failed writer left without its newline; otherwise that torn line swallows the next event (`TestLibrarian_ConcurrentWriters`, torn-line row).
- When a pruned clip's file cannot be deleted after its `removed` line is appended, `Enrol` returns the new ref with a `*LibraryWriteError`: the pruned clip is out (as `Remove.5` says), so `Enrol.9`'s "reads as before" cannot hold in that case.
- The voices package's gate run grew from 10.4 s to 13.4 s under `-race -count=3`.
- R1-01: a voice the library does not hold is checked before anything is written (a symbolic link or other non-directory in its place, or a `voice.json` that is not its record, is a `*LibraryWriteError` and nothing is written), and `voice.json` is read back after the create or the keep; an entry at `<id>.flac` that is not a regular file is no longer taken for the clip.
- R1-03: the librarian's unexported `files` (link, exclusive create) is the seam; `export_test.go`'s `NewLibrarianWithoutLinks` runs the `Enrol.3`, `Enrol.7`, `Enrol.8` rows through the fallback, and `NewLibrarianUnwritable` makes its writes fail.
- R1-04: `.<id>.flac.tmp` is created with `O_EXCL`; one that exists is another writer's, waited for up to 63 ms (1 ms doubling to 32 ms), as is an empty or missing `voice.json` the fallback has created but not filled. A `.tmp` a crashed writer left therefore blocks that one clip with a `*LibraryWriteError` naming it until it is removed.
- R1-05: the Enrol prune whose file cannot be deleted (`library.go:121`) has no row: `Enrol.9` and `Remove.5` disagree on it, and the question is with the maintainer.
- R1-13: every member appends through `librarian.encode`, which reads the voice's log (`storeReadEvents`) and stamps each event per clip (`librarianStamp`, Librarian.3); a Clock time no event can carry (zero, year outside 0-9999) is left as it is so the encoder still refuses it with an `*EventError` (the existing "a Clock time no event can carry" row). The `Librarian` interface comments were brought to the signed text of `Librarian.3`, `Enrol.2`, `Enrol.9`, `Tally.1`, `Review.1`, `Review.3`, `Remove.1`. The tests mutate-checked: with the stamp disabled, `TestLibrarian_EventsTakeEffectWhateverTheClock` and `TestEnrol_HeldWhateverTheClock` fail 25 subtests.
- R1-05: the code already behaved as `Enrol.9` says; `TestEnrol_PruneLeavesAFile` is test-only. The deletion seam is `librarianFiles.remove` (`NewLibrarianKeeping` in `export_test.go`), since a `0555` directory would also stop the appends.

## Review findings

### Review 2, 2026-11-19

Root review round 2 at `1550dec+wip`. No finding.

- **R1-05, R1-13 resolved.** `librarian.encode` (`internal/audio/voices/library.go:444-465`) is the one step `Enrol`, `Tally`, `Review` and `Remove` append through; `librarianStamp` (`:470-481`) takes the later of the clock and one nanosecond after the clip's latest logged event, whatever the order of the lines (`Librarian.3`). `TestLibrarian_EventsTakeEffectWhateverTheClock`, `TestEnrol_HeldWhateverTheClock`, `TestEnrol_PruneLeavesAFile` and `TestLibrarian_ConcurrentWriters` pass 5 shuffled race runs.
- **note** On the built binary, delete then `voices add` of the same file holds the clip again, and a trim, two moves and three deletes leave a log whose every `removed` and `added` is later than the clip's previous event.
- **note** A `Tally` gives a clip's `placed` and `named` one `at` (`Tally.1` says so); both count after the fold. The Ranker reads an event later than its `now` at full weight (`ranker.go:80-85`), so a stamp ahead of a clock that stepped back does not skew the rank.
