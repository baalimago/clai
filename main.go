package main

import (
	"context"
	"fmt"
	"os"

	"github.com/baalimago/clai/internal"
	"github.com/baalimago/go_away_boilerplate/pkg/ancli"
	"github.com/baalimago/go_away_boilerplate/pkg/misc"
	"github.com/baalimago/go_away_boilerplate/pkg/shutdown"
)

const usage = `clai - (c)ommand (l)ine (a)rtificial (i)ntelligence

Prerequisits:
  - Set the OPENAI_API_KEY environment variable to your OpenAI API key
  - Set the ANTHROPIC_API_KEY environment variable to your Anthropic API key
  - (Optional) Set the NO_COLOR environment variable to disable ansi color output
  - (Optional) Install glow - https://github.com/charmbracelet/glow for formated markdown output

Usage: clai [flags] <command>

Flags:
  -re, -reply bool             Set to true to reply to the previous query, meaing that it will be used as context for your next query. Default is false.
  -r, -raw bool                Set to true to print raw output (no animation, no glow). Default is false.
  -cm, -chat-model string      Set the chat model to use. Default is 'gpt-4-turbo-preview'. 
  -pm, -photo-model string     Set the image model to use. Default is 'dall-e-3'. 
  -pd, -photo-dir string       Set the directory to store the generated pictures. Default is $HOME/Pictures. 
  -pp, -photo-prefix string  Set the prefix for the generated pictures. Default is 'clai'. 
  -I, -replace string          Set the string to replace with stdin. Default is '{}'. (flag syntax borrowed from xargs)
  -i bool                       Set to true to replace '{}' with stdin. This is overwritten by -I and -replace. Default is false. (flag syntax borrowed from xargs)

Commands:
  h|help                        Display this help message
  q|query <text>                Query the chat model with the given text
  p|photo <text>                Ask the photo model a picture with the requested prompt
  g|glob  <glob> <text>         Query the chat model with the contents of the files found by the glob and the given text

  c|chat   n|new       <prompt>   Create a new chat with the given prompt.
  c|chat   c|continue  <chatID>   Continue an existing chat with the given chat ID.
  c|chat   d|delete    <chatID>   Delete the chat with the given chat ID.
  c|chat   l|list                 List all existing chats.
  c|chat   h|help                 Display detailed help for chat subcommands.

Examples:
  - clai h | clai -i q generate some examples for this usage string: '{}'
  - clai query "What's the weather like in Tokyo?"
  - clai glob "*.txt" "Summarize these documents."
  - clai -cm claude-3-opus-20240229 chat new "What are the atest advancements in AI?"
  - clai photo "A futuristic cityscape"
  - clai -pm dall-e-2 photo A cat in space
  - clai -pd ~/Downloads -pp holiday A beach at sunset
  - docker logs example | clai -I LOG q "Find errors in these logs: LOG"
  - clai c new "Let's have a conversation about climate change."
  - clai c list
  - clai c help
`

func main() {
	err := handleOopsies()
	if err != nil {
		ancli.PrintWarn(fmt.Sprintf("failed to handle oopsies, but as we didn't panic, it should be benign. Error: %v\n", err))
	}
	querier, err := internal.Setup(usage)
	if err != nil {
		ancli.PrintErr(fmt.Sprintf("failed to setup: %v\n", err))
		os.Exit(1)
	}
	ctx, cancel := context.WithCancel(context.Background())
	go func() { shutdown.Monitor(cancel) }()
	err = querier.Query(ctx)
	if err != nil {
		ancli.PrintErr(fmt.Sprintf("failed to run: %v\n", err))
		os.Exit(1)
	}
	cancel()
	if misc.Truthy(os.Getenv("DEBUG")) {
		ancli.PrintOK("things seems to have worked out. Good bye! 🚀\n")
	}
}
