# Plan: Add Hugging Face support (chat/text only for now)

## Goals

Add **Hugging Face chat/text** support by integrating Hugging Face’s OpenAI-compatible router, while:

- Reusing the existing `internal/text/generic.StreamCompleter` (no new streaming implementation)
- Keeping changes **testable** (no live HTTP in unit tests)
- Keeping changes **regression-safe** for existing vendors

**Out of scope for this plan (removed for now):**

- Photo generation (text-to-image)
- Video generation (text-to-video)

---

## Scan summary: repo conventions that matter

### Text/chat integration pattern

- Text vendors embed `internal/text/generic.StreamCompleter` and implement `Setup()` to map vendor config → `StreamCompleter` fields.
  - Examples: `internal/vendors/openai/gpt.go`, `internal/vendors/gemini/gemini.go`.
- `generic.StreamCompleter` assumes an **OpenAI-compatible SSE** endpoint:
  - request includes `stream: true`
  - response is `text/event-stream` with `data: ...` lines and `[DONE]`

This matches the HF router’s OpenAI compatibility.

---

## Hugging Face API to use (chat)

Use HF OpenAI-compatible router:

- **URL**: `https://router.huggingface.co/v1/chat/completions`
- **Auth**: `Authorization: Bearer <HF token>`
- **Token env var**: `HF_API_KEY`

Optional debugging env var:

- `DEBUG_HUGGINGFACE`

---

## Design: new package `internal/vendors/huggingface`

Create:

- `internal/vendors/huggingface/constants.go`
- `internal/vendors/huggingface/chat.go`
- `internal/vendors/huggingface/chat_test.go`

---

## Chat implementation (reuse `generic.StreamCompleter`)

### Type

Implement a vendor struct that embeds `generic.StreamCompleter` and only does config mapping:

```go
type HuggingFaceChat struct {
  generic.StreamCompleter

  Model       string  `json:"model"`
  MaxTokens   *int    `json:"max_tokens"`
  Temperature float64 `json:"temperature"`
  TopP        float64 `json:"top_p"`
  URL         string  `json:"url"`
}

const DefaultChatURL = "https://router.huggingface.co/v1/chat/completions"

var DefaultChat = HuggingFaceChat{
  Model:       "meta-llama/Meta-Llama-3.1-8B-Instruct", // placeholder; user overrides via --model
  Temperature: 1.0,
  TopP:        1.0,
  URL:         DefaultChatURL,
}
```

### Setup()

`Setup()` should:

1. Default `URL` to `DefaultChatURL` if empty.
2. Call `h.StreamCompleter.Setup("HF_API_KEY", h.URL, "DEBUG_HUGGINGFACE")`.
3. Map fields:
   - `h.StreamCompleter.Model = h.Model`
   - `h.StreamCompleter.MaxTokens = h.MaxTokens`
   - `h.StreamCompleter.Temperature = &h.Temperature`
   - `h.StreamCompleter.TopP = &h.TopP`
   - `toolChoice := "auto"; h.ToolChoice = &toolChoice`

### Tooling

If/when tools are enabled in text mode, keep parity with other vendors:

```go
func (h *HuggingFaceChat) RegisterTool(tool pub_models.LLMTool) {
  h.InternalRegisterTool(tool)
}
```

This relies on HF router supporting OpenAI tool calling; if it doesn’t for a chosen model/provider, the error will surface without breaking other vendors.

---

## Model selection & wiring (regression-safe)

### Prefix

Use a single explicit prefix:

- `hf:<model>`

Examples:

- `--model hf:Qwen/Qwen2.5-72B-Instruct`
- `--model hf:meta-llama/Meta-Llama-3.1-8B-Instruct`

### Wiring

In `internal/create_queriers.go`:

- Add import `internal/vendors/huggingface`
- In `selectTextQuerier`, very early (before all `strings.Contains(...)` checks), add:

```go
if strings.HasPrefix(conf.Model, "hf:") || strings.HasPrefix(conf.Model, "huggingface:") {
  found = true
  defaultCpy := huggingface.DefaultChat
  // prefer hf:, but accept huggingface:
  modelName := strings.TrimPrefix(conf.Model, "hf:")
  modelName = strings.TrimPrefix(modelName, "huggingface:")
  defaultCpy.Model = modelName
  qTmp, err := text.NewQuerier(ctx, conf, &defaultCpy)
  ...
}
```

This avoids accidental matching (e.g. model names containing `gpt`, etc.) and keeps the routing consistent with other explicit-prefix vendors (`novita:` / `ollama:`).

---

## Tests (TDD-first)

### Vendor setup mapping

Add `internal/vendors/huggingface/chat_test.go`:

- `TestSetup_ConfigMapping`
  - set `HF_API_KEY` to a non-empty value
  - call `Setup()`
  - assert embedded `StreamCompleter` fields match: `Model`, `MaxTokens`, `Temperature`, `TopP`, `URL`

- `TestSetup_DefaultURL`
  - leave `URL` empty
  - call `Setup()`
  - assert `StreamCompleter.URL == DefaultChatURL`

Note: these tests do not hit network.

### Selection wiring

Extend `internal/create_queriers_test.go`:

- `TestCreateTextQuerier_SelectsHuggingFaceOnPrefix`
  - model `hf:some/model`
  - assert it doesn’t error (with `HF_API_KEY` set)
  - and that the returned querier is non-nil

If you want a stronger assertion without depending on generic type formatting: call `CreateTextQuerier` with `ChatMode=false` and assert the returned value is not nil; then separately verify its underlying model config file is created with the HF defaults (optional).

---

## Implementation steps

1. Create `internal/vendors/huggingface/constants.go` with default URL and env keys.
2. Implement `internal/vendors/huggingface/chat.go` using embedded `generic.StreamCompleter`.
3. Add `internal/vendors/huggingface/chat_test.go` (mapping + default URL).
4. Wire `hf:` (and optional `huggingface:`) selection in `internal/create_queriers.go`.
5. Add/adjust `internal/create_queriers_test.go`.
6. Run `go test ./...`.

---

## Acceptance criteria

- `clai --model hf:<model>` works for chat/text via `https://router.huggingface.co/v2/chat/completions` with `HF_API_KEY`.
- No existing vendor selection or behavior regresses.
- Unit tests cover vendor setup mapping and selection wiring.
