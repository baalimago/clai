# Phase 6: Documentation and quality-gate sweep

**Status:** Complete
[← README](./README.md)

## Goal

Complete the repository's maintenance contracts (architecture doc, usage text, examples) and prove the whole change passes every QA gate.

## Specification

- **`architecture/audio.md`** in the style of `photo.md`/`video.md`: entry flow, key-files table, `audioConfig.json` reference, flag-override table, response-format negotiation, split/stitch behavior, tool bridge note. Add row to `architecture/README.md` index (Command docs section).
- **`main.go` usage**: final wording for the `a|audio t|transcribe` command row and `-am/-af/-parallelism` flags; add one example line (piped meeting-notes flow).
- **`examples.md` / README.md**: add the transcription pipe example if the files' conventions call for it (check both; additive only).
- **Setup wizard**: verify `clai setup` lists/edits `audioConfig.json`; wire it if phase 4 did not.
- **Docs accuracy check**: every path and flag named in `architecture/audio.md` must exist in the code (reviewer-checkable).
- **Quality-gate sweep** (record exact commands + outcomes in implementation notes):

  ```bash
  go run mvdan.cc/gofumpt@latest -w -l .
  go run honnef.co/go/tools/cmd/staticcheck@latest ./...
  go vet ./...
  go test ./... -race -cover -count=3 -timeout=30s
  go fix ./...
  go run github.com/mibk/dupl@latest -t 80 .
  # or: make qa
  ```

  Dupl findings triaged per repo duplication policy (signal, not verdict) — each accepted clone justified in the notes.
- **Coverage audit**: per-package coverage for all new packages recorded; every package ≥ 70 %, target 90 % for `internal/audio/`.

## Integration contract

`unit-test-only` — this phase adds documentation and runs gates; its "tests" are the gate commands themselves plus doc-accuracy review.

## Acceptance criteria

- [x] `make qa` passes clean, unedited, on the branch tip (command output cited). — exit 0, 39 `ok` package lines, no FAIL (see implementation notes)
- [x] `architecture/audio.md` exists, indexed in `architecture/README.md`, and names only real paths/flags. — every file in the key-files table exists on disk; flags match `parseFlags`
- [x] `clai help` (usage string) and the architecture doc agree on flags and command spelling. — both say `a|audio t|transcribe <file>`, `-am/-audio-model`, `-af/-audio-format`, `-parallelism`; asserted by `Test_goldenFile_AUDIO_help_includes_audio`
- [x] Coverage table recorded in implementation notes; all new packages ≥ 70 %. — `internal/audio` 91.6 %, `internal/audio/generic` 81.2 %
- [x] Worklog status board fully `Complete` (phases 1–5) before this phase closes. — board shows phases 1–5 Complete

## Error coverage

| Failure condition | Expected outcome | Test |
| ----------------- | ---------------- | ---- |
| Any QA gate fails | phase stays incomplete; failure recorded in notes and the owning phase reopened | `make qa` exit 0 — no gate failed |
| Coverage below 70 % on a new package | owning phase reopened with a Major finding | all new packages ≥ 81 % — not triggered |

## Implementation notes

**Session:** Claude, 2026-08-26. Files: `architecture/audio.md` (new), `architecture/README.md` (index row), `examples.md` (audio section), `main.go` (piped meeting-notes example line), `internal/setup/setup.go` + `internal/setup/setup_actions.go` (audioConfig excluded from the wizard's "model files" glob).

Deltas / findings:

- **Setup wizard was half-wired by phase 4:** the "general config" category globs `*Config.json`, so `audioConfig.json` was already listed and editable via `clai setup` with no change. But the "model files" category globs `*.json` with an exclude-contains list — without adding `"audioConfig"` there, the wizard would have mislisted `audioConfig.json` as a vendor model-price file. Both exclusion lists updated (`setup.go:193`, `setup_actions.go:709`).
- Usage command row and flags were already added in phase 4; this phase added only the pipe example line.
- README.md needed no change: its conventions cover vendors/API keys, not per-command examples — the audio vendors (OpenAI, OpenRouter) are already in its table.

Quality-gate sweep (branch tip, 2026-08-26):

| Command | Outcome |
| --- | --- |
| `make qa` (gofumpt -w -l, staticcheck, go vet, `go test ./... -race -cover -count=3 -timeout=30s`, go fix, dupl -t 80) | exit 0; 39 packages `ok`, no FAIL |
| `go run github.com/mibk/dupl@latest -t 80 .` | zero clones in any audio-related file; remaining clones are pre-existing (pi/gemini/berget/novita test scaffolds, setup_actions) and untouched by this effort |

Coverage audit (new code):

| Package / unit | Coverage |
| --- | --- |
| `internal/audio` | 91.6 % (target 90 % met) |
| `internal/audio/generic` | 81.2 % |
| `internal/vendors/openai/transcribe.go`, `openrouter/transcribe.go` | 100 % per `go tool cover -func` |
| `pkg/tools/audio_tool_transcribe.go` (`Call`/`Specification`) | 100 % |
| phase-4 wiring (`handleAudio`, `setupAudioTranscribeQuerier`, `resolveAudioInput`, `CreateAudioQuerier`) | 81–100 % per function |
| `audioTranscribeEngine` | 80 % |

## Review findings (review 1, 2026-08-26)

No findings against this phase's contract. Verified good: `make qa` re-run
independently on the branch tip → exit 0 (review 1; 38 `ok` package lines of
40 packages, 2 with no test files — the notes above say 39, a trivial
counting discrepancy, no package failed); `architecture/audio.md` re-audited
against the code — every key-file path exists, the flag table matches
`parseFlags`, and the routing/negotiation sections match
`create_queriers.go`/`transcriber.go`; coverage figures reproduced
(internal/audio 91.6 %, generic 81.2 %).

One doc consequence of **R1-01** (phase 4): `architecture/audio.md` and the
usage text advertise `-` stdin input without noting it currently breaks real
transcription for extensionless temp input — update the doc when R1-01 is
fixed. The board's "all phases Complete" claim is superseded: phase 4 is
Reopened (review 1), so this phase's final acceptance box ("status board fully
Complete") no longer holds until R1-01 closes.
