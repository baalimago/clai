# goai: Your CLI Companion for AI Interactions

goai is a versatile Command-Line Interface (CLI) tool designed to simplify interactions with AI models, specifically for querying chat models and generating images using OpenAI's powerful API.
This tool is developed in Go and aims to reduce the boilerplate associated with integrating OpenAI's API into CLI applications, providing users with an efficient way to harness the power of AI directly from their terminal.

(this readme was generated with `goai -raw=true g './*.go' write a README for this project`)
## Features

- **Query Chat Models:** Easily query OpenAI's chat models, like GPT-4, to receive text-based responses for your queries directly in your terminal.
- **Generate Images:** Generate images based on prompts using OpenAI's DALL-E model, with customization options for image quality, size, and style.
- **Glob Support:** Query the chat model using the contents of files matched by a specified glob pattern, alongside additional text input.
- **Formatted Markdown Output:** For users with Glow installed, goai provides beautifully formatted markdown output to enhance readability.
- **Flexibility:** Customize various settings, such as the AI model used, the directory for storing generated images, and the prefix for image files.

## Prerequisites

Before you begin using goai, ensure the following prerequisites are met:

- **OpenAI API Key:** Set the `OPENAI_API_KEY` environment variable to your OpenAI API key. See here: [OpenAI API Key](https://platform.openai.com/docs/quickstart/step-2-set-up-your-api-key).
- **Glow (Optional):** Install [Glow](https://github.com/charmbracelet/glow) for formatted markdown output when querying text responses.
- **No Color Output (Optional):** Set the `NO_COLOR` environment variable to disable ANSI color output.

## Installation
```bash
go install github.com/baalimago/goai@latest
```

## Configuration
On initial run, `goai` will create a configuration file at `$HOME/.goai/prompts.json` where you can configure the initlal prompts for the chat model. Modify this file to tweak your personal ai.

## Usage

After installation, you can start using goai by running commands directly from your terminal. Here's a quick overview of the commands and flags available:

### Global Flags

- `-cm`, `--chat-model <model>`: Set the chat model to use. Default is 'gpt-4-turbo-preview'.
- `-pm`, `--photo-model <model>`: Set the image model to use. Default is 'dall-e-3'.
- `-pd`, `--picture-dir <path>`: Set the directory to store generated pictures. Default is `$HOME/Pictures`.
- `-pp`, `--picture-prefix <prefix>`: Set the prefix for generated pictures. Default is 'goai'.

### Commands

- `q <text>`: Query the chat model with the given text.
- `p <text>`: Request a picture from the photo model with the given prompt.
- `g <glob> <text>`: Query the chat model with the contents of files found by the glob and the given text.
- `h`, `help`: Display the help menu with usage details.

Example usage:

```bash
goai q "Tell me a joke."
goai -r g '*.go' Generate a README for this project > README.md
printf "flowers" | go run . -pp flower -pd /home/$USER/AiCatFlowers -i p A cat made of {}
```

Since -N alternatives are disabled for many newer OpenAI models, you can use [repeater](https://github.com/baalimago/repeater) to generate several responses from the same prompt:
```bash
NO_COLOR=true repeater -n 10 -w 3 -increment -file out.txt -output FILE goai -pp flower -pd /home/$USER/AiCatFlowers p "A cat made of flowers"
```

## Contributing

Contributions to goai are welcome! If you have a feature request, bug report, or a patch, please feel free to submit an issue or pull request on GitHub.

## License

goai is open-source software licensed under the MIT license. See the LICENSE file for more details.

---

Enjoy using goai to streamline your AI interactions directly from your command line!
