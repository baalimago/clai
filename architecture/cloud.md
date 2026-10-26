# Cloud sync: `clai cloud`

clai can keep two stores in sync with clai.chat: a free **config store**
(profiles, MCP servers, shell contexts, skills, mode configs, theme) and a
$1/month **conversation store** (conversations, their images, reasoning
sidecars, and the conversations clai imports from other tools). This doc
answers: how a machine logs in, which files sync, how a sync avoids
overwriting another machine's work, and when a sync runs.

Read this when: changing `internal/cloud`, adding a file to the config dir
that should (or must not) sync, changing the conversation file layout, or
touching the foreign readers' export. The server half is
`cloud/site/{api,sync,devices,billing}.ts` in baalimago/clai-cloud (its
`architecture/sync.md`).

## Key files

| File | Purpose |
|---|---|
| `internal/cloud/cmd.go` | `Command`: `login`, `logout`, `status`, `sync [-background]`, `enable`, `disable` |
| `internal/cloud/engine.go` | `Engine.Sync`: pull, apply, conflicts, plan, upload, commit |
| `internal/cloud/client.go` | `Client`: the `/api/v1` calls; `APIError`, `TransportError` |
| `internal/cloud/paths.go` | `Allowed`: the allowlists, mirrored from the server's `validPath` |
| `internal/cloud/scan.go` | `scan`: walks a store's dirs and hashes files, skipping unchanged ones by size and mod time; `Quotas` |
| `internal/cloud/secrets.go` | `holdsSecret`: the credential check that keeps a config file local |
| `internal/cloud/settings.go`, `state.go` | `<config>/cloud/`: `credentials.json` (0600), `settings.json`, `state/<store>.json` |
| `internal/cloud/lock.go`, `background.go` | The sync lock and dirty marker, `AutoSync`, `SpawnSelf` |
| `internal/chat/imported.go` | `ExportForeign`, `importedReader`, `ReindexPulled` |
| `internal/cli/cli.go` | Wires the command, the hooks, and the post-command `AutoSync` |

## Flow

```text
clai cloud login
  POST /api/v1/device/code {name: hostname} → print verification_uri_complete + user code
  every interval: POST /api/v1/device/token → pending | slow_down (+5 s) | denied | expired | token
  save <config>/cloud/credentials.json (0600), reset state, sync

Engine.Sync(store)                                   (per store, config first)
  conversations only: Hooks.Export → chat.ExportForeign
  round ≤ 3:
    pull:  GET changes?since=cursor (pages) → apply each entry → save state per page
    push:  scan → plan (changed / deleted vs. state) → POST blobs/missing → PUT blobs
           → POST commit (≤ 200 per call) → state ← applied revs
    a conflict in the commit answer → next round
  conversations only: Hooks.Pulled → chat.ReindexPulled (chat index upsert/remove)

any other command exits 0 → cli.Run → cloud.AutoSync
  logged in, CLAI_CLOUD not off, a store enabled, last sync older than the interval
  → touch last-sync → SpawnSelf: `clai cloud sync -background`, detached, output → <cache>/cloud/sync.log
```

## Behavior

- **What syncs.** `Allowed` is the only gate, and it must match the
  server's `validPath`: a path the server refuses fails the whole commit.
  Config: `textConfig.json`, `photoConfig.json`, `videoConfig.json`,
  `theme.json`, `skills.json`, `profiles/*.json`, `mcpServers/*.json`,
  `shellContexts/*.json`, `skills/**`. Conversations:
  `conversations/<id>.json` (not `globalScope.json` or `prevQuery.json`),
  `conversations/assets/<sha256>.<ext>`, `conversations/reasoning/<id>/*.json`,
  `conversations/imported/<source>/<id>.json`. Never: vendor model files,
  `skills_trust.json`, the MCP token store, `chat_index.cache`, `dirs/`,
  `cloud/`, and any path segment starting with a dot.
