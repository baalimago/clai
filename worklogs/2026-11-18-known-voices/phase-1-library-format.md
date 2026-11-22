# Phase 1 — Library format

**Parent:** root · **Class:** contract · [README](README.md)

## Goal

Fix the files of the voice library, `<clai-config>/voices/<slug>/voice.json`, `<id>.flac` and
`events.jsonl`, their record schemas, the name, slug and clip-id rules, and the one way a reader folds
them, so that every later phase and any copy of the files reads the same library.

## Interface

A data contract for files. Written to `internal/audio/voices/schema/voice.schema.json` and
`internal/audio/voices/schema/event.schema.json` (the record schemas), with the layout below and the Go
mirror of both in `internal/audio/voices/store.go`. The paths, the record fields and the Go members
below are the frozen surface; everything else is body.

### Layout

```text
<clai-config>/voices/
  <slug>/                 one voice (one person)
    voice.json            the person's name, one record of voice.schema.json
    <id>.flac             one clip: 16 kHz mono FLAC
    events.jsonl          one record of event.schema.json per line, append only
    .<id>.flac.tmp        a clip being written; never part of the library
```

- store.1 — A voice is a directory `voices/<slug>/` whose `voice.json` is a record of
  `voice.schema.json` and whose name has that slug (`SlugOf`). Every other entry under `voices/` is not
  part of the library, and is never an error. (F10)
- store.2 — `voice.json` holds only the person's name. It is written once and never rewritten; the
  directory name, not a later writer's spelling, identifies the person. (F10)
- store.3 — A clip is `voices/<slug>/<id>.flac`, a 16 kHz mono FLAC, where `<id>` is the first 12
  lower-case hex digits of the sha256 of the file's bytes (`ClipIDOf`). A clip file is never rewritten:
  one id is one content. (F10)
- store.4 — `events.jsonl` is append only, one event per line. A line counts only once it ends in a
  newline. A complete line that is not a record of `event.schema.json`, a blank line included, is not
  an event and hides no other line. (F10)
- store.5 — A name that starts with `.` is never part of the library: not as a voice, a clip or a log.
  (F10)
- store.6 — A clip is in the library when its voice's events hold it (`ReadLibrary.4`–`.7`) and its
  `<id>.flac` exists. A `.flac` with no `added` event, and an `added` event with no file, are not clips.
- store.7 — The union of two copies is their merge: a directory that holds the files of both copies
  and, per `events.jsonl`, the complete lines of both in any order and with any line repeated, reads as
  one library, the same whichever copy came first. (F10)
- store.8 — No record carries audio. A clip's audio is only in its `.flac`. (F8)

The writer's side of F10 (the exclusive create of `voice.json`, the temporary name and rename of a clip,
one appended line per fact) is observable only through a writer; its clauses are `Enrol`, `Tally`,
`Review` and `Remove` in phase 4.

