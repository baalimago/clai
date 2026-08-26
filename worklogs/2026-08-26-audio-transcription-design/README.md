# Worklog: `clai audio` — transcription

Effort started: 2026-08-26. Repo: clai, branch `feat-audio-processing`.

## Status board

| Phase | File                                                                     | Status              | Summary                                                                                               |
| ----- | ------------------------------------------------------------------------ | ------------------- | ----------------------------------------------------------------------------------------------------- |
| 1     | [phase-1-segments-and-renderers.md](./phase-1-segments-and-renderers.md) | Complete            | Segment model, wire-format normalization, vtt/srt/text/json renderers                                 |
| 2     | [phase-2-generic-transcriber.md](./phase-2-generic-transcriber.md)       | Complete            | OpenAI-protocol multipart transcriber + openai/openrouter vendor structs; F1-01 fixed (diarize `chunking_strategy`) |
| 3     | [phase-3-ffmpeg-split-stitch.md](./phase-3-ffmpeg-split-stitch.md)       | Complete            | Best-effort ffmpeg chunking, parallel transcription, offset stitching                                 |
| 4     | [phase-4-mode-wiring.md](./phase-4-mode-wiring.md)                       | Complete    | `audio transcribe` subcommand, config, flags, routing; R1-01 fixed (stdin `-` extension sniffed) |
| 5     | [phase-5-audio-transcribe-tool.md](./phase-5-audio-transcribe-tool.md)   | Complete            | `audio_transcribe` built-in tool as thin adapter over the same engine                                 |
| 6     | [phase-6-docs-and-quality-gates.md](./phase-6-docs-and-quality-gates.md) | Complete            | `architecture/audio.md`, usage text, full QA sweep                                                    |

Router: take the first incomplete or reopened phase; order is strict (each phase builds on the previous).

## Severity taxonomy

- **Critical** — wrong externally observable behavior, data loss, or a QA-gate violation. Reopens the phase.
- **Major** — unmet contract row, missing error coverage, or missing test evidence. Reopens the phase.
- **Minor** — style, naming, doc nits with no behavioral impact. Logged, does not reopen.

## Strategy (shared invariants — read before any phase)

- **Pipes stay clean:** stdout carries only the rendered transcript; every status/progress/warning line goes to stderr via `ancli`.
- **Shared segment model** (defined in phase 1, consumed by all later phases):

  ```go
  // internal/audio package
  type Segment struct {
      Start   time.Duration
      End     time.Duration
      Speaker string // "" when model does not diarize
      Text    string
  }
  ```

  All vendor payloads normalize into `[]Segment`; all output formats render from it; chunk stitching offsets it. No later phase parses vendor JSON or emits vtt/srt directly.

  `Segment` is never marshaled directly for output — `time.Duration` would emit nanosecond ints. The `json` output format renders through a render-time DTO with `start`/`end` as **float seconds** (`{"start": 5.28, "end": 9.04, "speaker": "A", "text": "…"}`, `speaker` omitted when empty); defined in phase 1.

- **Response-format negotiation:** always request the richest machine format the endpoint supports — `diarized_json` when the model name contains `diarize`, else `verbose_json`. Never request `text`/`srt`/`vtt` from a vendor (OpenRouter 400s on them); those are local renderings.
- **Default output format is `vtt`** (`text|srt|json` selectable). Speaker labels render as WebVTT voice tags `<v A>` / SRT `A: ` prefixes.
- **Vendor pattern mirrors text:** generic engine in `internal/audio/generic/`, thin vendor structs in `internal/vendors/{openai,openrouter}/` embedding it (env key + URL + prefix-trim in `Setup()`), explicit routing in `internal/create_queriers.go` (`or:` prefix → OpenRouter; `whisper`/`transcribe` substring → OpenAI). Vendor-specific logic never leaks into `internal/audio/` or `internal/audio/generic/`.
- **File extensions are part of the transcription contract** (elevated by review 1, root cause of R1-01): the vendor infers upload format from the multipart filename and ffmpeg infers the chunk container from the output pattern's extension. Any code path that synthesizes a file the engine will touch (stdin temp files, chunk files, future tool-written files) must give it a real audio extension and thread that extension to every consumer (multipart part name, chunk pattern, error-message hints).
- **External binaries are best-effort and loud:** ffmpeg/ffprobe are optional prerequisites invoked through an injected command-runner (testable without ffmpeg installed); their use is announced on stderr, their absence produces an actionable error, never silent degradation.
- **Repo QA gates apply to every phase:** `make qa` — gofumpt, staticcheck, `go vet`, `go test ./... -race -cover -count=3 -timeout=30s`, `go fix`, dupl. New code needs 70%+ coverage, 90%+ preferred. Tests first, implementation second. No new third-party dependencies.

