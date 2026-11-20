# Phase 3 — Clip inspector

**Parent:** root · **Class:** contract · [README](README.md)

## Goal

Cut a clip from an audio file, measure whether it is good enough to name a person by (length, speech
share, clipping, loudness), and hand back the measurement together with the clip as 16 kHz mono FLAC,
or refuse it with what failed.

## Interface

Written to `internal/audio/voices/inspect.go`.

```go
package voices

import (
	"context"
	"time"
)

// Runner runs the external programs this package needs. It is declared here
// so that voices imports nothing from internal/audio; audio.ExecRunner
// satisfies it as it stands. (F9)
type Runner interface {
	LookPath(name string) (string, error)
	Run(ctx context.Context, name string, args ...string) (stdout, stderr string, err error)
}

// Cut is the part of a file to inspect, in time from the file's start. The
// zero From is the start of the file and the zero To is its end, so the zero
// Cut is the whole file.
type Cut struct {
	From time.Duration
	To   time.Duration
}

// Inspection is the measurement of one accepted clip, and the clip.
type Inspection struct {
	// FLAC is the clip, 16 kHz mono FLAC, as it will be stored.
	FLAC []byte
	// Seconds is the clip's length.
	Seconds float64
	// SpeechShare is the part of the clip that is speech, 0 to 1. (L3.1)
	SpeechShare float64
	// ClippedRun is the longest run of consecutive samples at full scale.
	// (L3.2)
	ClippedRun int
	// MeanDBFS is the clip's mean volume. (L3.3)
	MeanDBFS float64
}

// QualityCheck names one thing an inspection measures.
type QualityCheck string

const (
	CheckLength   QualityCheck = "length"
	CheckSpeech   QualityCheck = "speech"
	CheckClipping QualityCheck = "clipping"
	CheckLoudness QualityCheck = "loudness"
)

// ClipQualityError refuses a clip. Failed names every check that failed, in
// the order length, speech, clipping, loudness; the other fields are what was
// measured.
type ClipQualityError struct {
	Failed      []QualityCheck
	Seconds     float64
	SpeechShare float64
	ClippedRun  int
	MeanDBFS    float64
}

func (e *ClipQualityError) Error() string

// CutError refuses a cut that selects nothing from the file: a negative
// bound, a To at or before From, or a bound past the file's end. Length is
// the file's length.
type CutError struct {
	Cut    Cut
	Length time.Duration
}

func (e *CutError) Error() string

// MissingToolError says a program the inspection needs is not installed.
type MissingToolError struct{ Tool string }

func (e *MissingToolError) Error() string

// SourceError says the file could not be read as audio. Detail is the
// program's own last message, never audio.
type SourceError struct {
	File   string
	Detail string
	Err    error
}

func (e *SourceError) Error() string
func (e *SourceError) Unwrap() error

// Inspector measures a clip when it is added.
type Inspector interface {
	// Inspect cuts cut from file, measures it, and returns the measurement
	// with the clip as 16 kHz mono FLAC.
	//
	// - Inspect.1 — The clip is the file between cut.From and cut.To, to the
	//   sample; the zero Cut takes the whole file, so a pre-sliced clip needs
	//   no bound. Bounds are not rounded: 61.2 s to 69 s is a 7.8 s clip.
	// - Inspect.2 — An accepted clip is returned as 16 kHz mono FLAC,
	//   whatever the file's format, rate and channel count, with Seconds
	//   equal to its length.
	// - Inspect.3 — A file that is already 16 kHz mono FLAC, inspected with
	//   the zero Cut, is returned byte for byte, so a clip moved between
	//   people keeps its id.
	// - Inspect.4 — A clip shorter than 3 s or longer than 10 s (the clip
	//   length parameter) fails CheckLength. The message of a refusal that
	//   failed the length reads "clip too short: 1.5 s, a clip is 3–10 s",
	//   or "clip too long: 43.0 s, a clip is 3–10 s".
	// - Inspect.5 — A clip whose speech share is under L3.1 fails
	//   CheckSpeech.
	// - Inspect.6 — A clip with a run of full-scale samples as long as L3.2
	//   forbids fails CheckClipping.
	// - Inspect.7 — A clip whose mean volume is under L3.3 fails
	//   CheckLoudness.
	// - Inspect.8 — A clip that fails any check is a *ClipQualityError that
	//   names every failed check and carries the four measurements, and no
	//   Inspection. A clip exactly on a limit passes it.
	// - Inspect.9 — A cut that selects nothing from the file is a *CutError
	//   holding the cut and the file's length.
	// - Inspect.10 — The only programs run are ffmpeg and ffprobe, through
	//   the Runner. One of them that is needed and does not resolve is a
	//   *MissingToolError naming it, before anything else is run. (F9)
	// - Inspect.11 — A file that is missing, is not audio, or that the
	//   program fails on is a *SourceError holding the file's path and the
	//   program's last message.
	// - Inspect.12 — A cancelled ctx ends the inspection with an error that
	//   matches ctx.Err().
	// - Inspect.13 — However it ends, the inspection leaves no file behind,
	//   changes neither the source file nor anything under voices, and no
	//   error it returns carries audio. (F8)
	Inspect(ctx context.Context, file string, cut Cut) (Inspection, error)
}

// NewInspector returns the Inspector that runs its programs through runner.
//
// - NewInspector.1 — The Inspector holds no state besides the runner: calls
//   do not affect each other and may run at once.
func NewInspector(runner Runner) Inspector
```

