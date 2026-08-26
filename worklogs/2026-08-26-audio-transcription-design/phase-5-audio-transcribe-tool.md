# Phase 5: `audio_transcribe` built-in tool

**Status:** Complete
[← README](./README.md)

## Goal

Expose the transcription engine as the built-in tool `audio_transcribe` so a model can transcribe files mid-query — the first consumer of the mode-as-tool bridge.

## Specification

- Register `audio_transcribe` in the built-in tool registry (`internal/tools/`, following the existing built-in tool pattern; see `tooling.md`). Listed by `clai tools`, detail view prints its JSON schema, selectable via `-t audio_transcribe` and included in `-t "*"`.
- Tool schema (illustrative sketch — encode it in the actual `internal/tools` schema types, matching how existing built-ins declare required fields and enums):

  ```json
  {
    "name": "audio_transcribe",
    "description": "Transcribe a local audio file to text. Returns the transcript; timestamps and speaker labels included for formats that carry them.",
    "input": {
      "file_path": {"type": "string", "required": true},
      "output_format": {"type": "string", "enum": ["text", "vtt", "srt", "json"], "default": "text"}
    }
  }
  ```

  Tool default is `text` (model context rarely wants VTT framing), unlike the subcommand's `vtt` default — deliberate divergence, documented in the description.
- Executor is a **thin adapter**: loads `audioConfig.json` (model/parallelism from user config), builds the same querier path as phase 4 (size gate → split → transcribe → render), returns the rendered string as the tool result. No transcription logic in the tool layer — the bridge principle from README decision 3.
- stderr progress lines from phase 3 still go to stderr during tool execution (visible to the user, not injected into model context).
- Errors return as tool-result errors (standard tool error surface), not process exits.
- Respects existing tool-call budget flags (`-mtc`); nothing special-cased.

## Integration contract

| Scenario | Collaborators | Observable result | Required side effects | Prohibited side effects |
| -------- | ------------- | ----------------- | --------------------- | ----------------------- |
| tool call `{file_path: f.wav}` (mock transcriber via test model config) | tool executor, mock engine | tool result contains transcript text | — | transcript on process stdout |
| `{file_path: f.wav, output_format: "vtt"}` | same | result begins `WEBVTT` | — | — |
| `clai tools` listing | registry | `audio_transcribe` row present; detail prints schema | — | — |
| `-t website_text` (tool not selected) | allow-list | `audio_transcribe` not offered to model | — | tool invocable anyway |

## Acceptance criteria

- [x] Contract rows proven via the existing tooling test harness (cf. `main_tooling_*_e2e_test.go` patterns), race-clean:
  - transcript in tool result, not raw on stdout; tool default `text` beats config `vtt`: `Test_e2e_audio_transcribe_tool_returns_transcript`
  - `output_format: vtt` → `WEBVTT`: `Test_e2e_audio_transcribe_tool_vtt_format`
  - `clai tools` row + detail schema: `Test_e2e_audio_transcribe_tool_listing`
  - `-t website_text` → tool not offered/invoked: `Test_e2e_audio_transcribe_tool_not_selected`
- [x] Adapter contains no parsing/rendering/splitting logic. — `pkg/tools/audio_tool_transcribe.go` only validates input types and delegates to the injected `AudioTranscribeEngine`; the engine (`internal/create_queriers.go audioTranscribeEngine`) wires config → `createAudioSplitter` → `Render`, all logic in phases 1–3 code.
- [x] Tool result for a diarized fixture retains speaker prefixes in `text` format. — `Test_e2e_audio_transcribe_tool_diarized_text_keeps_speakers` (`A: mock transcription` via `test-diarize` mock model)
- [x] Coverage ≥ 70 % on the adapter. — `AudioTranscribeTool.Call`/`Specification` 100 % (`pkg/tools` unit tests); engine 80 % via e2e

## Error coverage

| Failure condition | Expected outcome | Test |
| ----------------- | ---------------- | ---- |
| `file_path` missing from input | schema/validation error returned to model | `pkg/tools TestAudioTranscribe_Call` (missing/non-string subtests) |
| File does not exist | tool-result error naming the path; query loop continues | `Test_e2e_audio_transcribe_tool_errors_keep_query_alive` (missing file: exit 0, path in output) |
| Invalid `output_format` | tool-result error listing valid values | `Test_e2e_audio_transcribe_tool_errors_keep_query_alive` (yaml → valid formats listed, exit 0) |
| Engine failure (vendor 500 via mock) | tool-result error with cause; no process exit | `Test_e2e_audio_transcribe_tool_errors_keep_query_alive` (unroutable model → cause in output, exit 0); `TestAudioTranscribe_Call` (engine error propagation) |

## Implementation notes

**Session:** Claude, 2026-08-26. Files: `pkg/tools/audio_tool_transcribe.go` (+test), `internal/tools/handler.go` (registration), `internal/create_queriers.go` (`createAudioSplitter` extraction + `audioTranscribeEngine` + `init` wiring), `internal/audio/querier.go` (mock diarize), `internal/vendors/mock.go` (`audio_transcribe` inputs), `main_tooling_audio_e2e_test.go`.

Deltas from spec:

- **Injected engine seam:** `pkg/tools` deliberately imports nothing outside `pkg/text/models` (repo layering), so the tool declares `var AudioTranscribeEngine func(filePath, outputFormat string) (string, error)` and package `internal` wires it in an `init()` in `create_queriers.go`. Unwired (e.g. external `pkg/tools` consumers) yields a clear tool error, never a panic.
- **Routing extracted, not duplicated:** phase 4's `CreateAudioQuerier` was refactored to share `createAudioSplitter` with the engine, keeping the phase-4 spec's "routing in create_queriers.go" true for both consumers.
- **Mock extended for diarization:** routing now mock-matches on `test`/`mock_test` *prefixes*; a model containing `diarize` (e.g. `test-diarize`) sets `MockTranscriber.Diarized`, which adds `A`/`B` speakers — needed to prove the speaker-prefix acceptance criterion without live keys.
- **No ctx in the tool interface** (`LLMTool.Call(Input)`): the engine uses `context.Background()`, same as other exec-backed built-ins.
- Mock vendor gained an `audio_transcribe` case in `inputsForTool` driven by `CLAI_MOCK_AUDIO_TRANSCRIBE_FILE`/`_FORMAT`, following the existing `CLAI_MOCK_*` convention.

Verification (all green):

- `go test ./pkg/tools/ . ./internal/... -race -count=3 -timeout=30s` via full suite → ok
- `make qa` → exit 0; adapter functions 100 % covered, engine 80 %

## Review findings (review 1, 2026-08-26)

No findings in this phase's own code. Verified good: the adapter
(`pkg/tools/audio_tool_transcribe.go`) truly contains no engine logic — input
type checks and delegation only, with a clear error when the engine is
unwired; the injected-seam wiring (`init` in `create_queriers.go`) keeps
`pkg/tools` free of internal imports; all four contract rows and four error
rows are honestly proven in `main_tooling_audio_e2e_test.go` (including
tool-default-`text`-beats-config-`vtt` and errors-keep-query-alive with exit
0 asserted). Note: the tool inherits **R1-01** (phase 4) only through the
engine for extensionless paths a model might pass; no separate fix needed
here.
