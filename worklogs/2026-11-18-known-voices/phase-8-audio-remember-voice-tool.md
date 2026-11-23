# Phase 8 — audio_remember_voice tool

**Parent:** root · **Class:** contract · [README](README.md)

## Goal

Let a model add one clip of a person it has identified in a recording to the voice library, unconfirmed,
and count a wrong library name against the clip that gave it.

## Interface

A tool schema. Written to `pkg/tools/audio_tool_remember_voice.go`, with its catalog row in
`internal/tools/handler.go` (`builtins()`) and its row in `pkg/tools/README.md`. The schema, the result
line, the typed errors and `AudioRememberVoiceDeps` are the frozen surface.

```go
package tools

import (
	"context"

	"github.com/baalimago/clai/internal/audio/voices"
	pub_models "github.com/baalimago/clai/pkg/text/models"
)

// RememberVoiceDeps is what one audio_remember_voice call works through. The
// tool runs no program and reads no configuration of its own. (F9)
type RememberVoiceDeps struct {
	// Inspector cuts and measures the clip; the root builds it over its
	// ffmpeg runner.
	Inspector voices.Inspector
	// Librarian keeps the library the clip is added to.
	Librarian voices.Librarian
}

// AudioRememberVoiceDeps is injected by the clai runtime, as
// AudioTranscribeEngine is. It returns the dependencies of one call.
//
// - AudioRememberVoiceDeps.1 — It is nil until a runtime sets it. The tool
//   asks it once per call that passed its argument checks and keeps nothing
//   between calls, so a runtime, or a test, that sets another function is
//   obeyed from the next call on. (F9)
var AudioRememberVoiceDeps func(ctx context.Context) (RememberVoiceDeps, error)

// ArgumentProblem says what is wrong with one argument of a call.
type ArgumentProblem string

const (
	// ArgumentMissing: a required argument is absent, or an empty string.
	ArgumentMissing ArgumentProblem = "missing"
	// ArgumentType: the argument is not of its schema type.
	ArgumentType ArgumentProblem = "type"
	// ArgumentRange: start is negative, end is not after start, or one of
	// them is not a finite number.
	ArgumentRange ArgumentProblem = "range"
)

// RememberVoiceArgumentError refuses a call for one argument. Its message
// names the argument and what it must be.
type RememberVoiceArgumentError struct {
	Argument string
	Problem  ArgumentProblem
}

func (e *RememberVoiceArgumentError) Error() string

// RememberVoiceUnavailableError says this runtime cannot remember voices:
// nothing was injected (Err is nil), or the injected function failed (Err
// says why).
type RememberVoiceUnavailableError struct{ Err error }

func (e *RememberVoiceUnavailableError) Error() string
func (e *RememberVoiceUnavailableError) Unwrap() error

// AudioRememberVoiceTool is the audio_remember_voice schema.
type AudioRememberVoiceTool pub_models.Specification

// AudioRememberVoice adds one clip of a person to the voice library.
//
// - Specification.1 — The tool is named audio_remember_voice. Its arguments
//   are file_path (string), start (number), end (number), name (string) and
//   overrules (string); the first four are required. No argument removes or
//   replaces a clip. (F5)
// - Registered.1 — The tool is one row of the built-in catalog that needs
//   ffmpeg, as audio_record is: a registry built where ffmpeg resolves holds
//   it, one built where ffmpeg does not resolve does not, and the catalog
//   names it on every machine.
var AudioRememberVoice = AudioRememberVoiceTool{
	Name:        "audio_remember_voice",
	Description: "Remember a person's voice, so that later transcripts name them: add one clip of that person speaking to the voice library. Use it when you know who a transcript speaker is, because they said their name or the user told you. Give the audio file, the person's name, and start and end in seconds taken from audio_transcribe's json segments: 3 to 10 seconds in which only that person speaks. If the transcript gave that speaker a wrong name from the library, also pass overrules with that segment's voice.clip. The clip stays unconfirmed until the user keeps it. Returns '<name>: clip <id> added (unconfirmed)'.",
	Inputs: &pub_models.InputSchema{
		Type: "object",
		Properties: map[string]pub_models.ParameterObject{
			"file_path": {Type: "string", Description: "Path to the local audio file the clip is cut from: the file that was transcribed."},
			"start":     {Type: "number", Description: "Where the clip starts, in seconds from the start of the file, as a segment's start gives it. Decimals are kept."},
			"end":       {Type: "number", Description: "Where the clip ends, in seconds from the start of the file. It must be after start; the clip must be 3 to 10 seconds long."},
			"name":      {Type: "string", Description: "The person's name, for example 'Christoph'. A name the library already holds adds the clip to that person."},
			"overrules": {Type: "string", Description: "Optional. The clip a wrong library name came from, '<slug>/<id>', copied from the segment's voice.clip. It counts that naming against that clip."},
		},
		Required: []string{"file_path", "start", "end", "name"},
	},
}

// Call runs the tool on context.Background().
//
// - Call.1 — Its result and its error are those of CallWithContext with a
//   background context and the same input.
func (a AudioRememberVoiceTool) Call(input pub_models.Input) (string, error)

// CallWithContext inspects the clip, enrols it unconfirmed and, when
// overrules is given, records the overrule.
//
// - CallWithContext.1 — file_path and name are non-empty strings, start and
//   end are numbers, overrules is a string. A required argument that is
//   absent or an empty string, or any argument of another type, is a
//   *RememberVoiceArgumentError naming it, with ArgumentMissing or
//   ArgumentType. An overrules that is absent or empty is not given.
// - CallWithContext.2 — A start below 0, an end that is not after start, or
//   a start or end that is not finite is a *RememberVoiceArgumentError
//   naming start or end, with ArgumentRange. An end of 0 never means the end
//   of the file.
// - CallWithContext.3 — An overrules that is not a clip reference
//   "<slug>/<id>" is ParseClipRef's *ClipRefError, and a name the name rule
//   refuses is ParseName's *VoiceNameError, both unchanged. Every refusal of
//   clauses 1–3 comes before the dependencies are asked for: nothing is
//   inspected and nothing is written.
// - CallWithContext.4 — With AudioRememberVoiceDeps nil the error is a
//   *RememberVoiceUnavailableError with a nil Err; when the injected
//   function fails, it wraps that error. Nothing is inspected or written.
//   (F9)
// - CallWithContext.5 — The clip is inspected once, by the injected
//   Inspector on ctx: the file is file_path as given, and the Cut runs from
//   start to end, each within a microsecond of the seconds given, so
//   61.2 to 69 is inspected as 61.2 s to 69 s.
// - CallWithContext.6 — An error of the inspection is returned unchanged, so
//   errors.As finds its *ClipQualityError, *CutError, *MissingToolError or
//   *SourceError and errors.Is its ctx.Err(). Nothing is enrolled and
//   nothing is overruled.
// - CallWithContext.7 — An accepted clip is enrolled once: the name as
//   given, the Inspection as the Inspector returned it, status Unconfirmed,
//   source SourceRemember, replacing nothing. After a nil error the library
//   lists the clip under the person of that name, unconfirmed, added by
//   audio_remember_voice. No argument and no later step of the call confirms
//   it: it stays unconfirmed until a Keep review. (F5, F6)
// - CallWithContext.8 — An error of the enrolment that comes with the zero
//   ClipRef is returned unchanged, so errors.As finds its *ClipExistsError,
//   *VoiceFullError, *LibraryReadError or *LibraryWriteError. Nothing is
//   overruled. An enrolment that returns the new clip's reference with a
//   *LibraryWriteError is not such an error: clause 16.
// - CallWithContext.9 — The tool removes and replaces nothing of its own.
//   The one clip that may leave the library during a call is the one Enrol
//   prunes (Enrol.5), and a person whose stored clips are all confirmed
//   keeps every one of them: the call fails with *VoiceFullError. (F5)
// - CallWithContext.10 — With overrules given, after the enrolment the clip
//   it names is reviewed once with Overrule: that clip's overruled count
//   grows by one and its status does not change. Without overrules no clip
//   is reviewed. (F6)
// - CallWithContext.11 — A review that fails after the clip was added does
//   not fail the call: the error is nil, the new clip stays enrolled, and
//   the result is the line of clause 12 followed by one newline and a
//   second line, "overrule of <slug>/<id> failed: <reason>", where
//   <slug>/<id> is the overrules argument and <reason> is the review
//   error's message on one line. A line that starts "overrule of " appears
//   in no other case.
// - CallWithContext.12 — The first line of the result of a call that
//   succeeded is exactly "<name>: clip <id> added (unconfirmed)", where
//   <name> is the name argument as given and <id> is the new clip's id, 12
//   lower-case hex digits. Unless clause 11 or clause 16 adds a line, that
//   line is the whole result: no newline and nothing before or after it,
//   the same with and without overrules. Nothing ever stands before it or
//   changes it. A name holds no ":" (ParseName), so the line
//   reads back in one way. The line is frozen: penelope-known-voices reads
//   it from a run's records to learn which clips the run added.
// - CallWithContext.13 — Every failure is an empty result with the error;
//   the run hands it to the model as a failed tool result. Whether it
//   succeeds or fails, the call writes nothing to the run's streams, asks
//   nothing and reports no progress.
// - CallWithContext.14 — No result and no error carries clip audio. (F8)
// - CallWithContext.15 — The tool itself runs no program, resolves none and
//   opens no connection: everything it does goes through the injected
//   Inspector and Librarian, and pkg/tools imports no package of
//   internal/audio but internal/audio/voices. (F9)
// - CallWithContext.16 — When the enrolment returns the new clip's reference
//   with a *LibraryWriteError (Enrol.9: the clip is added, and the file of
//   the clip pruned for it could not be deleted), the call succeeds: the
//   error is nil, the clip stays enrolled, an overrules is still reviewed
//   (clauses 10, 11), and the result is the line of clause 12, then the line
//   of clause 11 when the overrule failed, then one last line, "warning: "
//   followed by that error's message on one line, which names the leftover
//   file. Lines are separated by one newline, and none follows the last. A
//   line that starts "warning: " appears in no other case. (Enrol.9, F5)
func (a AudioRememberVoiceTool) CallWithContext(ctx context.Context, input pub_models.Input) (string, error)

// Specification advertises the schema above.
func (a AudioRememberVoiceTool) Specification() pub_models.Specification
```

