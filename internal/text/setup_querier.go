package text

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/baalimago/clai/internal"
	"github.com/baalimago/clai/internal/chat"
	"github.com/baalimago/clai/internal/debugflags"
	"github.com/baalimago/clai/internal/glob"
	"github.com/baalimago/clai/internal/models"
	"github.com/baalimago/clai/internal/skills"
	"github.com/baalimago/clai/internal/tools"
	"github.com/baalimago/clai/internal/utils"
	textmodels "github.com/baalimago/clai/pkg/text/models"
	"github.com/baalimago/go_away_boilerplate/pkg/ancli"
	imagodebug "github.com/baalimago/go_away_boilerplate/pkg/debug"
	"github.com/baalimago/go_away_boilerplate/pkg/misc"
	"github.com/baalimago/go_away_boilerplate/pkg/table"
)

// setupToolConfig by matching the -t/-tools selection with tConf to ensure that tools
// are propperly enabled
func setupToolConfig(tConf *Configurations, useTools string) {
	if !tConf.UseTools {
		// Any indication from flags about tools is considered as a flag
		// that user wants the LLM to use some sort of tool configuration
		tConf.UseTools = useTools != ""
	}

	if useTools == "" {
		return
	}
	tConf.RequestedToolGlobs = append(tConf.RequestedToolGlobs, strings.Split(useTools, ",")...)
	if tConf.UseTools {
		// Validate against tool registry and allow MCP-prefixed names.
		// tools.Registry only knows *local* tools; MCP tools are prefixed "mcp_".
		tools.Init()

		validTools := make([]string, 0, len(tConf.RequestedToolGlobs))

		for _, p := range tConf.RequestedToolGlobs {
			if debugflags.Enabled("PROFILES") {
				ancli.Noticef("found: '%v' in RequestedToolGlobs", p)
			}
			p = strings.TrimSpace(p)
			if p == "" {
				continue
			}

			if p == "*" {
				tConf.RequestedToolGlobs = nil
				return
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
		if len(validTools) == 0 && useTools != "" {
			ancli.Warnf("no valid tools found from -t/-tools flag; disabling tools for this run\n")
			tConf.UseTools = false
			tConf.RequestedToolGlobs = nil
		} else {
			tConf.RequestedToolGlobs = validTools
		}
	}
}

// setupCmdBanConfig appends the -cmd-ban flag entries onto the file+profile
// base. The cascade is purely additive (D5): the flag can only add
// restrictions, never remove them. Entries are comma-split, space-trimmed,
// and empty segments are dropped.
func setupCmdBanConfig(tConf *Configurations, cmdBan string) {
	if cmdBan == "" {
		return
	}
	for entry := range strings.SplitSeq(cmdBan, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		tConf.CmdBan = append(tConf.CmdBan, entry)
	}
}

// SetupQuerier composes the full text-querier path: config load, flag and
// profile cascades, glob/tool/skill/lookback setup, prompt construction and
// vendor routing. trustInput is the reader for interactive skill-trust
// prompts (the composition root passes the wizard's input source; text
// cannot import setup).
func SetupQuerier(ctx context.Context, confDir string, tf internal.TextFlags, args []string, trustInput io.Reader) (models.Querier, *Configurations, error) {
	// The flagset is first used to find chatModel and potentially setup a new configuration file from some default
	tConf, err := utils.LoadConfigFromFile(confDir, "textConfig.json", MigrateOldChatConfig, &Default)
	tConf.ConfigDir = confDir
	if err != nil {
		return nil, nil, fmt.Errorf("failed to load configs: %w", err)
	}
	logSkillDiscovery := shouldLogSkillDiscovery(args)
	structuredOutput := false

	// At the moment, the configurations are based on the config file. But
	// the configuration presecende is flags > file > default. So, we need
	// to re-apply the flag overrides to the configuration
	ApplyFlagOverrides(&tConf, tf)

	// Load response format from file if specified
	if responseFormatPath := tf.QueryText.ResponseFormat.Value(); responseFormatPath != "" {
		if err := tConf.LoadResponseFormat(responseFormatPath); err != nil {
			return nil, nil, fmt.Errorf("response format: %w", err)
		}
		structuredOutput = true
		logSkillDiscovery = false
	}

	if misc.Truthy(os.Getenv("DEBUG")) {
		ancli.PrintOK(fmt.Sprintf("config post flag override: %+v\n", imagodebug.IndentedJsonFmt(tConf)))
	}
	if globPattern := tf.AgentText.Glob.Value(); globPattern != "" {
		globStr, retArgs, globErr := glob.Setup(globPattern, args)
		args = retArgs
		if globErr != nil {
			return nil, nil, fmt.Errorf("failed to setup glob: %w", globErr)
		}

		tConf.Glob = globStr
	}
	err = tConf.ProfileOverrides()
	if err != nil {
		return nil, nil, fmt.Errorf("profile override failure: %v", err)
	}

	setupToolConfig(&tConf, tf.AgentText.UseTools.Value())

	setupCmdBanConfig(&tConf, tf.AgentText.CmdBan.Value())

	// We want some flags, such as model, to be able to overwrite the profile configurations
	// If this gets too confusing, it should be changed
	ApplyProfileOverrides(&tConf, tf)
	skillsConfig, err := skills.LoadConfig(confDir)
	if err != nil {
		return nil, nil, fmt.Errorf("load skills config: %w", err)
	}
	if !profileSetsSkills(&tConf) {
		tConf.UseSkills = skillsConfig.Enabled
	}
	if err := applyUseSkillsOverride(&tConf, tf.QueryText.UseSkills); err != nil {
		return nil, nil, err
	}
	if tConf.UseSkills {
		tools.Init()
		allTools := tools.Registry.All()
		knownToolNames := make([]string, 0, len(allTools))
		for name := range allTools {
			knownToolNames = append(knownToolNames, name)
		}
		cacheDir, _ := utils.GetClaiCacheDir()
		skillLogLevel := skills.ParseLogLevelFromEnv()
		if structuredOutput {
			skillLogLevel = skills.LogLevelError
		}
		skillMgr, err := skills.Discover(skills.Options{
			ConfigDir:    confDir,
			CacheDir:     cacheDir,
			WorkingDir:   mustGetwd(),
			LogQueryText: logSkillDiscovery,
			TrustPrompter: func(_ context.Context, prompt skills.TrustPrompt) (bool, error) {
				ancli.Warnf("%s", formatSkillTrustPrompt(prompt))
				answer, err := table.ReadUserInputFrom(trustInput)
				if err != nil {
					return false, err
				}
				answer = strings.ToLower(strings.TrimSpace(answer))
				return answer == "y" || answer == "yes", nil
			},
			LogLevel:       skillLogLevel,
			KnownToolNames: knownToolNames,
			LocalTools:     allTools,
		})
		if err != nil {
			return nil, nil, fmt.Errorf("discover skills: %w", err)
		}
		tConf.SkillsDescriptor = skillMgr.DescriptorBlock()
		tConf.SkillLoader = skillRuntimeAdapter{mgr: skillMgr}
	}
	if err := setupLookback(confDir, &tConf, tf.AgentText.UseLookback); err != nil {
		return nil, nil, err
	}

	// When directory reply mode is active, load the dirscope head directly
	// into InitialChat so that SetupInitialChat uses context from the
	// directory binding instead of globalScope.json. This eliminates the
	// filesystem roundtrip (write globalScope then read it back) that was
	// the root cause of the -dre query forking bug.
	if tConf.DirReplyMode {
		dirChat, err := chat.LoadDirScopedContext(confDir)
		if err != nil {
			return nil, nil, fmt.Errorf("load dir-scoped context: %w", err)
		}
		tConf.InitialChat = dirChat
	}

	err = tConf.SetupInitialChat(args)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to setup prompt: %v", err)
	}

	cq, err := CreateQuerier(ctx, tConf)

	if misc.Truthy(os.Getenv("DEBUG")) {
		ancli.PrintOK(fmt.Sprintf("querier post text querier create: %+v\n", tConf))
	}
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create text querier: %v", err)
	}
	return cq, &tConf, nil
}