## Design decisions (agreed 2026-08-26, design session with Lorentz)

1. **No audio capture in clai.** Files only; recording stays in userland (`pw-record`, ffmpeg, OBS).
2. **Two surfaces, one engine.** Subcommand `clai audio transcribe <file>` (aliases `a t`; chat-style noun namespace, room for `audio generate` etc.) plus built-in tool `audio_transcribe` as a thin adapter over the same querier.
3. **Generic mode-as-tool bridge, shaped not built.** Any querier mode can become a tool later (`image_generate`, `video_generate` return file paths by reference; transcribe returns text). Ship only `audio_transcribe`; generalize on the second consumer. Tool naming: `<modality>_<verb>`, verbs describe actual capability (`audio_load` rejected — reserved for true multimodal attachment).
4. **Normalize inside, render locally.** Forced by per-provider `response_format` fragmentation (survey below).
5. **Long files: best-effort ffmpeg split** (OpenAI cap 25 MB; 1 h meetings exceed it). Fixed-duration chunks, no overlap, chunk-relative timestamps offset and stitched locally, bounded parallel requests (`parallelism`, default 3).
6. **Vendors: OpenAI + OpenRouter first** via one generic OpenAI-protocol client; default model `whisper-1` (expected to rotate; single config field). HF ASR = future third implementation (divergent protocol).

### Protocol survey (2026-08)

| Surface                            | text             | srt/vtt | verbose_json | diarized_json |
| ---------------------------------- | ---------------- | ------- | ------------ | ------------- |
| OpenAI `whisper-1`                 | ✓                | ✓       | ✓ (+word ts) | —             |
| OpenAI `gpt-4o-transcribe`         | ✓                | —       | —            | —             |
| OpenAI `gpt-4o-transcribe-diarize` | —                | —       | —            | ✓             |
| OpenRouter `/audio/transcriptions` | 400!             | 400!    | ✓            | —             |
| HuggingFace ASR task API           | own chunked JSON |         |              |               |

- OpenAI multipart `/v1/audio/transcriptions` is the de facto standard; OpenRouter's endpoint (2026-07-22) is compatible (multipart or base64 `input_audio` JSON) but rejects `text`/`srt`/`vtt` with 400.
- HF's OpenAI-compatible router is chat-only; ASR uses HF's own task API.
- Diarization: `gpt-4o-transcribe-diarize` → `diarized_json`, labels `A:`/`B:`; up to 4 reference clips via `known_speaker_names[]`/`known_speaker_references[]`.

### Accepted trade-offs

- **Diarize label drift across chunks:** labels are per-request; chunk 1's "A" ≠ chunk 2's "A". Mitigate with identical `known_speaker_references[]` per chunk; warn when diarize meets a split file (phase 3).
- **Parallelism forfeits whisper `prompt` conditioning** (sequential-only); `parallelism: 1` could re-enable later.
- **Chunk-boundary word clipping** with no-overlap splits; overlap/dedup deferred.

## Target user story

```bash
clai a t meeting.wav | clai -p meetingnotes q "File these meeting notes: {}"
# or agentically:
clai -t "*" q "Transcribe meeting.wav and file the meeting notes"
```

## Session journal

