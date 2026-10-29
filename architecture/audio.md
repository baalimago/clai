# Audio Command Architecture

Command: `clai [flags] audio transcribe <file>` (aliases: `a t`)

The **audio** command namespace transcribes audio files using AI models
(currently the OpenAI multipart transcription protocol, served by OpenAI and
OpenRouter). The rendered transcript is the only thing written to stdout, so
it pipes cleanly; all status/progress lines go to stderr. Use `-` as the file
to read audio bytes from stdin.

## Entry Flow

```
internal/cli:Run()
  → cmd.Run(...)                       # go_away_boilerplate/pkg/cmd dispatch
    → audio → transcribe subcommand (Subcommander tree, internal/audio/cmd.go)
      → setupAudioTranscribeQuerier()
      → resolveAudioInput()           # positional file, or '-' → stdin temp file
                                      # (container sniffed → real extension, see sniff.go)
      → LoadConfigFromFile("audioConfig.json")
      → applyFlagOverridesForAudio()
      → CreateAudioQuerier()          # vendor routing + splitter
  → querier.Query(ctx)                # transcribe → render → stdout
```

## Key Files

| File                                        | Purpose                                                                                               |
| ------------------------------------------- | ----------------------------------------------------------------------------------------------------- |
| `internal/audio/cmd.go`                     | audio Subcommander tree — transcribe\|help verbs (see [cmd-dispatch.md](./cmd-dispatch.md))           |
| `internal/audio/board.go`                   | Adapter over the shared progress table ([board.md](./board.md)): diarization columns, `unknownSoFar` |
| `internal/audio/setup_transcribe.go`        | Transcribe-querier setup, stdin `-` input resolution                                                  |
| `internal/audio/audio.go`                   | `Segment` model, JSON parsing, `vtt\|srt\|text\|json` rendering, `Offset`                            |
| `internal/audio/split.go`                   | `Splitter`: byte-cap gate, plain ffmpeg chunking for non-diarized models, calibration routing for diarized ones |
| `internal/audio/plan.go`, `assemble.go`     | `ChunkPlanner` (silence-aligned cores under the seconds budget), `AudioAssembler` (PCM timeline, MP3 encode, manifests) |
| `internal/audio/sample.go`, `registry.go`, `mapping.go`, `discovery.go` | Sample extraction, append-only speaker registry, label mapping, bootstrap and discovery |
| `internal/audio/coordinator.go`, `stitch.go`, `cache.go` | Parallel coordinator with request accounting, `unknown-N` stitching, run-scoped request cache |
| `internal/audio/eval.go`                    | Acceptance evaluator: annotation parsing, one-to-one attribution report                              |
| `internal/audio/sniff.go`                   | `DetectExtension`: container magic → file extension                                                   |
| `internal/audio/querier.go`                 | `Configurations`, `TranscribeQuerier`, mock transcriber                                               |
| `internal/audio/generic/transcriber.go`     | Generic OpenAI-protocol multipart transcription client                                                |
| `internal/vendors/openai/transcribe.go`     | OpenAI vendor struct (`OPENAI_API_KEY`, api.openai.com)                                               |
| `internal/vendors/openrouter/transcribe.go` | OpenRouter vendor struct (`OPENROUTER_API_KEY`, `or:` prefix trim, extra headers)                     |
| `internal/audio/create_querier.go`          | `audio.CreateQuerier()`/`createSplitter()` — model routing; audio-transcribe tool bridge initialization |
| `pkg/tools/audio_tool_transcribe.go`        | `audio_transcribe` built-in tool (thin adapter, injected engine)                                      |

## Configuration

### `audioConfig.json`

```json
{
  "transcribe": {
    "model": "whisper-1",
    "output-format": "vtt",
    "parallelism": 3,
    "max-request-bytes": 26214400,
    "max-request-seconds": 1400,
    "max-speakers": 8,
    "strict-speakers": false
  }
}
```

### Key Fields