`voicestest.Runner`, written to `internal/audio/voices/voicestest/runner.go`, is `Inspector`'s test
support, not a second interface of this phase. The phase implements `Inspector`; the double of its
`Runner` is exported only so that the tests of phases 7–9 can run the real Inspector,
`voices.NewInspector(voicestest.NewRunner())`, without ffmpeg, and never script an Inspector of their
own. Its clauses say what those tests may rely on, and the Inspector's own suite, run over it, proves
them (L3.4).

```go
// Package voicestest is the test support of package voices: a Runner that
// answers the Inspector's ffmpeg and ffprobe calls from scripted audio, so a
// test needs neither program.
//
// - Package.1 — The package imports nothing from internal/audio, and no
//   non-test file of the repository imports it. (F9)
package voicestest

import "context"

// SampleRate is the rate of every sample slice in this package: 16 kHz, mono,
// signed 16 bit.
const SampleRate = 16000

// Call is one program run the Runner was asked for.
type Call struct {
	Name string
	Args []string
}

// Runner satisfies voices.Runner without running anything. The zero script
// resolves ffmpeg and ffprobe and knows no file. It is safe for concurrent
// use.
type Runner struct{ /* unexported */ }

// NewRunner returns a Runner with the zero script.
func NewRunner() *Runner

// LookPath resolves a program.
//
// - LookPath.1 — ffmpeg and ffprobe resolve unless Missing named them; any
//   other name, and a name Missing named, is an error.
func (r *Runner) LookPath(name string) (string, error)

// Run answers one call the Inspector of NewInspector makes.
//
// - Run.1 — A call that reads a file given to Serve is answered as the
//   programs would answer for audio that decodes to those samples: the
//   Inspector measures exactly them, cut to the sample.
// - Run.2 — A call that encodes a clip yields bytes that begin with a FLAC
//   header declaring 16 000 Hz and one channel. Equal samples give equal
//   bytes and different samples different bytes, so clip ids behave as with
//   the real encoder.
// - Run.3 — A file on disk that holds bytes Run.2 produced is read as its
//   samples without any scripting, and counts as 16 kHz mono FLAC, so a
//   stored clip can be inspected again (a trim, a move).
// - Run.4 — A call that reads a file given to Fail returns a non-nil error
//   with the scripted message on stderr. A file that is neither scripted nor
//   such a clip fails the way ffmpeg does for a missing file.
// - Run.5 — A call that reads a file given to Hang returns only when ctx
//   ends, with ctx's error.
// - Run.6 — A program Missing named, and any program other than ffmpeg and
//   ffprobe, is an error and no answer.
// - Run.7 — Every call is recorded, in order, for Calls.
func (r *Runner) Run(ctx context.Context, name string, args ...string) (stdout, stderr string, err error)

// Serve scripts file as audio that decodes to samples.
func (r *Runner) Serve(file string, samples []int16)

// Fail scripts file as one the program fails on, with message as its last
// stderr line.
func (r *Runner) Fail(file, message string)

// Hang scripts file as one the program never finishes with.
func (r *Runner) Hang(file string)

// Missing scripts programs as not installed.
func (r *Runner) Missing(programs ...string)

// Calls returns the calls run so far.
func (r *Runner) Calls() []Call

// Tone returns seconds of a steady tone whose peak and mean volume are near
// dbfs.
func Tone(seconds, dbfs float64) []int16

// Silence returns seconds of zero samples.
func Silence(seconds float64) []int16

// Speech returns seconds of audio that passes every check of the Inspector
// when seconds is within the clip length.
//
// - Speech.1 — For 3 to 10 seconds, NewInspector(NewRunner()) accepts a file
//   served with Speech(seconds); different seconds give different clips.
func Speech(seconds float64) []int16
```

