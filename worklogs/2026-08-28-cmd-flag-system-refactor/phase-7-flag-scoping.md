# Phase 7 — per-command flag scoping and alias values

**Status:** Complete
[Worklog README](./README.md)

## Goal

Split the shared flag surface so each command registers only the flags it
uses, and replace the short/long alias mutual-exclusion machinery with shared
`flag.Value` registrations.

## Specification

Repo: clai, branch `refactor-flag-system`. Affected: `internal/cmds/`
(registrars), `internal/setup_flags.go` (largely deleted),
`internal/setup_flags_test.go` (rewritten per command). Depends on phase 6.

1. **Alias technique** (decision D8). One value, two names:

   ```go
   fs.Var(&c.chatModel, "cm", desc)
   fs.Var(&c.chatModel, "chat-model", desc)
   ```

   with small `stringVal`/`boolVal`/`intVal` types implementing `flag.Value`
   (+`IsBoolFlag()` for bools — required for the phase-2 scanner arity) and
   recording explicit-set. This deletes `utils.ReturnNonDefault` call sites
   in flags code, `resolveIntAlias`, `exitWithFlagError` (including its
   `os.Exit(1)` — errors now flow through `cmd.Run`), and the `fs.Visit`
   set-detection. Explicit-set replaces every `*Set bool` field's derivation
   in `Configurations` (`MaxTokensSet`, `UseLookbackSet`, …) — the
   `Configurations` struct and `applyFlagOverridesFor*` cascade semantics are
   preserved unchanged; only how the struct is populated changes. Passing
   both aliases is no longer an error: last one parsed wins (stdlib
   repeat-flag semantics; README regression budget does not even need this —
   it is strictly more permissive).
2. **Scope table** (registrar composition; shared groups defined once):

   | Group | Flags | Commands |
   |---|---|---|
   | common | `-r/-raw` | all querier commands + `replay`, `dre`, `chat` |
   | text | `-re/-reply`, `-dre/-dir-reply`, `-cm/-chat-model`, `-t/-tools`, `-s/-skills`, `-cmd-ban`, `-lb/-lookback`, `-mt/-max-tokens`, `-mtc/-max-tool-calls`, `-max-tool-calls-after-handover`, `-rf/-response-format`, `-g/-glob`, `-p/-profile`, `-prp/-profile-path`, `-asc/-add-shell-context`, `-I/-replace`, `-i`, `-n/-non-interactive` | `query`, `chat`, `glob` |
   | photo | `-pm/-photo-model`, `-pd/-photo-dir`, `-pp/-photo-prefix`, `-re/-reply`, `-I/-replace`, `-i` | `photo` |
   | video | `-vm/-video-model`, `-vd/-video-dir`, `-vp/-video-prefix`, `-re/-reply`, `-I/-replace`, `-i` | `video` |
   | audio | `-am/-audio-model`, `-af/-audio-format`, `-parallelism` | `audio` |
   | macro | `-n/-non-interactive` | `setup`, `tools`, `profiles` (deviation, see notes: these are macro-input commands; without `-n` the auto-exit behavior would become inexpressible) — also part of the text group |
   | none | — | `help`, `version`, `confdir`, `completion`, `__complete`, `replay`+`dre` (beyond common) |

   Post-completion upgrade (2026-08-28, user-directed): the text group was
   split — `chat` now owns only the agent subset (`-cm`, `-t`, `-cmd-ban`,
   `-lb`, `-mt`, `-mtc`, `-max-tool-calls-after-handover`, `-g`, `-p`,
   `-prp`, `-n` + raw); `-re`, `-dre`, `-s`, `-rf`, `-asc`, `-I`, `-i`
   remain query/glob-only. Chat subs additionally register their own level:
   continue = raw+agent, list/delete = raw+`-n`, dir/dirv2 = raw, help =
   none. The stdlib flagset output is silenced upstream (dispatcher owns
   errors/help), removing the duplicated "Usage of x:" dump on `-h`.

   Arity convention holds across commands (same name ⇒ same arity — audit as
   part of this phase; the table above already satisfies it). The convention
   is scoped to top-level flagsets; once phase 8 nests, each level is its
   own namespace and may reuse names/abbreviations freely (README D11).
3. **Consequences to encode in help text:** flags now error on commands that
   don't own them (regression budget R-a); pre-command flags still work for
   any flag the *resolved* command owns (phase-2 scan). Each command's
   `Help()` lists exactly its own flags, generated from its flagset
   (`fs.PrintDefaults` into a buffer or a small formatter) — no hand-
   maintained flag lists left anywhere.
4. `internal/setup_flags.go` retains only `Configurations` and the
   `applyFlagOverridesFor*` / `applyProfileOverridesForText` functions;
   `parseFlags` is deleted.

## Integration contract

| # | Scenario | argv | Observable result | Prohibited |
|---|----------|------|-------------------|------------|
| 1 | owned flag accepted | `clai q -mt 100 hi` (mock vendor) | stoploss max-tokens 100 applied (`MaxTokensSet` semantics intact: explicit 0 overrides file) | — |
| 2 | unowned flag rejected | `clai photo -cm x hi` | "flag provided but not defined: -cm" via cmd error path, exit 1 | silent acceptance |
| 3 | both aliases, last wins | `clai q -cm a -chat-model b hi` | model `b` used | mutual-exclusion error |
| 4 | explicit zero overrides file | `clai q -mt 0 hi` with file-configured stoploss | stoploss unlimited (explicit-set `-mt 0` must beat the config-file value — the exact behavior `MaxTokensSet` exists for) | file value winning |
| 5 | bool alias arity | `clai -re q hi` | `-re` consumes no value token (scanner + IsBoolFlag) | `q` eaten as value |
| 6 | audio flags on audio only | `clai a t f.wav -af text` (mock) | text transcript | — |
| 7 | per-command help lists own flags | `clai photo -h` | photo flags only, exit 0 | text-only flags listed |