### `internal/audio/voices/schema/voice.schema.json`

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "voice.schema.json",
  "title": "voices/<slug>/voice.json",
  "type": "object",
  "required": ["name"],
  "additionalProperties": false,
  "properties": {
    "name": {
      "type": "string",
      "pattern": "^\\p{L}[\\p{L}\\p{N} .'’-]{0,38}$",
      "not": { "pattern": "^([sS][pP][eE][aA][kK][eE][rR]|[uU][nN][kK][nN][oO][wW][nN])-\\d+$" }
    }
  },
  "examples": [{ "name": "Lorentz" }, { "name": "Jean-Luc P." }]
}
```

### `internal/audio/voices/schema/event.schema.json`

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "event.schema.json",
  "title": "one line of voices/<slug>/events.jsonl",
  "type": "object",
  "required": ["id", "at", "event", "clip"],
  "properties": {
    "id": { "type": "string", "pattern": "^[0-9a-f]{16}$" },
    "at": { "type": "string", "pattern": "^\\d{4}-\\d{2}-\\d{2}T\\d{2}:\\d{2}:\\d{2}(\\.\\d{1,9})?Z$" },
    "event": { "enum": ["added", "confirmed", "overruled", "removed", "placed", "named"] },
    "clip": { "type": "string", "pattern": "^[0-9a-f]{12}$" },
    "status": { "enum": ["confirmed", "unconfirmed"] },
    "source": { "enum": ["voices add", "audio_remember_voice"] },
    "seconds": { "type": "number", "exclusiveMinimum": 0 },
    "landed": { "type": "boolean" },
    "speakers": { "type": "array", "minItems": 1, "items": { "type": "string", "minLength": 1 } }
  },
  "allOf": [
    {
      "if": { "properties": { "event": { "const": "added" } } },
      "then": { "required": ["status", "source", "seconds"] }
    },
    {
      "if": { "properties": { "event": { "const": "placed" } } },
      "then": { "required": ["landed"] }
    },
    {
      "if": { "properties": { "event": { "const": "named" } } },
      "then": { "required": ["speakers"] }
    }
  ],
  "examples": [
    { "id": "5a1f0c9e2b7d4a63", "at": "2026-11-18T09:12:44.120394Z", "event": "added", "clip": "9f3c2a1b0d4e", "status": "confirmed", "source": "voices add", "seconds": 8.4 },
    { "id": "0c44e1a97f20b3d8", "at": "2026-11-24T16:02:10Z", "event": "added", "clip": "1a7d0f23c9b8", "status": "unconfirmed", "source": "audio_remember_voice", "seconds": 9.7 },
    { "id": "77b0d2c4e19a5f06", "at": "2026-11-25T10:00:03Z", "event": "placed", "clip": "9f3c2a1b0d4e", "landed": true },
    { "id": "4d2e8f0a1b3c5d79", "at": "2026-11-25T10:00:03Z", "event": "placed", "clip": "e81d5530c6a9", "landed": false },
    { "id": "e3a9b6d0c1f24875", "at": "2026-11-25T10:00:03Z", "event": "named", "clip": "9f3c2a1b0d4e", "speakers": ["A"] },
    { "id": "19d8c7b6a5f4e3d2", "at": "2026-11-25T10:20:00Z", "event": "overruled", "clip": "e81d5530c6a9" },
    { "id": "aa01bb02cc03dd04", "at": "2026-11-26T08:00:00Z", "event": "confirmed", "clip": "1a7d0f23c9b8" },
    { "id": "f0e1d2c3b4a59687", "at": "2026-11-26T08:05:00Z", "event": "removed", "clip": "e81d5530c6a9" }
  ]
}
```

What each event says:

| `event`     | Fact                                                                                         | Extra fields                  |
| ----------- | -------------------------------------------------------------------------------------------- | ----------------------------- |
| `added`     | the clip was enrolled with this status, by `voices add` or `audio_remember_voice`, this long | `status`, `source`, `seconds` |
| `confirmed` | the user kept the clip                                                                       | none                          |
| `overruled` | one name this clip gave was corrected                                                        | none                          |
| `removed`   | the clip was removed                                                                         | none                          |
| `placed`    | the clip was picked for the naming of one recording; `landed` says whether it held its own region in at least one of the two orders (L5.2) | `landed`      |
| `named`     | in one recording the clip named these transcript speakers (their labels as transcribed)      | `speakers`                    |

`id` is unique per event, so two facts that are otherwise equal are two lines. `at` is UTC. A reader
ignores a field it does not know.

### `internal/audio/voices/store.go`