The root (phase 9) sets `AudioRememberVoiceDeps`; this phase does not. Until then the tool is offered
where ffmpeg resolves and answers with `*RememberVoiceUnavailableError`.

Consumes:

- `ParseClipRef`, `ClipRef`, `ClipRefError`, `ParseName`, `VoiceNameError`, `Unconfirmed`,
  `SourceRemember` (phase 1); `ReadLibrary`, `SlugOf`, `ClipIDOf`, `StoredVoice`, `ClipHistory` (phase 1),
  by the tests, to read what a call left in the library
- `NewRanker` (phase 2), for the tests' Librarian
- `Inspector`, `Inspector.Inspect`, `Cut`, `Inspection`, `ClipQualityError`, `CutError`,
  `MissingToolError`, `SourceError` (phase 3); `NewInspector` (phase 3), by the tests
- `internal/audio/voices/voicestest:NewRunner`, `Runner.Serve`, `Runner.Fail`, `Runner.Hang`,
  `Runner.Missing`, `Tone`, `Speech`, `SampleRate` (phase 3), by the tests: the runner under the suite's
  real Inspector (L8.1)
- `Librarian`, `Enroller.Enrol`, `Reviewer.Review`, `Enrolment`, `Overrule`, `Reviewed`,
  `ClipExistsError`, `VoiceFullError`, `ClipNotFoundError`, `LibraryWriteError` (phase 4);
  `NewLibrarian`, `Clock`, `Lister.List`, `ClipSummary`, `Tallier.Tally`, `ClipOutcome` (phase 4), by
  the tests
