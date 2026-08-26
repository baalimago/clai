package internal

import (
	"errors"
	"flag"
	"fmt"
	"os"

	"github.com/baalimago/clai/internal/audio"
	"github.com/baalimago/clai/internal/photo"
	"github.com/baalimago/clai/internal/text"
	"github.com/baalimago/clai/internal/utils"
	"github.com/baalimago/clai/internal/video"
	"github.com/baalimago/go_away_boilerplate/pkg/ancli"
)

type Configurations struct {
	ChatModel   string
	PhotoModel  string
	PhotoDir    string
	PhotoPrefix string
	PhotoOutput string
	VideoModel  string
	VideoDir    string
	VideoPrefix string
	VideoOutput string
	AudioModel  string
	// AudioFormat is the -af/-audio-format transcript output format
	// (vtt|srt|text|json); validated in CreateAudioQuerier.
	AudioFormat string
	// Parallelism is the -parallelism max concurrent transcription requests
	// for split audio files. 0 = no override.
	Parallelism   int
	StdinReplace  string
	ExpectReplace bool
	PrintRaw      bool
	ReplyMode     bool
	// DirReplyMode enables directory-scoped reply mode.
	// When true, the previous conversation is loaded from the directory binding
	// instead of the global globalScope.json.
	DirReplyMode bool
	// UseTools encodes tooling selection from CLI:
	//   ""      => no override
	//   "*"     => all tools
	//   "a,b,c" => only these tools
	UseTools  string
	UseSkills string
	// CmdBan is the raw comma-separated -cmd-ban flag value. Entries are
	// parsed (split, trimmed, empties dropped) and appended onto the
	// file+profile base in setupCmdBanConfig; the flag can only add bans.
	CmdBan string
	// UseLookback enables conversation lookback when true.
	// When false (default), defers to profile/file configuration.
	UseLookback bool
	// UseLookbackSet is true when -lb/-lookback was explicitly passed, so an
	// explicit -lb=false can override profile/file-enabled lookback.
	UseLookbackSet bool
	// MaxTokens is the -mt/-max-tokens value. Meaningful only when
	// MaxTokensSet is true.
	MaxTokens int
	// MaxTokensSet is true when -mt/-max-tokens was explicitly passed, so an
	// explicit 0 can override a file-configured stoploss.max-tokens.
	MaxTokensSet bool
	// MaxToolCalls is the -mtc/-max-tool-calls value. Meaningful only when
	// MaxToolCallsSet is true.
	MaxToolCalls int
	// MaxToolCallsSet is true when -mtc/-max-tool-calls was explicitly passed,
	// so an explicit 0 can override a file-configured max-tool-calls.
	MaxToolCallsSet bool
	// MaxToolCallsAfterHandover is the -max-tool-calls-after-handover value.
	// Meaningful only when MaxToolCallsAfterHandoverSet is true.
	MaxToolCallsAfterHandover int
	// MaxToolCallsAfterHandoverSet is true when
	// -max-tool-calls-after-handover was explicitly passed, so an explicit 0
	// can override a file-configured stoploss.max-tool-calls-after-handover.
	MaxToolCallsAfterHandoverSet bool
	Glob                         string
	Profile                      string
	ProfilePath                  string
	// ShellContext is the selected shell context name (ASC).
	ShellContext string
	// ResponseFormatPath is a path to a JSON file describing the OpenAI response_format.
	// Supports "json_object" and "json_schema" types.
	ResponseFormatPath string
	// NonInteractive disables the default interactive-macro behavior.
	// When true, macro mode appends trailing "q" terminators for auto-exit
	// instead of falling through to interactive stdin.
	NonInteractive bool
}

