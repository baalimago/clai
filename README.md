![Wakatime](https://wakatime.com/badge/user/018cc8d2-3fd9-47ef-81dc-e4ad645d5f34/project/018e07e1-bd22-4077-a213-c16290d3db52.svg)
[![Go Reference](https://pkg.go.dev/badge/github.com/baalimago/clai.svg)](https://pkg.go.dev/github.com/baalimago/clai/pkg/agent)

Test coverage: 92.0% 😍👌

<div align="center">
  <a  href="https://www.clai.chat">
  <img src="img/clai-logo.png" alt="clai logo" width="112">
  </a>
  <h2>Command Line Artificial Intelligence</h2>
  <p><code>clai</code> (<i>/klaɪ/</i>, like "cli" in "<b>cli</b>mate") is a unix-like command line agentic harness with batteries hosted online.</p>
</div>

### Get started

Installing:

```bash
curl -fsSL https://raw.githubusercontent.com/baalimago/clai/main/setup.sh | sh
```

You can also install via go:

```bash
go install github.com/baalimago/clai@latest
```

Or run it from the Nix flake without installing (flakes must be enabled):

```bash
nix run github:baalimago/clai
```

Then run:

```bash
clai | clai query Please give a concise explanation of clai
```

Install [Glow](https://github.com/charmbracelet/glow) for formatted markdown output when querying text responses.

See <a href="https://www.clai.chat">clai.chat</a> for details on usecase of all the features. Here's a brief summary:

<div align="center">
  <img src="img/clai-demo.gif" alt="Three aliases, a loop that verifies itself, a generated image read back by the model, a Jev classification, a stored chat, a model swap and a replay">
  <p>Eleven acts in one shell session, each explained in <a href="./examples.md#showcase">examples</a>.</p>
</div>


## Features

- **[Shell context injection](./architecture/shell-context.md)** - Inject shell context into the system prompt via configurable templates.
- **[Seamless conversation import](./architecture/continue-from-claudex.md)** - Ran out of session? Continue in clai!
- **[Agent Skills](./architecture/skills.md)** - Import skills from custom global directories, or project-level `.agents/skills`.
- **[Profiles](./examples.md#profiles-workflow-presets)** - Pre-prompted profiles enabling customized workflows and agents, run locally or [from the Hub](#run-an-agent-from-the-hub).
- **[Model fallback](./architecture/model-aliases.md)** - Name a verb (`strong`, `fast`, `cheap`) instead of a model: each machine resolves it to its own ordered list and the run tries them in turn, so a shared profile runs anywhere.
- **[Loop until verified](./examples.md#loop-until-the-goal-is-verified)** - Add `-loop` to a query and clai iterates toward the goal, accepting completion only when `-check` passes.
- **[Structured output](./examples.md#blocking-structured-output)** - `-response-format` returns JSON instead of prose, from any vendor; Jev answers a fixed set of questions about the data you pipe in.
- **[MCP client support](./examples.md#mcp-tools-external-tool-servers)** - Add any MCP server you'd like by pasting their configuration.
- **[Images, both ways](./architecture/images.md)** - Attach images with `-image`, a glob or stdin to any model, and get images back through the `generate_image` tool, or from an image-output model.
- **[Sync, git-style](./architecture/cloud.md)** - `clai cloud pull` and `commit` carry your config and conversations between machines, only when you ask.
- **[Reply only](./examples.md#keep-only-the-reply-write-it-to-a-file-watch-the-progress)** - `-reply-only` keeps stdout for the answer and draws the progress on stderr.
- **Unix-like** - Clai follows the [unix philosophy](https://en.wikipedia.org/wiki/Unix_philosophy) and works with data piped in and out.

All of these features are easily combined and tweaked, empowering users to accomplish very diverse use cases.
See [examples](./examples.md) for additional info.

## Run an agent from the Hub
Overwhelmed by all settings and configuration? Fear not! Load batteries from online by setting `-profile-path` to get a pre-configured agent.

```bash
clai -profile-path cchub:clai/pr-summarizer@1 query
```

The first run shows what the agent brings (model, tools, MCP servers, skills) and asks you to trust it. See `clai profiles -h` for more.

An agent can be uploaded by anyone to [clai.chat](https://www.clai.chat). A local file works just fine, or a github.com link copied from the browser.


## Use clai as a Go library

Embed the agent loop in a Go program with `github.com/baalimago/clai/pkg/agent`:

```go
// Supports strucutred output via types
// Support all things the CLI does
review, err := agent.NewTyped[Review](agent.WithModel("gpt-5.2")).Query(ctx, chat)
```

The agent runs silent and returns typed errors. See [pkg/agent](https://pkg.go.dev/github.com/baalimago/clai/pkg/agent) for the option surface, the command ban list and the token stoploss, and [pkg/tools](https://pkg.go.dev/github.com/baalimago/clai/pkg/tools) for the tool catalog.

## Sync across machines (optional)

`clai cloud` keeps your config (profiles, MCP servers, shell contexts, skills, theme) and, if you like, your conversations in step between machines through a [clai.chat](https://www.clai.chat) account.
It works like git: nothing syncs on its own, and clai works the same without an account.

```bash
clai cloud login              # approve this machine in the browser
clai cloud status             # what you would commit, and what the server has
clai cloud pull               # bring down your account's changes
clai cloud commit -m "laptop" # upload yours
```

Config sync is free (up to 1 GB); conversations cost $1 per started GB a month and need a card on file.
A config file holding a literal key is never committed, a clash keeps your file and saves the other copy for `clai cloud resolve`, and `-r` prints JSON.
See `clai cloud -h` and [architecture/cloud.md](./architecture/cloud.md).

## Supported vendors

| Vendor      | Environment Variable | Models                                                                                                                                                             |
| ----------- | -------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| Jev         | `TYPESAFE_API_KEY`   | [System One](https://docs.typesafe.ai/api) evaluation, model `jev-latest`, questions from `-response-format`                                                                    |
| OpenRouter  | `OPENROUTER_API_KEY` | [Text models](https://openrouter.ai/models), [image models](https://openrouter.ai/models?output_modalities=image), use prefix `or:`                              |
| Ollama      | `OLLAMA_API_KEY`     | Use format `ollama:` (defaults to llama3), server defaults to localhost:11434                                                                                      |
| HuggingFace | `HF_API_KEY`         | [Text models](https://huggingface.co/docs/inference-endpoints/chat-completions), use prefix `hf:`                                                                  |
| Mistral     | `MISTRAL_API_KEY`    | [Text models](https://docs.mistral.ai/getting-started/models/)                                                                                                     |
| OpenAI      | `OPENAI_API_KEY`     | [Text models](https://platform.openai.com/docs/models), [photo models](https://platform.openai.com/docs/models/dall-e)                                             |
| DeepSeek    | `DEEPSEEK_API_KEY`   | [Text models](https://api-docs.deepseek.com/quick_start/pricing), key is optional, no prefix (defaults to `deepseek-chat`)                                         |
| Berget AI   | `BERGET_API_KEY`     | [Text models](https://berget.ai/models), use prefix `berget:`                                                                                                      |
| Gemini      | `GEMINI_API_KEY`     | [Text models](https://ai.google.dev/gemini-api/docs/models), [photo models](https://ai.google.dev/gemini-api/docs/image-generation#image_generation_text-to-image) |
| xAi         | `XAI_API_KEY`        | [Text models](https://docs.x.ai/docs/models)                                                                                                                       |
| Inception   | `INCEPTION_API_KEY`  | [Text models](https://platform.inceptionlabs.ai/docs#models)                                                                                                       |
| Anthropic   | `ANTHROPIC_API_KEY`  | [Text models](https://platform.claude.com/docs/en/about-claude/models/overview)                                                                                    |


<sub>Moved but never forgotten:</sub>
<div align="center">
  <img src="img/banner.jpg" alt="The clai gopher">
</div>
