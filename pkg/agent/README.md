# pkg/agent

`pkg/agent` embeds the clai agent loop in a Go program. A clai agent is a control loop that calls a model and executes the tools the model asks for, until the model replies without a tool call. Two agents differ by three things: the system prompt, the model, and the tools they can reach.

The package runs silent: `Setup` sends the terminal stream to `io.Discard`, so the caller owns all output. Use `WithLogger` to receive one `log/slog` record per completed message.

## Typed answers

`NewTyped[T]` returns a parsed Go value instead of a chat. The agent sets `json_object` response format by default and unmarshals the assistant message into `T`.

```go
type Review struct {
	Verdict string `json:"verdict"`
	Score   int    `json:"score"`
}

querier := agent.NewTyped[Review](
	agent.WithModel("gpt-5.2"),
	agent.WithPrompt("You review pull requests."),
)
if err := querier.Setup(ctx); err != nil {
	return err
}
review, err := querier.Query(ctx, models.Chat{
	Created:  time.Now(),
	ID:       "review-1",
	Messages: []models.Message{{Role: "user", Content: page}},
})
```

`NewTypedMetadata[T]` is the same call plus an `agent.Metadata` result carrying token usage, the chat ID, the conversation path, the model, and the per-call cost breakdown. Metadata is built right after a billed call, so a parse failure still reports the usage it caused.

The full runnable version of this snippet lives in [`example_test.go`](./example_test.go). It runs against the built-in mock vendor on every `go test`, so it needs no API key and no network.

## Options

| Option | Effect |
|---|---|
| `WithModel(model string)` | Selects the model. Any vendor the [CLI supports](../../README.md#supported-vendors) works, including the `or:`, `berget:`, `hf:` and `ollama:` prefixes. |
| `WithPrompt(prompt string)` | Sets the system prompt. |
| `WithConfigDir(dir string)` | Sets the config directory. Appends `clai` unless the path already ends with it. Defaults to `~/.config/clai`. |
| `WithTools(tools []models.LLMTool)` | Registers extra tools for the run. |
| `WithToolGlobs(globs ...string)` | Selects tools by pattern, for example `mcp_*` or `cat`. |
| `WithMcpServers(servers []models.McpServer)` | Starts MCP servers. An explicit server that fails to start fails `Setup`. |
| `WithCmdBanList(entries ...string)` | Refuses matching commands before spawn. The refusal names the matched entry. |
| `WithStoploss(s Stoploss)` | Caps token spend per run. A zero-value `Stoploss` disables the cap. |
| `WithMaxToolCalls(n int)` | Caps tool calls per run. `0` means no limit. |
| `WithLogger(l *slog.Logger)` | Receives one record per completed message. |
| `WithSlogLevel(level slog.Level)` | Sets the single level for those records. Default `slog.LevelDebug`. |
| `WithSlogRuneLimit(n int)` | Caps each logged text to `n` runes. Default 200; `<= 0` disables the cap. |
| `WithUsageRecorder(rec models.CallUsageRecorder)` | Receives one `CompletedModelCall` per model step. |
| `WithToolCallRecorder(rec models.ToolCallRecorder)` | Receives one `ToolCall` per tool invocation. |
| `WithResponseFormat(rf models.ResponseFormat)` | Overrides the response format that `NewTyped` sets. |

## Safety

`WithCmdBanList` and `WithStoploss` are the two options that bound what a run can do.

The ban list applies to the freetext command tools (`cmd`, `async_cmd`). A refused command never reaches a subprocess, and the refusal text names the entry that matched, so the model can see why it was stopped. Entries are whitespace-split tokens matched against the command string; they are not shell glob patterns. Two agents in one process carry separate policies, so concurrent runs do not interfere.

`Stoploss` is spend control. When a run crosses `MaxTokens`, the agent injects `MaxTokensHandoverMsg` into the chat and lets the model wrap up. `MaxToolCallsAfterHandover` bounds the tool calls in that wrap-up phase; `<= 0` means unlimited.

## Errors

`Setup` and `Query` return errors, and provider failures arrive as typed errors from [`pkg/claierr`](../claierr). Match them with `errors.Is` or `errors.As`:

```go
if errors.Is(err, claierr.ErrRateLimited) {
	// back off
}
```

The sentinel vocabulary covers authentication failure, likely insufficient credits, model not found, rate limiting, provider unavailability, transport failure, unexpected provider response, context length exceeded, content filtering, and MCP server startup.

## Key files

| File | Role |
|---|---|
| [`agent.go`](./agent.go) | `Agent`, `Option`, and every `With*` constructor. |
| [`run.go`](./run.go) | `Run` and `Query`, the two run entry points. |
| [`typed.go`](./typed.go) | `NewTyped`, `NewTypedMetadata`, and the JSON extraction they share. |
| [`recorder.go`](./recorder.go) | Aliases for the recorder types, so one import covers the whole surface. |

Design notes for the loop itself live in [`architecture/tooling.md`](../../architecture/tooling.md) and [`architecture/tooling-async.md`](../../architecture/tooling-async.md).