- **State.** `state/<store>.json` keeps the cursor and, per path, the
  revision and hash this machine last agreed on with the server. A file is
  changed locally when its hash differs from that; a commit sends it with
  `base_rev` = that revision. An entry whose revision is not newer than the
  state's is this machine's own commit coming back, and is skipped.
- **Applying a remote entry.** Same bytes: record the revision. Missing
  here, or unchanged here since the last sync: download (the blob is
  re-hashed; a mismatch is a `ChecksumError`). Deleted remotely: delete
  only an unchanged local file; an edit made here survives and the push
  recreates it. A file deleted here but changed elsewhere comes back.
- **Conflicts** (both sides changed, or a first sync meets a file that
  differs):
  - A conversation where one copy only extends the other keeps the longer
    one; equal messages keep the server's unless only ours has a title.
  - A conversation that diverged is kept twice: the server's copy takes
    the path, and ours is written as a new chat (`NewChatID` shape) with
    `source: "clai"` and `source_id` set to the original id, the fork
    marker clai already uses.
  - Anything else takes the server's copy; ours goes to
    `<config>/cloud/conflicts/<path>.<UTC timestamp>`.
- **Credentials stay local.** In the config store, a file with a value
  shaped like a key (`sk-…`, `ghp_…`, `AKIA…`, a PEM private key), or a
  JSON field named like one (`*_TOKEN`, `api_key`, `password`,
  `Authorization`, …) holding a literal value, is skipped and reported.
  `$VAR`, `env:NAME` and URLs are references, not secrets. The check errs
  toward skipping.
- **Size.** A file over the store's per-file limit (1 MB config, 25 MB
  conversations) is skipped and reported, never sent.
- **Imported conversations.** `ExportForeign` runs before the conversation
  store scans. It writes each conversation the foreign readers discover,
  that no native clone already holds, to
  `conversations/imported/<source>/<id>.json`; images go to the shared
  `conversations/assets/`, and the asset sweep counts the exports' refs.
  `imported/.exported.json` records each export's message count, so an
  unchanged conversation is not read again. On another machine,
  `importedReader` (last in the reader list) lists the exports under their
  original source, so where the tool itself is installed its own reader
  wins the dedup. `chatListRow.Reader` routes a selected export to it.
- **Background sync.** `AutoSync` never blocks and never fails the
  command. It skips the `cloud`, `version`, `confdir`, help and completion
  commands, a machine with no login, `CLAI_CLOUD=off`, and anything within
  `min-interval-seconds` (default 60) of the last start. The spawned sync
  takes `<cache>/cloud/sync.lock`; a sync that finds it held leaves
  `sync.dirty` and exits, and the holder goes round again (at most three
  times). A lock older than ten minutes is taken over. The log is
  truncated past 1 MB.
- **Login and logout.** A login resets every store's state, so a machine
  that switches accounts starts from the server. Logout revokes the token
  (a 401 counts as done), deletes the credentials and the state, and
  leaves every local file. `CLAI_CLOUD_TOKEN` and `CLAI_CLOUD_URL` stand
  in for the credentials file, for CI.
- **Subscription.** A conversation store without a live subscription
  answers 402; the report prints the server's message and the other store
  still syncs. A lapsed subscription is readable but not writable.

## Gotchas

- `Allowed` and the server's `validPath` are two copies of one list. Change
  both, in both repos, in the same release.
- `settings.json` replaces the default `stores` map instead of merging into
  it, so `{"stores": {}}` turns every store off.
- The hash cache in the state file trusts size and mod time; a tool that
  rewrites a file with the same size within one mod-time tick is missed
  until its next change.
- `ExportForeign` keeps going past a conversation it cannot read and
  returns the joined errors; `Engine.Sync` reports that as a skip, not a
  failure.

## Related

- [config.md](./config.md), [chat.md](./chat.md),
  [continue-from-claudex.md](./continue-from-claudex.md),
  [images.md](./images.md), [cmd-dispatch.md](./cmd-dispatch.md)
