package main

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"

	"github.com/baalimago/clai/internal"
	"github.com/baalimago/go_away_boilerplate/pkg/ancli"
	"github.com/baalimago/go_away_boilerplate/pkg/misc"
	"github.com/baalimago/go_away_boilerplate/pkg/shutdown"
)

const usage = `clai - (c)ommand (l)ine (a)rtificial (i)ntelligence

Prerequisits:
  - Set the OPENAI_API_KEY environment variable to your OpenAI API key
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
  - clai -cm gpt-3.5-turbo chat Latest advancements in AI?
  - clai photo "A futuristic cityscape"
  - clai -pm dall-e-2 photo A cat in space
  - clai -pd ~/Downloads -pp holiday A beach at sunset
  - docker logs example | clai -I LOG q "Find errors in these logs: LOG"
  - clai c new "Let's have a conversation about climate change."
  - clai c list
  - clai c help
`

func run(ctx context.Context, API_KEY string, cq internal.ChatModelQuerier, pq internal.PhotoQuerier, args []string) error {
	cmd := args[0]
	if misc.Truthy(os.Getenv("DEBUG")) {
		ancli.PrintOK(fmt.Sprintf("args: %s\n", args))
	}
	switch cmd {
	case "query":
		fallthrough
	case "q":
		msgs := make([]internal.Message, 0)
		replyDebugMode := misc.Truthy(os.Getenv("DEBUG_REPLY_MODE"))
		if replyDebugMode {
			ancli.PrintOK(fmt.Sprintf("reply mode active: %v\n", replyDebugMode))
		}
		if cq.ReplyMode {
			c, err := internal.ReadPreviousQuery()
			if err != nil {
				if errors.Is(err, fs.ErrNotExist) {
					ancli.PrintWarn("no previous query found\n")
				} else {
					return fmt.Errorf("failed to read previous query: %w", err)
				}
			}
			msgs = append(msgs, c.Messages...)
		} else {
			msgs = append(msgs, internal.Message{Role: "system", Content: cq.SystemPrompt})
		}
		msgs = append(msgs, internal.Message{Role: "user", Content: strings.Join(args[1:], " ")})
		if replyDebugMode {
			ancli.PrintOK(fmt.Sprintf("messages pre-stream: %+v\n", msgs))
		}
		msg, err := cq.StreamCompletions(ctx, API_KEY, msgs)
		msgs = append(msgs, msg)
		cq.SaveAsPreviousQuery(msgs)
		if err != nil && !errors.Is(err, context.Canceled) {
			return fmt.Errorf("failed to query chat model: %w", err)
		}
		return nil

	case "photo":
		fallthrough
	case "p":
		err := pq.QueryPhotoModel(ctx, API_KEY, args[1:])
		if err != nil {
			return fmt.Errorf("failed to query photo model: %w", err)
		}
	case "glob":
		fallthrough
	case "g":
		glob := args[1]
		if !strings.Contains(glob, "*") {
			ancli.PrintWarn(fmt.Sprintf("argument: '%v' does not seem to contain a wildcard '*', has it been properly enclosed?\n", glob))
		}
		globMessages, err := internal.ParseGlob(glob)
		if err != nil {
			return fmt.Errorf("failed to parse glob: %w", err)
		}
		msgs, err := cq.ConstructGlobMessages(globMessages, args[2:])
		if err != nil {
			return fmt.Errorf("failed to construct glob messages: %w", err)
		}
		if misc.Truthy(os.Getenv("DEBUG")) {
			ancli.PrintOK(fmt.Sprintf("constructed messages: %v\n", msgs))
		}
		_, err = cq.StreamCompletions(ctx, API_KEY, msgs)
		return err
	case "chat":
		fallthrough
	case "c":
		err := cq.Chat(ctx, API_KEY, args[1], args[2:])
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
	API_KEY, cmq, pq, args := internal.Setup(usage)
	ctx, cancel := context.WithCancel(context.Background())
	go func() { shutdown.Monitor(cancel) }()
	err := run(ctx, API_KEY, cmq, pq, args)
	if err != nil {
		ancli.PrintErr(err.Error() + "\n")
		os.Exit(1)
	}
	cancel()
	if misc.Truthy(os.Getenv("DEBUG")) {
		ancli.PrintOK("things seems to have worked out. Good bye! 🚀\n")
	}
}
