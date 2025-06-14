package internal

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path"
	"runtime/debug"

	"github.com/baalimago/clai/internal/glob"
	"github.com/baalimago/clai/internal/models"
	"github.com/baalimago/clai/internal/photo"
	"github.com/baalimago/clai/internal/reply"
	"github.com/baalimago/clai/internal/setup"
	"github.com/baalimago/clai/internal/text"
	"github.com/baalimago/clai/internal/utils"
	"github.com/baalimago/go_away_boilerplate/pkg/ancli"
	imagodebug "github.com/baalimago/go_away_boilerplate/pkg/debug"

	"github.com/baalimago/go_away_boilerplate/pkg/misc"
)

type PromptConfig struct {
	Photo string `yaml:"photo"`
	Query string `yaml:"query"`
}

type Mode int

const (
	HELP Mode = iota
	QUERY
	CHAT
	GLOB
	PHOTO
	VERSION
	SETUP
	CMD
	REPLAY
)

var defaultFlags = Configurations{
	ChatModel:    "",
	PhotoModel:   "",
	PhotoDir:     fmt.Sprintf("%v/Pictures", os.Getenv("HOME")),
	PhotoPrefix:  "clai",
	PhotoOutput:  "local",
	StdinReplace: "{}",
	// Zero value, but explicitly set for clarity
	PrintRaw:      false,
	ExpectReplace: false,
	ReplyMode:     false,
	UseTools:      false,
	ProfilePath:   "",
}

const PROFILE_HELP = `Profiles overwrite certain model configurations. The intent of profiles
is to reduce usage for repetitive flags and to persist and tweak specific LLM agents.
For instance, you may create a 'gopher' profile with a prompt that explains the agent is
a programming helper and then specify which tools it may use.

Use this profile by passing the '-p/-profile' flag. Example:

1. clai setup -> 2 -> follow the setup wizard (create 'gopher' profile)
2. clai -p gopher -g internal/thing/handler.go q write tests for this file`

func getModeFromArgs(cmd string) (Mode, error) {
	switch cmd {
	case "photo", "p":
		return PHOTO, nil
	case "chat", "c":
		return CHAT, nil
	case "query", "q":
		return QUERY, nil
	case "glob", "g":
		ancli.PrintWarn("this way of calling glob will be deprecated in the future. Please use the -g <glob> flag with query/chat commands instead.\n")
		return GLOB, nil
	case "help", "h":
		return HELP, nil
	case "setup", "s":
		return SETUP, nil
	case "version", "v":
		return VERSION, nil
	case "cmd":
		return CMD, nil
	case "replay", "re":
		return REPLAY, nil
	default:
		return HELP, fmt.Errorf("unknown command: '%s'", os.Args[1])
	}
}

// setupTextQuerier by doing the most convuluted and organically grown configuration system known to man.
// Do I know 100% how it works at any given point? Sort of. Not really. Am I constantly impressed over how
// round this wheel I've reinvented is? Yeah, for sure. May it be simplified? Maybe, but it's features are
// quite complex.
func setupTextQuerier(ctx context.Context, mode Mode, confDir string, flagSet Configurations) (models.Querier, error) {
	// The flagset is first used to find chatModel and potentially setup a new configuration file from some default
	tConf, err := utils.LoadConfigFromFile(confDir, "textConfig.json", migrateOldChatConfig, &text.DEFAULT)
	tConf.ConfigDir = path.Join(confDir, ".clai")
	if err != nil {
		return nil, fmt.Errorf("failed to load configs: %err", err)
	}
	if mode == CHAT {
		tConf.ChatMode = true
	}

	if mode == CMD {
		tConf.CmdMode = true
		tConf.SystemPrompt = tConf.CmdModePrompt
	}

	// At the moment, the configurations are based on the config file. But
	// the configuration presecende is flags > file > default. So, we need
	// to re-apply the flag overrides to the configuration
	applyFlagOverridesForText(&tConf, flagSet, defaultFlags)

	if misc.Truthy(os.Getenv("DEBUG")) {
		ancli.PrintOK(fmt.Sprintf("config post flag override: %+v\n", imagodebug.IndentedJsonFmt(tConf)))
	}
	args := flag.Args()
	if mode == GLOB || flagSet.Glob != "" {
		globStr, retArgs, err := glob.Setup(flagSet.Glob, args)
		args = retArgs
		if err != nil {
			return nil, fmt.Errorf("failed to setup glob: %w", err)
		}

		tConf.Glob = globStr
	}
	err = tConf.ProfileOverrides()
	if err != nil {
		return nil, fmt.Errorf("profile override failure: %v", err)
	}

	// We want some flags, such as model, to be able to overwrite the profile configurations
	// If this gets too confusing, it should be changed
	applyProfileOverridesForText(&tConf, flagSet, defaultFlags)
	err = tConf.SetupPrompts(args)
	if err != nil {
		return nil, fmt.Errorf("failed to setup prompt: %v", err)
	}

	cq, err := CreateTextQuerier(ctx, tConf)

	if misc.Truthy(os.Getenv("DEBUG")) {
		ancli.PrintOK(fmt.Sprintf("querier post text querier create: %+v\n", tConf))
	}
	if err != nil {
		return nil, fmt.Errorf("failed to create text querier: %v", err)
	}
	return cq, nil
}