## Acceptance criteria

- [x] Scope table implemented exactly; recorded deviations (if any) justified
      in implementation notes and mirrored back into this table.
      Evidence: group registrars in `setup_flags.go` + per-command `register`
      composition in `cmds.go`; one deviation (macro `-n` group) mirrored
      into the table above.
- [x] `ReturnNonDefault` (flag call sites), `resolveIntAlias`,
      `exitWithFlagError`, `parseFlags`, and the `fs.Visit` block are gone;
      no `os.Exit` remains in flag handling.
      Evidence: all five deleted (grep); `utils.ReturnNonDefault` itself
      removed too — zero users remained; `grep os.Exit internal/*.go` empty.
- [x] All seven contract rows automated; the full `applyFlagOverridesFor*`
      precedence-cascade test suite passes unmodified (population changed,
      semantics identical).
      Evidence: rows 1/4 via stoploss e2e (`-max-tokens=5`,
      `-max-tool-calls=1`) + `Test_aliasValues` explicit-zero/set-detection +
      `Test_applyFlagOverridesForText_Stoploss` (unmodified); rows 2, 3, 7 +
      error rows via new `Test_e2e_flag_scoping`; row 5 via reply-mode
      dirscope e2e (`-r -re -cm mock_test q reply`, flags before command);
      row 6 via audio e2e (`-am test -af text a t …`). Cascade suite
      (`Test_applyFlagOverridesForTest`, `..._Stoploss`) unmodified, green.
- [x] Every command's `Help()` flag list is flagset-derived (no literal flag
      tables in help strings).
      Evidence: `claiCommand.Help()` renders `fs.PrintDefaults()`; the
      hand-maintained flag block in main.go's `usage` deleted (R-d) — top
      help now shows the generated command table + per-command `-h` pointer.
- [x] `make qa` exit 0.
      Evidence: exit 0, 2026-08-28 post-phase.

## Error coverage

| Failure condition | Expected outcome | Test |
|---|---|---|
| Invalid int value (`-mt abc`) | flagset parse error, exit 1, message names the flag | unit |
| Invalid skills value (`-s bogus`) | existing "expected '*' or 'none'" error preserved | existing test kept green |
| Unowned flag pre-command (`-pm x q hi`) | scanner resolves `q`; q's flagset rejects `-pm`, exit 1 | e2e |
| `-i` with `-I` combined | existing precedence (`-I` wins) preserved | existing test kept green |

## Implementation notes

**Session: Claude, 2026-08-28 (implementation, same session as phases 1–6).**

Deltas from spec:

- **Scope-table deviation (mirrored above): `-n/-non-interactive` also
  registers on `setup`, `tools`, `profiles`.** These consume macro inputs
  (`extractMacroInputs` + `utils.Live`); with the table as written,
  `clai -n s 0 0 q` — used by the setup macro e2e suite and real macro
  workflows — would become inexpressible, violating the README's
  expressiveness-parity rule. Implemented as a tiny `registerNonInteractive`
  group, also composed into the text group.
- Registrar decomposition avoids duplicate registration: `registerRaw`
  (common), `registerReplyStdin` (re/I/i, shared by text/photo/video),
  `registerText`, `registerPhoto`, `registerVideo`, `registerAudio`,
  `registerNonInteractive`. Value types `stringVal`/`boolVal` (+
  `IsBoolFlag`)/`intVal` with explicit-set live in `setup_flags.go`;
  `claiFlags` holds one value per logical flag and `configurations()`
  materializes the unchanged `Configurations` struct (incl. all `*Set`
  fields from `.set`).
- The joke `-A-helpful-nonexisting-flag` was dropped — with per-command
  `-h` now printing real flagset-derived help, it would have polluted every
  command's flag list.
- **Monolithic usage flag block deleted** (spec item 3's "no hand-maintained
  flag lists left anywhere" + R-d): main.go's `usage` template now takes
  the generated sorted command table (`cmd.DescribeSubcommands`, hidden
  `__complete` filtered out), config dir, cache dir; prerequisites and all
  examples preserved. `printHelp` shrank from 19 interpolations to 3.
- `utils.ReturnNonDefault` deleted entirely (not just call sites) — the
  flags code was its last consumer.

Test upgrades (all justified by R-a/R-d):

- `main_setup_macro_e2e_test.go`: `-n -r -cm test s …` → `-n s …` (setup
  no longer owns `-r`/`-cm`; both were inert in these tests).
- `main_help_e2e_test.go`: flag-block + alignment assertions → command-table
  assertions (+ `__complete` hidden); alignment helper deleted.
- `main_audio_e2e_test.go`: `-am/-af/-parallelism` now asserted in
  `audio -h` output instead of top help.
- `main_chat_e2e_test.go` `TestMainHelpDocumentsChatDirV2`: asserts `dirv2`
  in the rendered help (chat command description) instead of the usage
  const.
- `main_confdir_e2e_test.go`: exact old command-table line → name +
  description assertions.
- `internal/setup_flags_test.go`: `TestSetupFlags` table kept, runner swapped
  to a union-of-groups parser; two "conflicting aliases rejected" cases →
  "last one wins" (D8 semantics); `Test_resolveIntAlias` replaced by
  `Test_aliasValues` (same four-state matrix + last-wins + IsBoolFlag).

Verification:

- `make qa` → exit 0.
- Full e2e suite green (incl. `-race`); new `Test_e2e_flag_scoping` covers
  contract rows 2, 3, 7 and the error matrix.

## Review findings

_(appended by reviewers)_
