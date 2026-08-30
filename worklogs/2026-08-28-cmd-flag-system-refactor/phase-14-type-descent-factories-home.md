# Phase 14 — type descent: audio/photo/video factories move home

**Status:** Complete
[Worklog README](./README.md)

## Goal

Apply text's layering pattern to audio, photo and video: push the
vendor-shared symbols below the vendors, flip the upward arrows, move each
factory into its domain package, and delete package `internal`.

## Specification

Repo: clai, branch `refactor-flag-system`. Depends on phase 13. Directed by
Lorentz (2026-08-28): "follow that pattern; do the same for photo as well"
(video included — same knot, and it empties `create_queriers.go`).

**Verified vendor surfaces (2026-08-28):** photo: `Configurations`,
`Output`, `OutputType` consts, `SaveImage`, `StartAnimation`; video:
`Configurations`, `Output`, consts only (`SaveVideo` has no vendor
callers); audio: vendors touch nothing directly — only `audio/generic`
reaches up, for `Segment` + `ParseVerboseJSON`/`ParseDiarizedJSON`.
`photo|video/generic → chat` is cycle-free (chat's vendor closure is
anthropic/pi only).

1. **Audio flip.** `Segment` + the wire-parse cluster (`wireSegment`,
   `wirePayload`, `parsePayload`, `ParseVerboseJSON`, `ParseDiarizedJSON`,
   `secondsToDuration`) move to `audio/generic`; `internal/audio` keeps a
   `Segment` type alias + parser re-export vars so its own code and tests
   stay untouched; `generic/transcriber.go` drops the audio import. Then
   the phase-13-reverted relocation lands: `setupTranscribeQuerier`,
   `resolveAudioInput`, `createSplitter`, `CreateQuerier`, the
   `audio_transcribe` engine + `init()` all move into `internal/audio`;
   the audio command's `SetupTranscribeQuerier` dep dissolves.
2. **Photo descent.** `internal/photo/generic` gets the type defs
   (`Configurations`, `Output`, `OutputType` + consts), `prompt.go`
   (`SetupPrompts` must live with its receiver type), `store.go`
   (`SaveImage`), `funimation_0.go` (`StartAnimation`). `internal/photo`
   keeps type aliases, `DEFAULT`, `ValidateOutputType`,
   `ApplyFlagOverrides`, the command, and gains `CreateQuerier` (the
   factory, importing openai + gemini). Vendors switch to
   `photo "…/internal/photo/generic"` via import alias — zero body edits.
   The photo command's `CreateQuerier` dep dissolves (`LoadConfig` stays
   injected — setup owns the migration).
3. **Video descent.** Same shape: type defs + `prompt.go` →
   `internal/video/generic`; `Default`, `ValidateOutputType`, `SaveVideo`,
   aliases, command + new `CreateQuerier` stay in `internal/video`;
   openai's import aliased. Video command's `CreateQuerier` dep dissolves.
4. **Package `internal` is deleted.** After the three factories move,
   only the `ProfileHelp` re-export remains — the freeze exception is
   budgeted: `main_help_e2e_test.go` swaps `internal.ProfileHelp` for
   `profiles.Help` (one reference + import; the assertion itself is
   unchanged). The engine-bridge blank imports in `pkg/agent` and
   `pkg/text` re-target `internal/audio`;
   `Test_audioTranscribeEngineWired` keeps pinning the linkage.
5. **Tests move with their code** (parse tests may stay in audio via the
   re-exports; `create_queriers_test.go` photo part → photo;
   `create_queriers_audio_test.go` + `setup_audio_test.go` → audio;
   moved `SetupPrompts`/store tests → the generic packages).
6. **Behavior freeze** otherwise: zero e2e edits beyond the budgeted
   ProfileHelp swap; `pkg/agent`/`pkg/text` suites green; docs re-targeted
   (`cmd-dispatch.md` composition section + key files, `audio.md`,
   `photo.md`, `video.md`, `config.md`).

## Integration contract