| Field                      | Description                                                          |
| -------------------------- | -------------------------------------------------------------------- |
| `transcribe.model`         | Transcription model; routed to a vendor by name (see Vendor Routing), or a root-level `model-aliases` verb such as `fast` (see [model-aliases.md](./model-aliases.md)) |
| `transcribe.output-format` | Local render format: `vtt` (default), `srt`, `text`, or `json`       |
| `transcribe.parallelism`   | Max concurrent chunk requests when a large file is split (default 3) |
| `transcribe.max-request-bytes`   | Per-request upload cap for both split paths (default 25 MiB); zero means default, negative is an error |
| `transcribe.max-request-seconds` | Per-request audio cap for calibrated diarization (default 1400 s) |
| `transcribe.max-speakers`        | Speaker registry cap for calibrated diarization (default 8); sizes the prefix reserve |
| `transcribe.strict-speakers`     | Error instead of `unknown-N` output when a material speaker stays unresolved (default false) |

### Flag Overrides

| Flag                    | Config Field               |
| ----------------------- | -------------------------- |
| `-am` / `-audio-model`  | `transcribe.model`         |
| `-af` / `-audio-format` | `transcribe.output-format` |
| `-parallelism`          | `transcribe.parallelism`   |
| `-max-request-bytes`    | `transcribe.max-request-bytes`   |
| `-max-request-seconds`  | `transcribe.max-request-seconds` |
| `-max-speakers`         | `transcribe.max-speakers`        |
| `-strict-speakers`      | `transcribe.strict-speakers`     |

Precedence is the standard cascade: flags > file > defaults (see
[`config.md`](./config.md)).

## Vendor Routing

`audio.CreateQuerier()` in `internal/audio/create_querier.go` routes explicitly:

| Model Pattern                      | Vendor                                                                              |
| ---------------------------------- | ----------------------------------------------------------------------------------- |
| `or:` prefix                       | OpenRouter (`or:` trimmed from the wire model)                                      |
| contains `whisper` or `transcribe` | OpenAI                                                                              |
| `test`/`mock_test` prefix          | Mock transcriber (deterministic, network-free; contains `diarize` → speaker labels) |

## Response-Format Negotiation

The vendor request always asks for the richest machine format the endpoint
supports: `diarized_json` when the model name contains `diarize`, else
`verbose_json`. `text`/`srt`/`vtt` are never requested from a vendor
(OpenRouter rejects them); those are local renderings from the shared
`Segment` model. Speaker labels render as WebVTT voice tags (`<v A>`), SRT/text
`A: ` prefixes, and a `speaker` field in `json` output (float-second
timestamps). JSON output is indented with one segment object per block and a
trailing newline so large transcripts remain usable with `jq`, `head`, `tail`,
and line-oriented inspection tools.

## Large Files: Split and Stitch

Files over 25 MB exceed the vendor request cap. The `Splitter`
(`internal/audio/split.go`):

1. Requires `ffmpeg` **and** `ffprobe` on PATH (only for oversized files);
   missing binaries produce an actionable error with a manual split command.
2. Probes total duration and splits the input into fixed windows that target
   approximately 20 MB each (`-f segment -c copy`).
3. Transcribes chunks in a bounded worker pool (`parallelism`).
4. Offsets timestamps and stitches chunks in order.
5. Warns that diarization labels are request-local and can drift across chunks.

This plain path applies to non-diarized models only. Diarize models take
the calibration path below, which reconciles speakers across requests.

## Adaptive Speaker Calibration

Implemented 2026-09-02; the worklog with the measurements and decisions is
[`worklogs/2026-09-01-adaptive-speaker-calibration/`](../worklogs/2026-09-01-adaptive-speaker-calibration/).
An oversized recording transcribed with a diarize model takes this path
instead of the plain split above.

### Provider contract and evidence

The diarization provider assigns labels per request. A label such as `A` has no
meaning outside that request. The design carries audio evidence into every
ordinary diarization request so that any provider returning speaker labels and
timestamps can be supported.

The required provider behavior is: within one submitted recording, the provider
assigns the same local label to an exact previously diarized source clip and to
later target speech from that voice. The provider may use different letters in
every request. The splitter reconstructs the mapping independently per request
and never assumes that request 2's `A` is request 1's `A`.

This behavior was **measured before implementation** by the Phase 0 spike
(2026-09-02): with one clean clip per voice, every sample received one
dominant label, all samples were distinct, and 99.8% of core speech mapped to
a prefix label in two of two forward-order requests. Two provider facts
constrain the design:

