# Phase 2 — audio_sources tool

**Parent:** root · **Class:** contract · [README](README.md)

## Goal

Give a model a read-only way to ask which audio inputs this machine can record
now, and whether system audio is one of them.

## Interface

Written to `pkg/tools/audio_tool_sources.go`, with the engine in
`internal/audio/sources.go` and the registration row in
`internal/tools/handler.go`.

```go
package tools

import (
	"context"

	pub_models "github.com/baalimago/clai/pkg/text/models"
)

// AudioSourcesEngine answers one call with the lines a model reads;
// internal/audio installs it in its init, as it installs
// AudioTranscribeEngine. (AudioSourcesEngine.1, F8)
var AudioSourcesEngine func(ctx context.Context) (string, error)

// AudioSourcesTool is the audio_sources schema. (F1, F11)
type AudioSourcesTool pub_models.Specification

// AudioSources lists this machine's capture sources. (Specification.1)
var AudioSources = AudioSourcesTool{
	Name: "audio_sources",
	Description: "List the audio inputs this machine can record now: the built-in microphone and every system output whose monitor can be captured. The first line says whether system audio can be captured. Each further line is '<kind> \"<name>\" [<label>]', where <name> is what audio_record's devices argument takes, and a trailing ' default' marks the source a role selection picks. Call this before audio_record to learn the names.",
	Inputs: &pub_models.InputSchema{
		Type:       "object",
		Required:   make([]string, 0),
		Properties: map[string]pub_models.ParameterObject{},
	},
}

// Call runs the tool with context.Background(). (Call.1)
func (a AudioSourcesTool) Call(input pub_models.Input) (string, error)

// CallWithContext returns the source list.
//
// - A nil AudioSourcesEngine is an error saying audio discovery is not
//   available in this runtime. (CallWithContext.1, F8)
// - Otherwise it returns the engine's text and nothing else: the tool renders
//   nothing, and ctx reaches the engine so an error it reports arrives with
//   its remedy. (CallWithContext.2, F8, F11)
func (a AudioSourcesTool) CallWithContext(ctx context.Context, input pub_models.Input) (string, error)

// Specification advertises the schema above. (Specification.1)
func (a AudioSourcesTool) Specification() pub_models.Specification
```

```go
package audio

import (
	"context"

	"github.com/baalimago/clai/internal/capture"
)

// sourcesText renders one discovery for a model: a
// `system_audio: available|unavailable` line, then one line per source.
// (sourcesText.1, F11)
func sourcesText(ctx context.Context, r capture.Recorder) (string, error)
```

Clauses of `sourcesText`:

- `sourcesText.1` — `system_audio` reads `available` exactly when the list
  holds a sink. (F11)
- `sourcesText.2` — Each source renders as `<kind> "<name>" [<label>]`, in the
  recorder's order, with ` default` appended to a marked source; the name is
  quoted so it stays one token for a model to copy. (F11)
- `sourcesText.3` — An error from `Sources` is returned unchanged. (F8)

`init` in the same file sets `pkgtools.AudioSourcesEngine` to a closure over
`capture.New()`.

Consumes: `capture:Recorder`, `capture:Source`, `capture:Kind` (phase 1);
`pkg/text/models:LLMTool`, `Input`, `Specification`, `InputSchema`,
`ParameterObject` (import); `internal/tools:executableTool`, `registerLocalTools`
(import); the phase 1 fixture `internal/capture/testdata/fake_ffmpeg.sh`
(recorded fixture).

## Acceptance criteria

| #   | Criterion                                                                                   | Proves                            | Proof                               |
| --- | ------------------------------------------------------------------------------------------- | --------------------------------- | ----------------------------------- |
| 1   | The schema is named `audio_sources`, offers no argument and requires none                   | `Specification.1`, `F1`           | `TestAudioSources_Specification`    |
| 2   | The text starts with the system-audio line and lists every source with its default marker    | `sourcesText.1`, `sourcesText.2`, `F11` | `TestSourcesText`            |
| 3   | A machine with no sink reads `system_audio: unavailable` and is not an error                | `sourcesText.1`, `F11`            | `TestSourcesText`                   |
| 4   | An uninstalled engine reports that discovery is unavailable; the call context reaches the engine and its error returns unchanged | `CallWithContext.1`, `CallWithContext.2`, `sourcesText.3`, `F8` | `TestAudioSources_Call` |
| 5   | The tool is registered exactly when `ffmpeg` resolves                                        | `F8`                              | `TestRegisterLocalTools_AudioSourcesGate` |
| 6   | A linked binary carries the production engine                                               | `AudioSourcesEngine.1`, `F8`      | `Test_audioSourcesEngineWired`      |

## Test cases

