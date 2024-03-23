package main

import (
	"flag"
)

type flagSet struct {
	chatModel     string
	photoModel    string
	pictureDir    string
	picturePrefix string
	stdinReplace  string
	printRaw      bool
	replyMode     bool
}

func setupFlags(defaults flagSet) flagSet {
	cmShort := flag.String("cm", defaults.chatModel, "Set the chat model to use.  is gpt-4-turbo-preview. Mutually exclusive with chat-model flag.")
	cmLong := flag.String("chat-model", defaults.chatModel, "Set the chat model to use.  is gpt-4-turbo-preview. Mutually exclusive with cm flag.")

	pmShort := flag.String("pm", defaults.photoModel, "Set the image model to use.  is dall-e-3. Mutually exclusive with photo-model flag.")
	pmLong := flag.String("photo-model", defaults.photoModel, "Set the image model to use.  is dall-e-3. Mutually exclusive with pm flag.")

	pdShort := flag.String("pd", defaults.pictureDir, "Set the directory to store the generated pictures.  is $HOME/Pictures")
	pdLong := flag.String("picture-dir", defaults.pictureDir, "Set the directory to store the generated pictures.  is $HOME/Pictures")

	ppShort := flag.String("pp", defaults.picturePrefix, "Set the prefix for the generated pictures.  is 'clai'")
	ppLong := flag.String("picture-prefix", defaults.picturePrefix, "Set the prefix for the generated pictures.  is 'clai'")

	stdinReplaceShort := flag.String("I", defaults.stdinReplace, "Set the string to replace with stdin.  is '{}'. (flag syntax borrowed from xargs)")
	stdinReplaceLong := flag.String("replace", defaults.stdinReplace, "Set the string to replace with stdin.  is '{}. (flag syntax borrowed from xargs)'")
	defaultStdinReplace := flag.Bool("i", false, "Set to true to replace '{}' with stdin. This is overwritten by -I and -replace.  is false. (flag syntax borrowed from xargs)'")

	printRawShort := flag.Bool("r", defaults.printRaw, "Set to true to print raw output (don't attempt to use 'glow').  is false.")
	printRawLong := flag.Bool("raw", defaults.printRaw, "Set to true to print raw output (don't attempt to use 'glow').  is false.")

	replyShort := flag.Bool("re", defaults.replyMode, "Set to true to reply to the previous query, meaing that it will be used as context for your next query.  is false.")
	replyLong := flag.Bool("reply", defaults.replyMode, "Set to true to reply to the previous query, meaing that it will be used as context for your next query.  is false.")

	flag.Parse()
	chatModel, err := returnNonDefault(*cmShort, *cmLong, defaults.chatModel)
	exitWithFlagError(err, "cm", "chat-model")
	photoModel, err := returnNonDefault(*pmShort, *pmLong, defaults.photoModel)
	exitWithFlagError(err, "pm", "photo-model")
	pictureDir, err := returnNonDefault(*pdShort, *pdLong, defaults.pictureDir)
	exitWithFlagError(err, "pd", "picture-dir")
	picturePrefix, err := returnNonDefault(*ppShort, *ppLong, defaults.picturePrefix)
	exitWithFlagError(err, "pp", "picture-prefix")
	stdinReplace, err := returnNonDefault(*stdinReplaceShort, *stdinReplaceLong, defaults.stdinReplace)
	exitWithFlagError(err, "I", "replace")
	replyMode, err := returnNonDefault(*replyShort, *replyLong, defaults.replyMode)
	exitWithFlagError(err, "re", "reply")
	printRaw := *printRawShort || *printRawLong

	if *defaultStdinReplace && defaults.stdinReplace == "" {
		stdinReplace = "{}"
	}

	return flagSet{
		chatModel:     chatModel,
		photoModel:    photoModel,
		pictureDir:    pictureDir,
		picturePrefix: picturePrefix,
		stdinReplace:  stdinReplace,
		printRaw:      printRaw,
		replyMode:     replyMode,
	}
}
