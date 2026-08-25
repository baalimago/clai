package text

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/user"
	"path"
	"strings"

	"github.com/baalimago/clai/internal/cost"
	"github.com/baalimago/clai/internal/debugflags"
	"github.com/baalimago/clai/internal/models"
	"github.com/baalimago/clai/internal/text/generic"
	"github.com/baalimago/clai/internal/utils"
	"github.com/baalimago/clai/internal/vendors/openrouter"
	pub_models "github.com/baalimago/clai/pkg/text/models"
	pkgtools "github.com/baalimago/clai/pkg/tools"
	"github.com/baalimago/go_away_boilerplate/pkg/ancli"
	"github.com/baalimago/go_away_boilerplate/pkg/debug"
	"github.com/baalimago/go_away_boilerplate/pkg/misc"
)

// CanonicalModelString is the inverse of vendorType. Given a (vendor, family, modelVersion)
// triplet — typically parsed from a config filename — it returns the canonical model
// identifier that can be placed in a profile's "model" field and correctly routed.
func CanonicalModelString(vendor, family, modelVersion string) string {
	switch vendor {
	case "openrouter":
		return "or:" + modelVersion
	case "berget":
		if family == "berget" {
			return "berget:" + modelVersion
		}
		return "berget:" + family + "/" + modelVersion
	case "ollama":
		if strings.HasPrefix(modelVersion, "ollama:") {
			return modelVersion
		}
		return modelVersion
	case "novita":
		if strings.HasPrefix(modelVersion, "novita:") {
			return modelVersion
		}
		if family != "" {
			return "novita:" + family + "/" + modelVersion
		}
		return modelVersion
	case "huggingface", "hf":
		return "hf:" + modelVersion + ":" + family
	default:
		return modelVersion
	}
}

func vendorType(fromModel string) (string, string, string, error) {
	if strings.Contains(fromModel, "test") {
		return "mock", "test", fromModel, nil
	}
	if after, ok := strings.CutPrefix(fromModel, "or:"); ok {
		modelVersion := after
		return "openrouter", "chat", modelVersion, nil
	}
	if strings.HasPrefix(fromModel, "berget:") {
		vendor := "berget"
		modelVersion := fromModel[7:]
		parts := strings.Split(modelVersion, "/")
		if len(parts) > 1 {
			vendor = parts[0]
			modelVersion = parts[1]
		}
		return "berget", vendor, modelVersion, nil
	}
	if strings.Contains(fromModel, "gpt") {
		return "openai", "gpt", fromModel, nil
	}
	if strings.Contains(fromModel, "claude") {
		return "anthropic", "claude", fromModel, nil
	}
	if strings.Contains(fromModel, "ollama") {
		m := "llama3"
		if strings.HasPrefix(fromModel, "ollama:") {
			m = fromModel[7:]
		}
		return "ollama", m, fromModel, nil
	}
	if strings.Contains(fromModel, "novita") {
		m := ""
		modelVersion := fromModel
		if strings.HasPrefix(fromModel, "novita:") {
			parts := strings.Split(fromModel[7:], "/")
			if len(parts) > 1 {
				m = parts[0]
				modelVersion = parts[1]
			}
		}

		return "novita", m, modelVersion, nil
	}
	if strings.Contains(fromModel, "mistral") ||
		strings.Contains(fromModel, "mixtral") ||
		strings.Contains(fromModel, "codestral") ||
		strings.Contains(fromModel, "devstral") {
		return "mistral", "mistral", fromModel, nil
	}

	if strings.Contains(fromModel, "deepseek") {
		return "deepseek", "deepseek", fromModel, nil
	}
	if strings.Contains(fromModel, "mock") {
		return "mock", "mock", "mock", nil
	}
	if strings.Contains(fromModel, "mercury") {
		return "inception", "mercury", fromModel, nil
	}
	if strings.Contains(fromModel, "grok") {
		return "xai", "grok", fromModel, nil
	}
	if strings.Contains(fromModel, "gemini") {
		return "google", "gemini", fromModel, nil
	}
	if strings.HasPrefix(fromModel, "hf:") || strings.HasPrefix(fromModel, "huggingface:") {
		split := strings.Split(fromModel, ":")
		if len(split) < 3 {
			return "huggingface", fromModel, "", nil
		}
		// Format is: "hf:<model>:<inference provider>"
		// So we return modelVersion as split[1], and inference provider as "model"
		// The model is currently (26-01) only semantic, so it has no other usecase, so it works for now
		return split[0], split[2], split[1], nil
	}

	return "", "", "", fmt.Errorf("failed to find vendor for: %v", fromModel)
}

