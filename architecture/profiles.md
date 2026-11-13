# Profiles Command Architecture

Command: the `config` tree — `clai config list profile`, `clai config
validate profile`, `clai config schema profile`, `clai config trust profile`,
`clai config install profile`, `clai config update profile` and `clai config
publish profile` — plus the hidden `clai profiles
[list|validate|schema|trust|install|update|publish]` spellings, which stay
callable and re-enter the config verb they document.

`clai config list profile` prints one line per profile file under the clai
config directory, keeping the model, tools, prompt and source stamp. The
hidden `profiles` spelling still installs, updates and trusts remote profile
targets (`cchub:`, `gh:`, `https://`); [remote-targets.md](./remote-targets.md)
covers those. `config publish profile` sends a local profile to the Hub
(below).

Profiles themselves are used primarily via the `-p/-profile` and `-prp/-profile-path` flags on text-like commands (`query`, `chat`, `cmd`). Configuration semantics are described in `CONFIG.md`.

A profile run with no prompt and nothing on stdin sends `text.ProfileDefaultPrompt` as the user message instead of failing with `ErrNoPrompt`, so an agent like `clai -prp cchub:clai/reviewer q` acts on its own defaults. A plain query without a prompt is still an error.

Loading a profile (`-p` or `-prp`) is read-only (`utils.ReadConfigFile`): fields missing from the file are filled from `DefaultProfile` in memory, the file is never rewritten, nothing is created beside it, and a missing profile file is an error.

A profile may also pin the model the media tools its runs call use: `"audio": {"model": "gpt-4o-transcribe-diarize"}` sets the model its `audio_transcribe` calls run (`text.Configurations.AudioModel`), and `-am` still overrides it. The value may be an `audioConfig.json` alias verb or a remote model list, which the tool falls back through ([model-aliases.md](./model-aliases.md)); the checker routes it through the audio alias table. The audio key is read only for a text-querier run; a local `clai audio transcribe` reads `audioConfig.json` and its own `-am`. `"photo"` stays out: the image model follows `-pm` and `photoConfig.json` (see [audio.md](./audio.md), [images.md](./images.md)).

## Entry Flow

```text
internal/cli:Run()
  → cmd.Run(...)                  # go_away_boilerplate/pkg/cmd dispatch
    → `profiles` hidden alias re-enters dispatch (internal/cli/config.go)
      → config list profile       # internal/cli/config.go
```

## Key Files

| File | Purpose |
|------|---------|
| `internal/profiles/cmd.go` | profiles command + `list`, `trust`, `install`, `update`, `publish` subs (see [cmd-dispatch.md](./cmd-dispatch.md)) |
| `internal/profiles/publish.go` | `CommandDeps.Publish`, `send`, `newPublishCommand`; `PublishRequest`, `Published`, `HubPublisher`, `HandleRequiredError` |
| `internal/profiles/skills.go` | `clai skills` (`SkillsCommand`, `SkillsHelp`) and `CommandDeps.PublishSkill` |
| `internal/cli/cli.go` | `hubPublisher`: posts to `/api/v1/hub` with the `clai cloud login` key; the secret check is `sync.FindSecret` |
| `internal/profiles/remote.go` | `CommandDeps.Trust`, `Install`, `Update` and the `source` stamp |
| `internal/profiles/cmd.go` | Implements `clai profiles` command |
| `internal/cli/config.go` | `listProfiles`, `printProfileLine` and `reportUnreadableProfile`: the one listing `config list profile` and the hidden `profiles` spellings share |
| `internal/profiles/validate.go` | `config validate profile` (findings, `-json`, exit 0/1/2) and `config schema profile`; `CommandDeps.Validate` is the one checker, and an unreadable file target exits 2 |
| `internal/utils/config_dir.go` (or similar) | `GetClaiConfigDir()` |
| `internal/utils/json.go` (or similar) | `ReadAndUnmarshal()` helper |

## Behavior

Implemented in `internal/profiles/cmd.go`.

### Supported subcommands

- `clai config list profile`
- `clai config validate profile <target> [-json]`
- `clai config schema profile`
- `clai config trust profile <target>`
- `clai config install profile <target> [name]`
- `clai config update profile [name]`
- `clai config publish profile <profile> [-name] [-title] [-summary] [-note] [-handle]`

Any other subcommand returns an error.

