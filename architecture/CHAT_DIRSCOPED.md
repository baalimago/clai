# Directory scoped conversations

## Goal

Introduce a system which allows for directory scoped conversations. Once this is in place, it should be possible to
add tools which allows the agent to traverse the filesystem and build context.

Example:

- User is in `/foo/bar`
- Runs `clai -dirscoped-reply "…"`
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
  "dir_hash": "<sha256> ",
  "chat_id": "my_chat_id",
  "updated": "2026-01-30T12:34:56Z"
}
```

### Lookup performance

No scanning is needed.

On each invocation that needs a binding (e.g. `-dirscope-reply`):

1. Compute `cwd` (canonicalized absolute path)
2. Compute `<hash> := sha256(canonicalCwd)`
3. Read `<clai-config>/conversations/dirs/<hash>.json` directly

### Canonicalization

To avoid creating multiple bindings for “the same” directory, we canonicalize:

- `os.Getwd()`
- `filepath.Abs`
- `filepath.Clean`
- best-effort `filepath.EvalSymlinks` (fallback to cleaned abs)

### Update rules

We update the current directory’s pointer whenever the user meaningfully selects/creates a chat from that directory:

- `clai chat new ...` -
- `clai chat continue` - If no arguments are added, assume user wants to
- `clai -dre query ...` - New flag dont reply using prevQuery conversation, instead use directory-scoped conversation
- `clai -re query ...` - This will _not_ update dirscoped conversation with prevQuery

We want to ensure that `-re` flag works as it already do with a global reply, even though replying in another directory
is a quite rare usecase.

### Reset rules

To reset the conversation linked to a directory either:

- `clai query ...`: Newly created chat will now be dirscope mapped
- `clai chat list -> <select number> -> d`: This will now set the specified conversation as dirscope mapped

## Reply-mode behavior

Reply-mode selection order:

1. If a directory binding exists for CWD and the referenced conversation file can be loaded: use that conversation’s messages.
2. Else fall back to the current behavior: load `prevQuery.json`.

This preserves backward compatibility and keeps the change low-risk.

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

- After successful `chat new` query: `SaveDirScope(wd, chatID, storeDirInIndex())`
- After resolving `chat continue`: set pointer as well
- After every query `SaveDirScope(wd, chatID, storeDirInIndex)`, unless `-re` is flagged

### 4) Prefer dir-scoped context in reply-mode

In `internal/text/conf.go` (reply mode path):

- Attempt to load dir binding + conversation; if successful, append it to `InitialChat`.
- Else append `prevQuery.json`.

### 5) Add `clai chat dir`

- Add `dir` to chat subcommands.
- Implement `dirInfo()` to return the JSON described above.

### 6) Tests

Add unit tests:

- `internal/chat/dirscope_test.go`:
  - round-trip save/load
  - stable hash
  - missing binding returns ok=false
- Tests which ensures `<config-dir>/conversations/dirs` is setup on initial config
- Tests which ensures `<config-dir>/conversations/dirs` is setup on project which already has been initialized
