# Phase 9 — Known voices in transcription

**Parent:** root · **Class:** composition · [README](README.md)

## Goal

Make `audio_transcribe` and `clai audio transcribe` name the speakers the library knows, by wiring the
Ranker, the Inspector, the Librarian and the Namer into the transcribe route, the voices CLI and the
`audio_remember_voice` tool, while a run with an empty selection stays exactly as it is today (F3).

## Interface

The root's interface is four things: the `audio_transcribe` tool schema, the `-voices` flag, the
`transcribe.max-voices` key, and the output of a transcription. The tool schema is written to
`pkg/tools/audio_tool_transcribe.go`; the flag to `internal/flags.go` (`MediaToolFlags`) and
`internal/audio/cmd.go` (`Flags`); the key to `internal/audio/querier.go` (`TranscribeConfig`); the
output to `internal/audio/audio.go`; the run's ffmpeg check, its error and its warnings to
`internal/audio/known_voices.go`; the interrupt at a menu question to `internal/cli/compose.go`.
`architecture/audio.md`, `architecture/output.md` (the interrupt of Wire.6), the audio namespace help and
`pkg/tools/README.md` are edited in this phase to say the same. The engine's new signature is an API
change to `pkg/tools`; the phase says so in its Implementation notes when it is done. Four existing test
call sites use the old signature and are adapted to `AudioTranscribeCall` in this phase, with their
assertions unchanged: `pkg/tools/audio_tool_transcribe_test.go:12-15` (`withFakeEngine`),
`internal/audio/calibration_integration_test.go:294`, `internal/audio/record_interrupt_test.go:105` and
`internal/audio/tool_overrides_test.go:84`.

### Tool schema: `audio_transcribe`

```go
package tools

// AudioTranscribeCall is one audio_transcribe call, as the engine gets it.
type AudioTranscribeCall struct {
	FilePath     string
	OutputFormat string
	// Voices is the call's voices argument; empty when the model gave none.
	Voices []string
}

// AudioTranscribeEngine is injected by the clai runtime, as today; it now
// takes the call, as AudioRecordEngine does.
var AudioTranscribeEngine func(ctx context.Context, call AudioTranscribeCall) (string, error)

// AudioTranscribe is the audio_transcribe schema.
//
// - Tool.1 — The arguments are file_path (string, required), output_format
//   (string, one of text, vtt, srt, json; default text) and voices (array of
//   strings, optional). file_path and output_format mean what they mean
//   today, and a call without voices is checked and answered as today. (F3)
// - Tool.2 — voices are the people the model expects in the recording. A
//   call that gives at least one name is named after those people and
//   nobody else, whatever -voices says; a call that gives none, or an empty
//   array, follows -voices and then the library (Flag.3). (F11)
// - Tool.3 — A voices that is not an array of strings fails the call with
//   "voices must be an array of strings, the names of the people expected
//   in the recording". The call is refused when its arguments are checked,
//   before anything is transcribed, so no transcription is lost to it; F4
//   governs naming once the recording is transcribed. A name the library
//   does not hold never fails the call. (F11, F4)
var AudioTranscribe = AudioTranscribeTool{
	Name:        "audio_transcribe",
	Description: "Transcribe a local audio file to text. Returns the transcript; timestamps and speaker labels are included for output formats that carry them. When a long file is split, speaker labels carry the chunk number (1-A, 2-A), and labels from different chunks are not known to be the same person. Speakers whose voice is in the user's voice library are named instead of labelled; pass voices to say whom to expect. In text, vtt and srt a name that comes from a clip the user has not confirmed is written '<name> (unconfirmed)'. With output_format json and known voices the result is {\"segments\": [...], \"naming\": {...}}: a named segment carries voice {clip, status}, and naming says who was named and why anyone was not. JSON is pretty-printed with one segment object per indented block for jq and other line-oriented inspection. Default output_format is 'text'.",
	Inputs: &pub_models.InputSchema{
		Type: "object",
		Properties: map[string]pub_models.ParameterObject{
			"file_path": {
				Type:        "string",
				Description: "Path to the local audio file to transcribe.",
			},
			"output_format": {
				Type:        "string",
				Description: "Transcript output format. JSON is newline-formatted for jq parsing. Default is 'text'.",
				Enum:        &audioTranscribeFormats,
			},
			"voices": {
				Type:        "array",
				Description: "Optional. The names of the people expected in the recording, as the voice library holds them, for example [\"Lorentz\", \"Christoph\"]. Only they are named. Leave it out to name everyone the library knows.",
				Items:       &pub_models.ParameterObject{Type: "string"},
			},
		},
		Required: []string{"file_path"},
	},
}
```

### CLI: `-voices`

```text
clai audio transcribe [-am <model>] [-af vtt|srt|text|json] [-voices <names>] [other flags] <file>
clai [-am <model>] [-af <format>] [-voices <names>] [other flags] query <prompt>
clai [-am <model>] [-af <format>] [-voices <names>] [other flags] chat …
clai audio v|voices a|add … | l|list …                    (phase 7; amended 2026-11-19, A04)

  -voices <names>   The people to name, comma separated: -voices 'Lorentz,Christoph'.
                    Default: everyone in the voice library with a confirmed clip,
                    up to transcribe.max-voices; nobody when that is 0.

Flags stand before <file>: `clai audio transcribe` does not read a flag after it.
<file> is the one argument of `clai audio transcribe`, or `-` for stdin; it has no sub-verb.

Exit status and streams of every command are as today, but for what the clauses below
state: the warnings of Output.5 on stderr, the stop of Run.8, the hints of Wire.5, the
interrupt of Wire.6 and the help of Wire.7. (amended 2026-11-19, A04)
```

- Flag.1 — `<names>` is split at commas; blanks around a name are dropped, and so is an empty name. A
  name is matched by `Select` (name or slug, without case); it is not checked when the flag is read, and
  a name the library does not hold never fails the run. What can stop a run that names people is
  ffmpeg, not a name: Run.8. (F11, F4)
- Flag.2 — On `clai audio transcribe`, `-voices` with at least one name names after those people and
  nobody else. (F11)
- Flag.3 — On `query` and `chat`, `-voices` is the run's answer for every `audio_transcribe` call that
  gives no `voices` of its own; a call that gives one is named after the call's people only. `-voices`
  travels with `-am` and `-af`: through `internal:MediaToolFlags`, `MediaToolSettings` and
  `audio:ToolOverrides`. (F11)
- Flag.4 — Without `-voices`, with `-voices ''`, and with a tool call that gives no name, the people are
  the library's default: every person with a confirmed clip, in rank order of their best clip, up to
  `transcribe.max-voices`, and nobody when that is 0. (F11)
- Flag.5 — `-h` on `audio` names `-voices` and the `voices` verb with its short name `v`; `-h` on
  `audio transcribe` names `-voices` and no sub-verb. `-voices` is a flag of `transcribe` alone: the
  voices CLI, `clai audio voices …`, does not define it (Wire.4). (amended 2026-11-19, A04: `voices`
  was a sub-verb of `transcribe`, whose flags were left unread before it)

### Configuration: `transcribe.max-voices`

```json
{ "transcribe": { "max-voices": 12 } }
```

- Config.1 — `transcribe.max-voices` in `audioConfig.json` caps the people of the default selection.
  The default is 12, and a file without the key means 12. 0 turns the default selection off: only
  people named by `-voices` or by a tool call are named, and a run that names nobody has an empty
  selection (F3). A negative value fails the load with an error that names `transcribe.max-voices`. It
  never caps the people named by `-voices` or by a tool call. A file written before the key existed
  gets it added as 12, with the one-time notice on stderr that every new key gets; stdout is untouched.
  (F11, F3, max-voices)

### Output of a transcription

```json
{
  "segments": [
    {
      "start": 0,
      "end": 8.2,
      "speaker": "Lorentz",
      "voice": { "clip": "lorentz/9f3c2a1b0d4e", "status": "confirmed" },
      "text": "…"
    },
    { "start": 9, "end": 15.5, "speaker": "B", "text": "…" }
  ],
  "naming": {
    "status": "named",
    "origin": "flag",
    "speakers": [
      { "label": "A", "name": "Lorentz", "voice": { "clip": "lorentz/9f3c2a1b0d4e", "status": "confirmed" } },
      { "label": "B", "reason": "no-match" }
    ],
    "voices": [
      { "name": "Lorentz", "result": "named" },
      { "name": "Ada", "result": "unknown-voice" }
    ]
  }
}
```

- Output.1 — With a non-empty selection and the json format, the output is one object with exactly the
  keys `segments` and `naming`, in that order, indented as today's json and ended by one newline.
  `naming` is the Namer's report as `NamingReport.MarshalJSON` encodes it. (F6)
- Output.2 — A segment is today's object, `start`, `end`, `speaker` (left out when empty) and `text`,
  with `voice` between `speaker` and `text` exactly when a clip named it: `speaker` is then the person's
  plain name and `voice` is `{clip, status}`, the clip's reference and its status, `unconfirmed`
  included. A segment no clip named has no `voice` key and the speaker it was transcribed with. (F6)
- Output.3 — With a non-empty selection and the vtt, srt or text format, the output is today's rendering
  of the named segments, with the name where today's label stands: the plain name when a confirmed clip
  named the segment, and `<name> (unconfirmed)` when an unconfirmed clip did, so `Syrus (unconfirmed):
  …` in text and srt and `<v Syrus (unconfirmed)>…</v>` in vtt. Nothing else is added to stdout: the
  report and the clip's reference are in the json format only, where the speaker is always the plain
  name. (Glossary Unconfirmed, F6)
- Output.4 — With an empty selection, every format's output is byte for byte what it is today; json is
  the bare array of segments, with no `naming` and no `voice`. (F3)
- Output.5 — On `clai audio transcribe`, a run with a non-empty selection says on stderr what the
  naming did not do. Each case of L9.1 is a warning of its own, with that case's message and nothing of
  another case in it; two cases are never folded into one message. Each is one warning of the run,
  reported as the run reports its warnings (`runenv.Warnf`, subsystem `audio`): in text a line
  `warn [audio] <message>` on stderr. They come in L9.1's order, people in the report's order. Stdout
  is byte for byte what Output.1–Output.3 make it, in all four formats, with and without warnings. It
  is a warning only: nothing is asked, stdin is not read, and the run does not offer to label a speaker
  or to add a voice. With an empty selection there is no warning (F3). The `audio_transcribe` tool
  writes none of them: its result is its whole answer. (F4, L9.1)

