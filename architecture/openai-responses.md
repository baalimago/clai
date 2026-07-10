# OpenAI Responses API

This note describes how clai talks to OpenAI for **text** generation, and specifically
how it chooses between the two OpenAI text APIs. It is a vendor-specific companion to
[streaming.md](./streaming.md), which covers the vendor-agnostic event contract.

## The two APIs

OpenAI exposes two text endpoints:

| API              | Endpoint               | Notes                                                                                                                            |
| ---------------- | ---------------------- | -------------------------------------------------------------------------------------------------------------------------------- |
| Responses        | `/v1/responses`        | Current API. Required by the `codex` family and by reasoning-oriented models. Sends the full conversation as an `input[]` union. |
| Chat Completions | `/v1/chat/completions` | Legacy API. Normalized through the generic stream completer.                                                                     |

**The Responses API is the default for OpenAI models on the canonical OpenAI host.**
Chat Completions is used as an explicit opt-out and as the conservative default for
custom proxy hosts (see below).

## Selection rules

All routing is decided by `selectOpenAIURL(model, currentURL)` in
`internal/vendors/openai/stream_selection.go`, which returns the concrete endpoint URL
and whether the Responses API was chosen.

1. **Codex is Responses-only.** For models whose name contains `codex`, the Responses
   API is always used; a `/chat/completions` URL is redirected to the responses endpoint
   and a bare host is normalized to `<host>/v1/responses`.
2. **Explicit endpoint path wins.** A path containing `/chat/completions` keeps Chat
   Completions; a path containing `/responses` uses the Responses API. This holds for any
   host, so a proxy can select either API by naming the endpoint.
3. **Host-gated default.** When the path names neither endpoint, the Responses API is the
   default **only for the canonical OpenAI host** (an empty URL or `api.openai.com`). Any
   other host — including a bare custom proxy host — keeps the legacy Chat Completions API
   and is normalized to `<host>/v1/chat/completions`.

The vendor default URL (`GptDefault.URL`) is `ResponsesURL`, so fresh installs persist a
responses endpoint and use it. Two properties make the rollout conservative
(see also `internal/vendors/openai/gpt.go`):

- Existing installs that persisted a `/chat/completions` URL stay on the legacy path.
- A persisted **custom proxy** URL (e.g. `https://litellm.corp/v1/openai`) is **not**
  silently switched to the Responses wire format — host-gating keeps it on Chat
  Completions unless it explicitly names a `/responses` path. Only `api.openai.com`
  (and empty) URLs get the new default.

## Feature parity

The responses streamer (`internal/vendors/openai/responses_stream.go`) mirrors the
generic (Chat Completions) path:

- **Text + tool calls** are parsed from the SSE `type`-discriminated events and emitted
  as the same normalized events (`string`, `pub_models.Call`, `StopEvent`, `NoopEvent`,
  `error`) the querier already consumes. Tool-call argument buffers are keyed per output
  item (`item_id`, falling back to `output_index`) by `toolCallTracker`, so **parallel
  function calls** whose argument deltas interleave on the wire are not mixed together.
  Requests explicitly set `parallel_tool_calls:true`. The session runner drains the
  complete model stream, groups every emitted call into one assistant turn, executes the
  tools sequentially, appends all outputs, and only then asks the model to continue.
  Sequential local execution avoids races between stateful CLI tools while preserving
  the Responses API's parallel-call protocol and ensuring final usage is captured.
- **Tool resolution matches every other vendor.** The emitted `Call`'s identity is the
  tool name streamed off the wire, and the model can only pick from the **profile-filtered
  set advertised to it** — `mapResponsesTools` maps the querier's registered `g.tools`,
  which is where profile/`-t` filtering is enforced. `emitCall` then looks the name up in
  the global `tools.Registry` **only to enrich** the `Function` spec (the sole downstream
  consumer on this path is `Function.Arguments`, set independently); this is exactly what
  the generic Chat Completions path does in `doToolsCall`, and it is **not** a profile
  gate. Internally-dispatched tools — the lookback trio
  (`search_conversations`/`inspect_conversation`/`read_message`) and `load_skill` — are
  dispatched by name (`Execute` → `executeLookbackTool`/`executeLoadSkill`) and are
  deliberately never in the global registry, so the lookup misses by design. Like Anthropic
  (which builds the `Call` straight from the wire and never consults a registry) and the
  generic path (which tolerates the miss), `emitCall` keeps the wire name instead of
  aborting; `Call.Patch` back-fills `Function.Name` from `Call.Name`. A genuinely
  unadvertised/hallucinated name then degrades to a recoverable `ERROR: unknown tool call`
  tool result the model can recover from, rather than killing the run. (The earlier
  Responses-only hard-fail here was the sole vendor divergence.)