// lookbackInjectCount is how many of the newest history entries the passive
// descriptor block surfaces.
const lookbackInjectCount = 5

// setupLookback resolves conversation-lookback enablement with precedence
// CLI (-lb/-lookback) > profile (use_lookback) > config default, captures the
// session CWD as the default search anchor, and — when enabled and the CWD
// binding has recorded history — builds the recent-conversations descriptor and
// marks the lookback active so the tools are registered. Enabled-but-no-history
// surfaces nothing, matching the dirscope spec.
func setupLookback(confDir string, tConf *Configurations, lookback internal.BoolFlag) error {
	// Profile overrides have already been applied to tConf.UseLookback.
	// The flag (if explicitly set) takes final precedence, in both directions:
	// -lb enables, -lb=false disables profile/file-enabled lookback.
	if lookback.Explicit() {
		tConf.UseLookback = lookback.Value()
	}

	// The session CWD is the default anchor; the searcher canonicalizes it.
	tConf.LookbackCWD = mustGetwd()
	if !tConf.UseLookback {
		return nil
	}

	// Setup notices are debug-only chatter: they print when DEBUG_LOOKBACK (or
	// plain DEBUG) is truthy, and never in structured-response mode.
	debugLookback := debugflags.Enabled("LOOKBACK")

	// Tool registration is decoupled from local history: whenever lookback is
	// enabled the search/inspect/read tools are registered (so the agent can search
	// OTHER directories even from a dir with no recorded history). The passive
	// descriptor block, by contrast, is only injected when the CWD has history.
	desc, err := chat.BuildLookbackDescriptor(confDir, tConf.LookbackCWD, lookbackInjectCount)
	if err != nil {
		return fmt.Errorf("build lookback descriptor: %w", err)
	}
	if desc.HasHistory {
		tConf.LookbackDescriptor = desc.Block
		if debugLookback && tConf.ResponseFormat == nil {
			ancli.Noticef("lookback: surfaced %d recent conversation(s) for this directory\n", desc.Shown)
		}
	} else if debugLookback && tConf.ResponseFormat == nil {
		ancli.Noticef("lookback: enabled (no recorded history in this directory yet; search other paths with search_conversations)\n")
	}
	return nil
}