```go
// Package voices is the library of named voice clips under <clai-config>/voices.
//
// - Package.1 — The package imports nothing from internal/audio. (F9)
package voices

import "time"

// The names of the layout, relative to the clai config directory and to one
// voice's directory.
const (
	Dir        = "voices"
	VoiceFile  = "voice.json"
	EventsFile = "events.jsonl"
	ClipExt    = ".flac"
)

// Name is a person's name as voice.json stores it.
type Name string

// Slug is the directory name of a voice.
type Slug string

// ClipID names one clip within a voice: 12 lower-case hex digits.
type ClipID string

// ClipRef names one clip in the library. Its text form is "<slug>/<id>".
type ClipRef struct {
	Slug Slug
	ID   ClipID
}

// String is the text form, "<slug>/<id>".
func (r ClipRef) String() string

// Status says whether the user stands behind a clip. (F5)
type Status string

const (
	Confirmed   Status = "confirmed"
	Unconfirmed Status = "unconfirmed"
)

// Source says which member enrolled a clip.
type Source string

const (
	SourceAdd      Source = "voices add"
	SourceRemember Source = "audio_remember_voice"
)

// EventKind is the fact one event line states.
type EventKind string

const (
	EventAdded     EventKind = "added"
	EventConfirmed EventKind = "confirmed"
	EventOverruled EventKind = "overruled"
	EventRemoved   EventKind = "removed"
	EventPlaced    EventKind = "placed"
	EventNamed     EventKind = "named"
)

// Event is one record of event.schema.json.
type Event struct {
	ID       string    `json:"id"`
	At       time.Time `json:"at"`
	Kind     EventKind `json:"event"`
	Clip     ClipID    `json:"clip"`
	Status   Status    `json:"status,omitempty"`
	Source   Source    `json:"source,omitempty"`
	Seconds  float64   `json:"seconds,omitempty"`
	// Landed is set on a placed event and nil on every other.
	Landed   *bool     `json:"landed,omitempty"`
	Speakers []string  `json:"speakers,omitempty"`
}

// VoiceRecord is the one record of voice.schema.json.
type VoiceRecord struct {
	Name Name `json:"name"`
}

// ClipHistory is one clip the events hold, and everything said about it since
// it was last added.
type ClipHistory struct {
	Ref     ClipRef
	Status  Status
	Source  Source
	Seconds float64
	Added   time.Time
	// Events are the clip's events from its latest added event on, that event
	// first, in time order.
	Events []Event
}

// SkippedLines counts the complete lines of one events.jsonl that are not
// events. (store.4)
type SkippedLines int

// StoredVoice is one voice as the files hold it.
type StoredVoice struct {
	Name    Name
	Slug    Slug
	Clips   []ClipHistory
	Skipped SkippedLines
}

// NameRule is the rule a refused name broke.
type NameRule string

const (
	// NameShape: the name does not match ^\p{L}[\p{L}\p{N} .'’-]{0,38}$.
	NameShape NameRule = "shape"
	// NameReserved: the name matches ^(speaker|unknown)-\d+$ in any case, so
	// it could be taken for a label a transcript gives.
	NameReserved NameRule = "reserved"
)

// VoiceNameError refuses a name. Its message holds the name and the rule.
type VoiceNameError struct {
	Name string
	Rule NameRule
}

func (e *VoiceNameError) Error() string

// ClipRefError refuses a text that is not "<slug>/<id>".
type ClipRefError struct{ Ref string }

func (e *ClipRefError) Error() string

// EventError refuses an event that is not a record of event.schema.json.
// Field is the JSON field that is missing or wrong.
type EventError struct {
	Event Event
	Field string
}

func (e *EventError) Error() string

// LibraryReadError says the library could not be read: Path is the entry that
// failed, Err why.
type LibraryReadError struct {
	Path string
	Err  error
}

func (e *LibraryReadError) Error() string
func (e *LibraryReadError) Unwrap() error

// ParseName applies the name rule.
//
// - ParseName.1 — A text that matches ^\p{L}[\p{L}\p{N} .'’-]{0,38}$ and
//   does not match ^(speaker|unknown)-\d+$ in any case is returned unchanged,
//   nothing trimmed.
// - ParseName.2 — Any other text is a *VoiceNameError with rule NameShape,
//   or NameReserved for a speaker-N or unknown-N label in any case
//   ("speaker-3", "Speaker-1", "UNKNOWN-2").
func ParseName(raw string) (Name, error)

// SlugOf is the slug rule: the directory a name is stored under. The file name
// is this slug, never the raw name.
//
// - SlugOf.1 — The name is lower-cased, every run of characters that are
//   neither letters nor digits becomes one "-", and a leading or trailing "-"
//   is dropped: "Lorentz" is "lorentz", "Jean-Luc P." is "jean-luc-p",
//   "O’Brien" is "o-brien", "Søren 2" is "søren-2".
// - SlugOf.2 — Names that differ only in case, or only in which separators
//   they use, have one slug and are one person; a slug is never empty, never
//   starts with "." and holds no path separator. (F10)
func SlugOf(n Name) Slug

// ClipIDOf is the clip-id rule.
//
// - ClipIDOf.1 — The id is the first 12 lower-case hex digits of the sha256
//   of the FLAC bytes, so equal audio files have one id and two writers never
//   allocate one id for different files. (F10)
func ClipIDOf(flac []byte) ClipID

// ParseClipRef reads the text form of a clip reference.
//
// - ParseClipRef.1 — "<slug>/<id>" with a non-empty slug and an id of 12
//   lower-case hex digits round-trips through String; any other text is a
//   *ClipRefError holding it.
func ParseClipRef(s string) (ClipRef, error)

// EncodeEvent renders one event as the line a writer appends.
//
// - EncodeEvent.1 — The result is one JSON object that satisfies
//   event.schema.json, followed by one newline, with at in UTC.
// - EncodeEvent.2 — An event the schema refuses (a bad id or clip, a zero
//   time, an unknown kind, an added event without status, source or positive
//   seconds, a placed event without landed, a named event without
//   speakers) is an *EventError naming the
//   field, and no line.
func EncodeEvent(e Event) ([]byte, error)

// ReadLibrary reads every voice under <configDir>/voices by the layout rules.
// It is the one fold of the event lines: every reader of the library sees
// what it returns.
//
// - ReadLibrary.1 — With no voices directory the result is no voice and no
//   error. (F3)
// - ReadLibrary.2 — Voices come in slug order, each with its stored name, the
//   clips its events hold whose file exists, and the count of complete lines
//   that are not events. A voice whose clips are all removed is returned with
//   no clip. (store.1, store.4, store.6)
// - ReadLibrary.3 — An entry that is not part of the library (store.1,
//   store.5), an unfinished last line and a line that is not an event are
//   skipped, never an error. (F10)
// - ReadLibrary.4 — A clip is held when the events have an added event for it
//   and no removed event at or after its latest added event. A clip added
//   again after a removal is held again.
// - ReadLibrary.5 — A clip's history is its events at or after its latest
//   added event; what was said about an earlier life of the id is not
//   counted. An event for a clip that was never added is ignored.
// - ReadLibrary.6 — A clip is confirmed when its latest added event says so
//   or a confirmed event follows it; otherwise it is unconfirmed. Its source
//   and seconds are those of its latest added event. (F5)
// - ReadLibrary.7 — The order of the lines does not matter and events with
//   one id count once, so the union of two copies' lines reads as their
//   merge. (F10, store.7)
// - ReadLibrary.8 — A voices directory, a voice.json or an events.jsonl that
//   exists and cannot be read is a *LibraryReadError naming its path.
// - ReadLibrary.9 — Reading writes nothing, under voices or anywhere else.
//   (F3)
func ReadLibrary(configDir string) ([]StoredVoice, error)
```

