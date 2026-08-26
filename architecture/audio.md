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
  → internal.Setup(ctx, usage, args)
    → parseFlags()                     # extract CLI flags
    → getCmdFromArgs()                 # returns AUDIO mode
    → handleAudio()                    # verb dispatch (transcribe|help)
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
| `internal/setup.go` | `Setup()` AUDIO case — dispatches to `handleAudio` |
| `internal/setup_audio.go` | Verb dispatch, namespace help, stdin `-` input resolution |
| `internal/audio/audio.go` | `Segment` model, `verbose_json`/`diarized_json` parsing, `vtt|srt|text|json` rendering, `Offset` |
| `internal/audio/split.go` | `Splitter`: 25 MB size gate, ffmpeg chunking, bounded-parallel transcription, offset stitching |
| `internal/audio/sniff.go` | `DetectExtension`: container magic → file extension (vendors and ffmpeg infer format from filenames) |
| `internal/audio/querier.go` | `Configurations` (`audioConfig.json` schema), `TranscribeQuerier`, mock transcriber |
| `internal/audio/generic/transcriber.go` | Generic OpenAI-protocol multipart transcription client |
| `internal/vendors/openai/transcribe.go` | OpenAI vendor struct (`OPENAI_API_KEY`, api.openai.com) |
| `internal/vendors/openrouter/transcribe.go` | OpenRouter vendor struct (`OPENROUTER_API_KEY`, `or:` prefix trim, extra headers) |
| `internal/create_queriers.go` | `CreateAudioQuerier()`/`createAudioSplitter()` — model → vendor routing |
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

`CreateAudioQuerier()` in `internal/create_queriers.go` routes explicitly:

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
2. Probes total duration, splits into fixed-duration chunks targeting ~20 MB
   each (`-f segment -c copy`, temp dir, removed afterwards).
3. Transcribes chunks in a bounded worker pool (`parallelism`), offsets each
   chunk's timestamps by its planned start, and stitches in order.
4. Warns on stderr when measured chunk durations drift > 1 s from the plan,
   and when a diarize model meets a split file (speaker labels are
   per-request and may drift across chunks).

## Tool Bridge

`audio_transcribe` (built-in tool) is a thin adapter over the same engine:
`pkg/tools` declares the schema (`file_path` required; `output_format` enum
defaulting to `text` — model context rarely wants VTT framing) and delegates
to an engine function injected by the clai runtime. The tool layer contains no
transcription logic. Select it with `-t audio_transcribe` or `-t "*"`; inspect
it with `clai tools audio_transcribe`.

## Example

```bash
clai a t meeting.wav | clai -p meetingnotes q "File these meeting notes: {}"
# or agentically:
clai -t "*" q "Transcribe meeting.wav and file the meeting notes"
```