Consumes:

- `internal/testenv:Main`, `FakeBins` (import)
- `TestPackage_ImportsNoAudio` (phase 1) keeps proving F9 for this file.

`internal/audio:ExecRunner` is not consumed: this package may not import it (F9). The root passes it in.

## Acceptance criteria

| #   | Criterion                                                                                           | Proves                                   | Proof                                |
| --- | --------------------------------------------------------------------------------------------------- | ---------------------------------------- | ------------------------------------ |
| 1   | A good clip cut with decimal bounds comes back as 16 kHz mono FLAC with its four measurements       | `Inspect.1`, `Inspect.2`                 | `TestInspect_Accepts`                |
| 2   | A whole 16 kHz mono FLAC comes back byte for byte                                                   | `Inspect.3`                              | `TestInspect_StoredClipUnchanged`    |
| 3   | Too short and too long are refused with the README's message                                        | `Inspect.4`, `Inspect.8`, clip length    | `TestInspect_Length`                 |
| 4   | Mostly silence, clipped, and too quiet are each refused, and each limit itself passes               | `Inspect.5`–`Inspect.8`, L3.1, L3.2, L3.3 | `TestInspect_Speech`, `TestInspect_Clipping`, `TestInspect_Loudness` |
| 5   | A clip that fails several checks names all of them, in order                                        | `Inspect.8`                              | `TestInspect_EveryFailureNamed`      |
| 6   | A cut outside the file, a missing program, a file that is not audio and a cancelled context are four different typed errors | `Inspect.9`–`Inspect.12` | `TestInspect_Cut`, `TestInspect_MissingTool`, `TestInspect_Source`, `TestInspect_Cancelled` |
| 7   | No ending leaves a file behind or puts audio in an error                                            | `Inspect.13`, F8                         | `TestInspect_LeavesNothing`          |
| 8   | `audio.ExecRunner`'s method set is exactly `Runner`'s, with no import of `internal/audio`           | `Inspect.10`, F9                         | `TestRunner_ExecShape`, `TestPackage_ImportsNoAudio` |
| 9   | The whole Inspector suite passes over `voicestest.NewRunner()`, and a clip it produced can be inspected again from disk | `voicestest:Run.1`–`Run.7`, `LookPath.1`, `Speech.1`, `Package.1` | every `TestInspect_*` row, `TestVoicestest_StoredClipAgain`, `TestVoicestest_Runner`, `TestVoicestest_Speech`, `TestVoicestest_Imports` |

## Test cases

Every `TestInspect_*` row runs `NewInspector(voicestest.NewRunner())`, never the real programs, so the
suite that proves the Inspector also proves the double (L3.4): a source is `Serve`d as its samples, built
with `Tone`, `Silence` and hand-set samples. `TestRunner_ExecShape` runs a `testenv.FakeBins` stub. Each
row's Proves cell names the `voicestest` clauses it proves beside the Inspector's.

