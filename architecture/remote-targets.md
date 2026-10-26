# Remote profile targets

`-prp` takes a remote target as well as a local file. The profile is fetched
over https, checked strictly, trusted once per content hash, and cached, so a
scheduled run reads its bytes from disk and makes no network call.

```text
hub:<handle>/<agent>[@<version>]      an agent on the clai hub
gh:<owner>/<repo>[/<path>][@<ref>]    agent.json (or <path>) in a GitHub repo
https://<host>/<path>                 any URL that answers application/json
…#sha256=<hex>                        on any of them: consent to exactly these bytes
```

`-p <name>` and `-prp ./file.json` are unchanged. They work offline, need no
account, and fetch nothing. `IsRemote` matches only the `hub:`, `gh:`,
`https://` and `http://` prefixes. Plain `http://` is refused rather than
fetched.

## Files to read

| File | Purpose |
| --- | --- |
| `internal/remote/target.go` | `ParseTarget`, `Target` (`Base`, `String`, `Pinned`, `Unpinned`, `WithDigest`, `FetchURL`, `PageURL`, `DefaultName`) |
| `internal/remote/resolve.go` | `Resolver.Resolve`: cache or fetch, validate, hash check, trust, store. `Consent`, `Prompter`, and the refusal errors with `ExitCode() == ExitRefused` (3) |
| `internal/remote/validate.go` | `Validate`: the strict check, `ProfileKeys`, `InvalidProfileError` |
| `internal/remote/fetch.go` | `NewClient`/`CheckRedirect` (https-only redirects, at most 5), the 64 KiB cap, the content-type check |
| `internal/remote/store.go` | `TrustStore` (`<config>/trust.json`), `Cache` (`<cache>/remote/<sha256>.json` plus `index.json`), `Digest` |
| `internal/remote/summary.go` | `Summarize`: what the trust prompt and `profiles trust` show |
| `internal/remote/env.go` | `CLAI_CLOUD_URL`, `CLAI_CLOUD=off`, `NewResolver` |
| `internal/text/remote_profile.go` | `resolveRemoteProfilePath` (runs before `ProfileOverrides`), the terminal prompt, `terminalAvailable` |
| `internal/profiles/remote.go` | `profiles trust`, `install`, `update`; the `source` stamp |
| `internal/exitstatus.go` | `ExitCoder`/`ExitStatus`: how exit status 3 survives upstream `cmd.Run` |
| `internal/cli/cli.go` | `Deps.RemoteClient`, `remoteResolver` (the composition root) |
| `e2e/remote_targets_e2e_test.go` | The CLI contract against `fakeHub`, a local TLS server with clai.chat's raw routes that also stands in for GitHub and any https host |
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
base is `CLAI_CLOUD_URL`, an https origin that defaults to `https://clai.chat`.
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
    key (`"promt"` → `"prompt"`). Any other unknown key is listed and
    ignored, as a local profile's would be.
  - Each MCP server needs `command` or `url`.
  - A relative `envfile` is refused: the fetched profile has no directory
    for it to be relative to. Use `~/`, `$VAR/` or an absolute path.

  `hub:` and `https://` responses must be `application/json` (or
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
    profiles that changed.
  - `profiles list` shows the stamp. `text.Profile.Source` reads it, and the
    hub's agent page shows it as "based on".

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
  only hashes and `index.json` after runs that are not `-r`.
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
