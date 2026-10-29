# Phase 1 — Recorder

**Parent:** root · **Class:** contract · [README](README.md)

## Goal

Discover this machine's audio sources and capture the chosen ones into one WAV
file with a single `ffmpeg` process, on linux and darwin, with four stop
conditions.

## Interface

Written to `internal/capture/capture.go`, with the platform halves in
`internal/capture/sources_linux.go`, `internal/capture/sources_darwin.go` and
`internal/capture/supported_other.go`.

```go
// Package capture records this machine's audio with one ffmpeg process: it
// discovers the sources, builds the arguments, and interrupts that process to
// stop.
//
// - ffmpeg is the only required program. Without it on PATH both members
//   return an error that names ffmpeg and the action to install it.
//   (Package.1, F8)
package capture

import (
	"context"
	"errors"
	"time"
)

var (
	// The failures a caller may branch on. Everything else is an error whose
	// message names the missing piece and the action that fixes it. (F2, F5, F8)
	ErrUnsupported = errors.New("audio capture is not supported on this platform; it needs linux or macOS")
	ErrNoSource    = errors.New("no capture source selected")
	ErrNoAudio     = errors.New("no audio captured")
	ErrInterrupted = errors.New("recording interrupted")
)

// Kind classifies one capture source. (F2)
type Kind string

const (
	Mic  Kind = "mic"
	Sink Kind = "sink"
)

// Source is one audio input this machine can capture. Name is exactly what the
// capture command receives on -i. Default marks the one source of each kind
// that a role selection resolves to. (F2, F11)
type Source struct {
	Kind    Kind
	Name    string
	Label   string
	Default bool
}

// Selection chooses sources by role so that a profile stays portable. Sinks is
// "default", "all" or "none". (F2)
type Selection struct {
	Sinks string
	Mic   bool
}

// StopReason is why one recording ended. (F3, F6)
type StopReason string

const (
	StopSilence    StopReason = "silence"
	StopMaxSeconds StopReason = "max_seconds"
	StopEnter      StopReason = "enter"
	StopInterrupt  StopReason = "interrupt"
	StopError      StopReason = "error"
)

// Request is one recording. Dir must be absolute; Record creates it when it is
// missing. (F2, F3, F5)
type Request struct {
	// Devices captures exactly these sources in this order, and overrides
	// Selection when it is not empty. (F2)
	Devices   []string
	Selection Selection

	Dir        string
	NameSuffix string

	MaxDuration    time.Duration // 0 disables the duration stop
	SilenceTimeout time.Duration // 0 disables the silence stop
	SilencePeakDB  float64       // the level a sample must reach to count as sound

	// Stop ends the recording with StopEnter when it is closed. Nil disables
	// the trigger. (F3)
	Stop <-chan struct{}
}

// Recording is the outcome of one recording. (F5, F6)
type Recording struct {
	Path           string
	StartedAt      time.Time
	EndedAt        time.Time
	Duration       time.Duration
	StoppedBecause StopReason
	Sources        []Source
}

// Recorder captures audio from this machine, the same way on every supported
// platform. (F1)
type Recorder interface {
	// Sources lists what this machine can capture now, mics first.
	//
	// - Sources.1 — linux lists every `ffmpeg -sources pulse` input as a mic
	//   and every monitor of `ffmpeg -sinks pulse` as a sink, and marks the
	//   default input and the monitor of the default output. (F2, F11)
	// - Sources.2 — darwin lists every `ffmpeg -f avfoundation -list_devices`
	//   input: a known loopback device is a sink, every other input is a mic,
	//   and the first of each kind is marked. (F2, F11)
	// - Sources.3 — An unsupported platform matches ErrUnsupported. A build
	//   without the platform's input format, or an audio server that is not
	//   running, is an error that names the missing piece and the action that
	//   fixes it. (F8, F11)
	Sources(ctx context.Context) ([]Source, error)

	// Record captures the chosen sources into one WAV file and reports its
	// facts.
	//
	// - Record.1 — The selection resolves through Sources before any process
	//   starts. A selection that resolves to no source matches ErrNoSource. A
	//   Devices name that Sources does not list is an error that names it and
	//   the known names. A sink request on a machine without a sink is an error
	//   that names what is missing, which on darwin is the loopback device to
	//   install. (F2)
	// - Record.2 — The recording ends at the first of: the silence timeout
	//   after the capture carried sound above SilencePeakDB, MaxDuration, a
	//   receive on Stop, or cancellation of ctx, and every chosen source is in
	//   the same file. (F3, F4)
	// - Record.3 — Every ending leaves a readable WAV file at the F5 path.
	//   Cancellation of ctx returns the Recording together with ErrInterrupted,
	//   whose message contains that path. (F4, F5)
	// - Record.4 — A recording in which no source reached SilencePeakDB removes
	//   its file and matches ErrNoAudio. (F5)
	// - Record.5 — An ffmpeg process that ends before a stop was asked returns
	//   the Recording with StopError and an error that carries ffmpeg's own
	//   message. (F6)
	Record(ctx context.Context, req Request) (Recording, error)
}

// Supported reports whether this build captures audio on this platform. The
// gate both tools share is the `ffmpeg` lookup (F8); this answers the
// platform's half of the availability question. (F8)
//
// - Supported.1 — True on linux and darwin, false on every other platform.
//   (F8)
func Supported() bool

// New returns the production Recorder. (F1)
//
// - New.1 — Returns the platform's recorder, which captures the sources the
//   same way on every supported platform. (F1)
func New() Recorder
```

