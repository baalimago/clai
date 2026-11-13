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
| `internal/audio/split.go`                   | `Splitter`: byte-cap gate; routes by `Diarization()`: plain ffmpeg chunking, the request-local re-encode, or calibration |
| `internal/audio/plan.go`, `assemble.go`     | `ChunkPlanner` (silence-aligned cores under the seconds budget), `AudioAssembler` (PCM timeline, MP3 encode, manifests) |
| `internal/audio/sample.go`, `registry.go`, `mapping.go`, `discovery.go` | Sample extraction, append-only speaker registry, label mapping, bootstrap and discovery |
| `internal/audio/coordinator.go`, `stitch.go`, `cache.go` | Parallel coordinator with request accounting, `unknown-N` stitching, run-scoped request cache |
| `internal/audio/eval.go`                    | Acceptance evaluator: annotation parsing, one-to-one attribution report                              |
| `internal/audio/sniff.go`                   | `DetectExtension`: container magic → file extension                                                   |
| `internal/audio/querier.go`                 | `Configurations`, `TranscribeQuerier`, mock transcriber                                               |
| `internal/audio/generic/transcriber.go`     | Generic OpenAI-protocol multipart transcription client                                                |
| `internal/vendors/openai/transcribe.go`     | OpenAI vendor struct (`OPENAI_API_KEY`, api.openai.com)                                               |
| `internal/vendors/openrouter/transcribe.go` | OpenRouter vendor struct (`OPENROUTER_API_KEY`, `or:` prefix trim, `+diarize` suffix, extra headers)  |
| `internal/vendors/openrouter/transcribe_diarize.go` | `ParseDiarized`: OpenRouter's diarized `verbose_json` to speaker turns                        |
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

An agent run reads one more source above the file: the profile it runs. A
text command's profile may carry `"audio": {"model": "gpt-4o-transcribe-diarize"}`, which sets
the model its `audio_transcribe` calls use for that run; `-am` overrides the
profile. The profile key is the model only: the format stays the tool call's
choice or `-af`, and a profile does not dictate presentation. The value is
what `-am` takes: a model, a verb from `audioConfig.json`'s `model-aliases`,
or a remote model list (`cchub:…`), which the tool resolves and falls back
through exactly as `clai audio transcribe` does (see Tool Bridge).

## Vendor Routing

`audio.CreateQuerier()` in `internal/audio/create_querier.go` routes explicitly:

| Model Pattern                      | Vendor                                                                              |
| ---------------------------------- | ----------------------------------------------------------------------------------- |
| `or:` prefix                       | OpenRouter (`or:` trimmed from the wire model)                                      |
| `or:` prefix, `+diarize` suffix    | OpenRouter with speaker labels (`+diarize` trimmed too; see OpenRouter speaker labels) |
| contains `whisper` or `transcribe` | OpenAI                                                                              |
| `test`/`mock_test` prefix          | Mock transcriber (deterministic, network-free; contains `diarize` → speaker labels) |

## Response-Format Negotiation

The vendor request always asks for the richest machine format the endpoint
supports. The vendor package knows its model families and sets a typed
`generic.ResponseFormat` on the client (`openai.ResponseFormatFor`):

| Model (OpenAI)                                        | Requested format | Payload                                   |
| ----------------------------------------------------- | ---------------- | ----------------------------------------- |
| contains `diarize` (`gpt-4o-transcribe-diarize`)      | `diarized_json`  | timestamped segments with speaker labels  |
| contains `whisper` (`whisper-1`)                      | `verbose_json`   | timestamped segments                      |
| anything else (`gpt-transcribe`, `gpt-4o-transcribe`, `gpt-4o-mini-transcribe`, dated snapshots) | `json` | `text` only, plus `usage` |

The `gpt-transcribe` family rejects `verbose_json` and `diarized_json` with
HTTP 400 `unsupported_value` (measured 2026-10-30, fixtures in
`internal/audio/testdata/`). Its `json` payload carries no timestamps, so
`generic.ParseJSON` yields one segment per request: start at zero, end at
the billed duration when `usage.type` is `duration` (`gpt-transcribe`), else
zero (`gpt-4o-transcribe` bills tokens). An empty `text` is silence and
yields no segment. The plain split path offsets each chunk's segment by its
window start and clamps its end to the window, because the billed seconds are
rounded up and would otherwise overlap the next chunk by under a second. A
vendor that sets no format falls back to the model-name rule (`diarize` →
`diarized_json`, else `verbose_json`). OpenRouter sets `verbose_json` for every
model, since its endpoint accepts only `json` and `verbose_json`; a slug that
happens to contain `diarize` must not ask for `diarized_json`.

