package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/baalimago/go_away_boilerplate/pkg/ancli"
	"github.com/baalimago/go_away_boilerplate/pkg/shutdown"
)

const usage = `Goai - Go do AI stuff

Prerequisits:
  - Set the OPENAI_API_KEY environment variable to your OpenAI API key

Usage: goai [flags] <command>

Flags:
  -cm, --chat-model string      Set the chat model to use. Default is 'gpt-4-turbo-preview'. Short and long flags are mutually exclusive.
  -pm, --photo-model string     Set the image model to use. Default is 'dall-e-3'. Short and long flags are mutually exclusive.
  -pd, --picture-dir string     Set the directory to store the generated pictures. Default is $HOME/Pictures. Short and long flags are mutually exclusive.
  -pp, --picture-prefix string  Set the prefix for the generated pictures. Default is 'goai'. Short and long flags are mutually exclusive.
  -I, --replace string          Set the string to replace with stdin. Default is '{}'. (flag syntax borrowed from xargs)
  -i bool                       Set to true to replace '{}' with stdin. This is overwritten by -I and -replace. Default is false. (flag syntax borrowed from xargs)

Commands:
  t <text> Query the chat model with the given text
  p <text> Query the photo model with the given text
`

func errorOnMutuallyExclusiveFlags(flag1, flag2, shortFlag, longFlag, defualt string) string {
	if flag1 != defualt && flag2 != defualt {
		ancli.PrintErr(fmt.Sprintf("%s and %s flags are mutually exclusive\n", shortFlag, longFlag))
		flag.PrintDefaults()
		os.Exit(1)
	}
	if flag1 != defualt {
		return flag1
	}
	if flag2 != defualt {
		return flag2
	}
	return defualt
}

func setup() (string, string, string, string, string, string) {
	chatModelDefault := "gpt-4-turbo-preview"
	cmShort := flag.String("cm", chatModelDefault, "Set the chat model to use. Default is gpt-4-turbo-preview. Mutually exclusive with chat-model flag.")
	cmLong := flag.String("chat-model", chatModelDefault, "Set the chat model to use. Default is gpt-4-turbo-preview. Mutually exclusive with cm flag.")

	photoModelDefault := "dall-e-3"
	pmShort := flag.String("pm", photoModelDefault, "Set the image model to use. Default is dall-e-3. Mutually exclusive with photo-model flag.")
	pmLong := flag.String("photo-model", photoModelDefault, "Set the image model to use. Default is dall-e-3. Mutually exclusive with pm flag.")

	home := os.Getenv("HOME")
	pictureDirDefault := fmt.Sprintf("%v/Pictures", home)
	pdShort := flag.String("pd", pictureDirDefault, "Set the directory to store the generated pictures. Default is $HOME/Pictures")
	pdLong := flag.String("picture-dir", pictureDirDefault, "Set the directory to store the generated pictures. Default is $HOME/Pictures")

	picturePrefix := "goai"
	ppShort := flag.String("pp", picturePrefix, "Set the prefix for the generated pictures. Default is 'goai'")
	ppLong := flag.String("picture-prefix", picturePrefix, "Set the prefix for the generated pictures. Default is 'goai'")

	stdinReplace := ""
	stdinReplaceShort := flag.String("I", stdinReplace, "Set the string to replace with stdin. Default is '{}'. (flag syntax borrowed from xargs)")
	stdinReplaceLong := flag.String("replace", stdinReplace, "Set the string to replace with stdin. Default is '{}. (flag syntax borrowed from xargs)'")
	defaultStdinReplace := flag.Bool("i", false, "Set to true to replace '{}' with stdin. This is overwritten by -I and -replace. Default is false. (flag syntax borrowed from xargs)'")

	flag.Parse()
	chatModel := errorOnMutuallyExclusiveFlags(*cmShort, *cmLong, "cm", "chat-model", chatModelDefault)
	photoModel := errorOnMutuallyExclusiveFlags(*pmShort, *pmLong, "pm", "photo-model", photoModelDefault)
	pictureDir := errorOnMutuallyExclusiveFlags(*pdShort, *pdLong, "pd", "picture-dir", pictureDirDefault)
	picturePrefix = errorOnMutuallyExclusiveFlags(*ppShort, *ppLong, "pp", "picture-prefix", picturePrefix)
	stdinReplace = errorOnMutuallyExclusiveFlags(*stdinReplaceShort, *stdinReplaceLong, "I", "replace", stdinReplace)

	if *defaultStdinReplace && stdinReplace == "" {
		stdinReplace = "{}"
	}

	API_KEY := os.Getenv("OPENAI_API_KEY")
	if API_KEY == "" {
		ancli.PrintErr("OPENAI_API_KEY environment variable not set\n")
		os.Exit(1)
	}
	return chatModel, photoModel, pictureDir, API_KEY, picturePrefix, stdinReplace
}

