# Phase 13 — setup/composition code moves into its domain packages

**Status:** Complete
[Worklog README](./README.md)

## Goal

Dissolve `internal/setup.go`, `internal/setup_audio.go`,
`internal/setup_config_migrations.go` and most of
`internal/create_queriers.go`: text-querier composition → `internal/text`,
audio composition → `internal/audio`, config lifecycle → `internal/setup`.

## Specification

Repo: clai, branch `refactor-flag-system`. Depends on phase 12. Directed by
Lorentz (2026-08-28): "move setup code from setup.go into the respective
packages".

**Cycle facts (verified 2026-08-28):** no vendor imports `internal/text`,
`internal/audio` or `internal/setup`; `vendors/gemini` imports
`internal/chat` (harmless — chat imports no domain movers);
`vendors/openai` still imports `internal/photo`/`video`/`tools`, so the
photo/video factories stay in package `internal`; `pkg/tools` imports only
`pkg/text/models`, so the audio tool-bridge init can live in
`internal/audio`; `internal/setup` imports `internal/text`, so text can
never import setup — the trust-prompt input reader is injected instead.

1. **→ `internal/text`** (query/chat composition):
   `SetupTextQuerierWithConf` → `text.SetupQuerier(ctx, chatMode bool,
   confDir string, flagSet clicmd.Configurations, args []string,
   trustInput io.Reader)`; `CreateTextQuerier`+`selectTextQuerier` →
   `text.CreateQuerier(ctx, conf)`; the helper cluster moves unexported
   (`setupToolConfig`, `setupCmdBanConfig`, `setupLookback`,
   `applyUseSkillsOverride`, `profileSetsSkills`,
   `shouldLogSkillDiscovery`, `skillRuntimeAdapter`,
   `formatSkillTrustPrompt`, `mustGetwd`); `ApplyDirReplyChatID` becomes
   unexported in text (only the query command uses it). The `Mode` enum is
   deleted — `chatMode bool` replaces it. `trustInput` replaces the direct
   `setup.Input` read (text→setup would cycle); `main.go` supplies
   `func() io.Reader { return setup.Input }`-style access so the mutable
   global is read at call time, and `setup.Input`/`main_test.go` stay
   untouched.
2. **→ `internal/audio`**: `SetupAudioTranscribeQuerier` +
   `resolveAudioInput` (the audio command calls it directly — its
   `SetupTranscribeQuerier` dep is deleted), `createAudioSplitter`,
   `CreateAudioQuerier`, and `audioTranscribeEngine` **with its
   `init()` bridge** (`pkgtools.AudioTranscribeEngine`). `pkg/agent` gains
   a blank import of `internal/audio` so the `audio_transcribe` tool stays
   wired for library consumers (today that linkage rides
   `pkg/agent → internal`, which this phase severs).
3. **→ `internal/setup`** (config lifecycle): `ConfigRunPrep`,
   `migrateConfigs`, `setup_config_migrations.go` (old-config types +
   `migrateOldChatConfig`/`migrateOldPhotoConfig`), `LoadPhotoConfig`.
   `setup.Command()` drops its `CommandDeps` and calls `ConfigRunPrep`
   directly.
4. **Package `internal` residue**: `CreatePhotoQuerier`,
   `CreateVideoQuerier` (openai-cycle-bound) and the `ProfileHelp`
   re-export (e2e freeze). Deleted as dead: `PromptConfig` (no
   references), `Mode`/`QUERY`/`CHAT`, the `Configurations` alias and
   `defaultFlags` mirror if unreferenced after the moves.
5. **`main.go` wiring**: `configPrep` → `setup.ConfigRunPrep`; query deps
   shrink to ConfigPrep + trust-input access; chat's `SetupQuerier`
   closure wraps `text.SetupQuerier(..., chatMode=true, ...)`; photo's
   `LoadConfig` → `setup.LoadPhotoConfig`; audio deps shrink to
   ConfigPrep.
6. **`pkg/agent`**: `internal.CreateTextQuerier` → `text.CreateQuerier`;
   the internal import disappears (replaced by the blank audio import).
   Error strings (e.g. "failed to CreateTextQuerier") stay verbatim —
   `pkg/agent` tests pass unmodified.
7. **Tests move with their code** (`setup_test.go`,
   `setup_tool_config_test.go`, `setup_cmd_ban_test.go`,
   `setup_lookback_test.go` → text; `setup_audio_test.go`,
   `create_queriers_audio_test.go` → audio;
   `setup_config_migrations_test.go` → setup; `create_queriers_test.go`
   split by factory). Phase-12 unit tests may adapt to the shrunk deps
   structs.
