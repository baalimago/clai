package internal

import (
	"flag"
	"fmt"
	"os"

	"github.com/baalimago/clai/internal/photo"
	"github.com/baalimago/clai/internal/text"
	"github.com/baalimago/clai/internal/utils"
	"github.com/baalimago/go_away_boilerplate/pkg/ancli"
)

type Configurations struct {
	ChatModel    string
	PhotoModel   string
	PhotoDir     string
	PhotoPrefix  string
	PhotoOutput  string
	Vendor       string
	StdinReplace string
	PrintRaw     bool
	ReplyMode    bool
	UseTools     bool
}

func setupFlags(defaults Configurations) Configurations {
	cmShort := flag.String("cm", defaults.ChatModel, "Set the chat model to use.  is gpt-4-turbo-preview. Mutually exclusive with chat-model flag.")
	cmLong := flag.String("chat-model", defaults.ChatModel, "Set the chat model to use.  is gpt-4-turbo-preview. Mutually exclusive with cm flag.")

	pmShort := flag.String("pm", defaults.PhotoModel, "Set the image model to use.  is dall-e-3. Mutually exclusive with photo-model flag.")
	pmLong := flag.String("photo-model", defaults.PhotoModel, "Set the image model to use.  is dall-e-3. Mutually exclusive with pm flag.")

	pdShort := flag.String("pd", defaults.PhotoDir, "Set the directory to store the generated pictures.  is $HOME/Pictures")
	pdLong := flag.String("photo-dir", defaults.PhotoDir, "Set the directory to store the generated pictures.  is $HOME/Pictures")

	ppShort := flag.String("pp", defaults.PhotoPrefix, "Set the prefix for the generated pictures.  is 'clai'")
	ppLong := flag.String("photo-prefix", defaults.PhotoPrefix, "Set the prefix for the generated pictures.  is 'clai'")

	stdinReplaceShort := flag.String("I", defaults.StdinReplace, "Set the string to replace with stdin.  is '{}'. (flag syntax borrowed from xargs)")
	stdinReplaceLong := flag.String("replace", defaults.StdinReplace, "Set the string to replace with stdin.  is '{}. (flag syntax borrowed from xargs)'")
	defaultStdinReplace := flag.Bool("i", false, "Set to true to replace '{}' with stdin. This is overwritten by -I and -replace.  is false. (flag syntax borrowed from xargs)'")

	printRawShort := flag.Bool("r", defaults.PrintRaw, "Set to true to print raw output (don't attempt to use 'glow'). Default is false.")
	printRawLong := flag.Bool("raw", defaults.PrintRaw, "Set to true to print raw output (don't attempt to use 'glow'). Default is false.")

	replyShort := flag.Bool("re", defaults.ReplyMode, "Set to true to reply to the previous query, meaning that it will be used as context for your next query. Default is false.")
	replyLong := flag.Bool("reply", defaults.ReplyMode, "Set to true to reply to the previous query, meaning that it will be used as context for your next query. Default is false.")

	useToolsShort := flag.Bool("t", defaults.UseTools, "Set to true to use tools. Default is false.")
	useToolsLong := flag.Bool("tools", defaults.UseTools, "Set to true to use tools. Default is false.")

	flag.Parse()
	chatModel, err := utils.ReturnNonDefault(*cmShort, *cmLong, defaults.ChatModel)
	exitWithFlagError(err, "cm", "chat-model")
	photoModel, err := utils.ReturnNonDefault(*pmShort, *pmLong, defaults.PhotoModel)
	exitWithFlagError(err, "pm", "photo-model")
	pictureDir, err := utils.ReturnNonDefault(*pdShort, *pdLong, defaults.PhotoDir)
	exitWithFlagError(err, "pd", "photo-dir")
	picturePrefix, err := utils.ReturnNonDefault(*ppShort, *ppLong, defaults.PhotoPrefix)
	exitWithFlagError(err, "pp", "photo-prefix")
	stdinReplace, err := utils.ReturnNonDefault(*stdinReplaceShort, *stdinReplaceLong, defaults.StdinReplace)
	exitWithFlagError(err, "I", "replace")
	useTools, err := utils.ReturnNonDefault(*useToolsShort, *useToolsLong, defaults.UseTools)
	exitWithFlagError(err, "t", "tools")
	replyMode := *replyShort || *replyLong
	printRaw := *printRawShort || *printRawLong

	if *defaultStdinReplace && defaults.StdinReplace == "" {
		stdinReplace = "{}"
	}

	return Configurations{
		ChatModel:    chatModel,
		PhotoModel:   photoModel,
		PhotoDir:     pictureDir,
		PhotoPrefix:  picturePrefix,
		StdinReplace: stdinReplace,
		PrintRaw:     printRaw,
		ReplyMode:    replyMode,
		UseTools:     useTools,
	}
}

func applyFlagOverridesForText(tConf *text.Configurations, flagSet, defaultFlags Configurations) {
	if flagSet.StdinReplace != defaultFlags.StdinReplace {
		tConf.StdinReplace = flagSet.StdinReplace
	}
	if flagSet.ChatModel != defaultFlags.ChatModel {
		tConf.Model = flagSet.ChatModel
	}
	if flagSet.ReplyMode != defaultFlags.ReplyMode {
		tConf.ReplyMode = flagSet.ReplyMode
	}
	if flagSet.PrintRaw != defaultFlags.PrintRaw {
		tConf.Raw = flagSet.PrintRaw
	}
	if flagSet.UseTools != defaultFlags.UseTools {
		tConf.UseTools = flagSet.UseTools
	}
}

func applyFlagOverridesForPhoto(pConf *photo.Configurations, flagSet, defaultFlags Configurations) {
	if flagSet.ReplyMode != defaultFlags.ReplyMode {
		pConf.ReplyMode = flagSet.ReplyMode
	}
	if flagSet.StdinReplace != defaultFlags.StdinReplace {
		pConf.StdinReplace = flagSet.StdinReplace
	}
	if flagSet.PhotoModel != defaultFlags.PhotoModel {
		pConf.Model = flagSet.PhotoModel
	}
	if flagSet.PhotoPrefix != defaultFlags.PhotoPrefix {
		pConf.Output.Prefix = flagSet.PhotoPrefix
	}
	if flagSet.PhotoDir != defaultFlags.PhotoDir {
		pConf.Output.Dir = flagSet.PhotoPrefix
	}
	if flagSet.PhotoOutput != defaultFlags.PhotoOutput {
		pConf.Output.Type = photo.OutputType(flagSet.PhotoOutput)
	}
}

func exitWithFlagError(err error, shortFlag, longflag string) {
	if err != nil {
		// Im just too lazy to setup the err struct
		if err.Error() == "values are mutually exclusive" {
			ancli.PrintErr(fmt.Sprintf("flags: '%v' and '%v' are mutually exclusive, err: %v\n", shortFlag, longflag, err))
		} else {
			ancli.PrintErr(fmt.Sprintf("unexpected error: %v", err))
		}
		os.Exit(1)
	}
}