func applyUseSkillsOverride(tConf *Configurations, useSkills internal.StringFlag) error {
	if !useSkills.Changed() {
		return nil
	}
	switch useSkills.Value() {
	case "*":
		tConf.UseSkills = true
	case "none":
		tConf.UseSkills = false
	default:
		return fmt.Errorf("invalid skills flag value %q: expected '*' or 'none'", useSkills.Value())
	}
	return nil
}

func profileSetsSkills(tConf *Configurations) bool {
	return tConf.ProfileUseSkillsSet
}

func shouldLogSkillDiscovery(args []string) bool {
	for _, arg := range args[1:] {
		if strings.TrimSpace(arg) != "" {
			return true
		}
	}
	return false
}

// applyDirReplyChatID binds the directory-scoped chat id onto the querier.
func applyDirReplyChatID(confDir string, tConf *Configurations, q models.Querier) error {
	if tConf == nil {
		return fmt.Errorf("text configuration is nil")
	}
	chatID, err := chat.LoadDirScopeChatID(confDir)
	if err != nil {
		return fmt.Errorf("load dir reply chat id: %w", err)
	}
	if chatID == "" {
		return nil
	}
	tConf.InitialChat.ID = chatID
	if chatSetter, ok := q.(interface{ SetChatID(string) }); ok {
		chatSetter.SetChatID(chatID)
	}
	return nil
}

type skillRuntimeAdapter struct{ mgr *skills.Manager }

func (s skillRuntimeAdapter) LoadSkill(ctx context.Context, name, args string, baseTools map[string]textmodels.LLMTool) (LoadedSkillRuntime, error) {
	loaded, err := s.mgr.LoadSkill(ctx, name, args, baseTools)
	if err != nil {
		return LoadedSkillRuntime{}, err
	}
	return LoadedSkillRuntime{
		Name:            loaded.Skill.Name,
		SourceClass:     loaded.Skill.SourceClass,
		RenderedBody:    loaded.RenderedBody,
		UserVisibleBody: loaded.RenderedBody,
		Description:     loaded.Skill.Parsed.Metadata.Description,
		Warnings:        loaded.Warnings,
		EnabledTools:    loaded.EnabledTools,
		ActiveTools:     loaded.ActiveTools,
		ActivationErr:   loaded.ActivationErr,
		RawArgs:         loaded.RawArgs,
	}, nil
}

func formatSkillPromptDescription(description string) string {
	description = strings.TrimSpace(description)
	if description == "" {
		return "(none)"
	}
	return description
}

func formatSkillTrustPrompt(prompt skills.TrustPrompt) string {
	return fmt.Sprintf("Untrusted skill detected!\n  Name: %s\n  Source: %s\n  Path: %s\n  Hash: %s\n  Description: %s\n  Note: disable this check in settings with trust_all_skills.\nTrust this skill? [y/N]", prompt.Name, prompt.SourceClass, prompt.Path, prompt.Hash, formatSkillPromptDescription(prompt.Description))
}

func mustGetwd() string {
	wd, _ := os.Getwd()
	return wd
}
