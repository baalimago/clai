# Phase 3 — audio_record tool

**Parent:** root · **Class:** contract · [README](README.md)

## Goal

Let a model record this machine's microphone and system audio into one WAV
file, with the `capture` configuration section, a terminal stop line and the
recording facts as its answer.

## Interface

Written to `pkg/tools/audio_tool_record.go`, with the engine in
`internal/audio/record.go`, the configuration in `internal/audio/querier.go`
and the registration row in `internal/tools/handler.go`.

```go
package tools

import (
	"context"

	pub_models "github.com/baalimago/clai/pkg/text/models"
)

// AudioRecordCall is one parsed call. An empty or zero field takes the
// configured default. (F7)
type AudioRecordCall struct {
	// Devices records exactly these sources in this order and overrides Sinks
	// and Mic. (F2)
	Devices        []string
	MaxSeconds     int
	SilenceTimeout int
	NameSuffix     string
	Dir            string
	Sinks          string
	Mic            *bool
}

// AudioRecordEngine records one call and answers with the facts a model reads;
// internal/audio installs it in its init, as it installs AudioTranscribeEngine.
// (AudioRecordEngine.1, F8)
var AudioRecordEngine func(ctx context.Context, call AudioRecordCall) (string, error)

// AudioRecordTool is the audio_record schema. (F1, F2, F7)
type AudioRecordTool pub_models.Specification

// AudioRecord records this machine's audio. (Specification.1)
var AudioRecord = AudioRecordTool{
	Name: "audio_record",
	Description: "Record this machine's microphone and/or system audio (what the speakers play) into one 16 kHz mono WAV file and return its path and times. The recording stops on Enter when the terminal is interactive, after the configured silence timeout, or at max_seconds. Call audio_sources first to learn the device names.",
	Inputs: &pub_models.InputSchema{
		Type: "object",
		Properties: map[string]pub_models.ParameterObject{
			"devices":                 {Type: "array", Description: "Exact device names from audio_sources, recorded in this order. When set, sinks and mic are ignored.", Items: &pub_models.ParameterObject{Type: "string"}},
			"max_seconds":             {Type: "integer", Description: "Hard stop after this many seconds. Omit to use the configured default."},
			"silence_timeout_seconds": {Type: "integer", Description: "Stop after this many seconds without sound above the threshold, once sound has been heard. Omit to use the configured default; the configured 0 disables the trigger."},
			"name_suffix":             {Type: "string", Description: "Short context appended to the file name, for example 'release-week'. Reduced to lowercase words joined by dashes."},
			"dir":                     {Type: "string", Description: "Directory for the recording. Omit to use the configured directory; a leading ~/ is expanded."},
			"sinks":                   {Type: "string", Description: "Which system outputs to record: 'default' (what you hear), 'all' (every output), or 'none'. Omit to use the configured default.", Enum: &audioRecordSinks},
			"mic":                     {Type: "boolean", Description: "Record the default microphone. Omit to use the configured default."},
		},
	},
}

// audioRecordSinks are the allowed sinks values. (F2)
var audioRecordSinks = []string{"default", "all", "none"}

// Call runs the tool with context.Background(). (Call.1)
func (a AudioRecordTool) Call(input pub_models.Input) (string, error)

// CallWithContext parses the arguments and returns the recording facts.
//
// - A value of the wrong type, or a devices entry that is not a non-empty
//   string, is an error naming the field or the entry's index, and calls no
//   engine. (CallWithContext.1, F2)
// - A nil AudioRecordEngine is an error saying audio capture is not available
//   in this runtime. (CallWithContext.2, F8)
// - Otherwise it returns the engine's text unchanged, so the facts, an
//   interrupted recording's path and a backend's remedy all reach the model.
//   (CallWithContext.3, F4, F6, F8)
func (a AudioRecordTool) CallWithContext(ctx context.Context, input pub_models.Input) (string, error)

// Specification advertises the schema above. (Specification.1)
func (a AudioRecordTool) Specification() pub_models.Specification
```