// parseFlags parses CLI flags into an internal Configurations.
// For tooling:
//
//	-t=* or -tools=*          => UseTools="*" (all tools)
//	-t=a,b or -tools=a,b      => UseTools="a,b" (specific tools)
//	(flag omitted)            => UseTools="" (no override)
func parseFlags(defaults Configurations, args []string) (Configurations, []string, error) {
	fs := flag.NewFlagSet("clai", flag.ContinueOnError)
	fs.String("A-helpful-nonexisting-flag", "there is no default", "This isn't a flag. It's only here to tell you that 'clai h/help' gives better overview of usage than 'clai -h'.")

	cmShort := fs.String("cm", defaults.ChatModel, "Set the chat model to use. Mutually exclusive with chat-model flag.")
	cmLong := fs.String("chat-model", defaults.ChatModel, "Set the chat model to use. Mutually exclusive with cm flag.")

	pmShort := fs.String("pm", defaults.PhotoModel, "Set the image model to use. Mutually exclusive with photo-model flag.")
	pmLong := fs.String("photo-model", defaults.PhotoModel, "Set the image model to use. Mutually exclusive with pm flag.")

	pdShort := fs.String("pd", defaults.PhotoDir, "Set the directory to store the generated pictures. Default is $HOME/Pictures")
	pdLong := fs.String("photo-dir", defaults.PhotoDir, "Set the directory to store the generated pictures. Default is $HOME/Pictures")

	ppShort := fs.String("pp", defaults.PhotoPrefix, "Set the prefix for the generated pictures. Default is 'clai'")
	ppLong := fs.String("photo-prefix", defaults.PhotoPrefix, "Set the prefix for the generated pictures. Default is 'clai'")

	vmShort := fs.String("vm", defaults.VideoModel, "Set the video model. Mutually exclusive with video-model.")
	vmLong := fs.String("video-model", defaults.VideoModel, "Set the video model. Mutually exclusive with vm.")

	vdShort := fs.String("vd", defaults.VideoDir, "Set dir for generated videos. Default $HOME/Videos")
	vdLong := fs.String("video-dir", defaults.VideoDir, "Set dir for generated videos. Default $HOME/Videos")

	vpShort := fs.String("vp", defaults.VideoPrefix, "Set prefix for generated videos. Default 'clai'")
	vpLong := fs.String("video-prefix", defaults.VideoPrefix, "Set prefix for generated videos. Default 'clai'")

	amShort := fs.String("am", defaults.AudioModel, "Set the audio transcription model. Mutually exclusive with audio-model.")
	amLong := fs.String("audio-model", defaults.AudioModel, "Set the audio transcription model. Mutually exclusive with am.")

	afShort := fs.String("af", defaults.AudioFormat, "Set the transcript output format: vtt|srt|text|json. Mutually exclusive with audio-format.")
	afLong := fs.String("audio-format", defaults.AudioFormat, "Set the transcript output format: vtt|srt|text|json. Mutually exclusive with af.")

	parallelism := fs.Int("parallelism", defaults.Parallelism, "Set max parallel transcription requests for split audio files.")

	gShort := fs.String("g", defaults.Glob, "Use globbing from query or chat. This flag will deprecate glob mode in a future release.")
	gLong := fs.String("glob", defaults.Glob, "Use globbing from query or chat. This flag will deprecate glob mode in a future release.")

	pShort := fs.String("p", defaults.Profile, "Set this to the override profile you'd like to use. Configure with 'clai setup' -> 2.")
	pLong := fs.String("profile", defaults.Profile, "Set this to the override profile you'd like to use. Configure with 'clai setup' -> 2.")
	prPathShort := fs.String("prp", defaults.ProfilePath, "Set this to the path of a profile file to use. Mutually exclusive with -p/-profile.")
	prPathLong := fs.String("profile-path", defaults.ProfilePath, "Set this to the path of a profile file to use. Mutually exclusive with -p/-profile.")

	stdinReplaceShort := fs.String("I", defaults.StdinReplace, "Set the string to replace with stdin. (flag syntax borrowed from xargs)")
	stdinReplaceLong := fs.String("replace", defaults.StdinReplace, "Set the string to replace with stdin. (flag syntax borrowed from xargs)'")
	expectReplace := fs.Bool("i", defaults.ExpectReplace, "Set to true to replace '{}' with stdin. This is overwritten by -I and -replace. (flag syntax borrowed from xargs)'")

	printRawShort := fs.Bool("r", defaults.PrintRaw, "Set to true to print raw output (don't attempt to use 'glow').")
	printRawLong := fs.Bool("raw", defaults.PrintRaw, "Set to true to print raw output (don't attempt to use 'glow').")

	replyShort := fs.Bool("re", defaults.ReplyMode, "Set to true to reply to the previous query, meaning that it will be used as context for your next query.")
	replyLong := fs.Bool("reply", defaults.ReplyMode, "Set to true to reply to the previous query, meaning that it will be used as context for your next query.")

	dirReplyShort := fs.Bool("dre", defaults.DirReplyMode, "Set to true to reply to the previous directory-scoped conversation (bound to the current working directory).")
	dirReplyLong := fs.Bool("dir-reply", defaults.DirReplyMode, "Set to true to reply to the previous directory-scoped conversation (bound to the current working directory).")

	// ASC (auto-append shell context)
	ascShort := fs.String("asc", defaults.ShellContext, "Auto-append shell context by name. Mutually exclusive with add-shell-context.")
	ascLong := fs.String("add-shell-context", defaults.ShellContext, "Auto-append shell context by name. Mutually exclusive with asc.")

	rfShort := fs.String("rf", defaults.ResponseFormatPath, "Block streaming and print only the final structured response.")
	rfLong := fs.String("response-format", defaults.ResponseFormatPath, "Block streaming and print only the final structured response.")

	// Breaking change: -t/-tools are string-only value flags.
	// Use: -t=* or -t=a,b ("-t" without value is undefined/ignored).
	useToolsShort := fs.String("t", defaults.UseTools, "Enable tools. Use '*' for all tools or comma-separated list for specific tools.")
	useToolsLong := fs.String("tools", defaults.UseTools, "Enable tools. Use '*' for all tools or comma-separated list for specific tools.")
	cmdBan := fs.String("cmd-ban", defaults.CmdBan, "Append comma-separated command ban entries for this run (e.g. 'rm,sudo'). Commands matching a ban are refused before they spawn.")
	useSkillsShort := fs.String("s", defaults.UseSkills, "Enable skills. Use '*' to enable or 'none' to disable for the current run.")
	useSkillsLong := fs.String("skills", defaults.UseSkills, "Enable skills. Use '*' to enable or 'none' to disable for the current run.")
	useLookbackShort := fs.Bool("lb", defaults.UseLookback, "Enable conversation lookback (recent-conversations memory + search/inspect/read tools).")
	useLookbackLong := fs.Bool("lookback", defaults.UseLookback, "Enable conversation lookback (recent-conversations memory + search/inspect/read tools).")

	maxTokensShort := fs.Int("mt", defaults.MaxTokens, "Set the max context tokens for this run. 0 = unlimited. Overrides stoploss.max-tokens in textConfig.json.")
	maxTokensLong := fs.Int("max-tokens", defaults.MaxTokens, "Set the max context tokens for this run. 0 = unlimited. Overrides stoploss.max-tokens in textConfig.json.")
	maxToolCallsShort := fs.Int("mtc", defaults.MaxToolCalls, "Set the max tool calls for this run. 0 = unlimited. Overrides max-tool-calls in textConfig.json.")
	maxToolCallsLong := fs.Int("max-tool-calls", defaults.MaxToolCalls, "Set the max tool calls for this run. 0 = unlimited. Overrides max-tool-calls in textConfig.json.")
	maxToolCallsAfterHandover := fs.Int("max-tool-calls-after-handover", defaults.MaxToolCallsAfterHandover, "Set the max tool calls for the post-handover phase of this run. 0 = unlimited. Overrides stoploss.max-tool-calls-after-handover in textConfig.json.")

	nonInteractiveShort := fs.Bool("n", defaults.NonInteractive, "Disable interactive stdin fallback after macro inputs; instead auto-exit with trailing quits.")
	nonInteractiveLong := fs.Bool("non-interactive", defaults.NonInteractive, "Disable interactive stdin fallback after macro inputs; instead auto-exit with trailing quits.")

	err := fs.Parse(args)
	if err != nil {
		return Configurations{}, []string{}, fmt.Errorf("failed to parse args: %w", err)
	}

	postParseArgs := fs.Args()

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
	useSkills, err := utils.ReturnNonDefault(*useSkillsShort, *useSkillsLong, defaults.UseSkills)
	exitWithFlagError(err, "s", "skills")
	useLookback := *useLookbackShort || *useLookbackLong
	useLookbackSet := false
	mtSet := false
	maxTokensSet := false
	mtcSet := false
	maxToolCallsSet := false
	maxToolCallsAfterHandoverSet := false
	fs.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "lb", "lookback":
			useLookbackSet = true
		case "mt":
			mtSet = true
		case "max-tokens":
			maxTokensSet = true
		case "mtc":
			mtcSet = true
		case "max-tool-calls":
			maxToolCallsSet = true
		case "max-tool-calls-after-handover":
			maxToolCallsAfterHandoverSet = true
		}
	})
	profile, err := utils.ReturnNonDefault(*pShort, *pLong, defaults.Profile)
	exitWithFlagError(err, "p", "profile")
	profilePath, err := utils.ReturnNonDefault(*prPathShort, *prPathLong, defaults.ProfilePath)
	exitWithFlagError(err, "prp", "profile-path")
	videoModel, err := utils.ReturnNonDefault(*vmShort, *vmLong, defaults.VideoModel)
	exitWithFlagError(err, "vm", "video-model")
	videoDir, err := utils.ReturnNonDefault(*vdShort, *vdLong, defaults.VideoDir)
	exitWithFlagError(err, "vd", "video-dir")
	videoPrefix, err := utils.ReturnNonDefault(*vpShort, *vpLong, defaults.VideoPrefix)
	exitWithFlagError(err, "vp", "video-prefix")
	audioModel, err := utils.ReturnNonDefault(*amShort, *amLong, defaults.AudioModel)
	exitWithFlagError(err, "am", "audio-model")
	audioFormat, err := utils.ReturnNonDefault(*afShort, *afLong, defaults.AudioFormat)
	exitWithFlagError(err, "af", "audio-format")
	shellContext, err := utils.ReturnNonDefault(*ascShort, *ascLong, defaults.ShellContext)
	exitWithFlagError(err, "asc", "add-shell-context")
	responseFormatPath, err := utils.ReturnNonDefault(*rfShort, *rfLong, defaults.ResponseFormatPath)
	exitWithFlagError(err, "rf", "response-format")

	maxTokens, maxTokensSet, err := resolveIntAlias(*maxTokensShort, *maxTokensLong, defaults.MaxTokens, mtSet, maxTokensSet)
	if err != nil {
		return Configurations{}, []string{}, fmt.Errorf("flags: 'mt' and 'max-tokens' are mutually exclusive, err: %w", err)
	}
	maxToolCalls, maxToolCallsSet, err := resolveIntAlias(*maxToolCallsShort, *maxToolCallsLong, defaults.MaxToolCalls, mtcSet, maxToolCallsSet)
	if err != nil {
		return Configurations{}, []string{}, fmt.Errorf("flags: 'mtc' and 'max-tool-calls' are mutually exclusive, err: %w", err)
	}

	replyMode := *replyShort || *replyLong
	printRaw := *printRawShort || *printRawLong
	dirReplyMode := *dirReplyShort || *dirReplyLong

	if *expectReplace && defaults.StdinReplace == "" {
		stdinReplace = "{}"
	}

	newConf := Configurations{
		ChatModel:                    chatModel,
		PhotoModel:                   photoModel,
		PhotoDir:                     pictureDir,
		PhotoPrefix:                  picturePrefix,
		VideoModel:                   videoModel,
		VideoDir:                     videoDir,
		VideoPrefix:                  videoPrefix,
		AudioModel:                   audioModel,
		AudioFormat:                  audioFormat,
		Parallelism:                  *parallelism,
		StdinReplace:                 stdinReplace,
		PrintRaw:                     printRaw,
		ReplyMode:                    replyMode,
		DirReplyMode:                 dirReplyMode,
		UseTools:                     useTools,
		UseSkills:                    useSkills,
		CmdBan:                       *cmdBan,
		UseLookback:                  useLookback,
		UseLookbackSet:               useLookbackSet,
		MaxTokens:                    maxTokens,
		MaxTokensSet:                 maxTokensSet,
		MaxToolCalls:                 maxToolCalls,
		MaxToolCallsSet:              maxToolCallsSet,
		MaxToolCallsAfterHandover:    *maxToolCallsAfterHandover,
		MaxToolCallsAfterHandoverSet: maxToolCallsAfterHandoverSet,
		Glob:                         glob,
		ExpectReplace:                *expectReplace,
		Profile:                      profile,
		ProfilePath:                  profilePath,
		ShellContext:                 shellContext,
		ResponseFormatPath:           responseFormatPath,
		NonInteractive:               *nonInteractiveShort || *nonInteractiveLong,
	}

	return newConf, postParseArgs, nil
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
	if flagSet.DirReplyMode != defaultFlags.DirReplyMode {
		tConf.DirReplyMode = flagSet.DirReplyMode
	}
	if flagSet.PrintRaw != defaultFlags.PrintRaw {
		tConf.Raw = flagSet.PrintRaw
	}
	// Tool selection is interpreted in setupTextQuerier based on flagSet.UseTools.
	if flagSet.Profile != defaultFlags.Profile {
		tConf.UseProfile = flagSet.Profile
	}
	if flagSet.ProfilePath != defaultFlags.ProfilePath {
		tConf.ProfilePath = flagSet.ProfilePath
	}
	if flagSet.ShellContext != defaultFlags.ShellContext {
		tConf.ShellContext = flagSet.ShellContext
	}
	if flagSet.MaxTokensSet {
		if tConf.Stoploss == nil {
			tConf.Stoploss = &text.Stoploss{}
		}
		tConf.Stoploss.MaxTokens = flagSet.MaxTokens
	}
	if flagSet.MaxToolCallsSet {
		tConf.MaxToolCalls = &flagSet.MaxToolCalls
	}
	if flagSet.MaxToolCallsAfterHandoverSet {
		if tConf.Stoploss == nil {
			tConf.Stoploss = &text.Stoploss{}
		}
		tConf.Stoploss.MaxToolCallsAfterHandover = flagSet.MaxToolCallsAfterHandover
	}
}