func run(ctx context.Context, args []string, chatModel, photoModel, pictureDir, API_KEY, picturePrefix string) error {
	switch args[0] {
	case "text":
		fallthrough
	case "t":
		cmq := chatModelQuerier{
			Model:           chatModel,
			AssistentPrompt: "You are an assistent for a CLI interface. Answer concisely and informatively. Prefer markdown if possible.",
		}
		err := cmq.queryChatModel(ctx, chatModel, API_KEY, args[1:])
		if err != nil {
			return fmt.Errorf("failed to query chat model: %w", err)
		}
	case "photo":
		fallthrough
	case "p":
		pq := photoQuerier{
			model:         photoModel,
			API_KEY:       API_KEY,
			pictureDir:    pictureDir,
			picturePrefix: picturePrefix,
		}
		err := pq.queryPhotoModel(ctx, args[1:])
		if err != nil {
			return fmt.Errorf("failed to query photo model: %w", err)
		}
	default:
		return fmt.Errorf("unknown command: %s", args[0])
	}

	return nil
}

func parseArgsStdin(stdinReplaceSignal string) []string {
	args := flag.Args()
	file := os.Stdin
	fi, err := file.Stat()
	if err != nil {
		ancli.PrintErr(fmt.Sprintf("failed to stat stdin: %v", err))
		os.Exit(1)
	}
	size := fi.Size()
	if len(args) == 1 && size == 0 {
		ancli.PrintErr("found no prompt, set args or pipe in some string\n")
		fmt.Print(usage)
		os.Exit(1)
	}
	// If no data is in stdin, simply return args
	if size == 0 {
		return args
	}

	// There is data to read from stdin, so read it
	inputData, err := io.ReadAll(bufio.NewReader(os.Stdin))
	if err != nil {
		ancli.PrintErr("failed to read from stdin\n")
		os.Exit(1)
	}
	pipeIn := string(inputData)
	if len(args) == 1 && len(pipeIn) == 0 {
		ancli.PrintErr("found no prompt, set args or pipe in some string\n")
		fmt.Print(usage)
		os.Exit(1)
	}

	if len(args) == 1 {
		args = append(args, strings.Split(pipeIn, " ")...)
	}

	// Replace all occurances of stdinReplaceSignal with pipeIn
	for i, arg := range args {
		if arg == stdinReplaceSignal {
			args[i] = pipeIn
		}
	}

	return args
}

func main() {
	chatModel, photoModel, pictureDir, API_KEY, picturePrefix, stdinReplaceSignal := setup()
	args := parseArgsStdin(stdinReplaceSignal)
	ctx, cancel := context.WithCancel(context.Background())
	go shutdown.Monitor(cancel)
	go func() {
		err := run(ctx, args, chatModel, photoModel, pictureDir, API_KEY, picturePrefix)
		if err != nil {
			ancli.PrintErr(err.Error() + "\n")
			os.Exit(1)
		}
		cancel()
	}()
	<-ctx.Done()
	if os.Getenv("VERBOSE") == "true" {
		ancli.PrintOK("things seems to have worked out. Good bye! 🚀\n")
	}
}
