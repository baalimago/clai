# Audio Command Architecture

Command: `clai [flags] audio transcribe <file>` (aliases: `a t`)

The **audio** command namespace transcribes audio files using AI models
(currently the OpenAI multipart transcription protocol, served by OpenAI and
OpenRouter). The rendered transcript is the only thing written to stdout, so
it pipes cleanly; all status/progress lines go to stderr. Use `-` as the file
to read audio bytes from stdin.

## Entry Flow

```
main.go:run()
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

| File | Purpose |
|------|---------|
| `internal/audio/cmd.go` | audio Subcommander tree — transcribe|help verbs (see [cmd-dispatch.md](./cmd-dispatch.md)) |
| `internal/audio/setup_transcribe.go` | Transcribe-querier setup, stdin `-` input resolution |
| `internal/audio/audio.go` | `Segment` model, `verbose_json`/`diarized_json` parsing, `vtt|srt|text|json` rendering, `Offset` |
| `internal/audio/split.go` | `Splitter`: 25 MB size gate, ffmpeg chunking, bounded-parallel transcription, offset stitching |
| `internal/audio/sniff.go` | `DetectExtension`: container magic → file extension (vendors and ffmpeg infer format from filenames) |
| `internal/audio/querier.go` | `Configurations` (`audioConfig.json` schema), `TranscribeQuerier`, mock transcriber |
| `internal/audio/generic/transcriber.go` | Generic OpenAI-protocol multipart transcription client |
| `internal/vendors/openai/transcribe.go` | OpenAI vendor struct (`OPENAI_API_KEY`, api.openai.com) |
| `internal/vendors/openrouter/transcribe.go` | OpenRouter vendor struct (`OPENROUTER_API_KEY`, `or:` prefix trim, extra headers) |
| `internal/audio/create_querier.go` | `audio.CreateQuerier()`/`createSplitter()` — model → vendor routing; audio_transcribe tool-bridge init |
| `pkg/tools/audio_tool_transcribe.go` | `audio_transcribe` built-in tool (thin adapter, injected engine) |

## Configuration

### `audioConfig.json`

```json
{
  "transcribe": {
    "model": "whisper-1",
    "output-format": "vtt",
    "parallelism": 3
  }
}
```

### Key Fields

| Field | Description |
|-------|-------------|
| `transcribe.model` | Transcription model; routed to a vendor by name (see Vendor Routing) |
| `transcribe.output-format` | Local render format: `vtt` (default), `srt`, `text`, or `json` |
| `transcribe.parallelism` | Max concurrent chunk requests when a large file is split (default 3) |

### Flag Overrides

| Flag | Config Field |
|------|-------------|
| `-am` / `-audio-model` | `transcribe.model` |
| `-af` / `-audio-format` | `transcribe.output-format` |
| `-parallelism` | `transcribe.parallelism` |

Precedence is the standard cascade: flags > file > defaults (see
[`config.md`](./config.md)).

## Vendor Routing

`audio.CreateQuerier()` in `internal/audio/create_querier.go` routes explicitly:

| Model Pattern | Vendor |
|---------------|--------|
| `or:` prefix | OpenRouter (`or:` trimmed from the wire model) |
| contains `whisper` or `transcribe` | OpenAI |
| `test`/`mock_test` prefix | Mock transcriber (deterministic, network-free; contains `diarize` → speaker labels) |

## Response-Format Negotiation

The vendor request always asks for the richest machine format the endpoint
supports: `diarized_json` when the model name contains `diarize`, else
`verbose_json`. `text`/`srt`/`vtt` are never requested from a vendor
(OpenRouter rejects them); those are local renderings from the shared
`Segment` model. Speaker labels render as WebVTT voice tags (`<v A>`), SRT/text
`A: ` prefixes, and a `speaker` field in `json` output (float-second
timestamps).

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

This implementation does not reconcile diarized speakers. The fixed-overlap
experiment was removed because it increased the example meeting from 21 labels
to 57 labels. The adaptive calibration design below replaces that experiment.

## Adaptive Speaker Calibration (Planned)

The diarization provider assigns labels per request. A label such as `A` has no
meaning outside that request. The splitter must therefore carry acoustic
speaker evidence into every request instead of relying only on conversation at
a chunk boundary.

### Speaker sample registry

During one source recording, the splitter maintains a registry with one global
speaker ID and one or more clean audio samples for each discovered voice. A
sample should contain 5–10 seconds of one speaker, have no detected crosstalk,
and include guard silence around the extracted range. Registry entries are
recording-local; they do not claim a persistent real-world identity.

```text
global speaker A
  sample: source 00:02:14.2–00:02:22.0
  quality: isolated=1.0, speech=7.8s, verification=passed
