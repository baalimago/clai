# Add OpenAI Responses API (default)

## Goal
Switch OpenAI text/tool streaming from **Chat Completions** (`POST /v1/chat/completions`) to **Responses** (`POST /v1/responses`) and make Responses the **default and only** OpenAI text streaming path.

Constraints / non-goals:
- **No structured output support** beyond plain text streaming (no JSON schema / strict formats).
- Keep the repo’s normalized streaming contract: `string`, `pub_models.Call`, `models.StopEvent`, `models.NoopEvent`, `error`.
- Implementation is **OpenAI-vendor-specific** (no new generic adapter).
- Assumption: **all OpenAI models used by clai support the Responses API**.
- No new third-party dependencies.

## Current state (what we’re replacing)
- OpenAI vendor: `internal/vendors/openai/gpt.go` embeds `generic.StreamCompleter`.
- Generic stream completer posts to `ChatURL` and parses Chat Completions SSE (`choices[].delta...`).

## High-level design
Implement Responses streaming **inside** `internal/vendors/openai`:
- Build a Responses request body from `pub_models.Chat`.
- Send `POST /v1/responses` with SSE enabled.
- Parse Responses SSE events and translate them into the repo’s normalized streaming events.

The rest of clai (querier/chat loop, tool execution, persistence, etc.) stays unchanged.

## Implementation steps (test-first)

### 1) Add OpenAI Responses endpoint constant
**File:** `internal/vendors/openai/constants.go`
- Add:
  - `ResponsesURL = "https://api.openai.com/v1/responses"`
- Update the OpenAI GPT default:
  - `GptDefault.URL = ResponsesURL`

Notes:
- Keep `ChatURL` only if it’s still used anywhere else; otherwise delete in a cleanup PR.

### 2) Add vendor-local Responses request/stream implementation
Add new files:
- `internal/vendors/openai/responses_stream.go`
- `internal/vendors/openai/responses_models.go`

#### 2.1) Request model
Create minimal request structs to support clai’s use-cases:
- `model` (string)
- `input` (array)
- `stream` (bool)
- `tools` (optional; function tools)
- `tool_choice` (optional)

Input mapping (text-only):
- For each `pub_models.Message{Role, Content string}` emit a Responses input message item:
  - `role: msg.Role`
  - `content: [{type:"input_text", text: msg.Content}]`

Notes:
- clai message content is string-only, so multimodal inputs (images/files) are out of scope.

#### 2.2) Tools mapping
Reuse existing tool registry/definitions as much as possible.

Behavior:
- If tools exist, send them and set `tool_choice` to `"auto"`.
- When the model requests a tool, emit a `pub_models.Call` as clai already expects.

### 3) Integrate into `internal/vendors/openai/gpt.go`
Modify `ChatGPT` so it no longer uses `generic.StreamCompleter`.

Approach:
- Keep `ChatGPT` as the OpenAI model type.
- Implement `func (g *ChatGPT) StreamCompletions(ctx context.Context, chat pub_models.Chat) (chan models.CompletionEvent, error)` directly in the OpenAI vendor, calling Responses.
- Keep `Setup()` responsible for loading env (`OPENAI_API_KEY`) and debug enablement.

Important:
- Preserve existing `RegisterTool(...)` behavior so tools still work.

### 4) Parse Responses SSE to normalized events
Implement SSE reading similar to existing logic:
- Read by line (`ReadBytes('\n')`), accept `data: ...` frames.
- Trim prefix `"data: "` and whitespace.
- If payload is `[DONE]`, emit `models.StopEvent{}`.

Implement a small event decoder:
- Unmarshal each JSON `data:` payload to a minimal “union” structure (fields needed for the subset we handle).

Tool-call assembly:
- Maintain state per in-flight tool call:
  - `currentCallID string`
  - `currentToolName string`
  - `argsBuf bytes.Buffer` (or string)

Emission rules:
- Text delta → emit `string`.
- Function name/call_id discovery → update state.
- Function arguments delta → append to `argsBuf`.
  - Try `json.Unmarshal(argsBuf.Bytes(), &pub_models.Input)`.
  - If success → emit `pub_models.Call{ID: callID, Name: toolName, Inputs: &input, Type: "function"}` and reset buffers.
  - If failure due to partial JSON → emit `models.NoopEvent{}`.
  - If failure due to other reasons → emit `error` **wrapped with context**.
- A terminal Responses event (if present in the stream) should emit `models.StopEvent{}` as well.

Error handling rules:
- All returned errors must be wrapped with context:
  - `fmt.Errorf("openai responses: create request: %w", err)`
  - `fmt.Errorf("openai responses: do request: %w", err)`
  - `fmt.Errorf("openai responses: parse stream event: %w", err)`
- For non-200 responses:
  - read body
  - return `fmt.Errorf("openai responses: unexpected status code %v, body: %s", res.Status, body)`

### 5) Tests (vendor-local, must be first)
Add:
- `internal/vendors/openai/responses_stream_test.go`

Use `httptest.Server` that:
- verifies request path is `/v1/responses`
- returns `Content-Type: text/event-stream`
- writes SSE frames

Test cases:

1) **Text streaming**
- Server sends multiple text delta events then `[DONE]`.
- Assert channel emits the expected `string` chunks in order and eventually `models.StopEvent{}`.

2) **Function call streaming**
- Server sends events that define a function call (`name`, `call_id`) and streams `arguments` in multiple chunks.
- Assert:
  - exactly one `pub_models.Call` is emitted
  - `Call.ID` and `Call.Name` match
  - `Call.Inputs` matches expected JSON

3) **Non-200 response**
- Server returns 400 with a body.
- Assert `StreamCompletions` returns an error that includes:
  - context prefix (`openai responses`)
  - status and body

4) **Malformed SSE JSON**
- Server sends a `data:` line with invalid JSON.
- Assert:
  - the stream emits an `error` event (or `StreamCompletions` returns an error)
  - the error is wrapped with context: `openai responses: parse stream event: ...`

Run:
- `go test ./... -timeout=30s`

### 6) Remove/stop using Chat Completions for OpenAI
Once tests pass and OpenAI vendor is switched:
- Ensure nothing in OpenAI vendor depends on `generic.StreamCompleter` anymore.
- Ensure docs reflect that `architecture/STREAMING.md` is no longer OpenAI-specific (it currently names Chat Completions in the title).

## Acceptance criteria
- OpenAI streaming uses `/v1/responses` by default for all OpenAI models.
- Existing commands (`query`, `chat`, tool follow-up turns) continue to work.
- Tool calls still function and are emitted as `pub_models.Call`.
- All new code has tests covering streaming, tool calls, non-200, and malformed SSE.
- Errors are always returned with contextual wrapping.

## Follow-ups (explicitly out of scope)
- Multimodal message inputs (`input_image`, `input_file`) require changing `pub_models.Message` to support typed content parts.
- Structured outputs / JSON schema response formats.
- Conversation persistence via Responses “conversation” objects.