| Test                              | Level          | Proves                                         | Given                                                                                                              | Then                                                                                                          | Must not                                         |
| --------------------------------- | -------------- | ---------------------------------------------- | ------------------------------------------------------------------------------------------------------------------ | ------------------------------------------------------------------------------------------------------------- | ------------------------------------------------ |
| `TestInspect_Accepts`             | contract suite | `Inspect.1`, `Inspect.2`, `voicestest:Run.1`, `Run.2`, AC 1                 | A 120 s source of steady speech-level tone, served as its samples; `Cut{61.2 s, 69 s}`; then an 8 s source with the zero `Cut` | `Seconds` 7.8 and 8.0; `FLAC` starts with `fLaC` and declares 16 000 Hz, 1 channel; `SpeechShare` 1, `ClippedRun` 0 | Round a bound to whole seconds                |
| `TestInspect_StoredClipUnchanged` | contract suite | `Inspect.3`, `voicestest:Run.3`, AC 2                              | A 6 s 16 kHz mono FLAC file on disk (the `FLAC` of an earlier accepted clip, unscripted, `Run.3`), the zero `Cut`    | `FLAC` equals the file's bytes; `ClipIDOf` gives the same id as for the file                                  | Re-encode the file                               |
| `TestInspect_Length`              | contract suite | `Inspect.4`, `Inspect.8`, `voicestest:Run.1`, AC 3                 | Clips of 1.5 s, 2.999 s, 3 s, 10 s, 10.001 s and 43 s                                                              | 3 s and 10 s pass; the others are `ClipQualityError` with `CheckLength`; messages `clip too short: 1.5 s, a clip is 3–10 s` and `clip too long: 43.0 s, a clip is 3–10 s` | Return an `Inspection` with the error |
| `TestInspect_Speech`              | contract suite | `Inspect.5`, L3.1, `voicestest:Run.1`, AC 4                        | 10 s clips: 6 s of tone and one 4 s silence (share 0.6); 5.9 s of tone and 4.1 s of silence; tone broken by ten 0.2 s gaps; tone at -36 dBFS throughout | The first passes with `SpeechShare` 0.6; the second fails `CheckSpeech`; the third passes with share 1 (gaps under 0.3 s are not silences); the fourth fails with share 0 | Count a gap shorter than 0.3 s as silence |
| `TestInspect_Clipping`            | contract suite | `Inspect.6`, L3.2, `voicestest:Run.1`, AC 4                        | A clip with two consecutive full-scale samples; one with three; one with three full-scale samples that are not consecutive | `ClippedRun` 2 passes; 3 fails `CheckClipping`; the third passes with `ClippedRun` 1                        | Count separate peaks as one run                  |
| `TestInspect_Loudness`            | contract suite | `Inspect.7`, L3.3, `voicestest:Run.1`, AC 4                        | Clips of steady low noise with a one-sample click above -35 dBFS every 0.25 s (so no silence of 0.3 s), at a mean volume of -39.5 dBFS and of -40.5 dBFS | The first passes; the second fails `CheckLoudness` only, with `SpeechShare` 1 | Fail `CheckSpeech` for a quiet clip with enough speech |
| `TestInspect_EveryFailureNamed`   | contract suite | `Inspect.8`, `voicestest:Run.1`, AC 5                              | A 2 s clip of silence with a three-sample full-scale burst                                                         | `Failed` is `length, speech, clipping, loudness` in that order, with the four measurements set                | Stop at the first failed check                   |
| `TestInspect_Cut`                 | contract suite | `Inspect.9`, `voicestest:Run.1`, AC 6                              | A 20 s source with `Cut{-1 s, 5 s}`, `Cut{8 s, 8 s}`, `Cut{9 s, 4 s}`, `Cut{25 s, 0}`, `Cut{15 s, 30 s}`            | Each is a `CutError` with that cut and `Length` 20 s                                                          | Clamp a bound and carry on                       |
| `TestInspect_MissingTool`         | contract suite | `Inspect.10`, F9, `voicestest:LookPath.1`, `Run.7`, AC 6                         | `Missing("ffmpeg", "ffprobe")`; then `Missing("ffmpeg")`                                                          | `MissingToolError{Tool: "ffmpeg"}` both times, with `Calls()` empty                                    | Run a program, or return a generic error         |
| `TestInspect_Source`              | contract suite | `Inspect.11`, `voicestest:Run.4`, AC 6                             | A path that does not exist; a text file scripted with `Fail(file, "Invalid data found when processing input")` | `SourceError` with the path and, for the second, that message in `Detail`                                | Report a quality refusal for a file that is not audio |
| `TestInspect_Cancelled`           | contract suite | `Inspect.12`, `voicestest:Run.5`, AC 6                             | A file scripted with `Hang`, and a context cancelled during the call                                               | `errors.Is(err, context.Canceled)`                                                                            | Wait for the program to finish                   |
| `TestInspect_LeavesNothing`       | contract suite | `Inspect.13`, F8, AC 7                         | An accepted clip, a quality refusal, a `SourceError` and a cancellation, with `TMPDIR` and a `voices/` directory listed before and after | Both listings are unchanged, the source file's bytes are unchanged, and no error text holds a run of the clip's bytes | Leave a temporary file, or write under `voices/` |
| `TestInspect_Concurrent`          | unit           | `NewInspector.1`                               | One Inspector, eight goroutines inspecting two different sources                                                   | Each gets its own source's measurement; clean under `-race`                                                   | Share a temporary name between calls             |
| `TestRunner_ExecShape`            | unit           | `Inspect.10`, F9, AC 8                         | A test-local type with `audio.ExecRunner`'s two method signatures, copied, assigned to `Runner`; its `Run` on a `testenv.FakeBins` stub named `ffmpeg` | It compiles and the stub runs with exit 0                                                                     | Import `internal/audio`                          |
| `TestVoicestest_StoredClipAgain`  | contract suite | `voicestest:Run.2`, `Run.3`, `Inspect.3`, AC 9 | A clip accepted from `Serve`d samples, its `FLAC` written to a file; that file inspected whole and with `Cut{1 s, 5 s}` by a fresh `NewRunner()` | The whole inspection returns the same bytes and id; the cut returns a 4 s clip with another id, measured on those samples | Need `Serve` for a stored clip                   |
| `TestVoicestest_Runner`           | unit           | `voicestest:Run.6`, `Run.7`, `LookPath.1`, AC 9 | A `NewRunner()` called directly: `LookPath` of `ffmpeg`, `ffprobe` and `sox`; `Run` of `sox`; then `Missing("ffmpeg")`, `LookPath` and `Run` of `ffmpeg`; `Calls()` | `ffmpeg` and `ffprobe` resolve and `sox` does not; both `Run` calls return an error and no output; after `Missing`, `ffmpeg` does not resolve; `Calls()` holds the two `Run` calls, in order, with their arguments | Answer for a program it does not know, or lose a call |
| `TestVoicestest_Speech`           | unit           | `voicestest:Speech.1`, AC 9                    | `Speech(3)`, `Speech(6.5)`, `Speech(10)` served and inspected; `Speech(2)` and `Speech(11)`                         | The first three are accepted with different ids; the last two fail `CheckLength` only                         | Fail a check other than length                   |
| `TestVoicestest_Imports`          | unit           | `voicestest:Package.1`, F9, AC 9               | The import lists of `voicestest`'s files, and of every non-test Go file of the repository                           | `voicestest` imports nothing from `internal/audio`; no non-test file imports `voicestest`                     | Shell out to the go command                      |