```

Samples remain temporary files and are removed with the other split artifacts.
The implementation must retain source ranges and quality evidence in memory so
that a bad sample can be replaced and the result can be explained.

### Calibration reel

Before a chunk is transcribed, ffmpeg builds one request file with this shape:

```text
[speaker A sample][silence][speaker B sample][silence]...[chunk audio]
```

The splitter knows every sample interval. Labels returned inside those
intervals map request-local labels to global speakers. A mapping is accepted
only when one local label covers a high proportion of the sample speech (the
initial threshold should be 80%) and known samples receive distinct labels.
Calibration segments are removed from the rendered transcript, and the reel
duration is subtracted from the chunk timestamps.

If a sample receives mixed labels, two known samples collapse onto one local
label, or the same sample changes label during verification, the request is
ambiguous. The system retries with a longer or replacement sample; it does not
guess.

### Bootstrap and speaker discovery

1. Transcribe the first core chunk with an overlap but without a reel.
2. Select the longest isolated segment for each provisional local voice.
3. Build a reel from those candidates and transcribe the same chunk again.
4. Samples which collapse onto an existing calibrated label are the same
   speaker and are merged. A distinct sample is promoted to a global speaker
   only after a verification request gives it a stable, distinct label.
5. When a later chunk contains an unmapped label, extract its best isolated
   segment, add one candidate to the reel, and retranscribe that chunk. Repeat
   until every material label maps or no safe sample exists.

More than one new participant can first speak in a chunk. Discovery may add one
speaker per iteration for simpler validation, but it must allow several
iterations on the same chunk. Very short interjections and empty/noise segments
must not create speakers.

### Adaptive overlap retry

The normal overlap starts at 30 seconds. The duplicated transcript is matched
using normalized exact text first, then a high token-similarity threshold with
compatible timestamps. Match evidence is aggregated by local/global speaker
pair and must be one-to-one.

If material labels remain unresolved, retry only the affected boundary with a
larger window, for example 30, 60, 120, then 240 seconds. Each retry includes
the current calibration reel. Stop increasing the window before the complete
request can exceed the vendor's 25 MB limit. Core chunks must target less than
20 MB so the reel and the largest retry have reserved capacity.

Once the speaker registry is stable, later chunks can be transcribed in
parallel with the frozen reel. Discovery and registry changes are sequential;
otherwise concurrent chunks could allocate conflicting global IDs.

### Stitching and failure behavior

For every accepted chunk, the splitter removes calibration intervals, removes
the second copy of overlap audio, offsets the remaining timestamps, and appends
segments in source order. It caches requests by model plus audio/reel hash so a
retry or interrupted run does not pay for identical work twice.

The algorithm may return a transcript only when every material speaker label
has acoustic calibration or high-confidence overlap evidence. At the maximum
safe retry, unresolved labels cause an explicit error with diagnostic details.
They are never merged by temporal proximity and are never forced into a fixed
speaker count.

### Main implementation boundaries

`internal/audio/` owns chunk planning, sample selection, reel construction,
adaptive retries, confidence policy, and stitching. The shared segment parser
remains unchanged. `internal/vendors/openai/` continues to own only OpenAI
request behavior. Suggested injected units are `ChunkPlanner`, `SampleExtractor`,
`SpeakerRegistry`, and `BoundaryMatcher`; ffmpeg and transcription calls must
remain mockable for deterministic tests.

### Required tests

Tests must cover successful calibration across changed local labels, two new
speakers in one chunk, false labels collapsing onto an existing voice, mixed or
unstable sample rejection, geometric overlap growth, the 25 MB retry ceiling,
cache reuse, calibration timestamp removal, sequential discovery followed by
parallel steady-state work, cancellation, and explicit failure without a
guess. An end-to-end fixture must verify that six known voices produce six
evidence-backed labels, not merely six output strings.

## Temporary Investigation Recording

This local recording is the acceptance case while adaptive calibration is
developed. These machine-specific paths are temporary and must be removed from
the architecture document after the regression is resolved:

```text
audio:      /home/lorkin/Recordings/meetings/2026-09-01T05-19-04Z_release-week-launch.wav
transcript: /home/lorkin/Recordings/meetings/2026-09-01T05-19-04Z_release-week-launch.json
expected:   six meeting participants
```

Observed during the investigation:

| Attempt | Result | Lesson |
|---|---:|---|
| Original non-overlapping split | 21 labels | Request-local labels were appended without reconciliation. |
| Fixed 30-second overlap | 57 labels | Most participants did not speak at each boundary, so unmatched labels multiplied. |
| Forced six-label post-pass | 6 labels | The count was right, but temporal-neighbor merging could assign speech to the wrong person. This approach was removed. |
| Whole 84-minute 32-kbit MP3 | HTTP 400 | OpenAI rejected the compressed whole-file experiment as corrupted or unsupported. |

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
from the calling command's config. A query or chat can still configure the
tool for the run with `-am`/`-af`, which
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
