# Chat architecture

This document describes how **chats/conversations** are implemented in this repository (CLI flow, storage format, and how model replies are produced).

## High level

There are two related features:

1. **Interactive conversations** via `clai chat ...`
2. **“Previous query” replay** via the `prevQuery.json` mechanism (used by other modes to capture a one-off query as a chat that can be replayed/inspected later).

Both are stored as JSON chat transcripts under the user config directory:

- `~/.clai/conversations/*.json` (resolved via `internal/utils.GetClaiConfigDir()`)

Core implementation lives in:

- `internal/chat/*` (CLI handler + persistence helpers)
- `pkg/text/models/chat.go` (public Chat/Message model used across queriers/vendors)

## Data model

### `pkg/text/models.Chat`

A chat is:

```go
type Chat struct {
    Created  time.Time `json:"created"`
    ID       string    `json:"id"`
    Profile  string    `json:"profile,omitempty"`
    Messages []Message `json:"messages"`
}
```

Notes:

- `ID` is used as filename (see persistence).
- `Profile` is persisted on the chat and is used to “stick” the last-used profile when continuing a chat.
- `Messages` is an ordered transcript.

### `pkg/text/models.Message`

Messages support both plain text and multimodal “content parts” (primarily for vendors like OpenAI).

Key behaviors:

- JSON field `content` is **either** a string **or** an array of `{type,text,image_url}` parts.
- Internally this is represented as:

```go
type Message struct {
    Role         string
    ToolCalls    []Call
    ToolCallID   string

    Content      string             `json:"-"`
    ContentParts []ImageOrTextInput `json:"-"`
}
```

Custom marshal/unmarshal implements the polymorphic `content`.

Roles used throughout the codebase include `user`, `assistant`, `system`, and tool-related roles (depending on vendor/tooling).

## Persistence format and location

### Where chats are stored

Chats are stored as JSON files in:

- `<claiConfigDir>/conversations/<chatID>.json`

The config directory is managed/created by `internal/utils/config.go` (it ensures `conversations/` exists).

### Reading/writing chats

Implemented in `internal/chat/chat.go`:

- `FromPath(path string) (models.Chat, error)` reads JSON into a `pkg/text/models.Chat`.
- `Save(saveAt string, chat Chat) error` writes JSON to `path.Join(saveAt, chat.ID+".json")`.

### Chat IDs

`internal/chat/chat.go:IDFromPrompt(prompt string)` creates an ID by:

- taking the first 5 tokens of the prompt
- joining with `_`
- replacing `/` and `\\` with `.` to keep it path-safe

This means chat IDs are deterministic from the starting prompt, and collisions are possible if two chats share the same first 5 words.

## CLI entrypoints and user flows

### Handler construction

The chat CLI is wired through `internal/chat/handler.go`.

`chat.New(...)` builds a `*ChatHandler` with:

- a `models.ChatQuerier` (`cq.q`) which is the abstraction responsible for producing the next assistant/tool messages
- `preMessages` (seed messages, typically system prompts/profile-derived messages)
- `config` (`UseProfile`, `UseTools`, `Model`) used to determine querying behavior
- `convDir` set to `<claiConfigDir>/conversations`

### Subcommands

Implemented in `ChatHandler.actOnSubCmd(ctx)`:

- `chat new <prompt>`
  - creates a new `Chat` with `IDFromPrompt(prompt)`
  - seeds `Messages` with `preMessages + user(prompt)`
  - calls `TextQuery` once to get the first assistant response
  - enters the interactive loop

- `chat continue <chatID|index> [prompt]`
  - loads an existing chat from disk
  - if an optional prompt is provided, appends `user(prompt)`
  - prints the chat transcript
  - enters the interactive loop

- `chat list`
  - loads all chats from `<convDir>`, sorts by `Created` desc
  - displays a paginated selection table
  - allows actions on a selected chat: continue, edit, delete messages, save as prevQuery

- `chat delete <chatID|index>`
  - resolves the chat and deletes its JSON file

### ID vs index resolution

`findChatByID(potentialChatIdx string)` supports:

