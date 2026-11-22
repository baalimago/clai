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
    → audio → v|voices → a|add, l|list  # the voice library (voicescmd); no querier
    → audio → transcribe subcommand (Subcommander tree, internal/audio/cmd.go)
      → no file: transcribe's help and the error on stderr, exit 1
      → setupAudioTranscribeQuerier()
      → resolveAudioInput()           # positional file, or '-' → stdin temp file
                                      # (container sniffed → real extension, see sniff.go)
      → LoadConfigFromFile("audioConfig.json")
      → applyFlagOverridesForAudio()
      → resolveKnownVoices()          # -voices without ffmpeg stops here (Known voices)
        → Library.Select(-voices, max-voices)     # whom to name, read once
      → CreateAudioQuerier()          # vendor routing + splitter
  → querier.Query(ctx)                # transcribe → name → tally → render → stdout
```

## Key Files

| File                                        | Purpose                                                                                               |
| ------------------------------------------- | ----------------------------------------------------------------------------------------------------- |
| `internal/audio/cmd.go`                     | audio Subcommander tree — transcribe\|help verbs (see [cmd-dispatch.md](./cmd-dispatch.md))           |
| `internal/audio/board.go`                   | Adapter over the shared progress table ([board.md](./board.md)): diarization columns, `unknownSoFar` |
| `internal/audio/request_status.go`          | The status of a transcription sent as one request (see Progress)                                      |
| `internal/audio/request_retry.go`           | `resending`: a transcriber whose request is sent again on the schedule (see Request retries)          |
| `internal/audio/generic/stream.go`          | `WithHeard`/`ReportHeard`, `ParseStream`: a diarized transcript streamed as server-sent events        |
| `internal/audio/setup_transcribe.go`        | Transcribe-querier setup, stdin `-` input resolution                                                  |
| `internal/audio/audio.go`                   | `Segment` model, JSON parsing, `vtt\|srt\|text\|json` rendering, `Offset`                            |
| `internal/audio/split.go`                   | `Splitter`: byte-cap gate; routes by `Diarization()`: plain ffmpeg chunking, the request-local re-encode, or calibration |
| `internal/audio/plan.go`, `assemble.go`     | `ChunkPlanner` (silence-aligned cores under the seconds budget), `AudioAssembler` (PCM timeline, MP3 encode, manifests) |
| `internal/audio/sample.go`, `merge.go`, `registry.go`, `mapping.go` | Sample extraction, the chained merge of survey candidates (`ChainMerger`), the speaker registry, label mapping |
| `internal/audio/coordinator.go`, `stitch.go`, `cache.go` | The survey path (survey, merge, labelling pass, join, retry) with request accounting, `unknown-N` stitching, run-scoped request cache |
| `internal/audio/eval.go`                    | Acceptance evaluator: annotation parsing, one-to-one attribution report                              |
| `internal/audio/sniff.go`                   | `DetectExtension`: container magic → file extension                                                   |
| `internal/audio/querier.go`                 | `Configurations`, `TranscribeQuerier`, mock transcriber                                               |
| `internal/audio/generic/transcriber.go`     | Generic OpenAI-protocol multipart transcription client                                                |
| `internal/vendors/openai/transcribe.go`     | OpenAI vendor struct (`OPENAI_API_KEY`, api.openai.com)                                               |
| `internal/vendors/openrouter/transcribe.go` | OpenRouter vendor struct (`OPENROUTER_API_KEY`, `or:` prefix trim, `+diarize` suffix, extra headers)  |
| `internal/vendors/openrouter/transcribe_diarize.go` | `ParseDiarized`: OpenRouter's diarized `verbose_json` to speaker turns                        |
| `internal/audio/create_querier.go`          | `audio.CreateQuerier()`/`createSplitter()` — model routing; audio-transcribe tool bridge initialization |
| `pkg/tools/audio_tool_transcribe.go`        | `audio_transcribe` built-in tool (thin adapter, injected engine)                                      |
| `internal/audio/known_voices.go`            | Known voices in a transcription: the ffmpeg stop, the selection, naming with the route's own transcriber, the tally, the naming warnings; the runtime's voices dependencies |
| `internal/audio/namer.go`, `naming.go`      | `Namer`: two naming requests of picked clips and candidates, agreement; the naming report and its JSON |
| `internal/audio/voices/`                    | The voice library: `Library` (the files, the event log, ranking) and `Inspector` (imports nothing of `internal/audio`) |
| `internal/audio/voicescmd/`                 | `clai audio v\|voices a\|add\|l\|list`, mounted under `audio` beside `transcribe` (`voicescmd.Command`); its menu draws through `internal/menu` |
| `pkg/tools/audio_tool_remember_voice.go`    | `audio_remember_voice` built-in tool (injected `AudioRememberVoiceDeps`)                               |

## Configuration

### `audioConfig.json`

```json
{
  "transcribe": {
    "model": "whisper-1",
    "output-format": "vtt",
    "max-request-bytes": 26214400,
    "max-request-seconds": 1400,
    "max-voices": 12
  }
}
```

### Key Fields

| Field                      | Description                                                          |
| -------------------------- | -------------------------------------------------------------------- |
| `transcribe.model`         | Transcription model; routed to a vendor by name (see Vendor Routing), or a root-level `model-aliases` verb such as `fast` (see [model-aliases.md](./model-aliases.md)) |
| `transcribe.output-format` | Local render format: `vtt` (default), `srt`, `text`, or `json`       |
| `transcribe.parallelism`   | Caps the requests in flight when a large file is split. Unset (absent, 0 or negative, the default): the speaker labelling path sends every request of a stage at once, the plain path 3 chunks at a time. A positive value caps both paths, the merge's requests included; a file that holds one keeps it |
| `transcribe.max-request-bytes`   | Per-request upload cap for both split paths (default 25 MiB); zero means default, negative is an error |
| `transcribe.max-request-seconds` | Per-request audio cap for calibrated diarization (default 1400 s) |
| `transcribe.max-voices`          | People named by default: everyone in the voice library with a confirmed clip, best first, up to this many (default 12; absent means 12). 0 names nobody unless `-voices` or a tool call does; negative is a load error. It never caps the people named |

### Flag Overrides

| Flag                    | Config Field               |
| ----------------------- | -------------------------- |
| `-am` / `-audio-model`  | `transcribe.model`         |
| `-af` / `-audio-format` | `transcribe.output-format` |
| `-parallelism`          | `transcribe.parallelism`   |
| `-max-request-bytes`    | `transcribe.max-request-bytes`   |
| `-max-request-seconds`  | `transcribe.max-request-seconds` |
| `-voices`               | — the people to name, comma separated (Known voices); not a config field |

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
3. Transcribes chunks in a bounded worker pool (`parallelism`, 3 when unset).
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

## Progress

Every transcription reports its progress under the title `audio_transcribe`,
whichever route it takes, so a caller can tell a slow transcription from a
stuck one:

| Route | Progress |
| --- | --- |
| one request (the file as it is) | a status: `r.wav · 1 request · 19.2 MB`, `heard mm:ss` while a streaming provider answers, then `transcribed · N segments` or the failure |
| encoded for one request (request-local labels) | a status: `r.wav · 55.3 MB · encoding for 1 request`, then `requesting · 13.8 MB` and as above; when the encoded file is still over the byte cap, `encoded … MB, over the 25 MB request cap: splitting`, the chunk board, and the status's end after it |
| split, calibrated | the chunk board (one row per chunk or core); the speaker labelling board's words are below |

A status has no rows. Under `-json` with no `StatusOut` each state change is
one `progress` record on the run's stderr (`start`, `update`, `done` or
`failed`; the detail is the text above), on a terminal it is a live row that
erases itself, and any other writer gets one line per state change.

`heard mm:ss` is the end of the latest segment the provider has sent: the
splitter attaches a `generic.Heard` to the request's context
(`generic.WithHeard`), and a transcriber whose vendor sets `Stream` asks for
`stream=true` and reads the answer as server-sent events
(`generic.ParseStream`) only when one is attached. OpenAI's diarize family
streams (measured 2026-11-16 with `gpt-4o-transcribe-diarize`, 80 s of
speech): `transcript.text.segment` events carrying the fields of a
`diarized_json` segment arrive in bursts of about 26 s of audio, 10.4, 19.1,
26.0 and 27.6 s after the request, then `transcript.text.done` and `[DONE]`;
the same file unstreamed answered after 33.2 s. A stream that ends before
`transcript.text.done` is `generic.ErrStreamEnded`, never a short transcript.
The calibrated route attaches no `Heard`, so its requests are not streamed.

The speaker labelling board (Adaptive Speaker Calibration) has the plain
board's columns, marks and records, with its own words:

| Element | Text |
| --- | --- |
| header | `speaker labelling  r.wav  5 chunks × 17m31s  ≤23 requests` (3 per chunk plus 2 per merge link) |
| phases, in order | `finding voices · 5 chunks`, `matching voices · 31 clips` (the candidates), `labelling · 5 chunks · 6 speakers`, `second try · 2 chunks with unmatched voices` (only when a chunk needs one), `stitching`, `done · N segments · 17m elapsed` |
| row, a request in flight | `finding voices · 17m31s`, `labelling · 19m0s`, `second try · 19m11s`; a resend shows `retry 2/4 in 10s · 429`, then `labelling · try 3` |
| row, between survey and labelling | `6 voices found` (the chunk's material survey labels) |
| row, final | `✓ labelled · 6 speakers`, or `✗ 2 unmatched · 6 speakers` (material labels naming nobody); `request failed` when a request of the chunk failed the run; a failed second try keeps the row's labelling result |
| `req` cell | the chunk's requests so far, `2/3` |
| live footer | `≈7m left (≤14m) · 9m elapsed · requests 10/≤23 · resent 0 · in flight 5 · speakers 6 · unknown 0s · uploaded 1h37m`; `in flight` counts rows, so a merge request is not in it |
| final footer | `requests 18/≤23 · resent 0 · retries 0 · cache hits 0 · uploaded 3h18m · speakers 6 · unknown 0s · 17m elapsed`, failures included |
| finishing lines | `found 10 voices; more than 8 usually means one person was not recognised across chunks` when the merge found more than 8, then `chunk 3 (35:00–52:30) second try failed: provider unavailable (503); its labelling result is kept` per failed second try, then the unknown-speech summary |

The estimate counts, per unfinished chunk, its survey and labelling requests
left and the merge links left at the measured rate; the worst case adds a
second try for every chunk.

The mock transcriber (`test`, `mock_test`) answers at once, or, with
`CLAI_MOCK_TRANSCRIBE_DELAY` set to a Go duration, spreads its answer over
that long and reports each of its segments as heard, so a caller's progress
display can be tested without a provider.

## Request retries

Every transcription request is sent again when the provider may answer it
later: the single request, each plain chunk, every request of the speaker
labelling path (`survey`, `merge-forward`, `merge-reversed`, `labelling`,
`retry`) and both naming requests. The mechanism and the schedule
are the text query's (`internal/resend`, [query.md](./query.md#request-retry)):
waits of 1s, 10s, 30s and 60s, then the last error fails the run as before.

- Sent again: a rate limit (429), a provider failure (5xx) and a request
  that got no answer (`resend.Throttled`). The generic transcriber gives a
  failed status its `claierr` meaning through the vendor's decoder, and a
  failed `client.Do` is `claierr.ErrTransport`.
- Never sent again: a rejected key, an empty account (a 429
  `insufficient_quota` included), a request the provider rejected, and a
  failure inside an answer that had begun.
- A wait is never shorter than the provider's `Retry-After`
  (`claierr.APIError.RetryAfter`, seconds or an HTTP date).
- The wait happens inside the worker that holds the request, so the other
  chunks go on.
- A request sent again is the same request: there is no per-chunk limit to
  spend, and it is not one of the chunk's requests. The footer counts them as
  `resent`, beside `retries`, the second tries the labelling path sent.
- The row shows `retry 2/4 in 10s · 429` (`· no answer` without a status)
  while it waits, then `<purpose> · try 3`.
- Once the schedule is spent a model alias moves on to its next model, as
  for any unavailable model.
- `Splitter.Retry` is a collaborator: `createSplitter` sets
  `resend.Default()`, and the zero value sends once.

## Adaptive Speaker Calibration

Implemented 2026-09-02 ([`worklogs/2026-09-01-adaptive-speaker-calibration/`](../worklogs/2026-09-01-adaptive-speaker-calibration/),
cited as `asc`: the planner, assembler, mapper, budgets and provider facts);
its chunk-by-chunk speaker discovery was replaced on 2026-11-22 by the survey path
([`worklogs/2026-11-20-survey-calibration/`](../worklogs/2026-11-20-survey-calibration/README.md)).
An oversized recording transcribed with an anchored diarizer (OpenAI's
diarize family, the provider the anchoring was measured on) takes this path
instead of the plain split above: every chunk is heard once (the survey), the
voices it found are merged into one registry, and only then is every chunk
transcribed against that registry (the labelling pass). On the 88-minute
Retro the H1 measurement of that worklog took 18 requests and about 17
minutes, against 12 serial requests and 67.8 minutes for the chunk-by-chunk
path before it.

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
a prefix label in two of two forward-order requests. Provider facts that
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
   design recovers from both (the reversed order of the second try, the join)
   and never treats a single request as proof of identity.
4. Many clips in one request merge voices (H1, 2026-11-22): 25 candidate
   clips of the Retro in one request gave two people one label in both
   orders, while 7 clips kept six people apart. Hence the chained merge below,
   of 9–13 clips per request. Five survey requests at once all answered in
   5.2–6.3 minutes with no 429.

Provider-native `known_speaker_names`/`known_speaker_references` are not used
in the pipeline. The stated cap is four references of 2–10 s, which cannot hold
a six-speaker meeting, and in the spike they misassigned a whole speaker.

### Request budgets and encoding

Every request is bounded by two configurable budgets: `max-request-bytes`
(default 25 MiB; the encoded file plus a fixed multipart envelope reserve
must fit) and `max-request-seconds` (default 1400 s). A zero config value
means the default; a negative value is a load-time error. The planner runs once per recording and
sizes each core interval as the seconds budget minus a fixed prefix reserve
minus a 2% safety margin, then checks the encoded bytes. The reserve is fixed
at 8 sample slots of 11 s (88 s), the most voices the path labels, so no
prefix pushes a core over the budget and nothing is replanned. The tighter of the two budgets wins. Every request, the merge's
clips-only requests included, is assembled and checked against both budgets
before upload; a request of samples and no core extracts no core audio.

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

### Survey and merge

A local label is **material** when it has at least 3 s of speech and at least
1% of the chunk's speech. Non-material labels are still mapped when they match
a clip; otherwise their speech is kept and rendered as `unknown-N`. A segment
without a speaker is no label for any decision below.

1. **Survey.** Every chunk is transcribed once, all at once, with no prefix
   (kind `survey`). Its labels are used to cut candidates and for nothing
   else: no survey segment reaches the output.
2. **Candidates.** Each material label of each survey answer gives one
   candidate: its best clip of 3–10 s (longest speech, then clearance from
   other labels), named `c<chunk>-<label>`. A material label with no such run
   gives none.
3. **Merge** (`ChainMerger`, `merge.go`). The candidates of the first chunk
   that has any each begin one voice, with no request: the survey already kept
   one chunk's voices apart. Every later chunk with candidates is one link, in
   chunk order, each sent only when the one before it is decided: two
   requests at once, made only of clips and no core, holding the best clip of
   every voice so far and that chunk's candidates, forward by source start
   and in exactly the opposite order (kinds `merge-forward`,
   `merge-reversed`). A clip is settled in a request when one label holds at
   least 80% of its labelled speech. Clips settled in both orders on the same
   pair of labels are one voice: a candidate with a voice's pair joins that
   voice, two voices with one pair become one, candidates sharing a new pair
   begin a voice together, and an unsettled clip joins nothing.
4. **Registry.** The voices become `speaker-1`, `speaker-2`, … numbered by
   each voice's earliest clip in source time, each with its clips ranked
   (longer speech, larger clearance, earlier start); its sample is the first.
   More than 8 voices is a sign the merge failed to group someone, not a real
   meeting: the 8 with the most survey speech are kept, the others' speech
   becomes `unknown-N`, and the board's finishing lines say
   `found 10 voices; more than 8 usually means one person was not recognised across chunks`.

A recording with no material label sends no merge request; its labelling
requests equal its surveys and are answered by the cache.

### Speaker sample registry

The registry holds one global speaker ID per voice the merge found, with the
voice's ranked clips; the first is the speaker's sample. Registry entries are
recording-local and claim no real-world identity.

```text
speaker-2
  clips: source 00:00:18.0–00:00:28.0, 00:15:00.0–00:15:10.0
  sample: the first clip
