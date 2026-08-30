# Phase 8 — nested commands via `Subcommander`

**Status:** Complete
[Worklog README](./README.md)

## Goal

Convert clai's ad-hoc verb dispatchers — chat, audio, tools, profiles — to
first-class `Subcommander` trees with per-subcommand flagsets.

## Specification

Repo: clai, branch `refactor-flag-system`. Affected: `internal/cmds/`,
`internal/setup_audio.go` (verb switch dissolved), chat subcommand routing in
`internal/` + `internal/chat`, `internal/tools/cmd.go`,
`internal/profiles/cmd.go`. Depends on phases 6–7.

1. **Trees** (keys keep today's aliases):
   - `chat|c` → `continue|c`, `delete|d`, `list|l`, `dir`, `dirv2`,
     `help|h`. Parent `Run` with an unmatched/absent positional keeps
     today's `chat.New` behavior (it self-detects list-mode macros).
   - `audio|a` → `transcribe|t`, `help|h`. Parent `Run` with no verb prints
     the namespace help to stderr and errors (current behavior). Audio flags
     (`-am/-af/-parallelism`) move from `audio` to `transcribe` — the first
     realized benefit of level-scoped flags (D11): the flag sits next to its
     verb, `clai a t -af text f.wav`. Pre-command placement
     (`clai -af text a t f.wav`) now **errors** — accepted as regression
     budget R-e; existing e2e tests asserting the old placement are
     upgraded to the new form, and the audio parent's `Help()` shows the
     new idiom.
   - `tools|t` → `list` (default via parent `Run`), plus detail-view
     positional (`clai tools <name>`) staying on the parent as an
     `ArgCompleter`-ready positional (`tools.SubCmd` logic re-homed).
   - `profiles` → `list` (default via parent `Run`; `profiles/cmd.go` logic
     re-homed).
2. **Read-only chat invariant.** `NoCreateConfig` for `list|l`, `dir`,
   `dirv2`, `help|h` moves from string sniffing
   (`isReadOnlyChatSubCommand`/`chatSubCommand`) into those subcommands'
   `Setup` — the read-only property becomes structural. `delete` and
   `continue` keep full config access. `isReadOnlyChatSubCommand` and
   `chatSubCommand` are deleted.
3. **Macro inputs.** `extractMacroInputs` (SETUP/TOOLS/PROFILES positional →
   `setup.Input` injection) is preserved; it moves into the respective
   commands' `Setup`. `extractMacroInputs` itself is deleted.
4. **Per-subcommand help.** `clai chat -h` lists the sub table (upstream
   `DescribeSubcommands`); `clai chat list -h` shows list-specific help.
   The `audioNamespaceHelp` literal becomes the audio parent's `Help()`.
5. Dirscope behaviors are contract-frozen: `-dre` continues the bound chat in
   place (records/upserts), plain `-re` forks without recording; `chat dir`
   keeps stable v1 output, `dirv2` the token-usage variant.

## Integration contract

| # | Scenario | argv | Observable result | Prohibited |
|---|----------|------|-------------------|------------|
| 1 | chat list via aliases | `clai c l` / `clai chat list` | identical listing, exit 0 | config file creation (read-only dir) |
| 2 | chat continue | `clai c c 1` (seeded chat, mock vendor) | conversation continued | — |
| 3 | chat delete | `clai c d <id>` (seeded) | chat file removed | other chats touched |
| 4 | audio verb flags after verb | `clai a t -af text f.wav` (mock) | text transcript | flag error |
| 5 | sub flag before command rejected (R-e) | `clai -af text a t f.wav` | exit 1: scanner treats `-af` as unknown bool-arity → `text` becomes the candidate → unknown-command error naming `text` (documented outcome pinned in test) | exit 0; `-af` silently ignored |
| 6 | audio no verb | `clai a` | namespace help on stderr, exit 1 | exit 0 |
| 7 | tools default + detail | `clai tools` / `clai tools <name>` | list / detail view, exit 0 | — |
| 8 | profiles default | `clai profiles` (seeded profile) | listing, exit 0 | — |
| 9 | nested help | `clai chat -h`, `clai chat list -h` | sub table / list help, exit 0 | wrong level's help |
| 10 | unknown chat sub | `clai chat banana` | parent fallthrough → today's chat arg handling (documented outcome pinned in test) | dispatcher `ArgNotFoundError` |

## Acceptance criteria

- [x] All four trees implemented; string verb switches
      (`handleAudio`'s switch, chat sub sniffing, `tools.SubCmd`/
      `profiles.SubCmd` arg juggling) deleted — grep proves no caller.
      Evidence: `internal/cmds.go` trees; `handleAudio`,
      `isReadOnlyChatSubCommand`, `chatSubCommand`, `extractMacroInputs`,
      `tools.SubCmd`, `profiles.SubCmd` all deleted; grep clean.
- [x] All ten contract rows automated (rows 1–3 against a seeded temp config
      dir; row 1 asserts no writes).
      Evidence: row mapping in implementation notes; new
      `main_nested_e2e_test.go` (rows 1, 5, 9, 10) + existing suites
      (rows 2–4, 6–8), all green.
- [x] Read-only chat subs prove structurally read-only: row-1-style test runs
      against a `chmod`-read-only config dir without error.
      Evidence: `Test_e2e_chat_list_on_readonly_config_dir` (chmod 0o555
      whole config tree) + `Test_e2e_chat_list_does_not_create_config_dir`
      (missing dir, kept green).
- [x] Existing chat/audio/tools/profiles e2e suites pass, adapted only for
      help-text shape (R-d) and flag placement (R-e) — each adapted
      assertion listed in implementation notes with old → new form, proving
      expressiveness is preserved (D11: every old invocation has a new-form
      equivalent).
      Evidence: old→new table in implementation notes; suites green.
- [x] `make qa` exit 0.
      Evidence: exit 0 (one unrelated load-induced flake in
      `pkg/tools` async timing test under doubled parallel load; stable
      standalone with `-race -count=3`, `pkg/` untouched by this effort).

## Error coverage

| Failure condition | Expected outcome | Test |
|---|---|---|
| `clai a t` (no file) | "missing audio file argument" preserved, exit 1 | existing test kept green |
| `clai c d` (no id) | chat delete's current missing-arg error preserved | existing/adapted test |
| Unknown audio verb `clai a x` | parent fallthrough → namespace help + "unknown audio verb" error preserved | existing test kept green |
| Read-only sub on missing config dir | works without creating it (current `NoCreateConfig` guarantee) | e2e |

## Implementation notes

**Session: Claude, 2026-08-28 (implementation, same session as phases 1–7).**

Design deltas:

- **Subcommands share the parent's `claiFlags`.** `claiCommand` gained
  `parent`/`subs` fields and a `Subcommands()` method (nil map = plain
  command, upstream-tested behavior). A sub's `Flagset()` forces the
  parent's first and reuses its `claiFlags`, so parent-level flags
  (`chat -r list`) and sub-level flags (`a t -af text`) land in one value
  set while each level parses its own namespace (D11). Safe by
  construction: the dispatcher's arity union touches every top-level
  `Flagset()` before any descent.
- Chat tree: subs reconstruct the legacy `["chat", verb, args...]` shape;
  `continue`/`delete` use `fullChatSetup`, `list`/`dir`/`dirv2`/`help` use
  `readOnlyChatSetup` (`utils.NoCreateConfig = true` structurally). The
  parent keeps `fullChatSetup` — unmatched/absent positionals reach
  `chat.New` exactly as before (row 10: "unknown subcommand: 'banana'").
  Canonical verbs are passed to `chat.New` (it accepts canonical + alias).
- Audio tree: `transcribe|t` owns the audio flag group and calls
  `setupAudioTranscribeQuerier` directly; `help|h` prints the namespace
  help; the parent (owns `-r` only) errors with "missing audio verb" /
  `unknown audio verb: %q` after printing the namespace help to stderr —
  same texts as the deleted `handleAudio`. `audioNamespaceHelp` updated to
  the post-verb flag idiom.
- Tools/profiles: `tools.SubCmd` split into `tools.List()` +
  `tools.Detail(name)`; `profiles.SubCmd`/`runProfilesList` became
  `profiles.List()`. Parent `Run` keeps default-list + detail (tools) /
  default-list + unknown-subcommand error (profiles); each gains a `list`
  sub. Macro-input injection is a 4-line `injectMacroInputs` called from
  the setup/tools/profiles setups; `extractMacroInputs` deleted (its unit
  test deleted with it — behavior covered by setup-macro and chat-list
  e2e suites).

Contract row mapping:

| Row | Test |
|---|---|
| 1 | new `Test_e2e_chat_list_alias_equivalence` (identical output `c l` vs `chat list`); no-writes via `Test_e2e_chat_list_does_not_create_config_dir` |
| 2 | existing `-r -cm test c c 0` chat e2e (kept green) |
| 3 | existing chat-list delete macros (`c l 0 d`, `c l d d`, range delete) kept green |
| 4 | audio e2e, upgraded placement (`a t -am test -af text …`) |
| 5 | new `Test_e2e_audio_sub_flag_before_command_rejected` (pins `'text' is not a valid argument`) |
| 6 | existing `Test_goldenFile_AUDIO_*` no-verb case (kept green) |
| 7 | existing `tools` / `tools <name>` e2e (kept green) |
| 8 | existing profiles e2e (kept green) |
| 9 | new `Test_e2e_nested_help` (parent sub table via upstream helpText; sub-only help) |
| 10 | new `Test_e2e_unknown_chat_sub_falls_through` |

Adapted e2e assertions (old → new, R-e; expressiveness preserved):

- `-am test audio transcribe F` → `audio transcribe -am test F`
- `-am test -af text a t F` → `a t -am test -af text F` (also stdin `-`)
- `-am test a t F` → `a t -am test F` (missing-file, cascade, corrupt-config
  variants)
- `-am test -af yaml a t F` → `a t -am test -af yaml F`
- `-am mystery9000 a t F` → `a t -am mystery9000 F`
- `-am whisper-1 a t F` → `a t -am whisper-1 F`
- `-am test -af json a t F` → `a t -am test -af json F`
- Unit: `tools.SubCmd(ctx, ["tools"])`→`tools.List()`,
  `SubCmd(ctx, ["tools", name])`→`tools.Detail(name)`;
  `profiles.runProfilesList()`→`profiles.List()`; profiles `SubCmd`
  default/unknown tests → `TestList_Succeeds` (unknown-subcommand now
  parent-level, covered by the profiles command's run func).

Verification: `make qa` → exit 0; full e2e green. New chat-list tests pin
`HOME` to a temp dir (like the existing suite) — without it the foreign-
session scan of the real `~/.claude/projects` added ~2.3s per list run.

## Review findings

_(appended by reviewers)_
