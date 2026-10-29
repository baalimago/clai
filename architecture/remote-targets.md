# Remote profile targets

`-prp` takes a remote target as well as a local file. The profile is fetched
over https, checked strictly, trusted once per content hash, and cached, so a
scheduled run reads its bytes from disk and makes no network call.

```text
cchub:<handle>/<agent>[@<version>]      an agent on cchub, the clai hub
gh:<owner>/<repo>[/<path>][@<ref>]    agent.json (or <path>) in a GitHub repo
https://<host>/<path>                 any URL that answers application/json
…#sha256=<hex>                        on any of them: consent to exactly these bytes
```

A link to a file on GitHub reads as its `gh:` target, so a link copied from
the browser works: `https://github.com/<owner>/<repo>/blob/<ref>/<path>` (or
`/raw/`) and `https://raw.githubusercontent.com/<owner>/<repo>/<ref>/<path>`
become `gh:<owner>/<repo>/<path>@<ref>`, and a `HEAD` ref drops the `@`.
Fetched as plain https, the first answers GitHub's page data as JSON and the
second text/plain. The ref is one segment, or `refs/heads/<branch>` /
`refs/tags/<tag>`; a branch with a `/` in it needs the `gh:` form.

`-p <name>` and `-prp ./file.json` are unchanged. They work offline, need no
account, and fetch nothing. `IsRemote` matches only the `cchub:`, `gh:`,
`https://` and `http://` prefixes. Plain `http://` is refused rather than
fetched. A `.` or `..` is refused as a GitHub owner or repo,
as a segment of a `gh:` path or ref, and as a segment of an `https://`
path: the server would resolve it, so the target would read as one
address and fetch another (`gh:anthropics/..@evil`).

## Files to read

| File | Purpose |
| --- | --- |
| `internal/remote/target.go` | `ParseTarget`, `Target` (`Base`, `String`, `Pinned`, `Unpinned`, `WithDigest`, `FetchURL`, `PageURL`, `DefaultName`) |
| `internal/remote/resolve.go` | `Resolver.Resolve`: cache or fetch, validate, hash check, trust, store. `Consent`, `Prompter`, and the refusal errors with `ExitCode() == ExitRefused` (3) |
| `internal/remote/validate.go` | `Validate`: the strict check, `ProfileKeys`, `InvalidProfileError` |
| `internal/remote/fetch.go` | `NewClient`/`CheckRedirect` (https-only redirects, at most 5), the 64 KiB cap, the content-type check |
| `internal/remote/store.go` | `TrustStore` (`<config>/trust.json`), `Cache` (`<cache>/remote/<sha256>.json` plus `index.json`), `Digest` |
| `internal/remote/skill.go` | `ParseSkillTarget`, `Resolver.ResolveSkill`/`ResolveSkills`, `ResolvedSkill`, `SkillSummary`, `InvalidSkillError`, `ProfileSkills` |
| `internal/remote/models.go` | `ParseModelListTarget`, `ValidateModelList`, `Resolver.ResolveModelList`, `ResolveModelTarget`, `InvalidModelListError` |
| `internal/remote/summary.go` | `Summarize`: what the trust prompt and `profiles trust` show |
| `internal/remote/env.go` | `CLAI_CLOUD_URL`, `CLAI_CLOUD=off`, `NewResolver` |
| `internal/text/remote_profile.go` | `resolveRemoteProfilePath` (runs before `ProfileOverrides`), the terminal prompt, `terminalAvailable` |
| `internal/profiles/remote.go` | `profiles trust`, `install`, `update`; the `source` stamp |
| `internal/exitstatus.go` | `ExitCoder`/`ExitStatus`: how exit status 3 survives upstream `cmd.Run` |
| `internal/cli/cli.go` | `Deps.RemoteClient`, `remoteResolver` (the composition root) |
| `e2e/remote_targets_e2e_test.go` | The CLI contract against `fakeHub`, a local TLS server with clai.chat's raw routes that also stands in for GitHub and any https host |
| `e2e/profile_skills_e2e_test.go` | A profile's skills from the hub and GitHub: install, offline run, refusals, and publishing a skill then an agent that uses it |
| `e2e/remote_targets_pty_e2e_test.go` | The trust question on a real pseudo-terminal, with stdout piped |

## Flow