```

It is built by the merge and changes at most once more, by the join, which
only joins speakers: no speaker is added after the merge, and no sample
changes for the run. A speaker the survey heard in no chunk stays `unknown-N`.

### Calibration prefix and mapping

**Labelling pass.** Every chunk is transcribed once more, all at once (kind
`labelling`), with one clip per registry speaker, its sample, ordered by
source start:

```text
[speaker-1 sample][silence][speaker-2 sample][silence]...[core audio]
```

The splitter knows every sample interval in request time. Labels returned
inside those intervals map request-local labels to global speakers:

```text
speaker-1 sample → local C
speaker-2 sample → local A
target local C → speaker-1
target local A → speaker-2
```

**Join.** After every labelling request has answered, and once: two speakers
agree when at least 2 labelling requests settle both their clips and every
request that settles both gives them the same label. Speakers are taken in
registry order; a speaker joins the earliest earlier speaker it agrees with,
unless some request settled it on a different label than a speaker already
joined there. Two speakers whose survey candidates of one chunk held labels of
their own do not agree when that chunk's labelling answer holds a material
label no registry clip settled on: the survey heard two people there, and the
labelling request heard one's clip as the other's, so a clip is bad and a voice
is left over (the 88-minute Retro, 2026-11-23). A survey that splits one
person leaves nothing over, and still joins. The `join refused` record names
such pairs. The joined speaker holds the clips of both, and the registry is
numbered again by earliest clip, so the output has no gap. Labelling records
carry the IDs before the join; the `join` and `registry` records give the
mapping.

**Mapping.** In a labelling or second-try answer, a label names a speaker when
the registry clips settled on it all belong to one speaker after the join. A
label on which clips of two speakers settled names nobody: identity is never
merged by guesswork.

**Second try.** A chunk whose labelling answer leaves a material label naming
nobody is sent exactly once more (kind `retry`), with everything fixed at
once: the registry clips in the opposite order when clips of two speakers
settled on one label; for every speaker none of whose clips settled on a
label of its own (no clip settled, or its clip shared a label with another
speaker's), its next-ranked clip not yet sent for this chunk, in that request
only; and,
after the registry clips, one candidate clip for each material label no
registry clip settled on, cut from that label's speech in the labelling
answer, most speech first, as many as keep the prefix at 8 clips. A second
try that would equal the labelling request is not sent. Speech under a label
only a candidate settled on names nobody. The second try's answer replaces
the labelling answer only when it maps more speech; otherwise the labelling
answer stays.

**Length.** A run sends at most 3 requests per chunk (survey, labelling,
second try) plus 2 per merge link, one link per chunk after the first: 23 for
5 chunks. No stage runs twice and none waits on an answer to end.
`parallelism` unset sends every request of a stage at once; a set value caps
the requests in flight, the merge's pair included.

### Stitching and failure policy

For every chunk, the splitter takes its kept answer, maps labels to global
IDs, removes the prefix intervals, restores core timestamps from the
sample-count manifest, and appends segments in source order; no codec
timestamp correction is applied (the spike measured a median shift below the
tolerance). Every output segment comes from a labelling or second-try answer.
A run-scoped in-memory cache keyed by request bytes hash, model, endpoint,
and request options prevents paying twice for an identical request.

Speech whose label cannot be mapped is rendered as
`unknown-N`, numbered at stitch time per voice: one unmapped local label of
one chunk is one `unknown-N` for all its runs, in order of first appearance.
The provider keeps a label to one voice within a request, so this merges
nothing the provider did not; the same voice in two chunks is still two
names. The stderr summary gives duration, run count and reason per unknown
voice. Labels are never merged by temporal proximity, text, or a forced
speaker count. There is no strict mode: an unresolved label never fails
the run, because `unknown-N` can be named afterwards and a failed run loses
the whole transcription (`strict-speakers` was removed on 2026-11-22; an old
config that still sets it loads, as unknown fields are ignored).
`max-speakers` was removed with the survey path: room for 8 voices is fixed,
and an old config that still sets it loads.

What fails the run: a failed plan; a survey, merge or labelling request that
fails at assembly or a budget check, is refused for the key or the account,
or is not answered once its resends are spent; and an ended context at any
stage. The run then returns the error with its cause, cancels the requests in
flight, removes its files and leaves the board with its footer. A failed
second try does not fail the run, whatever the cause but an ended context:
the chunk keeps its labelling answer, its unmatched speech stays `unknown-N`,
and the finishing lines say `chunk 3 (35:00–52:30) second try failed:
<cause>; its labelling result is kept`, since the second try only improves
labels and an account running out of credit is likeliest at the end of a
long run.

### Investigating a run

`LOG_LEVEL=warn,audio=debug` writes the calibration's decisions to stderr,
one `calibration <event> key=value ...` line per event, with no transcript
text, audio, key or header. Chunk lines carry `chunk=N attempt=M`, where
`attempt` is the chunk's request count so far; a merge request's lines carry
`link=N`, N being the chunk whose candidates the link holds. Times are source
seconds unless a key says otherwise.

| Event | Says |
| --- | --- |
| `plan`, `plan-core` | the budgets, `voices=8`, the 88 s reserve, the core target and silences, `parallelism=all\|<n>` and `requests_max` (3 per chunk plus 2 per link); each core's planned and aligned end with the reason |
| `request` | the `kind` (`survey`, `merge-forward`, `merge-reversed`, `labelling`, `retry`), core (`0.0-0.0` for a merge request), request seconds and bytes, `cache=hit\|miss`, and the prefix in order as `id@source>request` intervals |
| `request-retry` | a request about to be sent again: its `kind`, `try=n/4`, the `delay` in seconds and the `cause` (`"rate limited (429)"`, `"provider unavailable (503)"`, `"connection failed: …"`). The chunk's `attempt` does not move |
| `response`, `sample`, `label` | per survey, labelling and second-try answer: the verdict counts and `took`, the seconds the request took (0.0 on a cache hit); each sample's dominant label, share and verdict; each local label's core speech, materiality and global id, or the reason it has none. A labelling answer's records carry the IDs before the join |
| `candidate` | per material survey label: the clip cut as `id=c<chunk>-<label>`, its speech, clearance and the label's survey speech, or `id=- reason=no-run-of-sample-length` |
| `merge` | per clip of a link: both verdicts as `label:share` and the outcome (`held`, `merged`, `joined`, `new`, `unsettled`) with its voice; per voice found: its speaker (`-` over the cap of 8), clips, sample and survey speech; then `state=done links= found= kept=` |
| `join` | per join: `from=`, `into=`, `shared=` (the requests that settle both) and `apart=0`; per pair the survey kept apart: `refused a= b= shared= chunks=` (the chunks whose survey heard both and whose labelling answer left a voice no clip settled on); then `state=done joined= speakers=` |
| `registry` | only after a join: each speaker with the IDs it held before the join (`was=speaker-1+speaker-3`), its clip count and sample |
| `retry` | per chunk with a material label naming nobody: `state=start` with `reversed`, `swapped` (`speaker@old>new`), `candidates` and `pending`; then `outcome=accepted` or `outcome=kept reason=no-gain` with `mapped`, `unknown` and `material_unmapped` as `before>after`, or `outcome=kept reason=request-failed cause="…"`; or `state=skipped reason=nothing-to-change` |
| `accepted`, `unmapped` | once per chunk, when its answer is final: the local-to-global map, its `requests`, `resent` and `retries`, mapped and unknown seconds, and each unmapped label with its reason |
| `unknown`, `stitched` | which `unknown-N` names each chunk produced, with voice and run counts, and the run totals |
| `naming`, `naming-speaker`, `naming-voice` | known voices: the candidate clip cut per speaker and whom it was named, or why nobody |

Every HTTP request adds `transcription request` (URL, model, format) and
`transcription response` (status, body bytes or segment count, `took`); a
plain chunk or a naming request sent again adds `transcription retry
request=chunk-2/5 try=… delay=… cause=…`. The response body is shown only at
`audio=trace`. A debug log of an 88-minute recording is about 40–60 KB, most
of it these records: the agent turn around it adds one line per model
response and two per tool call ([output.md](./output.md), "Diagnostics"),
not content.

### Main implementation boundaries

`internal/audio/` owns chunk planning, sample selection, prefix assembly, the
survey, the merge, the labelling pass, the join, the second try and
stitching. The shared segment parser remains unchanged.
`internal/vendors/openai/` continues to own only OpenAI request behavior. The
injected units are `ChunkPlanner`, `SampleExtractor`, `AudioAssembler`,
`Merger` (`ChainMerger`) and `LabelMapper`; ffmpeg and transcription calls
must remain mockable for deterministic tests.

### Required tests

Tests must cover, over the scripted diarizer and the fake runner only: every
chunk labelled from a survey whose first chunk holds few of the people, no
survey segment in the output, the fixed 88 s reserve, the merge (its own
contract in `merge_test.go`), the join and the cases that join nothing, the
second try fixing everything at once and kept only when it maps more, a voice
matching nobody kept as one `unknown-N`, more than 8 voices, `parallelism`
unset and capped, the fixed path length, every request kind sent again, each
real failure and the failed second try that does not fail the run, the event
log and the board's words, and the path end to end through the `Splitter`
with the real assembler. The end-to-end fixture contains annotated speaker
intervals and measures attribution quality; six output strings alone are not
evidence.

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
defaulting to `text` — model context rarely wants VTT framing; `voices`, an
optional array of the people expected, see Known voices; a `voices` that is
not an array of strings is refused before anything is transcribed) and
delegates to an engine function injected by the clai runtime,
`AudioTranscribeEngine(ctx, AudioTranscribeCall)`. The tool layer contains no
transcription logic. Select it with `-t audio_transcribe` or `-t "*"`; inspect
it with `clai tools audio_transcribe`.

The engine loads `audioConfig.json` itself, so the model does **not** come
from the calling command's config. A query, loop or chat can still configure
the tool for the run with `-am`/`-af`/`-voices`, which
`audio.SetTranscribeOverrides` layers on top (model overrides the file;
format overrides the tool call's own `output_format`, since an explicit
user choice outranks the model's per-call pick; `-voices` names the people
of every call that names nobody of its own). An invalid `-af` fails at
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

## Known voices

A diarized transcript names the people the user already knows instead of
labelling them A, B. The library is `<clai-config>/voices/`
(`internal/audio/voices`): short clips per person, ranked by how their
namings fare. It grows by hand (`clai audio voices add <wav> [-from s]
[-to s] [-name n]`), is browsed and corrected with `clai audio voices list
[person [clip [action]]] [-json]`, and grows on its own through the
`audio_remember_voice` tool, whose clips stay unconfirmed until the user
keeps them. `v`, `a` and `l` are the short names of `voices`, `add` and
`list`: `clai a v l lorentz` is `clai audio voices list lorentz`.

Whom a transcription names is resolved once, before the recording is
transcribed, by `Library.Select`: the `audio_transcribe` call's `voices`
argument when it names anyone, else the run's `-voices` (`clai audio
transcribe -voices 'Lorentz,Christoph' …`, or `-voices` on `query`/`chat`
for the tool, beside `-am`/`-af`), else everyone with a confirmed clip up to
`transcribe.max-voices`. A name the library does not hold is reported as
`unknown-voice` and never fails the run.

**Nobody to name changes nothing.** With an empty selection (nobody named and
no confirmed clip, no `voices/`, or an unreadable library) the run makes
today's requests and prints today's bytes; nothing under `voices/` is
written. Reading the library for the selection is the only access.

**Otherwise** the recording is transcribed as always (one request, a split or
calibration), then `Namer.Name` puts the selection's picked clips before one
candidate clip of each transcript speaker in two naming requests (forward and
reversed order), both built first and then sent at once to the transcriber
that transcribed the recording, within the run's request budgets; a failed
send cancels the other. A speaker takes a person's name only when both orders
agree. The picked clips' outcomes are tallied into
the library once (`placed`/`named` events). Naming never fails a
transcription: a failed request, no agreement, an unreadable library or a
failed tally leave the speakers as transcribed and say why in the report; only
the transcription's own failure fails the run. No clip audio leaves the
machine except inside a naming request, and none is in any output. Nothing of
this names a vendor or a model.

**ffmpeg is a dependency of naming people, not a naming failure.** When people
are named (the call's `voices`, else `-voices`) and `ffmpeg` does not resolve
through the route's `CommandRunner.LookPath`, the run stops before the library
is read and before any request, with a `*VoicesNeedFFmpegError` (`-voices
needs ffmpeg …`, or `voices needs ffmpeg …` for the tool's argument): exit 1
on `clai audio transcribe`, a failed tool result on the tool. With nobody named
ffmpeg is not looked for: the empty selection needs none, and a default
selection without it is transcribed and its naming is a failed request.

**`clai audio transcribe` warns of what the naming did not do**, one
`runenv.Warnf` warning of the `audio` subsystem per case (in text `warn
[audio] <message>` on stderr), after the transcript is written; stdout is the
same with or without them, and nothing is asked. With people named: `no voice
"<name>" in the library` per unknown person, `voice library unreadable:
<reason>; speakers left as labels`, `naming failed: <reason>; speakers left as
labels`, `the transcript has no speaker labels; nobody named`, `no speaker
talks long enough to compare; nobody named`, and `failed to find a match for
<name>, were they in the meeting?` per person absent or not placed. Under the
default selection only `naming failed: …` warns. An empty selection warns of
nothing, and the `audio_transcribe` tool never warns: its result is its whole
answer (`known_voices.go`, `namingWarnings`).

Output with a non-empty selection:

- `json` is one object, `{"segments": […], "naming": {…}}`, indented like
  the bare array. A segment a clip named has its `speaker` set to the
  person's plain name and `voice: {"clip": "<slug>/<id>", "status":
  "confirmed"|"unconfirmed"}` between `speaker` and `text`; any other segment
  is as transcribed. `naming` is the report: `status`, `origin` (`tool`,
  `flag` or `library`), `speakers` (each label's name and voice, or its
  `reason`), `voices` (each person's `result`), and `error`/`unrecorded`
  when set.
- `vtt`, `srt` and `text` are today's rendering with the name where the
  label stood, written `<name> (unconfirmed)` when an unconfirmed clip gave
  it, and nothing else.

`CLAI_MOCK_TRANSCRIBE_SCRIPT` is the test seam: set to a JSON array of
replies, the `test`/`test-diarize` mock answers a run's n-th Transcribe call
with the n-th reply (`{start, end, speaker, text}` in seconds), so end-to-end
rows and goldens drive a naming without a provider. The two naming requests,
sent at once, take the next two replies by their order (forward, then
reversed), whichever arrives first.

The voices CLI and `audio_remember_voice` get the same library and an
`Inspector` over `ExecRunner` from the runtime (`openVoices`,
`pkg/tools.AudioRememberVoiceDeps`, set in `create_querier.go`'s `init`), read
from `utils.GetClaiConfigDir` on every use. `voices` is a verb of `audio`,
beside `transcribe`; `transcribe` takes a file and nothing else, so
`clai audio transcribe voices` transcribes a file named `voices`. With no
file, `transcribe` prints its help (the text of `clai audio transcribe -h`)
and the missing-file error on stderr and exits 1, as `clai audio` does with
no verb.

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
# speakers named after the voice library (flags before the file):
clai audio voices add rec.wav -from 61.2 -to 69 -name Lorentz
clai audio transcribe -am gpt-4o-transcribe-diarize -voices 'Lorentz,Christoph' -af json meeting.wav
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
completed line on an interactive stdin, the stop file, or cancellation of the
tool call. Each ending names its own reason. End of input is not a line:
`/dev/null` is a character device too, so a non-interactive run keeps
recording until the silence timeout or `max-seconds`.

The stop file is the Enter of a run without a terminal, such as one a program
drives in the background: `CLAI_AUDIO_STOP_FILE` names a path, and the
recording ends with `stopped_because: stop_file` once something creates it
(checked every 200 ms, with `lstat`, so any kind of file counts). A path that
already exists when the recording starts is a `StaleStopFileError` before
anything records: it is left from an earlier stop and would end this one at
once. The status row lists it as `stop file`. The
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