- `LibraryReadError` (phase 1)
- `pkg/text/models:LLMTool`, `Input`, `Specification`, `InputSchema`, `ParameterObject` (import)
- `internal/tools:Registry` (import): the catalog `builtins()`, loaded by `registerLocalTools` into a
  `NewRegistry()` with an injected lookup
- `pkg/tools:AudioTranscribeTool`, `AudioTranscribeEngine` (import): the injection pattern this tool
  follows, and the tool whose `-af json` segments give `start`, `end` and `voice.clip`
- `internal/runenv:Text`, `With` (import): the run's streams, captured by `TestRememberVoice_SaysNothingElse`
- `internal/utils:GetClaiConfigDir` (import): the directory the tests' Librarian keeps its library under
- `internal/testenv:Main` (import). No test resolves a program, so no `FakeBins` stub is needed; the
  registration row is tested with an injected lookup.

## Acceptance criteria

| #   | Criterion                                                                                                                      | Proves                                                              | Proof                                                                 |
| --- | ------------------------------------------------------------------------------------------------------------------------------ | ------------------------------------------------------------------- | --------------------------------------------------------------------- |
| 1   | The schema is `audio_remember_voice` with `file_path`, `start`, `end`, `name` required and `overrules` optional, and reaches a provider as valid JSON Schema | `Specification.1`                                    | `TestAudioRememberVoice_Specification`, `TestToolSchemasMarshalAsJSONSchema` |
| 2   | A call with a good clip answers exactly `<name>: clip <id> added (unconfirmed)`, and the clip is in the library, unconfirmed, added by `audio_remember_voice` | `CallWithContext.5`, `CallWithContext.7`, `CallWithContext.12`, F5 | `TestRememberVoice_EnrolsUnconfirmed`, `TestRememberVoice_ResultLine` |
| 3   | A learned clip is pruned like any other, the tool never removes a confirmed clip, and a person at the cap (L4.2), all confirmed, refuses it | `CallWithContext.9`, F5, L4.2                            | `TestRememberVoice_PrunedLikeAnyOther`                                |
| 4   | With `overrules`, the clip named gets one overrule and keeps its status; without it nothing is reviewed; the new clip is never confirmed by the tool | `CallWithContext.10`, `CallWithContext.7`, F6          | `TestRememberVoice_Overrules`                                         |
| 5   | A bad argument, a bad clip reference and a refused name are typed errors before anything is inspected or written               | `CallWithContext.1`–`CallWithContext.3`                             | `TestRememberVoice_Arguments`, `TestRememberVoice_RefusedBeforeInspection` |
| 6   | A refused clip and a refused enrolment are each a typed error the caller can tell apart, with the library as the clauses say | `CallWithContext.6`, `CallWithContext.8` | `TestRememberVoice_InspectionRefusals`, `TestRememberVoice_EnrolRefusals` |
| 7   | A failure is an empty result and an error, and no call writes to the run's streams                                             | `CallWithContext.13`                                                | `TestRememberVoice_SaysNothingElse`                                   |
| 8   | No result and no error carries clip audio                                                                                      | `CallWithContext.14`, F8                                            | `TestRememberVoice_NoAudioLeaves`                                     |
| 9   | The tool works only through what is injected, is unavailable without it, and `pkg/tools` imports nothing of `internal/audio` but `voices` | `CallWithContext.4`, `CallWithContext.15`, `AudioRememberVoiceDeps.1`, `Call.1`, F9 | `TestRememberVoice_Unavailable`, `TestRememberVoice_DepsPerCall`, `TestRememberVoice_RunsNothingItself`, `TestRememberVoice_Call` |
| 10  | The tool is registered when ffmpeg resolves and only then, is in the catalog on every machine, and is in the package's README  | `Registered.1`                                                      | `TestRegisterLocalTools_AudioRememberVoiceGate`, `TestBuiltinCatalogHoldsEveryShippedTool`, `TestREADMECatalogIsComplete` |
| 11  | An overrule that fails after the clip was added leaves the call a success: the frozen line, then `overrule of <slug>/<id> failed: <reason>`, with the clip enrolled | `CallWithContext.11`, `CallWithContext.12` | `TestRememberVoice_OverruleFails` |
| 12  | A clip added while the pruned clip's file could not be deleted leaves the call a success: the frozen line first, a last line `warning: …` naming the leftover file, and the overrule still made | `CallWithContext.16`, `CallWithContext.8`, `CallWithContext.12`, `Enrol.9` | `TestRememberVoice_PruneLeavesAFile` |

