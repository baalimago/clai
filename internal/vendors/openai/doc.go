// Package openai implements OpenAI vendor integrations (chat, photo, video).
//
// Streaming paths:
//   - Non-Codex models use Chat Completions (/v1/chat/completions).
//   - Codex-named models (model contains "codex", case-insensitive) use Responses (/v1/responses).
//
// Tool calling:
//   - Chat Completions are normalized via the generic stream completer.
//   - Responses streaming is parsed directly and emits the same normalized events.
//
// Usage accounting:
//   - Chat Completions uses the generic stream completer token usage.
//   - Responses sets usage from the responses stream metadata.
package openai
