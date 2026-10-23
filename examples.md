# Examples

Dive deeper into the subject you're interested in by seeing the different files in the [./architecture](./architecture), see links below.

## Find your config dir

```bash
clai confdir
cd "$(clai confdir)"
clai confdir mcpServers
clai confdir conversations dirs
```

- `confdir` prints the absolute clai config dir path.
- You may also pass a registered config subpath, such as `mcpServers` or `conversations dirs`.
- This is useful for quickly jumping into config-managed files.

See: [`config.md`](./architecture/config.md).

## Query + reply + directory reply

```bash
clai query "Explain the design"
clai -re query "Now give the trade-offs"
clai -dre query "Apply it to this repo"
clai query -- -why does this fail
```

- `query` saves reply context to `<clai-config>/conversations/globalScope.json`.
- Non-reply queries also bind CWD → chat ID (dir-scope).
- `-re` loads `globalScope.json` as context.
- `-dre` first copies the directory-bound chat into `globalScope.json`, then uses normal `-re` plumbing.
- A prompt whose first word starts with `-` is parsed as a flag; pass it after `--`.

See: [`query.md`](./architecture/query.md), [`chat.md`](./architecture/chat.md), [`dre.md`](./architecture/dre.md).

## Loop until the goal is verified

```bash
clai loop -check 'go test ./...' fix the failing build
clai l -mi 3 -check ./verify.sh make the linter clean
```

- Each iteration is a fresh session. The previous iteration's final reply becomes its handover.
- The agent calls `complete_task` when it is done. With `-check`, an achieved call is accepted only when the command exits 0.
- `-mi` / `-max-iterations` bounds the run (default 10, `0` = unlimited). A blocked task or an exhausted bound exits 1.

See: [`query.md`](./architecture/query.md#loop-command).

## Blocking structured output

Use the included review schema to produce JSON that you can send directly to `jq`:

```bash
go run . -rf examples/response-format-review.json query \
  "Review the current changes. Return a summary, a findings list, and an approval decision." | jq
```

The `-rf` flag blocks streaming output. Standard output contains only the final structured response and one trailing newline.

Reasoning, tool activity, skill-discovery logs, lookback notices, formatting, and the completion notification bell do not appear in this output.

Errors still go to standard error.

## Auto-append shell context

```bash
clai -asc minimal query "Why is this command failing?"
clai -append-shell-context git query "Summarize the current repo state before answering"
```

- Shell contexts live in `<clai-config>/shellContexts/<name>.json`.
- The selected context runs shell commands, renders a template, and appends the result to your final query prompt.
- Use this when you want runtime context like cwd, branch name, or git status included automatically.

See: [`shell-context.md`](./architecture/shell-context.md).

## Inspect “what did it say last time?”

```bash
clai replay     # last message from globalScope.json
clai dre        # last message from directory-bound chat
clai -r replay  # raw (no pretty/glow)
```

- `replay` and `dre` do not call any LLM.
- They load a chat transcript and pretty-print the last message.

See: [`replay.md`](./architecture/replay.md), [`dre.md`](./architecture/dre.md).

## Bind a previous conversation to the current directory

```bash
clai chat list
clai chat continue 3
clai -dre query "Continue from that context"
```

- `chat continue <index|id>` selects an existing transcript and stores a directory binding.
- After that, `-dre` in this directory uses that conversation as context.

See: [`chat.md`](./architecture/chat.md).

## Profiles = workflow presets

```bash
clai profiles list
clai -p ops query "Find the owners of this subsystem"
```

- Profiles live in `<clai-config>/profiles/*.json`.
- They can override model, prompts, and requested tools.
- These are colliqualy "agent configurations"

See: [`profiles.md`](./architecture/profiles.md), [`config.md`](./architecture/config.md).

Also see examples:

- [ops](./examples/profiles/ops.json) - This agent can answer any question about your company's systems and customers
- [tradebot](./examples/profiles/trade-bot.json) - Fully functional polymarket trade bot, defined as json. Swap prompt and model, try it out!

## Tools: inspect vs enable

```bash
clai tools
clai tools rg
clai -t "rg,cat" query "Search for parsing logic and show me the file"
```

- `clai tools` is inspection only.
- `-t` enables tool calling for that _run_; without it, tool calls are disabled.
- `-t "*"` allows all registered tools.

See: [`tools-command.md`](./architecture/tools-command.md), [`tooling.md`](./architecture/tooling.md), [`config.md`](./architecture/config.md).

## MCP tools (external tool servers)

```bash
clai setup      # stage 3: MCP server configs
clai -t "mcp_linear*" query "List open incidents assigned to me"
```

- MCP server configs are stored in `<clai-config>/mcpServers/*.json`.
- MCP tool names are typically `mcp_<server>_<tool>` (and can be globbed).

See: [`tooling.md`](./architecture/tooling.md), [`setup.md`](./architecture/setup.md).

## Multimodal: images, photo + video

```bash
clai -img screenshot.png q "What is wrong in this dialog?"
cat diagram.png | clai q "Describe this diagram"
clai -g 'docs/*' q "Summarize these notes and figures"
clai -t generate_image -open q "Draw a fox in the snow"
clai -re -t generate_image q "Make it night"

clai photo "A minimal architecture diagram"
clai -re photo "Now simplify it further"
clai photo -img sketch.png -open "Make it a watercolour"

clai video "A slow pan across a terminal showing streaming output"
clai video -img still.png "Make the waves roll"
```

- `-img` attaches images to any model that reads them; a model that cannot fails before any request. Stdin that is wholly an image is attached too.
- `generate_image` is clai's own tool on every vendor. It uses the chat vendor's image backend when it has one (OpenAI, Gemini, OpenRouter), otherwise the `photoConfig.json` model; `-pm` overrides both. Images are saved under `<clai-config>/conversations/assets/`. On a terminal each shows in the tool's block as two rows, `▸ [rebus] image <handle> · <details>` and its `file://` URL; piped, each is its bare path.
- `photo`/`video` have separate mode configs: `photoConfig.json`, `videoConfig.json`.
- `photo` saves each image under `output.dir` and prints its path; `-img` adds input images, and `-re` also sends the previous query's latest image.

See: [`images.md`](./architecture/images.md), [`photo.md`](./architecture/photo.md), [`video.md`](./architecture/video.md), [`config.md`](./architecture/config.md).

## Audio transcription

```bash
clai audio transcribe meeting.wav
clai a t -af text meeting.wav
cat meeting.wav | clai a t -

clai a t meeting.wav | clai q "Summarize these meeting notes: {}"
# let the agent transcribe, picking the model + format the tool uses:
clai -am gpt-4o-transcribe-diarize -af json -t audio_transcribe q "Who said what in meeting.wav?"
```

- Transcripts render locally as `vtt` (default), `srt`, `text`, or `json`; stdout carries only the transcript, so it pipes cleanly.
- Files over 25 MB are split via ffmpeg and stitched back with adjusted timestamps.
- The `audio_transcribe` built-in tool exposes the same engine to agents: `clai -t "*" q "Transcribe meeting.wav and summarize it"`.

See: [`audio.md`](./architecture/audio.md).

## Streaming: one normalized event loop

- All vendors map their streaming responses to a small set of normalized events:
  - `string` text deltas
  - tool call events
  - stop/no-op/error events
- The querier loop is vendor-agnostic: it prints deltas, executes tools, and finalizes output.

See: [`streaming.md`](./architecture/streaming.md), [`query.md`](./architecture/query.md).