// setupConfigFile using unholy named returns since it kind of fits and im too lazy to explicitly declare. Hobby project
// and all that, be happy im refactoring this into something comprehensive at all..!
func setupConfigFile[C models.StreamCompleter](configPath string, userConf Configurations, dfault C) (modelConf C, retErr error) {
	retErr = utils.ReadAndUnmarshal(configPath, &modelConf)
	if retErr != nil {
		if errors.Is(retErr, os.ErrNotExist) {
			// Reset the retErr since any further error
			// will be returned as new errors
			retErr = nil
			data, err := json.Marshal(dfault)
			if err != nil {
				retErr = err
				return modelConf, fmt.Errorf("failed to marshal default model: %v, error: %w", dfault, retErr)
			}

			err = os.WriteFile(configPath, data, os.FileMode(0o644))
			if err != nil {
				return modelConf, fmt.Errorf("failed to write default model: %v, error: %w", dfault, err)
			}

			err = utils.ReadAndUnmarshal(configPath, &modelConf)
			if err != nil {
				return modelConf, fmt.Errorf("failed to read default model: %v, error: %w", dfault, err)
			}
		} else {
			return modelConf, fmt.Errorf("failed to load querier of model: %v, error: %w", userConf.Model, retErr)
		}
	}
	retErr = nil
	return
}