1. The provider over-segments within a single request. The reference meeting
   produced up to 20 labels for six people inside one 10-minute request (see
   the per-chunk table below). Spurious labels are therefore normal input, not
   an error, and the algorithm must tolerate them without inventing identities.
2. The `gpt-4o-transcribe` family enforces an audio-duration cap per request,
   in addition to the 25 MB size cap: 1400 s succeeded in the spike, the
   5065 s whole file returned HTTP 400. Both budgets apply to every assembled
   request.
3. The provider is not self-consistent: two prefix-free runs of the same
   chunk agree on 67–78% of speech, anchored repeats on about 94%. Two
   distinct samples can share a label depending on prefix order, and a
   speaker's speech can be split away from that speaker's own clip. The
   design recovers from both (reversed-order retry, discovery) and never
   treats a single request as proof of identity.

Provider-native `known_speaker_names`/`known_speaker_references` are not used
in the pipeline. The stated cap is four references of 2–10 s, which cannot hold
a six-speaker meeting, and in the spike they misassigned a whole speaker.

### Request budgets and encoding

Every request is bounded by two configurable budgets: `max-request-bytes`
(default 25 MiB; the encoded file plus a fixed multipart envelope reserve
must fit) and `max-request-seconds` (default 1400 s). A zero config value
means the default; a negative value is a load-time error. The planner runs once per recording and
sizes each core interval as the seconds budget minus a fixed prefix reserve
minus a 2% safety margin, then checks the encoded bytes. The reserve is
`max-speakers` (default 8) worst-case sample slots of 11 s each, so a speaker
discovered late never pushes a core over the budget and nothing is replanned.
The tighter of the two budgets wins.

Requests are re-encoded, never stream-copied. The assembler first builds a
canonical PCM timeline (mono, 16 kHz, signed 16-bit) so every boundary is a
known sample count, then encodes the timeline once with a deterministic
compressed codec from the provider's supported list (default: MP3, CBR 64 kbps,
mono, `-map_metadata -1 -fflags +bitexact`). For the 84-minute reference
meeting this yields four requests of about 21 minutes instead of eight of ten,
which halves prefix overhead and boundary count and gives the diarizer more
context per request. Prefix and core boundaries are derived from the PCM sample
count; ffprobe measures only the source, where seeking is inexact.

Chunk boundaries are aligned to silence. One `silencedetect` pass over the
source (default: −30 dB, minimum 400 ms) yields candidate cut points; the
planner moves each planned boundary to the nearest silence midpoint within 10%
of the target length so no utterance is cut, falling back to the planned time
when no silence exists in the window.

### Speaker sample registry

During one source recording, the splitter maintains a registry with one global
speaker ID and one active clean audio sample for each verified voice. A sample
contains 3–10 s of one local label with guard silence, chosen as the longest
segment with the largest clearance from other labels. Registry entries are
recording-local and claim no real-world identity.

```text
global speaker A
  sample: source 00:02:14.2–00:02:22.0
  quality: speech=7.8s, clearance=2.1s, verified-in=3 requests
```

The registry is append-only and holds at most `max-speakers` identities; a
candidate beyond that is left unmapped with reason `prefix-reserve-exhausted`.
Adding a speaker never invalidates a chunk that already mapped every label. A
sample is replaced only when it fails verification in a later request, cut
from the speaker's verified source ranges; at most two replacements per
speaker. Every mutation (add, replace, ambiguity replacement, freeze, refusal)
enters through one serialized entry point under the discovery lock with a
snapshot version re-check; the worklog lists them as a table and no other
mutation exists.

### Calibration prefix and mapping

Before a chunk is transcribed, the assembler builds one ordinary request:

```text
[speaker A sample][silence][speaker B sample][silence]...[core audio]
```

The splitter knows every sample interval in request time. Labels returned
inside those intervals map request-local labels to global speakers:

```text
global A sample → local C
global B sample → local A
target local C → global A
target local A → global B
```

A sample maps when one local label covers at least 80% of its labeled speech
and no other accepted sample shares that label. Every request that carries a
sample verifies it; there are no dedicated verification requests. A sample
that is mixed or missing fails that request's mapping and is replaced. Two
accepted samples sharing one label is ambiguity: the chunk is retried once
with the reversed prefix order and no registry change, and only a repeated
ambiguity replaces the samples. A collapse never merges two verified
identities.

