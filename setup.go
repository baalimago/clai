package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/baalimago/go_away_boilerplate/pkg/ancli"
)

type PromptConfig struct {
	Photo string `yaml:"photo"`
	Query string `yaml:"query"`
}

func errorOnMutuallyExclusiveFlags[T comparable](flag1, flag2, dfault T, shortFlag, longFlag string) T {
	if flag1 != dfault && flag2 != dfault {
		ancli.PrintErr(fmt.Sprintf("%s and %s flags are mutually exclusive\n", shortFlag, longFlag))
		flag.PrintDefaults()
		os.Exit(1)
	}
	if flag1 != dfault {
		return flag1
	}
	if flag2 != dfault {
		return flag2
	}
	return dfault
}

func setup() (string, chatModelQuerier, photoQuerier, []string) {
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

	picturePrefixDefault := "clai"
	ppShort := flag.String("pp", picturePrefixDefault, "Set the prefix for the generated pictures. Default is 'clai'")
	ppLong := flag.String("picture-prefix", picturePrefixDefault, "Set the prefix for the generated pictures. Default is 'clai'")

	stdinReplace := ""
	stdinReplaceShort := flag.String("I", stdinReplace, "Set the string to replace with stdin. Default is '{}'. (flag syntax borrowed from xargs)")
	stdinReplaceLong := flag.String("replace", stdinReplace, "Set the string to replace with stdin. Default is '{}. (flag syntax borrowed from xargs)'")
	defaultStdinReplace := flag.Bool("i", false, "Set to true to replace '{}' with stdin. This is overwritten by -I and -replace. Default is false. (flag syntax borrowed from xargs)'")

	printRawDefault := false
	printRawShort := flag.Bool("r", printRawDefault, "Set to true to print raw output (don't attempt to use 'glow'). Default is false.")
	printRawLong := flag.Bool("raw", printRawDefault, "Set to true to print raw output (don't attempt to use 'glow'). Default is false.")

	replyDefault := false
	replyShort := flag.Bool("re", replyDefault, "Set to true to reply to the previous query, meaing that it will be used as context for your next query. Default is false.")
	replyLong := flag.Bool("reply", replyDefault, "Set to true to reply to the previous query, meaing that it will be used as context for your next query. Default is false.")

	flag.Parse()
	chatModel := errorOnMutuallyExclusiveFlags(*cmShort, *cmLong, chatModelDefault, "cm", "chat-model")
	photoModel := errorOnMutuallyExclusiveFlags(*pmShort, *pmLong, photoModelDefault, "pm", "photo-model")
	pictureDir := errorOnMutuallyExclusiveFlags(*pdShort, *pdLong, pictureDirDefault, "pd", "picture-dir")
	picturePrefix := errorOnMutuallyExclusiveFlags(*ppShort, *ppLong, picturePrefixDefault, "pp", "picture-prefix")
	stdinReplace = errorOnMutuallyExclusiveFlags(*stdinReplaceShort, *stdinReplaceLong, stdinReplace, "I", "replace")
	replyMode := errorOnMutuallyExclusiveFlags(*replyShort, *replyLong, replyDefault, "re", "reply")
	printRaw := *printRawShort || *printRawLong

	if *defaultStdinReplace && stdinReplace == "" {
		stdinReplace = "{}"
	}

	API_KEY := os.Getenv("OPENAI_API_KEY")
	if API_KEY == "" {
		ancli.PrintErr("OPENAI_API_KEY environment variable not set\n")
		os.Exit(1)
	}
	cmq := chatModelQuerier{
		SystemPrompt: "You are an assistent for a CLI interface. Answer concisely and informatively. Prefer markdown if possible.",
		Raw:          printRaw,
		Url:          "https://api.openai.com/v1/chat/completions",
		replyMode:    replyMode,
	}
	pq := photoQuerier{
		PictureDir:    pictureDir,
		PicturePrefix: picturePrefix,
		PromptFormat:  "I NEED to test how the tool works with extremely simple prompts. DO NOT add any detail, just use it AS-IS: '%v'",
		raw:           printRaw,
	}

	homedirConfig(&cmq, &pq)
	// Flag overrides homedirConfig
	if chatModel != chatModelDefault {
		cmq.Model = chatModel
	}
	if printRaw {
		cmq.Raw = true
	}
	if photoModel != photoModelDefault {
		pq.Model = photoModel
	}
	if picturePrefix != picturePrefixDefault {
		pq.PicturePrefix = picturePrefix
	}
	if pictureDir != pictureDirDefault {
		pq.PictureDir = pictureDir
	}
	if os.Getenv("DEBUG") == "true" {
		ancli.PrintOK(fmt.Sprintf("chatModel: %v\n", cmq))
	}
	return API_KEY, cmq, pq, parseArgsStdin(stdinReplace)
}

func parseArgsStdin(stdinReplace string) []string {
	if os.Getenv("DEBUG") == "true" {
		ancli.PrintOK(fmt.Sprintf("stdinReplace: %v\n", stdinReplace))
	}
	args := flag.Args()
	fi, err := os.Stdin.Stat()
	if err != nil {
		panic(err)
	}
	var hasPipe bool
	if fi.Mode()&os.ModeNamedPipe == 0 {
		hasPipe = false
	} else {
		hasPipe = true
	}
	if len(args) == 1 && !hasPipe {
		if args[0] == "h" || args[0] == "help" || args[0] == "--help" || args[0] == "-h" {
			fmt.Print(usage)
			os.Exit(0)
		}
		ancli.PrintErr("found no prompt, set args or pipe in some string\n")
		fmt.Print(usage)
		os.Exit(1)
	}
	// If no data is in stdin, simply return args
	if !hasPipe {
		return args
	}

	inputData, err := io.ReadAll(os.Stdin)
	if err != nil {
		ancli.PrintErr(fmt.Sprintf("failed to read stdin: %v", err))
		os.Exit(1)
	}
	// There is data to read from stdin, so read it
	if err != nil {
		ancli.PrintErr("failed to read from stdin\n")
		os.Exit(1)
	}
	pipeIn := string(inputData)
	if len(args) == 1 {
		args = append(args, strings.Split(pipeIn, " ")...)
	}

	// Replace all occurrence of stdinReplaceSignal with pipeIn
	for i, arg := range args {
		if strings.Contains(arg, stdinReplace) {
			args[i] = strings.ReplaceAll(arg, stdinReplace, pipeIn)
		}
	}

	if os.Getenv("DEBUG") == "true" {
		ancli.PrintOK(fmt.Sprintf("args: %v\n", args))
	}
	return args
}