```go
package audio

import (
	"context"
	"io"

	"github.com/baalimago/clai/internal/capture"
	pkgtools "github.com/baalimago/clai/pkg/tools"
)

// CaptureConfig is the `capture` section of audioConfig.json. It holds no
// device names, so a configuration stays portable between machines. (F7)
type CaptureConfig struct {
	Dir            string  `json:"dir"`
	MaxSeconds     int     `json:"max-seconds"`
	SilenceTimeout int     `json:"silence-timeout"`
	SilencePeakDB  float64 `json:"silence-peak-db"`
	Sinks          string  `json:"sinks"`
	Mic            *bool   `json:"mic"`
}

// recordText records one call and renders its facts. (recordText.1, F6)
func recordText(ctx context.Context, call pkgtools.AudioRecordCall, r capture.Recorder, stdin io.Reader, stdinIsTTY bool) (string, error)
```

Clauses of `recordText`:

1. `recordText.1` — The call overrides the `capture` section, which overrides
   the built-in default. A `sinks` value outside `default`, `all` and `none`, a
   negative `max-seconds`, a negative `silence-timeout`, or a
   `silence-peak-db` above 0 is an error naming the field and its bound, before
   the recorder runs. (F7)
2. `recordText.2` — `Dir` expands a leading `~/` and is created when missing; a
   failure to create it is an error naming the directory. (F7)
3. `recordText.3` — A terminal stdin reads one line and closes the recorder's
   stop channel; a non-terminal stdin passes none. (F3)
4. `recordText.4` — One status line goes to stderr when the recording starts and
   nothing goes to stdout; the answer is six lines in this order: `recorded`,
   `started_at`, `ended_at`, `duration_seconds`, `stopped_because`,
   `sources`. (F6, F9)
5. `recordText.5` — An error from the recorder is returned unchanged. (F4, F8)

`init` in the same file sets `pkgtools.AudioRecordEngine` to a closure over
`capture.New()`, `os.Stdin` and `utils.IsTerminalWriter(os.Stdin)`.

Consumes: `capture:Recorder`, `capture:Request`, `capture:Recording`,
`capture:Selection`, `capture:Source` (phase 1); `pkg/text/models:LLMTool`,
`Input`, `Specification`, `InputSchema`, `ParameterObject` (import);
`internal/audio:Configurations`, `Default` (import);
`internal/utils:LoadConfigFromFile`, `AbsHomePath`, `IsTerminalWriter`,
`GetClaiConfigDir` (import); `internal/tools:executableTool`, `registerLocalTools`
(import); the phase 1 fixture `internal/capture/testdata/fake_ffmpeg.sh`
(recorded fixture).

## Acceptance criteria

| #   | Criterion                                                                                       | Proves                          | Proof                            |
| --- | ----------------------------------------------------------------------------------------------- | ------------------------------- | -------------------------------- |
| 1   | The schema names `audio_record`, offers seven optional arguments and requires none               | `Specification.1`, `F1`         | `TestAudioRecord_Specification`  |
| 2   | Wrong types and a bad `devices` entry are refused by field name or index without calling the engine | `CallWithContext.1`, `F2`   | `TestAudioRecord_Call`           |
| 3   | An uninstalled engine reports that capture is unavailable, its error text passes through, and the call context reaches it | `CallWithContext.2`, `CallWithContext.3`, `F8` | `TestAudioRecord_Call` |
| 4   | The call overrides the file and the file overrides the built-in default, and an out-of-range value is refused by field | `recordText.1`, `F7` | `TestRecordText_Config`, `TestRecordText_Facts` |
| 5   | The configured directory is created, and an uncreatable one is an error naming it                | `recordText.2`, `F7`            | `TestRecordText_DirError`        |
| 6   | A terminal stdin line ends the recording with `enter`; a non-terminal stdin passes no stop channel | `recordText.3`, `F3`           | `TestRecordText_Enter`           |
| 7   | The answer holds the six facts in order and the status line goes to stderr                       | `recordText.4`, `F6`, `F9`      | `TestRecordText_Facts`           |
| 8   | The tool is registered exactly when `ffmpeg` resolves and a linked binary carries the engine     | `F8`, `AudioRecordEngine.1`     | `TestRegisterLocalTools_AudioRecordGate`, `Test_audioRecordEngineWired` |
| 9   | A recorder error comes back unchanged, so an interrupted recording keeps its saved path          | `recordText.5`, `F4`            | `TestRecordText_RecorderError`   |

