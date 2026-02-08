# Examples

These examples build up from “one-shot prompts” to replies, directory-scoped conversations, tools, and multimodal.

> Notes
>
> - There is **no interactive chat loop**: each command is one turn and exits.
> - clai stores transcripts under `<clai-config>/conversations/` and uses `globalScope.json` for replies.

## 1) One-shot text query

Ask a question:

```bash
clai query "Explain big-O in one paragraph"
# alias: clai q "..."
```

- Streams the answer to stdout.
- Saves context to `<clai-config>/conversations/globalScope.json`.
- Also writes a conversation transcript `<clai-config>/conversations/<chatID>.json`.

## 2) Use stdin as the prompt

Pipe text into clai:

```bash
cat notes.txt | clai query Summarize:
```

If stdin is piped and **no args** are provided, stdin becomes the prompt.

## 3) Reply to the previous query (`-re`)

Continue from the last run (global):

```bash
clai -re query "Now rewrite that as bullet points"
```

This loads `<clai-config>/conversations/globalScope.json` and prepends it as context.

## 4) Inspect the most recent message

Replay the last message from the global previous query:

```bash
clai replay
# alias: clai re
```

Raw (no pretty-print/glow):

```bash
clai -r replay
```

## 5) Directory-scoped replies (`-dre`)

clai also tracks a conversation bound to your current working directory (CWD).

After you run a normal (non-reply) query in a directory, that chat becomes the directory binding.

Reply using the directory-bound conversation:

```bash
clai -dre query "Continue, but apply it to this project"
```

Show the last message from the directory-bound conversation:

```bash
clai dre
```

If nothing is bound, `dre` errors with:

```text
no directory-scoped conversation bound to current directory
```

## 6) List and continue an older conversation

List saved conversations:

```bash
clai chat list
```

Bind an existing chat to the current directory (by index from the list):

```bash
clai chat continue 3
```

Optionally append a new prompt while continuing:

```bash
clai chat continue 3 "What did we decide about the approach?"
```

After binding, you can continue from that chat in this directory with:

```bash
clai -dre query "Ok—next steps?"
```

## 7) Use a profile (workflow preset)

Profiles live under `<clai-config>/profiles/*.json` and can override model/prompt/tool defaults.

List profiles:

```bash
clai profiles list
```

Run a query with a profile:

```bash
clai -p "my-profile" query "Draft a design note"
```

## 8) See and select tools

List tools available to the runtime:

```bash
clai tools
```

Show the JSON schema for a specific tool:

```bash
clai tools rg
```

Allow tool calling for a run:

```bash
clai -t "rg,cat" query "Search for where Configurations is defined and show the relevant file"
```

Allow _all_ tools:

```bash
clai -t "*" query "Inspect this repo and explain how setup works"
```

Tool calling is only possible when tools are enabled/allowed for that run.

You may also import and append mcp servers, see `clai setup -> 3`.
These are stored in `<clai-config>/mcpServers/*.json`.
Use all, or some, mcp servers with glob selection.
Example:

- `mcp_linear*` -> Use all tools from mcp server `linear.json`
- `mcp_filesystem_write_file` -> Use only `write_file` tool from mcp server `filesystem`

## 9) Generate a shell command (`cmd`)

Ask for a bash command (cmd mode changes the system prompt and adds an execute/quit confirmation):

```bash
clai cmd "find all .go files changed in the last commit"
```

After the model outputs the command, clai asks:

```text
Do you want to [e]xecute cmd, [q]uit?:
```

## 10) Generate an image (`photo`)

Generate an image from text:

```bash
clai photo "A simple diagram of a request/response cycle"
```

Output behavior is controlled by `photoConfig.json` (save locally vs print URL).

Reply mode also works for photo:

```bash
clai -re photo "Now make it more minimal"
```

## 11) Generate a video (`video`)

Generate a video from text:

```bash
clai video "A slow pan across a terminal showing streaming output"
```

If your prompt contains a base64 image, clai can treat it as an input image for image-to-video (model-dependent).

## 12) Raw vs pretty output (`-r`)

Most commands that print model output support raw printing:

```bash
clai -r query "Output markdown without formatting"
```

Non-raw mode attempts pretty printing (and uses `glow` if installed).
