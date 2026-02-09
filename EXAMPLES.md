# Examples

Dive deeper into the subject you're interested in by seeing the different files in the [./architecture](./architecture), see links below.

## Query + reply + directory reply

```bash
clai query "Explain the design"
clai -re query "Now give the trade-offs"
clai -dre query "Apply it to this repo"
```

- `query` saves reply context to `<clai-config>/conversations/globalScope.json`.
- Non-reply queries also bind CWD → chat ID (dir-scope).
- `-re` loads `globalScope.json` as context.
- `-dre` first copies the directory-bound chat into `globalScope.json`, then uses normal `-re` plumbing.

See: [`QUERY.md`](./architecture/QUERY.md), [`CHAT.md`](./architecture/CHAT.md), [`DRE.md`](./architecture/DRE.md).

## Inspect “what did it say last time?”

```bash
clai replay     # last message from globalScope.json
clai dre        # last message from directory-bound chat
clai -r replay  # raw (no pretty/glow)
```

- `replay` and `dre` do not call any LLM.
- They load a chat transcript and pretty-print the last message.

See: [`REPLAY.md`](./architecture/REPLAY.md), [`DRE.md`](./architecture/DRE.md).

## Bind a previous conversation to the current directory

```bash
clai chat list
clai chat continue 3
clai -dre query "Continue from that context"
```

- `chat continue <index|id>` selects an existing transcript and stores a directory binding.
- After that, `-dre` in this directory uses that conversation as context.

See: [`CHAT.md`](./architecture/CHAT.md).

## Profiles = workflow presets

```bash
clai profiles list
clai -p ops query "Find the owners of this subsystem"
```

- Profiles live in `<clai-config>/profiles/*.json`.
- They can override model, prompts, and requested tools.
- These are colliqualy "agent configurations"

See: [`PROFILES.md`](./architecture/PROFILES.md), [`CONFIG.md`](./architecture/CONFIG.md).

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

See: [`TOOLS.md`](./architecture/TOOLS.md), [`TOOLING.md`](./architecture/TOOLING.md), [`CONFIG.md`](./architecture/CONFIG.md).

## MCP tools (external tool servers)

```bash
clai setup      # stage 3: MCP server configs
clai -t "mcp_linear*" query "List open incidents assigned to me"
```

- MCP server configs are stored in `<clai-config>/mcpServers/*.json`.
- MCP tool names are typically `mcp_<server>_<tool>` (and can be globbed).

See: [`TOOLING.md`](./architecture/TOOLING.md), [`SETUP.md`](./architecture/SETUP.md).

## Multimodal: photo + video

```bash
clai photo "A minimal architecture diagram"
clai -re photo "Now simplify it further"

clai video "A slow pan across a terminal showing streaming output"
```

- `photo`/`video` have separate mode configs: `photoConfig.json`, `videoConfig.json`.
- Output can be saved locally or printed as a URL, depending on config.

See: [`PHOTO.md`](./architecture/PHOTO.md), [`VIDEO.md`](./architecture/VIDEO.md), [`CONFIG.md`](./architecture/CONFIG.md).

## Streaming: one normalized event loop

- All vendors map their streaming responses to a small set of normalized events:
  - `string` text deltas
  - tool call events
  - stop/no-op/error events
- The querier loop is vendor-agnostic: it prints deltas, executes tools, and finalizes output.

See: [`STREAMING.md`](./architecture/STREAMING.md), [`QUERY.md`](./architecture/QUERY.md).