func NewQuerier[C models.StreamCompleter](ctx context.Context, userConf Configurations, dfault C) (Querier[C], error) {
	traceChatf("new querier start model=%q config_dir=%q initial_chat_id=%q messages=%d", userConf.Model, userConf.ConfigDir, userConf.InitialChat.ID, len(userConf.InitialChat.Messages))
	vendor, model, modelVersion, err := vendorType(userConf.Model)
	if err != nil {
		return Querier[C]{}, fmt.Errorf("failed to find vendorType: %w", err)
	}
	traceChatf("new querier vendor resolved vendor=%q model=%q version=%q", vendor, model, modelVersion)
	claiConfDir := userConf.ConfigDir
	noFrontslashModelVersion := strings.ReplaceAll(modelVersion, "/", "_")
	configPath := path.Join(claiConfDir, fmt.Sprintf("%v_%v_%v.json", vendor, model, noFrontslashModelVersion))
	traceChatf("new querier model config path=%q", configPath)
	querier := Querier[C]{}
	if debugflags.Enabled("TEXT_QUERIER") {
		querier.debug = true
	}
	querier.configDir = claiConfDir
	modelConf, err := setupConfigFile(configPath, userConf, dfault)
	if err != nil {
		return Querier[C]{}, fmt.Errorf("failed to setup config file: %w", err)
	}
	traceChatf("new querier model config loaded path=%q", configPath)

	if querier.debug {
		ancli.PrintOK(fmt.Sprintf("userConf: %v\n", debug.IndentedJsonFmt(userConf)))
	}
	// Inject the per-run command ban list at the spawn point before any tool
	// is registered, so every freetext execution in this run is covered (D6).
	// Unconditional: the default empty list keeps behavior permissive (D4).
	pkgtools.SetCmdBanList(userConf.CmdBan)
	querier.Raw = userConf.Raw
	output := userConf.Out
	if output == nil {
		output = os.Stdout
	}
	querier.outputModeKnown = true
	querier.outputIsTerminal = utils.IsTerminalWriter(output)
	querier.structuredOutput = userConf.ResponseFormat != nil
	querier.shouldSaveReply = !userConf.ChatMode && userConf.SaveReplyAsConv
	querier.replyMode = userConf.ReplyMode
	querier.dirReplyMode = userConf.DirReplyMode
	querier.useLookback = userConf.UseLookback
	querier.lookbackCWD = userConf.LookbackCWD
	querier.tooling.outputRuneLimit = userConf.ToolOutputRuneLimit
	querier.tooling.maxCalls = userConf.MaxToolCalls
	querier.stoploss = userConf.Stoploss
	// Agent-only runtime settings (slog logger, level, rune cap, recorder
	// hooks) ride one pointer (worklog 2026-08-15-agent-slog-output, D7). nil (the CLI and pkg/text paths) keeps
	// every channel disabled; the loose Configurations recorder fields no
	// longer exist.
	if userConf.AgentSettings != nil {
		querier.callUsageRecorder = userConf.AgentSettings.UsageRecorder
		querier.tooling.callRecorder = userConf.AgentSettings.ToolCallRecorder
		querier.agentSettings = userConf.AgentSettings
	}
	querier.out = output
	// MCP server stderr lines follow the same display policy as the session:
	// rolling output buffers them into the window, raw/structured output keeps
	// only errors (on stderr, so stdout stays clean), and any other mode keeps
	// the legacy direct print.
	querier.mcpSink = newMcpLogSink(mcpLogModeFor(querier.debug, querier.outputIsTerminal, querier.Raw, querier.structuredOutput, utils.RollingOutputEnabled()))
	setupTooling(ctx, modelConf, &userConf, querier.mcpSink)

	err = modelConf.Setup()
	if err != nil {
		return Querier[C]{}, fmt.Errorf("failed to setup model: %w", err)
	}

	traceChatf("post setup")
	currentUser, err := user.Current()
	if err == nil {
		querier.username = currentUser.Username
	} else {
		querier.username = "user"
	}
	traceChatf("user is: %v", currentUser.Name)
	querier.Model = modelConf
	traceChatf("querier: %v,\n===\nmodels: %v\n",
		debug.IndentedJsonFmt(querier),
		debug.IndentedJsonFmt(modelConf))
	querier.chat = userConf.InitialChat
	querier.systemPrompt = userConf.SystemPrompt
	traceChatf("new querier chat attached chat_id=%q messages=%d system_prompt_len=%d", querier.chat.ID, len(querier.chat.Messages), len(querier.systemPrompt))
	// Ensure profile selection is persisted in globalScope/saved conversations.
	querier.chat.Profile = userConf.UseProfile
	// One dimensions snapshot per interactive output session, bound to the
	// writer's fd: the observed size matches the file clai actually writes to
	// (R2-02). A non-terminal writer or a failed read yields the deterministic
	// dimensions.Fallback, so no width-aware path ever queries the terminal
	// again.
	querier.dims = utils.SessionDimensions(output)
	querier.tooling.skillLoader = userConf.SkillLoader
	querier.tooling.base = userConf.BaseTools
	querier.tooling.run = userConf.BaseTools
	querier.tooling.registered = userConf.RegisteredTools

	// Propagate response format to the model if it supports it
	if userConf.ResponseFormat != nil {
		if setter, ok := any(modelConf).(generic.ResponseFormatSetter); ok {
			setter.SetResponseFormat(toGenericResponseFormat(userConf.ResponseFormat))
		}
	}

	var fetcher cost.ModelCatalogFetcher
	if fetcher == nil {
		openrouterAPIKey := os.Getenv("OPENROUTER_API_KEY")
		if openrouterAPIKey != "" {
			openrouterCatalogFetcher, err := openrouter.NewModelCatalog(openrouterAPIKey)
			if err != nil {
				ancli.Warnf("found OPENROUTER_API_KEY but failed to init catalog fether: %v", err)
			}
			fetcher = openrouterCatalogFetcher
		}
	}
	costManager := new(cost.NewManager(fetcher, modelVersion, configPath))
	costManager.SetModelResolver(func(_ pub_models.Chat) string {
		if modelNamer, ok := any(modelConf).(ModelNamer); ok {
			if modelName := strings.TrimSpace(modelNamer.ModelName()); modelName != "" {
				return modelName
			}
		}
		return modelVersion
	})
	rdyChan, errChan := costManager.Start(ctx)
	querier.costEnricher = newCostEnricher(costManager, rdyChan)
	// IMPORTANT: avoid spawning a goroutine that writes to stdout/stderr in tests.
	// Some tests capture stdout by swapping the global os.Stdout which will race
	// with concurrent writers under -race.
	if !misc.Truthy(os.Getenv("CLAI_DISABLE_COST_ERR_LOG_GOROUTINE")) {
		go func() {
			for {
				select {
				case <-ctx.Done():
					return
				case err, open := <-errChan:
					if !open {
						return
					}
					ancli.Warnf("cost manager error: %v", err)
				}
			}
		}()
	}

	return querier, nil
}
