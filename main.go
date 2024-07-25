package main

import (
	"context"
	"fmt"
	"os"
	"runtime/pprof"

	"github.com/baalimago/clai/internal"
	"github.com/baalimago/go_away_boilerplate/pkg/ancli"
	"github.com/baalimago/go_away_boilerplate/pkg/misc"
	"github.com/baalimago/go_away_boilerplate/pkg/shutdown"
)

const usage = `clai - (c)ommand (l)ine (a)rtificial (i)ntelligence

Prerequisites:
  - Set the OPENAI_API_KEY environment variable to your OpenAI API key
  - Set the ANTHROPIC_API_KEY environment variable to your Anthropic API key
  - (Optional) Set the NO_COLOR environment variable to disable ansi color output
  - (Optional) Install glow - https://github.com/charmbracelet/glow for formated markdown output

Usage: clai [flags] <command>

Flags:
  -re, -reply bool             Set to true to reply to the previous query, meaning that it will be used as context for your next query. (default %v)
  -r, -raw bool                Set to true to print raw output (no animation, no glow). (default %v)
  -cm, -chat-model string      Set the chat model to use. (default is found in textConfig.json)
  -pm, -photo-model string     Set the image model to use. (default is found in photoConfig.json)
  -pd, -photo-dir string       Set the directory to store the generated pictures. (default %v)
  -pp, -photo-prefix string    Set the prefix for the generated pictures. (default %v)
  -I, -replace string          Set the string to replace with stdin. (default %v)
  -i bool                      Set to true to replace '-replace' flag value with stdin. This is overwritten by -I and -replace. (default %v)
  -t, -tools bool              Set to true to use text tools. Some models might not support streaming. (default %v)
  -g, -glob string             Set the glob to use for globbing. Same as glob mode. (default '%v')
  -p, -profile string          Set the profile which should be used. For details, see 'clai help profile'. (default '%v')

Commands:
  h|help                        Display this help message
  s|setup                       Setup the configuration files
  q|query <text>                Query the chat model with the given text
  p|photo <text>                Ask the photo model a picture with the requested prompt
  g|glob  <glob> <text>         Query the chat model with the contents of the files found by the glob and the given text
  cmd <text>                    Describe the command you wish to do, then execute the suggested command. It's a bit wonky when used with -re.

  c|chat   n|new       <prompt>   Create a new chat with the given prompt.
  c|chat   c|continue  <chatID>   Continue an existing chat with the given chat ID.
  c|chat   d|delete    <chatID>   Delete the chat with the given chat ID.
  c|chat   l|list                 List all existing chats.
  c|chat   h|help                 Display detailed help for chat subcommands.

Examples:
  - clai h | clai -i q generate some examples for this usage string: '{}'
  - clai query "What's the weather like in Tokyo?"
  - clai glob "*.txt" "Summarize these documents."
  - clai -cm claude-3-opus-20240229 chat new "What are the latest advancements in AI?"
  - clai photo "A futuristic cityscape"
  - clai -pm dall-e-2 photo A cat in space
  - clai -pd ~/Downloads -pp holiday A beach at sunset
  - docker logs example | clai -I LOG q "Find errors in these logs: LOG"
  - clai c new "Let's have a conversation about climate change."
  - clai c list
  - clai c help
`

func main() {
	if misc.Truthy(os.Getenv("DEBUG_CPU")) {
		f, err := os.Create("cpu_profile.prof")
		ok := true
		if err != nil {
			ancli.PrintErr(fmt.Sprintf("failed to create profiler file: %v", err))
		}
		if ok {
			defer f.Close()
			// Start the CPU profile
			err = pprof.StartCPUProfile(f)
			if err != nil {
				ancli.PrintErr(fmt.Sprintf("failed to start profiler : %v", err))
			}
			defer pprof.StopCPUProfile()
		}
	}

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
		ancli.PrintOK("things seems to have worked out. Bye bye! 🚀\n")
	}
}