| Test                                       | Level                | Proves                                             | Given                                                                    | Then                                                                             | Must not                  |
| ------------------------------------------ | -------------------- | -------------------------------------------------- | ------------------------------------------------------------------------ | -------------------------------------------------------------------------------- | ------------------------- |
| `TestAudioSources_Specification`           | contract suite       | `Specification.1`, `F1`, AC 1                      | The package value                                                        | Name `audio_sources`, no properties, `Required` empty                             | Require an argument       |
| `TestAudioSources_Call`                    | contract suite       | `Call.1`, `CallWithContext.1`, `CallWithContext.2`, `F8`, AC 4 | A stub engine that records its context and returns text; then a nil engine | The text comes back unchanged from both `Call` and `CallWithContext`, the context is cancelled by the caller, and a nil engine yields the runtime error | Panic, or render anything |
| `TestSourcesText`                          | integration scenario | `sourcesText.1`, `sourcesText.2`, `sourcesText.3`, `F11`, AC 2, AC 3 | The real recorder over phase 1's fixture listing one mic and one monitor, then a mic-only listing, then a listing that fails | `system_audio: available` with quoted names and ` default` on both, then `system_audio: unavailable` with the mic line only, then the recorder's error unchanged | Shell out to real audio   |
| `TestRegisterLocalTools_AudioSourcesGate`  | unit                 | `F8`, AC 5                                         | `registerLocalTools` with a lookup that resolves ffmpeg, then one that fails | Present in the first registry, absent in the second                              | Consult the real `PATH`   |
| `Test_audioSourcesEngineWired`             | unit                 | `AudioSourcesEngine.1`, `F8`, AC 6                 | An import of `internal/audio`                                            | `AudioSourcesEngine` is not nil                                                  | Touch a device            |

## Error management

| Failure                     | Expected outcome                                              | Test                                      |
| --------------------------- | ------------------------------------------------------------- | ----------------------------------------- |
| Engine not installed        | Error saying discovery is unavailable in this runtime          | `TestAudioSources_Call`                   |
| Backend error               | Engine error returned unchanged                               | `TestAudioSources_Call`                   |
| No system audio on this machine | Not an error: `system_audio: unavailable`                  | `TestSourcesText`                         |
| `ffmpeg` absent from `PATH` | The tool is absent from the registry                          | `TestRegisterLocalTools_AudioSourcesGate` |

## Golden diff

None. `clai tools` is asserted by footer only
(`Test_goldenFile_TOOLS_lists_tools_and_footer`), and completion does not
snapshot the tool catalog.

## Removes

None.

## Local facts

**L1 — Result text.** Measured fixture shape, newline terminated:

```text
system_audio: available
mic "alsa_input.pci-0000_00_1f.3.analog-stereo" [Built-in Audio Analog Stereo] default
sink "alsa_output.pci-0000_00_1f.3.analog-stereo.monitor" [Monitor of Built-in Audio Analog Stereo] default
```

Names are `strconv.Quote`d, labels are bracketed, and ` default` closes a marked
line. The model copies a quoted name into `audio_record`'s `devices`
argument, which is why the name and not the label is quoted.

**L2 — Registration.** The tool joins the `executableTools` table in
`internal/tools/handler.go` with `executable: "ffmpeg"`, so the existing
injected lookup decides availability and an unsupported platform fails at call
time with `capture.ErrUnsupported` instead of hiding the tool. Both audio tools
share that one condition, so `clai tools` answers exactly what a run can use.

**L3 — Injection.** `internal/audio`'s `init` installs the engine, so every
binary that links the package answers calls without per-run wiring, as with
`audio_transcribe`. A test that needs a fixed answer overrides
`pkg/tools.AudioSourcesEngine` and restores it in `t.Cleanup`.

**L4 — Documentation.** This phase adds a catalog row to `pkg/tools/README.md`;
`architecture/audio.md` gains its capture section in phase 3.

## Human required

None. The linux and darwin device listings are README human checks the
maintainer runs once every phase is complete.

## Implementation notes

Implemented:
- `pkg/tools/audio_tool_sources.go`: schema + engine injection.
- `internal/audio/sources.go`: `sourcesText` renderer + `init` wiring through `capture.New()`.
- `internal/tools/handler.go`: `audio_sources` is registered only when `ffmpeg` resolves on PATH.

Notes:
- The tool takes no args, so it ignores its input map.
- The call path returns errors from discovery unchanged so remedy text remains intact.

## Worklog

Commands run (this session):

```sh
# package tests
go test ./pkg/tools -count=1
go test ./internal/audio -count=1
go test ./internal/tools -count=1 -run TestRegisterLocalTools_AudioSourcesGate -timeout=30s
go test ./internal/tools -count=1 -timeout=30s

# repo-wide sanity (expected fail pre-existing in internal/summary)
go test ./... -count=1 -timeout=30s
```

