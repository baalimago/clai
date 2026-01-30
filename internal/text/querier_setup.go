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

	"github.com/baalimago/clai/internal/models"
	"github.com/baalimago/clai/internal/utils"
	"github.com/baalimago/go_away_boilerplate/pkg/ancli"
	"github.com/baalimago/go_away_boilerplate/pkg/debug"
	"github.com/baalimago/go_away_boilerplate/pkg/misc"
)

func vendorType(fromModel string) (string, string, string, error) {
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
		} else {
			// Format is: "hf:<model>:<inference provider>"
			// So we return modelVersion as split[1], and inference provider as "model"
			// The model is currently (26-01) only semantic, so it has no other usecase, so it works for now
			return split[0], split[2], split[1], nil
		}
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
	vendor, model, modelVersion, err := vendorType(userConf.Model)
	if err != nil {
		return Querier[C]{}, fmt.Errorf("failed to find vendorType: %v", err)
	}
	claiConfDir := userConf.ConfigDir
	noFrontslashModelVersion := strings.ReplaceAll(modelVersion, "/", "_")
	configPath := path.Join(claiConfDir, fmt.Sprintf("%v_%v_%v.json", vendor, model, noFrontslashModelVersion))
	ancli.Noticef("config path: %v, modelVersion: %v", configPath, modelVersion)
	querier := Querier[C]{}
	if misc.Truthy(os.Getenv("DEBUG")) || misc.Truthy(os.Getenv("TEXT_QUERIER_DEBUG")) {
		querier.debug = true
	}
	querier.configDir = claiConfDir
	modelConf, err := setupConfigFile(configPath, userConf, dfault)
	if err != nil {
		return Querier[C]{}, fmt.Errorf("failed to setup config file: %w", err)
	}

	if querier.debug {
		ancli.PrintOK(fmt.Sprintf("userConf: %v\n", debug.IndentedJsonFmt(userConf)))
	}
	setupTooling(ctx, modelConf, userConf)

	err = modelConf.Setup()
	if err != nil {
		return Querier[C]{}, fmt.Errorf("failed to setup model: %w", err)
	}

	termWidth, err := utils.TermWidth()
	if err == nil {
		querier.termWidth = termWidth
	} else {
		if querier.debug {
			ancli.PrintWarn(fmt.Sprintf("failed to get terminal size: %v\n", err))
		}
	}
	currentUser, err := user.Current()
	if err == nil {
		querier.username = currentUser.Username
	} else {
		querier.username = "user"
	}
	querier.Model = modelConf
	if querier.debug {
		ancli.Okf("querier: %v,\n===\nmodels: %v\n",
			debug.IndentedJsonFmt(querier),
			debug.IndentedJsonFmt(modelConf))

		ancli.Okf("Out is: %v", userConf.Out)
	}
	querier.chat = userConf.InitialChat
	querier.Raw = userConf.Raw
	querier.cmdMode = userConf.CmdMode
	querier.shouldSaveReply = !userConf.ChatMode && userConf.SaveReplyAsConv
	querier.tokenWarnLimit = userConf.TokenWarnLimit
	querier.toolOutputRuneLimit = userConf.ToolOutputRuneLimit
	querier.maxToolCalls = userConf.MaxToolCalls
	querier.out = userConf.Out
	return querier, nil
}