## Error management

| Failure                                         | Expected outcome                                                                         | Test                            |
| ----------------------------------------------- | ---------------------------------------------------------------------------------------- | ------------------------------- |
| Clip shorter than 3 s or longer than 10 s       | `*ClipQualityError` with `CheckLength`; `clip too short: 1.5 s, a clip is 3–10 s`        | `TestInspect_Length`            |
| Speech share under L3.1                         | `*ClipQualityError` with `CheckSpeech` and the measured share                            | `TestInspect_Speech`            |
| A full-scale run as long as L3.2 forbids        | `*ClipQualityError` with `CheckClipping` and the run's length                            | `TestInspect_Clipping`          |
| Mean volume under L3.3                          | `*ClipQualityError` with `CheckLoudness` and the measured volume                         | `TestInspect_Loudness`          |
| Several checks fail                             | One `*ClipQualityError` naming all of them                                               | `TestInspect_EveryFailureNamed` |
| A cut that selects nothing from the file        | `*CutError` with the cut and the file's length                                           | `TestInspect_Cut`               |
| `ffmpeg` (or a needed `ffprobe`) does not resolve | `*MissingToolError` naming it; nothing is run                                          | `TestInspect_MissingTool`       |
| File missing, not audio, or the program fails   | `*SourceError` with the path and the program's last message                              | `TestInspect_Source`            |
| Context cancelled                               | An error matching `ctx.Err()`                                                            | `TestInspect_Cancelled`         |

