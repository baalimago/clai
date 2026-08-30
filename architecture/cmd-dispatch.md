# Command Dispatch and Flag System Architecture

clai's dispatch is built on `go_away_boilerplate/pkg/cmd`: a command map, an
arity-aware argument scanner, optional `Subcommander` nesting, and a built-in
shell-completion engine. clai contributes one generic adapter type
(package `internal` at the internal root) plus a scope table of per-command flag groups; each
command is defined in its domain package and wired in `main.go`.

## Entry flow

```text
main.go:run(args)
  → cmd.Run(ctx, ["clai", args...], commands(), usage)   # map built in main.go
    → arity-aware scan finds the command token (flags may precede it)
    → the command's own flagset parses the remaining args
    → Subcommander descent: first positional matching a subcommand key
      selects it; each level parses its own flagset
    → leaf.Setup(ctx) → leaf.Run(ctx)      # parents of an executed sub never run
```

`shutdown.Monitor` starts before `cmd.Run`; the cancel func rides the ctx
(`utils.ContextCancelKey`) so nested tool calls can stop the run.

## The command map (`main.go`) and command homes

`main.go` is the composition root: `commands()` builds the
`map[string]cmd.Command` with the keys `query|q`, `chat|c`, `photo|p`,
`video|v`, `audio|a`, `setup|s`, `version`, `replay|re`, `dir-replay|dre`,
`tools|t`, `profiles`, `confdir`. The `completion` command and the hidden
`__complete` protocol are auto-registered by `cmd.Run` (upstream). There is
no `help` command: bare `clai` prints the full usage, and every command's
`-h` prints its Help() text with examples, flags and subcommands (see
[help.md](./help.md)).

Each command lives in its domain package and exposes a
`Command(deps) *internal.Command` constructor:

| Command | Package |
|---|---|
| query | `internal/text` |
| chat, replay, dre | `internal/chat` |
| photo / video / audio | `internal/photo` / `video` / `audio` |
| setup | `internal/setup` |
| tools / profiles | `internal/tools` / `internal/profiles` |
| version / confdir | `internal/version` / `internal/confdir` |

Every factory lives in its domain package: `text.SetupQuerier`/
`text.CreateQuerier` (query composition), `photo.CreateQuerier`,
`video.CreateQuerier`, and audio's transcribe setup + `audio_transcribe`
tool-bridge init. This works because the vendor-shared surface of each
domain sits **below** the vendors — `pkg/text/models` for text,
`photo/generic`, `video/generic` and `audio/generic` for the rest (types,
prompt setup, image saving, transcript parsing) — so vendors never import
a domain root package. `setup.ConfigRunPrep` (theme + united config
migration) and the old-config migrations live in `internal/setup`, which
imports the domain packages and is therefore injected into their commands
from `main.go`. Package `internal` at the root is the shared leaf every
subpackage depends on — organizational machinery only: the
`internal.Command` adapter holding

- `Register` — composes the flag groups the command owns onto its memoized
  flagset,
- `OnSetup` — command-specific setup (config load, querier construction),
- `OnRun` — optional custom run; the default runs `querier.Query` and fires
  the completion bell,
- `Subs` — the `Subcommander` tree wiring; subs share their parent's values
  by closing over the same flag structs,
- `CompleteFlagValueFn`/`CompleteArgsFn` — optional completion hooks; the
  engine reads them off the deepest resolved command, so a sub owning value
  flags carries the hooks too.

Package `internal` imports only `utils`, `models` and upstream
`pkg/cmd`; it must never import a domain package.

## Flag scoping

Flags are alias-aware `flag.Value` primitives
(`StringFlag`/`BoolFlag`/`IntFlag` in `internal/flags.go`): a short and
long alias register against the same value ("last one parsed wins", no
mutual-exclusion machinery), each value carries its own default, and
`Explicit()`/`Changed()` drive the override cascades — `Changed()`
preserves the historic flag-equal-to-its-default-is-ignored semantics.
Cross-command groups (`RawFlag`, `ReplyStdinFlags`, `NonInteractiveFlag`,
`AgentTextFlags`, `QueryTextFlags`, composed as `TextFlags` for
`text.SetupQuerier`) live in package `internal`. Photo and video share the
parameterized `MediaFlags` group (`MediaFlagSpec` names the medium's flags
and dir default; `Apply(MediaConfig)` runs the shared override cascade), so
`photo.Flags`/`video.Flags` are aliases of it; audio owns its own `Flags`
struct. Each
command constructs its flag structs in `Command(deps)` and its closures
capture them — there is no central flag bag. Groups compose per command:

| Group | Flags | Commands |
|---|---|---|
| raw | `-r/-raw` | query, chat (+ subs except help), photo, video, audio, replay, dre |
| reply/stdin | `-re/-reply`, `-I/-replace`, `-i` | query, photo, video |
| agent text | `-cm`, `-t`, `-cmd-ban`, `-lb`, `-mt`, `-mtc`, `-max-tool-calls-after-handover`, `-g`, `-p`, `-prp`, `-n` (+ long forms) | query |
| media tools | `-am`, `-af` (+ long forms) — configure the media tools an agent run may call | query |
| chat | `-r`, `-n`, `-p` (+ long forms) | chat (+ subs) |
| query-only | `-dre`, `-s`, `-rf`, `-asc` (+ long forms) | query |
| photo | `-pm`, `-pd`, `-pp` (+ long forms) | photo |
| video | `-vm`, `-vd`, `-vp` (+ long forms) | video |
| audio | `-am`, `-af`, `-parallelism` (+ `-r`) | audio **transcribe** (sub-level) |
| non-interactive | `-n/-non-interactive` | setup, tools, profiles, chat (+ subs), query (in agent text) |

