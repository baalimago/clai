# Phase 15 — dissolve the flag monolith

**Status:** Complete (review 1 findings fixed)
[Worklog README](./README.md)

## Goal

Replace the all-command `Flags`/`Configurations`/`DefaultFlags` bag with
composable flag groups: shared groups in package `internal`, domain flags
in their domain packages, **with byte-identical CLI behavior**.

## Specification

Repo: clai, branch `refactor-flag-system`. Depends on phase 14. Directed by
Lorentz (2026-08-28): dissolve the whole flag structure, "keeping
functionality AND having a cleaner implementation" — i.e. structural
redesign at strict behavior parity, including the existing
flag-equal-to-its-default-is-ignored override semantics.

1. **Primitives** (`internal/flags.go`): `StringFlag`/`BoolFlag`/`IntFlag`
   keep the alias-value design (one value backs short+long, last parsed
   wins, `IsBoolFlag` for scanner arity) and now carry their own default.
   API: `Register(fs, desc, names...)`, `Value()`, `Explicit()` (was the
   `*Set` fields), `Changed()` (value ≠ default — reproduces today's
   defaults-comparison cascades locally, preserving the quirk exactly).
2. **Shared groups in `internal`** (cross-command surface only):
   `RawFlag` (-r), `ReplyStdinFlags` (-re/-I/-i, StdinReplace default
   `{}`), `NonInteractiveFlag` (-n), `AgentTextFlags` (-cm/-t/-cmd-ban/
   -lb/-mt/-mtc/-max-tool-calls-after-handover/-g/-p/-prp + -n),
   `QueryTextFlags` (-dre/-s/-rf/-asc); constructors set defaults; one
   `Register(fs)` each. `TextFlags` composes the four text-path groups as
   the parameter type for `text.SetupQuerier` (both query and chat build
   it; unregistered groups stay zero-valued, which is behavior-identical
   to today's defaults-initialized bag).
3. **Domain flags move home**: photo (-pm/-pd/-pp, dir default
   `$HOME/Pictures`, prefix `clai`), video (-vm/-vd/-vp, ditto Videos),
   audio (-am/-af/-parallelism) each become an exported `Flags` struct in
   their package, constructed in `Command(deps)` and captured by its
   closures. `RegisterPhoto`/`RegisterVideo`/`RegisterAudio` and the
   `Register*` method family are deleted with the bag.
4. **Adapter sheds the flag layer**: `Register` becomes
   `func(fs *flag.FlagSet)`; `Flags`, `Configurations`, `DefaultFlags`,
   `NewFlags`, `Conf()` and the `Parent` field are deleted (parent/sub
   value sharing now falls out of subs closing over the same structs the
   parent built). The session globals stay centralized: the adapter gains
   optional `Raw *RawFlag` / `NonInteractive *NonInteractiveFlag` fields
   and `Setup` keeps setting `utils.ReadonlyConfig` /
   `utils.NoCreateConfig` / `utils.Live` from them (nil ⇒ false).
5. **Cascades rewritten field-local**: `text.ApplyFlagOverrides`/
   `ApplyProfileOverrides` and the setup helpers consume `TextFlags`
   (`Changed()` where they compared to defaults, `Explicit()` where they
   used `*Set`); photo/video/audio `ApplyFlagOverrides` consume their own
   `Flags`. `chat.newReadOnlyHandler` takes a raw bool.
   `SetupAudioTranscribeQuerier`-successor takes `audio.Flags`.
6. **Behavior freeze**: zero e2e edits; every flag name, description,
   help output, alias pairing, arity, and override outcome identical.
   Unit tests migrate to the new shapes (same scenarios).

## Integration contract

| # | Scenario | Observable result | Prohibited |
|---|----------|-------------------|------------|
| 1 | full e2e suite | passes with zero test edits | any assertion change |
| 2 | `clai <cmd> -h` for all commands | identical flag lists (names + descriptions) | — |
| 3 | override cascade parity | flag==default still ignored; explicit-set flags (-mt=0 etc.) still win | semantics drift |
| 4 | scanner arity | `clai -cm x q hi` / bool-flag detection unchanged | — |

## Acceptance criteria

- [x] `internal.Flags`, `internal.Configurations`, `internal.DefaultFlags`
      and all `Register*` methods deleted; groups + primitives remain;
      grep proves no reference.
      Evidence: `internal/flags.go` is primitives + groups only (208
      lines); repo-wide grep clean (one doc reference retargeted).
- [x] Domain flag structs live in photo/video/audio; text path consumes
      `internal.TextFlags`.
      Evidence: `photo.Flags`/`video.Flags`/`audio.Flags` in their
      `cmd.go`s; `text.SetupQuerier(ctx, chatMode, confDir, tf
      internal.TextFlags, args, trustInput)`.
- [x] Contract rows 1–4: e2e green unmodified (rows 1, 2, 4); row 3 via
      migrated cascade unit tests covering the same scenario tables.
      Evidence: `go test . -race` green with zero e2e edits; stoploss/
      override tables migrated; `Test_changedSemantics` pins the
      explicit-default-ignored quirk;
      `Test_textFlagsRegistration` pins every flag name.
- [x] Touched packages ≥70% coverage; numbers recorded.
      Evidence: internal 79.1%, text 81.3%, chat 71.7%, photo 83.6%,
      video 77.4%, audio 82.8% (make qa).
- [x] Docs re-targeted (`cmd-dispatch.md` flag-scoping section,
      `config.md`); `make qa` exit 0.
      Evidence: exit 0 (first run); cmd-dispatch/config/query/
      shell-context updated.

## Error coverage

| Failure condition | Expected outcome | Test |
|---|---|---|
| Non-integer value on int flag | same "invalid value" error | migrated flags tests |
| Flag on non-owning command | same "flag provided but not defined" | existing e2e kept green |
| Bool-flag arity in scanner | `IsBoolFlag` still reported | migrated alias test |

## Implementation notes

**Session: Claude, 2026-08-28 (extension, same session as phases 11–14).**

Deltas from the specification:

- **Dir-reply forcing moved into `text.ApplyFlagOverrides`**: the old
  query-command mutation of the bag copy (`conf.ReplyMode = true` before
  the call) became `if DirReply.Changed() { tConf.DirReplyMode = …;
  tConf.ReplyMode = true }` inside the cascade — same observable outcome
  (dirscope e2e green), one fewer moving part; the
  `applyDirReplyChatID` gate simplified to `DirReply.Value()`.
- **Dead cascades dropped**: `PhotoOutput`/`VideoOutput` override branches
  compared a field no flag ever set (no `-photo-output` flag exists) —
  removed as dead code rather than ported.
- **Setup helpers narrowed**: `setupToolConfig`/`setupCmdBanConfig` take
  plain strings, `setupLookback` a `BoolFlag`, `applyUseSkillsOverride`
  a `StringFlag` — instead of the whole bag.
- **Adapter globals**: `Command` carries optional `Raw`/`NonInteractive`
  group pointers; `Setup` derives `utils.ReadonlyConfig`/`Live` from them
  (nil ⇒ false), keeping the session globals single-sourced. The `Parent`
  field is gone — chat's tree shares one `TextFlags` by closure; the
  audio tree shares its parent's `RawFlag` pointer.

Verification: full e2e green with zero edits (flag names, help output,
scanner arity and cascade outcomes identical); `make qa` exit 0 first
run; binary reinstalled. New pins: `Test_changedSemantics`
(explicit-default-ignored quirk is now a documented, tested contract
rather than an accident of the bag comparison) and
`Test_textFlagsRegistration` (full text flag surface by name).

Post-completion (Lorentz review): `NewIntFlag` deleted — written for
symmetry but dead, since no int flag carries a non-zero default (the
zero-value `IntFlag` suffices). `NewTextFlags`/`NewReplyStdinFlags`/
`NewStringFlag` verified in production use (query/chat composition,
photo/video defaults).

## Review findings

### Review 1 (2026-08-28, holistic `/code-review high`, phases 11–15)

- **CR-03 (Major, reopens):** the audio `transcribe` sub registers only
  `-am/-af/-parallelism` (`Register: f.Register`,
  `internal/audio/cmd.go:80`) and omits the raw group, so `-r` at the
  level the help recommends fails. Reproduced: `clai a t -r file.wav`
  exits 1 with `flag provided but not defined: -r`, while
  `clai -r a t file.wav` works. Every chat sub deliberately re-registers
  raw (`internal/chat/cmd.go:57-65`) because each nesting level is an
  independent namespace (D11); the transcribe sub even shares the
  parent's `Raw` pointer, so the value flows once registered — a pure
  registration omission, not a budgeted breaking change.
- **CR-10 (Minor, logged):** the photo/video flag surface (`Flags`,
  `NewFlags`, `Register`, the ~20-line `ApplyFlagOverrides` cascade,
  the `conf.go` alias blocks, the `mustSet` test helper) is a verbatim
  pair — branch-introduced `dupl -t 80` clone groups absent on `main`
  (`photo/cmd.go:103` vs `video/cmd.go:89` etc.). The
  ExpectReplace→Reply→StdinReplace precedence cascade is now maintained
  in two places (three, counting `text/cmd.go:95`). Fix direction: a
  shared parameterized media group plus an `Apply` method on
  `ReplyStdinFlags` in `internal/flags.go`. Supersedes the phase-12
  "accepted clone" note, which predates the phase-15 flag rework.

#### Fixes (2026-08-28, same session as the review)

Both fixed; `make qa` exit 0, binary reinstalled.

- **CR-03 fixed** — the audio `transcribe` sub now registers the raw group
  alongside its own flags (`raw.Register(fs); f.Register(fs)`), matching
  how every chat sub re-registers raw. Pinned by
  `Test_goldenFile_AUDIO_raw_flag_at_verb_level`, which runs both
  placements (`a t -r …` and `-r a t …`).
- **CR-10 fixed** — photo and video now share `internal.MediaFlags`:
  `MediaFlagSpec` carries the medium's flag names, descriptions and dir
  default; `Apply(MediaConfig)` (a struct of field pointers) runs the one
  override cascade. `photo.Flags`/`video.Flags` are type aliases of the
  group, so both packages' public API is unchanged while the struct,
  `Register` and the 20-line cascade exist once. The cascade is tested
  once in `internal.Test_mediaFlagsApply` (table: no-flags,
  flags-beat-file, flag-equal-to-default ignored, `-i` placeholder, `-I`
  overrules `-i`); the domain tests shrank to field-binding checks.
  Verified with `dupl -t 80`: the photo/video `ApplyFlagOverrides`,
  `conf.go` and `mustSet` clone groups are all gone (30 → 28 groups
  repo-wide, none naming photo or video).

  **Deliberate non-unification:** text keeps its own reply/stdin branches.
  It has no `StdinReplace.Changed()` branch, and
  `git show HEAD:internal/setup_flags.go` confirms the pre-refactor
  `applyFlagOverridesForText` had none either — only the photo/video
  cascades did. Sharing one implementation across all three would have
  changed text behavior, so the media group covers photo and video only.
