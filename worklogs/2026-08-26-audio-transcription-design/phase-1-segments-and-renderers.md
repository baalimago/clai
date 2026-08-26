# Phase 1: Segment model, normalization, renderers

**Status:** Complete
[← README](./README.md)

## Goal

Establish the vendor-agnostic `Segment` model with pure functions that normalize vendor wire payloads into it and render it to `vtt|srt|text|json`.

## Specification

New package `internal/audio/` (no HTTP, no exec — pure functions only):

- `Segment` struct as defined in the README strategy.
- `ParseVerboseJSON([]byte) ([]Segment, error)` — OpenAI/OpenRouter `verbose_json` payload (`segments[].{start,end,text}`, float seconds → `time.Duration`). Word-level granularity is ignored in this phase (segment granularity only).
- `ParseDiarizedJSON([]byte) ([]Segment, error)` — `gpt-4o-transcribe-diarize` `diarized_json` payload (`segments[].{speaker,start,end,text}`); `speaker` mapped to `Segment.Speaker`.
- `Offset(segs []Segment, delta time.Duration) []Segment` — shifts all timestamps; used by phase 3 stitching.
- `Render(segs []Segment, format OutputFormat) (string, error)` with `OutputFormat ∈ {vtt, srt, text, json}`:
  - `vtt`: valid WebVTT — `WEBVTT` header, `HH:MM:SS.mmm --> HH:MM:SS.mmm` cues, `<v X>text</v>` when `Speaker != ""`.
  - `srt`: 1-based numbered cues, `HH:MM:SS,mmm` comma separator, `X: text` prefix when `Speaker != ""`.
  - `text`: plain concatenated text, one segment per line, speaker prefix `X: ` when present; no timestamps.
  - `json`: rendered via a render-time DTO — `[{"start": <float seconds>, "end": <float seconds>, "speaker": "<label, omitted when empty>", "text": "…"}]`. `[]Segment` is never marshaled directly (`time.Duration` would emit nanosecond ints; float seconds is the wire contract for tool/script consumption).
- `ParseOutputFormat(string) (OutputFormat, error)` for flag/config validation.

Timestamps are `time.Duration` internally (per repo convention: no formatted-string storage); formatting happens only at render time.

## Integration contract

`unit-test-only` — this phase has no external boundary. Table-driven tests over payload fixtures (OpenAI verbose_json, OpenRouter verbose_json, diarized_json) checked into `testdata/`.

**Fixture provenance:** live API captures when keys are available, otherwise fixtures authored from the vendor docs / README protocol survey are acceptable. Every fixture file cites its source (API capture date+model, or doc URL) in a `testdata/README.md` line.

## Acceptance criteria

- [x] `ParseVerboseJSON` round-trips a real whisper-1 `verbose_json` fixture into correct `[]Segment` (start/end/text verified against fixture values). — `TestParseVerboseJSON_Whisper1Fixture`, `TestParseVerboseJSON_OpenRouterFixture`
- [x] `ParseDiarizedJSON` preserves speaker labels from a diarized fixture. — `TestParseDiarizedJSON_PreservesSpeakers`
- [x] `Render` produces spec-valid VTT (header, arrow syntax, dot millis) and SRT (index, comma millis) for the same input, including voice tags/prefixes when speakers are present and their absence when not. — `TestRender` (cases `vtt/srt with/without speakers`, exact-string assertions)
- [x] `Offset` shifts start and end uniformly and is a no-op at delta 0. — `TestOffset` (also asserts input not mutated)
- [x] `text` render of a diarized fixture reads `A: …` lines; of a non-diarized fixture, bare lines. — `TestRender` (cases `text with/without speakers`)
- [x] `json` render yields `start`/`end` as float seconds (test asserts a known value, e.g. `5.28`, from the fixture) and omits `speaker` when empty; no nanosecond integers anywhere in the output. — `TestRender` (json cases, exact-string), `TestRenderJSON_FloatSecondsContract`
- [x] Package coverage ≥ 90 % (`go test -cover`). — 98.4 % (`go test ./internal/audio/ -race -cover -count=3 -timeout=30s`)

## Error coverage

| Failure condition | Expected outcome | Test |
| ----------------- | ---------------- | ---- |
| Malformed JSON payload | wrapped parse error naming the format | `TestParse_MalformedJSON` |
| Valid JSON, missing `segments` field | error, not empty success | `TestParse_MissingSegmentsField` |
| Empty `segments` array | `[]Segment{}`, no error; `Render` yields valid empty document per format: vtt → `WEBVTT` header only, srt → empty string, text → empty string, json → `[]` | `TestParse_EmptySegmentsArray`, `TestRenderEmpty` |
| Unknown output format string | `ParseOutputFormat` error listing valid values | `TestParseOutputFormat` (also `TestRenderUnknownFormat` for `Render`) |
| Negative or end<start timestamps in payload | segments passed through unaltered (garbage-in tolerated), render does not panic | `TestRenderGarbageTimestampsDoesNotPanic` |

## Implementation notes

**Session:** Claude, 2026-08-26. Files: `internal/audio/audio.go`, `internal/audio/audio_test.go`, `internal/audio/testdata/`.

Deltas from spec:

- **Text trimmed at parse time.** whisper-1 emits segment text with a leading space (`" Hello …"`); `ParseVerboseJSON`/`ParseDiarizedJSON` apply `strings.TrimSpace` so renders don't carry stray whitespace. Tests assert the trimmed values.
- **Millisecond precision pinned at parse.** Float seconds → `time.Duration` rounds to whole milliseconds (`math.Round(s*1000)`), matching subtitle timestamp resolution and keeping the json render's float seconds free of float-noise (`5.28`, never `5.279999…`).
- **Missing vs. empty `segments`** distinguished via a `*[]wireSegment` field: absent key → error, `[]` → empty success.
- **No live API keys** during this session; all fixtures are doc-derived per the provenance rule, cited in `testdata/README.md`.
- **dupl** flagged the initial per-format render tests as three structural clones; consolidated into one table-driven `TestRender`, dupl now clean for the package.

Verification (all green):

- `go test ./internal/audio/ -race -cover -count=3 -timeout=30s` → ok, **98.4 %** coverage
- `make qa` (full sweep) → pass; gofumpt/staticcheck/vet/fix clean, no dupl clones in `internal/audio`

## Review findings (review 1, 2026-08-26)

No findings. Verified good: all seven acceptance criteria and five error rows
re-traced against `audio.go`/`audio_test.go` — the missing-vs-empty
`segments` distinction (`*[]wireSegment`), ms-precision rounding, float-second
json DTO (no nanosecond ints reachable: `Segment` has no marshal path in
`Render`), empty-document renders per format, and negative-timestamp
formatting (`formatTimestamp` sign handling) all hold. Fixture provenance is
recorded in `testdata/README.md` per the V1-02 resolution.
