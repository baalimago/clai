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

- `-re` remains a *global* reply mode that uses `prevQuery.json` exactly as today.
- Directory-scoped reply is opt-in via `-dir-reply` (alias `-dre`).
- Reply actions do not mutate directory bindings.

We update the current directory’s pointer whenever the user meaningfully selects/creates a chat from that directory
*outside of reply mode*:

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

Note: in this codebase, `-r` is **`-raw`** (output formatting), *not reply*. Global reply is `-re/-reply`.

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

## Implementation plan (TDD-first)

### 1) Directory pointer persistence (`internal/chat/dirscope.go`)

Implement:

- `(cq *ChatHandler) LoadDirScope(dir) (DirScope, ok, error)`
- `(cq *ChatHandler) SaveDirScope(dir, chatID, storeDir bool) error`

Notes:

- Uses atomic write via temp file + `os.Rename`.
- Uses the canonicalization + hashing rules above.

### 2) Wire pointer updates into chat mode

In `internal/chat/handler.go`:

- After successful `chat new`: `SaveDirScope(wd, chatID, storeDirInIndex())`
- After resolving `chat continue`: `SaveDirScope(wd, chatID, storeDirInIndex())`
- After every non-reply `query`: `SaveDirScope(wd, chatID, storeDirInIndex())`

### 3) Add directory-scoped reply mode

In the reply mode path (where `-re` is handled today):

- Keep `-re` exactly as-is (load `prevQuery.json`).
- Add `-dir-reply` / `-dre`:
  - Load dir binding + conversation.
  - If missing/unloadable, error (no fallback to `prevQuery.json`).

### 4) Add `clai chat dir`

- Add `dir` to chat subcommands.
- Implement `dirInfo()` to return the JSON described above.

### 5) Tests

Add unit tests:

- `internal/chat/dirscope_test.go`:
  - round-trip save/load
  - stable hash
  - missing binding returns ok=false
- Tests which ensures `<config-dir>/conversations/dirs` is setup on initial config
- Tests which ensures `<config-dir>/conversations/dirs` is setup on project which already has been initialized

Add reply-mode tests:

- `-re` continues to use `prevQuery.json` even if a dir binding exists.
- `-dir-reply`/`-dre` uses the dir binding when present.
- `-dir-reply`/`-dre` errors when no binding exists (no fallback).
- Neither `-re` nor `-dir-reply` mutates/creates the dir binding.
