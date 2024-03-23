package main

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"

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
  -re, --reply bool             Set to true to reply to the previous query, meaing that it will be used as context for your next query. Default is false.
  -r, --raw bool                Set to true to print raw output (no animation, no glow). Default is false.
  -cm, --chat-model string      Set the chat model to use. Default is 'gpt-4-turbo-preview'. Short and long flags are mutually exclusive.
  -pm, --photo-model string     Set the image model to use. Default is 'dall-e-3'. Short and long flags are mutually exclusive.
  -pd, --picture-dir string     Set the directory to store the generated pictures. Default is $HOME/Pictures. Short and long flags are mutually exclusive.
  -pp, --picture-prefix string  Set the prefix for the generated pictures. Default is 'clai'. Short and long flags are mutually exclusive.
  -I, --replace string          Set the string to replace with stdin. Default is '{}'. (flag syntax borrowed from xargs)
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
  - clai -cm gpt-3.5-turbo chat Latest advancements in AI?
  - clai photo "A futuristic cityscape"
  - clai -pm dall-e-2 photo A cat in space
  - clai -pd ~/Downloads -pp holiday A beach at sunset
  - docker logs example | clai -I LOG q "Find errors in these logs: LOG"
  - clai c new "Let's have a conversation about climate change."
  - clai c list
  - clai c help
`

func run(ctx context.Context, API_KEY string, cq chatModelQuerier, pq photoQuerier, args []string) error {
	cmd := args[0]
	if os.Getenv("DEBUG") == "true" {
		ancli.PrintOK(fmt.Sprintf("args: %s\n", args))
	}
	switch cmd {
	case "query":
		fallthrough
	case "q":
		msgs := make([]Message, 0)
		if cq.replyMode {
			c, err := readPreviousQuery()
			if err != nil {
				if errors.Is(err, fs.ErrNotExist) {
					ancli.PrintWarn("no previous query found\n")
				} else {
					return fmt.Errorf("failed to read previous query: %w", err)
				}
			}
			msgs = append(msgs, c.Messages...)
		} else {
			msgs = append(msgs, Message{Role: "system", Content: cq.SystemPrompt})
		}
		msgs = append(msgs, Message{Role: "user", Content: strings.Join(args[1:], " ")})
		msg, err := cq.streamCompletions(ctx, API_KEY, msgs)
		msgs = append(msgs, msg)
		cq.saveAsPreviousQuery(msgs)
		if err != nil && !errors.Is(err, context.Canceled) {
			return fmt.Errorf("failed to query chat model: %w", err)
		}
		return nil

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
		_, err = cq.streamCompletions(ctx, API_KEY, msgs)
		return err
	case "chat":
		fallthrough
	case "c":
		err := cq.chat(ctx, API_KEY, args[1], args[2:])
		if err != nil {
			return fmt.Errorf("failed to chat: %w", err)
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
	go func() { shutdown.Monitor(cancel) }()
	err := run(ctx, API_KEY, cmq, pq, args)
	if err != nil {
		ancli.PrintErr(err.Error() + "\n")
		os.Exit(1)
	}
	cancel()
	if os.Getenv("DEBUG") == "true" {
		ancli.PrintOK("things seems to have worked out. Good bye! 🚀\n")
	}
}
