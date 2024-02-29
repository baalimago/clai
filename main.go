package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/baalimago/go_away_boilerplate/pkg/ancli"
)

const usage = `Goai - Go do AI stuff

Prerequisits:
  - Set the OPENAI_API_KEY environment variable to your OpenAI API key

Usage: goai [flags] <command>

Flags:
  -cm, --chat-model string    Set the chat model to use. Default is 'gpt-4-turbo-preview'. Short and long flags are mutually exclusive.
  -pm, --photo-model string   Set the image model to use. Default is 'dall-e-3'. Short and long flags are mutually exclusive.

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

func main() {
	chatModelDefault := "gpt-4-turbo-preview"
	cmShort := flag.String("cm", chatModelDefault, "Set the chat model to use. Default is gpt-4-turbo-preview. Mutually exclusive with chat-model flag.")
	cmLong := flag.String("chat-model", chatModelDefault, "Set the chat model to use. Default is gpt-4-turbo-preview. Mutually exclusive with cm flag.")

	photoModelDefault := "dall-e-3"
	pmShort := flag.String("pm", photoModelDefault, "Set the image model to use. Default is dall-e-3. Mutually exclusive with photo-model flag.")
	pmLong := flag.String("photo-model", photoModelDefault, "Set the image model to use. Default is dall-e-3. Mutually exclusive with pm flag.")

	pictureDirDefault := "./pictures"
	pdShort := flag.String("pd", pictureDirDefault, "Set the directory to store the generated pictures. Default is ./pictures")
	pdLong := flag.String("picture-dir", pictureDirDefault, "Set the directory to store the generated pictures. Default is ./pictures")

	flag.Parse()
	chatModel := errorOnMutuallyExclusiveFlags(*cmShort, *cmLong, "cm", "chat-model", chatModelDefault)
	photoModel := errorOnMutuallyExclusiveFlags(*pmShort, *pmLong, "pm", "photo-model", photoModelDefault)
	pictureDir := errorOnMutuallyExclusiveFlags(*pdShort, *pdLong, "pd", "picture-dir", pictureDirDefault)

	API_KEY := os.Getenv("OPENAI_API_KEY")
	if API_KEY == "" {
		ancli.PrintErr("OPENAI_API_KEY environment variable not set\n")
		os.Exit(1)
	}

	args := flag.Args()
	if len(args) == 0 {
		ancli.PrintErr("No command specified")
		fmt.Print(usage)
		os.Exit(1)
	}

	ctx := context.Background()
	switch args[0] {
	case "text":
		fallthrough
	case "t":
		err := queryChatModel(ctx, chatModel, API_KEY, args[1:])
		if err != nil {
			ancli.PrintErr(err.Error() + "\n")
			os.Exit(1)
		}
	case "photo":
		fallthrough
	case "p":
		err := queryPhotoModel(ctx, photoModel, API_KEY, pictureDir, args[1:])
		if err != nil {
			ancli.PrintErr(err.Error() + "\n")
			os.Exit(1)
		}
	default:
		ancli.PrintErr(fmt.Sprintf("Unknown command: %s\n", args[0]))
		os.Exit(1)
	}
}