- passing a numeric index from the `chat list` view
- passing an ID-like prompt (it calls `IDFromPrompt(...)` on the input)

If a numeric index is provided and additional tokens exist on the command line, those tokens are re-joined and treated as the new prompt to append when continuing.

## Interactive loop (how “chatting” happens)

The interactive behavior is in `ChatHandler.loop(ctx)`:

1. A `defer Save(convDir, cq.chat)` ensures the current state is persisted when the loop exits due to errors/panic/return.
   - Note: the loop is intended to run indefinitely until user interrupts or a query fails.

2. Every iteration:

   - The chat’s `Profile` field is updated to the currently active profile (`cq.config.UseProfile`) so the conversation remains in sync with user configuration.

   - It inspects `lastMessage := cq.chat.Messages[len-1]`:

     - If `lastMessage.Role == "user"`:
       - it pretty-prints that message (so the prompt is shown)

     - Else (assistant/tool/system last message):
       - it prints a prompt line including the current effective config: tools/profile/model
       - reads a new line from stdin (`utils.ReadUserInput()`)
       - appends it as a new `user` message

3. It calls the model via the querier:

   - `newChat, err := cq.q.TextQuery(ctx, cq.chat)`

   The contract is: `TextQuery` takes the full transcript and returns an updated `Chat` (typically with one or more new assistant/tool messages appended, and possibly with tool call handling performed).

4. The handler assigns `cq.chat = newChat` and the loop repeats.

### Choosing/sticking profiles when continuing

In `cont(ctx)`:

- If the loaded chat already has `chat.Profile != ""`, it overrides the current runtime config so continuing uses the same profile as last time.
- Otherwise, if a `--profile`/`UseProfile` was provided on the CLI, the handler stamps it into `chat.Profile` so the first continuation persists it.

This makes `clai chat continue <id>` default to the profile last used for that conversation.

## Listing, inspecting, editing and deleting chats

`internal/chat/handler_list_chat.go` provides a TUI-like flow:

- `list()` reads every JSON file in `<convDir>`, unmarshals to `Chat`, sorts by `Created` desc.
- `listChats()` uses `utils.SelectFromTable` to show:
  - index
  - created timestamp
  - number of messages
  - prompt summary (taken from the first user message)

After selecting a chat, `actOnChat()` prints a details view and offers actions:

- `[c]ontinue` → starts the main loop on that chat
- `[e]dit messages` → opens a chosen message in `$EDITOR` and saves the updated transcript
- `[d]elete messages` → allows deleting selected message indices
- `[p]revQuery` → saves this chat’s messages as `prevQuery.json` (see below)

## “Previous query” capture and replay

`internal/chat/reply.go` implements a special chat file:

- `<claiConfigDir>/conversations/prevQuery.json`

### SaveAsPreviousQuery

`SaveAsPreviousQuery(claiConfDir, msgs)` writes:

- always: `prevQuery.json` with ID `prevQuery`
- additionally (when `len(msgs) > 2`): saves a *new conversation file* derived from the first user message using `IDFromPrompt(firstUserMsg.Content)`.

Intent: preserve one-off queries and optionally promote richer exchanges into normal conversations.

### LoadPrevQuery

Loads `prevQuery.json` (printing a warning if absent).

## Model interaction surface

The chat handler does not call vendor APIs directly. It depends on:

- `internal/models.ChatQuerier` (interface) with method `TextQuery(ctx, chat) (Chat, error)`

Different queriers/vendors implement this (see `internal/text/*` and `internal/vendors/*`). The handler treats it as a black box that:

- may append assistant responses
- may execute tool calls and append tool results
- may alter the transcript to match the vendor’s expectations

## Operational characteristics / caveats

- **No explicit exit command in the loop**: quitting is typically via Ctrl+C or by causing input/query to fail. (The prompt prints `| [q]uit` but `q` is currently treated as a normal user message.)
- **Filename collisions**: IDs are derived from prompt tokens; different conversations can map to the same ID.
- **Persistence timing**: chats are saved on loop exit via `defer Save(...)`. Edits/deletes save immediately.
- **JSON is user-editable**: the help text explicitly encourages manual editing under `.../.clai/conversations`.