## Test cases

The `ffmpeg` in every row is phase 1's fixture, never the real program.

| Test                                   | Level                | Proves                                     | Given                                                                  | Then                                                                                 | Must not                      |
| -------------------------------------- | -------------------- | ------------------------------------------ | ---------------------------------------------------------------------- | ------------------------------------------------------------------------------------ | ----------------------------- |
| `TestAudioRecord_Specification`        | contract suite       | `Specification.1`, `F1`, AC 1               | The package value                                                      | Name `audio_record`, seven optional properties, three `sinks` values, no required     | Require an argument           |
| `TestAudioRecord_Call`                 | contract suite       | `Call.1`, `CallWithContext.1`–`CallWithContext.3`, `F2`, `F8`, AC 2, AC 3 | Inputs with a string `mic`, `devices: ["a", 7]`, `devices: [""]`; then a stub engine and a nil engine | Each error names the field or index; the stub sees the call's context and its text returns unchanged; the nil engine reports the runtime error | Call the engine on a bad input |
| `TestRecordText_Facts`                 | integration scenario | `recordText.1`, `recordText.4`, `F6`, `F9`, AC 4, AC 7 | A test home with `audioConfig.json` naming a temporary directory, the fixture in `spoken` mode, stdio captured | Six lines in order, the WAV exists at the reported path, and the status line is on stderr | Write to stdout              |
| `TestRecordText_Config`                | integration scenario | `recordText.1`, `F7`, AC 4                 | A file with `sinks: "speakers"`, then `max-seconds: -1`, then settings a call overrides | Errors naming the field; the call's values reach the recorder                        | Start ffmpeg on a bad value    |
| `TestRecordText_RecorderError`         | integration scenario | `recordText.5`, `F4`, AC 9                 | A cancelled call context, and a fixture that fails on its own                          | The interrupted error holds the saved path; the failing error holds ffmpeg's message | Wrap either error away         |
| `TestRecordText_DirError`              | integration scenario | `recordText.2`, `F7`, AC 5                  | `dir` below a regular file                                              | Error naming the directory                                                           | Start a recording             |
| `TestRecordText_Enter`                 | integration scenario | `recordText.3`, `F3`, AC 6                  | A reader holding `"\n"` with `stdinIsTTY` true, then false, then an empty reader with `stdinIsTTY` true | The recording ends with `enter`; the non-terminal run passes no stop channel; the empty reader's stop channel stays open (the phase-4 e2e runs the recording to its silence timeout) | Treat end of input as an Enter press |
| `TestEnterStop_EOFIsNotALine`          | unit                 | `recordText.3`, `F3`                       | An empty reader and a reader holding one line, both as a terminal       | Only the completed line closes the stop channel                                       | Close on EOF, or leave the channel open on Enter |
| `TestRegisterLocalTools_AudioRecordGate` | unit               | `F8`, AC 8                                  | `registerLocalTools` with a lookup that resolves ffmpeg, then one that fails | Present, then absent                                                            | Consult the real `PATH`       |
| `Test_audioRecordEngineWired`          | unit                 | `AudioRecordEngine.1`, `F8`, AC 8            | An import of `internal/audio`                                           | `AudioRecordEngine` is not nil                                                       | Touch a device                |

## Error management

