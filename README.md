# clai: command line artificial intelligence

`clai` brings convenience to the OpenAI models.
You can generate images, text and summarize content from the command line with ease (see examples below).
Changing models and tweaking the prompt is as easy as changing a few flags, or persist it with a configuration file.

## Prerequisites
- **OpenAI API Key:** Set the `OPENAI_API_KEY` environment variable to your OpenAI API key. See here: [OpenAI API Key](https://platform.openai.com/docs/quickstart/step-2-set-up-your-api-key).
- **Glow (Optional):** Install [Glow](https://github.com/charmbracelet/glow) for formatted markdown output when querying text responses.

## Installation
```bash
go install github.com/baalimago/clai@latest
```

### Examples
```bash
clai query "Tell me a joke."
```
```bash
clai --raw `                    # Don't format output as markdown` \
    --chat-model gpt-3.5-turbo `# Use some other model` \
    glob '*.go' Generate a README for this project > README.md
```
```bash
printf "flowers" `                  # Pipe any data into clai, such as a specialized prompt` \
    | clai  --photo-prefix flower ` # Photos are stored locally with randomized string as suffix, this sets prefix` \
            --photo-dir  /tmp/ `    # You can modify where to store the rendered image ` \ 
            -i `                    # Use xargs notation for replacing some substring with the piped in content` \
            photo A cat made of {}
```
```bash
clai help `# For more info about the available commands (and shorthands)`
```


Since -N alternatives are disabled for many newer OpenAI models, you can use [repeater](https://github.com/baalimago/repeater) to generate several responses from the same prompt:
```bash
NO_COLOR=true repeater -n 10 -w 3 -increment -file out.txt -output BOTH \
    clai -pp flower_INC p A cat made of flowers
```

## Configuration
On initial run, `clai` will create a configuration file at `$HOME/.clai/prompts.json` where you can configure the initlal prompts for the chat model. Modify this file to tweak your personal ai.

## Honorable mentions
This project is heavily inspired by: [https://github.com/Licheam/zsh-ask](https://github.com/Licheam/zsh-ask), many thanks to Licheam for the inspiration.