func printHelp(usage string, args []string) {
	if len(args) > 1 && (args[1] == "profile" || args[1] == "p") {
		fmt.Println(PROFILE_HELP)
		return
	}
	fmt.Printf(usage,
		defaultFlags.ReplyMode,
		defaultFlags.PrintRaw,
		defaultFlags.PhotoDir,
		defaultFlags.PhotoPrefix,
		defaultFlags.StdinReplace,
		defaultFlags.ExpectReplace,
		defaultFlags.UseTools,
		defaultFlags.Glob,
		defaultFlags.Profile,
	)
}

func Setup(ctx context.Context, usage string) (models.Querier, error) {
	flagSet := setupFlags(defaultFlags)
	args := flag.Args()
	if len(args) == 0 {
		return nil, fmt.Errorf("no command provided")
	}

	mode, err := getModeFromArgs(args[0])
	if err != nil {
		return nil, err
	}

	confDir, err := os.UserConfigDir()
	if err != nil {
		return nil, fmt.Errorf("failed to find home dir: %v", err)
	}

	switch mode {
	case CHAT, QUERY, GLOB, CMD:
		return setupTextQuerier(ctx, mode, confDir, flagSet)
	case PHOTO:
		pConf, err := utils.LoadConfigFromFile(confDir, "photoConfig.json", migrateOldPhotoConfig, &photo.DEFAULT)
		if err != nil {
			return nil, fmt.Errorf("failed to load configs: %w", err)
		}
		if misc.Truthy(os.Getenv("DEBUG")) {
			ancli.PrintOK(fmt.Sprintf("photoConfig pre override: %+v\n", pConf))
		}
		applyFlagOverridesForPhoto(&pConf, flagSet, defaultFlags)
		if misc.Truthy(os.Getenv("DEBUG")) {
			ancli.PrintOK(fmt.Sprintf("photoConfig post override: %+v\n", pConf))
		}
		err = pConf.SetupPrompts()
		if err != nil {
			return nil, fmt.Errorf("failed to setup prompt: %v", err)
		}
		pq, err := NewPhotoQuerier(pConf)
		if misc.Truthy(os.Getenv("DEBUG")) {
			ancli.PrintOK(fmt.Sprintf("photo querier: %+v\n", imagodebug.IndentedJsonFmt(pq)))
		}
		if err != nil {
			return nil, fmt.Errorf("failed to create photo querier: %v", err)
		}
		return pq, nil
	case HELP:
		printHelp(usage, args)
		os.Exit(0)
	case VERSION:
		bi, ok := debug.ReadBuildInfo()
		if !ok {
			return nil, errors.New("failed to read build info")
		}
		version := bi.Main.Version
		checksum := bi.Main.Sum
		if version == "" || version == "(devel)" {
			version = BuildVersion
		}
		if checksum == "" {
			checksum = BUILD_CHECKSUM
		}
		fmt.Printf("version: %v, go version: %v, checksum: %v\n", version, bi.GoVersion, checksum)
		os.Exit(0)
	case SETUP:
		err := setup.Run()
		if err != nil {
			return nil, fmt.Errorf("failed to run setup: %w", err)
		}
		os.Exit(0)
		return nil, nil
	case REPLAY:
		err := reply.Replay(flagSet.PrintRaw)
		if err != nil {
			return nil, fmt.Errorf("failed to replay previous reply: %w", err)
		}
		os.Exit(0)
	default:
		return nil, fmt.Errorf("unknown mode: %v", mode)
	}
	return nil, errors.New("unexpected conditional: how did you end up here?")
}
