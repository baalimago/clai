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
	ChatModel     string
	PhotoModel    string
	PhotoDir      string
	PhotoPrefix   string
	PhotoOutput   string
	StdinReplace  string
	ExpectReplace bool
	PrintRaw      bool
	ReplyMode     bool
	UseTools      bool
	Glob          string
	Profile       string
}

func setupFlags(defaults Configurations) Configurations {
	flag.String("A-helpful-nonexisting-flag", "there is no default", "This isn't a flag. It's only here to tell you that 'clai h/help' gives better overview of usage than 'clai -h'.")

	cmShort := flag.String("cm", defaults.ChatModel, "Set the chat model to use. Mutually exclusive with chat-model flag.")
	cmLong := flag.String("chat-model", defaults.ChatModel, "Set the chat model to use. Mutually exclusive with cm flag.")

	pmShort := flag.String("pm", defaults.PhotoModel, "Set the image model to use. Mutually exclusive with photo-model flag.")
	pmLong := flag.String("photo-model", defaults.PhotoModel, "Set the image model to use. Mutually exclusive with pm flag.")

	pdShort := flag.String("pd", defaults.PhotoDir, "Set the directory to store the generated pictures. Default is $HOME/Pictures")
	pdLong := flag.String("photo-dir", defaults.PhotoDir, "Set the directory to store the generated pictures. Default is $HOME/Pictures")

	ppShort := flag.String("pp", defaults.PhotoPrefix, "Set the prefix for the generated pictures. Default is 'clai'")
	ppLong := flag.String("photo-prefix", defaults.PhotoPrefix, "Set the prefix for the generated pictures. Default is 'clai'")

	gShort := flag.String("g", defaults.Glob, "Set this to true to use globbing, but from query/chat. This flag will deperecate glob mode in some upcoming release.")
	gLong := flag.String("glob", defaults.Glob, "Set this to true to use globbing, but from query/chat. This flag will deperecate glob mode in some upcoming release.")

	pShort := flag.String("p", defaults.Profile, "Set this to the override profile you'd like to use. Configure with 'clai setup' -> 2.")
	pLong := flag.String("profile", defaults.Profile, "Set this to the override profile you'd like to use. Configure with 'clai setup' -> 2.")

	stdinReplaceShort := flag.String("I", defaults.StdinReplace, "Set the string to replace with stdin. (flag syntax borrowed from xargs)")
	stdinReplaceLong := flag.String("replace", defaults.StdinReplace, "Set the string to replace with stdin. (flag syntax borrowed from xargs)'")
	expectReplace := flag.Bool("i", defaults.ExpectReplace, "Set to true to replace '{}' with stdin. This is overwritten by -I and -replace. (flag syntax borrowed from xargs)'")

	printRawShort := flag.Bool("r", defaults.PrintRaw, "Set to true to print raw output (don't attempt to use 'glow').")
	printRawLong := flag.Bool("raw", defaults.PrintRaw, "Set to true to print raw output (don't attempt to use 'glow').")

	replyShort := flag.Bool("re", defaults.ReplyMode, "Set to true to reply to the previous query, meaning that it will be used as context for your next query.")
	replyLong := flag.Bool("reply", defaults.ReplyMode, "Set to true to reply to the previous query, meaning that it will be used as context for your next query.")

	useToolsShort := flag.Bool("t", defaults.UseTools, "Set to true to use tools.")
	useToolsLong := flag.Bool("tools", defaults.UseTools, "Set to true to use tools.")

	flag.Parse()
	chatModel, err := utils.ReturnNonDefault(*cmShort, *cmLong, defaults.ChatModel)
	exitWithFlagError(err, "cm", "chat-model")
	photoModel, err := utils.ReturnNonDefault(*pmShort, *pmLong, defaults.PhotoModel)
	exitWithFlagError(err, "pm", "photo-model")
	pictureDir, err := utils.ReturnNonDefault(*pdShort, *pdLong, defaults.PhotoDir)
	exitWithFlagError(err, "pd", "photo-dir")
	picturePrefix, err := utils.ReturnNonDefault(*ppShort, *ppLong, defaults.PhotoPrefix)
	exitWithFlagError(err, "pp", "photo-prefix")
	glob, err := utils.ReturnNonDefault(*gShort, *gLong, defaults.Glob)
	exitWithFlagError(err, "g", "glob")
	stdinReplace, err := utils.ReturnNonDefault(*stdinReplaceShort, *stdinReplaceLong, defaults.StdinReplace)
	exitWithFlagError(err, "I", "replace")
	useTools, err := utils.ReturnNonDefault(*useToolsShort, *useToolsLong, defaults.UseTools)
	exitWithFlagError(err, "t", "tools")
	profile, err := utils.ReturnNonDefault(*pShort, *pLong, defaults.Profile)
	exitWithFlagError(err, "p", "profile")
	replyMode := *replyShort || *replyLong
	printRaw := *printRawShort || *printRawLong

	if *expectReplace && defaults.StdinReplace == "" {
		stdinReplace = "{}"
	}

	return Configurations{
		ChatModel:     chatModel,
		PhotoModel:    photoModel,
		PhotoDir:      pictureDir,
		PhotoPrefix:   picturePrefix,
		StdinReplace:  stdinReplace,
		PrintRaw:      printRaw,
		ReplyMode:     replyMode,
		UseTools:      useTools,
		Glob:          glob,
		ExpectReplace: *expectReplace,
		Profile:       profile,
	}
}

// applyFlagOverridesForText is defined here, and not as a method on text.Confugrations, as that would
// cause import cycle.
//
// The default flags are needed to ensure that the configuration isn't being overwritten by the default flags.
// Meaning: Only set the value of tConf to the flag, if it's not the default, leave the configuration found in file.
// If there is no check if the flagSet is default, there may be a case where default > file, which breaks
// the configuration convention flags > file > default
func applyFlagOverridesForText(tConf *text.Configurations, flagSet, defaultFlags Configurations) {
	if flagSet.ExpectReplace {
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
	if flagSet.Profile != defaultFlags.Profile {
		tConf.UseProfile = flagSet.Profile
	}
}

func applyProfileOverridesForText(tConf *text.Configurations, flagSet, defaultFlags Configurations) {
	if flagSet.ChatModel != defaultFlags.ChatModel {
		tConf.Model = flagSet.ChatModel
	}
}

func applyFlagOverridesForPhoto(pConf *photo.Configurations, flagSet, defaultFlags Configurations) {
	if flagSet.ExpectReplace {
		pConf.StdinReplace = flagSet.StdinReplace
	}
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
		pConf.Output.Dir = flagSet.PhotoDir
	}
	if flagSet.PhotoOutput != defaultFlags.PhotoOutput && flagSet.PhotoOutput != "" {
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