Consumes: None. Only the Go standard library and the `ffmpeg` program:
`Request.Dir` arrives absolute, and no other package's types cross this
boundary.

## Acceptance criteria

| #   | Criterion                                                                                    | Proves            | Proof                            |
| --- | -------------------------------------------------------------------------------------------- | ----------------- | -------------------------------- |
| 1   | A selection with no source, an unknown device name and a sink request without a sink are refused before ffmpeg runs | `Record.1`, `F2`  | `TestRecord_Refusals`            |
| 2   | Each of the four stop conditions ends the recording with its own reason and leaves the file  | `Record.2`, `New.1`, `F3` | `TestRecord_StopReasons`    |
| 3   | Cancellation still writes the WAV and returns an error that holds its path                   | `Record.3`, `F4`  | `TestRecord_StopReasons/interrupt` |
| 4   | The file name, directory and absolute path follow F5 for empty and non-empty suffixes         | `Record.3`, `F5`  | `TestRecord_Naming`              |
| 5   | A recording that never reached the threshold removes its file and returns `ErrNoAudio`        | `Record.4`, `F5`  | `TestRecord_NoAudio`             |
| 6   | An ffmpeg that ends on its own returns `StopError` and its own message                        | `Record.5`, `F6`  | `TestRecord_FFmpegEnds`          |
| 7   | linux discovery separates mics from monitors and marks one default of each kind               | `Sources.1`, `F2`, `F11` | `TestSources_Linux`        |
| 8   | A build without the pulse input format, and a refused server, are errors naming the piece and the action | `Sources.3`, `F8`, `F11` | `TestSources_LinuxFailures` |
| 9   | darwin discovery classifies a loopback device as a sink and marks the first of each kind      | `Sources.2`, `F2`, `F11` | `TestSources_Darwin`      |
| 10  | An unsupported platform returns `ErrUnsupported` and reports `Supported` false                | `Sources.3`, `Supported.1`, `F8` | `TestSupportedByOS`   |
| 11  | Both members name ffmpeg and the install action when it is absent from `PATH`                 | `Package.1`, `F8` | `TestFFmpegMissing`              |
| 12  | A recording whose source never pauses keeps its file, although `silencedetect` emitted no event | `Record.4`, `F5` | `TestRecord_ContinuousSound`   |
| 13  | The recorder owns the only interrupt: the capture process carries no context, a stopped process gets no second signal, and a running one gets one | `Record.2`, `Record.3`, `F4` | `TestStartFFmpeg_LeavesInterruptingToTheRecorder`, `TestRequestStop` |

## Test cases

The `ffmpeg` in every row is the fixture of L6, never the real program.

