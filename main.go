package main

import (
	"context"
	"fmt"
	"os"

	"github.com/baalimago/go_away_boilerplate/pkg/ancli"
	"github.com/baalimago/go_away_boilerplate/pkg/shutdown"
)

const usage = `clai - (c)ommand (l)ine (a)rtificial (i)ntelligence

Prerequisits:
  - Set the OPENAI_API_KEY environment variable to your OpenAI API key
  - (Optional) Set the NO_COLOR environment variable to disable ansi color output
  - (Optional) Install glow - https://github.com/charmbracelet/glow for formated markdown output

Usage: clai [flags] <command>

Flags:
  -cm, --chat-model string      Set the chat model to use. Default is 'gpt-4-turbo-preview'. Short and long flags are mutually exclusive.
  -pm, --photo-model string     Set the image model to use. Default is 'dall-e-3'. Short and long flags are mutually exclusive.
  -pd, --picture-dir string     Set the directory to store the generated pictures. Default is $HOME/Pictures. Short and long flags are mutually exclusive.
  -pp, --picture-prefix string  Set the prefix for the generated pictures. Default is 'clai'. Short and long flags are mutually exclusive.
  -I, --replace string          Set the string to replace with stdin. Default is '{}'. (flag syntax borrowed from xargs)
  -i bool                       Set to true to replace '{}' with stdin. This is overwritten by -I and -replace. Default is false. (flag syntax borrowed from xargs)

Commands:
  q <text> Query the chat model with the given text
  p <text> Ask the photo model a picture with the requested prompt
  g <glob> <text> Query the chat model with the contents of the files found by the glob and the given text
`

func run(ctx context.Context, API_KEY string, cq chatModelQuerier, pq photoQuerier, args []string) error {
	cmd := args[0]
	if os.Getenv("DEBUG") == "true" {
		ancli.PrintOK(fmt.Sprintf("command: %s\n", cmd))
	}
	switch cmd {
	case "query":
		fallthrough
	case "q":
		err := cq.queryChatModel(ctx, API_KEY, cq.constructMessages(args[1:]))
		if err != nil {
			return fmt.Errorf("failed to query chat model: %w", err)
		}
	case "photo":
		fallthrough
	case "p":
		err := pq.queryPhotoModel(ctx, API_KEY, args[1:])
		if err != nil {
			return fmt.Errorf("failed to query photo model: %w", err)
		}
	case "glob":
		fallthrough
	case "g":
		msgs, err := cq.constructGlobMessages(args[1], args[2:])
		if err != nil {
			return fmt.Errorf("failed to construct glob messages: %w", err)
		}
		if os.Getenv("DEBUG") == "true" {
			ancli.PrintOK(fmt.Sprintf("constructed messages: %v\n", msgs))
		}
		err = cq.queryChatModel(ctx, API_KEY, msgs)
		if err != nil {
			return fmt.Errorf("failed to query chat model with glob: %w", err)
		}
	case "h":
		fallthrough
	case "help":
		fmt.Print(usage)
	default:
		return fmt.Errorf("unknown command: '%s'\n%v", args[0], usage)
	}

	return nil
}

func main() {
	API_KEY, cmq, pq, args := setup()
	ctx, cancel := context.WithCancel(context.Background())
	go shutdown.Monitor(cancel)
	go func() {
		err := run(ctx, API_KEY, cmq, pq, args)
		if err != nil {
			ancli.PrintErr(err.Error() + "\n")
			os.Exit(1)
		}
		cancel()
	}()
	<-ctx.Done()
	if os.Getenv("DEBUG") == "true" {
		ancli.PrintOK("things seems to have worked out. Good bye! 🚀\n")
	}
}