| Failure                              | Expected outcome                                             | Test                                      |
| ------------------------------------ | ------------------------------------------------------------ | ----------------------------------------- |
| Field of the wrong type              | Error naming the field and its type                           | `TestAudioRecord_Call`                    |
| `devices` entry empty or not a string | Error naming the entry index                                 | `TestAudioRecord_Call`                    |
| Engine not installed                 | Error saying capture is unavailable in this runtime           | `TestAudioRecord_Call`                    |
| Engine error, including `ErrInterrupted` | Error text unchanged, so the saved path survives           | `TestRecordText_RecorderError`            |
| `sinks`, `max-seconds` or `silence-timeout` out of range | Error naming the field and its bound      | `TestRecordText_Config`                   |
| Configured directory cannot be created | Error naming the directory; no recording starts             | `TestRecordText_DirError`                 |
| `ffmpeg` absent from `PATH`          | Both tools are absent from the registry                       | `TestRegisterLocalTools_AudioRecordGate`  |

## Golden diff

None. `clai tools` is asserted by footer only
(`Test_goldenFile_TOOLS_lists_tools_and_footer`), and completion does not
snapshot the tool catalog.

## Removes

None.

## Local facts

**L1 — Result text.** Exactly six newline-terminated lines, in this order:
`recorded: <absolute path>`, `started_at: <RFC3339 UTC>`,
`ended_at: <RFC3339 UTC>`, `duration_seconds: <integer>`,
`stopped_because: <silence|max_seconds|enter|interrupt|error>`,
`sources: <kind>=<label>` with further sources appended as `; <kind>=<label>`.
The status line written before the recording starts reads
`recording… Enter stops it, or the silence timeout, or the maximum duration`.

**L2 — Injection.** `internal/audio`'s `init` builds the recorder per call, so
one recording leaves no state behind for the next one; the engine closure also
carries `os.Stdin` and the terminal check. A test that needs a fixed answer
overrides `pkg/tools.AudioRecordEngine` and restores it in `t.Cleanup`. The
existing `pkg/agent` linkage test pattern proves the init ran.

**L3 — Terminal stop line.** One goroutine reads a single completed line with
`bufio.Reader` and closes the stop channel. End of input is not a line:
`utils.IsTerminalWriter` reports every character device as a terminal, and
`/dev/null` — the stdin of a scripted or harnessed run — is one, so closing on
EOF interrupted ffmpeg before it wrote the WAV (R3-01). A non-interactive run
therefore keeps recording until the silence timeout or `max-seconds`. The read
cannot be interrupted with the standard library, so at most one abandoned read
outlives a recording; the tool runs once per call, and the recorder's context
is the tool call's context, so Ctrl+C still finalizes the file through the
interrupted path.

**L4 — Config location.** `Configurations` gains
`Capture CaptureConfig \`json:"capture"\``, and `Default` carries the README
parameters table, so `utils.LoadConfigFromFile(confDir, "audioConfig.json", nil,
&Default)` yields the file merged over the defaults and writes the new section
into an existing file through the repository's upgrade path. `Mic` is a `*bool`
so an absent key resolves to `true` and an explicit `false` is honoured.

**L5 — Registration and documentation.** The tool joins `audio_sources`' row in
the `executableTools` table with the same `executable: "ffmpeg"`. This phase
adds its catalog row to `pkg/tools/README.md` and a capture section to
`architecture/audio.md` covering both tools, the configuration, the stop
conditions, the platform matrix and the prerequisites.

## Human required

None. Listening to a real recording, granting the darwin microphone permission
and holding a loopback device are README human checks the maintainer runs once
every phase is complete.

## Implementation notes

