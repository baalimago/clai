# Phase 16 — misplaced-flag hints + query-level media tool overrides

**Status:** Complete
[Worklog README](./README.md)

## Goal

Tell the user where a misplaced flag belongs instead of failing with a
confusing message, and let a normal query/chat configure the media models
its tools use (`-am`/`-af` for `audio_transcribe` today, extensible to
video/photo tools).

## Specification

Repo: clai (branch `refactor-flag-system`) + go_away_boilerplate `main`.
Directed by Lorentz (2026-08-29) after a script broke:

```bash
clai -am gpt-4o-transcribe-diarize -af json -t 'audio_transcribe,...' q
→ error: 'gpt-4o-transcribe-diarize' is not a valid argument
```

**Diagnosis (verified 2026-08-29).** Two independent facts:

1. `-am` is a sub-level flag (`audio transcribe`) since phase 8 (D11/R-e).
   The pre-dispatch scan's value-taking union covers **top-level flagsets
   only** (`pkg/cmd/setup.go:valueFlagUnion`), so `-am` was not known to
   consume a value: the scanner took `gpt-4o-transcribe-diarize` as the
   command token, matched nothing, and returned `ArgNotFoundError`. Hence
   an error naming the model, not the flag.
2. The flags were a **no-op in that invocation even before the refactor**.
   The `audio_transcribe` tool runs through the mode-as-tool bridge, whose
   engine loads `audioConfig.json` from disk and takes the format from the
   tool call's own `output_format` argument
   (`internal/audio/create_querier.go:transcribeEngine`; identical at
   `git show HEAD:internal/create_queriers.go:audioTranscribeEngine`).
   `applyFlagOverridesForAudio` was only ever called from the audio
   *command* path (`HEAD:internal/setup_audio.go:61`), never from a query.

Lorentz's direction: do not break compatibility — configuring audio (and
later video) models from a normal query is wanted, since tooling makes
queries omni-modal.

### Part 1 — flag-placement hints (upstream `pkg/cmd`)

Generic dispatcher feature; no clai-specific knowledge.

1. `flagOwners(commands)` walks every registered command's flagset **and**
   its `Subcommander` descendants, mapping flag name → sorted command paths
   that define it (`"audio transcribe"`, `"chat continue"`). Pure, built on
   the error path only, so hot paths are untouched. `Flagset()` purity
   (phase 1) makes the walk safe.
2. New `MisplacedFlagError{Flag, Owners, Candidate}`: printed cleanly by
   `printHelp` (same branch treatment as `ArgNotFoundError`, which today is
   the only error that avoids the `unknown error:` prefix).
3. `parse` returns it instead of a bare `ArgNotFoundError` when the
   unmatched command candidate is preceded by a flag token whose name is
   unknown at top level but defined deeper — i.e. the candidate was that
   flag's value.
4. `parseFlagset` enriches stdlib's `flag provided but not defined: -x`
   with the owner list when `x` is defined elsewhere; the wrapped error
   keeps `errors.Is/As` behavior for existing consumers.

### Part 2 — media tool overrides (clai)

5. `internal.AgentTextFlags` (shared by query, chat and chat continue)
   gains `AudioModel` (`-am`/`-audio-model`) and `AudioFormat`
   (`-af`/`-audio-format`) — same names as the audio command's own flags,
   different level, same meaning. This also restores the broken script
   verbatim, and puts the names back into the top-level scan union.
6. `internal/audio` gains run-scoped overrides applied by the tool engine
   on top of `audioConfig.json`, mirroring the established
   `pkgtools.SetCmdBanList` pattern for run-scoped tool configuration:
   `SetTranscribeOverrides(model, format string)` +
   `ResetTranscribeOverridesForTests`. The format override beats the tool
   call's `output_format` argument (an explicit user choice outranks the
   model's per-call pick); an empty override leaves current behavior.
7. Wiring: `text.SetupQuerier` applies the overrides through an injected
   `ApplyAudioOverrides func(model, format string)` on the query/chat
   command deps, wired to `audio.SetTranscribeOverrides` in `main.go`
   (composition root; `internal/text` keeps no domain dependency).
   Video/photo get the same seam when such tools land — no flags are added
   for tools that do not exist yet (a flag that silently does nothing is
   the exact failure mode this phase fixes).
8. Docs: `architecture/cmd-dispatch.md` (flag table + hint behavior),
   `architecture/audio.md` (tool overrides), `examples.md`.

## Integration contract