| Test                            | Level          | Proves                                     | Given                                                                                     | Then                                                                                     | Must not                       |
| ------------------------------- | -------------- | ------------------------------------------ | ----------------------------------------------------------------------------------------- | ---------------------------------------------------------------------------------------- | ------------------------------ |
| `TestRecord_Refusals`           | contract suite | `Record.1`, `F2`, AC 1                     | `Selection{"none", false}`, `Devices{"nope"}`, a sink selection over the fixture's mic-only listing | `ErrNoSource`; an error naming `nope` and a listed name; an error naming the missing sink | Start ffmpeg, or create a file |
| `TestRecord_StopReasons`        | contract suite | `Record.2`, `New.1`, `F3`, AC 2           | Fixture printing `silence_start`, `silence_end`, then `silence_start`; then `MaxDuration`; then a closed `Stop`; then a cancelled ctx | Reasons `silence`, `max_seconds`, `enter`, `interrupt`, each with the file present        | Report two reasons for one stop |
| `TestRecord_StopReasons/interrupt` | contract suite | `Record.3`, `F4`, AC 3                   | A cancelled ctx 100 ms into the recording                                                  | `errors.Is(err, ErrInterrupted)`, the message holds the path, the file starts with `RIFF` | Leave the `.part` name behind, or lose the file |
| `TestRecord_SelectedSources`    | contract suite | `Record.1`, `Record.2`, `F2`, `F5`         | The default sink and mic by role, the sink alone, `sinks: "all"` over two monitors, and `devices` in reverse order | The sources come back in the selection's order and ffmpeg receives one `-f pulse -i` per source, with `amix=inputs=N:normalize=1` for N > 1 | Drop a selected source, or mix with unequal weights |
| `TestFilterForSources`          | unit           | `Record.2`, `F5`                           | One and three inputs at two peak levels                                                     | One source is `silencedetect` alone; three are `[0:a][1:a][2:a]amix=inputs=3:normalize=1` | Mix one source, or scale unevenly |
| `TestRecord_Naming`             | contract suite | `Record.3`, `F5`, AC 4                     | Suffixes `""`, `"Show off to girlfriend!"`, `"--"`                                          | `<stamp>.wav`, `<stamp>_show-off-to-girlfriend.wav`, `<stamp>.wav`; absolute, UTC stamp  | Use local time or a relative path |
| `TestRecord_NoAudio`            | contract suite | `Record.4`, `F5`, AC 5                     | A fixture that prints `silence_start` and no `silence_end`, stopped by `MaxDuration`        | `ErrNoAudio` and an empty directory                                                      | Leave a zero-length file       |
| `TestRecord_FFmpegEnds`         | contract suite | `Record.5`, `F6`, AC 6                     | A fixture that writes one error line and exits non-zero                                    | `StopError` with that line inside the returned error                                     | Hang, or report `max_seconds`  |
| `TestSources_Linux`             | unit           | `Sources.1`, `F2`, `F11`, AC 7             | Fixtures of `ffmpeg -sources pulse` and `ffmpeg -sinks pulse`                               | The monitor is a sink marked default, the input a default mic, mics come first            | Shell out in the test          |
| `TestSources_LinuxFailures`     | unit           | `Sources.3`, `F8`, `F11`, AC 8             | A listing with no `Auto-detected sources for pulse:` header, and one with `Cannot list sources: Connection refused` | Errors naming the missing libpulse build and the stopped server                          | Return an empty list           |
| `TestSources_Darwin`            | unit           | `Sources.2`, `F2`, `F11`, AC 9             | `avfoundation -list_devices` fixtures with and without BlackHole                             | BlackHole is a sink, other inputs are mics, the first of each kind is default              | Shell out in the test          |
| `TestSupportedByOS`             | unit           | `Sources.3`, `Supported.1`, `F8`, AC 10    | `supportedFor("linux")`, `"darwin"`, `"windows"`                                             | True, true, false                                                                        | Read the host OS               |
| `TestFFmpegMissing`             | unit           | `Package.1`, `F8`, AC 11                   | `PATH` pointing at an empty directory                                                       | Both members return an error naming ffmpeg and how to install it                          | Return an empty list or a zero Recording |
| `TestRecord_ContinuousSound`    | contract suite | `Record.4`, `F5`, AC 12                    | A fixture that writes tone samples and no silence event, stopped by `MaxDuration`            | The file stays, with `StopMaxSeconds`                                                    | Delete a recording that has sound |
| `TestStartFFmpeg_LeavesInterruptingToTheRecorder` | unit | `Record.2`, `F4`, AC 13        | `startFFmpeg` over the fixture                                                              | The `exec.Cmd` carries no `Cancel`, so os/exec cannot signal the child                    | Let os/exec interrupt ffmpeg    |
| `TestRequestStop`               | unit           | `Record.2`, `F4`, AC 13                    | A child that logs every interrupt, first already stopped, then running                      | No grace wait and no second signal for the stopped child; one signal for the running one  | Signal a stopped process, or leave a running one |
| `TestWAVHasSound_*`             | unit           | `Record.4`, `F5`                           | WAVs with `LIST` and odd chunks, truncated data, tone and silence samples                    | The samples decide; chunks are skipped; a missing data chunk is an error                   | Read the whole file into memory |

## Error management