8. **Behavior freeze**: zero main-package e2e edits; `pkg/agent` tests
   unmodified; docs re-targeted (`cmd-dispatch.md` key-files table,
   `config.md`/`colours.md` ConfigRunPrep references, any doc naming
   `internal/setup.go`).

## Integration contract

Relocation phase — the externally observable contract is "nothing moved":

| # | Scenario | Observable result | Prohibited |
|---|----------|-------------------|------------|
| 1 | full e2e suite (`main` package) | passes with zero test edits | any assertion change |
| 2 | `pkg/agent` suite | passes unmodified | error-string drift |
| 3 | `audio_transcribe` tool via pkg/agent linkage | engine wired (non-nil) when `pkg/agent` is imported | silent nil-engine regression |
| 4 | config migrations on first command run | same upgrade announcements/warnings | — |

## Acceptance criteria

- [x] `internal/setup.go`, `internal/setup_config_migrations.go` deleted;
      residue per spec item 4 **plus the audio composition** (delta 1 in
      notes: `audio/generic` imports `internal/audio`, so audio is
      openai-cycle-bound like photo/video — `setup_audio.go` stays, with
      the factory block in `create_queriers.go`).
      Evidence: file listing; package doc on `create_queriers.go` states
      the constraint.
- [x] Contract rows 1–2: both suites green with zero edits. Row 3: a test
      in `pkg/agent` (or audio) proves the engine is wired through the
      blank import. Row 4: existing migration tests green in their new
      home.
      Evidence: `go test . -race` green, `pkg/agent` green unmodified
      (except the new `engine_linkage_test.go`);
      `Test_audioTranscribeEngineWired`; migration tests in
      `text/config_migration_test.go` + `setup/config_migrations_test.go`.
- [x] No `Mode`, `PromptConfig`, or dangling alias references (grep).
      Evidence: `Mode`/`QUERY`/`CHAT`, `PromptConfig`, the
      `Configurations` alias and `defaultFlags` mirror all deleted with
      `setup.go`.
- [x] Moved-code packages hold ≥70% coverage; numbers recorded.
      Evidence: text 81.3%, setup 77.6%, audio 91.4%, chat 71.7%
      (`make qa` run). Residual package `internal` sits at 47.9%
      (pre-existing factory-routing coverage shape, not new code).
- [x] Docs re-targeted; grep for `internal/setup.go` in `architecture/`
      is clean.
      Evidence: `cmd-dispatch.md`, `config.md`, `colours.md`, `audio.md`
      updated; repo-wide grep clean.
- [x] `make qa` exit 0.
      Evidence: exit 0 (first run).

## Error coverage

| Failure condition | Expected outcome | Test |
|---|---|---|
| Broken theme at `setup.ConfigRunPrep` | warn-and-continue preserved | existing behavior, e2e kept green |
| Unknown transcriber model | same routing error text | moved audio tests kept green |
| Trust prompt with nil input reader | same behavior as today (`table.ReadUserInputFrom(nil)`) | skills e2e kept green |
| `audio_transcribe` with unwired engine | pkg/tools' explicit nil-engine error (unchanged) | existing pkg/tools test |

## Implementation notes

**Session: Claude, 2026-08-28 (extension, same session as phases 11–12).**

Deltas from the specification:

1. **Audio composition could not move (spec item 2 reduced).** Discovered
   at build time: `internal/audio/generic` imports `internal/audio`
   (`transcriber.go`), and both `vendors/openai` and `vendors/openrouter`
   import `audio/generic` — so `internal/audio` importing any transcriber
   vendor is a cycle. The audio factories (`createAudioSplitter`,
   `CreateAudioQuerier`, `audioTranscribeEngine` + init) and
   `SetupAudioTranscribeQuerier`/`resolveAudioInput` therefore stay in
   package `internal`, exactly like photo/video; the audio command keeps
   its phase-12 injected dep. The cycle-facts paragraph in this spec was
   wrong about audio ("no vendor imports internal/audio" — true directly,
   false transitively); `create_queriers.go`'s package doc now records
   the real constraint.
2. **`migrateOldChatConfig` → `text.MigrateOldChatConfig`** (not setup):
   `text.SetupQuerier` loads `textConfig.json` with it, and text cannot
   import setup. It is chat/text-domain anyway; `setup.migrateConfigs`
   calls it cross-package. `migrateOldPhotoConfig` stays in setup with
   `LoadPhotoConfig`.
3. **`pkg/text/full.go` also composed via `internal.CreateTextQuerier`**
   (spec only named pkg/agent) — updated to `text.CreateQuerier` with the
   same blank `internal` import for the engine bridge.
