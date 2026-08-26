# Phase 4: Mode wiring — subcommand, config, flags, routing

**Status:** Complete
[← README](./README.md)

## Goal

Make `clai audio transcribe <file>` (alias `a t`) a first-class mode: config file, flag overrides, querier creation, and usage text.

## Specification

- **Command parsing** (`internal/setup.go` / `getCmdFromArgs`): new mode `AUDIO` with verb subcommands, chat-style — `audio transcribe <file>` and aliases `a t`. Unknown verb under `audio` prints namespace help and errs. `audio help` lists verbs.
- **Config**: `audioConfig.json` in the config dir, loaded via `LoadConfigFromFile`, registered so `clai confdir`/setup discover it:

  ```json
  {
    "transcribe": {
      "model": "whisper-1",
      "output-format": "vtt",
      "parallelism": 3
    }
  }
  ```

- **Flag overrides** (photo/video style, applied in `applyFlagOverridesForAudio`): `-am/-audio-model`, `-af/-audio-format` (validated via phase-1 `ParseOutputFormat`), `-parallelism`. `-r/-raw` continues to mean no glow/animation (transcript printing is already raw by nature; flag accepted, no-op beyond suppressing any stderr animation).
- **Input resolution**: positional file path required; `-` reads audio bytes from stdin into a temp file (scriptability) whose extension is sniffed from the container magic bytes (extensions are part of the transcription contract — README strategy, R1-01); unrecognized container errs pre-flight listing recognized formats. Missing/nonexistent path errs before querier creation.
- **Querier routing** (`internal/create_queriers.go`, `CreateAudioQuerier`): explicit prefix routing per README strategy — `or:` → openrouter transcriber; model containing `whisper` or `transcribe` → openai; model `test`/`mock_test` → mock transcriber (mirrors text mock path) enabling e2e tests without network. Unmatched model → error listing supported routes.
- **Querier** implements `models.Querier` (`Query(ctx) error`): resolves size gate → phase-3 orchestration or phase-2 direct call → phase-1 render → stdout write.
- **Usage text** in `main.go` gains the `a|audio t|transcribe <file>` command row and the new flags (final wording pass happens in phase 6).
- **E2E**: `main_audio_e2e_test.go` following existing `main_*_e2e_test.go` conventions, using the mock model.

## Integration contract

| Scenario | Collaborators | Observable result | Required side effects | Prohibited side effects |
| -------- | ------------- | ----------------- | --------------------- | ----------------------- |
| `clai audio transcribe f.wav` (mock model) | mock transcriber, temp config dir | rendered VTT on stdout, exit 0 | — | status text on stdout |
| `clai a t f.wav -af text` | same | plain text on stdout | — | VTT header present |
| `clai a t f.wav -am or:openai/whisper-1` | routing unit test | openrouter transcriber selected | — | openai selected |
| `clai audio` (no verb) / unknown verb | — | namespace help on stderr, non-zero exit | — | panic |
| `cat f.wav \| clai a t -` | mock transcriber | transcript on stdout | temp file cleaned | — |
| missing `audioConfig.json` | defaults | works with `Default` config; file created per repo convention for mode configs | — | crash |

## Acceptance criteria

- [x] All contract rows covered by e2e or focused unit tests (cited per row), passing `-race -count=3 -timeout=30s`:
  - mock VTT on stdout + audioConfig.json created from defaults: `Test_goldenFile_AUDIO_transcribe_vtt`
  - `a t -af text` alias, no VTT header: `Test_goldenFile_AUDIO_alias_and_text_format`
  - `or:` → openrouter routing (plus openai/mock/diarize routes): `internal.TestCreateAudioQuerier_Routing`
  - no verb / unknown verb → help on stderr, exit 1, stdout empty: `Test_goldenFile_AUDIO_namespace_help`
  - `cat f.wav | clai a t -` transcript on stdout: `Test_goldenFile_AUDIO_stdin_dash`; temp file cleanup proven in `internal.TestResolveAudioInput` (dash subtest)
  - missing audioConfig.json → defaults + file created: `Test_goldenFile_AUDIO_transcribe_vtt`