- **Image input.** Message `ContentParts` carrying an image are mapped to the Responses
  `input_image` content type (`{type:"input_image", image_url:"<data-uri>"}`) — note the
  Responses `image_url` is a plain data-URL **string**, unlike the Chat Completions object
  form. Text parts map to `input_text`. This mirrors the vision support of the generic path.
- **Reasoning.** For reasoning models the request opts in with
  `reasoning:{summary:"auto", effort:<cfg>}` — the `summary` is what makes the API stream
  `response.reasoning_summary_text.delta`, which is surfaced as `models.ReasoningEvent`
  (rendered as `[thinking]…[/thinking]`). Effort comes from the `reasoning_effort` config
  (`ChatGPT.ReasoningEffort`, one of `minimal|low|medium|high`); empty omits `effort` and
  uses the API default. Non-reasoning models send no `reasoning` object. The same
  `reasoning_effort` is forwarded on the Chat Completions opt-out path (a top-level
  request field there) for reasoning models.
- **Structured output** is forwarded via `text.format` (`mapResponseFormat`). Unlike Chat
  Completions, the Responses API places the `name`/`schema`/`strict` fields directly on
  the format object rather than under `json_schema`.
- **Sampling.** `temperature`/`top_p` are forwarded only for non-reasoning models
  (reasoning models — gpt-5.x, o-series, codex — reject them; see `isReasoningModel`,
  which excludes the non-reasoning `gpt-5-chat-*` variant). `isReasoningModel` first
  normalizes the model ID (`normalizeModelID`) so provider-qualified (`openai/o3-mini`)
  and fine-tuned (`ft:o3-mini:org::id`) names classify correctly. `max_output_tokens` is
  forwarded when set. Frequency/presence penalties are not part of the Responses API. The
  same reasoning-model gate is applied on the **Chat Completions opt-out path** in
  `gpt.go`.
- **Reasoning continuity (stateless).** Reasoning models produce hidden reasoning items
  that precede their tool calls. Because clai is `store:false` (the server keeps nothing),
  continuity has to travel with the client, so for reasoning models the request adds
  `include:["reasoning.encrypted_content"]`. Each completed reasoning item
  (`response.output_item.done`, `type:"reasoning"`) is captured with its sealed
  `encrypted_content`, rides on the emitted `pub_models.Call`, and is
  stamped onto the persisted assistant turn. On the next turn they are replayed as
  `input[]` items of `type:"reasoning"` **immediately before** the turn's `function_call`,
  restoring the model's own chain-of-thought across a stateless tool loop. The opaque blobs
  are **never inlined** into the conversation JSON — they live in a per-chat sidecar (see
  [chat.md](./chat.md)). Replay is gated to reasoning models on the Responses path; other
  models and the Chat Completions path never receive these OpenAI-only items (they would
  otherwise be rejected). Same-model continuation is the supported case; replaying items
  produced by a *different* model may be rejected by the API and surfaces via the error
  handling below.
- **Store.** `store:false` is always sent. clai holds full conversation state client-side
  (it resends the whole `input[]` each turn and never uses `previous_response_id`), so it
  opts out of the Responses API's default server-side retention. Reasoning continuity is
  preserved without server state via the encrypted-reasoning replay above.
- **Usage** is captured from the `response.completed` **and** `response.incomplete`
  metadata (`mapUsage`). `response.incomplete` is the terminal event emitted when the
  output is truncated (e.g. `max_output_tokens` or content filter); it is handled exactly
  like `response.completed`.
- **Errors.** `response.failed` (actionable detail nested under `response.error`, with a
  top-level `error` fallback) and the terminal top-level `error` event (detail on
  `message`/`code`) are both surfaced as an `error` event so the consumer aborts, rather
  than ending on a silent EOF.

## Stream termination

`handleResponsesStreamEvent` returns a `done` flag. On a terminal event
(`response.completed`, `response.incomplete`, or a `[DONE]` frame mapped to it) the
reader returns immediately.
This prevents a server that emits both `response.completed` and a trailing `[DONE]` from
attempting a second `StopEvent` send after the consumer has already stopped reading
(which would otherwise block the producer goroutine, since the consumer cancels the
context on the first stop).

## Files

| File                                          | Purpose                                                                          |
| --------------------------------------------- | -------------------------------------------------------------------------------- |
| `internal/vendors/openai/stream_selection.go` | `selectOpenAIURL`, endpoint resolution, `isReasoningModel`/`isCodexModel`, `normalizeModelID` |
| `internal/vendors/openai/gpt.go`              | `ChatGPT` model: setup, tool mapping, dispatch to responses vs generic           |
| `internal/vendors/openai/responses_stream.go` | Responses request build + SSE parsing + event normalization + reasoning capture/replay |
| `internal/vendors/openai/responses_models.go` | Responses request/response wire types                                            |
| `internal/chat/reasoning_sidecar.go`          | Out-of-band persistence of reasoning items, keyed by persisted tool-call ID |