The `json` format also implies a per-request audio-duration cap. Measured
2026-10-30 against `gpt-transcribe` with files under the byte cap: 1713 s and
2998 s succeeded, 5139 s returned HTTP 400 `invalid_value` "Audio file might
be corrupted or unsupported" while `whisper-1` accepted the same file. The
`Splitter` therefore asks the vendor client for its `RequestFormat()` and, for
`json`, probes a sub-byte-cap file with `ffprobe` and splits it when it runs
longer than `max-request-seconds` (default 1400 s, the budget the diarize
spike measured; the config field is shared). An oversized `json` file is
chunked by the tighter of the byte and duration budgets. Without `ffprobe` on
PATH a short file is posted whole and a long one surfaces the vendor's error;
`whisper-1` is never probed, as it has no duration cap. `text`/`srt`/`vtt` are
never requested from a vendor (OpenRouter rejects them); those are local
renderings from the shared `Segment` model. Speaker labels render as WebVTT voice tags (`<v A>`), SRT/text
`A: ` prefixes, and a `speaker` field in `json` output (float-second
timestamps). JSON output is indented with one segment object per block and a
trailing newline so large transcripts remain usable with `jq`, `head`, `tail`,
and line-oriented inspection tools.

## OpenRouter speaker labels

OpenRouter diarizes only when the request carries `response_format=verbose_json`
and a `diarize=true` form field. A model name asks for it with the `+diarize`
suffix: `or:elevenlabs/scribe-v2+diarize`. The suffix is clai's, not
OpenRouter's: OpenRouter's own variants use `:` (`:free`, `:batch`) and stay
part of the slug, so `or:elevenlabs/scribe-v2:batch+diarize` sends
`elevenlabs/scribe-v2:batch`. The vendor package parses the suffix, adds the
field through `generic.Transcriber.Fields`, and parses the payload itself
(`openrouter.ParseDiarized`), because the speaker sits where the generic
`verbose_json` parser does not look and is an integer:

- `words[]` carry `speaker` (integer) and `speaker_label` (`"speaker_0"`);
  `segments[]` may be one block with no speaker (`elevenlabs/scribe-v2`).
  Consecutive words of one speaker become one segment, from the first word's
  start to the last word's end. A word without a speaker, such as an
  `audio_event`, stays in the turn it falls in.
- Labeled words win over labeled segments, because a coarse segment can span
  two speakers. Labeled segments are used when no word carries a label.
- Labels are renamed `A`, `B`, … (then `AA`) in order of first appearance, so
  a transcript reads the same whichever diarizer of a ladder answered.
- Speech without any label is an error (`openrouter.ErrNoSpeakerLabels`),
  never a transcript that silently lost its speakers. Silence is zero
  segments. A `+diarize` anywhere but at the end of the name, or in another
  case, is refused before any request.

Measured 2026-11-13 against the live endpoint with a 21 s two-speaker clip
(multipart, `verbose_json`, `diarize=true`): `elevenlabs/scribe-v2` answered
200 with both speakers correct for $0.0013. `deepgram/nova-3`,
`assemblyai/universal-3-6-pro`, both `mistralai/voxtral-*-transcribe-3-0`,
`x-ai/grok-stt-2.0`, `google/gemini-3.5-transcribe` and
`fish-audio/transcribe-1-pro` answered 400 "The selected model does not
support diarize"; `openai/gpt-4o-transcribe` and `google/chirp-3` answered
400 because they reject `verbose_json`. The 400 reaches the user with the
vendor's text; it is not a fallback reason, like any request failure.