- [x] Flag overrides demonstrably beat config file values (defaults → file → flags). — `Test_goldenFile_AUDIO_config_cascade` (file beats default vtt; `-af json` beats file's text) and `internal.TestApplyFlagOverridesForAudio`
- [x] `clai help` output includes the audio command and flags. — `Test_goldenFile_AUDIO_help_includes_audio` (`a|audio`, `-am`, `-af`, `-parallelism`)
- [x] Coverage ≥ 70 % on new wiring code. — `go tool cover -func`: `handleAudio` 100 %, `setupAudioTranscribeQuerier` 100 %, `resolveAudioInput` 85 %, `CreateAudioQuerier` 95.7 %, `TranscribeQuerier.Query` 81.2 %

## Error coverage

| Failure condition | Expected outcome | Test |
| ----------------- | ---------------- | ---- |
| Nonexistent input file | pre-flight error naming the path, non-zero exit, no querier created | `Test_goldenFile_AUDIO_missing_input_file`, `internal.TestResolveAudioInput` |
| Invalid `-af` value | error listing valid formats | `Test_goldenFile_AUDIO_invalid_format_flag`, `internal.TestCreateAudioQuerier_Routing` (invalid format subtest) |
| Unroutable model name | error listing supported routes/prefixes | `Test_goldenFile_AUDIO_unroutable_model`, `internal.TestCreateAudioQuerier_Routing` (unroutable subtest) |
| Missing vendor API key | phase-2 `Setup` error surfaced with env-var name, non-zero exit | `Test_goldenFile_AUDIO_missing_api_key` |
| Corrupt `audioConfig.json` | load error naming the file path | `Test_goldenFile_AUDIO_corrupt_config` |

## Implementation notes

**Session:** Claude, 2026-08-26. Files: `internal/audio/querier.go` (+test), `internal/setup_audio.go` (+test), `internal/create_queriers.go` (`CreateAudioQuerier`, +test), `internal/setup.go`, `internal/setup_flags.go`, `internal/completion.go` (+golden updates), `main.go`, `main_audio_e2e_test.go`.

Deltas from spec:

- **Mock transcriber returns a fixed transcript** ("mock transcription" / "of an audio file") instead of echoing the file name — the stdin `-` path uses a random temp name, so echoing would make e2e output nondeterministic. The mock still stats the file so missing inputs fail.
- **`TranscribeQuerier` implements `SuppressCompletionNotification()`** — main.go rings the completion bell as `\a` on stdout, which would corrupt piped transcripts; the audio querier suppresses it (pipes-stay-clean invariant, not in the phase spec).
- **Mode config schema lives in `internal/audio/querier.go`** (`Configurations`/`Default`) beside the querier, matching photo/video convention of conf-in-mode-package.
- **`json` render gets a trailing newline appended at write time** (render itself stays newline-free per phase 1 contract).
- **Completion registry updated** (`a`/`audio` commands, `-am/-af/-audio-model/-audio-format/-parallelism` flags) and the completion golden tests' expected lists/counts updated accordingly — repo maintenance contract, not a test cheat: the lists are the feature.
- **`-r/-raw` needed no code**: transcript printing has no glow/animation path.
- `audioConfig.json` added to the united config migration block in `Setup()` (created/upgraded before dispatch like the other mode configs).

Verification (all green):

- `go test ./internal/ ./internal/audio/ . -race -count=3 -timeout=30s` via full suite → ok
- `make qa` → exit 0

**Session:** Claude, 2026-08-26 (R1-01 fix). Files: `internal/audio/sniff.go` (+test),
`internal/setup_audio.go`, `internal/setup_audio_test.go`, `main_audio_e2e_test.go`.

- Fix implemented as pure-Go magic-byte sniffing (`audio.DetectExtension`, first
  12 bytes) rather than the ffprobe route the finding suggested: external
  binaries are best-effort per Strategy, and requiring ffprobe for every piped
  sub-cap input would have violated that. All formats OpenAI accepts (wav, mp3,
  flac, ogg, m4a, mp4, webm) are magic-sniffable; magic table validated against
  real ffmpeg-generated files of all seven containers.
- Unrecognized stdin bytes now fail pre-flight with the recognized-format list,
  before any temp file exists — strictly better than the prior vendor 400,
  since an unsniffable container was headed for rejection anyway.
- The stdin header is sniffed before `os.CreateTemp`, so the sniff-error path
  creates nothing to clean up; the chunk pattern and manual-split hint inherit
  the extension with no change to `split.go`.
- E2E stdin fixture upgraded to real RIFF/WAVE magic (the old
  `RIFF-fake-wav-from-stdin` bytes were not a valid WAV header); new e2e
  `Test_goldenFile_AUDIO_stdin_unrecognized_format` pins the error path.
- New seam test `internal.TestStdinMultipartFilename` drives the resolved
  stdin path through the real `generic.Transcriber` multipart encoder against
  an `httptest` server and asserts the vendor-visible filename matches
  `clai-audio-stdin-*.wav` — the boundary review 1 found untested.
- Live reproduction of the R1-01 ffmpeg failure re-run with the fixed naming:
  `ffmpeg -i clai-audio-stdin-12345.wav -f segment -segment_time 2.000 -c copy
  chunk_%03d.wav` → two valid RIFF chunks (previously errored extensionless).
- Coverage: `DetectExtension` 100 %, `resolveAudioInput` 86.2 %. `make qa` →
  exit 0.

## Review findings (review 1, 2026-08-26)

Verified good (traced in the code, independently of the notes):

- All six integration-contract rows and five error rows re-traced against
  `main_audio_e2e_test.go`, `internal/setup_audio_test.go`,
  `internal/create_queriers_audio_test.go`. The e2e stdout assertions are
  exact-match (`FailTestIfDiff` against full golden strings), not
  substring-lenient; the config cascade is proven in both directions
  (file-beats-default and flag-beats-file).
- Pipes stay clean on every path: transcript → `Out` (stdout), splitter status
  → injected writer defaulting to stderr, completion bell suppressed via
  `SuppressCompletionNotification`, namespace help on error → stderr with
  empty stdout asserted.
- The stdin temp-file cleanup runs on every branch: config-load error and
  querier-creation error in `setupAudioTranscribeQuerier`, plus the deferred
  call in `Query`; unit-proven for both the dash and the
  must-not-delete-user-files cases.
- `printHelp` format-verb count re-audited against the three new `%v`s in the
  usage string — argument order is correct.

Findings:

- [x] **R1-01 (Major)** — *resolved 2026-08-26, see implementation notes
  (R1-01 fix session): container sniffed from magic bytes, temp file named
  `clai-audio-stdin-*.<ext>`, multipart-filename seam and ffmpeg pattern both
  proven.* — stdin `-` input resolves to an extensionless temp
  file (`internal/setup_audio.go:78`,
  `os.CreateTemp("", "clai-audio-stdin-*")`), and the file extension is
  load-bearing on both downstream sinks:
  1. **Split path — CONFIRMED by reproduction.** `internal/audio/split.go:176`
     builds the chunk pattern as `chunk_%03d` + `filepath.Ext(filePath)`; for
     the stdin temp file the extension is empty and ffmpeg's segment muxer
     cannot infer the per-chunk container. Reproduced (ffmpeg 7.x): the exact
     command the splitter issues, `ffmpeg -v error -i clai-audio-stdin-12345
     -f segment -segment_time 2.000 -c copy chunks/chunk_%03d`, fails with
     `Output file does not contain any stream` / `Error opening output file`;
     the identical command with `.wav` appended to the pattern succeeds. So
     any piped input over 25 MB hard-fails. The manual-split hint at
     `split.go:127` also renders with an empty extension in this mode.
  2. **Vendor request — PLAUSIBLE, boundary untested.**
     `internal/audio/generic/transcriber.go:125` names the multipart file part
     `filepath.Base(file.Name())`. OpenAI validates upload format from that
     filename's extension, so `clai-audio-stdin-123456` is expected to be
     rejected (400, invalid/unsupported file format) even under the size cap.
     Every `-` test runs against the mock, which stats the file and ignores
     its name — the mock passes on both sides of this severed seam.

  **Failure scenario:** `cat meeting.wav | clai a t -` with a real API key →
  vendor 400 (sub-cap) or ffmpeg split error (over-cap), despite the
  integration-contract row and the usage text advertising exactly this flow.

  **Fix:** give the stdin temp file a real extension (sniff the container via
  ffprobe when available, or accept a format hint), ensure the chunk pattern
  and the manual-split hint inherit it, and add a test asserting the multipart
  filename sent for stdin input carries a recognized audio extension.
