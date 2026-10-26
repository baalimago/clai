# Query Command Architecture

Command: `clai [flags] query <text>` (aliases: `q`)

The **query** command is the primary way to send a one-shot text prompt to an LLM and receive a streamed response. It is the workhorse of clai.

## Entry Flow

```
internal/cli:Run()
  → cmd.Run(...)                       # go_away_boilerplate/pkg/cmd dispatch
    → query command Setup (internal/text/cmd.go)
      → resolves the query flag groups into Configurations
      → setupTextQuerierWithConf()     # build the Querier
    → adapter Run → querier.Query(ctx) # execute the query
```

## Key Files

| File | Purpose |
|------|---------|
| `internal/text/cmd.go` | query command definition and its `-l` loop-mode wiring; dispatch via `cmd.Run` (see [cmd-dispatch.md](./cmd-dispatch.md)) |
| `internal/text/loop.go` | `LoopRunner` — per-iteration context isolation, handover chats, iteration bound |
| `internal/text/loop_display.go` | loop progress title and the per-iteration rolling-window rule |
| `internal/text/loop_tool.go` | `complete_task` tool: submission validation and the `-check` verification gate |
| `internal/setup.go` | `setupTextQuerierWithConf()` builds the text querier |
| `internal/flags.go` | flag primitives + shared groups (`TextFlags` feeds `text.SetupQuerier`) |
| `internal/text/conf.go` | `text.Configurations` struct + `SetupInitialChat()` |
| `internal/text/querier_setup.go` | `NewQuerier()` — vendor routing, model config file creation |
| `internal/text/querier.go` | `Querier.Query()` — streaming loop, token handling, post-processing |
| `internal/text/querier_tool.go` | Tool call handling during query execution |
| `internal/utils/prompt.go` | `Prompt()` — stdin/args merging and `{}` replacement; `PromptFrom()` takes stdin as a reader |
| `internal/imageinput/collect.go` | `ImageCollector` — `-img`, `-g` and stdin into pending images, glob messages and the prompt |
| `internal/text/query_images.go` | the query command's `ImageTurn`: asset store, normalizer, presenter, persister and `generate_image` setup (see [images.md](./images.md)) |
| `internal/text/create_querier.go` | `text.CreateQuerier()` — vendor selection by model name |
| `internal/chat/reply.go` | `SaveAsPreviousQuery()` — persists result for `-re` replies |
| `internal/chat/chat.go` | `HashIDFromPrompt()` — generates chat IDs |

## Loop Mode

Flag: `-l` / `-loop` on `query`

`clai -l q <text>` runs the same config, prompt and summarizer composition
as a plain query and then hands the resulting querier to `text.LoopRunner`.
One run owns multiple `ChatQuerier.TextQuery` iterations. Each iteration is a
fresh chat session equipped with the `complete_task` tool; an iteration that
ends without an accepted completion passes its final assistant reply to the
next iteration as handover.

The flag may be written before or after the command, because the dispatcher
forwards a flag to the level that defines it. Prepending `-l` therefore lifts
an existing prompt or shell alias into a loop without changing anything else.

| Flag | Purpose |
| --- | --- |
| `-l` / `-loop` | Enable loop mode for this query. |
| `-mi` / `-max-iterations` | Iteration bound. Default 10; `0` means unlimited; a negative value is rejected before the first model call. Requires `-l`. |
| `-check` | Shell command run through `sh -c` when `complete_task` reports `achieved`. A non-zero exit rejects the submission and the loop continues. Requires `-l`. |

A loop-only flag without `-l` fails setup with a `requires -l` error, so a
flag that would configure nothing cannot pass silently.

The loop exits 0 only after `complete_task` reports `achieved`; when `-check`
is set, the verification command must also exit 0. A `blocked` submission
exits 1 with the reported summary; an exhausted bound exits 1 with
`-max-iterations`. Every iteration after the first keeps the seed chat through
its first `user` message, so all persisted iterations share one `GroupKey` in
`clai chat list`. Every iteration also inherits the seed chat's profile, so a
loop run with `-p` persists the same profile on each iteration. A `StopEvent`
inside one iteration cancels only that iteration's child context, never the
outer loop.

Flags are the shared `TextFlags` groups plus `LoopFlags`; loop settings live
in no configuration file. See `internal/flags.go`, `internal/text/loop.go`
and `internal/text/cmd.go`.

