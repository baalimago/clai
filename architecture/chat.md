# Chat architecture

This document describes how **chats/conversations** work in this repository: storage format, “previous query” replay, and the CLI flows around `clai chat continue` and directory-scoped replies.

## High level

There are two related mechanisms:

1. **Conversation transcripts** stored on disk as JSON (so they can be replayed/inspected/edited later).
2. **Reply context pointers**:
   - `globalScope.json` (global reply context)
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

    // Source is a stable, human-readable origin label.
    // Empty ("") for native clai chats.
    // Examples: "claude-code", "codex", "cursor", "clai" (for forked chats).
    Source string `json:"source,omitempty"`

    // SourceID is the originating tool's conversation identifier, or the
    // parent chat ID when Source == "clai" (fork).
    // clai treats (Source, SourceID) as a unique pair for dedup/provenance.
    SourceID string `json:"source_id,omitempty"`

    // GroupKey is a hex-encoded SHA-256 of the first user message's canonical
    // text. Stamped once on first persist, never rewritten. Empty for pre-feature
    // chats or conversations without a user message.
    GroupKey string `json:"group_key,omitempty"`

    // Title and Summary are model-generated labels (see summaries.md);
    // SummaryAt is provenance only, never a staleness index.
    Title     string    `json:"title,omitempty"`
    Summary   string    `json:"summary,omitempty"`
    SummaryAt time.Time `json:"summary_at,omitzero"`

    Messages         []Message   `json:"messages"`
    TokenUsage       *Usage      `json:"usage,omitempty"`
    RecentTokenUsage *Usage      `json:"recent_usage,omitempty"`
    Queries          []QueryCost `json:"queries,omitempty"`
}
```

Notes:

- `ID` is used as the filename.
- `Profile` can be persisted on the chat and is used to “stick” the last-used profile when continuing/using a conversation.
- `Messages` is an ordered transcript.
- `TokenUsage` contains the billable usage for all model calls in the latest session.
- `RecentTokenUsage` contains only the final model call. This value estimates the context size of the next request.
- `Queries` contains the usage and cost for each recorded session. Rows with `purpose: "summary"` belong to the summarizer, not to the conversation's own model calls.
- `Title`/`Summary`/`SummaryAt` are generated once, in flight on the first persist or in batch by `clai chat summarize`; every surface falls back to the first user message when `Title` is empty. See `architecture/summaries.md`.

### `pkg/text/models.Message`

Messages support both plain text and multimodal “content parts”.

Key behaviors:

- JSON field `content` is **either** a string **or** an array of parts. A text-only message
  keeps the string form, byte for byte.
- `Parts` is the closed set of typed parts (`pkg/text/models/parts.go`): `TextPart` and
  `ImagePart`. They encode as `{"type":"text","text":…}` and
  `{"type":"image","asset":"<sha256-hex>.<ext>","mime":…,"origin":"user|tool|generated"}`.
  An image is referenced by asset, never by bytes: marshalling a pending image (bytes, no
  asset) fails with `ErrPendingImage`.
- `ImagePart.Handle()` is the first 6 hex digits of the image's SHA-256, equal for the pending
  and the stored form. `Message.Describe()` renders a message as text, an image as
  `[image <handle>]`.
- Legacy arrays (`{"type":"image_url","image_url":{"url":"data:<mime>;base64,…"}}`) decode into
  pending `ImagePart`s with origin `user`; a non-data or corrupt URL fails with
  `ErrLegacyImageURL`, and any other part type with `ErrUnknownPartType`.
- OpenAI's opaque reasoning items (`ReasoningItems`) are held in memory and persisted **out
  of band**, never inlined, so the conversation JSON stays human-readable and portable.

Roles used include `user`, `assistant`, `system`, and tool-related roles (depending on vendor/tooling).

## Persistence format and location

### Where chats are stored

Chats are stored as JSON files in:

- `<clai-config>/conversations/<chatID>.json`

The config directory creation ensures `conversations/` exists (see `internal/utils/config.go`).

#### Image assets (`internal/asset`)

Image bytes live in a content-addressed store, `asset.NewStore(dir)`, meant for
`<clai-config>/conversations/assets/`; a conversation holds only the file name, its *ref*,
`<sha256-hex>.<ext>`. The extension comes from the MIME type (`png`, `jpg`, `gif`, `webp`;
nothing else is an image), and `asset.Sniff` must agree with the declared type. `Put` is
idempotent, never rewrites an existing file, and writes through a temp file and a rename, so a
failed write leaves nothing under the ref; the directory appears on the first `Put`. `Load`
re-hashes the bytes and fails with `ErrAssetCorrupt` on a mismatch. `Resolve` maps a 6-hex-digit
handle to the one ref it prefixes, or fails with `ErrUnknownHandle` or `*AmbiguousHandleError`.
`Sweep(live)` removes every ref-named file not in `live` and leaves other files alone.
`assettest.NewMemory()` is an in-memory implementation that passes the same contract suite.

#### Conversation persister (`internal/chat/persister.go`)

`chat.NewPersister(confDir, store)` ties conversations to the asset store, and no conversation
reaches disk with a pending image:

- `Save` stores every pending `ImagePart` with `Put` first, then writes the chat with refs only
  and returns it as written. When `Put` fails, nothing is written. A chat with no image is
  written exactly as before, and no `assets/` directory appears.
- `Promote` writes the files `SaveAsPreviousQuery` writes (below). It stores the images once, so
  every copy references the same asset files.
- `Load` reads a conversation. A legacy inline image becomes a pending part, whose bytes must
  sniff as the declared type (`asset.ErrNotImage` or `*asset.MIMEMismatchError`, naming the path
  and message index). The file is rewritten as refs only on its next save.
- `Delete` removes the conversation and its reasoning sidecar, then sweeps every asset that no
  remaining `conversations/*.json` file references, `globalScope.json` included. A missing id is
  `ErrChatNotFound`. A failed sweep, or a conversation file it cannot decode, is
  `*SweepFailedError`, and the sweep then removes nothing. A sidecar that cannot be removed does
  not stop the delete or the sweep: it is a typed, non-fatal error returned after the sweep, and
  `clai chat delete` prints it as a warning, as before images were stored.

The free writers `Save`, `SaveWithoutIndex` and `SaveAsPreviousQuery` behave the same over
`asset.NewStore(<conversations>/assets)`.

Every reader of a conversation the user continues goes through `Load`: `-re`
(`LoadPrevQuery`), `-dre` (`LoadDirScopedContext`), `chat continue`, `chat delete` and replay.
Cloning a foreign conversation on continue writes it with `Save`, and `chat delete` is
`Delete`, so it sweeps the assets only that conversation referenced. `chat.AssetsDir(confDir)`
and `chat.GlobalScopePath(confDir)` name the asset directory and `globalScope.json` for other
packages; `photo.PreviousQueryImage` reads the last image of the previous query through them.

#### Images on text surfaces

A surface that shows only text renders an image as its `Message.Describe()` label,
`[image <handle>]`: the `chat list` index preview and the selected row's `summary:` line, the
`chat continue` preview, the lookback tools (`search_conversations`, `inspect_conversation`,
`read_message`) and the summarizer's transcript. Replay prints more: a stored image as
`[image <handle>] <absolute asset path>`, and a pending legacy one as
`[image <handle>] (not stored yet)` (see [replay.md](./replay.md)). The message editor offers only
the text parts of a parts message, joined by newlines; on save the edited text replaces the first
text part, the other text parts are dropped, and every image keeps its place.

#### Reasoning sidecar (OpenAI Responses)

Reasoning-model tool loops on the OpenAI Responses API must replay the model's sealed
reasoning items to stay coherent while remaining stateless (`store:false`). These opaque
blobs are stored **out of band**, one file per assistant turn, keyed by response id:

- `<clai-config>/conversations/reasoning/<chatID>/<tool_call_id>.json` (an ordered array of
  `{id, encrypted_content, summary}` items).

The first tool-call ID in the assistant turn keys the sidecar. That ID is already part of
the portable transcript, avoiding an extra OpenAI-specific field in message JSON.

`Save`/`FromPath` write and restore the sidecar automatically (best-effort — a sidecar
failure never blocks saving or loading the conversation itself); deleting a chat GCs its
`reasoning/<chatID>/` directory. See `internal/chat/reasoning_sidecar.go` and
[openai-responses.md](./openai-responses.md#feature-parity).

### Reading/writing chats

Implemented in `internal/chat/chat.go`:

- `FromPath(path string) (models.Chat, error)` reads JSON into a `pkg/text/models.Chat`.
- `Save(saveAt string, chat Chat) error` stores the chat's pending images under
  `<saveAt>/assets/`, then writes JSON to `path.Join(saveAt, chat.ID+".json")`.
- The index row's first user message is that message's `Describe()`, so an image-only message
  previews as `[image <handle>]`.

### Chat IDs

Chat ID generation is implemented in `internal/chat/chat.go`:

- `HashIDFromPrompt(prompt string)` is the current/default ID format.
- `IDFromPrompt(prompt string)` is **deprecated** (kept for backward compatibility and resolution).

ID resolution when selecting/continuing chats (`internal/chat/handler.go:findChatByID`) supports:

1. selecting by **index** from `clai chat list`
2. selecting by **exact chat ID`
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
- updates the global previous query (`globalScope.json`)
- updates the directory binding for the current working directory (CWD)

Subsequent queries can be threaded using:

- `-re` (reply to global previous query)
- `-dre` (reply to directory-scoped previous query)

### `chat dir` and `chat dirv2`

Both commands show information for the conversation that is bound to the current directory. They use `globalScope.json` when no directory binding exists.

`clai chat dir` keeps the original text and JSON formats. This format does not show `RecentTokenUsage`.

`clai chat dirv2` adds total and recent usage. In raw mode (`-r`), it returns this structure:

```json
{
  "version": 2,
  "scope": "dir",
  "chat_id": "example-chat-id",
  "profile": "default",
  "updated": "2026-08-03T12:00:00Z",
  "conversation_created": "2026-08-03T11:00:00Z",
  "replies_by_role": {"assistant": 2, "user": 2},
  "token_usage": {
    "recent": {"uncached_input": 1200, "cached_input": 8000, "output": 300, "total": 9500},
    "total": {"uncached_input": 2400, "cached_input": 15000, "output": 600, "total": 18000}
  },
  "cost_usd": 0.12,
  "title": "Fix auth token refresh",
  "summary": "Asked why refresh tokens expired early. Found the clock skew and fixed the signing window."
}
```

`title` and `summary` are present only when the conversation has been labelled (`omitempty`, no `version` bump); the v1 `chat dir -r` record carries the same two optional keys, and both pretty outputs add `title:` / `summary:` lines after `prompt:`.

The token fields have these meanings:

- `uncached_input`: input tokens that were not read from the provider cache.
- `cached_input`: input tokens that were read from the provider cache.
- `output`: output tokens from the model.
- `total`: the provider-reported total token count.

The `token_usage.total` object sums all recorded query usage. The `token_usage.recent` object contains the final model call.

The v2 format has one numeric cost field. It does not include the duplicate v1 cost strings, price fields, or top-level token fields.

### `chat continue` (bind a chat to the current directory)

`clai chat continue <chatID|index> [prompt...]`:

- loads an existing chat from disk by ID, or by index. The index counts the rows `clai chat list` shows by default (native, external and grouped rows, newest first). An external row is cloned into a native chat, and a group row selects its newest member. `chat delete <index>` uses the same numbering and refuses an external row with `ErrForeignRowSelected`.
- if extra tokens are present after an index selection, they are re-joined and treated as the optional `prompt`
- prints a **fast obfuscated preview** (not full transcript rendering), with the prompt as the last user message
- updates the directory-scoped binding for the current working directory to point at that chat
- if a prompt is present, sends it as `clai [-r] [-p <profile>] -dre query <prompt>` would. `internal/cli` injects the sender (`text.DirReplySender`) as `CommandDeps.SendPrompt`, since the chat package holds no querier

The primary purpose is selection/binding: choose which existing transcript should be used as reply context for `-dre` in the current directory.

Behavior on missing chat:

- If the requested chat cannot be resolved (no exact match, legacy ID, or hash-derived ID), `clai chat continue` now prints the following error message to stderr:

```
could not find chat with id: "<id>"
```

and then reverts into the `clai chat list` UI so the user may select another conversation interactively. This is a UX-friendly fallback. Real filesystem or listing errors (for example permissions/IO errors) are still propagated as fatal errors rather than treated as a not-found case.

#### Output format (preview)

The preview uses `internal/chat/obfuscated_print.go` and prints early messages in an obfuscated single-line form:

- `[#<nr> r: "<role>" l: 00042]: <msg-preview>`

Behavioral details:

- message length is **zero-padded** to 5 digits
- long messages are shortened with `...` and a “and N more runes” note
- the last messages (roughly the last six) are pretty-printed more fully

After binding the directory scope without a prompt, `chat continue` prints a notice that the chat is replyable with:

- `clai -dre query <prompt>`

### Profile “sticking” when continuing

In `internal/chat/handler.go:cont`:

- If the loaded chat already has `chat.Profile != ""`, it overrides the runtime handler config so continuation uses that profile.
- Otherwise, if a `--profile` was provided (wired into `UseProfile`), the handler stamps it into `chat.Profile` so future continuations persist it.
- A `-prp` run saves the remote target as typed, without a `#sha256=` pin. The wide chat list limits the Profile column to 20 characters. Longer targets show the prefix, an ellipsis, and the end of the target. `chat continue` passes the full target as `-prp` and a local name as `-p` (`text.profileArgs`).

### `chat summarize` (label existing conversations)

`clai chat summarize <window>` (alias `clai c s`) generates a title and summary for every native conversation updated inside the window that has none yet (`-force` regenerates), after a confirmation naming the count and a token estimate (`-y` skips it; required with `-n`). It is the only chat subcommand that talks to a model: the summarizer is a lazy constructor injected from `internal/cli` and invoked by this verb alone. Workers write conversation files through `SaveWithoutIndex`; the coordinator rewrites the index once. Details in `architecture/summaries.md`.

### `chat list` / inspect / edit / delete

`clai chat list` exists for discovering chats and doing transcript operations.

Implementation: `internal/chat/handler_list_chat.go`:

- `list()` reads every JSON file in `<convDir>`, unmarshals to `Chat`, sorts by `Created` desc.
- `listChats()` uses `utils.SelectFromTable` to show a selection table; each row shows `labelFor(title, firstUserMessage)` — the title when the chat is labelled, else the first user message.
- Table toggles, each re-deriving the in-memory view without a new discovery pass: `[d]irscoped convs` (offered when the working directory has bindings; native rows only, foreign rows never hidden by it) and `[f]oreign convs` (offered when a source reader produced rows; hides or shows every external conversation, groups collapse over the remaining members). Each label carries its state in words (`[d]irscoped convs: off|on`, `[f]oreign convs: shown|hidden`) and is bold and underlined while in the non-default position (colour on); both default to off/shown and last for the list session. The labels come in three tiers chosen per render so the prompt line fits the terminal: long (`[d]irscoped convs: off`, `[f]oreign convs: shown`), compact (`[d]ir: off`, `[f]oreign: shown`) and terse (`[d]:off`, `[f]:shown`). `listPromptTier` replicates the table's prompt composition (`select`, the toggles, `[p]rev`, `[n]ext`, `[b]ack`, `[q]uit`, `[/] filter`, a reserved `page 99/99`), measures it with the longest state word and a four-column typing margin, and takes the first tier that fits; an unknown width keeps the long tier. With both toggles the long tier needs 119 columns and the compact tier 101; below that the terse tier is used. Residual: the table library never measures or clears a wrapped prompt line (`selectNumbers` clears `amPrinted+1` lines), so below about 91 columns a paginated list still wraps the terse prompt and drifts one line per keypress — a pre-feature limitation (the one-toggle prompt with a page counter already measured 82) that a fix in the table package would close.

After selecting a chat, `actOnChat()` prints a details view and offers actions. The view is
rendered first. Going [b]ack, or into the edit or delete picker, clears it by the terminal rows it
took at the current width, counted rune by rune as a terminal wraps them (`utils.TerminalRows`), so
the lines above it stay; the other choices leave it on screen. The label lines (title, summary or
first message) are sanitized to one row first, so escape sequences and line breaks in stored text
cannot skew the count. Emoji widths are where terminals disagree: one that draws a ZWJ or
skin-tone sequence as one glyph uses fewer rows than counted, and the clear can reach a line
above; one that draws a VS16 emoji (❤️) two cells wide can use more, and leave part of the view.

- edit messages (via `$EDITOR`; images stay in place, see above). A message opened and saved
  unchanged is not written again; the newline an editor adds at the end and CRLF line ends do not
  count as changes, and a saved edit drops the newline the editor added. A tool-call turn, whose
  stored text is empty, opens with its calls (`Call.PrettyPrint`, inputs sorted by name; the picker
  shows them too unless the turn holds reasoning); changing that text is refused with a warning,
  since tool calls cannot be edited as text. A blank edit removes the message's text but keeps its
  images and tool calls; one that would leave the message empty is refused with a warning, since
  vendors reject empty messages. The warning points to `[d]elete`, or, for a tool result, to
  replacing its text, since deleting it would leave its call without a result.
- delete messages
- save as `globalScope.json`

No interactive chat session is started from this UI.

## “Previous query” capture and replay

A special chat file is used for the global reply context:

- `<clai-config>/conversations/globalScope.json`

Implemented in `internal/chat/reply.go`.

### SaveAsPreviousQuery

`SaveAsPreviousQuery(claiConfDir, msgs)` writes:

- always: `globalScope.json` with ID `globalScope`
- additionally (when `len(msgs) > 2`): saves a *new conversation file* derived from the first user message using `HashIDFromPrompt(firstUserMsg.Content)`

This preserves one-off queries and optionally promotes richer exchanges into normal conversations.
Pending images are stored once before any file is written, so every copy references the same
asset files.

The mirror is built by **explicit field copy**, so a field added to `Chat` must be copied there (and into the promotion copy) or it is lost on reply. `title`, `summary` and `summary_at` are mirrored. Because the mirror carries `ID: globalScope`, a plain `-re` **forks**: `SetupInitialChat` (`internal/text/conf.go`) adopts `Created`, `Messages`, `Queries` and — when its own chat has no summary — the three label fields from the mirror, generates a fresh id, and the finalizer writes a new conversation file next to the untouched parent. The fork therefore inherits its parent's label and the in-flight summarizer does not run for it. `-dre` continues the bound conversation in place from the full file and needs no adoption.

### LoadPrevQuery

Loads `globalScope.json` (printing a warning if absent).

## Directory-scoped replies

Directory-scoped bindings, the `version: 2` binding record (`abs_path` + timestamped `history`), and
conversation lookback are described in `architecture/dirscope.md`.

In short:

- `clai -dre query ...` replies using the conversation bound to CWD
- `clai chat continue ...` is a convenient way to bind CWD → an existing conversation
- each non-reply query also records the chat into the directory's history (see `dirscope.md`)

## No model interaction

The chat handler holds no querier and calls no vendor. Every subcommand
reads stored transcripts and writes conversation state: `continue` prints a
chat and binds CWD to it, `delete` unlinks it and sweeps its unreferenced assets,
`list`/`dir`/`dirv2` render.
Turns are taken with `clai -dre query ...` afterwards. The one exception is
`summarize`, which receives a lazily constructed summarizer by injection
(`CommandDeps.NewSummarizer`) and is the only verb that invokes it.

Consequently the chat tree needs no API key, no model config and no tool
setup, and registers only the flags it reads: `-r`, `-n` and `-p` (which
stamps the profile onto the continued conversation, steering later `-dre`
queries); `summarize` takes the window as its positional argument and adds its own `-force`, `-y`, `-workers`
and `-sm` on its level only. It used to construct a full text querier through
`text.SetupQuerier` and discard it, which made `clai c c 0` fail without a
vendor key and put the whole agent flag group on `clai c -h`.

## Caveats

- **Shell-driven**: each question/answer is a normal CLI invocation.
- **ID mismatches**: older conversations may use the legacy `IDFromPrompt` naming; lookup supports both.
- **JSON is user-editable**: transcripts live under `<clai-config>/conversations` and can be edited manually.
