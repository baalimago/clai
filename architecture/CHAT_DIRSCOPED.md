# Directory scoped conversations

## Goal

Introduce a system which allows for directory scoped conversations. Once this is in place, it should be possible to
add tools which allows the agent to traverse the filesystem and build context.

Example:

- User is in `/foo/bar`
- Runs `clai -dir-reply "…"` (alias: `-dre`)
- The tool should use the conversation associated with `/foo/bar` as context.

## Current state

- Conversations are JSON files stored at `<clai-config>/conversations/<chatID>.json`.
- Reply mode currently uses `prevQuery.json` (`internal/chat/reply.go`) as a _global_ transcript.

## Design: one pointer file per directory

For each directory with a dirscoped conversation, we will create a file which maps dirscope -> conversation.

### Storage

We add a new config subdir (created by `CreateConfigDir`):

- `<clai-config>/conversations/dirs/`

Each directory binding is stored as:

- `<clai-config>/conversations/dirs/<sha256(canonicalDir)>.json`

The name is hashed to obfuscate filestructure of important conversations. Not that the user wouldn't be
turbo-pwned if Trudy reach the config files, but still.

Example file:

```json
{
  "version": 1,
  "dir_hash": "<sha256>",
  "chat_id": "my_chat_id",
  "updated": "2026-01-30T12:34:56Z"
}
```

### Lookup performance

No scanning is needed.

On each invocation that needs a binding (e.g. `-dir-reply`/`-dre`):

1. Compute `cwd` (canonicalized absolute path)
2. Compute `<hash> := sha256(canonicalCwd)`
3. Read `<clai-config>/conversations/dirs/<hash>.json` directly

### Canonicalization

To avoid creating multiple bindings for “the same” directory, we canonicalize:

- `os.Getwd()`
- `filepath.Abs`
- `filepath.Clean`
- best-effort `filepath.EvalSymlinks` (fallback to cleaned abs)

### Update rules (creating/updating directory bindings)

**Important: this design follows Model 1 (backward compatible reply mode).**

- `-re` remains a _global_ reply mode that uses `prevQuery.json` exactly as today.
- Directory-scoped reply is opt-in via `-dir-reply` (alias `-dre`).
- Reply actions do not mutate directory bindings.

We update the current directory’s pointer whenever the user meaningfully selects/creates a chat from that directory
_outside of reply mode_:

- `clai chat new ...`: after creating the chat, bind CWD -> that `chat_id`.
- `clai chat continue ...`: after resolving the chat to continue, bind CWD -> that `chat_id`.
- `clai query ...`: after creating/resolving the chat used for the query, bind CWD -> that `chat_id`.

We **do not** update the directory binding when running any reply mode:

- `clai -re "..."`: reply with global `prevQuery.json` (existing behavior).
- `clai -dir-reply "..."` / `clai -dre "..."`: reply with the directory-scoped conversation.

### Reset rules

To reset (rebind) the conversation linked to a directory:

- `clai query ...`: The chat used for that query becomes the new binding for CWD.
- `clai chat list -> <select number> -> d`: Set the specified conversation as the binding for CWD.

## Reply-mode behavior

### Global reply (existing; unchanged)

- `clai -re "..."` loads `prevQuery.json` and replies using that global transcript.

### Directory-scoped reply (new; opt-in)

- `clai -dir-reply "..."` (alias: `-dre`) attempts to load the directory binding for CWD.
- If a binding exists for CWD and the referenced conversation file can be loaded: use that conversation’s messages.
- Else: return an error explaining that no directory-scoped conversation is bound to the current directory.

This makes the new behavior explicit and keeps `-re` backward compatible.

### Example scenario (expected behavior)

Legend:

- `c0`, `c1` = directory-scoped conversations (stored under `<clai-config>/conversations/<chatID>.json`)
- `g0`, `g1`, ... = **global** previous-query transcript (`<clai-config>/conversations/prevQuery.json`)
- `dir(/path)=<chat>` = current directory binding (stored under `<clai-config>/conversations/dirs/<hash>.json`)

Note: in this codebase, `-r` is **`-raw`** (output formatting), _not reply_. Global reply is `-re/-reply`.

[/foo/bar/]$ clai query hello ->

- use/create **c0**; bind **dir(/foo/bar)=c0**; update global **g=g0**

[/foo/bar/]$ clai -r query hello ->

- same semantics as `query` (non-reply), just raw output; bind **dir(/foo/bar)=c0'**; update **g=g1**

[/foo/bar/]$ clai -dre query hello ->

- dir-reply uses **dir(/foo/bar)=c0'** as context; binding unchanged

[/foo/bar/baz/]$ clai -dre query hello ->

- **ERROR** if `dir(/foo/bar/baz)` is unset (no fallback to global `g`)

[/foo/bar/baz/]$ clai query hello ->

- use/create **c1**; bind **dir(/foo/bar/baz)=c1**; update global **g=g2**

[/foo/bar/baz/]$ clai -dre query hello ->

- dir-reply uses **c1** as context

[/foo/bar/]$ clai -re query hello ->

- global reply uses **g** (currently **g2**, from the last non-reply query), ignoring dir bindings

## `clai chat dir`

Add a new subcommand:

- `clai chat dir`

It prints JSON describing the conversation bound to CWD.

Initial fields (extensible):

- `chat_id`
- `profile`
- `am_messages`
- `updated` (from pointer file)
- `conversation_created` (from conversation file)