Consumes:

- `internal/testenv:Main` (import)

## Acceptance criteria

| #   | Criterion                                                                                                   | Proves                                         | Proof                                   |
| --- | ----------------------------------------------------------------------------------------------------------- | ---------------------------------------------- | --------------------------------------- |
| 1   | A library written by hand from the layout and the schemas' examples reads back as those voices and clips    | store.1, store.3, store.6, `ReadLibrary.2`     | `TestStore_WriteThenRead`               |
| 2   | Names are accepted and refused exactly by the two patterns, and the stored file name is the slug            | `ParseName.1`, `ParseName.2`, `SlugOf.1`, `SlugOf.2` | `TestStore_NameRule`, `TestStore_SlugRule` |
| 3   | A clip id is the first 12 hex digits of its file's sha256                                                   | `ClipIDOf.1`, store.3, F10                     | `TestStore_ClipID`                      |
| 4   | Dot names, foreign entries, an unfinished line and a broken line never change or fail a read                | store.1, store.4, store.5, `ReadLibrary.3`, F10 | `TestStore_NotPartOfTheLibrary`        |
| 5   | The union of two copies' files and lines, in either order and with repeated lines, reads as one same library | store.7, `ReadLibrary.7`, F10                  | `TestStore_UnionIsMerge`                |
| 6   | Added, removed, added again, confirmed: the events alone decide what is held and with which status          | `ReadLibrary.4`, `ReadLibrary.5`, `ReadLibrary.6`, F5 | `TestStore_Fold`                 |
| 7   | Every line `EncodeEvent` returns satisfies `event.schema.json`, and every example of both schemas is read   | `EncodeEvent.1`, store.2, store.8              | `TestStore_SchemaAndCodecAgree`         |
| 8   | No `voices/` reads as an empty library; an unreadable one is a `LibraryReadError`; reading writes nothing   | `ReadLibrary.1`, `ReadLibrary.8`, `ReadLibrary.9`, F3 | `TestStore_ReadMissing`, `TestStore_ReadUnreadable`, `TestStore_ReadWritesNothing` |
| 9   | The package imports nothing from `internal/audio`                                                           | `Package.1`, F9                                | `TestPackage_ImportsNoAudio`            |

