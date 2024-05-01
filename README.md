# clai: command line artificial intelligence
[![Go Report Card](https://goreportcard.com/badge/github.com/baalimago/clai)](https://goreportcard.com/report/github.com/baalimago/clai)

`clai` integrates AI models of multiple vendors via with the terminal.
You can generate images, text, summarize content and chat while using native terminal functionality, such as pipes and termination signals.

The multi-vendor aspect enables easy comparisons between different models, also removes the need for multiple subscriptions: most APIs are usage-based (some with expiration time).

![clai_in_action_example](./img/example.gif "Example of clai in action")

## Prerequisites
- **Go:** Install Go from [here](https://golang.org/doc/install).
- **OpenAI API Key:** Set the `OPENAI_API_KEY` env var to your [OpenAI API key](https://platform.openai.com/docs/quickstart/step-2-set-up-your-api-key). [Text models](https://platform.openai.com/docs/models/gpt-4-and-gpt-4-turbo), [photo models](https://platform.openai.com/docs/models/dall-e).
- **Anthropic API Key:** Set the `ANTHROPIC_API_KEY` env var to your [Anthropic API key](https://console.anthropic.com/login?returnTo=%2F). [Text models](https://docs.anthropic.com/claude/docs/models-overview#model-recommendations).
- **Mistral API Key:** Set the `MISTRAL_API_KEY` env var to your [Mistral API key](https://console.mistral.ai/). [Text models](https://docs.mistral.ai/getting-started/models/)
- **Glow**(Optional): Install [Glow](https://github.com/charmbracelet/glow) for formatted markdown output when querying text responses.

Note that you can only use the models that you have bought an API key for.

Most text and photo based models within the respective vendors are supported, see [model configurations](#models) for how to swap.
Mistral does not have function call support as of 2024-05 since it doesn't work very well.

## Installation
```bash
go install github.com/baalimago/clai@latest
```

## Examples

All of the queries support xargs-like `-i`/`-I`/`-replace` flags.
Example `clai h | clai -i q Summarize this for me: {}`, this would summarize the output of `clai h`.

Regardless of you wish to generate a photo, continue a chat or reply to your previous query, the prompt system remains the same.

```bash
clai help `# For more info about the available commands (and shorthands)`
```

### Queries
```bash
clai query My favorite color is blue, tell me some facts about it
```
```bash
clai -re `# Use the -re flag to use the previous query as context for some next query` \
    q Write a poem about my favorite colour 
```

Personally I have `alias ask=clai q` and then `alias rask=clai -re q`.
This way I can `ask` -> `rask` -> `rask` for a temporary conversation.

Every 'temporary conversation' is also saved as a chat, so it's possible to continue it later, see below.

### Chatting
```bash
clai chat new Lets have a conversation about Hegel
```
```bash
clai chat list `# List all your chats`
```
```bash
clai -chat-model claude-3-opus-20240229 `  # Using some other model` \
    c continue 1`                          # Continue some previous chat` 
```

```bash
clai c continue 1 now here is what i would like to s `# Continue some previous chat with message ` 
```

Flag `-chat-model` works for any text-based model, regardless of vendor. 
Ditto, `-photo-model` for any photo-based models.

### Globs
```bash
clai -raw `                    # Don't format output as markdown` \
    glob '*.go' Generate a README for this project > README.md
```

### Photos
```bash
printf "flowers" | clai -i --photo-prefix=flowercat --photo-dir=/tmp photo "A cat made out of {}"
```
Since -N alternatives are disabled for many newer OpenAI models, you can use [repeater](https://github.com/baalimago/repeater) to generate several responses from the same prompt:
```bash
NO_COLOR=true repeater -n 10 -w 3 -increment -file out.txt -output BOTH \
    clai -pp flower_INC p A cat made of flowers
```


## Configuration
`clai` will create configuration files at [os.GetConfigDir()](https://pkg.go.dev/os#UserConfigDir)`/.clai/`.
Two default command-related ones `textConfig.json` and `photoConfig.json`, then one for each specific model.
The configuration system is as follows:
1. Default configurations from `textConfig.json` or `photoConfig.json`, here you can set your default model (which implies vendor)
1. Override the configurations using flags

The `text/photo-Config.json` files configures _what_ you want done, **not** how the models should perform it.
This way it scales for any vendor + model.

### Models
There's two ways to configure the models:
1. Set flag `-chat-model` or `-photo-model` 
1. Set the `model` field in the `textConfig.json` or `photoConfig.json` file. This will make it default, if not overwritten by flags.

Then, for each model, a new configuration file will be created.
Since each vendor's model supports quite different configurations, the model configurations aren't exposed as flags.
Example `.../.clai/openai_gpt_gpt-4-turbo-preview.json` which the contains configurations specific for this model, such as temperature.

### Conversations
Within [os.GetConfigDir()](https://pkg.go.dev/os#UserConfigDir)`/.clai/conversations` you'll find all the conversations.
You can also modify the chats here as a way to prompt, or create entirely new ones as you see fit.

## Honorable mentions
This project was originally inspired by: [https://github.com/Licheam/zsh-ask](https://github.com/Licheam/zsh-ask), many thanks to Licheam for the inspiration.
