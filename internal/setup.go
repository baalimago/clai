package internal

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path"
	"runtime/debug"

	"github.com/baalimago/clai/internal/glob"
	"github.com/baalimago/clai/internal/models"
	"github.com/baalimago/clai/internal/photo"
	"github.com/baalimago/clai/internal/text"
	"github.com/baalimago/clai/internal/utils"
	"github.com/baalimago/go_away_boilerplate/pkg/ancli"
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
}

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
	default:
		return HELP, fmt.Errorf("unknown command: '%s'", os.Args[1])
	}
}

func setupTextQuerier(mode Mode, confDir string, flagSet Configurations) (models.Querier, error) {
	// The flagset is first used to find chatModel and potentially setup a new configuration file from some default
	tConf, err := utils.LoadConfigFromFile(confDir, "textConfig.json", migrateOldChatConfig, &text.DEFAULT)
	tConf.ConfigDir = path.Join(confDir, ".clai")
	if err != nil {
		return nil, fmt.Errorf("failed to load configs: %err", err)
	}
	if mode == CHAT {
		tConf.ChatMode = true
	}
	// At the moment, the configurations are based on the config file. But
	// the configuration presecende is flags > file > default. So, we need
	// to re-apply the flag overrides to the configuration
	applyFlagOverridesForText(&tConf, flagSet, defaultFlags)

	if misc.Truthy(os.Getenv("DEBUG")) {
		ancli.PrintOK(fmt.Sprintf("config post flag override: %+v\n", tConf))
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
	err = tConf.SetupPrompts(args)
	if err != nil {
		return nil, fmt.Errorf("failed to setup prompt: %v", err)
	}

	cq, err := CreateTextQuerier(tConf)

	if misc.Truthy(os.Getenv("DEBUG")) {
		ancli.PrintOK(fmt.Sprintf("querier post text querier create: %+v\n", tConf))
	}
	if err != nil {
		return nil, fmt.Errorf("failed to create text querier: %v", err)
	}
	return cq, nil
}

func Setup(usage string) (models.Querier, error) {
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
	case CHAT, QUERY, GLOB:
		return setupTextQuerier(mode, confDir, flagSet)
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
			ancli.PrintOK(fmt.Sprintf("photo querier: %+v\n", pq))
		}
		if err != nil {
			return nil, fmt.Errorf("failed to create photo querier: %v", err)
		}
		return pq, nil
	case HELP:
		fmt.Printf(usage,
			defaultFlags.ReplyMode,
			defaultFlags.PrintRaw,
			defaultFlags.PhotoDir,
			defaultFlags.PhotoPrefix,
			defaultFlags.StdinReplace,
			defaultFlags.ExpectReplace,
			defaultFlags.UseTools,
			defaultFlags.Glob,
		)
		os.Exit(0)
	case VERSION:
		bi, ok := debug.ReadBuildInfo()
		if !ok {
			return nil, errors.New("failed to read build info")
		}
		fmt.Printf("version: %v, go version: %v, checksum: %v\n", bi.Main.Version, bi.GoVersion, bi.Main.Sum)
		os.Exit(0)
	default:
		return nil, fmt.Errorf("unknown mode: %v", mode)
	}
	return nil, errors.New("unexpected conditional: how did you end up here?")
}