```text
text.SetupQuerier
  → resolveRemoteProfilePath (only when IsRemote(-prp))
    → Resolver.Resolve(target, ConsentAsk)
      → cache hit for a pinned target, or for any target under CLAI_CLOUD=off
        otherwise fetch FetchURL(hub base)
      → Validate → Summarize → Digest
      → #sha256= present: must match, and counts as consent
      → trusted (base, digest) in trust.json? run
        else terminal? ask (summary + page link), record a yes
        else UntrustedError (exit 3)
      → Cache.Store(target, body)
    → ProfilePath = <cache>/remote/<sha256>.json
  → ProfileOverrides reads it like any profile, read-only
    (utils.ReadConfigFile: defaults filled in memory, nothing written)
```

A hub target is read from the hub's raw routes:
`<base>/hub/<handle>/<agent>/v/<n>/raw` when pinned, and `/raw` otherwise. The
base is `CLAI_CLOUD_URL`, an https origin that defaults to `https://www.clai.chat`.
A `gh:` target reads
`raw.githubusercontent.com/<owner>/<repo>/<ref or HEAD>/<path or agent.json>`.

## Behavior

- **Pinned means cached.** A hub version or a GitHub ref names fixed bytes.
  Once one is cached, it is read from `<cache>/remote/` with no fetch. An
  unpinned target is fetched on every run. When its bytes change, the new
  hash is untrusted, so the run asks again, or exits 3 where nobody can be
  asked.
- **Trust is per base and hash.** `trust.json` maps a target's `Base()`
  (without the version, the ref or the digest) to the hashes the user
  trusted. Pinning a version whose bytes are already trusted does not ask
  again. `trust.json` lives in the config dir and is never synced, because
  trust is a decision about one machine.
- **Who can be asked.** `terminalAvailable` requires two things. Stdin must
  be a character device other than `/dev/null`, and the answer terminal
  (`TTY`, default `/dev/tty`) must open and must not be `/dev/null`.
  `-ni` turns the prompt off. Cron, CI, pipes and the test binaries never
  qualify, so they get exit 3 with the commands that would let the target
  run: read it, trust it, or pin it with its hash. The question and the
  unpinned-`gh:` notice go to stderr, so `clai -prp … q … | jq` still asks
  and its stdout holds only the reply.
- **The hash is consent.** `#sha256=<hex>` lets a fresh container or a CI
  job run a target nobody trusted there before. The bytes must match the
  hash, and the match is recorded as trust.