| # | Scenario | Observable result | Prohibited |
|---|----------|-------------------|------------|
| 1 | full e2e suite | passes with only the budgeted ProfileHelp-reference swap | any assertion change |
| 2 | `pkg/agent`/`pkg/text` suites | green; engine wired via `internal/audio` blank import | nil-engine regression |
| 3 | vendor behavior (photo save, animation, transcribe parse) | byte-identical — import alias means zero vendor body edits beyond the import path | — |
| 4 | `go list` | no package imports `clai/internal` (it no longer exists) | resurrecting it |

## Acceptance criteria

- [x] `internal/create_queriers.go` and package `internal` deleted; the
      three factories live in their domain packages; grep proves no
      `clai/internal"` import remains.
      Evidence: `internal/*.go` empty; repo-wide grep clean.
- [x] Vendor packages import `photo/generic`, `video/generic`,
      `audio/generic` only — no vendor imports `internal/photo`,
      `internal/video`, or `internal/audio` (`go list` evidence).
      Evidence: `go list` over `./internal/vendors/...` shows zero
      domain-root imports.
- [x] Contract rows 1–2 green; row 4 by grep/go-list.
      Evidence: e2e green with only the budgeted ProfileHelp swap
      (`main_help_e2e_test.go`); `pkg/agent`/`pkg/text` green;
      `Test_audioTranscribeEngineWired` retargeted to `internal/audio`.
- [x] Moved-code packages ≥70% coverage; numbers recorded.
      Evidence: audio 82.3%, audio/generic 82.1%, photo 81.4%,
      photo/generic 78.0%, video 74.5%, video/generic 85.7% (make qa).
- [x] Docs re-targeted; `make qa` exit 0.
      Evidence: cmd-dispatch/config/query/photo/video/audio docs updated;
      exit 0.

## Error coverage

| Failure condition | Expected outcome | Test |
|---|---|---|
| Unknown photo/video/audio model | same routing error text | moved factory tests kept green |
| Invalid output type | same `ValidateOutputType` error | existing tests kept green |
| Transcript payload malformed | same parse error | audio parse tests kept green via re-exports |

## Implementation notes

**Session: Claude, 2026-08-28 (extension, same session as phases 11–13).**

Deltas from the specification:

- **`SecondsToDuration` exported in `audio/generic`** — `audio/split.go`
  uses it for chunk offsets, so it crossed the package line with the
  parse cluster.
- **`SetupPrompts` moved with its types** for both photo and video (Go
  methods must live with their receiver's defining package); the reply
  branch pulls `chat` into `photo/generic`/`video/generic`, which is
  cycle-free (chat's vendor closure is anthropic/pi).
- **`photo/generic` also took `DEFAULT`'s neighbors' tests**: store,
  funimation and new prompt tests live there; `DEFAULT` and
  `ValidateOutputType` stayed in `photo` (no vendor callers) as
  specified.
- **Dissolved deps**: with factories home, photo lost its
  `CreateQuerier` dep (keeps `LoadConfig` — setup owns the migration),
  video and audio kept only `ConfigPrep`. Their `cmd_test`s now drive
  the real factory paths (offline: vendor constructors only need an API
  key env var).
- **Vendor edits were import-line-only**, via aliased imports
  (`photo "…/internal/photo/generic"` etc.) in dalle/sora/gemini-image
  files and their tests — zero body churn as specified.

New tests beyond relocations: `photo/generic` prompt tests (formatting,
seeded reply-context, no-history degradation) + `StartAnimation` smoke;
`video/generic` prompt tests incl. the base64 image-prompt branch;
photo factory routing table (unknown model, invalid/missing output,
gemini route).

Verification: `make qa` exit 0; full e2e green (only budgeted edit:
`internal.ProfileHelp` → `profiles.Help` in `main_help_e2e_test.go`);
no new dupl clones; binary reinstalled. Package `internal` no longer
exists — `main.go` composes purely from domain packages +
`internal/setup` + `internal/clicmd`.

## Review findings

_(appended by reviewers)_
