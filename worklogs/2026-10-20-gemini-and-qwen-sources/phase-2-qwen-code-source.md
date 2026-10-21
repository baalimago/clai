# Phase 2 — Qwen Code source

**Parent:** root · **Class:** contract · [README](README.md)

## Goal

Read Qwen Code conversations from the append-only record chain, without writing
to the store.

## Interface

Written to `internal/vendors/qwen/source_reader.go`.

```go
// SourceReader discovers and reads Qwen Code sessions.
//
// It reads the Qwen Code store that F1 names: one record log per session. The
// active transcript is one parentUuid chain. The reader never writes to the
// store. (F1, F3, F7)
type SourceReader struct {
	// Root overrides the runtime base. It exists for tests and a non-default
	// install; production code leaves it empty.
	Root string
	// FS is the filesystem the reader opens. It exists for tests; production
	// code leaves it nil.
	FS fs.FS
}

// Source returns the stable source name "qwen". (Source.1, F10)
func (r SourceReader) Source() string

// Discover lists every main Qwen Code conversation.
//
// - A missing store root yields no rows and no error. (Discover.1, F4, L1)
// - One row per file with a sessionId and at least one conversation message;
//   an empty file and a file whose chain has no conversation message yield
//   none. (Discover.2, F1, F3, F4, F10, L2, L3, L4, L5)
// - MessageCount equals len(the messages Read returns) minus the preamble.
//   (Discover.3, F5)
// - FirstUserMessage is the last custom_title payload when present, else the
//   first prompt, truncated to the preview width F9 defines;
//   FullFirstUserMessage is the full first prompt, else the preview.
//   (Discover.4, F9)
// - Cwd is the first record's cwd; Created is the first record's timestamp,
//   else the file modification time; Model is the last assistant model.
//   (Discover.5, F10, L1)
// - A file that cannot be read to its end is skipped and not cached; a cache
//   hit costs one stat and no open. (Discover.6, F6, F7)
func (r SourceReader) Discover(ctx context.Context, cache vendors.SourceCache) ([]vendors.SourceRow, error)

// Read returns one Qwen Code conversation as a clai Chat.
//
// - Messages start with one system preamble; Source is "qwen" and SourceID is
//   the requested id. (Read.1, F5, F6, F7, F10)
// - The conversation is the parentUuid chain from the last conversation record
//   to the root, with records that share a uuid aggregated in file order.
//   Artifact records, non-conversation system subtypes and sidechain records
//   never join the chain; other system records join it and map to no message.
//   (Read.2, F3, L2, L3, L5)
// - A call with a tool_result record becomes a structured Call plus one
//   {role: "tool"} message; any other call becomes one
//   [tool call: name(args)] text line. (Read.3, F8, L4)
// - A user record's text is systemPayload.displayText when the payload pairs
//   it with hook context, else its text parts without the prompt-submit
//   context part; agent_mention and agent_message records are not prompts.
//   (Read.4, F9, L4)
// - An empty id, an unknown id, a file with mixed session ids, and a chain with
//   no conversation message are errors. (Read.5, F1, F3, F4, L2)
func (r SourceReader) Read(ctx context.Context, cache vendors.SourceCache, sourceID string) (pub_models.Chat, error)
```

Consumes: `internal/vendors:SourceReader`, `internal/vendors:SourceRow`,
`internal/vendors:SourceCache`, `internal/vendors:OpenAbs`,
`internal/vendors:StatAbs`, `internal/vendors:ScanJSONLRecords`,
`internal/vendors:ReadMaxToken`, `internal/vendors:WalkJSONLFiles`,
`internal/vendors:TruncateOneLine`, `internal/vendors:HomeRelativeRoot`
(import); `pkg/text/models:Chat`, `pkg/text/models:Message`,
`pkg/text/models:Call`, `pkg/text/models:Specification` (import).

## Golden diff

None.

## Removes

None.

## Local facts

**L1 — Root.** `Root` overrides the runtime base F1 names. The walk root is
`<root>/projects`.

**L2 — Conversation records.** A record with `type` `user`, `assistant` or
`tool_result` is a conversation record. A `system` record is one too, except
the artifact subtypes `session_artifact_event` and `session_artifact_snapshot`
and the non-conversation subtypes `session_sources_snapshot`,
`managed_session_header_v1`, `managed_session_event_v1` and
`managed_session_commit_v1`. A record with `isSidechain` true is excluded.
Invalid identity fields, unknown types and mixed session ids drop the file.

**L3 — Chain.** The leaf is the last chain tip whose chain holds a `user`,
`assistant` or `tool_result` record: a trailing root `system` record must not
hide the conversation behind it. Walk `parentUuid` with a uuid lookup; a
missing parent ends the chain, and a cycle ends it at the repeated uuid.
Reverse the chain. Aggregate fragments that share a uuid by appending their
parts and keeping the first record's type, the last timestamp and the first
non-empty model and tool result.

**L4 — Mapping.** A `system` record maps to no message. An `assistant` part
with `thought: true` becomes `ReasoningContent`; a `functionCall` part becomes
a call; text becomes content. A `tool_result` record's `functionResponse`
parts become `{role: "tool"}` messages. `chat_compression` is ignored, so the
pre-compression history in the chain stays visible.

**L5 — Compression.** The reader never honours `chat_compression`; the chain
already contains the original messages before the compression record.

## Human required

None.

## Implementation notes

Implemented `SourceReader` under `internal/vendors/qwen/source_reader.go` and comprehensive unit tests under `internal/vendors/qwen/source_reader_test.go`. All local facts and discovery/read contracts verified with test coverage at 84.9%.

| Run   | Commit      | Command                        | Result           |
| ----- | ----------- | ------------------------------ | ---------------- |
| start | 4036e11+wip | make qa && dupl -t 80         | all gates passed |
| end   | 4036e11+wip | make qa && dupl -t 80         | all gates passed |

## Review findings

| ID    | Severity | Node | Owner | Status     | Summary                                                                                                                                                                                                                                                                                                       |
| ----- | -------- | ---- | ----- | ---------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| R1-01 | major    | 1, 2 | fix   | Closed wip | A record whose only part is `thought:true` produced `Content:""` with no tool calls, which the shared `MapAssistantBlocks` and `cursor` rewrite to `[thinking] <reasoning>`. Both readers now do the same. Tests: `TestQwenSourceReader_ReasoningOnlyAssistant`, `TestSourceReader_ReasoningOnlyAssistant` (F8, L4) |
| R1-02 | major    | 2    | fix   | Closed wip | The leaf was the last conversation record, so a trailing root `system` record emptied the chain and hid the session from the list. The leaf is now the last chain tip whose chain holds a conversation record. Test: `TestQwenSourceReader_TrailingRootSystemRecord`; L3 amended (F3)                      |
| R1-03 | minor    | 2    | fix   | Closed wip | The walk opened every subagent transcript under `<project>/subagents`, which is never a user conversation. The walk now skips that directory. Test: `TestQwenSourceReader_SubagentDirSkipped` (F3, Read.2)                                                                                                  |