- **Strict validation, for remote profiles only.** The body must be at
  most 64 KiB and hold exactly one JSON object.
  - A key within two edits of a profile key is an error that suggests the
    key (`"promt"` → `"prompt"`). Any other unknown key is ignored, as a
    local profile's would be, and listed in the trust prompt's
    `not read by clai` line. `description`, the hub's own field
    (`hubKeys`), is expected and never listed.
  - Every field `Summarize` reads has the shape it decodes (`model`,
    `prompt` and each server's `command`, `url` and `envfile` strings,
    `use_tools` a boolean, `tools` and `args` arrays of strings, each
    server's `env` an object of strings), so a body `Validate` accepts can
    always be summarised.
  - Each MCP server needs `command` or `url`.
  - A relative `envfile` is refused: the fetched profile has no directory
    for it to be relative to. Use `~/`, `$VAR/` or an absolute path.
  - `auth`, when present, must be an object. Its `required_env` must be an
    array of valid variable names (`serverconfig.ValidEnvName`), each listed
    once and none also a key of that server's `env`, which would shadow the
    variable the profile says it needs. `token_env` must be a valid name,
    `token_command` a non-empty array of strings and `scopes` an array of
    strings. An unknown auth key within two edits of one of these is an
    error that suggests it; any other is listed as ignored. A malformed
    entry is reported by field or index (`required_env[2]`), never echoed:
    it may be a pasted secret, and the names are printed in the trust
    prompt, so they must not carry newlines or escape sequences. Local
    config is lenient about duplicates and `env` overlap.

  The trust prompt's `needs` section names each variable a server reads
  from the environment, with its server: `auth.required_env`, plus
  `auth.token_env` when no `auth.token_command` takes precedence over it
  (`Summary.Needs`).
- **The trust prompt.** `text.FormatRemoteTrustPrompt` draws it in clai's
  shared look (`internal/look`, the same `▸ head · detail` sections and
  aligned rows `clai cloud` prints): the target with its sha256 and hub
  page, then `Summary.Lines`, everything the profile makes this machine do
  or the model read, then the question on its own line. On a terminal
  without `NO_COLOR` the markers, details and question are in the theme's
  colours (`look.For`); the profile's own strings are never painted.
  `Summary.String` is the same without colour, as `clai profiles trust`
  prints it.

  ```text
  ▸ cchub:ada/notes@1 · fetched · runs on your machine
    sha256   6934c528…
    page     https://www.clai.chat/hub/ada/notes/

  ▸ MCP servers · 2
    memory   npx -y @modelcontextprotocol/server-memory@2.0.0
             env MEMORY_FILE_PATH=~/.clai/memory.json
    vault    https://mcp.example.com
             runs here for a token: vault print token
  ▸ tools · date, mcp_memory*
  ▸ never runs · git push, rm
  ▸ needs · VAULT_ADDR for vault
  ▸ model · gpt-6.1-sol
  ▸ prompt · 1213 characters
    │ You keep the team's notes.
    │ … 812 more characters not shown

  ? Trust cchub:ada/notes@1? [y/N]
  ```

  - Under each server: its `auth.token_command` (`credential.go` runs it
    with `exec`) and every `env` entry as `KEY=value`. An argument that
    holds a space or a quote is quoted. A package runner fetching an
    unversioned package is marked `! not pinned`, and so is an unpinned
    skill target under `skills`.
  - Tools (`Summary.Access`, mirroring `ProfileOverrides`): a fetched or
    installed profile (`ProfileFetched`, or a `source` stamp) gets only the
    tools it names. `use_tools` with no `tools` list and no servers of its
    own gets no tools, since every local tool and MCP server would be one
    prompt injection away; the prompt says `tools · none · no MCP
    servers`. A `tools` list is shown as written; a profile with servers
    is narrowed to their tools plus that list.
  - Bans: the profile's `cmd-ban` is shown under its tools as `never
    runs` (`Summary.CmdBan`), since `cmd` and `async_cmd` refuse a command
    line that contains an entry before it spawns. A profile with no tools
    has nothing to ban, so the line is left out.
  - Profile: a `-prp` run's conversation is stamped with the target it
    ran as (pin fragment left out), so `clai chat list` names the hub
    profile, and a reply through `chat continue` fetches it again with
    `-prp`.
  - History: a fetched or installed profile's `save-reply-as-conv` is
    ignored (`ProfileOverrides`), so its runs are saved and listed by
    `clai chat list` unless your own `textConfig.json` turns saving off.
    The run is the record of what the agent did on this machine.
  - Shell context: a fetched or installed profile's `shell_context` is
    ignored too (`ProfileOverrides`). A shell context runs commands from
    this machine's own definitions and puts their output in the prompt;
    which, and when, is the user's choice, so `-asc` or your own profile
    sets it. `clai profiles publish` refuses a profile that names one, and
    the Hub refuses it on publish.
  - `prompt` shows the first `PreviewLines` (6) lines and at most
    `PreviewChars` (400) characters of the prompt behind a `│` bar, so it
    never reads as clai's own output, and says how many characters it
    left out. A skill's description is shown the same way
    (`SkillSummary.Lines`).
  - Every string from the fetched bytes (names, command lines, env, model,
    ignored keys, skill targets, prompt, skill name and description, and
    the target and page in `FormatRemoteTrustPrompt`) goes through
    `remote.Printable`: C0 and C1 controls, DEL and bidi formatting
    characters become visible escapes (`\x1b`, `\u202e`), invalid UTF-8
    becomes U+FFFD. Only the prompt preview keeps line breaks, as separate
    quoted lines.

  `cchub:` and `https://` responses must be `application/json` (or
  `+json`). `raw.githubusercontent.com` serves `text/plain`, so `gh:`
  relies on `Validate` alone. Local profiles keep the lenient loader.
- **Offline.** `CLAI_CLOUD=off` makes every target cache-only. An
  uncached target is an `OfflineError` (exit 3).
- **Exit status 3.** Upstream `cmd.Run` collapses every failure to 1.
  `internal.Command` records an error implementing `ExitCoder` in the
  `ExitStatus` that `cli.Run` put on the context, and `ExitStatus.Resolve`
  swaps the 1 for that code.