The rolling window pins a one-line progress header in loop mode:
`▸ loop 2/10 - what remains`. The marker and the phase share the row but not
the colour: the marker uses the `loopTitle` accent and the phase the muted
activity colour. The goal is not shown: it is the same on every iteration and
would repeat on every header. Iteration 1 has no handover, so its summary is
the plan derived from the initial prompt (`SummaryKindLoopPlan`). Later
iterations start with the previous handover's progression: the first and last
non-empty handover lines, bounded to 120 runes. While the summarizer runs, the
row names that work: `▸ loop 2/10 - summarizing…`. When the summary lands, the
row is rewritten in place; the summary has 120 runes instead of the chat-list
title's 60. The placeholder appears exactly when a summarizer launches for that
iteration, decided once the iteration's chat is known: a resumed seed chat that
already carries a summary launches none and shows no placeholder. Every
iteration waits for its summary under the same bound the
finalizer's join uses before it freezes the row, so a slow summarizer still
reaches the header. A summary that fails or does not land leaves the fallback
phase, or the bare iteration marker for iteration 1. The header wraps over at
most four rows and marks a clip with an ellipsis.

Each iteration keeps its header in the scrollback. At stream end the window
collapses to its header, and the next iteration starts a fresh frame below
it; rows never overwrite an earlier iteration's progress record. The
collapsed header is in place before any other output: the settle runs before
the finalizer's index and cost lines, and on the error and cancellation paths
as well. A write that lands behind a drawn frame would move the cursor below
the viewport's bookkeeping and strand rows at the next clear; a notice that
must reach the user goes above the frame instead
(`ActivityViewport.PrintAbove`). Intermediate iterations never print an
answer: the header stays as the compact progress record. The iteration that
achieves the goal prints its answer below the header; `blocked` and exhausted
runs leave the header and report through the error instead. Raw, structured,
debug and redirected output keep the per-iteration prints they had before, and
show no header. See `internal/text/loop_display.go`.

When an iteration ends, its collapsed header gains a second row listing the
iteration's tool calls in order, with consecutive repeats folded and failed
calls (an `ERROR: ` result) marked: `↳ go ✗ · cat ×2 · apply_patch · go`.

Every loop run that started at least one iteration ends with a summary,
whatever stopped it (achieved, blocked, exhausted, or stopped by an error or
cancellation): iterations used, wall time, tokens in/out (with cached input),
the estimated cost when a price is known, and tool calls per tool with the
failure count. Only what each iteration added is counted, so a resumed seed
chat's earlier cost is not. Token counts use SI prefixes with three
significant digits (`41.2k in (12k cached) · 3.12k out`). A terminal session
prints it below the loop's output; raw, structured and redirected runs print
it to stderr so stdout stays machine-readable. See
`internal/text/loop_summary.go`.