These labels are request-local (`generic.DiarizationRequestLocal`): a label
means nothing outside its request, and no calibration maps it. OpenRouter
caps the file part at 25 MiB and the provider request at 60 s. Measured the
same day: a 26,214,400-byte file passed the size check and a 26,230,000-byte
one was refused ("Audio file exceeds the 25 MB multipart upload limit"), so
the default `max-request-bytes` is OpenRouter's cap exactly. With 64 kbit/s
mono MP3 built from TTS speech, `scribe-v2` answered a 10-minute file
(4.8 MB) in 10.2 s and a 45-minute file (21.6 MB) in 31.9 s, upload
included. One request is therefore bounded by what the request codec fits
under the cap, `requestCodecDuration` (54.6 minutes at 25 MiB, about 39 s at
the measured rate): the `Splitter` probes a request-local file under the
byte cap when `ffprobe` is on PATH and sends a longer one, such as a
low-bitrate voice memo, through the re-encode route below. Without
`ffprobe` the file is posted whole and the vendor's timeout answers.

## Large Files: Split and Stitch

Files over 25 MB exceed the vendor request cap. The `Splitter`
(`internal/audio/split.go`):

1. Requires `ffmpeg` **and** `ffprobe` on PATH (only for oversized files);
   missing binaries produce an actionable error with a manual split command.
2. Probes total duration and splits the input into fixed windows that target
   approximately 20 MB each (`-f segment -segment_time -c copy`). The windows
   divide the probed duration plus one second (`splitSlack`), so the last cut
   falls past the end: windows of exactly duration/N left a 908-byte fourth
   chunk after three 200 s ones of a stream-copied MP3 (frame quantization
   past the probed 600.000 s), which OpenRouter refused with 400 "unsupported
   or malformed audio" (measured 2026-11-13). A fixed window length, rather
   than a list of cuts, keeps every chunk bounded when `ffprobe`
   underestimates the duration, as it does for a VBR MP3 without a Xing
   header. A cue that ends past its window is clamped to the window, never
   before its own start.
3. Transcribes chunks in a bounded worker pool (`parallelism`).
4. Offsets timestamps and stitches chunks in order.
5. Prefixes every speaker label with its chunk when there is more than one
   chunk, because labels are request-local (see below).

The `Splitter` routes an oversized file by what the transcriber's labels mean
(`Diarization()`, typed `generic.Diarization`; a transcriber without the
method has none), never by the model name:

| Diarization | Oversized file |
| --- | --- |
| none | this plain path |
| anchored (OpenAI's diarize family, `diarized_json`) | the calibration path below, which reconciles speakers across requests |
| request-local (OpenRouter `+diarize`) | re-encoded once at the request codec (MP3, 64 kbit/s, mono, 16 kHz), which fits 54.6 minutes under 25 MiB; sent as one request when it fits, else this plain path over the encoded file |

A meeting recorded by `audio_record` is a 16 kHz WAV, 1.9 MB a minute, so
anything past 13 minutes is oversized; the re-encode keeps a meeting of up to
54 minutes in one request with one set of labels. Speaker numbering
restarts in every request, so when the plain path transcribes more than one
chunk it prefixes every label with its chunk: `1-A`, `1-B`, `2-A`. `2-A` is
not known to be `1-A`; two people are never merged under one label, and the
board header says `speaker labels per chunk`, and so does the
`audio_transcribe` tool description, for the agent that reads the result.
Whoever reads the transcript reconciles them from what was said. For `json`-format models the plain path also handles files under the
byte cap that exceed `max-request-seconds` (see Response-Format Negotiation).

## Adaptive Speaker Calibration

Implemented 2026-09-02; the worklog with the measurements and decisions is
[`worklogs/2026-09-01-adaptive-speaker-calibration/`](../worklogs/2026-09-01-adaptive-speaker-calibration/).
An oversized recording transcribed with an anchored diarizer (OpenAI's
diarize family, the provider the anchoring was measured on) takes this path
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
setup, not mid-conversation. A profile's `audio.model` reaches the same
global, resolved before the flags are re-applied (`text.ApplyProfileOverrides`),
so `-am` wins. Without a flag or a profile key the tool keeps using the
config file and its per-call format.

Each call composes its run like the subcommand: the model resolves through
`audioConfig.json`'s `model-aliases` and walks the ladder with
`modelfallback.New` (`transcribeChain`), so a candidate whose key is missing
falls through to the next with the usual notice on the run's stderr
([model-aliases.md](./model-aliases.md)). A remote model list named by `-am`
or a profile is fetched once at text setup (`text.resolveRemoteAudioModel`,
with the run's remote resolver) and handed over as
`ToolOverrides.Models`; a remote list named by `audioConfig.json` itself is
resolved by the engine with the same resolver, handed over as
`ToolOverrides.NewResolver`. The profile checker routes `audio.model`
through the audio alias table (`profile.Env.ResolveAudioModel`), so an alias
is accepted and a malformed one is refused. The table is read only when a
profile names an audio model, from `audioConfig.json` or the shipped ladders
when the file is absent, so a broken audio config never fails a run whose
profile has no audio model.

## Example

```bash
clai a t meeting.wav | clai -p meetingnotes q "File these meeting notes: {}"
# or agentically:
clai -t "*" q "Transcribe meeting.wav and file the meeting notes"
# picking the transcription model the tool uses for this run:
clai -am gpt-4o-transcribe-diarize -af json -t audio_transcribe q "Summarize meeting.wav"
# or pinning it in a profile, for every run of that agent:
clai -p meeting -t audio_transcribe q "Transcribe today's meeting"
# speaker labels through OpenRouter:
clai a t -am or:elevenlabs/scribe-v2+diarize meeting.wav
```

A shared profile that must label speakers on a machine with either key names
a ladder, for example an audio alias published as a remote model list:

```json
["gpt-4o-transcribe-diarize", "or:elevenlabs/scribe-v2+diarize"]
```

With `OPENAI_API_KEY` set the OpenAI diarizer answers; without it the
OpenRouter one does.

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
signed 16-bit PCM WAV into a `.part` file with `-flush_packets 1`, so every
packet reaches the file as it is encoded. Without the flag ffmpeg holds up to
256 KiB in memory, eight seconds of this format, and a killed ffmpeg loses
them (measured 2026-11-10 with ffmpeg 8.0.1: killed after 2.5 s the file was
empty; with the flag it held all but the last packet).

The process runs in its own process group (`group_unix.go`) and carries no
context. A terminal's Ctrl-C therefore reaches clai alone, and the recorder is
the only one that stops ffmpeg: on every stop it sends that group one `SIGINT`,
waits up to two seconds for ffmpeg to finalize the RIFF header, and kills the
group when it is still running. One interrupt only: ffmpeg exits before it
finalizes the WAV when it receives a second `SIGINT`, which is what a second
Ctrl-C did while ffmpeg shared clai's group. Then `settleWAV` checks the two
header sizes against the bytes on disk. A killed ffmpeg leaves `0xFFFFFFFF` in
both; the recorder writes the sizes that follow from the samples and drops a
trailing half sample, so the file plays (`Recording.Finish` is `repaired`,
otherwise `finalized`). The file is then renamed into place.

On WASIX there is no group: `group_other.go` builds there, and the recorder
signals the process it started and no other (`process.go`). The Go port to
WASIX satisfies the `linux` build tag, so the constraint excludes it by name.
Its ffmpeg is a wrapper script around a program that cannot take a signal; the
wrapper ends that program's inputs so it finishes the file, and a group signal
would reach the program past the wrapper and end it with nothing saved (in the
browser machine this hung the wrapper and killed the worker, 2026-11-10).
`TestGroupFileByPlatform` fails when the two files' constraints drift.

The recording ends at the first of the silence timeout, `max-seconds`, a
completed line on an interactive stdin, or cancellation of the tool call.
Each ending names its own reason. End of input is not a line: `/dev/null` is a
character device too, so a non-interactive run keeps recording until the
silence timeout or `max-seconds`. The
samples of the finalized WAV decide whether audio was heard, not the silence
events: a source that never pauses emits no event at all. A recording in which
no sample reached the silence threshold produces no file, unless it was
cancelled.

Cancellation, which is what the first Ctrl-C or `SIGTERM` of a run becomes,
stops the recording the same graceful way and keeps the file at its normal
path. It is kept only, never transcribed: the call returns no facts but a
`*capture.InterruptedError`, whose text is the tool result and the last line
of the notice on stderr (a `progress` record with state `done` and that text
as detail under `-json`):

```text
recording interrupted: saved /home/<user>/Recordings/2026-11-10T10-00-00Z.wav
```

The text adds `; ffmpeg did not finish in time and was killed, so the last
moments may be missing` after a repair, and `; no sample reached the silence
threshold` for a quiet recording, which a cancellation keeps because the
threshold may be wrong for the room and nothing reads the file before the user
does. A cancelled recording without a single sample leaves no file and says
`recording interrupted: nothing saved: <cause>`, state `failed`.

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