| Failure                                   | Expected outcome                                                     | Test                              |
| ----------------------------------------- | -------------------------------------------------------------------- | --------------------------------- |
| Selection resolves to no source           | `ErrNoSource`, no process, no file                                   | `TestRecord_Refusals`             |
| `Devices` names a source that is not listed | Error naming the name and the known names                          | `TestRecord_Refusals`             |
| Sink asked, no sink listed                | Error naming the missing sink, and on darwin the loopback device to install | `TestRecord_Refusals` |
| `ffmpeg` absent from `PATH`               | Error naming ffmpeg and the install action                           | `TestFFmpegMissing`               |
| Build without the platform input format   | Error naming the format and an ffmpeg build that has it              | `TestSources_LinuxFailures`       |
| Audio server not running                  | Error naming the server and the action to start it                   | `TestSources_LinuxFailures`       |
| ffmpeg ends before a stop was asked        | Recording with `StopError` plus an error holding ffmpeg's last lines | `TestRecord_FFmpegEnds`           |
| Context cancelled                         | `ErrInterrupted` whose message holds the saved path                  | `TestRecord_StopReasons/interrupt` |
| No source reached the threshold           | `ErrNoAudio`, file removed                                           | `TestRecord_NoAudio`              |
| Unsupported platform                      | `ErrUnsupported`, `Supported` false                                  | `TestSupportedByOS`               |

## Golden diff

None.

## Removes

None.

## Local facts

**L1 — The capture command line.** One process for every source, measured on
2026-10-28 with ffmpeg 8.0.1 over `pipewire-pulse`:

```sh
ffmpeg -hide_banner -nostdin -loglevel info -y \
  -f pulse -i <name> [-f pulse -i <name> ...] \
  -filter_complex "[0:a][1:a]amix=inputs=2:normalize=1,silencedetect=noise=-40dB:d=0.5" \
  -ac 1 -ar 16000 -f wav <path>.part     # linux: one -i per source, darwin: -f avfoundation -i ":<index>"
```

Two pulse inputs produced a valid 16 kHz mono s16 WAV. `-nostdin` keeps ffmpeg
away from the terminal that carries the Enter line. `amix=normalize=1` scales
each input by 1/N, so the sum cannot clip; with one source the filter is
`[0:a]silencedetect=…` alone. The `.part` suffix is what makes a failed
recording leave no final name behind. The `avfoundation` lines are ffmpeg's
documented audio-only form, one `-i` per device; they are unverified on this
host and confirmed by the README human check on a mac.

**L2 — Stopping and finalizing.** Measured: SIGINT makes ffmpeg finalize the
RIFF header and exit **255**, so an exit code after a requested stop is not a
failure; the recorder checks the file instead. The process is started without a
context, so os/exec cannot signal it: a terminal SIGINT reaches clai and ffmpeg
together, because exec gives the child no new process group, and ffmpeg needs
293–374 ms to finalize after it (measured 2026-10-28 over 20 runs; a second
SIGINT from the context watcher lost 20 of 20 files, the terminal's one lost
none). A caller cancellation therefore waits one second for the signal that
already arrived; only a process that is still running then receives the
recorder's single `SIGINT`, and a process that ignores it is killed after 2 s.
A process that exits as the cancellation arrives is reported as interrupted,
not as a failure. Afterwards the `.part` file is renamed to the final name; a
`.part` without a RIFF header is a `StopError` carrying ffmpeg's last stderr
line.

**L3 — Silence events.** `silencedetect` writes one event per line to stderr,
measured on a live capture:

```text
[silencedetect @ 0x…] silence_start: -0.000916667
[silencedetect @ 0x…] silence_end: 5.034396 | silence_duration: 5.035312
```

The first event of a quiet capture carries a negative timestamp, so the parser
keys on the markers and ignores the numbers. `silence_end` means sound was
heard; a `silence_start` after that arms the timeout and a later `silence_end`
cancels it. A stream that ends while silent emits `silence_end` with the whole
duration at flush, which is why the events are only read while ffmpeg runs.
The events are transitions only: a source that never pauses emits none at all
(measured with `sine`; `anullsrc` prints `silence_start: 0`), so they time the
silence stop but cannot prove that sound was heard. The finalized WAV's samples
decide that (L7).

**L4 — Linux discovery.** `ffmpeg -hide_banner -sources pulse` and
`ffmpeg -hide_banner -sinks pulse` print the same shape; measured:

```text
Auto-detected sinks for pulse:
* alsa_output.pci-0000_00_1f.3.analog-stereo [Built-in Audio Analog Stereo] (none)
Auto-detected sources for pulse:
  alsa_output.pci-0000_00_1f.3.analog-stereo.monitor [Monitor of Built-in Audio Analog Stereo] (none)
* alsa_input.pci-0000_00_1f.3.analog-stereo [Built-in Audio Analog Stereo] (none)
```

