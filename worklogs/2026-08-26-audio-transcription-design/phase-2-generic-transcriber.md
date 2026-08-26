# Phase 2: Generic transcriber + vendor structs

**Status:** Complete
[← README](./README.md)

## Goal

Implement the OpenAI-protocol multipart transcription client once in `internal/audio/generic/` and expose it through thin `openai` and `openrouter` vendor structs, mirroring `generic.StreamCompleter`.

## Specification

- `internal/audio/generic/transcriber.go` — `Transcriber` struct:
  - `Setup(apiKeyEnv, url, debugEnv string) error` — reads bearer token from env (same signature family as `StreamCompleter.Setup`), configurable endpoint URL, optional `ExtraHeaders`.
  - `Transcribe(ctx context.Context, filePath string) ([]audio.Segment, error)` — multipart POST: `file` part (streamed from disk, not slurped), `model`, `response_format` per README negotiation rule (`diarized_json` iff model name contains `diarize`, else `verbose_json`). Parses via phase-1 functions. Respects ctx cancellation.
  - Injected `*http.Client` (defaultable) so tests use `httptest.Server`.
- `internal/vendors/openai/transcribe.go` — struct embedding `generic.Transcriber`; `Default` with model `whisper-1`, URL `https://api.openai.com/v1/audio/transcriptions`, key `OPENAI_API_KEY`.
- `internal/vendors/openrouter/transcribe.go` — embeds the same; URL `https://openrouter.ai/api/v1/audio/transcriptions`, key `OPENROUTER_API_KEY`, trims `or:` model prefix in `Setup()`, sets the same `HTTP-Referer`/`X-OpenRouter-Title` headers as the chat vendor.
- No vendor-specific behavior inside `internal/audio/generic/` (repo rule); differences live entirely in the vendor `Setup()`s.
- Diarize extras (`known_speaker_names[]`/`known_speaker_references[]`) are **out of scope** for this phase; noted as a follow-up field on the config in phase 4 or later.

## Integration contract

| Scenario | Collaborators | Observable result | Required side effects | Prohibited side effects |
| -------- | ------------- | ----------------- | --------------------- | ----------------------- |
| whisper-1 model, valid audio file | `httptest.Server` asserting multipart fields | request has `response_format=verbose_json`, returns parsed `[]Segment` | Authorization: `Bearer <key>` header | no `text/srt/vtt` response_format ever sent |
| model `gpt-4o-transcribe-diarize` | same | request has `response_format=diarized_json`; speakers populated | `chunking_strategy=auto` field sent (F1-01: OpenAI 400s diarize requests without it) | `chunking_strategy` on non-diarize requests |
| OpenRouter vendor with model `or:openai/whisper-1` | same | `model` field sent without `or:` prefix | OpenRouter extra headers present | — |
| large file (multi-MB fixture) | same | request body streamed (no full-file buffering assertion via io behavior) | — | file not read into memory as one slice |

## Acceptance criteria

- [x] Both vendor structs produce correct requests against `httptest.Server` (fields, headers, auth) — cited per contract row:
  - whisper-1 + verbose_json + bearer auth: `generic.TestTranscribe_VerboseJSON`, `openai.TestTranscriber_Transcribe`
  - diarize model → diarized_json + speakers: `generic.TestTranscribe_DiarizedModel`
  - OpenRouter `or:` trim + extra headers: `openrouter.TestTranscriber_Transcribe_TrimsPrefixAndSendsHeaders`, `generic.TestTranscribe_ExtraHeaders`
  - 3 MB file streamed (chunked, no Content-Length, byte-count equality): `generic.TestTranscribe_StreamsLargeFile`
- [x] Response payloads flow through phase-1 parsers into `[]Segment` (fixture equality). — `generic.TestTranscribe_VerboseJSON` asserts full segment equality against the phase-1 fixture
- [x] `create_queriers`-style routing decision is NOT in this phase (phase 4); package compiles standalone. — no changes outside `internal/audio/generic/` and the two vendor files
- [x] Coverage ≥ 80 % for `internal/audio/generic/` and the two vendor files. — generic 81.2 % package coverage; `openai/transcribe.go` and `openrouter/transcribe.go` both 100 % (`go tool cover -func`)

## Error coverage

| Failure condition | Expected outcome | Test |
| ----------------- | ---------------- | ---- |
| API key env unset/empty | `Setup` error naming the env var | `generic.TestSetup_MissingAPIKey`, `openai.TestTranscriber_Setup_MissingKey`, `openrouter.TestTranscriber_Setup_MissingKey` |
| Input file does not exist / unreadable | error before any HTTP request | `generic.TestTranscribe_FileMissing` (asserts zero server hits) |
| Non-200 response | error including status and (truncated) body | `generic.TestTranscribe_Non200` |
| 200 with malformed JSON body | wrapped phase-1 parse error | `generic.TestTranscribe_MalformedResponseBody` |
| ctx cancelled mid-request | request aborts, `ctx.Err()`-wrapped error returned promptly | `generic.TestTranscribe_ContextCancelled` (`errors.Is(err, context.Canceled)`, <2 s abort) |