The pre-existing spellings — `clai profiles`, `clai profiles list`, `clai
profiles validate <target>`, `clai profiles schema`, `clai profiles trust
<target>`, `clai profiles install <target> [name]`, `clai profiles update
[name]` and `clai profiles publish <profile>` — stay callable and hidden:
bare `profiles` rebuilds `config list profile`, and each subcommand
re-enters its `config` verb (see
[cmd-dispatch.md](./cmd-dispatch.md#compatibility-hidden-spellings)).

### Publishing

`publish` reads `<config>/profiles/<profile>.json`, or the path given when
it holds a separator or ends in `.json`, and sends the bytes as they are on
disk to clai.chat's `POST /api/v1/hub` as the account of the
`clai cloud login` key (`CLAI_CLOUD_TOKEN` and `CLAI_CLOUD_URL` stand in, as
for sync). Before anything leaves the machine it runs `profile.Validate`
with the `Publish` route, the same checker a run uses, and `sync.FindSecret`;
a failure of either sends nothing. Publish has no tool registry, so the
checker judges no tool name there. The name defaults to the file's base name
and must be a Hub name. The first publish of a name creates the agent; the
same name again is its next version, with `-note` as the changelog. An
account without a handle gets `HandleRequiredError` with the suggested
handle, printed as the command to rerun with `-handle`. Output is the
address and version, the page URL, and the `-prp` line pinned by the
returned sha256. Flags may follow the profile: `publishCommand` parses
what comes after it again. The profiles command's config prep may have
added default fields to the file first, so the published bytes, and their
hash, are the upgraded file. The checker refuses a profile whose `"skills"` holds a
target that can change (no version, ref or hash), and one that names a
`shell_context`, which is the runner's own.

`clai skills publish <skill>` sends a skill the same way, with `kind:
"skill"`: `<config>/skills/<skill>/SKILL.md`, or a folder holding one, or
the `SKILL.md` path. It must pass `skills.CheckSkill` and `FindSecret`;
only `SKILL.md` is sent. The name is `-name`, else the frontmatter `name`
when it is a Hub name, else the folder's name. Output ends with the
`"skills"` line to paste into a profile, pinned by the returned hash.
`clai skills` with no subcommand prints its help, which explains skills and
the targets a profile's `"skills"` takes.

### Listing logic

`listProfiles()` in `internal/cli/config.go`, reached as `clai config list
profile` and as the hidden `profiles` and `profiles list`:

1. Resolve the config dir (`utils.GetClaiConfigDir()`), then
   `<configDir>/profiles`.
2. Without that directory, warn `no profiles directory found at <dir>` on
   stderr (subsystem `profiles`) and list nothing.
3. With no `*.json` file in it, warn `no profiles found in <dir>` and list
   nothing.
4. For each file, read it and run the profile checker (`profile.Validate`,
   `file` route). A file that cannot be read, or that the checker refuses,
   keeps one line in the listing that says `cannot read` with the reason and
   the `clai profiles validate <path>` command, and is warned about on stderr
   too, so the listing and the diagnosis agree.
5. A file the checker accepts prints one line: `profile <path>: name=<name>
   model=<model> tools=[…] prompt="<first sentence>"`, then
   ` source=<target>` when the profile carries a source stamp. An empty
   `name` comes from the file name.

## Developer notes

- `config list profile` reads every file and judges it with the same checker
  a run uses, so a refused profile is named instead of listed as healthy.
  `clai config validate profile <target>` is the
  command that does, printing every finding of the one checker in
  `internal/profile`; `-json` prints the findings array alone. It exits 0
  with no error finding on any route, 1 with one, and 2 on a usage error —
  including a target it cannot read, which is no finding but no answer
  either. It never runs the profile. A remote target is read through
  `remote.Resolver.Peek`, which records no trust.
- The checker judges a `tools` entry against the local tool registry, which
  holds the built-ins and the executables found on `PATH`: exact names and
  globs alike, as clause 6 says. An entry that names the MCP namespace
  (`mcp_…`, `tools.SelectsMCPTools`) is not judged. Such a name exists only
  while an MCP server runs, so no offline registry can hold it, and a run
  selects it without a lookup ([config.md](./config.md), tool usage). A
  profile may get those servers from its own `mcp_servers`, from the config
  dir's `mcpServers` directory, or from neither: the exemption does not
  depend on knowing the server. A typo outside the namespace, `filetree` for
  `file_tree`, is still refused on the file and remote routes.
- Creation/editing of profiles is done via `clai setup` (stage `2`).