## Test cases

Every test writes real files under the `testenv` home and reads them back; the package's `TestMain` is
`testenv.Main`. The schema files are read by the tests, which check the keywords the two schemas use
(`required`, `enum`, `pattern`, `not`, `additionalProperties`, the two `if`/`then` pairs) with a
test-local checker; no library is added.

| Test                            | Level          | Proves                                                    | Given                                                                                                                              | Then                                                                                                             | Must not                                      |
| ------------------------------- | -------------- | --------------------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------- | ---------------------------------------------------------------------------------------------------------------- | --------------------------------------------- |
| `TestStore_WriteThenRead`       | contract suite | store.1, store.3, store.6, `ReadLibrary.2`, AC 1          | `voices/lorentz/` and `voices/syrus/` with `voice.json`, two `.flac` files each named by `ClipIDOf`, and `events.jsonl` from the schema's examples | Two voices in slug order with their stored names, each clip with its ref, status, source, seconds and added time | Return a clip whose file is missing, or a file with no `added` event |
| `TestStore_NameRule`            | unit           | `ParseName.1`, `ParseName.2`, AC 2                        | `Lorentz`, `Jean-Luc P.`, `O’Brien`, `Åsa 2`, a 39-rune name; then `""`, `2pac`, a 40-rune name, `a/b`, `.x`, `e` + U+0301, `speaker-3`, `unknown-12`, `Speaker-1`, `UNKNOWN-2` | The first five come back unchanged; the next six are `VoiceNameError{Rule: NameShape}`; the last four `NameReserved` | Trim or rewrite an accepted name          |
| `TestStore_SlugRule`            | unit           | `SlugOf.1`, `SlugOf.2`, F10, AC 2                         | `Lorentz`, `LORENTZ`, `Jean-Luc P.`, `Jean Luc P`, `O’Brien`, `Søren 2`                                                               | `lorentz`, `lorentz`, `jean-luc-p`, `jean-luc-p`, `o-brien`, `søren-2`                                             | Return an empty slug, a leading `.`, or a `/` |
| `TestStore_ClipID`              | unit           | `ClipIDOf.1`, store.3, F10, AC 3                          | Two byte strings and a copy of the first                                                                                           | 12 lower-case hex digits equal to the sha256 prefix; equal for the copy, different for the other                 | Depend on a name, a time or a counter         |
| `TestStore_ClipRef`             | unit           | `ParseClipRef.1`                                          | `lorentz/9f3c2a1b0d4e`; then `lorentz`, `/9f3c2a1b0d4e`, `lorentz/9F3C`, `a/b/c`                                                     | The first round-trips through `String`; the others are `ClipRefError` holding the text                           | Accept an upper-case or short id              |
| `TestStore_NotPartOfTheLibrary` | contract suite | store.1, store.2, store.4, store.5, `ReadLibrary.3`, F10, AC 4 | A valid voice plus: `.lorentz/`, `.1a7d0f23c9b8.flac.tmp`, a file under `voices/`, a directory with no `voice.json`, one whose name has another slug, a `voice.json` with a second field, a blank line, a non-JSON line, a line with an unknown `event`, and a last line with no newline | The read equals the read of the valid voice alone; `Skipped` is 3 for that voice                                 | Fail, or count the unfinished line as an event or as skipped |
| `TestStore_UnionIsMerge`        | contract suite | store.7, `ReadLibrary.7`, F10, AC 5                       | Two copies of one voice that share a prefix of lines and each add their own (an enrolment in one, a confirm and a naming in the other); merged as a‖b, b‖a and a‖b‖a | The three merges read the same: both enrolments held, the confirm and the naming counted once                    | Count a repeated line twice, or depend on line order |
| `TestStore_Fold`                | contract suite | `ReadLibrary.4`, `ReadLibrary.5`, `ReadLibrary.6`, F5, AC 6 | One log: clip X added unconfirmed, named, overruled, confirmed; clip Y added, named, removed, added again; clip Z added then removed; a `named` for a clip never added | X is confirmed with three events after its `added`; Y is held with only its second `added`; Z and the unknown clip are absent | Carry Y's old naming into its new life, or resurrect Z |
| `TestStore_SchemaAndCodecAgree` | contract suite | `EncodeEvent.1`, store.2, store.8, AC 7                   | Every `examples` entry of both schema files; one event of each kind through `EncodeEvent` (`placed` with `landed` true and false), one with a non-UTC time                 | Each example decodes into `Event` or `VoiceRecord` and reads back equal; each encoded line passes the schema, ends in one newline and has `at` ending in `Z` | Emit a field the schema does not declare, or any audio |
| `TestStore_EncodeRefusals`      | unit           | `EncodeEvent.2`                                           | An id of 15 digits, a zero time, kind `"sung"`, a clip id `XYZ`, `added` without status, without source, with seconds 0, `placed` with a nil `Landed`, `named` with no speakers | Each is an `EventError` whose `Field` is `id`, `at`, `event`, `clip`, `status`, `source`, `seconds`, `landed`, `speakers` | Return a line together with the error         |
| `TestStore_ReadMissing`         | contract suite | `ReadLibrary.1`, F3, AC 8                                 | A config directory with no `voices/`                                                                                               | No voice, nil error                                                                                              | Create `voices/`                              |
| `TestStore_ReadUnreadable`      | contract suite | `ReadLibrary.8`, AC 8                                     | `voices` that is a regular file; then a voice whose `events.jsonl` is a directory                                                  | `errors.As` finds a `LibraryReadError` whose `Path` is that entry                                                | Return a partial library with a nil error     |
| `TestStore_ReadWritesNothing`   | contract suite | `ReadLibrary.9`, F3, AC 8                                 | A library of two voices, a listing of every path, size and modification time before the read                                       | The listing is identical after the read                                                                          | Create, touch or remove a file                |
| `TestPackage_ImportsNoAudio`    | unit           | `Package.1`, F9, AC 9                                     | The import lists of every Go file in `internal/audio/voices`, tests included                                                        | None is `internal/audio` or a package under it other than `voices` itself, and the tests add only `voicestest` (phase 3's test support, under `voices`) | Shell out to the go command                   |

## Error management

| Failure                                                    | Expected outcome                                                     | Test                            |
| ---------------------------------------------------------- | -------------------------------------------------------------------- | ------------------------------- |
| A name that does not match the shape pattern               | `*VoiceNameError{Rule: NameShape}`, the message holds the name       | `TestStore_NameRule`            |
| A name that is a transcript label in any case (`speaker-3`, `Speaker-1`) | `*VoiceNameError{Rule: NameReserved}`                                | `TestStore_NameRule`            |
| A clip reference that is not `<slug>/<id>`                 | `*ClipRefError` holding the text                                     | `TestStore_ClipRef`             |
| An event the schema refuses, given to `EncodeEvent`        | `*EventError` naming the field, no line                              | `TestStore_EncodeRefusals`      |
| `voices/`, a `voice.json` or an `events.jsonl` that cannot be read | `*LibraryReadError` naming the path, no partial result       | `TestStore_ReadUnreadable`      |
| A complete line that is not an event, or an unfinished line | Not an error: skipped, the complete ones counted in `Skipped`       | `TestStore_NotPartOfTheLibrary` |
| No `voices/` directory                                     | Not an error: no voices                                              | `TestStore_ReadMissing`         |

## Golden diff

None.

## Removes

None.

## Local facts

None.

## Human required

None.

## Implementation notes

| Run   | Commit      | Command                                                                                                                                                   | Result                                                                                                                                                                                            |
| ----- | ----------- | --------------------------------------------------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| start | 1550dec+wip | `go test ./internal/audio/voices/... -race -count=3 -cover -timeout=30s -p 4`, `go vet`, staticcheck, `gofumpt -l internal/audio/voices`, `dupl -t 80 .`  | package absent (this phase creates it): test, vet, staticcheck and gofumpt report no such directory; dupl 49 groups                                                                                 |
| end   | 1550dec+wip | the same                                                                                                                                                  | ok, 14 phase 1 tests (64 with subtests) pass ×3; coverage 94.1 % package, 96.6 % `store.go`; vet, staticcheck exit 0; gofumpt lists only phase 3's `inspect.go`, `voicestest/runner.go`; dupl 49 |
| end   | 1550dec+wip | the same with `-shuffle=on` (seeds 1795014030323020208, 1795014030326128363)                                                                              | ok, 64 ×3 pass; coverage 94.1 %; vet, staticcheck exit 0; gofumpt lists only phase 3's files; dupl 49                                                                                              |
| fix-start | 1550dec+wip | `make qa`; `go vet ./...`; `dupl -t 80 .` (A02) | `make qa` exit 2: `internal/audio/voices` `panic: test timed out after 30s` (testenv: busy, 29.1 s CPU); `internal/audio` 27.8 s, `pkg/tools` 16.6 s; vet exit 0; dupl 49 groups. Alone, voices ×3 19.9 s; `ReadLibrary` 2.4 ms per 50 lines under `-race` |
| fix-end | 1550dec+wip | `go test ./internal/audio/... ./pkg/tools/... ./internal/tools/... ./internal/menu/... ./pkg/diagnostic/... -race -count=3 -cover -timeout=30s -p 11`; `go vet ./...`; staticcheck and `gofumpt -l` on `internal/audio/...`, `pkg/tools/...`; `dupl -t 80 .`; then `make qa` | all ok ×2 (second `-shuffle=on`); voices 7.8 s, 8.5 s, 92.4 %, 219 tests per count as before; vet, staticcheck, gofumpt clean; dupl 49; `make qa` exit 0, 106 packages ok, voices 10.5 s. Alone, voices ×3 5.9 s |
| fix-start | 1550dec+wip | `env -u OPENAI_API_KEY -u ANTHROPIC_API_KEY -u OPENROUTER_API_KEY go test ./internal/audio/voices/... ./internal/menu/... ./pkg/diagnostic/... -race -count=3 -cover -timeout=30s -p 4`; `go vet`, staticcheck and `gofumpt -l` on `internal/audio/voices/...`, `internal/menu/...`; `dupl -t 80 .` (R1-09) | all ok: voices 6.0 s 92.4 %, voicestest 2.2 s 94.4 %, menu 1.4 s 96.0 %, diagnostic 2.9 s 93.3 %; vet, staticcheck, gofumpt clean; dupl 49 groups |
| fix-end | 1550dec+wip | the same | all ok (load average 34): voices 8.4 s 95.4 %, voicestest 2.6 s 94.4 %, menu 1.6 s 96.0 %, diagnostic 3.1 s 93.3 %; voices 63 tests, 248 with subtests, menu 33 and 166, per count; vet, staticcheck, gofumpt clean; dupl 49 groups, none in voices or menu |
| fix-end | 1550dec+wip | the same with `-shuffle=on` | all ok (load average 21): voices 8.4 s 95.4 %, voicestest 2.4 s 94.4 %, menu 1.5 s 96.0 %, diagnostic 3.0 s 93.3 %; vet, staticcheck, gofumpt clean; dupl 49 groups |

Andon: none.
Surprises:
- gofumpt re-indents the Interface's doc-comment lists (`// - X` to `//   - X`); the declarations stay token-identical.
- `Skipped` counts distinct non-event lines, so a merge that repeats a broken line reads the same (store.7).
- `SlugOf` returns `-` for a text with no letter or digit (never a valid name), so SlugOf.2's "never empty" holds for any input.
- A line whose `at` matches the pattern but is no calendar time (month 13), or is year 1, is skipped as not an event.
- `TestMain` is in `main_test.go` (package `voices_test`), shared with phase 3.
- A02: under `-race`, `json.Unmarshal` cost about 40 µs per event line; the lines `EncodeEvent` writes are now scanned directly and any other text still goes to `json.Unmarshal` (fuzzed against the old decoder: same events, same skips).

## Review findings

None.