| Run   | Commit      | Command                                                                                              | Result                                                    |
| ----- | ----------- | ---------------------------------------------------------------------------------------------------- | --------------------------------------------------------- |
| start | 1c75d78+wip | `go test ./internal/capture ./internal/audio ./pkg/tools ./internal/tools -count=1 -timeout=60s`    | FAIL: capture 3, audio 5; pkg/tools and internal/tools pass |
| end   | 1c75d78+wip | `go test ./internal/capture ./internal/audio ./pkg/tools ./internal/tools -race -count=3 -timeout=180s` | pass                                                  |
| end   | 1c75d78+wip | `go vet ./...` && `staticcheck ./...` && `go test ./e2e/... -count=1 -timeout=120s`                  | pass                                                      |

Andon: none.
Surprises:
- The start gate failed inside phase 1: the fixture routed recording calls into its discovery branch and the recorder waited twice on one process, ignored the call context and read the whole WAV into memory. `internal/capture` was repaired in this phase; the fixture now routes on the arguments, as ffmpeg does.
- The full `go test ./... -race -count=1` gate still fails only in `internal/summary` (`TestNewAgentSummarizer_loadsConfig`); the same failure reproduces from `git archive HEAD` in a clean directory, so it predates this worklog.

## Review findings

### Review 1, 2026-10-28 — root review

**Status: Complete — no finding in this phase; R1-01's interruption path is
fixed in phase 1 and re-verified here.**

**Verified good.**

- **The configuration cascade is call > file > default** and every bound is
  checked before a recorder runs (`internal/audio/record.go:74-111`,
  `TestRecordText_Config`): `capture.sinks`, `capture.max-seconds`,
  `capture.silence-timeout` and `capture.silence-peak-db` each fail by field
  name, and a refused configuration never reaches `Recorder.Record`.
- **`Mic` is a `*bool` on both sides**, so an absent key resolves to `true`
  and an explicit `false` survives, and a per-call `mic` overrides the file.
- **The directory is created and expanded** (`~` through `utils.AbsHomePath`),
  and an uncreatable directory is an error naming it before any recording
  (`recordText.2`, `TestRecordText_DirError`).
- **The result is the six pinned lines in order**, the status line goes to
  stderr and nothing goes to stdout (`TestRecordText_Facts`,
  `TestRecordText_WritesNothingToStdout`).
- **The engine is installed by `internal/audio`'s `init`** and the tool is
  registered exactly when `ffmpeg` resolves; both gate tests pass.
- **A recorder error is returned unchanged**, so an interrupted recording's
  saved path survives the tool layer (`TestRecordText_RecorderError`).

**Findings**

None. The blocked recording that this phase exposed was phase 1's R1-01: the
cancellation test here passed only because phase 1's fixture is a shell trap
that tolerates a repeated SIGINT. The real-ffmpeg measurement in phase 1's
review found the lost file.

**Notes.**

- A per-call `silence_timeout_seconds: 0` takes the configured default rather
  than disabling the timer; zero means "not set" in `AudioRecordCall`, as its
  interface comment says, and the schema tells a model that the *configured*
  `0` disables. A model that wants the timer off must rely on the file.
- The start line is printed before `Recorder.Record` resolves the selection,
  so a refused selection prints `recording…` on stderr before the error.
  Cosmetic, and F9 permits exactly one status line.

### Review 2, 2026-10-28 — root review, holistic sweep

**Status: no finding in this phase.** R2-01 and R2-02 are recorder fixes
(phase 1); `recordText` needed no change. The cancelled-call path now pays the
recorder's one-second grace before ffmpeg receives its single `SIGINT`
(`TestRecordText_RecorderError/cancelled call`), and a source that never
pauses now reaches the six result lines instead of `ErrNoAudio`. Package time
under the gate: `go test ./internal/audio -race -cover -count=3 -timeout=30s`
passes in 17.0 s, 88.9 % coverage.

### Review 3, 2026-10-28 — root review, holistic sweep

**Status: Complete — R3-01 fixed and verified in the same session.**

**Findings**

