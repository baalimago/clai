// Package openai implements OpenAI vendor integrations (chat, photo, video).
//
// Streaming paths:
//   - The Responses API (/v1/responses) is the default on the canonical OpenAI host
//     (empty URL or api.openai.com).
//   - The legacy Chat Completions API (/v1/chat/completions) is used when the URL path
//     names "/chat/completions" (opt-out) and, conservatively, as the default for any
//     custom proxy host that does not explicitly name a "/responses" path (so persisted
//     proxy configs are not silently migrated to the Responses wire format).
//   - Codex-named models (model contains "codex", case-insensitive) are
//     Responses-only; a chat/completions URL is redirected to responses for them.
//
// See selectOpenAIURL in stream_selection.go for the exact resolution rules.
//
// Tool calling:
//   - Chat Completions are normalized via the generic stream completer.
//   - Responses streaming is parsed directly and emits the same normalized events.
//
// Image input:
//   - Both paths accept image ContentParts. Responses maps them to input_image content
//     (image_url is a plain data-URL string, unlike the Chat Completions object form).
//
// Structured output:
//   - Both paths honor the configured response format. Chat Completions sends
//     response_format; Responses sends the equivalent text.format payload.
//
// Sampling parameters:
//   - Responses forwards temperature/top_p only for non-reasoning models, since
//     reasoning models (gpt-5.x except gpt-5-chat, o-series, codex) reject them.
//     max_output_tokens is forwarded when set. frequency/presence penalties are not part
//     of the Responses API and are therefore only applied on the Chat Completions path.
//
// Reasoning:
//   - For reasoning models the Responses request opts in with reasoning.summary="auto"
//     (so reasoning summary deltas stream as [thinking]) and reasoning.effort from the
//     reasoning_effort config (minimal|low|medium|high; empty uses the API default). The
//     same reasoning_effort is forwarded on the Chat Completions path for reasoning models.
//   - isReasoningModel first normalizes the model id (normalizeModelID) so provider-qualified
//     ("openai/o3-mini") and fine-tuned ("ft:o3-mini:org::id") names classify correctly.
//
// Reasoning continuity:
//   - Reasoning models also request include=["reasoning.encrypted_content"]. Their sealed
//     reasoning items (id + encrypted_content) are captured from the stream, ride on the
//     emitted Call onto the assistant turn, and are replayed as type:"reasoning" input items
//     before the function_call on the next turn — preserving chain-of-thought across a
//     stateless (store=false) tool loop. The opaque items are stored out-of-band in a
//     per-chat sidecar (internal/chat/reasoning_sidecar.go), never inlined into the
//     conversation JSON, and are only ever sent to OpenAI reasoning models.
//
// Store:
//   - Responses always sends store=false; clai is stateless (resends full input[] each
//     turn), matching the Chat Completions privacy posture. Reasoning continuity is kept
//     client-side via the encrypted-reasoning replay above, not server-side state.
//
// Usage accounting:
//   - Chat Completions uses the generic stream completer token usage.
//   - Responses sets usage from the responses stream metadata.
package openai