## Implementation notes

**Session:** Claude, 2026-08-26. Files: `internal/audio/generic/transcriber.go` + test, `internal/vendors/openai/transcribe.go` + test, `internal/vendors/openrouter/transcribe.go` + test.

Deltas from spec:

- **Client injection is a public `Client *http.Client` field** (defaulted in `Setup` when nil) instead of a private field, so vendor-package tests could inject too. In practice all tests override `URL` against `httptest.Server` with the default client, mirroring how chat vendor tests work.
- **Streaming proven via chunked transfer:** the file part goes through `io.Pipe`, so the request has no Content-Length; `TestTranscribe_StreamsLargeFile` asserts `ContentLength <= 0` plus byte-count equality on a 3 MB file. That is the io-behavior assertion the contract row asked for — no full-file slurp is possible through a pipe.
- **Vendor default vars named `TranscriberDefault`** (repo convention is `<Thing>Default`/`GptDefault`, not the spec's `Default`, which would collide within the packages).
- **Debug output via `slog.Debug`**, not `ancli.PrintOK`: ancli OK/notice print to stdout, which would violate the pipes-stay-clean invariant.
- One `make qa` run hit a pre-existing flake: `internal/vendors/anthropic` `Test_context` timed out at 30 s under full-suite load; passes in isolation (1.2 s, count=3) and on re-run of the full suite. Not related to this change.

Verification (all green):

- `go test ./internal/audio/... ./internal/vendors/openai/ ./internal/vendors/openrouter/ -race -cover -count=3 -timeout=30s` → ok; generic 81.2 %, openai 73.0 % (pkg), openrouter 57.1 % (pkg), transcribe files 100 %
- `make qa` (full sweep) → exit 0

## Review findings (review 1, 2026-08-26)

Verified good (traced in the code, not from the notes):

- All four integration-contract rows and five error rows re-read in
  `generic/transcriber_test.go` and the two vendor tests: bearer auth, model
  and `response_format` fields, `or:` trim, extra headers, and 3 MB
  chunked-streaming (no Content-Length + byte-equality) are all genuinely
  asserted against `httptest.Server`.
- The negotiation invariant ("never request text/srt/vtt") holds by
  construction: `responseFormat()` (`transcriber.go:53`) can only return
  `verbose_json` or `diarized_json`.
- ctx cancellation is honored on request creation, `Do`, and body read, with
  `context.Canceled` preserved in the chain and a timing-bounded test.
- The `io.Pipe` writer goroutine cannot leak: `CloseWithError` always runs,
  and an aborted request drains via the pipe's error propagation.

Findings:

- [ ] **R1-03 (Minor, logged — does not reopen)** — the spec deferred diarize
  extras with "noted as a follow-up field on the config in phase 4 or later",
  but the note never landed: no phase-4 config field, no architecture-doc
  mention, no worklog follow-up entry. Consequence: the README's accepted
  trade-off for diarize label drift names `known_speaker_references[]` as the
  mitigation, and that mitigation currently has no tracked landing place —
  only the stderr warning exists. Record the follow-up (config-field sketch in
  `architecture/audio.md` or a worklog note) so the promise is findable.

Cross-reference: **R1-01** (filed in phase 4) has one sink in this phase's
code — `transcriber.go:125` names the multipart part after the local file, so
extensionless temp files likely fail OpenAI's filename-based format check.
The fix is routed through phase 4 (temp-file naming); if instead fixed here,
keep the vendor-agnostic rule: the generic layer must not special-case
vendors.

## Field findings (2026-08-26, live usage)

**Session:** Claude, 2026-08-26, reported by Lorentz from a real
`gpt-4o-transcribe-diarize` run. Phase reopened and closed in-session.

- [x] **F1-01 (Major)** — diarize requests were rejected live with
  `400: "chunking_strategy is required for diarization models"`. The
  transcriber negotiated `diarized_json` but never sent `chunking_strategy`;
  every diarize fixture/httptest passed because nothing asserted the field.
  The protocol survey missed that the parameter is mandatory for diarize
  models. **Fix:** `writeMultipart` sends `chunking_strategy=auto` iff the
  response format is `diarized_json` (keyed to the negotiation, not to a
  vendor name — generic layer stays vendor-agnostic; OpenRouter never
  receives it since it has no diarize models). Validated test-first:
  `generic.TestTranscribe_DiarizedModel` asserts the field (failed before the
  fix, passes after) and `TestTranscribe_VerboseJSON` pins its absence for
  non-diarize requests. Contract row updated. `make qa` → exit 0.