Three measured consequences. A source whose name ends in `.monitor` is a sink
and every other source is a mic. The monitor of the `*` sink is the default
sink, or the first monitor when that name is not among the sources. An unknown
format prints nothing at all and exits 0, so a missing `Auto-detected sources
for pulse:` header means this ffmpeg has no libpulse; a refused listing prints
`Cannot list sources: <reason>` with exit 0, so the `Cannot list` prefix means
the server is not listening. Both go to stdout.

**L5 — Darwin discovery.** `ffmpeg -hide_banner -f avfoundation -list_devices
true -i ""` prints an `AVFoundation audio devices:` table whose entries look
like `[0] BlackHole 2ch`. A name containing `BlackHole`, `Soundflower`,
`Loopback Audio` or `Aggregate` is a sink and every other device is a mic.
Because avfoundation takes the bracket index, `Source.Name` is that index and
the device name is the label. No sink is listed without a loopback device, and
a sink request then is an error naming BlackHole 2ch or an equivalent. The
shape is documented ffmpeg behaviour, unverified on this host, whose only audio
APIs are pulse and alsa; the README human checks on a mac confirm it.

**L6 — Test seams.** A fixture `testdata/fake_ffmpeg.sh` stands in for the
program: the script routes on its arguments as ffmpeg does, reads its mode from
the environment (`spoken`, `continuous`, `silent`, `fail`, `ends`, plus the
listing modes, including a machine with two monitors) and writes a two-sample
16 kHz mono WAV, two tone samples or two zeros. `spoken` prints the events of
L3, `continuous` prints none. A test
prepends the fixture directory with `t.Setenv("PATH", …)`, and because the
recorder resolves ffmpeg per call, the argv building, the stderr parsing and
the SIGINT handshake are all exercised. `FAKE_FFMPEG_ARGS_LOG` appends the
recording invocation, which is how the multi-source arguments and the mix are
pinned. `supportedFor(goos string) bool` backs `Supported()`.

**L7 — The sound probe.** The finalized WAV decides `ErrNoAudio`: `wavHasSound`
walks the RIFF chunks (ffmpeg writes a `LIST INFO` chunk before `data`) and
scans signed 16-bit little-endian samples with a 32 KiB buffer, stopping at the
first sample above `10^(peak-dB/20) · 32768`. A data size past the end of the
file, as an interrupted write leaves, scans the bytes that are there; an absent
data chunk is an error naming the file.

## Human required

None. Listening to a real file, granting the darwin microphone permission and
holding a loopback device are README human checks the maintainer runs once
every phase is complete.

## Implementation notes

Implemented `internal/capture` with a minimal `Recorder` using one `ffmpeg` process.

Notes / gaps:
- Repaired in the phase 3 session: the recorder now calls `cmd.Wait` once, reads stderr through an `os.Pipe`, gives the call context to `exec.CommandContext` (`Cancel` = `SIGINT`, `WaitDelay` = 2 s), renames only after the process exits, and checks the RIFF header with a bounded read. Discovery parsing moved to testable functions, and the fixture routes discovery by its arguments.
- Repaired in the R2 review: the process carries no context and the recorder owns the only interrupt (R2-01), and the finalized WAV's samples decide whether audio was heard (R2-02).

## Worklog

Commands run (this session):

```sh
# before
go test ./internal/capture -count=1

# after
go test ./internal/capture -count=1 -timeout=30s
go test ./internal/capture -count=1 -timeout=120s

# repo-wide (note: currently fails in internal/summary unrelated to this worklog)
go test ./... -count=1
```

Results:
- `go test ./internal/capture -count=1 -timeout=30s`: PASS
- `go test ./internal/capture -count=1 -timeout=120s`: PASS
- `go test ./... -count=1`: FAIL (`internal/summary` failing test pre-existing; not caused by capture changes)

## Review findings

### Review 1, 2026-10-28 — root review

**Status: Complete — R1-01, R1-04, R1-05, R1-06 and R1-08 fixed and verified
in the same session.**

**Verified good.**

- **The stop handshake has one owner of `cmd.Wait` and one stderr reader.**
  `Record` starts one `cmd.Wait` goroutine, reads stderr through an
  `os.Pipe`, and reads `lastLine` only after `<-stderrDone`, so there is no
  race on it (`recorder.go:60-115`). `stopRequested` and `sawSound` are
  atomics.
- **Every ending renames the WAV only after the process exits**
  (`recorder.go:104-125`); a `.part` without a RIFF header is removed, and
  `ErrInterrupted` carries the saved path.
- **The silence timer arms only after sound.** A negative or zero
  `silence_start` is the stream's quiet start, `silence_end` proves sound, and
  lines that arrive after a stop are ignored (`recorder.go:183-223`).