A plain query prints the same summary, titled `▸ query done in 4s` (or
`stopped` when it ended on an error), below its answer when opted in with
`-qs`/`-query-summary` or `query-summary: true` in `textConfig.json`. It is
off by default. Loop iterations never print it, since the loop reports for
the whole run. See `internal/text/run_summary.go`.

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
2. Collects the inputs with `imageinput.ImageCollector.Collect` (see
   [Image input collection](#image-input-collection)): the `-img` images, the `-g` matches and
   stdin, plus the prompt text that `utils.Prompt` would build from the args and stdin
3. If **glob mode** (`-g` flag): the collected text-file messages are framed by
   `glob.FramedChat` between the glob system message and the `#####` user message, exactly as
   before; matches that are images go to the prompt message instead
4. If **reply mode** (`-re`): loads `globalScope.json` through `chat.Persister.Load` and prepends
   those messages; a stored image stays a ref and a legacy inline image arrives as a pending part
5. Appends the user message: `Content` with the prompt when no image was collected, otherwise
   `Parts` holding the images in collection order followed by the prompt text
6. Generates the chat ID

### Stdin Handling

`utils.Prompt()` in `internal/utils/prompt.go`:

- If pipe detected and no args: stdin becomes the prompt
- If pipe detected and args present: replaces `{}` (or custom `-I` token) in args with stdin content
- If no pipe: joins args as the prompt
- With no args and no pipe it fails with `utils.ErrNoPrompt`

`Prompt()` decides whether stdin is piped and delegates to `PromptFrom(stdinReplace, args, stdin)`,
where a nil reader means a terminal. Stdin that is wholly an image is attached instead (see below).

### Image flags

| Flag | Purpose |
| --- | --- |
| `-img` / `-image <path\|glob>` | Attach images; repeatable, in order. |
| `-open` | Open each generated image; same as `photoConfig.json` `auto-open-images`. |
| `-pm` / `-photo-model <model>` | The model behind `generate_image` for this run; overrides `photoConfig.json` `model` and the chat vendor's default. |

`-pm` belongs to the agent group's media-tool flags beside `-am`/`-af`, so `query`
parses it in both plain and loop mode. `-t generate_image` is a known selection: it
never warns or disables tools, and the
turn registers the tool itself. `-t '*'` selects `view_image` and `generate_image` with every
other tool. See [images.md](./images.md) for the whole image path.

### Image input collection

`imageinput.ImageCollector.Collect` (`internal/imageinput/collect.go`) gathers a command's inputs
into pending user images (`pub_models.ImagePart`, not yet stored) plus the prompt text:

- **`-img` values**, in flag order: `~` is expanded, then the glob, and matches are taken in lexical
  order. The MIME type is sniffed from the bytes, never taken from the extension. A value that matches
  nothing fails with `*imageinput.NoMatchError`, a non-image with `asset.ErrNotImage`, and a file over
  20 MiB with `*asset.ImageTooLargeError` before it is read (`asset.ReadLocalImage`).
- **`-g` matches** that sniff as images become images, and an image over 20 MiB fails with
  `*asset.ImageTooLargeError` once read. Every other match, whatever its size, becomes the same file
  message as before (`glob.FileMessage`), without the system and `#####` framing.
- **Stdin** that is an image, raw or as canonical base64 once trimmed, becomes one image, and the
  prompt is built from the args alone. An image over 20 MiB fails with `*asset.ImageTooLargeError`.
  Any other stdin goes through `PromptFrom` unchanged, so text prompts are byte-for-byte what
  `Prompt()` builds. The collector never extracts base64 from inside prompt text.

Images come back in the order `-img`, `-g`, stdin. Collect never writes to the asset store; the turn
stores pending images when a model step begins.

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

1. **StreamCompletions**: calls `Model.StreamCompletions(ctx, chat)` → returns `chan CompletionEvent`.
   The chat it sends is the session chat prepared for images (see [Images in a turn](#images-in-a-turn)).
2. **Event loop**: reads from channel, dispatching:
   - `string` → appends to `fullMsg`, prints to stdout (streaming output)
   - `pub_models.Call` → collected for the step's tool batch (see below)
   - `models.ImageProgressEvent` → starts the image progress row (`ImageTurn.Progress`);
     every other event but `NoopEvent` stops it first
   - `models.ImageEvent` → stored, presented and kept for the step's assistant message
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
    `DefaultHandoverInstructions`), prints a notice (outside loop mode, where
    the crossing is expected operation), and marks the session
    handover-requested. The handover starts the wrap-up phase: post-handover
    tool calls are counted against `stoploss.max-tool-calls-after-handover`
    (absent or 0 = unlimited, the default) with a fresh counter, so the agent
    can keep working — e.g. summarizing context into a file — until it
    produces a plain reply. Only when a configured wrap-up allowance is
    exhausted do later tool calls run the refusal ladder until the run ends
    cleanly (`io.EOF`). A step that ends with a plain reply ends the run
    without a handover — there is nothing to hand over.
5. **Post-processing** (`sessionFinalizer.Finalize`, `internal/text/finalizer.go`):
   - Appends assistant message to chat; enriches cost whenever usage is present
   - Pretty-prints final output (via glow when the destination is a terminal, unless `-r`/`--raw`).
     When the last step wrote no text, the prose shown before its tool calls stays as it is and
     is not printed again (`QuerySession.finalTextShown`).
   - Joins the in-flight conversation summarizer, launched alongside the
     main call on a new conversation's first persist, and stamps
     `title`/`summary` on a result. A plain query takes the result only if it
     already landed (labelling is best effort); a loop iteration waits at
     most 5 s (see `architecture/summaries.md`)
   - Saves conversation via the turn's `chat.Persister.Promote` (unless in chat mode),
     which writes what `SaveAsPreviousQuery()` writes

   The order on the success path is **display → join → persist**, so the
   answer is never delayed by the summary wait and the file, index and
   `globalScope.json` mirror are written once. A failed, empty or
   interrupted run abandons the summarizer and persists without joining;
   no summarizer failure reaches the answer, exit status or persistence.
   See `architecture/summaries.md`.

### Images in a turn

`text.Configurations.ImageTurn` (`internal/text/turn.go`) wires the turn's image
children: the asset store, the `imagenorm.Normalizer`, the `present.Presenter`, the
`chat.Persister`, a stderr writer, and the image-generation setup with its injected
`imagegen.NewGeneratorFunc`. When it is nil, as `pkg/agent` and `pkg/text` build it, the
runner uses a default for the config dir: the disk store under `conversations/assets/`,
a presenter on the querier's output that never opens a viewer, and no generation.

The `query` command builds its own (`internal/text/query_images.go`, set through
`Querier.setImageTurn`): the presenter opens images when `-open` or `photoConfig.json`'s
`auto-open-images` is set, with `present.NewCommandOpener(photoConfig.json image-opener)`;
generation is requested when an entry of `-t`,
or of the profile's tools, matches `generate_image` as any tool pattern does, with the model
from `-pm`, else `route.ImageModelFor(chat vendor, photoConfig.json model)`; and the generator
constructor is `cli.Deps.NewImageGenerator`
(`route.NewGenerator` in production, a refusing constructor in every e2e `TestMain`).

- **Step start.** Every pending image in the chat is stored, so each handle the model
  sees resolves (for example in `generate_image`'s `input_images`). The chat is then
  normalized for the route's `ImageCapabilities`, and that copy is what
  `StreamCompletions`, and the stoploss's `CountInputTokens`, receive. Labels, notes and
  synthetic messages exist only in the copy, never on disk.
- **Image-output models.** An `ImageEvent` (from an OpenRouter image model) is stored, then
  presented as one line on its own row, as it arrives. The final assistant message holds its text and images as `Parts`, in
  arrival order; a step that ends in tool calls attaches its images to the assistant
  tool-call message. A text-only step still persists `Content`.
- **Tool results.** A `PartsCaller` result that holds an image is persisted as `Parts`;
  the images the tool generated are presented, with the call's `rebus` when it is one to
  three emoji, and a tool's own images (MCP, `view_image`) are not. Every other result is
  the `tools.InvokeWith` string, as before.
- **Generation.** When `generate_image` is requested, the turn registers the tool for every
  completer that takes tools; a vendor's built-in image output is never enabled. With
  structured output (`-rf`) generated images are stored but not presented.
- **Failures.** A generated image that cannot be stored makes its tool result an `ERROR`
  text; the batch's other calls still run and record their results, then the turn fails.
  An image stored before a later failure keeps its file, its line and its part in the
  failed-run persist. A viewer that fails to
  open is one notice on stderr and never fails the turn.

### Rate Limit Handling

A rate limit surfaces as a typed, terminal error on the step: the runner returns it on the first call, unretried (`claierr.ErrRateLimited` via `errors.Is`, facts such as `ResetAt` via `errors.As`). clai itself never sleeps or retries a throttled call — retry policy belongs to the caller, which has the context to choose it (worklog `2026-09-05-error-propagation`, phase 3; [errors.md](./errors.md)). `InputTokenCounter` remains in use only for the token stoploss budget, not for backoff.

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

A Gemini 3 preview call without a thought signature is handed back to the user
unrun. When every call of a step is handed back, the runner decides it before
the batch: the step ends the turn as a step that answers does (no further
request; its prose and reasoning are the answer, or the answer shown before
stays), and a step with no answer at all fails the turn with
`*text.HandedBackError`. In a batch that also runs a call, only the unsigned
calls are dropped.

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
- **Raw (`-r`)**: tokens stream directly, no post-processing formatting. Reasoning with no visible text (e.g. Anthropic's omitted thinking display) prints no `[thinking]` wrapper.
- **Structured (`-rf`/`-response-format`)**: blocks streaming and prints only the final structured response, followed by one newline. It suppresses reasoning, tool activity, skill-discovery logs, lookback notices, formatting, and BEL. The model must implement `generic.ResponseFormatSetter`; one that does not fails with `generic.ErrResponseFormatUnsupported` instead of silently streaming prose. OpenAI-compatible vendors send `response_format`, Jev reads its questions from `json_schema.schema`, and Anthropic maps `json_schema` onto `output_config.format` (it rejects `json_object`, which it has no equivalent for).
- **Cmd mode (`cmd` command)**: output is treated as a shell command; user is prompted to execute it
