package main

import (
	"bufio"
	"encoding/json"
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

func parsePrompts(promptPath string) (string, string, error) {
	var prompts PromptConfig
	file, err := os.Open(promptPath)
	if err != nil {
		return "", "", fmt.Errorf("failed to open prompts file: %w", err)
	}
	defer file.Close()
	fileBytes, err := io.ReadAll(file)
	if err != nil {
		return "", "", fmt.Errorf("failed to read prompts file: %w", err)
	}
	err = json.Unmarshal(fileBytes, &prompts)
	if err != nil {
		return "", "", fmt.Errorf("failed to unmarshal prompts file: %w", err)
	}
	return prompts.Query, prompts.Photo, nil
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

	picturePrefix := "goai"
	ppShort := flag.String("pp", picturePrefix, "Set the prefix for the generated pictures. Default is 'goai'")
	ppLong := flag.String("picture-prefix", picturePrefix, "Set the prefix for the generated pictures. Default is 'goai'")

	stdinReplace := ""
	stdinReplaceShort := flag.String("I", stdinReplace, "Set the string to replace with stdin. Default is '{}'. (flag syntax borrowed from xargs)")
	stdinReplaceLong := flag.String("replace", stdinReplace, "Set the string to replace with stdin. Default is '{}. (flag syntax borrowed from xargs)'")
	defaultStdinReplace := flag.Bool("i", false, "Set to true to replace '{}' with stdin. This is overwritten by -I and -replace. Default is false. (flag syntax borrowed from xargs)'")

	printRawDefault := false
	printRawShort := flag.Bool("r", printRawDefault, "Set to true to print raw output (don't attempt to use 'glow'). Default is false.")
	printRawLong := flag.Bool("raw", printRawDefault, "Set to true to print raw output (don't attempt to use 'glow'). Default is false.")

	flag.Parse()
	chatModel := errorOnMutuallyExclusiveFlags(*cmShort, *cmLong, "cm", "chat-model", chatModelDefault)
	photoModel := errorOnMutuallyExclusiveFlags(*pmShort, *pmLong, "pm", "photo-model", photoModelDefault)
	pictureDir := errorOnMutuallyExclusiveFlags(*pdShort, *pdLong, "pd", "picture-dir", pictureDirDefault)
	picturePrefix = errorOnMutuallyExclusiveFlags(*ppShort, *ppLong, "pp", "picture-prefix", picturePrefix)
	stdinReplace = errorOnMutuallyExclusiveFlags(*stdinReplaceShort, *stdinReplaceLong, "I", "replace", stdinReplace)
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
		systemPrompt: "You are an assistent for a CLI interface. Answer concisely and informatively. Prefer markdown if possible.",
		glowify:      !printRaw,
	}
	pq := photoQuerier{
		pictureDir:    pictureDir,
		picturePrefix: picturePrefix,
		promptFormat:  "I NEED to test how the tool works with extremely simple prompts. DO NOT add any detail, just use it AS-IS: '%v'",
	}

	homedirConfig(&cmq, &pq)
	cmq.model = chatModel
	pq.model = photoModel
	return API_KEY, cmq, pq, parseArgsStdin(stdinReplace)
}

func parseArgsStdin(stdinReplace string) []string {
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
		if arg == stdinReplace {
			args[i] = pipeIn
		}
	}

	return args
}