- **Profiles subcommands are consent.** They call `Resolve` with
  `ConsentGiven`, so they record trust without prompting.
  - `profiles trust <target>` prints the summary and records the hash.
  - `profiles install <target> [name]` writes
    `<config>/profiles/<name>.json` with a `source` stamp,
    `{"target": "<target>#sha256=<hex>"}`. It refuses to overwrite a
    profile that came from elsewhere.
  - `profiles update [name]` re-resolves each stamped profile's
    `Unpinned()` target (a hub agent's newest version) and rewrites the
    profiles that changed. Naming the profile is consent to its newest
    version; with no name, each version nobody trusted yet shows the trust
    prompt first (`CommandDeps.NewPrompt`), and with no terminal it is
    left as an `UntrustedError`.
  - A fetched or installed profile gets only the tools it names (see the
    trust prompt's Tools above).
  - `profiles list` shows the stamp. `text.Profile.Source` reads it, and the
    hub's agent page shows it as "based on".

## Skills

A profile's `"skills"` names skill targets (`ParseSkillTarget`): a
`cchub:` skill, a `gh:` `SKILL.md` (a path that is not a `.md` file names
its folder; no path means the repo's root `SKILL.md`) or an `https://`
URL. Almost every skills marketplace (skills.sh, SkillsMP, Anthropic's
`anthropics/skills`) lists skills that live in GitHub repos, so `gh:`
reaches them.

- `Resolver.ResolveSkill` reads the target like a profile (`read` with
  `skillDoc`: `Accept: text/markdown`, no JSON requirement), caps it at
  64 KiB, checks it with `skills.CheckSkill` (an `InvalidSkillError`,
  exit 3, when it has no description), checks `#sha256=`, authorizes it
  per base and hash in the same `trust.json`, stores it in the cache and
  lays it out as `<cache>/remote/skills/<sha256>/<name>/SKILL.md`
  (`Cache.skillRoot`). The name is the frontmatter `name`, or the hub name,
  or the folder that holds the file. The trust prompt shows
  `SkillSummary`: the name and description.
- `Validate` checks `"skills"` is a list of skill targets, and `Summarize`
  lists them (`Summary.Skills`, marked "not pinned" when they can change).
- In a run, `text.remoteRun` resolves the profile and then its skills. A
  yes at the profile's prompt in this run is consent for its skills
  (`ConsentGiven`); otherwise each untrusted skill asks, or exits 3 where
  nobody can be asked. A `#sha256=` on the profile pins the profile's
  bytes, which name the skills but do not hold them, so it is not consent
  for an unpinned skill.
- `profiles trust`, `install` and `update` resolve the skills with
  `ConsentGiven` too (`CommandDeps.trustSkills`), so an installed profile
  runs offline and in cron with its skills cached and trusted.
- `profiles publish` refuses a skill that is not pinned by version, ref or
  hash (`pinnedSkills`): whoever trusts a published version trusts the
  bytes of its skills too.

## Gotchas

- `remote.ProfileKeys` must list every JSON key of `text.Profile`.
  `TestRemoteProfileKeys_matchProfile` fails when they drift.
- Untrusted bytes are never cached. Only a resolve that passed trust calls
  `Cache.Store`, so a cache hit never skips the trust check. Trust is checked
  again on every resolve anyway.
- A cached profile is named by its hash, so nothing may write to it.
  Profile loading is read-only (`utils.ReadConfigFile`, see
  [profiles.md](profiles.md)); the loader it replaced rewrote a profile to
  add missing fields and created config dirs beside it, which broke the
  hash and filled `<cache>/remote/`. The e2e suite checks the cache holds
  only hashes and `index.json` after runs that are not `-r`; a profile with
  skills adds `skills/`.
- A missing agent or version is a `FetchError` naming the target ("not
  found", exit 1): it is a failed run, not a refusal.
- Remote fetches always go through `Deps.RemoteClient`. Tests point it at
  an `httptest.NewTLSServer` client and set `CLAI_CLOUD_URL` to the server's
  URL; the e2e client also reroutes `raw.githubusercontent.com` there. The
  PTY test's child has no injected client and trusts the server through
  `SSL_CERT_FILE`. No test reaches the network. `fakeHub` mirrors the hub's
  routes by hand: a change to them in clai-cloud's `cloud/site/app.ts`
  changes it too.
- The hub's machine API (`GET /api/v1/hub/<handle>/<agent>`) does not exist
  yet. When it does, `FetchURL` can move to it, keeping the cache and trust
  layout.
