# Cloud sync: `clai cloud`

clai can keep two stores on clai.chat: a **config store** (profiles, MCP
servers, shell contexts, skills, mode configs, theme) and a **conversation
store** (conversations, their images, reasoning sidecars, and, when asked,
the conversations clai imports from other tools). Config sync is free (up
to 1 GB); conversations cost $1 per started GB a month and need a card on
file. Sync works like git: nothing
runs on its own, only `clai cloud` subcommands talk to clai.chat, and clai
works the same without an account. This doc answers: how a machine logs in,
which files sync, how pull and commit avoid overwriting another machine's
work, and what a conflict leaves on disk.

Read this when: changing `internal/cloud`, adding a file to the config dir
that should (or must not) sync, changing the conversation file layout, or
touching the foreign readers' export. The server half is in
baalimago/clai-cloud (`architecture/sync.md`, and `architecture/cli-login.md`
for the login).

## Key files

| File | Purpose |
|---|---|
| `internal/cloud/client.go` | `Client`: `Do`, `PostJSON`; `APIError` (with `URL` for a page that fixes it, and `Suggested`, the handle the Hub offers with `handle_required`), `TransportError`; `StartDevice`, `PollDevice`, `Me`, `RevokeCurrent` |
| `internal/cloud/login.go` | `LoginFlow.Run`: start, show the code, poll until approved, denied or expired |
| `internal/cloud/credentials.go` | `<config>/cloud/credentials.json` (0600); `CLAI_CLOUD_TOKEN`, `CLAI_CLOUD_URL` |
| `internal/cloud/sync/paths.go` | `Store`, `Allowed` (mirrors the server's `validPath`), `StoreOf` |
| `internal/cloud/sync/scan.go` | `scan`: hashes a store's files, reusing hashes by size and mod time; `FileLimits` |
| `internal/cloud/sync/secrets.go` | `FindSecret`: the credential check; vectors in `testdata/secrets.vectors.json`, shared with clai-cloud |
| `internal/cloud/sync/state.go` | `<config>/cloud/state/<store>.json`, `base/`, `theirs/`, `trash/`; `ResetState` |
| `internal/cloud/sync/plan.go` | `Engine.Plan`: what a commit would send, leave out, or is blocked by |
| `internal/cloud/sync/pull.go` | `Engine.Pull`, the first pull's question, `Mode` |
| `internal/cloud/sync/commit.go` | `Engine.Commit`, `BehindError`, `ConflictsOpenError` |
| `internal/cloud/sync/progress.go` | `Progress` and `Phase` (reported through `Engine.Progress`), `uploadMissing` (parallel uploads), `checkBilling`, `BillingBlockedError` |
| `internal/cloud/sync/resolve.go`, `diff.go`, `status.go`, `merge.go` | `Resolve`, `Diff`, `Status`/`Log`, the conversation merge and fork |
| `internal/cloud/cloudcmd/` | `Command`: the subcommands and their output; `ExitDecisionNeeded` |
| `internal/cloud/cloudcmd/view.go` | `statusLines`, `blockedLines`, `style`: the `▸ store · detail` look and the grouped status table |
| `internal/cloud/cloudtest/` | An in-memory clai.chat for tests |
| `internal/chat/imported.go` | `ExportForeign`, `importedReader`, `ReindexPulled` |
| `internal/cli/cli.go` | Wires the command, its hooks and the exit-4 seam |

## Flow

```text
login    POST /cli/v1/start {client} -> user code, approve at /cli/login#CODE
         POST /cli/v1/token {deviceCode} every interval -> {key, access, expiresAt, endpoint, account}
         save credentials, forget any sync state. Nothing is pulled or committed.

status   plan (local, offline) + GET stores/<s>/changes?since=cursor (what the server has)
pull     changes since cursor -> per path:
           mine unchanged since base          -> take theirs (update, add, delete)
           same bytes                         -> record the base
           conversation, both changed         -> longer copy wins, or theirs becomes a fork
           otherwise, -theirs                 -> mine to cloud/trash/, take theirs
                      -mine                   -> keep mine, base = theirs (commit sends it)
                      default                 -> keep mine, theirs to cloud/theirs/<path>: a conflict
commit   refuse while conflicts are open (exit 4)
         plan -> conversations that grow: GET /me, no card or overdue -> BillingBlockedError, nothing uploaded
         -> missing blobs -> PUT blobs, 6 at a time -> POST stores/<s>/commit {message, changes[base_rev]}
         the server applies all or nothing: one stale base_rev -> BehindError (exit 4)
resolve  -mine keeps the file as it is; -theirs trashes it and takes cloud/theirs/<path>
log      GET stores/<s>/log?limit=
```

## Behavior

- **Login syncs nothing.** It stores the CLI key, the access the browser
  granted (read or write) and the account, and clears any earlier sync state,
  since the new login may be another account. `logout` revokes the key on
  the server first; a key the server already revoked still logs out.
- **The first pull asks.** Until a store's first pull on this machine, files
  that exist here and on the account with different bytes and no recorded
  base are the first pull's question: take the account's (`-theirs`), keep
  this machine's (`-mine`), or treat each as a conflict. On a terminal the
  command asks; otherwise it stops with exit 4 and changes nothing.
- **A conflict never touches the local file.** The server's side is written to
  `cloud/theirs/<path>` and recorded in the state; commit refuses until
  `resolve` settles it. `diff` shows base, mine and theirs.
- **Conversations merge on their own.** When one copy only adds messages to
  the other, the longer wins. When both added different messages, the
  server's copy is written as a new chat (`source: "clai"`, `source_id` the
  original id, title suffixed with the other device's name) and the local file
  stays.
- **What a commit leaves out.** A config file in which `FindSecret` finds a
  literal credential (a key-like field name, or an opaque value, not a `~/` or `/` path, under
  `env`); a file over `FileLimits`; and `conversations/imported/` unless
  `-imports`, which first runs the `Export` hook. `status` lists all three.
- **Exit 4** (`ExitDecisionNeeded`) means a decision is needed: conflicts
  left by a pull, a commit behind the server or blocked by conflicts, or a
  first pull with nobody to ask. `cmd.Run` maps every error to 1, so the
  command prints its message, calls `Deps.SetExit(4)` and returns nil;
  `cli.Run` wires `SetExit` to `internal.ExitStatus.Set`, whose `Resolve`
  turns that success into 4 (an `ExitCoder` error, such as remote targets'
  exit 3, replaces a failure instead).
- **Output.** Every store gets one `▸ <store> · detail · detail` line, the
  marker in the theme's primary colour and the details in its secondary one,
  and a page to visit sits on its own `→ <url>` row. Colour needs a terminal
  on stdout and colour enabled (`Deps.Color`). `status` lists up to ten
  changes one per line; past that it groups them, as `duls` groups a tree:
  chats, images, reasoning, each imported tool, or a config directory, by
  size, with files, size, share and the largest files that fit, at most six
  rows with the rest folded into `+N smaller`.
- **402 answers** (`card_required` for a conversation commit without a card,
  `payment_overdue`)
  carry a URL to the Billing and usage page, which commit prints. The
  engine asks `/me` first when a conversation commit would grow the store,
  so a refused commit uploads nothing (`BillingBlockedError`). While
  `/me` says `billing_open` is false the reason is `BillingNotOpen` and no
  billing link is printed. A plain
  `clai cloud commit` then commits the config store and says why the
  conversations were left out; naming the conversations is an error.
- **Progress.** Status, pull and commit report `Progress` (exporting,
  scanning, hashing, uploading, committing, downloading) to
  `Engine.Progress`. `scan` walks first and hashes second, so hashing
  reports against a known total and counts the files whose cached hash
  still holds (`Reused`). `cloudcmd` draws it as a `board.Status` line on
  stderr, only on a terminal, and stops it before any output or question.
- **Nothing is done twice.** Commit saves the hash cache right after the
  plan, before billing, uploads or the commit can fail, so the next run
  reads only files that moved. Uploads ask the server which blobs it
  lacks, so an interrupted commit resends only the rest. `status` asks the
  same question and lists the changes already uploaded but not committed
  (`RemoteStatus.Uploaded`), per file or per group.
- Replaced local files go to `cloud/trash/<path>.<UTC time>` and are pruned
  after 30 days. Config base copies under `cloud/base/` make `diff` work
  offline; a conversation's base is fetched from the server.
- `-r` prints JSON for `whoami`, `status`, `pull`, `commit` and `log`.

## Gotchas

- `Allowed` and the server's `validPath` are two copies of one list, and
  `secrets.vectors.json` is one file in two repos. Change both together.
- A flag on a `cloud` subcommand must not reuse a global flag's name (`-n`,
  for instance): the dispatcher would read the global one.
- The hash cache in the state file trusts size and mod time; a tool that
  rewrites a file with the same size within one mod-time tick is missed
  until its next change.
- An unreadable state file is a `StateError`, never a reset: a reset would
  make the next pull a first pull and offer to replace every file.

## Related

- [config.md](./config.md), [chat.md](./chat.md),
  [continue-from-claudex.md](./continue-from-claudex.md),
  [images.md](./images.md), [cmd-dispatch.md](./cmd-dispatch.md)
