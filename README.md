# clai: command line artificial intelligence

[![Go Report Card](https://goreportcard.com/badge/github.com/baalimago/clai)](https://goreportcard.com/report/github.com/baalimago/clai)
![Wakatime](https://wakatime.com/badge/user/018cc8d2-3fd9-47ef-81dc-e4ad645d5f34/project/018e07e1-bd22-4077-a213-c16290d3db52.svg)

Test coverage: 42.018% 😒👍

`clai` (/klaɪ/, like "cli" in "**cli**mate") is a command line context-feeder for any ai task.

<div align="center">
  <img src="img/banner.jpg" alt="Banner">
</div>

## Features

- **[MCP client support](./EXAMPLES.md#Tooling)** - Add any MCP server you'd like by simply pasting their configuration.
- **Vendor agnosticism** - Use any functionality in Clai with [most LLM vendors](#supported-vendors) interchangeably.
- **[Conversations](./EXAMPLES.md#Conversations)** - Create, manage and continue conversations.
- **Rate limit circumvention** - Automatically summarize + recall complex tasks.
- **[Profiles](./EXAMPLES.md#Profiles)** - Pre-prompted profiles enabling customized workflows and agents.
- **Unix-like** - Clai follows the [unix philosophy](https://en.wikipedia.org/wiki/Unix_philosophy) and works seamlessly with data piped in and out.

All of these features are easily combined and tweaked, empowering users to accomplish very diverse use cases.
See [examples](./EXAMPLES.md) for additional info.

## Supported vendors

| Vendor    | Environment Variable | Models                                                                                                                                                     |
| --------- | -------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------- |
| OpenAI    | `OPENAI_API_KEY`     | [Text models](https://platform.openai.com/docs/models/gpt-4-and-gpt-4-turbo), [photo models](https://platform.openai.com/docs/models/dall-e)               |
| Anthropic | `ANTHROPIC_API_KEY`  | [Text models](https://docs.anthropic.com/claude/docs/models-overview#model-recommendations)                                                                |
| Mistral   | `MISTRAL_API_KEY`    | [Text models](https://docs.mistral.ai/getting-started/models/)                                                                                             |
| Deepseek  | `DEEPSEEK_API_KEY`   | [Text models](https://api-docs.deepseek.com/quick_start/pricing)                                                                                           |
| Novita AI | `NOVITA_API_KEY`     | [Text models](https://novita.ai/model-api/product/llm-api?utm_source=github_clai&utm_medium=github_readme&utm_campaign=link), use prefix `novita:<target>` |
| Ollama    | N/A                  | Use format `ollama:<target>` (defaults to llama3), server defaults to localhost:11434                                                                      |
| Inception | `INCEPTION_API_KEY`  | [Text models](https://platform.inceptionlabs.ai/docs#models)                                                                                               |

Note that you can only use the models that you have bought an API key for.

## Get started

```bash
go install github.com/baalimago/clai@latest
```

You may also use the setup script:

```bash
curl -fsSL https://raw.githubusercontent.com/baalimago/clai/main/setup.sh | sh
```

Either look at `clai help` or the [examples](./EXAMPLES.md) for how to use `clai`.
If you have time, you can also check out [this blogpost](https://lorentz.app/blog-item.html?id=clai) for a slightly more structured introduction on how to use Clai efficiently.

Install [Glow](https://github.com/charmbracelet/glow) for formatted markdown output when querying text responses.
