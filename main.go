package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"runtime/pprof"

	"github.com/baalimago/clai/internal"
	"github.com/baalimago/clai/internal/utils"
	"github.com/baalimago/go_away_boilerplate/pkg/ancli"
	"github.com/baalimago/go_away_boilerplate/pkg/misc"
	"github.com/baalimago/go_away_boilerplate/pkg/shutdown"
)

const usage = `clai - (c)ommand (l)ine (a)rtificial (i)ntelligence

Prerequisites:
  - Set the environment variable to your API key according to the vendor you seek to use
  - (Optional) Set the NO_COLOR environment variable to disable ansi color output
  - (Optional) Install glow - https://github.com/charmbracelet/glow for formatted markdown output

Usage: clai [flags] <command>

Flags:
  -re, -reply bool             Set to true to reply to the previous query, meaning that it will be used as context for your next query. (default %v)
  -r, -raw bool                Set to true to print raw output (no animation, no glow). (default %v)
  -cm, -chat-model string      Set the chat model to use. (default is found in %v/textConfig.json)
  -pm, -photo-model string     Set the image model to use. (default is found in %v/photoConfig.json)
  -pd, -photo-dir string       Set the directory to store the generated pictures. (default is found in %v/photoConfig.json)
  -pp, -photo-prefix string    Set the prefix for the generated pictures. (default is found in %v/photoConfig.json)
  -vd, -video-dir string       Set the directory to store the generated videos. (default %v)
  -vp, -video-prefix string    Set the prefix for the generated videos. (default %v)
  -t, -tools string            Set to <tool_a>,<tool_b> for specific tool, or */"" to use all built in or MCP tools. See available tools with 'clai tools' (default %v)
  -g, -glob string             Set the glob to use for globbing. (default '%v')
  -p, -profile string          Set the profile which should be used. For details, see 'clai help profile'. (default '%v')
  -prp, profile-path string    Set the path to a profile file to use instead of -p/-profile.

Config dir: %v
Cache dir:  %v

Commands:
  h|help                        Display this help message
  s|setup                       Setup the configuration files
  q|query <text>                Query the chat model with the given text
  p|photo <text>                Ask the photo model for a picture with the given prompt
  v|video <text>                Ask the video model for a video with the given prompt
  re|replay                     Replay the most recent message.
  t|tools [tool name]           List available tools, both mcp and built-in. Or show details for a specific tool.

  c|chat   n|new       <prompt>   Create a new chat with the given prompt.
  c|chat   c|continue  <chatID>   Continue an existing chat with the given chat ID or index.
  c|chat   d|delete    <chatID>   Delete the chat with the given chat ID or index.
  c|chat   l|list                 List all existing chats.
  c|chat   h|help                 Display detailed help for chat subcommands.

Examples:
  - clai h | clai query generate some examples for this usage string: 
  - clai -t website_text query "What's the weather like in Tokyo? Use website_text to fetch data"
  - clai -glob "*.txt" query Please summarize these documents: 
  - clai -cm claude-3-opus-20240229 chat new "What are the latest advancements in AI?"
  - clai -pm dall-e-2 photo A cat in space
  - docker logs example | clai -I LOG q "Find errors in these logs: LOG"
  - clai c new "Let's have a conversation about climate change."
  - clai c list
  - clai c help
`

func main() {
	ancli.SetupSlog()
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

	configDirPath, err := utils.GetClaiConfigDir()
	if err != nil {
		ancli.Errf("failed to find config dir path: %v", err)
		os.Exit(1)
	}

	err = utils.CreateConfigDir(configDirPath)
	if err != nil {
		ancli.Errf("failed to find config dir path: %v", err)
		os.Exit(1)
	}

	ctx, cancel := context.WithCancel(context.Background())
	// Build in cancel into the context to allow it to be called downstream
	// Anti-pattern? Not sure, honestly, needed here to cleanly stop
	// clai in case of nested tool calls. Could've been solved by proper structure
	// but who has time for proper structure?
	ctx = context.WithValue(ctx, utils.ContextCancelKey, cancel)
	querier, err := internal.Setup(ctx, usage)
	if err != nil {
		if errors.Is(err, utils.ErrUserInitiatedExit) {
			ancli.Okf("Seems like you wanted out. Byebye!\n")
			os.Exit(0)
		}
		ancli.PrintErr(fmt.Sprintf("failed to setup: %v\n", err))
		os.Exit(1)
	}
	go func() { shutdown.Monitor(cancel) }()
	err = querier.Query(ctx)
	if err != nil {
		if errors.Is(err, utils.ErrUserInitiatedExit) {
			ancli.Okf("Seems like you wanted out. Byebye!\n")
			os.Exit(0)
		} else {
			ancli.PrintErr(fmt.Sprintf("failed to run: %v\n", err))
			os.Exit(1)
		}
	}
	cancel()
	if misc.Truthy(os.Getenv("DEBUG")) {
		ancli.PrintOK("things seems to have worked out. Bye bye! 🚀\n")
	}
}