## Test cases

Every row calls the tool through its schema: `AudioRememberVoice.CallWithContext(ctx, input)` with a
`pub_models.Input` whose numbers are `float64`, as a decoded tool call carries them. The dependencies
are those of L8.1: the real `voices.NewInspector(voicestest.NewRunner())` and the real Librarian over a
library under the `testenv` home, set through `AudioRememberVoiceDeps` and restored by the test's
cleanup. Unless a row says otherwise, `meeting.wav` is `Serve`d as `Tone(120, -20)`, 120 s of
speech-level tone, and a good call is `start` 61.2, `end` 69 on it; rows that need several clips cut
them at different starts of a source served as `Speech` pieces joined, so each has its own id. "The
library" in a Then is what `List` and `ReadLibrary` show afterwards.

| Test                                            | Level          | Proves                                                    | Given                                                                                                                                             | Then                                                                                                                                                 | Must not                                                  |
| ----------------------------------------------- | -------------- | --------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------- | --------------------------------------------------------- |
| `TestAudioRememberVoice_Specification`          | contract suite | `Specification.1`, AC 1                                   | `AudioRememberVoice.Specification()`                                                                                                              | Name `audio_remember_voice`; properties exactly `file_path` string, `start` number, `end` number, `name` string, `overrules` string; required exactly the first four | Declare an argument that removes, replaces or confirms a clip |
| `TestToolSchemasMarshalAsJSONSchema` (existing) | contract suite | `Specification.1`, AC 1                                   | `exportedTools()` with `AudioRememberVoice` added                                                                                                 | Its input schema encodes with `type` `object`, and `required` and `properties` that are not null                                                     | Leave the tool out of `exportedTools()`                   |
| `TestRememberVoice_Arguments`                   | contract suite | `CallWithContext.1`, `CallWithContext.2`, AC 5            | Calls without `file_path`, with `name` `""`, without `end`, with `start` `"61.2"`, `file_path` 7, `overrules` 3; then `start` -1, `start` 8 `end` 8, `start` 9 `end` 4, `start` 5 `end` 0, `end` +Inf; then a good call with `overrules` `""` | `RememberVoiceArgumentError` with `file_path`/`missing`, `name`/`missing`, `end`/`missing`, `start`/`type`, `file_path`/`type`, `overrules`/`type`; then `range` naming `start`, `end`, `end`, `end`, `end`; the last call succeeds and reviews nothing | Take `end` 0 as the end of the file, or call the Inspector |
| `TestRememberVoice_RefusedBeforeInspection`     | contract suite | `CallWithContext.3`, AC 5                                 | `overrules` `lorentz`, `lorentz/9F3C`, `a/b/c`; then `name` `speaker-2` and `2pac`; a deps function that counts its calls                          | `ClipRefError` holding the text, three times; `VoiceNameError` twice; the deps function was never called and the library is empty                    | Inspect, enrol, or create `voices/`                       |
| `TestRememberVoice_Unavailable`                 | contract suite | `CallWithContext.4`, F9, AC 9                             | A good call with `AudioRememberVoiceDeps` nil; then with a function that returns an error                                                         | `RememberVoiceUnavailableError` with a nil `Err`; then one whose `errors.Is` finds that error; no `voices/` exists                                    | Panic on the nil function, or fall back to a default library |
| `TestRememberVoice_EnrolsUnconfirmed`           | contract suite | `CallWithContext.5`, `CallWithContext.7`, `CallWithContext.12`, F5, AC 2 | `file_path` `meeting.wav`, `start` 61.2, `end` 69, `name` `Christoph`, an empty library, and the Inspector wrapped so that its calls are recorded and passed on; then a call with `start` 20, `end` 25 and `name` `CHRISTOPH` | `Inspect` was called once per call, with `meeting.wav` and a Cut within 1 µs of 61.2 s and 69 s; the result is `Christoph: clip <id> added (unconfirmed)`; the library holds that clip under `christoph`, status `unconfirmed`, source `audio_remember_voice`, 7.8 s long, and `<id>` is `ClipIDOf` of its file; the second call adds a 5 s clip to the same person and answers `CHRISTOPH: clip <id> added (unconfirmed)` | Enrol a confirmed clip, round a bound to whole seconds, or inspect twice |
| `TestRememberVoice_ResultLine`                  | contract suite | `CallWithContext.12`, AC 2                                | Good calls for `Christoph`, `Jean-Luc P.` and `O’Brien`, each with and without `overrules`                                                        | Each result matches `^(.+): clip ([0-9a-f]{12}) added \(unconfirmed\)$` once, group 1 is the name as given, group 2 the new clip's id; it holds no newline; with `overrules` naming a held clip it is byte for byte the line without | Append anything to the line when the overrule succeeded   |
| `TestRememberVoice_InspectionRefusals`          | contract suite | `CallWithContext.6`, AC 6                                 | On `meeting.wav`: `start` 61.2 `end` 62.7; `start` 150 `end` 155; a good cut after `Missing("ffmpeg")`. On `notes.txt` after `Fail("notes.txt", "Invalid data found when processing input")`: a good cut. On a file given to `Hang`, with a ctx cancelled during the call. Each call with `overrules` naming a held clip | `errors.As` finds `ClipQualityError` (message `clip too short: 1.5 s, a clip is 3–10 s`), `CutError`, `MissingToolError{Tool: "ffmpeg"}`, `SourceError` holding `notes.txt`; `errors.Is` finds `context.Canceled`; the library and the held clip's overruled count are as before | Wrap the error so that its type is lost, enrol, or overrule |
| `TestRememberVoice_EnrolRefusals`               | contract suite | `CallWithContext.8`, L4.2, AC 6                           | The same cut remembered twice for one person; a person at the cap (L4.2), all confirmed; `voices` that is a regular file; a voice directory made read-only; each call with `overrules` naming a held clip where the library is readable | `ClipExistsError`, `VoiceFullError`, `LibraryReadError`, `LibraryWriteError`, each found by `errors.As`; the held clip's overruled count is as before | Overrule after a refused enrolment                        |
| `TestRememberVoice_PrunedLikeAnyOther`          | contract suite | `CallWithContext.9`, F5, L4.2, AC 3                       | A person at the cap (L4.2) with two unconfirmed clips and the rest confirmed, with tallied histories, then a call; a person at the cap, all confirmed, then a call | The first call succeeds: the person is still at the cap, every confirmed clip among the clips held, and the one gone is the last unconfirmed clip of `NewRanker().Rank` over the clips held before the call; the second is `VoiceFullError` and every file and line of that voice is byte for byte as before | Remove a confirmed clip, remove more than one clip, or call `Remove` |
| `TestRememberVoice_Overrules`                   | contract suite | `CallWithContext.10`, `CallWithContext.7`, F6, AC 4       | Lorentz holds a confirmed clip `e81d…` with four namings; a call for `Ada` with `overrules` `lorentz/e81d5530c6a9`; then a call for `Ada` with another clip and no `overrules` | After the first: `e81d…` has `Overruled` 1, `Named` 4 and status `confirmed`, and one `overruled` line was appended; Ada's new clip is `unconfirmed` with no `confirmed` event. After the second: no further `overruled` line anywhere | Review with `Keep`, overrule the new clip, or overrule twice |
| `TestRememberVoice_OverruleFails`               | contract suite | `CallWithContext.11`, `CallWithContext.12`, AC 11         | A call for `Ada` with `overrules` `lorentz/ffffffffffff`, a clip the library does not hold; then one naming a held clip of a voice whose `events.jsonl` is read-only | Both return a nil error and exactly two lines: the first matches the regex of `TestRememberVoice_ResultLine` with Ada's new clip id, the second is `overrule of lorentz/ffffffffffff failed: ` followed by the message of the `ClipNotFoundError`, then of the `LibraryWriteError`, that `Review` returns for that ref; Ada's clips are in the library, `unconfirmed`; no `overruled` line was appended | Return an error, remove the new clip, change the first line, or spread the reason over several lines |
| `TestRememberVoice_PruneLeavesAFile`            | contract suite | `CallWithContext.16`, `CallWithContext.8`, `CallWithContext.12`, AC 12 | The real Librarian wrapped so that `Enrol` enrols and then returns the reference it got with a `*LibraryWriteError` naming a file under the person's directory. In turn: a good call for `Ada`; one with `overrules` naming a held clip; one with `overrules` `lorentz/ffffffffffff`; then the wrapper returning the zero reference with that error | A nil error and, in turn: two lines, the first matching the regex of `TestRememberVoice_ResultLine` with the new clip's id, the second `warning: ` and the error's message with the file's path; the same two lines, and the held clip's `Overruled` grew by one; three lines, the frozen line, the `overrule of lorentz/ffffffffffff failed: …` line, then the `warning: ` line; each result ends without a newline and Ada's clips are in the library, `unconfirmed`. With the zero reference: an empty result and the `LibraryWriteError`, nothing overruled | Fail a call whose clip was added, change or precede the first line, skip the overrule, or put the warning before the overrule line |
| `TestRememberVoice_SaysNothingElse`             | contract suite | `CallWithContext.13`, AC 7                                | A ctx carrying `runenv.Text` over three buffers; one good call, one call of each failing row above                                                 | Each failure returns `""` and a non-nil error; the stdout and stderr buffers are empty after every call and nothing was read from stdin              | Print a notice, a progress line or the error itself       |
| `TestRememberVoice_NoAudioLeaves`               | contract suite | `CallWithContext.14`, F8, AC 8                            | The `FLAC` the Inspector returns for the good cut, got by inspecting it directly; the result of a good call, of a call whose overrule failed, and the text of every error of the rows above | No result and no error text holds a 16-byte run of that `FLAC`, raw, hex or base64                                                                   | Put clip bytes, or an encoding of them, in a result or error |
| `TestRememberVoice_RunsNothingItself`           | unit           | `CallWithContext.15`, F9, AC 9                            | `PATH` set to an empty directory, then a good call; the import lists of every Go file in `pkg/tools`, tests included                                 | The call succeeds; no non-test import is `internal/audio` or a package under it other than `internal/audio/voices`, and the tests add only `voicestest` | Look up ffmpeg, or shell out to the go command            |
| `TestRememberVoice_DepsPerCall`                 | unit           | `AudioRememberVoiceDeps.1`, AC 9                          | Two libraries under two directories; a good call with the deps function of the first, then the function swapped for the second's and a good call; a function that counts its calls | Each clip is in its own library only; the function was called once per call                                                                         | Keep the first call's Librarian                           |
| `TestRememberVoice_Call`                        | unit           | `Call.1`, AC 9                                            | `AudioRememberVoice.Call(input)` with a good input, then with `start` -1; a deps function that records its ctx                                      | The success line and a `RememberVoiceArgumentError`, as `CallWithContext` gives them; the recorded ctx has no deadline and is not cancelled          | Differ from `CallWithContext`                             |
| `TestRegisterLocalTools_AudioRememberVoiceGate` | unit           | `Registered.1`, AC 10                                     | `registerLocalTools` into a `NewRegistry()` with a lookup that resolves only `ffmpeg`; then one that resolves nothing                              | The first registry holds `audio_remember_voice`; the second does not                                                                                 | Register the tool without ffmpeg, or resolve through `PATH` |
| `TestBuiltinCatalogHoldsEveryShippedTool` (existing) | unit      | `Registered.1`, AC 10                                     | The literal list of visible catalog names, with `audio_remember_voice` added                                                                      | The catalog's visible names equal the list                                                                                                           | Hide the tool                                             |
| `TestREADMECatalogIsComplete` (existing)        | unit           | `Registered.1`, AC 10                                     | `pkg/tools/README.md` with the row `audio_remember_voice` / `AudioRememberVoice`                                                                   | Every exported tool has its row and every row its tool                                                                                               | Leave the tool out of the README                          |