## Golden diff

None.

## Removes

None.

## Local facts

Moved unchanged from the README's Phase-local facts:

| Parameter              | Default                                                              | Decided by                                       |
| ---------------------- | -------------------------------------------------------------------- | ------------------------------------------------ |
| L3.1 speech share      | ≥ 0.6 of the clip above -35 dBFS, silences of 0.3 s or more counted out | maintainer, 2026-11-18                           |
| L3.2 clipping          | no run of 3 or more consecutive samples at full scale                | maintainer, 2026-11-18                           |
| L3.3 loudness          | mean volume ≥ -40 dBFS                                               | maintainer, 2026-11-18                           |

**L3.4 — Test seam.** The contract suite runs against `voicestest.Runner`, the exported double declared
in the Interface section. It resolves `ffmpeg` and `ffprobe` as the row scripts and answers a decode or
encode call from the row's samples (16 kHz mono signed 16-bit: tone, silence, full-scale runs), so no
row needs the real programs and every measurement is exact. The double and the Inspector agree on the
calls made; that agreement is body of this phase, and the suite passing over the double is its proof. The measurements are defined on those samples: speech
share is one minus the share of the clip inside stretches of 0.3 s or more in which every sample is
under -35 dBFS; a sample is at full scale at 32767 or -32768; mean volume is the RMS of the samples in
dBFS. `FakeBins` only makes a program resolve on `PATH`; it measures nothing. (Phase-authored;
it is signed with this phase's interface.)

## Human required

None. The README's human check for node 3 (listening to an enrolled clip) is run by the maintainer once
every phase is `Complete`; it is not a step of this phase.

## Implementation notes

| Run   | Commit      | Command                                                                                                                                                                 | Result                                                                                                                                  |
| ----- | ----------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------- | --------------------------------------------------------------------------------------------------------------------------------------- |
| start | 1550dec+wip | scoped gates (Q18): `go test ./internal/audio/voices/... -race -count=3 -cover -timeout=30s -p 4`; `go vet`, staticcheck, `gofumpt -l` on it; `dupl -t 80 .`            | package not yet created: the pattern matches nothing for test, vet, staticcheck, gofumpt; dupl 49 groups                                |
| end   | 1550dec+wip | the same, `go test` with `-shuffle=on`                                                                                                                                  | voices ok 94.4 %, voicestest ok 94.9 %; 22 phase-3 tests passed, 0 failed; vet, staticcheck, gofumpt clean; dupl 49 groups, none in voices |
| end   | 1550dec+wip | the same, `go test` with `-shuffle=on`, fresh seed                                                                                                                      | voices ok 94.4 %, voicestest ok 94.9 %; 22 phase-3 tests passed, 0 failed; vet, staticcheck, gofumpt clean; dupl 49 groups, none in voices |
| fix-start | 1550dec+wip | `make qa`; `go vet ./...`; `dupl -t 80 .` (A02) | `make qa` exit 2: `internal/audio/voices` timed out at 30 s in `TestInspect_LeavesNothing` (testenv: busy); vet exit 0; dupl 49 groups. `TestInspect_*` were 4.5 s of voices' 7.2 s per count under `-race -cover` |
| fix-end | 1550dec+wip | `go test ./internal/audio/... ./pkg/tools/... ./internal/tools/... ./internal/menu/... ./pkg/diagnostic/... -race -count=3 -cover -timeout=30s -p 11`; `go vet ./...`; staticcheck and `gofumpt -l` on `internal/audio/...`, `pkg/tools/...`; `dupl -t 80 .`; then `make qa` | all ok ×2 (second `-shuffle=on`); voices 7.8 s, 8.5 s, 92.4 %; voicestest 2.1 s, 2.6 s, 94.4 %; test sets unchanged (voices 219, voicestest 6 per count); vet, staticcheck, gofumpt clean; dupl 49; `make qa` exit 0, voices 10.5 s |
| fix-start | 1550dec+wip | `env -u OPENAI_API_KEY -u ANTHROPIC_API_KEY -u OPENROUTER_API_KEY go test ./internal/audio/voices/... ./internal/menu/... ./pkg/diagnostic/... -race -count=3 -cover -timeout=30s -p 4`; `go vet`, staticcheck and `gofumpt -l` on `internal/audio/voices/...`, `internal/menu/...`; `dupl -t 80 .` (R1-06) | all ok: voices 6.0 s 92.4 %, voicestest 2.2 s 94.4 %, menu 1.4 s 96.0 %, diagnostic 2.9 s 93.3 %; vet, staticcheck, gofumpt clean; dupl 49 groups |
| fix-end | 1550dec+wip | the same | all ok (load average 34): voices 8.4 s 95.4 %, voicestest 2.6 s 94.4 %, menu 1.6 s 96.0 %, diagnostic 3.1 s 93.3 %; voices 63 tests, 248 with subtests, menu 33 and 166, per count; vet, staticcheck, gofumpt clean; dupl 49 groups, none in voices or menu |
| fix-end | 1550dec+wip | the same with `-shuffle=on` | all ok (load average 21): voices 8.4 s 95.4 %, voicestest 2.4 s 94.4 %, menu 1.5 s 96.0 %, diagnostic 3.0 s 93.3 %; vet, staticcheck, gofumpt clean; dupl 49 groups |

Andon: none.
Surprises:
- gofumpt indents the `- Clause.N —` items of top-level doc comments; declarations stay token-identical to the Interface blocks (go/ast comparison, 36 declarations, 0 differ).
- `Runner` has no stdin, so an accepted clip is encoded from the source again, same filter, into a private temp dir and read back; FLAC written to a pipe lacks STREAMINFO's sample count.
- ffprobe gives no duration for some files (browser WebM); the length then comes from the decoded samples, proved by the added `TestInspect_LengthFromDecode`.
- Digital silence is -Inf dBFS; `MeanDBFS` is floored at -91 dBFS, ffmpeg volumedetect's floor, so no measurement is infinite.
- Rows added beyond the table for paths it does not reach: `TestInspect_UnusableAnswers`, `TestVoicestest_Scripts`, `TestVoicestest_UnsupportedCalls`.
- Phase 1 owns the `voices_test` `TestMain` (`main_test.go`); `voicestest` has its own.
- A02: under `-race -cover` each block a per-sample loop enters is an atomic counter add; `inspectMeasure` now walks runs (one block per sample), decoding is `binary.Decode`, `voicestest.Tone` copies one period.
- R1-06: a temporary directory that cannot be made or removed is a `*SourceError` with the file's path and the failure's text as `Detail`; no other phase 3 type fits it.

## Review findings

None.
