# Chat architecture

This document describes how **chats/conversations** work in this repository: storage format, “previous query” replay, and the CLI flows around `clai chat continue` and directory-scoped replies.

## High level

There are two related mechanisms:

1. **Conversation transcripts** stored on disk as JSON (so they can be replayed/inspected/edited later).
2. **Reply context pointers**:
   - `prevQuery.json` (global reply context)
   - directory-scoped bindings under `conversations/dirs/` (per-CWD reply context)

Important behavioral change vs older versions:

- There is **no interactive chat loop**. One CLI invocation corresponds to one “turn” (or selection operation) and then exits.

All transcripts/pointers live under the clai config directory:

- `<clai-config>/conversations/*.json`

Resolved via `internal/utils.GetClaiConfigDir()`.

Core implementation lives in:

- `internal/chat/*` (handlers, persistence helpers, and dirscope bindings)
- `pkg/text/models/chat.go` (public `Chat`/`Message` types used throughout queriers/vendors)

## Data model

### `pkg/text/models.Chat`

```go
type Chat struct {
    Created  time.Time `json:"created"`
    ID       string    `json:"id"`
    Profile  string    `json:"profile,omitempty"`
    Messages []Message `json:"messages"`
}
```

Notes:

- `ID` is used as the filename.
- `Profile` can be persisted on the chat and is used to “stick” the last-used profile when continuing/using a conversation.
- `Messages` is an ordered transcript.

### `pkg/text/models.Message`

Messages support both plain text and multimodal “content parts”.

Key behaviors:

- JSON field `content` is **either** a string **or** an array of `{type,text,image_url}` parts.
- Internally this is represented by `Content` and `ContentParts` with custom marshal/unmarshal.

Roles used include `user`, `assistant`, `system`, and tool-related roles (depending on vendor/tooling).

## Persistence format and location

### Where chats are stored

Chats are stored as JSON files in:

- `<clai-config>/conversations/<chatID>.json`

The config directory creation ensures `conversations/` exists (see `internal/utils/config.go`).

### Reading/writing chats

Implemented in `internal/chat/chat.go`:

- `FromPath(path string) (models.Chat, error)` reads JSON into a `pkg/text/models.Chat`.
- `Save(saveAt string, chat Chat) error` writes JSON to `path.Join(saveAt, chat.ID+".json")`.

### Chat IDs

Chat ID generation is implemented in `internal/chat/chat.go`:

- `HashIDFromPrompt(prompt string)` is the current/default ID format.
- `IDFromPrompt(prompt string)` is **deprecated** (kept for backward compatibility and resolution).

ID resolution when selecting/continuing chats (`internal/chat/handler.go:findChatByID`) supports:

1. selecting by **index** from `clai chat list`
2. selecting by **exact chat ID**
3. fallback: derive legacy ID via `IDFromPrompt(...)`
4. fallback: derive hash ID via `HashIDFromPrompt(...)`

## CLI entrypoints and user flows

### Mental model: the shell is the “chat UI”

You do not “enter a chat” inside `clai`.

Instead:

- each `clai ... query <prompt>` invocation appends a new user message, calls the model, and writes an updated transcript to disk
- reply flags decide **which transcript** is used as context

This makes chats composable with normal shell tooling (pipes, redirects, history, scripts).

### Query (normal way to add a message)

`clai query <prompt>`:

- creates/updates a transcript
- updates the global previous query (`prevQuery.json`)
- updates the directory binding for the current working directory (CWD)

Subsequent queries can be threaded using:

- `-re` (reply to global previous query)
- `-dre` (reply to directory-scoped previous query)

### `chat continue` (bind a chat to the current directory)

`clai chat continue <chatID|index> [prompt...]`:

- loads an existing chat from disk (by index or ID)
- if extra tokens are present after an index selection, they are re-joined and treated as the optional `prompt` to append
- if an optional prompt is present, appends `user(prompt)` in-memory
- prints a **fast obfuscated preview** (not full transcript rendering)
- updates the directory-scoped binding for the current working directory to point at that chat

The primary purpose is selection/binding: choose which existing transcript should be used as reply context for `-dre` in the current directory.

#### Output format (preview)

The preview uses `internal/chat/obfuscated_print.go` and prints early messages in an obfuscated single-line form:

- `[#<nr> r: "<role>" l: 00042]: <msg-preview>`

Behavioral details:

- message length is **zero-padded** to 5 digits
- long messages are shortened with `...` and a “and N more runes” note
- the last messages (roughly the last six) are pretty-printed more fully

After binding the directory scope, `chat continue` prints a notice that the chat is replyable with:

- `clai -dre query <prompt>`

### Profile “sticking” when continuing

In `internal/chat/handler.go:cont`:

- If the loaded chat already has `chat.Profile != ""`, it overrides the runtime handler config so continuation uses that profile.
- Otherwise, if a `--profile` was provided (wired into `UseProfile`), the handler stamps it into `chat.Profile` so future continuations persist it.

### `chat list` / inspect / edit / delete

`clai chat list` exists for discovering chats and doing transcript operations.

Implementation: `internal/chat/handler_list_chat.go`:

- `list()` reads every JSON file in `<convDir>`, unmarshals to `Chat`, sorts by `Created` desc.
- `listChats()` uses `utils.SelectFromTable` to show a selection table.

After selecting a chat, `actOnChat()` prints a details view and offers actions:

- edit messages (via `$EDITOR`)
- delete messages
- save as `prevQuery.json`

No interactive chat session is started from this UI.

## “Previous query” capture and replay

A special chat file is used for the global reply context:

- `<clai-config>/conversations/prevQuery.json`

Implemented in `internal/chat/reply.go`.

### SaveAsPreviousQuery

`SaveAsPreviousQuery(claiConfDir, msgs)` writes:

- always: `prevQuery.json` with ID `prevQuery`
- additionally (when `len(msgs) > 2`): saves a *new conversation file* derived from the first user message using `HashIDFromPrompt(firstUserMsg.Content)`

This preserves one-off queries and optionally promotes richer exchanges into normal conversations.

### LoadPrevQuery

Loads `prevQuery.json` (printing a warning if absent).

## Directory-scoped replies

Directory-scoped bindings and lookup are described in more detail in `architecture/CHAT_DIRSCOPED.md`.

In short:

- `clai -dre query ...` replies using the conversation bound to CWD
- `clai chat continue ...` is a convenient way to bind CWD → an existing conversation

## Model interaction surface

The chat handler does not call vendor APIs directly. It depends on:

- `internal/models.ChatQuerier` with `TextQuery(ctx, chat) (Chat, error)`

Different queriers/vendors implement this (see `internal/text/*` and `internal/vendors/*`). The handler treats it as a black box that may append assistant responses, run tool calls, and/or transform the transcript.

## Caveats

- **Shell-driven**: each question/answer is a normal CLI invocation.
- **ID mismatches**: older conversations may use the legacy `IDFromPrompt` naming; lookup supports both.
- **JSON is user-editable**: transcripts live under `<clai-config>/conversations` and can be edited manually.