// resolveIntAlias resolves a short/long integer flag pair from explicit
// visitation rather than comparison with parser defaults, so an explicit value
// equal to the default (including zero) is still recognized as set (R5-02).
// Rules: neither alias set -> default, unset; exactly one set -> that value;
// both set and equal -> that value; both set and different -> the existing
// mutual-exclusion error. The resolved set flag is true when at least one
// alias of the pair was explicitly passed.
func resolveIntAlias(short, long, defaultVal int, shortSet, longSet bool) (int, bool, error) {
	if !shortSet && !longSet {
		return defaultVal, false, nil
	}
	if shortSet && longSet && short != long {
		return 0, false, errors.New("values are mutually exclusive")
	}
	if shortSet {
		return short, true, nil
	}
	return long, true, nil
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

func applyFlagOverridesForVideo(vConf *video.Configurations, flagSet, defaultFlags Configurations) {
	if flagSet.ExpectReplace {
		vConf.StdinReplace = flagSet.StdinReplace
	}
	if flagSet.ReplyMode != defaultFlags.ReplyMode {
		vConf.ReplyMode = flagSet.ReplyMode
	}
	if flagSet.StdinReplace != defaultFlags.StdinReplace {
		vConf.StdinReplace = flagSet.StdinReplace
	}
	if flagSet.VideoModel != defaultFlags.VideoModel {
		vConf.Model = flagSet.VideoModel
	}
	if flagSet.VideoPrefix != defaultFlags.VideoPrefix {
		vConf.Output.Prefix = flagSet.VideoPrefix
	}
	if flagSet.VideoDir != defaultFlags.VideoDir {
		vConf.Output.Dir = flagSet.VideoDir
	}
	if flagSet.VideoOutput != defaultFlags.VideoOutput && flagSet.VideoOutput != "" {
		vConf.Output.Type = video.OutputType(flagSet.VideoOutput)
	}
}

func applyFlagOverridesForAudio(aConf *audio.Configurations, flagSet, defaultFlags Configurations) {
	if flagSet.AudioModel != defaultFlags.AudioModel {
		aConf.Transcribe.Model = flagSet.AudioModel
	}
	if flagSet.AudioFormat != defaultFlags.AudioFormat {
		aConf.Transcribe.OutputFormat = flagSet.AudioFormat
	}
	if flagSet.Parallelism != defaultFlags.Parallelism {
		aConf.Transcribe.Parallelism = flagSet.Parallelism
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
