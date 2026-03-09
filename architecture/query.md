# Query Command Architecture

Command: `clai [flags] query <text>` (aliases: `q`)

The **query** command is the primary way to send a one-shot text prompt to an LLM and receive a streamed response. It is the workhorse of clai.

## Entry Flow

```
main.go:run()
  → internal.Setup(ctx, usage, args)
    → parseFlags()           # extract CLI flags
    → getCmdFromArgs()       # returns QUERY mode
    → setupTextQuerier()     # build the Querier
  → querier.Query(ctx)      # execute the query
```

## Key Files

| File | Purpose |
|------|---------|
| `internal/setup.go` | `Setup()` dispatches to `setupTextQuerier()` for QUERY mode |
| `internal/setup_flags.go` | `parseFlags()` extracts all CLI flags into `Configurations` |
| `internal/text/conf.go` | `text.Configurations` struct + `SetupInitialChat()` |
| `internal/text/querier_setup.go` | `NewQuerier()` — vendor routing, model config file creation |
| `internal/text/querier.go` | `Querier.Query()` — streaming loop, token handling, post-processing |
| `internal/text/querier_tool.go` | Tool call handling during query execution |
| `internal/utils/prompt.go` | `Prompt()` — stdin/args merging and `{}` replacement |
| `internal/create_queriers.go` | `CreateTextQuerier()` — vendor selection by model name |
| `internal/chat/reply.go` | `SaveAsPreviousQuery()` — persists result for `-re` replies |
| `internal/chat/chat.go` | `HashIDFromPrompt()` — generates chat IDs |

## Configuration Cascade

The query command applies configuration in this order (lowest to highest precedence):

1. **Hard-coded defaults** (`text.Default` in `internal/text/conf.go`)
2. **`textConfig.json`** loaded from config dir
3. **Model-specific config** (e.g., `openai_gpt_gpt-5.2.json`)
4. **Profile overrides** (if `-p`/`-profile` or `-prp`/`-profile-path` is set)
5. **CLI flags** (e.g., `-cm`, `-r`, `-t`)

See `CONFIG.md` for full details.

## Prompt Assembly

`text.Configurations.SetupInitialChat(args)` in `internal/text/conf.go`:

1. If **not reply mode**: creates initial chat with system prompt message
2. If **glob mode** (`-g` flag): reads matching files into messages via `glob.CreateChat()`
3. If **reply mode** (`-re`): loads `globalScope.json` and prepends those messages
4. Calls `utils.Prompt(stdinReplace, args)` to build the user prompt from CLI args + stdin
5. Runs `chat.PromptToImageMessage(prompt)` to detect and extract base64-encoded images
6. Appends the user message to `InitialChat.Messages`
7. Generates chat ID via `HashIDFromPrompt(prompt)`

### Stdin Handling

`utils.Prompt()` in `internal/utils/prompt.go`:

- If pipe detected and no args: stdin becomes the prompt
- If pipe detected and args present: replaces `{}` (or custom `-I` token) in args with stdin content
- If no pipe: joins args as the prompt

## Vendor Routing

`CreateTextQuerier()` in `internal/create_queriers.go` routes by model name substring:

| Pattern | Vendor |
|---------|--------|
| `hf:` / `huggingface:` prefix | HuggingFace |
| contains `claude` | Anthropic |
| contains `gpt` | OpenAI |
| contains `deepseek` | DeepSeek |
| contains `mercury` | Inception |
| contains `grok` | xAI |
| contains `mistral`/`mixtral`/`codestral`/`devstral` | Mistral |
| contains `gemini` | Google |
| `ollama:` prefix | Ollama |
| `novita:` prefix | Novita |

Each vendor has a default config struct (e.g., `openai.GptDefault`). A model-specific JSON config file is created/loaded at `<configDir>/<vendor>_<model>_<version>.json`.

## Query Execution

`Querier.Query()` in `internal/text/querier.go`:

1. **Token warning**: estimates token count; prompts user if above `tokenWarnLimit`
2. **StreamCompletions**: calls `Model.StreamCompletions(ctx, chat)` → returns `chan CompletionEvent`
3. **Event loop**: reads from channel, dispatching:
   - `string` → appends to `fullMsg`, prints to stdout (streaming output)
   - `pub_models.Call` → tool call handling (see below)
   - `error` → propagated
   - `models.StopEvent` → cancels context
   - `models.NoopEvent` → ignored
4. **Post-processing** (`postProcess()`):
   - Appends assistant message to chat
   - Saves conversation via `SaveAsPreviousQuery()` (unless in chat mode)
   - Pretty-prints final output (via glow if available, unless `-r`/`--raw`)

### Rate Limit Handling

If `StreamCompletions` returns `ErrRateLimit`, the querier sleeps until the reset time and retries (up to 3 times). If the model implements `InputTokenCounter`, it uses adaptive backoff.

## Tool Calls

When the LLM returns a `pub_models.Call` event:

1. `handleToolCall()` in `internal/text/querier_tool.go`
2. Calls `doToolCallLogic()`:
   - Post-processes current output
   - Patches the call for vendor compatibility
   - Appends assistant tool-call message to chat
   - Invokes `tools.Invoke(call)` → looks up tool in registry, calls it
   - Applies `toolOutputRuneLimit` truncation
   - Appends tool output message to chat
3. Recursively calls `TextQuery()` with updated chat (model sees tool output and continues)

Tool call limits (`max-tool-calls` in config) enforce a soft cap with escalating warnings.

## Directory Scope Binding

After a successful non-reply query, `Setup()` in `internal/setup.go` updates the directory-scoped binding:

```go
chat.UpdateDirScopeFromCWD(claiConfDir, tConf.InitialChat.ID)
```

This allows subsequent `-dre` queries from the same directory to continue the conversation.

## Output Modes

- **Default (animated)**: tokens stream to stdout character-by-character, then the full message is pretty-printed (via `glow` if installed)
- **Raw (`-r`)**: tokens stream directly, no post-processing formatting
- **Cmd mode (`cmd` command)**: output is treated as a shell command; user is prompted to execute it