### The run

These bind both routes, `clai audio transcribe` and the `audio_transcribe` tool.

- Run.1 — The people are resolved once per transcription, by `Select` with the tool call's `voices`,
  the run's `-voices` and `transcribe.max-voices`, before the recording is transcribed, unless Run.8
  stops the run first. (F11)
- Run.2 — With an empty selection the run makes exactly the transcription requests it makes today and
  returns byte-identical output: no naming request is sent, no program is run that is not run today, and
  nothing under `voices/` is created or written. Reading the library for the selection is the only
  access to it. (F3)
- Run.3 — With a non-empty selection the recording is transcribed as today, on the same route: one
  request, a split or calibration, with the same requests. Then `Name` is called once, with the
  recording, the segments that came back and the selection, and its naming requests go to the
  transcriber that transcribed the recording, one request each, within the request budgets the run
  resolved (`max-request-bytes`, `max-request-seconds`; phase 5 `Name.20`). (F1, Scope)
- Run.4 — The naming's outcomes are tallied once, after `Name`, when there is at least one; with none,
  nothing under `voices/` is written. (F6)
- Run.5 — Naming never fails the run. Whatever the report's status, an unreadable library, an unknown
  person and a failed naming request included, the run ends as a transcription with an empty selection
  ends, with that exit status, and its output is the transcript, as transcribed where no name was
  agreed, with the report; on `clai audio transcribe` the warnings of Output.5 say the same on stderr. A
  `Tally` that fails does not fail it either: its error is the report's `unrecorded`. ffmpeg missing
  while people are named is not a naming failure: Run.8. (F4)
- Run.6 — A transcription that fails, fails as today, with today's error; then nothing is named and
  nothing is tallied. (F4)
