# Query Command Architecture

Command: `clai [flags] query <text>` (aliases: `q`)

The **query** command is the primary way to send a one-shot text prompt to an LLM and receive a streamed response. It is the workhorse of clai.

## Entry Flow

```
main.go:run()
  → cmd.Run(...)                       # go_away_boilerplate/pkg/cmd dispatch
    → query command Setup (internal/text/cmd.go)
      → resolves the query flag groups into Configurations
      → setupTextQuerierWithConf()     # build the Querier
    → adapter Run → querier.Query(ctx) # execute the query
```

## Key Files

| File | Purpose |
|------|---------|
| `internal/text/cmd.go` | query command definition; dispatch via `cmd.Run` (see [cmd-dispatch.md](./cmd-dispatch.md)) |
| `internal/setup.go` | `setupTextQuerierWithConf()` builds the text querier |
| `internal/flags.go` | flag primitives + shared groups (`TextFlags` feeds `text.SetupQuerier`) |
| `internal/text/conf.go` | `text.Configurations` struct + `SetupInitialChat()` |
| `internal/text/querier_setup.go` | `NewQuerier()` — vendor routing, model config file creation |
| `internal/text/querier.go` | `Querier.Query()` — streaming loop, token handling, post-processing |
| `internal/text/querier_tool.go` | Tool call handling during query execution |
| `internal/utils/prompt.go` | `Prompt()` — stdin/args merging and `{}` replacement |
| `internal/text/create_querier.go` | `text.CreateQuerier()` — vendor selection by model name |
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

`text.CreateQuerier()` in `internal/text/create_querier.go` routes by model name substring:

| Pattern | Vendor |
|---------|--------|
| `hf:` / `huggingface:` prefix | HuggingFace |
| `or:` prefix | OpenRouter |
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

`Querier.Query()` in `internal/text/querier.go` delegates to the `sessionRunner`
loop (`internal/text/session_runner.go`). Each model step:

1. **StreamCompletions**: calls `Model.StreamCompletions(ctx, chat)` → returns `chan CompletionEvent`
2. **Event loop**: reads from channel, dispatching:
   - `string` → appends to `fullMsg`, prints to stdout (streaming output)
   - `pub_models.Call` → collected for the step's tool batch (see below)
   - `error` → propagated
   - `models.StopEvent` → cancels context
   - `models.NoopEvent` → ignored
   - `dimensions.Dimensions` (resize, rolling terminal sessions only) →
     refreshes the session snapshot, resizes the rolling viewport, and
     redraws it in one frame before further output
3. **Tool batch** (when the step produced tool calls): the stoploss controller
   preflights the whole batch against both run budgets before any tool runs
   (`internal/text/tool_executor.go`). Refused calls never reach their
   implementation; their tool result carries the escalation text.
4. **Stoploss check** (after the tool batch, so the chat order stays
    `[assistant tool-call] [tool results] [handover user msg]`): the latest
    request footprint (`prompt_tokens + completion_tokens`, else
    `total_tokens`, else the model's `InputTokenCounter` estimate) is compared
    to `stoploss.max-tokens`. On the first crossing clai appends the handover
    user message (`stoploss.max-tokens-handover-instructions`, default
    `DefaultHandoverInstructions`), prints a notice, and marks the session
    handover-requested. The handover starts the wrap-up phase: post-handover
    tool calls are counted against `stoploss.max-tool-calls-after-handover`
    (absent or 0 = unlimited, the default) with a fresh counter, so the agent
    can keep working — e.g. summarizing context into a file — until it
    produces a plain reply. Only when a configured wrap-up allowance is
    exhausted do later tool calls run the refusal ladder until the run ends
    cleanly (`io.EOF`). A step that ends with a plain reply ends the run
    without a handover — there is nothing to hand over.
5. **Post-processing** (`postProcess()`):
   - Appends assistant message to chat
   - Saves conversation via `SaveAsPreviousQuery()` (unless in chat mode)
   - Pretty-prints final output (via glow when the destination is a terminal, unless `-r`/`--raw`)

### Rate Limit Handling

If `StreamCompletions` returns `ErrRateLimit`, the querier sleeps until the reset time and retries (up to 3 times). If the model implements `InputTokenCounter`, it uses adaptive backoff.

## Tool Calls

When a model step returns one or more `pub_models.Call` events, the
`toolExecutor` (`internal/text/tool_executor.go`) processes them as one batch:

1. Every call is preflighted against the stoploss controller before any tool
    side effect runs. Within the phase budget (`max-tool-calls` before a
    handover, `stoploss.max-tool-calls-after-handover` after it) a call is
    allowed and its result carries the remaining-count prefix; over budget the
    call is refused and never invoked.
2. The transcript keeps immediate assistant→tool pairing in the model's
   emission order. `load_skill` self-emits its assistant tool-call at
   execution time (the trust prompt and load errors precede the call echo, and
   a failed load leaves no dangling call), so a batch containing `load_skill`
   is split into segments: consecutive non-skill calls share one grouped
   assistant turn, and each `load_skill` runs as its own pair in batch order.
   One tool result per call is emitted in original order — including
   refusals, whose result carries the escalation text. When a refusal reaches
   the final warning, the `io.EOF` that ends the run is deferred until every
   call of the batch has emitted its result, so the persisted transcript
   always pairs one tool result with every declared call.
3. `tools.Invoke(call)` looks the tool up in the registry and runs it, then
   `toolOutputRuneLimit` truncation is applied to the result.
4. The runner continues the loop with the updated chat (the model sees the
   tool output and continues).

Tool call limits (`max-tool-calls` in config) enforce a soft cap with
escalating warnings. `max-tool-calls: 0` (or nil) means **unlimited** — no
budget prefix, no warnings. After a stoploss handover the effective budget is
`stoploss.max-tool-calls-after-handover` with a fresh counter: absent or 0
means unlimited post-handover tool calls (the default), and a positive value
bounds the wrap-up phase, after which the same escalation ladder applies until
the run ends cleanly.

## Directory Scope Binding

After a successful non-reply query, `Setup()` in `internal/setup.go` updates the directory-scoped binding:

```go
chat.UpdateDirScopeFromCWD(claiConfDir, tConf.InitialChat.ID)
```

This allows subsequent `-dre` queries from the same directory to continue the conversation.

## Output Modes

- **Default (animated)**: tokens stream to stdout character-by-character, then the full message is pretty-printed (via `glow` when the destination is a terminal)
- **Raw (`-r`)**: tokens stream directly, no post-processing formatting
- **Structured (`-rf`/`-response-format`)**: blocks streaming and prints only the final structured response, followed by one newline. It suppresses reasoning, tool activity, skill-discovery logs, lookback notices, formatting, and BEL.
- **Cmd mode (`cmd` command)**: output is treated as a shell command; user is prompted to execute it