### Discovery

A local label is **material** when it has at least 3 s of speech and at least
1% of the chunk's speech. Only material labels create speakers. Non-material
labels are still mapped when they match a sample; otherwise their speech is
kept and rendered as `unknown`.

1. Transcribe the first core chunk without a prefix.
2. Extract one candidate sample for every material label, assemble the prefix
   from all candidates, and transcribe the same chunk once more.
3. Candidates that collapse onto one label are one voice; keep the better clip.
   Candidates with a dominant, distinct label are promoted. Mixed candidates
   are replaced once and the chunk is retried.
4. For a later chunk, unmapped material labels trigger discovery: take the
   discovery lock, re-read the registry (another worker may have added the
   voice), extract candidates for all unmapped labels, and retranscribe the
   chunk once with the extended prefix. At most two discovery iterations per
   chunk.

Discovery is serialized; all other chunks run in parallel against a frozen
registry snapshot. Accepted chunk results are final.

Budget: at most 4 requests per chunk, an injectable coordinator field that
equals the longest production path (bootstrap: two passes, one mixed-candidate
iteration, one recovery retry; later chunks: one pass, one recovery retry, two
discovery iterations). The recovery retry is one slot per chunk, spent either
on the reversed-order retry or on a sample replacement, and discovery runs
only once the chunk's latest request shows no failed registry sample: a
failed speaker's speech is unanchored and must never seed a candidate. Every transcription request counts, including bootstrap
passes, discovery iterations, and replacement retries; cache hits are
reported, not counted. There is no run limit and no cost limit: uploaded
audio is bounded by the per-chunk limit times the chunk count times
`max-request-seconds`. Exhaustion is an error under both policies and is
reported with the affected source interval, labels, attempts, and consumed
requests.

### Stitching and failure policy

For every accepted chunk, the splitter maps labels to global IDs, removes the
prefix intervals, restores core timestamps from the sample-count manifest, and
appends segments in source order; no codec timestamp correction is applied
(the spike measured a median shift below the tolerance). A run-scoped in-memory cache keyed by request
bytes hash, model, endpoint, and request options prevents paying twice for an
identical request.

Best-effort is the default. Speech whose label cannot be mapped is rendered as
`unknown-N`, numbered at stitch time per contiguous unmapped run in source
order, with a stderr summary of duration and reason per unknown. Labels are
never merged by temporal proximity, text, or a forced speaker count. With
`strict-speakers` enabled (config field and `-strict-speakers` flag), any
unresolved material label is an error with the same diagnostics and no
transcript on stdout.

### Main implementation boundaries

`internal/audio/` owns chunk planning, sample selection, prefix assembly,
discovery, continuous verification, and stitching. The shared segment parser
remains unchanged. `internal/vendors/openai/` continues to own only OpenAI
request behavior. Suggested injected units are `ChunkPlanner`,
`SampleExtractor`, `AudioAssembler`, `SpeakerRegistry`, and `LabelMapper`;
ffmpeg and transcription calls must remain mockable for deterministic tests.

### Required tests

Tests must cover successful calibration across permuted local labels, several
new speakers in one chunk, spurious labels collapsing onto an existing voice,
mixed sample rejection and replacement, exact request-interval mapping,
byte and seconds budget enforcement, silence-aligned boundaries, bounded
discovery iterations, cache reuse, prefix removal, serialized discovery with
frozen-registry parallelism, best-effort `unknown` rendering, strict failure,
and cancellation. The end-to-end fixture contains annotated speaker intervals
and measures attribution quality; six output strings alone are not evidence.

## Temporary Investigation Recording

This local recording is the acceptance case while adaptive calibration is
developed. These machine-specific paths are temporary and must be removed from
the architecture document after the regression is resolved:

```text
audio:      /home/lorkin/Recordings/meetings/2026-09-01T05-19-04Z_release-week-launch.wav
            pcm_s16le, 16 kHz, mono, 5065 s, 162 MB
transcript: /home/lorkin/Recordings/meetings/2026-09-01T05-19-04Z_release-week-launch.json
expected:   six meeting participants
```

Observed during the investigation:

| Attempt                     |    Result | Lesson                                                                                       |
| --------------------------- | --------: | -------------------------------------------------------------------------------------------- |
| Original split              | 21 labels | Request-local labels were appended without reconciliation.                                   |
| Forced six-label post-pass  |  6 labels | Right count, wrong attribution risk from temporal-neighbor merging. Removed.                  |
| Whole 84-minute 32-kbit MP3 |  HTTP 400 | The provider's per-request duration cap (≈1400–1500 s) rejects it; compression is not at fault. |

Labels per 633 s chunk in the original split, with the count of labels having
at least 2 s of speech:

| Chunk | Labels | ≥ 2 s |
| ----: | -----: | ----: |
|     1 |      4 |     3 |
|     2 |      9 |     6 |
|     3 |      8 |     6 |
|     4 |     13 |     8 |
|     5 |      7 |     5 |
|     6 |      6 |     6 |
|     7 |     20 |     6 |
|     8 |     12 |     7 |

The generated six-label file was experimental and must not be treated as a
correctly diarized transcript. Acceptance requires six acoustically verified
identities and no count-forcing post-pass.

## Tool Bridge

`audio_transcribe` (built-in tool) is a thin adapter over the same engine:
`pkg/tools` declares the schema (`file_path` required; `output_format` enum
defaulting to `text` — model context rarely wants VTT framing) and delegates
to an engine function injected by the clai runtime. The tool layer contains no
transcription logic. Select it with `-t audio_transcribe` or `-t "*"`; inspect
it with `clai tools audio_transcribe`.