If no binding exists, it prints `{}`.

## Implementation details (how it works in this repo)

This document describes the _intended_ design. Here is how it is actually implemented today in `clai`.

### Files and responsibilities

- `internal/chat/dirscope.go`
  - Implements the persistence layer for dir bindings.
  - A "dir binding" is a small JSON file in `<confDir>/conversations/dirs/<hash>.json`.
  - Key methods:
    - `(*ChatHandler).LoadDirScope(dir string) (DirScope, ok, error)`
    - `(*ChatHandler).SaveDirScope(dir, chatID string) error`
    - `(*ChatHandler).UpdateDirScopeFromCWD(chatID string) error` (and a package-level wrapper)

- `internal/chat/dirscope_prevquery.go`
  - Bridges directory-scoped conversations into the existing _global reply_ mechanism.
  - `SaveDirScopedAsPrevQuery(confDir)` loads the bound dir conversation and overwrites
    `<confDir>/conversations/prevQuery.json` with those messages.
  - This is what makes `clai -dre query ...` work _without_ having to add a brand new reply pipeline.

- `internal/setup.go`
  - Orchestrator that wires flags/modes to the right behavior.
  - When `-dre/-dir-reply` is used with `query`, it first calls `SaveDirScopedAsPrevQuery(...)` and then sets
    `ReplyMode=true` so the existing reply path uses `prevQuery.json`.
  - After a successful `query`, it updates the directory binding via `chat.UpdateDirScopeFromCWD(...)`.

- `internal/chat/replay.go` + `internal/dre.go`
  - Separate from replying, there is a dedicated `dre` _command_ that prints the last message of the dir-scoped
    conversation bound to CWD:
    - `clai dre` → `internal/dre.go` calls `chat.Replay(raw, true)`.
    - `chat.Replay(..., true)` → loads the dir binding and prints the last message from that conversation.

### Canonicalization + hashing in code

The pointer-file name is derived from the current directory:

1. `canonicalDir(dir)`:
   - If `dir==""`, it uses `os.Getwd()`.
   - Converts to absolute path: `filepath.Abs`.
   - Cleans it: `filepath.Clean`.
   - Best-effort resolves symlinks: `filepath.EvalSymlinks(clean)` (if it fails, it keeps the clean path).

2. `dirHash(canonicalDir)`:
   - `sha256.Sum256([]byte(canonicalDir))` and hex-encodes it.

3. Final path:
   - `<confDir>/conversations/dirs/<hash>.json` (`hash` is the hex sha256 string)

So the lookup is O(1): canonicalize → hash → read exactly one file.

### Save/Load semantics

- `LoadDirScope(dir)`:
  - Computes the hash path and `os.ReadFile`s it.
  - If missing (`fs.ErrNotExist`), returns `(DirScope{}, ok=false, nil)`.
  - Otherwise returns the unmarshaled `DirScope{Version, DirHash, ChatID, Updated}`.

- `SaveDirScope(dir, chatID)`:
  - Computes canonical+hash, creates a `DirScope{Version:1, DirHash:..., ChatID:..., Updated: now}`.
  - Writes JSON using an atomic pattern: `os.CreateTemp(...); json.Encode; close; os.Rename(tmp, final)`.
  - Requires that `<confDir>/conversations/dirs` already exists (config setup creates it).

### How `-dre query ...` actually replies

There are two different concepts that are easy to mix up:

1. `clai dre` (a **command**) prints the last message in the dir-scoped conversation (no LLM call).

2. `-dre/-dir-reply` (a **flag**) modifies how `query` behaves.

For the flag case, the flow is:

1. `internal/setup.go` sees `mode==QUERY` and `postFlagConf.DirReplyMode==true`.
2. It calls `chat.SaveDirScopedAsPrevQuery(confDir)`:
   - loads dir binding (`LoadDirScope("")`)
   - loads that conversation file (`FromPath(<chat_id>.json)`)
   - converts messages to `pkg/text/models.Message`
   - appends **two** `{"role":"assistant","content":""}` marker messages (this matches an existing CLI-level
     expectation tested in `main_test.go`)
   - overwrites `prevQuery.json` (`SaveAsPreviousQuery`)
3. `setup.go` forces `ReplyMode=true` so the existing reply path uses the just-written `prevQuery.json`.

Net effect:

- Directory-scoped replies are implemented by _copying_ the bound directory conversation into the global
  `prevQuery.json` and then reusing the old `-re` reply machinery.
- This implies `-dre query ...` is not reading the conversation file directly during the reply; it is reading
  `prevQuery.json` after it has been replaced.

### When bindings are updated

Bindings are updated after every successful non-reply `query` (including `-dre query ...`):

- In `internal/setup.go` (after `setupTextQuerierWithConf` succeeds), it calls:
  - `chat.UpdateDirScopeFromCWD(confDir, updateChatID)`

For normal `query`, `updateChatID` is `tConf.InitialChat.ID`.

For `-dre query`, it intentionally sets `updateChatID` to the _bound_ chat id (`dirReplyChatID`) so the directory
continues to point at the same chat you are replying to.

### Current mismatch vs the architecture doc

The architecture doc says "`-dir-reply` / `-dre` loads the directory binding + conversation".

In the current implementation, `-dre` for queries works by:

- loading the dir binding + conversation _once_,
- copying it into `prevQuery.json`,
- then using the existing global reply code path against `prevQuery.json`.

So the reply plumbing is still "global", but the source transcript gets swapped beforehand.