- Run.7 — No clip audio is in any output, tool result, error or stream of the run. (F8)
- Run.8 — ffmpeg is a dependency of naming people, not a naming failure. When people are named for a
  transcription (the call's `voices` holds a name, or else the run's `-voices` does, as Flag.1 reads
  it) and `ffmpeg` does not resolve through the route's `CommandRunner.LookPath`, the transcription
  stops before any request: nothing is transcribed, no naming request is sent, the library is neither
  read nor written, and there is no output. The failure is a `*VoicesNeedFFmpegError`, whose message
  says `-voices needs ffmpeg` (`voices needs ffmpeg` for the tool's argument): exit 1 on
  `clai audio transcribe`, a failed tool result on the tool. With nobody named, ffmpeg is not looked
  for before the transcription: a default selection (F11) on a machine without ffmpeg is transcribed as
  today and its naming is a failed naming request (`Name.16`, Run.5), warned of as L9.1 says; an empty
  selection never needs ffmpeg (F3). (F4, F11)

```go
package audio

// VoicesNeedFFmpegError says people were named for a transcription on a
// machine where ffmpeg does not resolve. Written to
// internal/audio/known_voices.go.
type VoicesNeedFFmpegError struct {
	// Origin says who named the people: voices.OriginTool for the call's
	// voices argument, voices.OriginFlag for -voices.
	Origin voices.Origin
	// Err is the failed lookup.
	Err *voices.MissingToolError
}

// Error starts "-voices needs ffmpeg" for OriginFlag and "voices needs
// ffmpeg" for OriginTool.
func (e *VoicesNeedFFmpegError) Error() string
func (e *VoicesNeedFFmpegError) Unwrap() error
```

### Wiring

- Wire.1 — One library serves every member: a clip enrolled by `voices add` or by `audio_remember_voice`
  is selected, picked and named with by the next transcription, and its tally shows in `voices list`.
  The library is `<clai-config>/voices`, the directory `utils.GetClaiConfigDir` returns, read on every
  use, and every event carries the wall clock's time, as `Librarian.3` stamps it.
- Wire.2 — `pkg/tools:AudioRememberVoiceDeps` is set by the clai runtime. Each call of it returns
  `RememberVoiceDeps` with `voices.NewInspector` over `audio:ExecRunner` and `voices.NewLibrarian` over
  the clai config directory, `voices.NewRanker()` and the wall clock; a config directory that does not
  resolve is its error, which the tool reports as its `*RememberVoiceUnavailableError`.
  `AudioTranscribeEngine` is set as today. `pkg/tools` imports nothing from `internal/audio` but
  `internal/audio/voices`, and `voices` imports nothing from `internal/audio`. (F9)
- Wire.3 — No member of the root names a vendor or a model: naming is the same on every transcriber the
  route resolves, model aliases and fallbacks included. (F1)

- Wire.4 — `audio` holds `VoicesCommand(open)` under `VoicesKey`, beside `transcribe`, `calibrate` and
  `help`, and `transcribe` holds no command. On the real command tree `clai audio voices list …`,
  `clai audio voices add …` and their short names (`clai a v l …`, `clai a v a …`, `clai audio v
  list …`) reach phase 7's CLI (`voices.1`). `clai audio transcribe` takes a file and nothing else, as
  before this phase: `clai audio transcribe voices` transcribes a file named `voices`, with the
  transcribe flags before it read as for any file, and a file of that name that does not exist is
  the error any missing file is. (amended 2026-11-19, A04: `transcribe` held the command, and a file
  named `voices` had to be written `./voices`.) `open` returns
  `VoicesDeps` with the same Librarian and Inspector as Wire.2, `audio:ExecRunner` as the `Runner`, and
  `Width` left nil, so tables take the width of stdout; a
  config directory that does not resolve is its error and the run's failure. (F9)
- Wire.5 — `-json` reaches the `list` leaf wherever phase 7 reads it: `voices list -json`,
  `voices list lorentz -json`, and the run's global `clai -json audio voices list` each print
  the JSON document of `list.14` and exit 0, by the short names too (`clai a v l -json`). The
  misplaced-flag hint of `architecture/output.md` is not
  printed for a `-json` after the `voices` verb, nor for `add`'s `-from`, `-to` and `-name` after
  `<wav>`. Everywhere else `-json` is found and reported as today. A
  `-json`, `-from`, `-to` or `-name` before the file on `clai audio transcribe`, which `transcribe`
  does not define, is refused with the line it had before this phase,
  `failed to parse flagset: flag provided but not defined: <flag>`: no verb below `transcribe` defines
  those flags any more. The hint that follows it is the dispatcher's
  for the flag's owners: for `-json` it is the hint it was before this phase; for `-from` and `-to`,
  which had none, it is `hint: '<flag>' belongs to 'audio voices add' — place it there, after
  that command.`; for `-name` it names `audio voices add` among the commands that own it. The
  exit status is 1 in every case, as before. (Maintainer, 2026-11-19, R1-11: the hints. Amended
  2026-11-19, A04: the hints' target is `audio voices add`, and the line `flag provided but not
  defined here: <flag>`, which R1-11 accepted because the voices verbs stood below `transcribe`, is
  gone with them; a scratch build of the move at 1550dec+wip prints the lines stated here.)
- Wire.6 — The interrupt key at a menu question leaves the menu, on the real command tree too. Every
  run's context carries one `menu.Asking` (`WithAsking`). A first interrupt signal (SIGINT) that
  arrives while it reports `Waiting` cancels the run's context and is otherwise no signal of the run:
  nothing is reported for it, neither `shutting down gracefully` nor `interrupted by signal`, and the
  run's output and exit status are the command's own. So `clai audio voices list`,
  interrupted at a question, exits 0 with stderr empty and stdout as `q` leaves it, and nothing typed
  afterwards is acted on (voices.6, `Navigate.30`). Every other signal is handled as today: a SIGTERM,
  an interrupt while no menu question waits (a transcription, an action that runs, a clip that plays, a
  run that never shows a menu), and any signal after the first. (menu rule 10, `Navigate.31`)
- Wire.7 — `clai audio transcribe` with no file, with or without flags before the place a file would
  stand, prints on stderr the help that `clai audio transcribe -h` prints, together with the error
  that says the audio file argument is missing, and exits 1, as `clai audio` with no verb and
  `clai chat` with no subcommand print their help with their error. Stdout stays empty, nothing is
  transcribed and no request is sent. `-` is a file: stdin is read, as today. (Maintainer,
  2026-11-19, A04: before, the error line alone was printed.)

### Test seam: the mock transcriber's script

- Mock.1 — With `CLAI_MOCK_TRANSCRIBE_SCRIPT` unset, the `test` and `test-diarize` models answer as
  today. Set to the path of a JSON file holding an array of replies, the n-th `Transcribe` call of one
  transcription run answers the n-th reply, an array of `{start, end, speaker, text}` objects in the
  json output's units; calls past the end answer as today. A file that is missing or is not such an
  array fails the transcription with an error naming the variable.

Consumes:

- `Ranker`, `NewRanker` (phase 2)
- `Inspector`, `NewInspector`, `Runner` (phase 3)
- `internal/audio/voices/voicestest:NewRunner`, `Runner.Serve`, `Speech` (phase 3), by the tests: the
  runner under the real Inspector of `TestTranscribe_EnrolThenNamed`
- `Librarian`, `NewLibrarian`, `Clock`, `Selector.Select`, `Asked`, `Selection`, `Selection.Empty`,
  `Tallier.Tally`, `ClipOutcome`, `Tallied` (phase 4)
- `Namer`, `NewNamer`, `Naming`, `NamedSegment`, `Provenance`, `NamingReport`,
  `NamingReport.MarshalJSON`, `Provenance.MarshalJSON`, `NamingStatus` (phase 5); L5.4 (phase 5), by
  the end-to-end rows and goldens that name: their `ffmpeg` stub writes the file its last argument names
- `VoicesCommand`, `VoicesOpener`, `VoicesDeps`, `VoicesKey`, and the CLI clauses `voices.1`,
  `voices.5`, `add.3`, `add.4`, `list.14` (phase 7)
- `AudioRememberVoiceDeps`, `RememberVoiceDeps`, `RememberVoiceUnavailableError`, `AudioRememberVoice`
  and its result lines (phase 8)
- `internal/runenv:Env` (`JSON`, `WriteJSONDocument`); `internal/cli` `-json` scan and misplaced-flag
  hint (`architecture/output.md`) (import)
- `ClipRef`, `Status`, `LibraryReadError`, `Dir` (phase 1)
- `internal/audio:TranscribeQuerier`, `ToolOverrides`, `SetTranscribeOverrides` (import): the transcribe
  route. `ToolOverrides` gains the run's voices beside `Model` and `Format`.
- `internal/audio:Configurations`, `TranscribeConfig` (import): gains `max-voices`.
- `internal/audio:Flags`, `ApplyFlagOverrides` (import): `clai audio transcribe`'s flags gain `-voices`.
- `internal:MediaToolFlags`, `MediaToolSettings` (import): gain `-voices` beside `-am` and `-af`.
- `internal/audio:Transcriber`, `Segment`, `CommandRunner`, `ExecRunner`, `Budgets`, `ResolveBudgets`
  (import)
- `internal/audio:MockTranscriber` (import): the `test` and `test-diarize` route the gates transcribe
  with; gains `Mock.1`. Its `Transcribe` stats the file it is given, so a naming request's file must
  exist when it is sent.
- `pkg/tools:AudioTranscribeTool`, `AudioTranscribeEngine`; `internal/tools:Registry` (import)
- `internal:Command.Subs`, `Help` (the help Wire.7 prints); `go_away_boilerplate/pkg/cmd:Subcommander`
  (import)
- `internal/utils:GetClaiConfigDir` (import)
- `Asking`, `Asking.Waiting`, `WithAsking` (phase 6): the run's record of a waiting menu question
  (Wire.6); `voices.6` (phase 7)
- `MissingToolError` (phase 3); `Origin`, `OriginTool`, `OriginFlag`, `OriginLibrary` (phase 4);
  `NamingReport.Status`, `.Origin`, `.Voices`, `.Err`, `VoiceNaming`, `VoiceUnknown`, `VoiceAbsent`,
  `VoiceNotPlaced` and the `NamingStatus` constants (phase 5): what Run.8 and Output.5 read
- `internal/audio:CommandRunner.LookPath` (import): where Run.8 looks for `ffmpeg`
- `internal/runenv:Warnf`, `SubsystemAudio` (import): how Output.5's warnings are reported
- `internal/cli` composition (`compose.go`: the run's context, its `firstSignal` and the interrupted
  branch of the dispatch), `internal/runtimeboundary:SignalSource`, `DispatchMonitor`,
  `internal/interrupt:SignalError` (import): the signal handling Wire.6 changes for one case; the fake
  signal source of `internal/cli/interrupt_test.go` drives its test
- `internal/testenv:Main`, `FakeBins`; `e2e/e2etest:Harness`, `WriteExecutable` (import):
  `WriteExecutable` places the naming rows' `ffmpeg` and `ffprobe` stubs, as `withSplittingFFmpeg`
  (`e2e/records/tooling_audio_e2e_test.go`) does today. `FakeBins` writes stubs that only exit 0: it
  serves a row that needs a program to resolve and nothing more, never one whose stub must write a file.

## Acceptance criteria

| #   | Criterion                                                                                                                 | Proves                                         | Proof                                                                 |
| --- | ------------------------------------------------------------------------------------------------------------------------- | ---------------------------------------------- | --------------------------------------------------------------------- |
| 1   | With no voice named and no confirmed clip, no `voices/`, or an unreadable library, both routes give today's bytes in all four formats, send today's requests and write nothing under `voices/` | `Run.2`, `Output.4`, `Tool.1`, `Mock.1`, F3    | `Test_e2e_AUDIO_unchanged_without_voices`, `TestTranscribe_EmptySelectionSkipsNaming`, the eight existing goldens and the existing audio e2e tests, unedited |
| 2   | Flow "The user transcribes a file with known voices": `Select`, `Transcribe`, `Name`, `Tally`, and a json output of `segments` and `naming` | `Run.1`, `Run.3`, `Run.4`, `Output.1`, `Output.2`, `Flag.2`, F6 | `TestTranscribe_NamesKnownVoices`, golden `voices-named`              |
| 3   | Flow "The agent transcribes with known voices": the tool's `voices` reach `Select` and the result carries named segments and the report | `Tool.2`, `Run.1`, `Run.3`, `Output.1`, F11    | `TestAudioTranscribeTool_Voices`, `Test_e2e_audio_transcribe_tool_voices`, golden `voices-tool` |
| 4   | The tool's `voices` beat `-voices`, which beats the library's default up to `max-voices`                                   | `Tool.2`, `Flag.1`–`Flag.4`, `Config.1`, F11   | `TestTranscribe_Precedence`, `TestFlags_Voices`, `TestConfig_MaxVoices` |
| 5   | A failed naming request, an agreement on nothing, an unreadable library with people named, an unknown person and a failed tally all return the transcript with the reason, and exit 0 | `Run.5`, `Tool.3`, F4                          | `TestTranscribe_NamingNeverFails`, `TestTranscribe_TallyFailureIsReported`, golden `voices-not-diarized` |
| 6   | A transcription that fails still fails as today                                                                           | `Run.6`, F4                                    | `TestTranscribe_TranscriptionFailureStillFails`                       |
| 7   | vtt, srt and text carry the plain name for a confirmed clip, `<name> (unconfirmed)` for an unconfirmed one, and nothing else | `Output.3`                                     | `TestRender_NamedSegments`, golden `voices-text`                      |
| 8   | A clip added by `voices add`, and one added by `audio_remember_voice`, names a speaker of the next transcription and shows its naming in `voices list` | `Wire.1`, `Wire.2`, `Flag.5`, F9               | `TestTranscribe_EnrolThenNamed`, `TestRoot_VoicesDeps`, `TestRoot_ImportDirection` |
| 9   | Naming works the same on every transcriber, and no clip audio is in any output                                            | `Wire.3`, `Run.7`, F1, F8                      | `TestTranscribe_AnyTranscriber`, `TestTranscribe_NoAudioInOutput`     |
| 10  | The golden diff below holds: eight goldens unchanged but for `help`'s `audio\|a` line, four new (amended 2026-11-19) | Golden diff                                    | `go test ./e2e -run '^TestDiagnosticOutputGoldens$'`                  |
| 11  | On the real command tree, `audio voices` and its short names are the voices CLI, `voices` after `transcribe` is a file, and `-json` and `add`'s flags work wherever phase 7 reads them, with no misplaced-flag hint (amended 2026-11-19, A04) | `Wire.4`, `Wire.5`, `Flag.5`                   | `Test_e2e_AUDIO_voices_dispatch`, `Test_e2e_voices_flags_anywhere`    |
| 12  | The runtime's `AudioRememberVoiceDeps` and the voices opener return the real children over the config directory, and say so when it does not resolve | `Wire.2`, `Wire.4`, F9                         | `TestRoot_VoicesDeps`                                                 |
| 13  | On `clai audio transcribe` every naming failure is its own warning on stderr, stdout is byte-identical in all four formats, nothing is asked, and an empty selection warns of nothing | `Output.5`, `Output.3`, `Run.5`, L9.1, F4, F3  | `TestTranscribe_NamingWarnings`, golden `voices-named`                |
| 14  | People named without ffmpeg stop the transcription before any request, on both routes; nobody named without ffmpeg is transcribed, and a default selection's naming is a warned failure | `Run.8`, `Run.5`, F4, F11                      | `TestTranscribe_VoicesNeedFFmpeg`, `Test_e2e_AUDIO_voices_without_ffmpeg` |
| 15  | On the real composition, an interrupt at a menu question exits 0 in silence and a `y` typed afterwards deletes nothing; every other signal is as today | `Wire.6`, menu rule 10                         | `TestRun_InterruptAtAMenuQuestion`, the existing `internal/cli` interrupt tests, unedited |
| 16  | A stray `-json`, `-from`, `-to` or `-name` before the file on `audio transcribe` is refused in the wording of before this phase, with a hint that names `audio voices add` for `add`'s three (amended 2026-11-19, A04) | `Wire.5`                                       | `Test_e2e_voices_flags_anywhere`                                      |
| 17  | `clai audio transcribe` with no file prints its help and the error on stderr and exits 1, with nothing on stdout and nothing transcribed (2026-11-19, A04) | `Wire.7`                                       | `Test_e2e_AUDIO_transcribe_without_file`, `TestAudioCommand_TranscribeWithoutFile` |

## Test cases

Integration scenarios run in `internal/audio` with the real children: `NewLibrarian` over the `testenv`
home with `NewRanker` and a stepping clock, `NewNamer`, and the transcribe route as the commands build
it. The transcriber is a scripted `Transcriber` that counts its calls and answers the recording with a
stated transcript and each naming request by region, as in phase 5; ffmpeg is a recording
`CommandRunner`. A scenario that inspects a clip (`TestTranscribe_EnrolThenNamed`) runs the real
Inspector over phase 3's test support, `voices.NewInspector(voicestest.NewRunner())`, handed in through
the two injection points the interfaces declare: the `VoicesOpener` given to `VoicesCommand(open)` and
`pkg/tools:AudioRememberVoiceDeps`.

End-to-end rows run the CLI through `e2etest.Harness` with the `test-diarize` model and
`CLAI_MOCK_TRANSCRIBE_SCRIPT` (`Mock.1`). No end-to-end row and no golden inspects a clip: no consumed
member gives a `PATH` stand-in that speaks the Inspector's ffmpeg and ffprobe dialogue, and
`testenv.FakeBins` writes stubs that only exit 0. The rows that name
(`Test_e2e_audio_transcribe_tool_voices` and the goldens `voices-named`, `voices-text` and
`voices-tool`) put two stubs on `PATH` with `e2etest.WriteExecutable`, as `withSplittingFFmpeg` does
today: an `ffmpeg` that writes a few bytes to the file its last argument names, and an `ffprobe` that
prints a duration. Phase 5's L5.4 makes that `ffmpeg` enough for the Namer: each naming request's file
exists and is not empty (`Name.15`), so `MockTranscriber.Transcribe`'s stat of the file it is given
succeeds and the script's reply is returned.

Libraries are seeded through `Enrol`
with inspections built from fixed bytes, so clip ids are the same on every machine. A failure clause is
exercised with a stub child that breaks its contract only in the way the row names.

| Test                                          | Level                | Proves                                              | Given                                                                                                                                                                 | Then                                                                                                                                                                              | Must not                                                   |
| --------------------------------------------- | -------------------- | --------------------------------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ---------------------------------------------------------- |
| `Test_e2e_AUDIO_unchanged_without_voices`     | integration scenario | `Run.2`, `Output.4`, `Tool.1`, `Config.1`, F3, AC 1 | `audio transcribe` in vtt, srt, text and json, and the `audio_transcribe` tool, over: no `voices/`; a library whose clips are all unconfirmed; `voices` that is a regular file; an `audioConfig.json` written without `max-voices`; a listing of the config directory before and after | Every stdout and tool result equals the constant the existing e2e tests pin (`wantMockVTT`, `e2etest.MockTranscription`, the bare json array); exit 0; the listing is unchanged but for `audioConfig.json`; stderr gains nothing but the upgrade notice naming `transcribe.max-voices`, once | Create `voices/`, add a `naming` key, or run ffmpeg        |
| `TestTranscribe_EmptySelectionSkipsNaming`    | integration scenario | `Run.2`, `Mock.1`, F3, AC 1                         | The route with a stub Namer and a stub `Tally` that fail the test when called, a counting transcriber and runner; an empty selection; then `CLAI_MOCK_TRANSCRIBE_SCRIPT` unset on the `test` model; then set to a missing file and to a file holding `{}` | One `Transcribe` call, with the recording's path; no runner invocation; output equal to `Render` of the segments; the mock's two segments as today; an error naming `CLAI_MOCK_TRANSCRIBE_SCRIPT` for the missing and for the malformed file                               | Call `Name` or `Tally`, or send a second request           |
| `TestTranscribe_NamesKnownVoices`             | integration scenario | `Run.1`, `Run.3`, `Run.4`, `Output.1`, `Output.2`, `Flag.2`, F6, AC 2 | A library with Lorentz (two confirmed clips) and Syrus (one unconfirmed); `-voices 'Lorentz, syrus'`, json; a transcript with speakers `A` and `B`; naming answers that agree on `A` = Lorentz and `B` = Syrus | Calls in order: `Select`, one `Transcribe` of the recording, two naming requests to the same transcriber, one `Tally`; output is `{segments, naming}`; `A`'s segments read `Lorentz` with `voice.status` `confirmed`, `B`'s `Syrus` with `unconfirmed`; `events.jsonl` gains one `placed` line per picked clip and one `named` line per naming clip | Change a start, an end or a text; tally twice              |
| `TestTranscribe_Precedence`                   | integration scenario | `Tool.2`, `Flag.3`, `Flag.4`, `Config.1`, F11, AC 4 | A library of four people with confirmed clips and one with only an unconfirmed clip; in turn: tool `voices` `["Syrus"]` with `-voices Lorentz`; tool with no `voices` and `-voices Lorentz`; tool `voices` `[]` and `-voices ''`; neither, with `max-voices` 2; neither, with `max-voices` absent; neither, with `max-voices` 0; `-voices Lorentz` with `max-voices` 0 | `naming.origin` and the people of `naming.voices` are: `tool`, Syrus only; `flag`, Lorentz only; `library`, the four confirmed people; `library`, the two whose best clips rank first; `library`, the four; no `naming` at all and today's bare array (the empty selection); `flag`, Lorentz only | Merge the tool's people with the flag's, or cap named people by `max-voices` |
| `TestFlags_Voices`                            | unit                 | `Flag.1`, `Flag.2`, `Flag.3`, AC 4                  | `-voices 'Lorentz, Christoph ,,syrus'` on `audio transcribe`, on `query` and on `chat`; `-voices ''`; no flag; a run that sets `-am`, `-af` and `-voices` together      | The people are `Lorentz`, `Christoph`, `syrus` on all three commands; the empty and the absent flag give no people; the three settings reach the tool's run together               | Fail on a name the library does not hold                   |
| `TestConfig_MaxVoices`                        | unit                 | `Config.1`, AC 4                                    | `audioConfig.json` with `max-voices` 5, 0, absent and -1, over a library of six people with confirmed clips; then `-voices` naming all six with `max-voices` 2 and with 0 | The default selection has five people, nobody, six (12 is the cap, and the upgraded file reads 12), and the load fails with an error naming `transcribe.max-voices`; all six are selected both times | Read 0 as 12, or cap named people                          |
| `TestAudioTranscribeTool_Voices`              | contract suite       | `Tool.1`, `Tool.2`, `Tool.3`, AC 3                  | Calls through the schema with an engine that records its `AudioTranscribeCall`: `voices` `["Lorentz","Christoph"]`; no `voices`; `voices` `[]`; `voices` `"Lorentz"`; `voices` `[1]`; the specification's properties | The engine gets the two names; then an empty `Voices` twice; then the `Tool.3` error twice, with the engine not called; the properties are `file_path`, `output_format`, `voices`, with only `file_path` required | Reject a call without `voices`, or check a name            |
| `Test_e2e_audio_transcribe_tool_voices`       | integration scenario | `Tool.2`, `Flag.3`, `Run.3`, `Output.1`, `Mock.1`, AC 3 | `-r -cm mock_test -t=audio_transcribe -am test-diarize -af json -voices Lorentz q tool_audio_transcribe` over a seeded library, a script that agrees on `A` = Lorentz, and the `ffmpeg` and `ffprobe` stubs of the preamble on `PATH` (L5.4) | Exit 0; the tool result holds `"naming"` with `"origin": "flag"` and a segment whose speaker is `Lorentz` with a `voice`                                                           | Write the transcript raw to stdout                         |
| `TestTranscribe_NamingNeverFails`             | integration scenario | `Run.5`, F4, AC 5                                   | In turn: the transcriber failing on the first naming request; naming answers that agree on nothing; `voices` a regular file with `-voices Lorentz`; `-voices Ada` on a library without Ada; a stub Namer that returns `request-failed` | Each run ends without error, exit 0 on the CLI; the segments are the transcript's; `naming.status` is `request-failed`, `no-agreement`, `library-unreadable`, `no-voice`, `request-failed`; Ada is `unknown-voice`; nothing is tallied in the four runs whose naming was not answered | Return an error, or drop the transcript                    |
| `TestTranscribe_TallyFailureIsReported`       | integration scenario | `Run.5`, `Run.4`, F4, AC 5                          | A named run with a stub Librarian whose `Tally` returns a `*voices.LibraryWriteError`; then a named run in text format                                                 | The json output has the named segments and `naming.unrecorded` with the error's message; the text output is the named transcript; no error either time                           | Fail the run, or lose the names                            |
| `TestTranscribe_TranscriptionFailureStillFails` | integration scenario | `Run.6`, F4, AC 6                                 | A non-empty selection and a transcriber that fails on the recording; the same run with an empty selection                                                              | The same error both times; no `Name` call, no `Tally` call, no output                                                                                                             | Return a report in place of the error                      |
| `TestRender_NamedSegments`                    | unit                 | `Output.1`–`Output.4`, AC 7                         | Three segments, `Lorentz` named by a confirmed clip, `Syrus` by an unconfirmed clip, `B` unnamed, with a report; rendered in the four formats; then the same segments unnamed with an empty selection | json is the object of the Interface's example shape, keys in order, speakers `Lorentz` and `Syrus` plain with `voice.status`; text and srt read `Lorentz: …`, `Syrus (unconfirmed): …`, `B: …`; vtt reads `<v Lorentz>`, `<v Syrus (unconfirmed)>`, `<v B>`; the empty selection gives today's bytes in all four | Add a `voice` key to an unnamed segment, a report to vtt, srt or text, or `(unconfirmed)` to json or to a confirmed name |
| `TestTranscribe_EnrolThenNamed`               | integration scenario | `Wire.1`, `Flag.5`, F9, AC 8                        | In `internal/audio`, the commands as the root builds them: `VoicesCommand(open)` and `AudioRememberVoiceDeps`, both returning `voices.NewInspector(voicestest.NewRunner())` and a real Librarian over the `testenv` home's config directory, with the sources `Serve`d as `Speech`. `voices add <wav> -name Lorentz`; an `AudioRememberVoice` call for Syrus; then `audio transcribe -af json <file>` on the scripted transcriber, which agrees on both, without `-voices` and then with `-voices Syrus`; then `voices list -json`; then `audio -h` and `audio transcribe -h` (amended 2026-11-19, A04) | Lorentz is named with status `confirmed` by default selection; Syrus is not selected (unconfirmed, nobody named him) until `-voices Syrus` names him with `unconfirmed`; `voices list -json` shows `named` 1 for each naming clip; `audio -h` names `-voices` and the `voices` verb with `v`, and `audio transcribe -h` names `-voices` and holds no `voices add` | Use a second library, or need a restart to see a new clip  |
| `Test_e2e_AUDIO_voices_dispatch`              | integration scenario | `Wire.4`, `Flag.5`, AC 11                           | Through the harness, in a directory holding an audio file named `voices`, over a seeded library, without a terminal: `audio transcribe -am test voices`; `audio voices list`, `audio v list`, `a v l`; `a v l lorentz`; `audio voices`; `a v` (amended 2026-11-19, A04: the case `audio transcribe -voices Lorentz voices list` is gone, since it now transcribes the file `voices`) | The first prints the mock transcript; the next three print the people table of phase 7, with the same stdout; the fifth prints Lorentz's clips; the last two print the voices synopsis on stderr, naming `clai audio v\|voices a\|add <wav>` and `clai audio v\|voices l\|list [person` (amended 2026-11-19, R3-01), and exit 2 with stdout empty; nothing is transcribed in the last six | Open the voices CLI for a file named `voices`, need `./` before it, or transcribe a file for `voices list` |
| `Test_e2e_voices_flags_anywhere`              | integration scenario | `Wire.5`, AC 11                                     | Over a seeded library, each as `audio voices …` (amended 2026-11-19, A04): `audio voices list -json`; `voices list lorentz -json`; `-json audio voices list`; `voices add <wav> -from 61.2 -to 69`; `a v a <wav> -from abc -name Syrus`; then `-r -cm test q hello -json`; then `audio transcribe -am test -json <file>`, and the same with `-from 1`, `-to 2` and `-name x` in place of `-json` | The first three print the `list.14` array (everyone, Lorentz alone, everyone) and exit 0; the two adds reach phase 7's `add` with the flags after `<wav>`: the first is the usage line of `add.3`, the second the usage error of `add.4` naming `-from` and not the line of `add.3` (so `-name` was read), each exit 2 with nothing inspected; stderr holds no misplaced-flag hint in the five; the sixth is reported exactly as it is before this phase; the last four each exit 1 with a line holding `failed to parse flagset: flag provided but not defined: ` and the flag, and nothing transcribed; the hint is Wire.5's: for `-json` the hint the same command had at 1550dec, for `-from` and `-to` `hint: '<flag>' belongs to 'audio voices add' — place it there, after that command.`, for `-name` a hint that names `audio voices add` among its owners (amended 2026-11-19, journal `maintainer` line, R1-11; amended 2026-11-19, A04: the target and the line) | Swallow a `-json` outside the voices verb, print the hint for a flag phase 7 reads, transcribe `<wav>`, or read a stray flag on `audio transcribe` as a file |
| `TestTranscribe_NamingWarnings`               | integration scenario | `Output.5`, `Output.3`, `Run.5`, L9.1, F4, F3, AC 13 | `audio transcribe` as the command builds it, with the run's stdout, stderr and stdin captured, over a library of Lorentz, Syrus and Christoph (confirmed clips) and the scripted transcriber. In turn: (1) `-voices 'Lorentz,Ada,Bo'`, Lorentz named; (2) `-voices 'Lorentz,Syrus,Christoph'`, Lorentz named, Syrus's clip landing and naming nobody, Christoph's landing in neither order; (3) `-voices Lorentz,Syrus`, answers that agree on nothing; (4) `-voices Lorentz,Ada`, the transcriber failing on the first naming request with `429 rate limited`; (5) `-voices Lorentz` with `voices` a regular file; (6) `-voices Lorentz` on a transcriber that returns no labels; (7) `-voices Lorentz` on a transcript whose every run is under 3 s; (8) `-voices Ada`; (9) no `-voices`, the default selection, Lorentz named and the others not; (10) no `-voices`, the naming request failing; (11) no `-voices` on the transcriber without labels; (12) no `-voices` over a library with no confirmed clip; (13) run 2 through the `audio_transcribe` tool. Runs 1–4 in each of text, vtt, srt and json | stderr holds exactly, as `warn [audio]` lines in this order: (1) `no voice "Ada" in the library`, `no voice "Bo" in the library`; (2) `failed to find a match for Syrus, were they in the meeting?`, then the same for Christoph; (3) that line for Lorentz and for Syrus; (4) `no voice "Ada" in the library`, then one line starting `naming failed: `, holding `429 rate limited` and ending `; speakers left as labels`, and no line for Lorentz; (5) one line starting `voice library unreadable: `; (6) and (7) L9.1's one line each; (8) `no voice "Ada" in the library` alone; (9) nothing; (10) the `naming failed: ` line alone; (11), (12) and (13) nothing. Every run exits 0; stdout equals the stdout of the same run with stderr discarded and is the same in runs 1–4 whatever the warnings, per format; stdin is unread | Fold two cases into one line, print a warning on stdout or into the transcript, warn with an empty selection or from the tool, warn `failed to find a match` under the default selection, ask a question, or exit non-zero |
| `TestTranscribe_VoicesNeedFFmpeg`             | integration scenario | `Run.8`, `Run.5`, F4, F11, F3, AC 14                 | A `CommandRunner` whose `LookPath("ffmpeg")` fails, a counting transcriber, a library with confirmed clips and a listing of it before and after. In turn: `audio transcribe -voices Lorentz`; `-voices Ada`, whom the library does not hold; the tool with `voices` `["Lorentz"]`; the tool with no `voices` on a run with `-voices Lorentz`; `-voices ''`; no `-voices`; no `-voices` over a library with no confirmed clip | The first four fail with a `*VoicesNeedFFmpegError` (`errors.As`), `Origin` `flag`, `flag`, `tool`, `flag`, wrapping a `*voices.MissingToolError` for `ffmpeg`; the message holds `-voices needs ffmpeg`, and `voices needs ffmpeg` without the dash for the tool's argument; no `Transcribe` call, no output, the listing unchanged; exit 1 on the CLI. The fifth and sixth transcribe once, exit 0, give the transcript as transcribed with `naming.status` `request-failed`, and warn `naming failed: …; speakers left as labels`. The last is today's bytes with no warning and no `LookPath` call | Send a request before stopping, report the stop as a naming status, stop a run that named nobody, or look for ffmpeg with an empty selection |
| `Test_e2e_AUDIO_voices_without_ffmpeg`        | integration scenario | `Run.8`, F4, F3, AC 14                               | Through the harness with `PATH` an empty directory and a seeded library: `audio transcribe -am test-diarize -voices Lorentz <file>`; then `audio transcribe -am test-diarize <file>` over a library with no confirmed clip | The first exits 1, stdout empty, stderr holding `-voices needs ffmpeg`; the second prints the mock transcript and exits 0 | Depend on an ffmpeg the machine has                        |
| `TestRun_InterruptAtAMenuQuestion`            | integration scenario | `Wire.6`, `Navigate.30`, `Navigate.31`, voices.6, AC 15 | In `internal/cli`, the composition with the fake signal source of `interrupt_test.go`, stdin and stdout reported as terminals, stdin a pipe, over a seeded library. In turn (each as `audio voices …`; amended 2026-11-19, A04): `audio voices list`, an interrupt sent once the people level waits; `… voices list lorentz <third>`, answer `d`, an interrupt at the delete question, then `y` and a newline; the first again with SIGTERM in place of the interrupt; `… voices list lorentz <first>`, answer `p`, an interrupt while a player runs: a `play` shell script the test writes itself into a directory it puts first on `PATH`, which blocks until it is signalled or killed (`internal/cli` tests are `package cli`, out of reach of `e2etest.WriteExecutable`, and a `testenv.FakeBins` stub exits at once); an interrupt during `audio transcribe -am test <file>` held by `CLAI_MOCK_TRANSCRIBE_DELAY` | The first two return status 0 with stderr empty, holding neither `shutting down gracefully` nor `interrupted`, and stdout equal to the same walk left with `q` but for the echoed `q`; the third clip is still listed after the late `y`. The last three end as they do at 1550dec for a signal: the shutdown warning, the interruption as the run's one error, status 143, 130 and 130 | Report an interrupt taken at a menu question, exit 130 for it, delete on the late `y`, or quieten any other signal |
| `Test_e2e_AUDIO_transcribe_without_file` (2026-11-19, A04) | integration scenario | `Wire.7`, AC 17 | Through the harness, stdin empty: `audio transcribe`; `a t`; `audio transcribe -am test`; `audio transcribe -voices Lorentz -af json`; then `audio transcribe -h`, whose output is the help to look for | Each of the four exits 1 with stdout empty; stderr holds the help of `audio transcribe -h` whole (its first line, its examples and its `-voices` flag line among them) and one error line that says the audio file argument is missing; nothing is transcribed (no mock transcript, no `transcribing ` line) | Print the error alone, print the help on stdout, exit 0 or 2, or read stdin as the file |
| `TestAudioCommand_TranscribeWithoutFile` (2026-11-19, A04) | integration scenario | `Wire.7`, AC 17 | In `internal/audio`, the command tree of `Command(deps)` run with a captured Env: `audio transcribe`, and `audio transcribe -af text` | The run's error says the audio file argument is missing; the Env's stderr holds the `transcribe` command's `Help()`, its stdout nothing; no querier is run | Return the error with no help written, or write the help of the `audio` namespace in its place |
| `TestRoot_VoicesDeps`                         | integration scenario | `Wire.1`, `Wire.2`, `Wire.4`, F9, AC 8, AC 12       | The runtime's `AudioRememberVoiceDeps` and the opener given to `VoicesCommand`, called in the `testenv` home; a clip enrolled through the tool's Librarian, with an inspection built from fixed bytes, and listed through the opener's, and one the other way round; then both with a config directory that does not resolve, and an `audio_remember_voice` call with good arguments | Both return an Inspector and a Librarian over `<clai-config>/voices`, and each Librarian lists the clip the other enrolled; then both return an error, and the call fails with `*RememberVoiceUnavailableError` wrapping it, with nothing inspected | Keep a Librarian between calls, create `voices/` when asked for dependencies, or need an inspection |
| `TestRoot_ImportDirection`                    | unit                 | `Wire.2`, F9, AC 8                                  | The import lists of every Go file in `pkg/tools` and in `internal/audio/voices`, tests included                                                                         | No non-test file of `pkg/tools` imports an `internal/audio` package but `internal/audio/voices`, and its tests add only `voicestest`; `voices` imports none                         | Shell out to the go command                                |
| `TestTranscribe_AnyTranscriber`               | integration scenario | `Wire.3`, `Run.3`, F1, AC 9                         | The named run of `TestTranscribe_NamesKnownVoices` on three transcribers: one answered in one request, one behind a split, one with no `Diarization` method; then one that returns no speaker labels | The three outputs are equal; the last gives the transcript with `naming.status` `not-diarized` and no naming request                                                              | Branch on a model name, or type-assert the transcriber     |
| `TestTranscribe_NoAudioInOutput`              | integration scenario | `Run.7`, F8, AC 9                                   | Clip files and a recording holding a marker string; a named run, a failed naming and a failed tally, in json and text; stdout, stderr, the tool result and every error rendered as text | The marker is in none of them                                                                                                                                                      | Print a clip's bytes or a request file's content           |
| `TestDiagnosticOutputGoldens`                 | repeatable command   | Golden diff, `Output.1`, `Output.2`, `Run.5`, AC 10 | The twelve goldens of the Golden diff                                                                                                                                 | The eight existing files are byte for byte as committed; the four new ones match                                                                                                 | Regenerate an existing golden                              |

## Error management

| Failure                                                          | Expected outcome                                                                                                      | Test                                            |
| ---------------------------------------------------------------- | --------------------------------------------------------------------------------------------------------------------- | ----------------------------------------------- |
| The tool's `voices` is not an array of strings                   | The call fails with "voices must be an array of strings, the names of the people expected in the recording"; nothing transcribed | `TestAudioTranscribeTool_Voices`      |
| `transcribe.max-voices` is negative                              | The config load fails with an error naming `transcribe.max-voices`                                                     | `TestConfig_MaxVoices`                          |
| People named (`-voices`, the tool's `voices`) and ffmpeg does not resolve | `*VoicesNeedFFmpegError` before any request: `-voices needs ffmpeg`, exit 1 on the CLI, a failed tool result on the tool; nothing transcribed, the library untouched | `TestTranscribe_VoicesNeedFFmpeg`, `Test_e2e_AUDIO_voices_without_ffmpeg` |
| Nobody named, a default selection, and ffmpeg does not resolve   | Not an error: transcribed as today, `naming.status` `request-failed`; the CLI warns `naming failed: <reason>; speakers left as labels` | `TestTranscribe_VoicesNeedFFmpeg`               |
| A named person the library does not hold                         | Not an error: the run succeeds; `naming.voices` lists the name as `unknown-voice`; the CLI warns `no voice "<name>" in the library` | `TestTranscribe_NamingNeverFails`, `TestTranscribe_NamingWarnings` |
| A named person in the library whom no speaker was matched to     | Not an error: `naming.voices` lists the person `absent` or `not-placed`; the CLI warns `failed to find a match for <name>, were they in the meeting?` and asks nothing | `TestTranscribe_NamingWarnings`                 |
| The library cannot be read, nobody named                         | Not an error and no report: the empty selection, today's output                                                       | `Test_e2e_AUDIO_unchanged_without_voices`       |
| The library cannot be read, people named                         | Not an error: the transcript as transcribed, `naming.status` `library-unreadable` with `error`                         | `TestTranscribe_NamingNeverFails`               |
| A naming request cannot be built or is not answered              | Not an error: the transcript as transcribed, `naming.status` `request-failed` with `error`; nothing tallied; the CLI warns `naming failed: <reason>; speakers left as labels` | `TestTranscribe_NamingNeverFails`, `TestTranscribe_NamingWarnings` |
| The naming agrees on nothing                                     | Not an error: the transcript as transcribed, `naming.status` `no-agreement`; the outcomes are tallied                  | `TestTranscribe_NamingNeverFails`               |
| The transcriber returns no speaker labels                        | Not an error: the transcript as transcribed, `naming.status` `not-diarized`                                            | `TestTranscribe_AnyTranscriber`, golden `voices-not-diarized` |
| `Tally` fails after a naming                                     | Not an error: the named output, `naming.unrecorded` holds the `*voices.LibraryWriteError`'s or `*voices.LibraryReadError`'s message | `TestTranscribe_TallyFailureIsReported` |
| The transcription itself fails                                   | Today's error, unchanged; nothing named, nothing tallied                                                              | `TestTranscribe_TranscriptionFailureStillFails` |
| The clai config directory does not resolve (voices CLI, `audio_remember_voice`) | The opener's error is the run's failure; the tool fails with `*RememberVoiceUnavailableError` wrapping it; nothing written | `TestRoot_VoicesDeps` |
| `voices` with no verb on the real tree (`audio voices`, `a v`)   | Phase 7's synopsis on stderr, exit 2; nothing transcribed                                                              | `Test_e2e_AUDIO_voices_dispatch`                |
| A stray `-json`, `-from`, `-to` or `-name` before the file on `audio transcribe` | `failed to parse flagset: flag provided but not defined: <flag>`, exit 1, with Wire.5's hint: `-json`'s as before this phase, `audio voices add` named for `-from`, `-to` and `-name` (amended 2026-11-19, journal `maintainer` line, R1-11; amended 2026-11-19, A04) | `Test_e2e_voices_flags_anywhere`                |
| `clai audio transcribe` with no file                             | The help of `audio transcribe -h` and the error `missing audio file argument…` on stderr, stdout empty, exit 1; nothing transcribed (2026-11-19, A04) | `Test_e2e_AUDIO_transcribe_without_file`, `TestAudioCommand_TranscribeWithoutFile` |
| An interrupt (SIGINT) while a voices menu question waits         | Not an error: the menu is left as by `q`, exit 0, stderr empty                                                         | `TestRun_InterruptAtAMenuQuestion`              |
| Any other signal                                                 | As today: the shutdown warning, the interruption as the run's error, exit 128 plus the signal's number                  | `TestRun_InterruptAtAMenuQuestion`              |
| `CLAI_MOCK_TRANSCRIBE_SCRIPT` names a missing or malformed file  | The mock's transcription fails with an error naming the variable                                                      | `TestTranscribe_EmptySelectionSkipsNaming`      |

## Golden diff

Goldens are `e2e/testdata/diagnostics/*.golden`, regenerated with
`go test ./e2e -run '^TestDiagnosticOutputGoldens$' -args -update-diagnostic-goldens` (README,
Validation policy). The four new goldens run with a library seeded under the isolated config directory
and the recording under the isolated home, so every path in them is `$CONFIG` or `$HOME`; a variable
whose value is a path is set by the row's setup and is not listed in the golden's `env` header. That
setup also puts the `ffmpeg` and `ffprobe` stubs of the Test cases preamble first on `PATH` for
`voices-named`, `voices-text` and `voices-tool` (L5.4); `voices-not-diarized` runs no program
(`Name.3`). The libraries are seeded through `Enrol`, never through `voices add`.

| Golden                                                                                              | Change                                                                                                                                                             |
| --------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| `success`, `warning`, `error`, `buffered`, `help`, `json-query`, `json-error`, `json-output`        | None: byte for byte as committed before this phase (F3), with one exception: `help`'s `audio\|a` line reads `Transcribe and record audio: audio t\|transcribe <file> ('-' reads stdin), audio v\|voices, audio c\|calibrate` (maintainer, journal 2026-11-19; amended 2026-11-19). No other golden, and no other line of `help`, may change. |
| `voices-named` (new)                                                                                | `audio transcribe -am test-diarize -af json -voices Lorentz,Ada <file>`: status 0; stdout is `{"segments": […], "naming": {…}}` with speaker `A` as `Lorentz` carrying `voice`, speaker `B` unnamed with `no-match`, Lorentz `named`, Ada `unknown-voice`, origin `flag`. Amended 2026-11-19 (Output.5): stderr gains the one line `warn [audio] no voice "Ada" in the library`; stdout is unchanged |
| `voices-not-diarized` (new)                                                                         | `audio transcribe -am test -af json <file>` over a library with a confirmed clip: status 0; stdout is the mock's two segments without speakers and `naming` with status `not-diarized`, origin `library`. Unchanged by Output.5: nobody was named, so stderr stays empty (L9.1) |
| `voices-text` (new)                                                                                 | `audio transcribe -am test-diarize -af text -voices Lorentz,Syrus <file>` over a library where Lorentz's clip is confirmed and Syrus's is not: status 0; stdout reads `Lorentz: …` and `Syrus (unconfirmed): …`, with no report. Unchanged by Output.5: both are named, so stderr stays empty |
| `voices-tool` (new)                                                                                 | `-r -cm mock_test -t=audio_transcribe -am test-diarize -af json q tool_audio_transcribe` over a library with a confirmed clip: status 0; the tool result is the `voices-named` shape with origin `library` |

## Removes

None.

## Local facts

Every other fact this phase upholds is a Root fact: F1, F3, F4, F6, F8, F9, F11 and the max-voices
parameter.

**L9.1 — The naming warnings of `clai audio transcribe`.** One warning per case, in this order; `<name>`
is the name as given for a person the library lacks and the stored name otherwise, and `<reason>` is the
report's `error` on one line. "Named" means the people came from `-voices` (`naming.origin` `flag`);
"default" means the library's default selection (`origin` `library`). Binds Output.5.

| #   | Case, read from the naming report                                                    | Selection       | Message, once per person or once per run                             | Decided by                                   |
| --- | ------------------------------------------------------------------------------------ | --------------- | -------------------------------------------------------------------- | -------------------------------------------- |
| 1   | A person with `unknown-voice`                                                        | named           | `no voice "<name>" in the library`, per person                       | orchestrator, 2026-11-19, stated to the maintainer and not objected (journal, 2026-11-19 `orchestrate` line) |
| 2   | Status `library-unreadable`                                                          | named           | `voice library unreadable: <reason>; speakers left as labels`        | plan, 2026-11-19; told to the maintainer by the orchestrator and not objected (journal, 2026-11-19 `orchestrate` line) |
| 3   | Status `request-failed` (network, quota, rate limit, a request that cannot be built, ffmpeg missing under the default selection) | named, default  | `naming failed: <reason>; speakers left as labels`                   | orchestrator, 2026-11-19, stated to the maintainer and not objected (journal, 2026-11-19 `orchestrate` line) |
| 4   | Status `not-diarized`                                                                | named           | `the transcript has no speaker labels; nobody named`                 | plan, 2026-11-19; told to the maintainer and not objected, as case 2 |
| 5   | Status `no-candidate`                                                                | named           | `no speaker talks long enough to compare; nobody named`              | plan, 2026-11-19; told to the maintainer and not objected, as case 2 |
| 6   | Status `named` or `no-agreement`: a person with `absent` or `not-placed`             | named           | `failed to find a match for <name>, were they in the meeting?`, per person | maintainer, 2026-11-19                 |

Under the default selection only case 3 warns: nobody said those people were in the meeting, so a person
who is not matched, a transcript without labels and one without a candidate are the ordinary run. A
person whose status is `not-compared` has no line of their own: cases 2–5 say why once. A failed tally
(`naming.unrecorded`) is not a naming failure and has no warning here.

## Human required

The root's go-live waits on the measurement note (journal, maintainer decisions Q8, Q11, Q16). The
hermetic tests prove the rule on stated labels; only a run on real recordings with real provider keys
says whether real voices are named right often enough. The maintainer provides the keys and the
recordings and is asked for nothing else; the agent runs the measurement and does its own debugging
(Q8). Building and measuring run in parallel (Q16): nothing in this phase waits for the measurement
but its `Complete`.

- **Artifacts the person produces.** Two, and no more:
  1. A keys file that sets `OPENAI_API_KEY` and `OPENROUTER_API_KEY`, at a path outside the repository
     that the maintainer names. The agent loads it into the environment of the commands below and never
     prints, logs or copies a value: not into the note, the notebook, the worklog or a command line.
  2. The recordings of the two Daily syncs whose named transcripts are in the notebook under
     `meetings/wasmer/transcriptions/` (Q11), made on different days, at paths the maintainer names.
     The earlier is the first recording, the later the second.
- **What the agent runs.** On the built binary, with the keys loaded: it enrols each person of the
  second recording, and one person who is in the first recording and not in the second, with
  `clai audio voices add <first recording>.wav -from <s> -to <s> -name <person>` (amended
  2026-11-19, A04), on a
  stretch where the notebook's transcript of the first recording has that person speaking alone. Then
  it runs the README's two root human checks,
  `clai audio transcribe -am gpt-4o-transcribe-diarize -voices <people> -af json <second recording>.wav`
  and the same with `-am 'or:elevenlabs/scribe-v2+diarize'`, the absent person among `<people>`. The
  ground truth is the notebook's named transcript of the second recording (Q11), not anybody's ears:
  each segment of the json output counts, by its length, for the speaker that transcript names for the
  passage.
- **The measurement note.** A dated note the agent writes at the notebook's `codebases/imago/clai/`,
  which the README's go/no-go parameter then cites in place of "measurement note, pending" (the
  orchestrator makes that README edit). It states, for each of the two transcribers: the share of each
  enrolled person's speech that carries their name, and the share of other people's speech that carries
  any library name, with the absent person's clip in the selection; the commands as run, the binary's
  commit, the two transcripts it counted against, and the verdict. The go/no-go holds when the first
  share is at least 94 % and the second at most 2 %, on both transcribers. The note holds no key and no
  audio.
- **Before.** While the phase is being built, the agent may run a prototype of the same naming request
  (the layout of `Name.5` and `Name.6`) on the two recordings, to learn early which way the measurement
  points (Q16). A prototype result goes into the note under its own dated heading and never stands in
  for the run on the built binary. The agent commits no measurement tooling to the repository: the
  measurement is out of Scope, so whatever it counts with stays outside the tree. It records in
  Implementation notes the enrolment command and the two commands as they are typed on the built binary.
- **Where the agent stops.** After every row of Test cases passes, the Golden diff holds and the gates
  have passed twice, and before the phase is reported `Complete`: phase 9 is not `Complete` until the
  measurement note exists. When the keys file or a recording is missing at that point, the agent writes
  `date · work · phase 9 waiting on human` and the line becomes `waiting on human: phase 9`; under Q18
  the orchestrator writes both. When the maintainer has provided them, he writes
  `date · maintainer · phase 9 handoff done` and sets the line `running`. When both artifacts are
  already there, there is no handoff: the agent goes straight on to the measurement.
- **After.** The agent runs the commands above and writes the note. When the go/no-go holds on both
  transcribers, it records the note's path and its four figures in Implementation notes, and the phase
  can be `Complete`. When the note says no-go on either transcriber, the phase is not `Complete` and
  the root is not marked done: the agent stops and files a design andon, and the maintainer decides
  what changes.

## Implementation notes

| Run   | Commit      | Command                                                                                                                                              | Result                                                                    |
| ----- | ----------- | ---------------------------------------------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------- |
| start | 1550dec+wip | `env -u OPENAI_API_KEY -u ANTHROPIC_API_KEY -u OPENROUTER_API_KEY make qa`; `go vet ./...`; `go run github.com/mibk/dupl@latest -t 80 .`             | 106 packages ok, 0 failed (`internal/audio` 20.7 s); vet exit 0; 49 groups |
| end   | 1550dec+wip | the same three                                                                                                                                       | 106 packages ok, 0 failed (`internal/audio` 21.0 s); vet exit 0; 49 groups |
| end   | 1550dec+wip | `make lint`, then `go test ./... -race -count=3 -cover -timeout=30s -p 11 -shuffle=on`; `go vet ./...`; `go run github.com/mibk/dupl@latest -t 80 .` | 106 packages ok, 0 failed (`internal/audio` 21.3 s); vet exit 0; 49 groups |
| fix-start | 1550dec+wip | `env -u OPENAI_API_KEY -u ANTHROPIC_API_KEY -u OPENROUTER_API_KEY go test ./internal/audio/ ./internal/capture/... ./internal/... ./e2e/... ./pkg/diagnostic/... -race -count=3 -cover -timeout=30s -p 6`; `go vet ./...`; staticcheck and `gofumpt -l` on `internal/audio`, `internal/capture`, `e2e/...`; `dupl -t 80 .` | 101 packages ok (`internal/audio` 17.6 s, `internal/capture` 14.3 s, e2e 3.0–10.2 s); vet, staticcheck, gofumpt clean; 49 groups |
| fix-end   | 1550dec+wip | the same | 101 packages ok (`internal/audio` 18.6 s, `internal/capture` 17.6 s, e2e 4.3–11.1 s); vet, staticcheck, gofumpt clean; 49 groups; R1-11 not fixed (plan) |
| fix-end   | 1550dec+wip | the same, `go test … -shuffle=on` | 101 packages ok (`internal/audio` 18.1 s, `internal/capture` 17.3 s, e2e 3.9–11.3 s); vet, staticcheck, gofumpt clean; 49 groups |
| fix-start | 1550dec+wip | R1-17, R1-11, R1-12 (`Wire.6`): `env -u OPENAI_API_KEY -u ANTHROPIC_API_KEY -u OPENROUTER_API_KEY make qa`; `go vet ./...`; `go run github.com/mibk/dupl@latest -t 80 .` | exit 0, 106 packages ok (`internal/audio` 20.9 s, `internal/cli` 5.9 s); vet exit 0; 49 groups |
| fix-end   | 1550dec+wip | the same three | exit 0, 106 packages ok (`internal/audio` 21.9 s, `internal/cli` 7.4 s); vet exit 0; 49 groups |
| fix-end   | 1550dec+wip | `make lint`, then `go test ./... -race -count=3 -cover -timeout=30s -p 11 -shuffle=on`; `go vet ./...`; `dupl -t 80 .` | lint exit 0; 106 packages ok (`internal/audio` 22.3 s, `internal/cli` 7.2 s); vet exit 0; 49 groups |
| fix-end   | 1550dec+wip | R1-11 rows: `env -u OPENAI_API_KEY -u ANTHROPIC_API_KEY -u OPENROUTER_API_KEY make qa`; `go vet ./...`; `dupl -t 80 .` | exit 0, 106 packages ok (`internal/audio` 21.9 s, `e2e/interactive` 7.0 s); vet exit 0; 49 groups |
| replan | 1550dec+wip | A04 (maintainer, 2026-11-19; major): the voices CLI hangs under `audio` (`v\|voices`), `transcribe` takes a file only, and with no file prints its help with the error. Amended in place: the CLI block, `Flag.5`, `Wire.4`, `Wire.5`, `Wire.6`, new `Wire.7`, Consumes (`Command.Help`), AC 11, 16, new 17, `TestTranscribe_EnrolThenNamed`, `Test_e2e_AUDIO_voices_dispatch`, `Test_e2e_voices_flags_anywhere`, `TestRun_InterruptAtAMenuQuestion`, new `Test_e2e_AUDIO_transcribe_without_file` and `TestAudioCommand_TranscribeWithoutFile`, Error management, Human required | documents only, no code changed or reverted; a scratch build of the move at 1550dec+wip gave Wire.5's lines; the body follows by R1-18..R1-21; the amended interface waits for re-validation and the maintainer's sign-off |
| fix-start | 1550dec+wip | R1-18, R1-19, R1-20, R1-21 (one run): `env -u OPENAI_API_KEY -u ANTHROPIC_API_KEY -u OPENROUTER_API_KEY make qa`; `go vet ./...`; `go run github.com/mibk/dupl@latest -t 80 .` | exit 0, 106 packages ok (`internal/audio` 24.9 s, `internal/cli` 8.2 s, `e2e/interactive` 9.5 s); vet exit 0; 49 groups |
| fix-end   | 1550dec+wip | the same three | exit 0, 106 packages ok (`internal/audio` 25.5 s, 92.4 %; `internal/cli` 8.3 s; `e2e/interactive` 9.3 s); vet exit 0; 49 groups (R1-18..R1-21) |
| fix-start | 1550dec+wip | R3-01 (the e2e row), R3-04, the audio `Desc` (maintainer, 2026-11-19): `env -u OPENAI_API_KEY -u ANTHROPIC_API_KEY -u OPENROUTER_API_KEY make qa`; `go vet ./...`; `go run github.com/mibk/dupl@latest -t 80 .` | exit 0, 106 packages ok (`internal/audio` 22.3 s, `internal/cli` 6.5 s, `e2e/interactive` 7.2 s); vet exit 0; 49 groups |
| fix-end   | 1550dec+wip | the same three | exit 0, 106 packages ok (`internal/audio` 23.5 s, 92.4 %; `internal/cli` 8.7 s; `e2e/interactive` 8.9 s); vet exit 0; 49 groups; `TestDiagnosticOutputGoldens` regenerated, only `help.golden` line 16 changed (R3-01, R3-04) |

Andon: none.
Surprises:
- API change to `pkg/tools`: `AudioTranscribeEngine` takes `AudioTranscribeCall{FilePath, OutputFormat, Voices}` instead of `(filePath, outputFormat string)`; the four named call sites are adapted with their assertions unchanged (`withFakeEngine` wraps the old shape, so its callers are untouched).
- The voices subtree gives the dispatcher owners for `-json`, `-from`, `-to` and `-name`: a stray `-json` on `audio transcribe` before the file now reads "flag provided but not defined here: -json" (was "failed to parse flagset: flag provided but not defined: -json"), with the same global `-json` hint.
- The library folds a clip's events by time, so a fixture clock later than the wall clock hides the route's tallies (`named` 0); the fixtures' clocks start in 2020.
- `TestFlags_Voices` is two tests: `query` and `chat continue` in `internal/text` (`mediaToolSettings` is unexported there), `audio transcribe` and the tool's run in `internal/audio`.
- `audio.CommandDeps` gains `OpenVoices` (nil is the runtime's opener), so `TestTranscribe_EnrolThenNamed` hands the voicestest Inspector to the real tree; `internal/cli` passes nothing.
- The first `audio transcribe` of a fresh config directory writes today's `photoConfig.json`, `videoConfig.json` and `shellContexts/default.json` (config prep), so `Test_e2e_AUDIO_unchanged_without_voices` compares listings from the second run on and the library from the start.
- R1-02's class on this phase's paths: a naming whose ffmpeg exits 3 still exits 0 (F4), and tool errors become tool results; today's split route (`-max-request-bytes 10`, ffprobe exiting 3) exits 3 on 1550dec as well, left as today (Run.6).
- R1-11 is a body change only by breaking go_away_boilerplate's `Command.Flagset` contract ("pure and memoized … the same *flag.FlagSet instance") or by rewording the dispatcher's error for `audio transcribe voices` owners in `internal/cli`; neither was done.
- R1-17 (2026-11-19): `resolveKnownVoices` (`known_voices.go`) is Run.8 then the selection, called by both routes with `ExecRunner{}` before the remote model list is fetched, so the selection now comes before that fetch; `*VoicesNeedFFmpegError` wraps a `*voices.MissingToolError`. `namingWarningsOf` is L9.1; `TranscribeQuerier` reports the warnings through `runenv.Warnf` after the transcript is written, only when `knownVoices.warnings` is `warnOnStderr` (set by `clai audio transcribe`'s setup; the tool leaves the zero `warnNone`). Golden `voices-named` gained only `warn [audio] no voice "Ada" in the library`.
- R1-12 `Wire.6` (2026-11-19): `run` gives the run one `menu.Asking` (`menu.WithAsking`); `firstSignal.leavesMenu` takes a first `os.Interrupt` while `Waiting` by calling the run's cancel with no cause and not passing it on, so the monitor neither reports nor escalates it. On a real pty (the built binary, `python3 pty`, Ctrl-C at the people question and at a delete question) the run exits 0 and the only bytes after the question are the tty's own `^C` echo: no newline, so the shell prompt follows on the question's row.
- R1-11 not closed (2026-11-19): the four rows cannot hold as signed. At 1550dec `audio transcribe -am test -from 1 <file>` and `-to 2` printed no hint, and `-name x` named five owners; now each prints `hint: '<flag>' belongs to 'audio transcribe voices add'…` (for `-name` with the five others), because `voices add` owns those flags. Only `-json`'s hint is as before. Wire.5 says the hint is as before; restoring it is a body change in the dispatch hint, so this needs the maintainer (design andon proposed to the orchestrator).
- R1-11 closed (2026-11-19, test only): the maintainer accepted the new hints (journal, 2026-11-19 `maintainer` line); Wire.5, the CLI block, the `Test_e2e_voices_flags_anywhere` row and the Error management row are amended to state them. The four rows in `Test_e2e_voices_flags_anywhere` pin, for `audio transcribe -am test <flag> <file>` with `-json`, `-from 1`, `-to 2` and `-name x`: exit 1, `flag provided but not defined here: <flag>`, exactly one hint (`-json`'s as at 1550dec; `'-from'`/`'-to' belongs to 'audio transcribe voices add' — place it there, after that command.`; `-name`'s naming `'audio transcribe voices add'` among its owners), and nothing transcribed. No dispatcher change.
- R1-18, R1-19 (A04, `Wire.4`, `Wire.7`): `transcribe` holds no command. Its `OnSetup` checks for the file first, before `ConfigPrep`: with none it writes `tc.Help()` on stderr (what `-h` prints, now that `transcribe` has no subcommands) and returns `errMissingAudioFile`, the error `resolveAudioInput` also returns; nothing is prepared, resolved or transcribed. On the built binary `clai audio transcribe` and `clai audio transcribe -am test` print the help, a blank line and `error: missing audio file argument, usage: clai audio transcribe <file>`, exit 1, stdout empty.
- R1-18: the audio `Desc` (`cmd.go:98`, an anchor of R1-18) is unchanged: it never named the voices CLI, and the Golden diff holds `help.golden`, which prints it, byte for byte. `audio -h` names `v|voices` in its Verbs and examples.
- R1-20 (`Wire.5`): with no verb below `transcribe`, a stray flag before the file reads `failed to parse flagset: flag provided but not defined: <flag>` again, and the dispatcher's hint names `'audio voices add'` (`-from 1` on the built binary: `hint: '-from' belongs to 'audio voices add' — place it there, after that command.`). No dispatcher change.
- R1-21: `architecture/audio.md`, `cmd-dispatch.md` and `output.md` state the tree `audio` → `transcribe|t`, `voices|v` (→ `add|a`, `list|l`), `calibrate|c`, `help|h`, Wire.7, the `-json` exception after `clai audio voices list`, and the stray-flag hint.
- 2026-11-19, maintainer: the audio `Desc` (`cmd.go:98`) names `audio v|voices`; `help.golden` was regenerated with `-update-diagnostic-goldens` and only its `audio|a` line changed (the other seven and the four voices goldens byte for byte). The Golden diff's first row and AC 10 are amended to name the exception. On the built binary `clai -h | grep audio` shows `audio|a  Transcribe and record audio: audio t|transcribe <file> ('-' reads stdin), audio v|voices, audio c|calibrate`.
- R3-01: `Test_e2e_AUDIO_voices_dispatch` asserts `clai audio v|voices a|add <wav>` and `clai audio v|voices l|list [person` (row amended). R3-04: `architecture/audio.md`'s Entry Flow draws `→ audio → v|voices → a|add, l|list` under `→ cmd.Run(...)`, before the transcribe subcommand, marked as running no querier.

Human required (measurement, not run: no provider request was made). On the built binary (`go build -o <bin>/clai .` at the measured commit; `ffmpeg` and `ffprobe` on `PATH`; keys loaded from the maintainer's keys file without printing; a fresh `CLAI_CONFIG_DIR`):

```sh
clai audio voices add <first recording>.wav -from <s> -to <s> -name <person>
clai audio transcribe -am gpt-4o-transcribe-diarize -voices <people> -af json <second recording>.wav
clai audio transcribe -am 'or:elevenlabs/scribe-v2+diarize' -voices <people> -af json <second recording>.wav
```

## Review findings

### Review 2, 2026-11-19

Root review round 2 at `1550dec+wip`. No finding. The phase stays In Progress on the measurement (Human required).

- **R1-11, R1-12 (`Wire.6`), R1-17 resolved.**
  - `Wire.5`: the four stray flags before the file read `flag provided but not defined here: <flag>` with the hints the clause states, exit 1; compared against a 1550dec build.
  - `Wire.6`: `firstSignal.leavesMenu` (`internal/cli/compose.go:480-492`) takes only a first `os.Interrupt` while `Asking.Waiting`, cancels without a cause and does not pass it on. On a pty: exit 0, stderr empty at a question; SIGTERM at a question exit 143 with both lines; Ctrl-C while a clip plays exit 130 with both lines. `TestRun_InterruptAtAMenuQuestion`: 15 shuffled race runs ok.
  - `Run.8`: `resolveKnownVoices` (`internal/audio/known_voices.go:69-80`) looks ffmpeg up before the library is read, on both routes; without ffmpeg `-voices Lorentz` exits 1 with `-voices needs ffmpeg …` and the tool answers a failed result; nobody named and a default selection transcribes and reports `request-failed`.
  - `Output.5`, L9.1: seen on the built binary, one warning each and stdout unchanged: `no voice "Ada" in the library`; `voice library unreadable: …`; `naming failed: …` (default selection, no ffmpeg); `the transcript has no speaker labels; nobody named`; `no speaker talks long enough to compare; nobody named`; `failed to find a match for Syrus, were they in the meeting?`. The default selection warns only of case 3, and the tool writes none.
- **note** F3 compared against a 1550dec build, byte for byte on stdout, stderr and exit status, and by a checksum of every file under the config directory: 17 transcription and tool commands (four formats, `test` and `test-diarize`, the global `-json`, a bad format, a missing file, a split, four tool runs) in six settings: no library, no ffmpeg on `PATH`, an unreadable library, `voices` as a file, a library with only an unconfirmed clip, and a confirmed library with `max-voices` 0. Identical but for the random chat id; nothing under `voices/` written. Help texts and the tool listing differ as the phase states.
- **note** `naming failed: ffmpeg is not installed; inspecting a clip needs ffmpeg and ffprobe; speakers left as labels` is what the default selection warns without ffmpeg: the reason is phase 3's `MissingToolError` message, which speaks of inspecting a clip. The behaviour is `Name.16` and L9.1 case 3; the wording is reported to the orchestrator, not filed.

### Review 3, 2026-11-19

Review round 3 of the A04 change at `1550dec+wip` (R1-18..R1-21), phase 9's part. One finding.

- **R1-18 (phase 9 part), R1-19, R1-20, R1-21 resolved; none of R1-01..R1-17 regressed** (the gates pass at start and end).
  - `Wire.4`, `Flag.5`: `audio`'s `Subs` hold `transcribe|t`, `VoicesKey`, `calibrate|c`, `help|h` (`internal/audio/cmd.go:195-200`); `transcribe` holds none. On the built binary `clai a t -am test voices` transcribes the file `voices`; with no such file `clai audio transcribe voices` fails with `failed to find audio file: stat voices: …`, as `nonexist.wav` does; `clai audio transcribe -am test -voices Lorentz voices list` transcribes `voices` (a word after the file is not read, as before this phase). `clai audio -h` lists `v|voices`; `clai audio transcribe -h` has no subcommands section and names `-voices`.
  - `Wire.7`: `transcribe.OnSetup` (`internal/audio/cmd.go:134-139`) checks for the file before `ConfigPrep`, writes `tc.Help()` on stderr and returns `errMissingAudioFile`. On the built binary `clai audio transcribe`, `clai a t -am test -voices Lorentz -af json` and `clai audio transcribe --` exit 1 with stdout empty, stderr the whole text `-h` prints then `error: missing audio file argument, usage: clai audio transcribe <file>`, and the scratch config directory left empty. Under the global `-json` the help is `output` records and the error an `error` record, as `clai -json audio` gives. `clai audio transcribe -` still reads stdin.
  - `Wire.5`: `clai audio transcribe -from 1 voices` and `-name x voices` exit 1 with `failed to parse flagset: flag provided but not defined: <flag>` and the hint naming `'audio voices add'`. (The root usage on stdout is the dispatcher's, known and out of scope.)
  - `Wire.6`: `TestRun_InterruptAtAMenuQuestion` runs `audio voices list`; with `TestVoicesList_InterruptLeavesAsQuit` and the e2e voices rows it passes `-count=3 -shuffle=on` under `-race`.
  - R1-21: `architecture/cmd-dispatch.md:228-241` and `output.md:68-70` state the tree, Wire.7 and the hint; `audio.md` but for R3-04.
- **R3-04 (minor, owner fix)** · phase 9 Interface (`architecture/audio.md` says the same), R1-21 · `architecture/audio.md:27` · The Entry Flow draws `→ audio → v|voices → a|add, l|list` one level under `→ querier.Query(ctx)`, as a step of a transcription's query. The voices CLI is dispatched by `cmd.Run` beside `transcribe` and runs no querier; the line moved with A04 and kept the place it had as a sub-verb of `transcribe`.
  - [ ] Draw the voices branch under `→ cmd.Run(...)`, at the level of `→ audio → transcribe subcommand`, not under `querier.Query(ctx)`.
- **note** The `audio` `Desc` (`internal/audio/cmd.go:98`), which `clai -h` prints as `audio|a  Transcribe and record audio: audio t|transcribe <file> ('-' reads stdin), audio c|calibrate`, does not name `v|voices`. It is unchanged because `help.golden` is held byte for byte by the Golden diff (F3); it does not name `h|help` either, so it is a summary, not a list of verbs. Whether `clai -h` should show `voices` is the maintainer's call (a Golden diff amendment), not a finding.
- **note** `internal/audio` took 27.3 s at the start and 26.6 s at the end of the 30 s per-binary timeout under `make qa` (`-count=3`; earlier runs 20.3–25.5 s). A04's tests add little: `TestVoices_Verb` 0.07 s, `TestTranscribe_EnrolThenNamed` 0.04 s, the others under 0.01 s alone under `-race`. The margin is load, not this change, but it is thin.