The engine loads `audioConfig.json` itself, so the model does **not** come
from the calling command's config. A query, loop or chat can still configure
the tool for the run with `-am`/`-af`, which
`audio.SetTranscribeOverrides` layers on top (model overrides the file;
format overrides the tool call's own `output_format`, since an explicit
user choice outranks the model's per-call pick). An invalid `-af` fails at
setup, not mid-conversation. Without the flags the tool keeps using the
config file and its per-call format.

## Example

```bash
clai a t meeting.wav | clai -p meetingnotes q "File these meeting notes: {}"
# or agentically:
clai -t "*" q "Transcribe meeting.wav and file the meeting notes"
# picking the transcription model the tool uses for this run:
clai -am gpt-4o-transcribe-diarize -af json -t audio_transcribe q "Summarize meeting.wav"
```

## Capture: `audio_sources` and `audio_record`

Two built-in tools capture this machine's audio with one `ffmpeg` process.
`internal/capture` owns discovery, the argument line, the stop handshake and
the final file; `internal/audio` owns the `capture` configuration and the
result text; `pkg/tools` holds only the schemas. Both tools are registered
exactly when `ffmpeg` resolves through `PATH`.

`audio_sources` is read-only. Its first line is
`system_audio: available` or `system_audio: unavailable`; every further line
is `<kind> "<name>" [<label>]`, with ` default` on the source a role
selection picks. A name is what `audio_record`'s `devices` argument takes.

`audio_record` records the microphone, the system output, or both, and answers
with six lines in this order: `recorded`, `started_at`, `ended_at`,
`duration_seconds`, `stopped_because`, `sources`. The tool writes one status
row to stderr and nothing to stdout.

### Capture configuration

The `capture` section of `audioConfig.json` holds the defaults. A tool call
argument overrides the file; the file overrides the built-in default. The file
holds no device names, so it stays portable.

| Field             | Default        | Meaning                                                  |
| ----------------- | -------------- | -------------------------------------------------------- |
| `dir`             | `~/Recordings` | Directory of the output file; a leading `~/` is expanded |
| `max-seconds`     | `5400`         | Hard stop; `0` disables it                               |
| `silence-timeout` | `600`          | Stop after this many seconds of silence; `0` disables    |
| `silence-peak-db` | `-40`          | Average level a frame must reach to count as sound       |
| `sinks`           | `default`      | `default`, `all` or `none` system outputs                 |
| `mic`             | `true`         | Record the default microphone                            |
| `calibrated`      | `false`        | Silence levels were measured or accepted; see below      |

An unknown `sinks` value, a negative duration or a positive
`silence-peak-db` is an error that names the field before any recording
starts.

### Silence calibration

`clai audio calibrate` measures the room and writes the threshold it derives.
It runs `ffmpeg` against the default microphone for 3 seconds, reads the
`lavfi.astats.Overall.RMS_level` that `astats` prints for every frame, about
20 a second, and keeps the middle frame. The median and not the loudest frame,
because `silencedetect` compares a frame's average power to the threshold: one
keyboard click would otherwise set a threshold above speech, and the silence
stop would then fire in the middle of a meeting. The written threshold is that
level rounded to the nearest decibel plus 6 dB of headroom, and never above -1
dB, so even a loud room yields a level below full scale. The verb writes
`silence-peak-db` and `calibrated: true` into `audioConfig.json`, keeps every
other field, and prints:

```text
mic "alsa_input.pci-0000_00_1f.3.analog-stereo" · ambient level -44 dB RMS · silence-peak-db -40 → -38
```

A microphone that reports digital silence (`-inf`) is muted or disconnected:
the verb refuses, names the cause, writes nothing and fails. Only the default
microphone is measured, because the room, not the system output, sets the
noise floor. Accepting the defaults with `[A]` sets the same flag without a
measurement.

### Capture pipeline

One `ffmpeg` process receives one input per source. The sources are mixed with
`amix=normalize=1`, so equal weights cannot clip, and `silencedetect` reports
the silence events that time the silence stop. The process writes a 16 kHz mono
signed 16-bit PCM WAV into a `.part` file; on every stop the recorder sends at
most one `SIGINT`, waits for ffmpeg to finalize the RIFF header and renames the
file into place. One interrupt only: ffmpeg exits before it finalizes the WAV
when it receives a second `SIGINT`. The process carries no context, because the
terminal's `SIGINT` reaches clai and ffmpeg together and ffmpeg needs about
300 ms to finalize; a caller cancellation waits one second for the process to
stop on that signal before it sends its own.

The recording ends at the first of the silence timeout, `max-seconds`, a
completed line on an interactive stdin, or cancellation of the tool call.
Each ending names its own reason. End of input is not a line: `/dev/null` is a
character device too, so a non-interactive run keeps recording until the
silence timeout or `max-seconds`. Cancellation still
finalizes the file and returns an error that contains the saved path. The
samples of the finalized WAV decide whether audio was heard, not the silence
events: a source that never pauses emits no event at all. A recording in which
no sample reached the silence threshold produces no file.

While a recording runs, one stderr row names the silence state, the threshold
that decides it, the destination directory and every stop that can end it:

```text
▸ recording  ⠋ 0:12 · ▁ silent 00:06 / 10m · threshold -40 dB · /home/<user>/Recordings · stops: [Enter], 10m silence, max 1h30m
```

`▮ sound` means a frame of the last window reached `silence-peak-db`; `▁
silent` counts how long the input stayed below it and names the silence stop
that counters it. Both come from the same `silencedetect` events that drive
the silence stop, so the row shows what the stop sees and not a second
measurement. On a terminal the row spins, counts elapsed time and erases
itself before the tool result, so the result's `recorded:` line is the only
place the final path appears: the recorder names the file when ffmpeg starts.
Off a terminal the row is one plain line naming the destination and the
stops, without the state, the threshold and `[Enter]`, which no terminal is
watching.

When `capture.calibrated` is false and stdin is a terminal, one line sits
above the row:

```text
silence levels may not match this machine · run 'clai audio calibrate', or [A] and Enter to acknowledge
```

`[A]` then Enter stores `calibrated: true` and prints the outcome above the
row; any other line stops the recording. A store that fails is reported and
never ends the recording: a meeting must not be lost because a convenience
flag could not be written.

| Platform | Backend                        | Discovery                               | System audio                           |
| -------- | ------------------------------ | --------------------------------------- | -------------------------------------- |
| linux    | pulse (pipewire or pulseaudio) | `ffmpeg -sources pulse`, `-sinks pulse` | output monitors                        |
| darwin   | AVFoundation                   | `ffmpeg -f avfoundation -list_devices`  | a loopback input such as BlackHole 2ch |

`examples/profiles/meeting-transcriber.json` is the worked example: it
inspects, records and transcribes with these tools. The design record is
[`worklogs/2026-10-28-audio-record-tool/`](../worklogs/2026-10-28-audio-record-tool/).
