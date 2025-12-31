package internal

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path"
	"runtime/debug"
	"strings"

	"github.com/baalimago/clai/internal/chat"
	"github.com/baalimago/clai/internal/glob"
	"github.com/baalimago/clai/internal/models"
	"github.com/baalimago/clai/internal/photo"
	"github.com/baalimago/clai/internal/profiles"
	"github.com/baalimago/clai/internal/setup"
	"github.com/baalimago/clai/internal/text"
	"github.com/baalimago/clai/internal/tools"
	"github.com/baalimago/clai/internal/utils"
	"github.com/baalimago/clai/internal/video"
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
	VIDEO
	VERSION
	SETUP
	CMD
	REPLAY
	TOOLS
	PROFILES
)

var defaultFlags = Configurations{
	ChatModel:    "",
	PhotoModel:   "",
	PhotoDir:     path.Join(os.Getenv("HOME"), "Pictures"),
	PhotoPrefix:  "clai",
	PhotoOutput:  "local",
	VideoModel:   "",
	VideoDir:     path.Join(os.Getenv("HOME"), "Videos"),
	VideoPrefix:  "clai",
	VideoOutput:  "url",
	StdinReplace: "{}",
	// Zero value, but explicitly set for clarity
	PrintRaw:      false,
	ExpectReplace: false,
	ReplyMode:     false,
	UseTools:      "",
	ProfilePath:   "",
}

const ProfileHelp = `Profiles overwrite certain model configurations. The intent of profiles
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
	case "video", "v":
		return VIDEO, nil
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
	case "version":
		return VERSION, nil
	case "cmd":
		return CMD, nil
	case "replay", "re":
		return REPLAY, nil
	case "tools", "t":
		return TOOLS, nil
	case "profiles":
		return PROFILES, nil
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
	tConf, err := utils.LoadConfigFromFile(confDir, "textConfig.json", migrateOldChatConfig, &text.Default)
	tConf.ConfigDir = confDir
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
		globStr, retArgs, globErr := glob.Setup(flagSet.Glob, args)
		args = retArgs
		if globErr != nil {
			return nil, fmt.Errorf("failed to setup glob: %w", globErr)
		}

		tConf.Glob = globStr
	}
	err = tConf.ProfileOverrides()
	if err != nil {
		return nil, fmt.Errorf("profile override failure: %v", err)
	}

	// Interpret CLI tools flag string:
	// flagSet.UseTools:
	//   ""       => no override, leave tConf.UseTools/Tools as config/profile decided
	//   "*"      => enable tooling, all tools
	//   "a,b,c"  => enable tooling, only those tools
	if flagSet.UseTools != "" {
		tConf.UseTools = true

		if flagSet.UseTools == "*" {
			// All tools: len(Tools)==0 is interpreted as "all tools"
			tConf.RequestedToolGlobs = nil
		} else {
			// Validate against tool registry and allow MCP-prefixed names.
			// tools.Registry only knows *local* tools; MCP tools are prefixed "mcp_".
			tools.Init()
			parts := strings.Split(flagSet.UseTools, ",")
			validTools := make([]string, 0, len(parts))

			for _, p := range parts {
				p = strings.TrimSpace(p)
				if p == "" {
					continue
				}

				// MCP tools: accept any name starting with "mcp_"
				if strings.HasPrefix(p, "mcp_") {
					validTools = append(validTools, p)
					continue
				}

				// Local tools: must exist in the registry
				wCardTools := tools.Registry.WildcardGet(p)
				if len(wCardTools) > 0 {
					for _, t := range wCardTools {
						validTools = append(validTools, t.Specification().Name)
					}
				} else {
					ancli.Warnf("attempted to select unknown tool '%s' via -t/-tools, skipping\n", p)
				}
			}

			// If nothing valid was found, disable tools from CLI perspective
			if len(validTools) == 0 {
				ancli.Warnf("no valid tools found from -t/-tools flag; disabling tools for this run\n")
				tConf.UseTools = false
				tConf.RequestedToolGlobs = nil
			} else {
				tConf.RequestedToolGlobs = validTools
			}
		}
	}

	// We want some flags, such as model, to be able to overwrite the profile configurations
	// If this gets too confusing, it should be changed
	applyProfileOverridesForText(&tConf, flagSet, defaultFlags)
	err = tConf.SetupInitialChat(args)
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
		fmt.Println(ProfileHelp)
		return
	}

	cfgDir, _ := utils.GetClaiConfigDir()
	cacheDir, _ := utils.GetClaiCacheDir()
	fmt.Printf(usage,
		defaultFlags.ReplyMode,
		defaultFlags.PrintRaw,
		cfgDir,
		cfgDir,
		cfgDir,
		cfgDir,
		defaultFlags.VideoDir,
		defaultFlags.VideoPrefix,
		defaultFlags.UseTools,
		defaultFlags.Glob,
		defaultFlags.Profile,
		cfgDir,
		cacheDir,
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

	claiConfDir, err := utils.GetClaiConfigDir()
	if err != nil {
		return nil, fmt.Errorf("failed to find config dir: %v", err)
	}

	switch mode {
	case CHAT, QUERY, GLOB, CMD:
		return setupTextQuerier(ctx, mode, claiConfDir, flagSet)
	case VIDEO:
		vConf, err := utils.LoadConfigFromFile(claiConfDir, "videoConfig.json", nil, &video.Default)
		if err != nil {
			return nil, fmt.Errorf("failed to load configs: %w", err)
		}
		applyFlagOverridesForVideo(&vConf, flagSet, defaultFlags)

		err = vConf.SetupPrompts()
		if err != nil {
			return nil, fmt.Errorf("failed to setup prompt: %v", err)
		}
		vq, err := CreateVideoQuerier(vConf)
		if err != nil {
			return nil, fmt.Errorf("failed to create video querier: %v", err)
		}
		return vq, nil
	case PHOTO:
		pConf, err := utils.LoadConfigFromFile(claiConfDir, "photoConfig.json", migrateOldPhotoConfig, &photo.DEFAULT)
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
		pq, err := CreatePhotoQuerier(pConf)
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
		fmt.Println(bi.Main.Path)
		for _, dep := range bi.Deps {
			fmt.Printf("%s %s\n", dep.Path, dep.Version)
		}
		return nil, nil
	case SETUP:
		err := setup.SubCmd()
		if err != nil {
			return nil, fmt.Errorf("failed to run setup: %w", err)
		}
		os.Exit(0)
		return nil, nil
	case REPLAY:
		err := chat.Replay(flagSet.PrintRaw)
		if err != nil {
			return nil, fmt.Errorf("failed to replay previous reply: %w", err)
		}
		os.Exit(0)
	case TOOLS:
		tools.Init()
		return nil, tools.SubCmd(ctx, args)
	case PROFILES:
		return nil, profiles.SubCmd(ctx, args)
	default:
		return nil, fmt.Errorf("unknown mode: %v", mode)
	}
	return nil, errors.New("unexpected conditional: how did you end up here?")
}