- [x] **R3-01** (major, `recordText.3`, `F3`) — **a non-interactive run
  interrupted its own recording.** `enterStop` closed the stop channel on any
  read result, including EOF. `utils.IsTerminalWriter` calls every character
  device a terminal, so a run whose stdin is `/dev/null` — what a script, a
  cron job and this session's harness give a process — armed the Enter stop,
  read EOF at once and interrupted ffmpeg before it created the WAV. Measured
  through the shipped tool: `clai -cm test -t audio_record q
  tool_audio_record </dev/null` failed in 0.44 s with `ffmpeg produced no
  readable wav: open …wav.part: no such file or directory`, and no file
  existed. A phase-4 e2e already worked around this by pointing `os.Stdin` at
  an empty regular file, which hid the user-facing case. `enterStop` now
  closes the channel only for a completed line; EOF leaves the recording to
  the silence timeout or `max-seconds`. After the fix the same command
  recorded for 2 s and returned the six facts. `TestEnterStop_EOFIsNotALine`
  and `TestRecordText_Enter/end of terminal stdin is not an Enter press` pin
  it; phase 4's e2e now runs over `/dev/null`. L3 records the new rule.

**Notes.**

- A real terminal where the user presses Ctrl+D at an empty prompt no longer
  stops the recording; the documented stop is Enter, and the silence timeout
  or `max-seconds` still bound the recording. Ctrl+C keeps its meaning.
- The read cannot be interrupted, so one goroutine stays blocked on a real
  terminal after a silence or duration stop, as L3 already records.

### Review 4, 2026-10-28 — root review

**Status: Complete — A01 fixed in the fix session (2026-10-28).**

**Findings**

- [x] **A01** (quality andon) —
  `TestRecordText_RecorderError/cancelled call` (`record_test.go:270-289`)
  starts the call and cancels 100 ms later (`record_test.go:284`). The cancel
  must land after discovery and inside the recording; when the host is loaded,
  discovery takes longer and the cancel lands in `Recorder.Record`'s `sources`
  call, which returns `ffmpeg -sources pulse failed: signal: killed` and fails
  the `ErrInterrupted` assertion. The fixed timer is the flake; the misleading
  error is A01's code half (phase 1). The test must wait for the `.part` file,
  then cancel. Full work list in the README row.

**Verified good.**

- **The six result lines and the one status line.** `recordingFacts` writes
  `recorded`, `started_at`, `ended_at`, `duration_seconds`,
  `stopped_because`, `sources`, in that order
  (`internal/audio/record.go:141-159`); the start line goes to stderr.
  `TestRecordText_Facts` and `TestRecordText_WritesNothingToStdout` pass.
- **The configuration cascade.** `resolveCaptureConfig` applies the call over
  the file and the file over `Default`, and refuses an unknown `sinks`, a
  negative duration or a positive `silence-peak-db` by field name before the
  recorder runs (`internal/audio/record.go:74-110`).
  `TestRecordText_Config` passes.
- **R3-01 still holds.** `enterStop` closes the stop channel only for a
  completed line (`internal/audio/record.go:124-138`), so `/dev/null` leaves
  the recording to the silence timeout or `max-seconds`.
  `TestEnterStop_EOFIsNotALine` and `TestRecordText_Enter` pass, and the
  phase-4 e2e runs over `/dev/null`.
- **The registration gate.** Both audio tool rows share
  `executable: "ffmpeg"` (`internal/tools/handler.go:83-84`), and
  `requireAudioToolGate` covers both (R1-09's one helper).

### Fix session, 2026-10-28 — A01 closed

`TestRecordText_RecorderError/cancelled call` no longer cancels on a 100 ms
timer. It records through the fixture's default live mode, waits for the
`.part` file with `cancelOnPartFile`, then cancels, so the assertion pins a
cancelled recording. A cancellation that lands before the recording starts
now carries the cancellation instead of ffmpeg's signal (phase 1's
`discoveryCancellation`), proven by `TestRecord_CancelDuringDiscovery`.

`go test ./internal/audio -race -run TestRecordText_RecorderError -count=20`
passes; the package passes the gate at 16.4 s and 88.9 % coverage.