- **2026-08-26 (Claude, design session):** Q&A design with Lorentz (decisions above), protocol survey via web research, worklog structured into phases. No implementation yet.
- **2026-08-26 (Claude, worklog validation):** Pre-implementation validation. All repo references verified sound. Findings V1-01…V1-05 filed and resolved in place (see feedback index). Verdict after fixes: ready.
- **2026-08-26 (Claude, phase 1 implementation):** `internal/audio/` created: `Segment`, `ParseVerboseJSON`/`ParseDiarizedJSON`, `Offset`, `Render` (vtt/srt/text/json), `ParseOutputFormat`. Doc-derived fixtures (no live keys). 98.4 % coverage, full `make qa` green. Deltas: text trimmed at parse, ms-precision durations. Phase 1 Complete.
- **2026-08-26 (Claude, phase 2 implementation):** `internal/audio/generic/Transcriber` (streamed multipart POST, format negotiation, ctx-aware) + `openai.TranscriberDefault`/`openrouter.TranscriberDefault` vendor structs. All contract rows tested against `httptest.Server`. `make qa` green (one pre-existing `anthropic/Test_context` flake under full-suite load, passes isolated). Phase 2 Complete.
- **2026-08-26 (Claude, phase 3 implementation):** `audio.Splitter` + `ExecRunner`/`CommandRunner` seam: size gate, ffmpeg/ffprobe hard-require on split path, plan-offset stitching, bounded pool with ctx-cancel, drift + diarize warnings on injected stderr writer. All contract rows faked, 93.2 % package coverage, `make qa` green. Phase 3 Complete.
- **2026-08-26 (Claude, phase 4 implementation):** `clai audio transcribe` / `a t` wired end to end: `AUDIO` mode + verb dispatch (`internal/setup_audio.go`), `audioConfig.json` (united migration + defaults), `-am/-af/-parallelism` flags, `CreateAudioQuerier` routing (or:→openrouter, whisper|transcribe→openai, test→mock), stdin `-` input, completion registry, usage text. 11 e2e tests + routing/input/override units. `make qa` green. Phase 4 Complete.
- **2026-08-26 (Claude, phase 5 implementation):** `audio_transcribe` built-in tool: schema in `pkg/tools` with injected engine func (wired from `internal` init), registered in the tool registry, mock-vendor inputs via `CLAI_MOCK_AUDIO_TRANSCRIBE_*`. Routing shared with the subcommand via `createAudioSplitter`; mock gained diarize labels (`test-diarize`). 6 tooling e2e tests + adapter units, `make qa` green. Phase 5 Complete.
- **2026-08-26 (Claude, phase 6 implementation):** `architecture/audio.md` + index row, `examples.md` audio section, usage pipe example, setup wizard "model files" exclusion for audioConfig (general-config listing already covered it via `*Config.json` glob). Final sweep: `make qa` exit 0 on branch tip, coverage audit recorded (internal/audio 91.6 %). Phase 6 Complete — **effort done, all phases Complete**.
- **2026-08-26 (Claude, review 1):** Independent code review of all six phases. Gates re-run on the branch tip: `make qa` → exit 0 (38 `ok` package lines of 40 packages, 2 without tests — phase 6 notes said 39, trivial count discrepancy, nothing failed). All acceptance criteria, contract rows, and error rows re-traced against the code and tests, not the notes; shared invariants (pipes clean, negotiation, temp cleanup, ctx-cancel, bounded pool) verified branch-by-branch — details in each phase's "Review findings (review 1)" section. Three findings: **R1-01 (Major, phase 4 reopened)** — stdin `-` creates an extensionless temp file; the ffmpeg split path is confirmed broken for it by live reproduction (the chunk pattern inherits the empty extension and the segment muxer errors), and the OpenAI multipart filename likely fails vendor format detection — the `-` contract row is only proven against the mock. **R1-02 (Minor, phase 3)** — ≥1000-chunk inputs stitch out of order (lexical glob vs `%03d` overflow), ~20 GB to trigger. **R1-03 (Minor, phase 2)** — the promised diarize `known_speaker_references[]` follow-up note never landed anywhere. New cross-phase invariant elevated to Strategy: file extensions are part of the transcription contract. **Verdict: ships clean through the gates, but not ready** — the advertised stdin flow fails at the real boundary; phase 4 reopened to fix R1-01, minors logged without reopening.
- **2026-08-26 (Claude, R1-01 fix):** Phase 4 reopened→Complete. Stdin `-` container is now sniffed from the first 12 bytes (`audio.DetectExtension`, pure Go — no ffprobe dependency, keeping external binaries best-effort) and the temp file named `clai-audio-stdin-*.<ext>`; unrecognized bytes fail pre-flight listing the recognized formats (wav/mp3/flac/ogg/m4a/mp4/webm — exactly OpenAI's accepted set), before any temp file exists. Chunk pattern and manual-split hint inherit the extension untouched. Seam proven both sides: `internal.TestStdinMultipartFilename` asserts the vendor-visible multipart filename through the real `generic.Transcriber` against `httptest`, and the R1-01 ffmpeg reproduction re-run with the fixed naming splits cleanly. Magic table validated against real ffmpeg-generated files of all seven containers. New e2e for the unrecognized-stdin error path; stdin fixtures upgraded to real RIFF/WAVE magic. `DetectExtension` 100 % covered, `make qa` exit 0. **All phases Complete again — ready for re-review.**
- **2026-08-26 (Claude, F1-01 field fix):** Lorentz's first live diarize run 400'd: OpenAI requires `chunking_strategy` for diarization models, which the protocol survey missed and no fixture asserted. Phase 2 reopened→Complete in-session: failing test first (`TestTranscribe_DiarizedModel` now asserts `chunking_strategy=auto`; `TestTranscribe_VerboseJSON` pins its absence for non-diarize), then `writeMultipart` sends the field iff `diarized_json` is negotiated — keyed to the format, not a vendor, so the generic layer stays vendor-agnostic. Contract row updated; `make qa` exit 0.

## Feedback index

Pre-implementation validation round (2026-08-26), all resolved by spec edits:

| ID    | Severity | Phase | Finding                                                                                                      | Resolution                                                                                    |
| ----- | -------- | ----- | ------------------------------------------------------------------------------------------------------------ | --------------------------------------------------------------------------------------------- |
| V1-01 | Major    | 1     | `json` render contradicted README struct tags (ns ints vs float seconds); no acceptance criterion for `json` | Render-time DTO with float seconds; struct tags dropped; criterion added                      |
| V1-02 | Minor    | 1     | `testdata/` fixtures nonexistent, no acquisition procedure for "real" payloads                               | Provenance rule: live capture when keys available, else doc-derived; source cited per fixture |
| V1-03 | Minor    | 3     | `segment_time` needed total duration but ffprobe was optional                                                | ffprobe now a hard requirement on the split path; formula pinned                              |
| V1-04 | Note     | 1     | Empty render pinned only for vtt/text                                                                        | srt → empty string, json → `[]` added                                                         |
| V1-05 | Note     | 5     | Tool schema sketch could be transcribed literally                                                            | Marked illustrative; encode in `internal/tools` schema types                                  |

Review 1 (2026-08-26, post-implementation code review):

| ID    | Severity | Phase                                 | Finding                                                                                                                                                                                   | Status                  |
| ----- | -------- | ------------------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ----------------------- |
| R1-01 | Major    | [4](./phase-4-mode-wiring.md)         | stdin `-` temp file has no extension → ffmpeg split confirmed broken (chunk pattern inherits empty ext), vendor multipart filename likely rejected; `-` flow proven only against the mock | Resolved 2026-08-26 — container magic sniffed into temp-file extension; multipart seam + ffmpeg pattern proven |
| R1-02 | Minor    | [3](./phase-3-ffmpeg-split-stitch.md) | ≥1000 chunks stitch out of order: `chunk_%03d` overflow vs lexical glob sort (~20 GB input)                                                                                               | Logged, does not reopen |
| R1-03 | Minor    | [2](./phase-2-generic-transcriber.md) | Deferred diarize `known_speaker_names/references` follow-up was never noted in phase 4+ or docs; README's drift mitigation has no landing place                                           | Logged, does not reopen |

Field reports (2026-08-26, live usage after review 1):

| ID    | Severity | Phase                                 | Finding                                                                                                                                        | Status                                                                          |
| ----- | -------- | ------------------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------- | -------------------------------------------------------------------------------- |
| F1-01 | Major    | [2](./phase-2-generic-transcriber.md) | Live diarize request 400s: OpenAI requires `chunking_strategy` for diarization models; transcriber never sent it, no test asserted the field | Resolved 2026-08-26 — `chunking_strategy=auto` sent iff `diarized_json` negotiated |