## Error management

Every row but the last two is an empty result with the error; the run renders it to the model as a failed
tool result and shows nothing else (`CallWithContext.13`).

| Failure                                                              | Expected outcome                                                                                         | Test                                         |
| -------------------------------------------------------------------- | -------------------------------------------------------------------------------------------------------- | -------------------------------------------- |
| A required argument absent or an empty string                        | `*RememberVoiceArgumentError` naming it, `ArgumentMissing`; nothing inspected or written                 | `TestRememberVoice_Arguments`                |
| An argument of another type                                          | `*RememberVoiceArgumentError` naming it, `ArgumentType`                                                  | `TestRememberVoice_Arguments`                |
| `start` negative, `end` not after `start`, or either not finite      | `*RememberVoiceArgumentError` naming `start` or `end`, `ArgumentRange`                                   | `TestRememberVoice_Arguments`                |
| `overrules` that is not `<slug>/<id>`                                | `*ClipRefError` holding the text; nothing inspected or written                                           | `TestRememberVoice_RefusedBeforeInspection`  |
| A name the name rule refuses                                         | `*VoiceNameError` with its rule; nothing inspected or written                                            | `TestRememberVoice_RefusedBeforeInspection`  |
| Nothing injected, or the injected function fails                     | `*RememberVoiceUnavailableError`, wrapping the function's error when there is one                        | `TestRememberVoice_Unavailable`              |
| The clip fails an inspection check                                   | `*ClipQualityError` unchanged, with its message (`clip too short: 1.5 s, a clip is 3–10 s`); nothing enrolled or overruled | `TestRememberVoice_InspectionRefusals` |
| `start` and `end` select nothing from the file                       | `*CutError` unchanged                                                                                    | `TestRememberVoice_InspectionRefusals`       |
| ffmpeg or ffprobe does not resolve                                   | `*MissingToolError` unchanged                                                                            | `TestRememberVoice_InspectionRefusals`       |
| The file is missing or is not audio                                  | `*SourceError` unchanged                                                                                 | `TestRememberVoice_InspectionRefusals`       |
| The context is cancelled                                             | An error matching `ctx.Err()`                                                                            | `TestRememberVoice_InspectionRefusals`       |
| The person already holds this clip                                   | `*ClipExistsError` unchanged; nothing overruled                                                          | `TestRememberVoice_EnrolRefusals`            |
| The person's stored clips are all confirmed, at the cap              | `*VoiceFullError` unchanged; no clip removed (F5)                                                        | `TestRememberVoice_EnrolRefusals`, `TestRememberVoice_PrunedLikeAnyOther` |
| The library cannot be read or written                                | `*LibraryReadError` or `*LibraryWriteError` unchanged, naming the path                                   | `TestRememberVoice_EnrolRefusals`            |
| The overrule fails after the clip was added (the clip named is not held, or the append fails) | Not an error: the frozen line, then `overrule of <slug>/<id> failed: <reason>`; the new clip stays, unconfirmed | `TestRememberVoice_OverruleFails` |
| The clip was added and the pruned clip's file could not be deleted (`Enrol.9`) | Not an error: the frozen line, the overrule line when the overrule failed, then `warning: <the *LibraryWriteError's message>`; the new clip stays, unconfirmed | `TestRememberVoice_PruneLeavesAFile` |

## Golden diff

None.

## Removes

None.

## Local facts

**L8.1 — Test seam.** No dependency is faked by this phase. The suite's Inspector is the real
`voices.NewInspector(voicestest.NewRunner())`, over phase 3's exported runner double, scripted with
`Serve`, `Fail`, `Hang` and `Missing` and fed with `Tone` and `Speech`; the Librarian is the real
`NewLibrarian(dir, NewRanker(), clock)` over a directory under the `testenv` home, with a test clock
that steps one second per call. What a call inspects, enrols, prunes and overrules is therefore read
back from the library, and every refusal is the consumed member's own error. A row may wrap the
Inspector to record its calls and pass them on; it changes no answer. One row,
`TestRememberVoice_PruneLeavesAFile`, wraps the Librarian so that its `Enrol` enrols for real and then
returns its reference with a `*LibraryWriteError`: a deletion that fails cannot be forced through the
tool's schema, and phase 4 proves the Librarian's side in `TestEnrol_PruneLeavesAFile`. (Phase-authored;
it is signed with this phase's interface.)

## Human required

None.

## Implementation notes

| Run   | Commit      | Command | Result                |
| ----- | ----------- | ------- | --------------------- |
| start | 1550dec+wip | `go test ./pkg/tools/... ./internal/tools/... ./pkg/diagnostic/... -race -count=3 -cover -timeout=30s -p 4` + vet, staticcheck, gofumpt -l, dupl -t 80 | 12 packages ok, 1012 tests per count, 0 failed; vet, staticcheck, gofumpt clean; dupl 49 groups |
| end   | 1550dec+wip | `go test ./pkg/tools/... ./internal/tools/... ./pkg/diagnostic/... -race -count=3 -cover -timeout=30s -p 4` + vet, staticcheck, gofumpt -l, dupl -t 80 | 12 packages ok, 0 failed; pkg/tools 21.7 s, so the suite was made cheaper (Surprises) and the gates run again |
| end   | 1550dec+wip | `go test ./pkg/tools/... ./internal/tools/... ./pkg/diagnostic/... -race -count=3 -cover -timeout=30s -p 4 -shuffle=on` + vet, staticcheck, gofumpt -l, dupl -t 80 | 12 packages ok, 0 failed; pkg/tools 20.5 s |
| end   | 1550dec+wip | `go test ./pkg/tools/... ./internal/tools/... ./pkg/diagnostic/... -race -count=3 -cover -timeout=30s -p 4` + vet, staticcheck, gofumpt -l, dupl -t 80 | 12 packages ok, 1123 tests per count (3369 passes), 0 failed; pkg/tools 16.2 s, 92.1 % (new files 100 %); vet, staticcheck, gofumpt clean; dupl 49 groups, none in the new files |
| end   | 1550dec+wip | `go test ./pkg/tools/... ./internal/tools/... ./pkg/diagnostic/... -race -count=3 -cover -timeout=30s -p 4 -shuffle=on` + vet, staticcheck, gofumpt -l, dupl -t 80 (seed 1795017848613832788) | 12 packages ok, 1123 tests per count (3369 passes), 0 failed; pkg/tools 16.2 s; vet, staticcheck, gofumpt clean; dupl 49 groups |
| fix-start | 1550dec+wip | `make qa`; `go vet ./...`; `dupl -t 80 .` (A02) | `make qa` exit 2 (A02, `internal/audio/voices`); `pkg/tools` ok 16.6 s; vet exit 0; dupl 49 groups. Alone, pkg/tools ×3 15.3 s, 10.2 s user CPU (7.8 s and 0.6 s without this phase's files) |
| fix-end | 1550dec+wip | `go test ./internal/audio/... ./pkg/tools/... ./internal/tools/... ./internal/menu/... ./pkg/diagnostic/... -race -count=3 -cover -timeout=30s -p 11`; `go vet ./...`; staticcheck and `gofumpt -l` on `internal/audio/...`, `pkg/tools/...`; `dupl -t 80 .`; then `make qa` | all ok ×2 (second `-shuffle=on`); pkg/tools 15.5 s, 14.6 s, 92.3 %, 496 tests per count as before; vet, staticcheck, gofumpt clean; dupl 49; `make qa` exit 0, pkg/tools 14.4 s. Alone, pkg/tools ×3 14.0 s |
| fix-start | 1550dec+wip | `env -u OPENAI_API_KEY -u ANTHROPIC_API_KEY -u OPENROUTER_API_KEY go test ./internal/audio/ ./pkg/tools/... -race -count=3 -cover -timeout=30s -p 4`; `go vet ./internal/audio/... ./pkg/tools/...`; staticcheck and `gofumpt -l` on `internal/audio`, `pkg/tools` | 2 packages ok (`internal/audio` 17.6 s, 92.4 %; `pkg/tools` 13.0 s, 92.3 %); vet, staticcheck, gofumpt clean |
| fix-end   | 1550dec+wip | the same | 2 packages ok (`internal/audio` 18.0 s, 92.4 %; `pkg/tools` 13.9 s, 92.3 %); vet, staticcheck, gofumpt clean (R1-14) |
| fix-end   | 1550dec+wip | the same, `go test … -shuffle=on` | 2 packages ok (`internal/audio` 18.1 s; `pkg/tools` 13.4 s); vet, staticcheck, gofumpt clean |

Andon: none.
Surprises:
- gofumpt reindents the Interface's doc-comment bullets (`//   - X.1 —`); only comments move, the tokens are identical (AST print with bodies stripped).
- The method bodies delegate to `pkg/tools/audio_tool_remember_voice_call.go`, so the interface file's imports stay those of the Interface block.
- Under `-race` each library regenerated the 120 s tone, taking pkg/tools from 7.9 s to 21.7 s; each test now makes it once and serves it when the deps function is asked (16.2 s).
- Beyond the clauses: deps with a nil Inspector or Librarian is a `*RememberVoiceUnavailableError` whose Err names the member; null is absent; Go `int`/`int64` bounds are numbers; an end within 1 ns of start is `ArgumentRange`; a bound past `time.Duration`'s range is held at its maximum, so the Inspector answers `*CutError`.
- The tests also wrap the Librarian (recording `Review` and `Remove`, passing them on), beside L8.1's Inspector wrapper, to prove "never Keep" and "never Remove".
- A02: each env still clones the 120 s meeting when it is served (4.4 ms under `-race`), the largest phase-8 cost left in pkg/tools.
- R1-14: the call fails only on the zero reference beside an enrolment error (`CallWithContext.8`). A reference beside an error is the added clip: the overrule still runs, and the result gains a last line, `warning: ` and the message. The interface comments for `.8`, `.11`, `.12` and the new `.16` now carry the signed text.
- R1-14: `TestRememberVoice_PruneLeavesAFile` wraps the real Librarian in `rememberPruneLeaves` (L8.1). Its zero-reference call also names a held clip in `overrules`, to prove that a failed enrolment overrules nothing.

## Review findings

### Review 2, 2026-11-19

Root review round 2 at `1550dec+wip`. No finding.

- **R1-14 (tool) resolved.** `rememberVoice` (`pkg/tools/audio_tool_remember_voice_call.go:33-62`) fails only on the zero ref; with a ref beside a `*LibraryWriteError` it answers the frozen line, still overrules, and ends with `warning: <message>` on one line (`CallWithContext.8`, `.11`, `.12`, `.16`). `TestRememberVoice_PruneLeavesAFile` passes.
- **note** The mock chat model has no arguments for `audio_remember_voice`, so the tool was not driven on the built binary; its contract suite is the evidence.
