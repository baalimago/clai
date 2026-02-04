# Directory scoped conversations

## Goal

Allow conversations to be scoped to the current working directory (CWD). This enables directory-specific context and future tooling that can traverse the filesystem and build relevant context.

Example:

- User is in `/foo/bar`
- Runs `clai -dir-reply ...` (alias: `-dre`)
- The tool uses the conversation bound to `/foo/bar` as reply context.

## Storage

Dir bindings are stored as pointer files under:

- `<clai-config>/conversations/dirs/`

Each binding:

- `<clai-config>/conversations/dirs/<sha256(canonicalDir)>.json`

Pointer file shape:

```json
{
  "version": 1,
  "dir_hash": "<sha256>",
  "chat_id": "my_chat_id",
  "updated": "2026-01-30T12:34:56Z"
}
```

Conversations themselves are stored as usual:

- `<clai-config>/conversations/<chatID>.json`
- Global reply transcript is:
  - `<clai-config>/conversations/prevQuery.json`

## Canonicalization

To avoid multiple bindings for the same directory, the directory is canonicalized before hashing:

- `os.Getwd()` (when dir is empty)
- `filepath.Abs`
- `filepath.Clean`
- best-effort `filepath.EvalSymlinks` (fallback to cleaned abs)

## Lookup

On each invocation that needs a binding:

1. Canonicalize `cwd`
2. Compute `sha256(canonicalCwd)`
3. Read `<clai-config>/conversations/dirs/<hash>.json`

No directory scanning is needed.

## Update rules

Bindings are updated when the user selects/creates a chat from a directory outside of reply mode:

- `clai query ...`: bind CWD → the chat used for the query
- `clai chat new ...`: bind CWD → the newly created chat
- `clai chat continue ...`: bind CWD → the continued chat

Bindings are **not** updated by reply actions:

- `clai -re ...` (global reply)
- `clai -dre ...` / `clai -dir-reply ...` (dir-scoped reply)

## Reply behavior

### Global reply (existing)

- `clai -re ...` replies using `<clai-config>/conversations/prevQuery.json`.

### Directory-scoped reply (opt-in)

- `clai -dre query ...` uses the conversation bound to CWD as reply context.
- If there is no binding for CWD, it errors (no fallback to global).

## `clai chat dir`

A convenience command to display which chat is associated with the current directory.

Resolution:

1. If a dir binding exists for CWD and the referenced chat loads: show that chat (`scope="dir"`).
2. Else, if `prevQuery.json` exists: show that global chat (`scope="global"`, `chat_id="prevQuery"`).
3. Else: empty.

Output:

- Raw (`-r`): stable JSON object; empty state is `{}`.
- Non-raw: a short human-readable block.

Fields include:

- `scope`: `"dir" | "global"`
- `chat_id`
- `profile` (if present)
- `updated` (dir binding only)
- `conversation_created`
- `replies_by_role`
- `tokens_total`