Results:
- `go test ./pkg/tools -count=1`: PASS
- `go test ./internal/audio -count=1`: PASS
- `go test ./internal/tools -count=1 -run TestRegisterLocalTools_AudioSourcesGate -timeout=30s`: PASS
- `go test ./internal/tools -count=1 -timeout=30s`: PASS
- `go test ./... -count=1 -timeout=30s`: FAIL (pre-existing failure in `internal/summary`, reproduced; not caused by this phase)

## Review findings

### Review 1, 2026-10-28 — root review

**Status: Complete — R1-02 and R1-03 fixed and verified in the same session.**

**Verified good.**

- **The schema has no argument and requires none**
  (`pkg/tools/audio_tool_sources.go:20-25`), which is what F1 and AC 1 ask
  for.
- **The rendering is exactly the measured contract**: `system_audio:
  available|unavailable`, then `<kind> "<quoted name>" [label]` with a
  trailing ` default` (`internal/audio/sources.go:13-33`), proven over the
  real recorder and phase 1's fixture.
- **A `Sources` error returns unchanged**, so the recorder's remedy reaches
  the model (`sourcesText` has no wrapping).
- **The registration gate is two-sided**: `audio_sources` is present when the
  injected lookup resolves `ffmpeg` and absent when it fails
  (`internal/tools/handler.go:83`, `TestRegisterLocalTools_AudioSourcesGate`;
  both audio gates share `requireAudioToolGate`).
  The engine is wired by `internal/audio`'s `init`, and
  `Test_audioSourcesEngineWired` proves it.

**Findings**

- [x] **R1-02** (minor, `CallWithContext.2`, phase-2 AC 4) —
  `TestAudioSources_Call` never asserted that the engine's text is returned,
  and never called `Call` at all; the test case row requires "the text comes
  back unchanged from both `Call` and `CallWithContext`". A wrapper or a
  dropped result would have passed. The test now asserts the exact text from
  both entry points, the live context of `Call`, the cancelled context and
  the unchanged engine error (`pkg/tools/audio_tool_sources_test.go:29-76`).
- [x] **R1-03** (minor, evidence level) — the test case row names an
  integration scenario over "the real recorder over phase 1's fixture", but
  `TestSourcesText` used a local `stubRecorder` for every case, so no ffmpeg
  ran and the failing listing was a sentinel rather than the recorder's
  error. The test now drives `capture.New()` over the fixture in all three
  modes (available, mic-only, refused) and compares the error with the
  recorder's own (`internal/audio/sources_test.go:11-53`); the stub is
  deleted.
- [x] **R1-09** (minor, `F8` evidence) —
  `TestRegisterLocalTools_AudioSourcesGate` and phase 3's
  `TestRegisterLocalTools_AudioRecordGate` were the same twenty-line test
  twice, and dupl flagged them at the 80-token threshold. Both now call one
  `requireAudioToolGate` (`internal/tools/audio_sources_gate_test.go:10-42`),
  the test names stay, and dupl reports no clone among the worklog's files.

**Notes.**

- `audio_sources` cannot hide an empty machine: a listing with no source is
  rendered as `system_audio: unavailable` and no further lines. F11 forbids
  an empty list as a *failure* result; an empty machine is an honest answer.

### Review 2, 2026-10-28 — root review, holistic sweep

**Status: no finding.** The renderer, the unchanged error passthrough and the
registration gate are untouched by R2-01 and R2-02. `TestSourcesText` still
drives the real recorder over the fixture, which now writes a real two-sample
WAV; discovery is unaffected. `go test ./internal/audio ./internal/tools
-race -cover -count=3 -timeout=30s` passes (89.0 %, 84.4 %).

### Review 3, 2026-10-28 — root review, holistic sweep

**Status: no finding.** R3-01 is an `audio_record` stop-line defect and R3-02
is recorder evidence; neither touches `sourcesText` or the schema. Measured
on this host through the shipped tool (`clai -cm test -t audio_sources q
tool_audio_sources`): `system_audio: available`, one default mic and one
default monitor, with the pulse names quoted as L1 describes. That is the
linux half of README human check 2; the maintainer's certification remains
R1-07.

### Review 4, 2026-10-28 — root review

**Status: no finding.** The renderer, the error passthrough and the shared
registration gate are unchanged since review 3.

**Verified good.**

- `sourcesText` writes `system_audio: available|unavailable` first and one
  `<kind> "<name>" [<label>]` line per source, with ` default` closing a marked
  line (`internal/audio/sources.go:13-31`); `TestSourcesText` drives the real
  recorder over the phase-1 fixture, including the mic-only and failing
  listings.
- A nil `AudioSourcesEngine` returns the runtime error, and an engine error
  returns unchanged, so a backend's remedy reaches the model
  (`TestAudioSources_Call`).
- `TestRegisterLocalTools_AudioSourcesGate` still shares
  `requireAudioToolGate` with `audio_record` (R1-09).
