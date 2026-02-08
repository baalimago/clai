# Chat Completions Streaming Architecture

This document explains how **streaming** works in clai when calling LLM chat-completions APIs.

It is an extension of `architecture/QUERY.md`: QUERY describes **when** a streaming request is executed; this document describes **how the streamed response is represented, normalized, and consumed**, independent of vendor.

## Scope

- Applies to all commands that rely on `Model.StreamCompletions(...)` (e.g. `query`, `chat`, and any tool-driven follow-up turns).
- Covers the **generic vendor streaming layer**, which normalizes vendor-specific streaming payloads into a single stream of Go events.

## Key Idea: One Generic Event Stream

All vendors ultimately stream into the same consumer loop:

- A model implementation produces a stream of events.
- The querier/chat handler reads events and decides what to do:
  - print text to stdout as it arrives
  - detect tool/function calls
  - track usage / stop reasons
  - terminate on errors

In code, the contract is:

- `Model.StreamCompletions(ctx, chat) (chan completion.Event, error)` (exact types vary by package, but the pattern is consistent)
- The returned channel carries a **normalized** sequence of events.

### Normalized event types

Across vendors, clai reduces streaming to a small set of event shapes (as described in `architecture/QUERY.md`):

- `string` chunks: plain assistant text deltas
- `pub_models.Call`: a tool/function call request (name + JSON args)
- `models.StopEvent`: signals the model has finished this turn
- `models.NoopEvent`: keepalive / ignored
- `error`: any streaming/parsing/network error

The consumer reads until it sees a terminal condition (stop event, channel close, or an error).

## Files to Read

### Generic streaming (vendor-agnostic)

| File | Purpose |
|------|---------|
| `internal/text/querier.go` | The stream consumer loop: prints deltas, dispatches tool calls, handles stop conditions |
| `internal/text/generic/stream_completer.go` | Generic stream completer: takes vendor events and emits normalized events |
| `internal/text/generic/stream_completer_models.go` | Small model-related helpers/types used by the generic stream completer |
| `internal/text/generic/stream_completer_setup.go` | Wiring/config for building the stream completer |

### Vendor implementations (examples)

| Vendor | File(s) | Notes |
|--------|---------|------|
| Anthropic | `internal/vendors/anthropic/claude_stream.go`, `claude_stream_block_events.go` | Parses SSE/event-stream frames and turns them into blocks/deltas |
| OpenAI | `internal/vendors/openai/gpt.go` | Uses OpenAI-compatible streaming and maps deltas/tool calls into generic events |
| Others | `internal/vendors/*/*.go` | Each vendor maps its wire format into the same normalized events |

## Streaming Data Flow (End-to-End)

At a high level:

```
CLI (query/chat)
  → build InitialChat (messages + config)
  → Model.StreamCompletions(ctx, chat)
      → vendor HTTP request (stream=true)
      → vendor streaming parser (SSE/JSON lines/etc)
      → generic event normalization
      → chan<event> back to caller
  → querier/chat event loop consumes events
      → prints text as it arrives
      → on tool call: run tool + append messages + continue
      → on stop: finalize output + persist globalScope/chat
```

The important architectural point is that **the querier does not care about the vendor wire format**. It receives a single stream of normalized events.

## The Generic Vendor System (Works for Both)

clai supports:

1. **Vendor-specific model implementations** (OpenAI, Anthropic, Gemini, etc.)
2. A **generic streaming adapter** used to unify behavior across vendors

This generic layer is specifically designed so that streaming works the same way regardless of which underlying API is used:

- Text deltas become `string` events
- Tool/function calls become `pub_models.Call` events
- Vendor stop/finish signals become `models.StopEvent`
- Anything else becomes `models.NoopEvent` or an `error`

This means the rest of the app (query/chat/tool recursion) can be implemented once.

## Streaming Loop Responsibilities

The consumer loop (see `internal/text/querier.go`, and chat equivalents) is responsible for:

1. **Aggregating assistant text**
   - Each `string` delta is appended to a buffer (e.g. `fullMsg`)
   - Deltas are printed immediately for the interactive streaming experience

2. **Tool call detection and execution**
   - When a `pub_models.Call` is seen, the current assistant output is finalized
   - The tool call is appended to the chat
   - The tool is invoked via the registry (`internal/tools`)
   - Tool output is appended to the chat
   - The model is called again (recursive continuation) so it can incorporate the tool result

3. **Termination**
   - `models.StopEvent` ends the turn
   - Channel close ends the turn (depending on vendor)
   - Any `error` aborts the query with context

4. **Post-processing**
   - Append the assistant message to the chat
   - Save `globalScope.json` for reply mode
   - In non-raw mode, pretty-print the final output (glow, etc.)

## Vendor Streaming Differences (and How They Get Normalized)

Vendors differ in at least four common ways:

1. **Transport**: SSE (`text/event-stream`) vs JSON lines vs chunked JSON
2. **Delta shape**: content tokens, content blocks, role markers, partial JSON tool args
3. **Tool calls**:
   - some vendors stream function name first then args
   - others stream structured tool-call deltas
4. **Stop conditions**:
   - explicit finish_reason/stop_reason
   - an event like `[DONE]`
   - clean EOF

The streaming adapters convert these vendor-specific variations into the normalized event set so the querier can remain vendor-agnostic.

## Error Handling

Streaming can fail mid-response (network, vendor errors, invalid event frames). The architecture uses these rules:

- Vendor parser errors are surfaced as `error` events or as an error returned from `StreamCompletions`.
- The consumer loop stops immediately on error.
- Higher-level logic (as described in `QUERY.md`) may retry on rate limit errors.

## How This Relates to QUERY.md

- `QUERY.md` explains how a user prompt becomes a streaming model call and how the app manages configuration, tool calls, and persistence.
- This document explains the **streaming contract** that makes that possible across vendors.

If you are modifying streaming behavior, start from:

- `internal/text/querier.go` (consumer semantics)
- `internal/text/generic/stream_completer.go` (normalization rules)
- the vendor stream implementations (wire parsing)