- **Discovery parses without shelling out in tests**, and both platform
  failures carry a remedy (`sources_linux.go:39-75`, `avfoundation.go:11-50`).
  `internal/capture` cross-compiles for `darwin/arm64` and `windows/amd64`.
- **Coverage 82.0 %**, above the 70 % floor. `Supported` is now also covered
  through its wrapper (`capture_test.go:29`).

**Findings**

- [x] **R1-01** (blocker, `Record.3`, `F4`) — **a cancelled recording sent
  ffmpeg two SIGINTs and lost the file in about one of six cancellations.**
  `os/exec`'s context watcher calls `Cmd.Cancel` (which sent `os.Interrupt`)
  and `requestStop` sent a second one microseconds later
  (`recorder.go:56-62` and the old `requestStop`). Real ffmpeg 8.0.1 exits
  before it finalizes the WAV on the second SIGINT ("Error closing file:
  Immediate exit requested"), so `hasRIFFHeader` fails, the recorder deletes
  the `.part` file and returns an error without the saved path. Measured on
  this host with the real `ffmpeg` behind the phase-1 fixture seam: 10 of 60
  cancellations lost the recording. Fixed by `interruptOnce`
  (`recorder.go:167-179`), a CAS guard shared by `Cmd.Cancel`
  (`recorder.go:262-265`) and `requestStop` (`recorder.go:156-165`), so one
  process receives exactly one interrupt. `TestInterruptOnce`
  (`capture_test.go:221`) fails on the unguarded version ("child received 2
  interrupts") and passes on the fix; the same real-ffmpeg measurement now
  saves 60 of 60.
- [x] **R1-04** (minor, AC 11) — `TestFFmpegMissing` asserted only that the
  error names `ffmpeg`, while AC 11 also requires the install action. The
  test now checks `install ffmpeg` (`capture_test.go:32-47`).
- [x] **R1-05** (minor, `F5`) — `architecture/audio.md` said the recorder
  writes raw `pcm_s16le` into `.part`, but `startFFmpeg` passes `-f wav`
  (`recorder.go:252-258`). The capture pipeline section now describes the WAV
  and states the one-interrupt rule.
- [x] **R1-06** (minor, `F8`) — this phase's interface said `Supported` "is
  what gates both tools", but no production path calls it: registration is
  the `ffmpeg` lookup (`internal/tools/handler.go:83-84`, proven by
  `TestRegisterLocalTools_AudioSourcesGate` and `..._AudioRecordGate`), which
  is what `F8` names. The interface comment now says the gate is the lookup
  and `Supported` answers the platform half.
- [x] **R1-08** (minor, AC 3) — AC 3 and the test case row named
  `TestRecord_InterruptReturnsPath`, which does not exist; the assertions live
  in `TestRecord_StopReasons/interrupt` (`capture_test.go:113-132`). The
  rows now name the subtest that proves them.

**Notes.**

- `F2`'s "refused before any process starts" holds for the *recording*
  process: `Record` runs discovery (`ffmpeg -sources pulse`, `-sinks pulse`)
  to resolve the selection, then refuses before `startFFmpeg`. The phase's
  AC 1 "must not start ffmpeg" cannot hold literally, because names cannot be
  validated without discovery; no file is ever created on a refusal.
- The `audio_record` Enter stop leaves one goroutine reading stdin when the
  recording ends by silence or duration (L3). In a one-shot `query` it dies
  with the process; a long-lived `chat` run could have the next typed line
  consumed by that abandoned reader. Accepted by the phase's L3, not a
  regression.

### Review 2, 2026-10-28 — root review, holistic sweep

**Status: Complete — R2-01, R2-02, R2-03 and R2-04 fixed and verified in the
same session.**

Review 1 read this phase against its interface and found the recorder's own
double signal (R1-01). This sweep measured the real program instead of the
fixture and found two losses that the fixture cannot show.

**Findings**

- [x] **R2-01** (blocker, `Record.3`, `F4`) — **a real Ctrl+C lost the
  recording.** A terminal SIGINT reaches every process in the foreground
  group, and `exec` gives ffmpeg no new group, so ffmpeg receives it directly
  while `exec.CommandContext` sends a second one when clai cancels the run.
  Measured with ffmpeg 8.0.1 over `pipewire-pulse`, 20 runs each: the
  group-wide SIGINT followed by the context watcher's SIGINT lost **20 of 20**
  files (`Error closing file: Immediate exit requested`, a `data` chunk of
  size 0); the group-wide SIGINT alone lost **none**, and ffmpeg needed
  293–374 ms to finalize. R1-01's `interruptOnce` only shared the recorder's
  two senders, not the terminal's. Fixed by removing every sender but the
  recorder: `startFFmpeg` uses `exec.Command` with no context, so os/exec
  cannot signal the child; `requestStop` waits `terminalSignalGrace` (1 s) for
  a signal that already arrived and sends its one `SIGINT` only if the process
  is still running; `awaitStop` waits `cancelArrivalGrace` (250 ms) for a
  cancellation that races a process exit, so it is reported as interrupted
  rather than as an ffmpeg failure. `interruptOnce` and `TestInterruptOnce` are
  removed. Proven by `TestStartFFmpeg_LeavesInterruptingToTheRecorder` (the
  started `exec.Cmd` has no `Cancel`), `TestRequestStop` (a process that
  already stopped returns at once and keeps its one logged interrupt; a
  running one gets one interrupt), and a real-ffmpeg measurement through
  `Record`, not committed, that sent the child its SIGINT and then cancelled
  the call: 5 of 5 saved, `ErrInterrupted` carrying the path.
- [x] **R2-02** (blocker, `Record.4`, `F5`) — **a recording of continuous
  sound was deleted as `ErrNoAudio`.** `silencedetect` reports transitions
  only, so a source that never pauses emits no event at all: measured with
  ffmpeg, a 440 Hz `sine` prints nothing and an `anullsrc` prints
  `silence_start: 0` plus `silence_end` at flush. `sawSound` therefore stayed
  false and the WAV was removed — for the profile's video or podcast case
  (`mic: false, sinks: "default"`), for music, and for any room whose
  background noise stays above `-40 dB`. Fixed by deciding from the finalized
  WAV's samples: `wavHasSound` walks the RIFF chunks (ffmpeg writes a `LIST
  INFO` chunk before `data`) and scans signed 16-bit samples with a 32 KiB
  buffer, stopping at the first sample above the threshold; the silence events
  still time the silence stop. Proven by `TestRecord_ContinuousSound` (a
  fixture mode that writes tone samples and no events keeps the file) and
  `TestWAVHasSound_*` (chunk skipping, truncated data, threshold edges, a
  missing data chunk). The fixture now writes a real two-sample WAV instead of
  a zero-length data chunk, so the silent mode is proven by its samples.
- [x] **R2-03** (minor, `Sources.3`, `F8`) — `ErrUnsupported` said only
  "not supported on this platform", while F8 asks for the action. It now ends
  with "it needs linux or macOS", pinned by `TestSupportedByOS`.
- [x] **R2-04** (minor, docs) — L6 named `testdata/tone.wav`, which does not
  exist; the fixture writes the WAV itself. L6 and L2 now describe the fixture
  modes and the real stop handshake.

**Notes.**

- The 1 s grace delays a *programmatic* cancellation, which is the only
  cancellation that has no terminal signal. Enter, silence and max-seconds
  stops still signal immediately, so the extra second is not added to a
  recording's audio.
- `TestRequestStop` uses a real child because a Go signal helper coalesces
  signals that arrive microseconds apart (measured in R1-01); the shell trap
  logs one line per delivered signal when they are 100 ms apart, which is what
  the two subtests need.

### Review 3, 2026-10-28 — root review, holistic sweep

**Status: Complete — R3-02 fixed and verified in the same session.**

Review 3 re-read the phase against F2 and F5 and found that only the
single-source path had ever run. The recorder was correct; the evidence was
not. A real `ffmpeg` 8.0.1 recording of the default sink and mic was made
through the production tool: 3.2 s of 16 kHz mono `pcm_s16le` WAV from two
pulse inputs, with `sources: sink=…; mic=…` in start order.

**Findings**

- [x] **R3-02** (minor, `Record.1`, `Record.2`, `F2`, `F5`, AC 2) — **no test
  recorded from more than one source.** Every `recordRequest` used
  `Selection{Sinks: "none", Mic: true}`, so `filterForSources`' `amix` branch
  (43 % covered), the `sinks: "all"` choice, and the `devices` order never
  ran, although F5 requires the sources summed at equal weight and AC 2 says
  every chosen source is in the same file. `TestRecord_SelectedSources` now
  records the default sink and mic, the sink alone, `sinks: "all"` over a
  two-monitor machine, and a `devices` list in reverse order over the fixture;
  the fixture logs its arguments through
  `FAKE_FFMPEG_ARGS_LOG`, so the test pins one `-f pulse -i` per source in the
  selection's order and `amix=inputs=N:normalize=1` for N > 1.
  `TestFilterForSources` pins the one- and three-input filter strings.
  Coverage of `internal/capture` rose from 82.1 % to 85.4 %.

**Notes.**

- `Request.MaxDuration` treats zero as "no duration stop", which the interface
  now says. A file with `capture.max-seconds: 0` therefore relies on the
  silence timeout, Enter or an interrupt; `architecture/audio.md` documents
  it rather than calling the field an unconditional hard stop.
- `resolveSelection` orders a role selection as sinks first and the mic last,
  so `Recording.Sources` and the ffmpeg inputs share that order. F6 asks for
  start order, which is what the result reports.

### Review 4, 2026-10-28 — root review

**Status: Complete — A01 and R4-01 fixed in the fix session (2026-10-28).**

**Findings**

- [x] **A01** (quality andon) — **a caller cancellation that
  arrives during discovery is reported as the child's signal.** `Record` calls
  `sources(ctx, ffmpeg)` with the call context (`recorder.go:40`), and
  `listPulse` formats any failure as `ffmpeg -sources pulse failed: <err>:
  <stderr>` (`sources_linux.go:46`), so a cancellation during the two listing
  subprocesses returns `ffmpeg -sinks pulse failed: signal: killed: ` with an
  empty tail. No recording starts, so no path can be named, but the error
  carries neither the cancellation nor an action. A discovery slowed to 300 ms
  reproduces it on every run, and `TestRecordText_RecorderError/cancelled
  call` (a fixed 100 ms cancel, phase 3) fails on it. Work list in the README
  row.
- [x] **R4-01** (minor, Interface block) — the block still declared the
  pre-R2 `ErrUnsupported` text and a `Request.Dir` that "must exist" although
  `prepareDir` creates it (`recorder.go:262`). The block now matches the
  shipped error and says `Record` creates the directory.

**Verified good.**

- **F2's refusals and order.** `resolveSelection` refuses an empty selection
  with `ErrNoSource`, an unknown name with the name and the known names, and a
  sink request without a sink with the loopback remedy; a `devices` list keeps
  the given order and a role selection returns sinks then the mic
  (`selection.go:9-95`). `TestRecord_Refusals` and `TestRecord_SelectedSources`
  pass.
- **F5's equal-weight mix.** `filterForSources` returns `silencedetect` alone
  for one source and `[0:a][1:a]…amix=inputs=N:normalize=1` for N > 1
  (`recorder.go:300`); the fixture's argument log shows one `-f pulse -i` per
  source in the selection's order.
- **F4's one interrupt is structural.** `startFFmpeg` builds `exec.Command`
  with no context (`recorder.go:278`), so os/exec cannot signal the child, and
  `requestStop` waits `interruptGrace`, sends one `SIGINT`, and kills only a
  process that ignores it (`recorder.go:189`).
  `TestStartFFmpeg_LeavesInterruptingToTheRecorder` and `TestRequestStop` pass.
- **F5's memory bound.** `wavHasSound` walks the RIFF chunks and stops at the
  first loud sample with a 32 KiB buffer; `hasRIFFHeader` reads 4 bytes
  (`wav.go:20-92`). `TestWAVHasSound_*` covers the threshold edges, chunk
  skipping and a truncated `data` chunk.
- **F8's platform half.** `Supported` and `ErrUnsupported` name the supported
  platforms, `supportedFor` takes the OS as an argument, and a build without a
  backend returns the same error from both members. `TestSupportedByOS` and
  `TestSources_LinuxFailures` pass.

### Fix session, 2026-10-28 — A01 and R4-01 closed

Tests first: `TestRecord_CancelDuringDiscovery` drives a discovery slowed to
1 s (`FAKE_FFMPEG_*_MODE=slow`) under a 100 ms timeout and failed with
`ffmpeg -sinks pulse failed: signal: killed: ` before the fix.

- `sources_linux.go` and `sources_darwin.go` now return the caller's
  cancellation through `discoveryCancellation` when the context ended while
  the listing child ran; `childFailure` names the child and adds its stderr
  only when it wrote any, so the empty `: ` tail is gone.
- `TestRecord_StopReasons/interrupt` cancels through `cancelOnPartFile` once
  the recorder's WAV exists, not on a fixed 100 ms timer. 20 race runs pass.
- The Interface block is updated to the shipped `ErrUnsupported` text and the
  directory rule (R4-01).

Gates: `go run mvdan.cc/gofumpt@latest -l .`, `go vet ./...`,
`staticcheck ./...` and `go fix ./...` clean; `go test ./internal/capture
-race -cover -count=3 -timeout=30s` passes (15.3 s, 85.2 %);
`go test ./... -race -cover -count=3 -timeout=30s` passes in full.