4. **The engine bridge init stays in `internal`** (consequence of delta
   1), so `pkg/agent`/`pkg/text` blank-import `internal` rather than
   `internal/audio`. `Test_audioTranscribeEngineWired` pins the linkage.
5. **Query command's querier dep dissolved entirely**: `OnSetup` calls
   `text.SetupQuerier`/`applyDirReplyChatID` in-package;
   `QueryCommandDeps` is now `{ConfigPrep, TrustInput}`. Chat's stays
   injected (chat cannot import text). `setup.Command()` lost its deps
   struct and calls `ConfigRunPrep` directly.

Relocation map as landed: `text/setup_querier.go` (SetupQuerier + tool/
cmd-ban/skills/lookback helpers + skill trust prompt + dir-reply chat-id),
`text/create_querier.go` (vendor routing), `text/config_migration.go`;
`setup/config_lifecycle.go` (ConfigRunPrep, migrateConfigs,
LoadPhotoConfig), `setup/config_migrations.go` (photo migration);
`internal` residue: `create_queriers.go` (photo/video/audio factories +
engine init + ProfileHelp re-export) and `setup_audio.go`.

Tests: `setup_test.go` → `text/setup_querier_run_test.go` (adapted to
`SetupQuerier(ctx, false, …)`), tool-config/cmd-ban/lookback tests →
text (flags literals retyped to `clicmd.Configurations`; the duplicated
`captureStdout` helper was dropped in favor of text's existing one —
clearing a phase-12 dupl clone), `create_queriers_test.go` split
(vendor-routing suite → text, photo part stays), chat-config migration
test → text, photo migration test → setup. `Test_SetupQuerier_chatMode`
added (mock vendor, temp config).

Verification: full e2e green with zero edits; `make qa` exit 0 first
run; no new dupl clones on moved files; binary reinstalled.
`SetupQuerier`'s trust-prompt reader is passed by the composition root
(`func() io.Reader { return setup.Input }`), so `setup.Input` and
`main_test.go` stayed untouched as specified.

## Review findings

### Review 1 (2026-08-28, holistic `/code-review high`, phases 11–15)

- **CR-06 (Minor, logged — perf):** read-only chat subs
  (list/dir/dirv2/help) set `utils.NoCreateConfig = true` then still run
  the full migration pass (`internal/chat/cmd.go:133` → ConfigPrep →
  `migrateConfigs`), which reads and JSON-parses four mode configs plus
  every `profiles/*.json` and discards every result (the loader never
  writes under NoCreateConfig, `internal/utils/config.go:246-275`).
  Users put `clai -r chat dirv2` in shell precmd hooks, so this dead
  4+N-file pass runs per prompt render. Fix: skip `migrateConfigs` in
  `ConfigRunPrep` when NoCreateConfig/ReadonlyConfig is set — only
  `GetClaiConfigDir` + `LoadTheme` are needed there.
- **CR-07 (Minor, logged — perf/design):** every config-touching
  invocation migrates all four mode configs plus all profiles
  (`internal/setup/config_lifecycle.go:50`), then the dispatched command
  re-reads its own config and re-runs its migration callback a second
  time (e.g. `photo/cmd.go:74` → `setup.LoadPhotoConfig` →
  `migrateOldPhotoConfig` again, idempotent only via the "Super hacky
  dodge" early-return in `config_migrations.go:38-40`; same double-load
  for text/video/audio). Cheaper: migrate only the dispatched domain's
  config and return the already-migrated value through the
  ConfigRunPrep seam instead of discarding it.

#### Fixes (2026-08-28, same session as the review)

- **CR-06 fixed** — `readOnlyChatSetup` calls `internal.PrepTheme()`
  instead of the injected `ConfigPrep`, so list/dir/dirv2/help resolve the
  config dir and load the theme without the migration pass. Behavior is
  identical by construction (they set `utils.NoCreateConfig = true` first,
  under which the loader writes nothing and returns `added=nil`), and the
  dead 4+N-file read is gone from the `clai -r chat dirv2` precmd path.
  The chat `CommandDeps.ConfigPrep` stays — continue/delete still need the
  full prep. Full e2e suite green unmodified.
- **CR-07 not fixed (deliberate)** — the united all-domain migration is
  decision Q5 of the config-migration design (any clai run upgrades every
  mode config and profile); narrowing it to the dispatched domain is a
  user-visible policy change that belongs to Lorentz, not to a review
  cleanup. The remaining half — the dispatched domain's config being
  loaded twice — would require threading a typed config back through the
  `ConfigPrep` seam, re-complicating the composition root that phases
  12–14 simplified, to save one small JSON read per run. The migration
  callbacks are idempotent, so the cost is bounded. Left logged; the hot
  path that actually mattered is covered by CR-06.