Each nesting level is an independent flag namespace, but **placement is a
convenience, not a rule**: a flag written at the wrong level of the resolved
path is forwarded to the level that defines it, so these are equivalent:

```bash
clai -parallelism 2 -af text a t f.wav
clai a -parallelism 2 t -af text f.wav
clai a t -parallelism 2 -af text f.wav
```

Per-command `-h` prints a flagset-derived flag list; there are no
hand-maintained flag tables. Help still shows each flag where it is
*defined* — that is what the command reads — so a flag absent from
`clai c -h` is one chat does not use, not one it refuses to parse.

Prompt text whose first word starts with `-` is parsed as a flag (accepted
regression R-b). The escape is the stdlib `--` separator, documented in the
dispatcher usage and in `clai q -h`:

```bash
clai q -- -why does this fail
```

**Flag forwarding (upstream).** `pkg/cmd` resolves a flag to the level that
defines it rather than to the level it was typed at:

- the pre-dispatch scan's value-taking union covers **every** flagset, not
  just top-level ones, so a deep value flag consumes its value instead of
  letting that value be read as the command name;
- while parsing a level, a flag it does not define is moved to an
  already-resolved ancestor (set directly) or held pending for a
  descendant, then injected into that level's args before it parses.
  Injection goes *before* the level's own tokens, so an explicit value at
  the owning level still wins;
- forwarding never loosens validation — the owning flagset parses the
  value, so `-af nonsense` fails exactly as it does at its own level.

**Misplaced-flag hints.** What remains an error is a flag whose owner is
**not on the resolved path** — it would configure a command this run never
reaches, and silently accepting it is the defect the hint exists for:

```text
$ clai -parallelism 2 q hello
flag provided but not defined here: -parallelism
Hint: '-parallelism' belongs to 'audio transcribe' — place it there, after
that command.
```

The owner map (`flagOwners`) walks every registered flagset and its
`Subcommander` descendants, on the error path only.

A flag name may live at several levels: `-am`/`-af` are `query` flags (they
configure the `audio_transcribe` tool an agent run may call) **and**
`audio transcribe` flags (they configure that command's own run), so the
hint lists every owner. On one path the shallowest owner takes the flag.

## Subcommander trees

- `chat` → `continue|c`, `delete|d`, `list|l`, `dir`, `dirv2`, `help|h`.
  Subcommands share the tree's flag values (parent and subs register
  subsets of one shared `ChatFlags`), so `clai chat -r list` and
  `clai chat list -r` both work. `list`/`dir`/`dirv2`/
  `help` are structurally read-only (`utils.NoCreateConfig`). Unmatched
  positionals stay with the parent and reach `chat.New` unchanged.
- `audio` → `transcribe|t`, `help|h`; the parent errors with the namespace
  help when the verb is missing or unknown.
- `tools` → `list`; the parent defaults to listing and treats a positional
  as a tool-detail lookup.
- `profiles` → `list`; the parent defaults to listing.

## Config prep and sentinels

`setup.ConfigRunPrep` (config dir + theme + united config migration),
injected from `main.go`, runs only in config-touching commands: query, chat
(continue/delete), photo, video, audio(transcribe), setup.

Commands that render content without reading a mode config call
`internal.PrepTheme` instead (config dir + theme, no migration): `replay`,
`dir-replay` and the read-only chat subs (`list`, `dir`, `dirv2`, `help`).
They need the theme for role colors and the completion bell, while the
migration pass would be a no-op under `utils.NoCreateConfig` — and
`clai -r chat dirv2` runs on shell-prompt hot paths. `completion`,
`__complete`, `confdir` and `version` touch no config at all.

Clean exits use sentinels honored by `cmd.Run` via `errors.Is`:
`cmd.ErrUserInitiatedExit` (new code) and `table.ErrUserInitiatedExit`
(TUI-originated) both yield a silent exit 0. Commands that finish normally
return nil.

## Completion

The engine lives upstream (`pkg/cmd/completion.go`): command/subcommand/flag
name suggestions derive from the registered map and flagsets, and the
`__complete` protocol (`value\tkind` lines, kinds `plain|file|dir`) plus the
bash/zsh scripts are generic. clai plugs in value sources via hooks in
`internal/completion.go` (`CompletionSources`; the tool-name lookup
is injected by the command packages, since package `internal` must not import the
tool registry):

- `-cm` → model history, `-p` → profiles (query, chat and every chat sub),
  `-asc` → shell contexts, `-t` → tool names (comma-split, from
  `tools.Names`), `-prp` → file kind;
- `-pd`/`-vd` → dir kind (photo/video);
- prompt commands suppress positional completion once prompt text begins;
- `tools` completes tool names for its detail positional.

Data loads lazily inside the hook call (memoized per process);
`tools.Init()` runs only when a tool-value hook fires.

## Key files

| File | Purpose |
|------|---------|
| `main.go` | `usageTemplate`, `run()`, and `commands()` — the composition root wiring deps into each domain package's `Command()` |
| `internal/` (root package) | `internal.Command` adapter, flag primitives + shared groups, `PrepTheme`, completion data loaders + hooks |
| `internal/<domain>/cmd.go` | each command's definition (help text, flag groups, setup/run) |
| `internal/text/setup_querier.go` | `text.SetupQuerier`: config load, cascades, glob/tool/skill/lookback setup |
| `internal/setup/config_lifecycle.go` | `setup.ConfigRunPrep`, united config migration |
| `internal/<domain>/create_querier.go` | each domain's model → vendor routing factory |
| `internal/<domain>/generic/` | the domain's vendor-shared surface (types, parsing, saving) — below the vendors |
| `go_away_boilerplate/pkg/cmd` | dispatcher, scanner, `Subcommander`, completion engine |
