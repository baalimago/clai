# Phase 1 — Gemini CLI source

**Parent:** root · **Class:** contract · [README](README.md)

## Goal

Read Gemini CLI conversations from the append-only JSONL log and the legacy
JSON conversation document, without writing to the store.

## Interface

Written to `internal/vendors/gemini/source_reader.go`.

```go
// SourceReader discovers and reads Gemini CLI sessions.
//
// It reads the Gemini CLI store that F1 names. A session file is either an
// append-only JSONL log or a legacy JSON ConversationRecord. The reader never
// writes to the store. (F1, F2, F7)
type SourceReader struct {
	// Root overrides the Gemini home. It exists for tests and a non-default
	// install; production code leaves it empty.
	Root string
	// FS is the filesystem the reader opens. It exists for tests; production
	// code leaves it nil.
	FS fs.FS
}

// Source returns the stable source name "gemini". (Source.1, F10)
func (r SourceReader) Source() string

// Discover lists every main Gemini CLI conversation.
//
// - A missing store root yields no rows and no error. (Discover.1, F4, L1)
// - One row per main conversation file; a subagent file, a file without a
//   sessionId, and a file whose replay yields no conversation message yield
//   none. (Discover.2, F1, F2, F4, F10, L2, L3, L4, L5)
// - MessageCount equals len(the messages Read returns) minus the preamble.
//   (Discover.3, F5)
// - FirstUserMessage is the metadata summary when present, else the first
//   prompt, truncated to the preview width F9 defines; FullFirstUserMessage is
//   the full first prompt, else the preview. (Discover.4, F9)
// - Cwd is the trimmed content of <slug>/.project_root, else empty; Created is
//   startTime; Model is the last model recorded on a gemini message.
//   (Discover.5, F10, L1)
// - A file that cannot be replayed to its end is skipped and not cached; a
//   cache hit costs one stat and no open. (Discover.6, F6, F7)
func (r SourceReader) Discover(ctx context.Context, cache vendors.SourceCache) ([]vendors.SourceRow, error)

// Read returns one Gemini CLI conversation as a clai Chat.
//
// - Messages start with one system preamble; Source is "gemini" and SourceID
//   is the requested id. (Read.1, F5, F6, F7, F10)
// - Replay applies metadata records, message records keyed by id, $set
//   (including its messages replacement), $patch (content, tool results,
//   updates, removeIds, orderIds) in file order, and $rewindTo, which drops
//   the rewound record and every message after it. (Read.2, F2, L3)
// - A settled tool call with a result becomes a structured Call plus one
//   {role: "tool"} message; any other call becomes one
//   [tool call: name(args)] text line. (Read.3, F8, L5)
// - A user message whose trimmed text is empty or starts with /, ?,
//   <session_context> or <hook_context> is not a message. (Read.4, F9)
// - A legacy JSON document and a JSONL log use the same message mapping. A
//   file with no sessionId, a subagent session, an empty id, an unknown id and
//   a session with no conversation messages are errors. (Read.5, F1, F2, F4, L4)
func (r SourceReader) Read(ctx context.Context, cache vendors.SourceCache, sourceID string) (pub_models.Chat, error)
```

Consumes: `internal/vendors:SourceReader`, `internal/vendors:SourceRow`,
`internal/vendors:SourceCache`, `internal/vendors:OpenAbs`,
`internal/vendors:StatAbs`, `internal/vendors:ScanJSONLRecords`,
`internal/vendors:ReadMaxToken`, `internal/vendors:TruncateOneLine`,
`internal/vendors:HomeRelativeRoot` (import); `pkg/text/models:Chat`,
`pkg/text/models:Message`, `pkg/text/models:Call`,
`pkg/text/models:Specification` (import).

## Golden diff

None.

## Removes

None.

## Local facts

**L1 — Root.** `Root` overrides the root F1 names. The walk root is
`<root>/tmp`.

**L2 — Walk.** The walk visits files directly under a directory named `chats`
whose suffix is `.jsonl` or `.json`. It tolerates unreadable entries and stops
on context cancellation. `WalkJSONLFiles` fixes the `.jsonl` suffix, so this
reader owns its walk.

**L3 — Replay order.** `$rewindTo` first, then `$patch`, then a record with an
`id` and no `$patch`, then `$set`, then a partial metadata record. A rewound or
removed id that is absent clears the message list. `orderIds` moves the named
ids after every other id, in the given order.

**L4 — Mapping.** `info`, `error` and `warning` records map to no message. A
modern `gemini` record whose content holds a `functionCall` or `thought` part
uses that content as the source of truth; otherwise the legacy `thoughts` and
`toolCalls` metadata are used. Thought text becomes `ReasoningContent`.

**L5 — Tool pairing.** A call is paired when its result is non-null. The result
patch carries no status, so the result itself is the settlement signal. Result
text is the string, the joined text of a part array, or the
`functionResponse.response.output` of a function-response part; any other
value is JSON-encoded.

## Human required

None.

## Implementation notes

Implemented `SourceReader` under `internal/vendors/gemini/source_reader.go` and comprehensive unit tests under `internal/vendors/gemini/source_reader_test.go`. All local facts (L1–L5) and discovery/read contracts verified with test coverage at 84.1%.

## Review findings

R1-01 (filed in phase 2) also fixes this node: a reasoning-only assistant
record now maps to `[thinking] <reasoning>` instead of an empty message.
Test: `TestSourceReader_ReasoningOnlyAssistant`.