| # | Scenario | Observable result | Prohibited |
|---|----------|-------------------|------------|
| 1 | `clai -am <model> -af json -t audio_transcribe q <prompt>` | runs; the tool transcribes with `<model>` and returns JSON | parse error |
| 2 | `clai -pd /tmp q hi` (media flag not owned by query) | error names `-pd` and says it belongs to `photo` | bare "is not a valid argument" naming the value |
| 3 | `clai q -parallelism 2` (sub-level flag after the wrong command) | "not defined" error plus owner hint (`audio transcribe`) | silent acceptance |
| 4 | `clai a t -am <model> f.wav` | unchanged direct-transcription behavior | regression |
| 5 | no `-am` given | tool keeps using `audioConfig.json` + per-call format | override leaking across runs |

## Acceptance criteria

- [x] Upstream: `flagOwners` + `MisplacedFlagError`, both error sites
      enriched, `printHelp` prints them cleanly; `pkg/cmd` tests cover
      contract rows 2 and 3 plus the no-hint case (unknown token that is
      not a flag value); upstream QA green.
      Evidence: `pkg/cmd/flag_hint_test.go` (6 tests: owner map, both hint
      sites, both no-hint cases, arity-union guard); upstream `make qa`
      exit 0, `pkg/cmd` 95.3%.
- [x] clai: `-am`/`-af` on query/chat/chat-continue; override applied by
      the engine; `main.go` wiring; contract rows 1, 4, 5 pinned by e2e.
      Evidence: `Test_e2e_audio_transcribe_tool_flag_overrides` (model
      override beats a config naming `whisper-1` with no API key; `-af
      json` beats the tool's own format; bad `-af` fails the run);
      existing audio-command tests unchanged for row 4.
- [x] Override is run-scoped and reset between runs; unit test pins it.
      Evidence: `Test_SetTranscribeOverrides` (set/read/reset, rejected
      format not stored, empty override is a no-op).
- [x] Touched packages keep ≥70% coverage; `make qa` exit 0 in both repos.
      Evidence: clai exit 0 — internal 79.3%, audio 81.8%, chat 71.8%,
      text 81.5%; upstream exit 0.
- [x] Docs updated (cmd-dispatch flag table + hint section, audio.md,
      examples.md); the completion e2e's "forbidden on query" assertion
      updated deliberately.

## Error coverage

| Failure condition | Expected outcome | Test |
|---|---|---|
| Flag value mistaken for a command | `MisplacedFlagError` naming flag + owner | upstream scan test |
| Unknown flag on a command that doesn't own it | "not defined" + owner hint | upstream parse test |
| Unknown token that is not a flag value | plain `ArgNotFoundError` (no hint) | upstream scan test |
| Invalid `-af` value on a query | same "invalid format" error as the audio command | clai e2e |
| Override set, then a second run without it | second run uses the config file | clai unit test |

## Implementation notes

**Session: Claude, 2026-08-29.**

Deltas from the specification:

- **Owner paths use the long name.** `flagOwners` takes the first segment
  of each `"name|shortcut"` key, per the documented key convention, so
  hints read `'audio transcribe'`, not `'a t'`. Multiple owners render as
  `'audio transcribe', 'chat', 'chat continue' or 'query'`
  (`quotedOwners`), since `-am`/`-af` are legitimately defined at several
  levels after this phase.
- **`resolveSubcommands` gained the command map** so sub-level parse
  errors get hints too (`clai a t -pd /tmp`); it previously took only the
  resolved command.
- **`MisplacedFlagError` wraps rather than replaces.** It keeps the
  original error (`ArgNotFoundError` or the stdlib parse error) in
  `Unwrap`, so existing `errors.Is/As` consumers are unaffected;
  `printHelp` gained a branch so it prints cleanly instead of via the
  `unknown error:` fallback.
- **Format override beats the tool call.** `-af` outranks the model's
  `output_format` argument (an explicit user choice wins), and is
  validated in `SetTranscribeOverrides` so a typo fails at setup rather
  than inside a tool call mid-conversation.
- **No video/photo flags yet.** No such tool exists, and a flag that
  silently does nothing is exactly the defect this phase fixes. The seam
  is in place: `internal.MediaToolFlags` + the `ApplyMediaOverrides`
  dep on query/chat take new fields additively when a video or photo tool
  lands.

Two tests pinned behavior this phase deliberately changes:
`Test_e2e_audio_sub_flag_before_command_rejected` (renamed to
`..._hints_owner`; it asserted the old value-blaming message) and the
completion e2e's forbidden-flag list (`-af` is a query flag now; the
assertion moved to `-parallelism`, still sub-level-only).

Verification: both repos `make qa` exit 0; binary reinstalled and the
reported invocation checked end-to-end against a live model —
`clai -am gpt-4o-transcribe-diarize -af json -t '...' q 'say hi'` → exit 0.

## Review findings

_(appended by reviewers)_
