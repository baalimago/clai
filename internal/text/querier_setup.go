package text

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/user"
	"path"
	"strings"

	"github.com/baalimago/clai/internal/models"
	"github.com/baalimago/clai/internal/tools"
	"github.com/baalimago/clai/internal/utils"
	"github.com/baalimago/go_away_boilerplate/pkg/ancli"
	"github.com/baalimago/go_away_boilerplate/pkg/debug"
	"github.com/baalimago/go_away_boilerplate/pkg/misc"
)

func vendorType(fromModel string) (string, string, string) {
	if strings.Contains(fromModel, "gpt") {
		return "openai", "gpt", fromModel
	}
	if strings.Contains(fromModel, "claude") {
		return "anthropic", "claude", fromModel
	}
	if strings.Contains(fromModel, "ollama") {
		m := "llama3"
		if strings.HasPrefix(fromModel, "ollama:") {
			m = fromModel[7:]
		}
		return "ollama", m, fromModel
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

		return "novita", m, modelVersion
	}
	if strings.Contains(fromModel, "mistral") || strings.Contains(fromModel, "mixtral") {
		return "mistral", "mistral", fromModel
	}

	if strings.Contains(fromModel, "deepseek") {
		return "deepseek", "deepseek", fromModel
	}
	if strings.Contains(fromModel, "mock") {
		return "mock", "mock", "mock"
	}

	return "VENDOR", "NOT", "FOUND"
}

func NewQuerier[C models.StreamCompleter](userConf Configurations, dfault C) (Querier[C], error) {
	vendor, model, modelVersion := vendorType(userConf.Model)
	claiConfDir := userConf.ConfigDir
	configPath := path.Join(claiConfDir, fmt.Sprintf("%v_%v_%v.json", vendor, model, modelVersion))
	querier := Querier[C]{}
	querier.configDir = claiConfDir
	var modelConf C
	err := utils.ReadAndUnmarshal(configPath, &modelConf)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			data, err := json.Marshal(dfault)
			if err != nil {
				return querier, fmt.Errorf("failed to marshal default model: %v, error: %w", dfault, err)
			}
			err = os.WriteFile(configPath, data, os.FileMode(0o644))
			if err != nil {
				return querier, fmt.Errorf("failed to write default model: %v, error: %w", dfault, err)
			}

			err = utils.ReadAndUnmarshal(configPath, &modelConf)
			if err != nil {
				return querier, fmt.Errorf("failed to read default model: %v, error: %w", dfault, err)
			}
		} else {
			return querier, fmt.Errorf("failed to load querier of model: %v, error: %w", userConf.Model, err)
		}
	}

	if misc.Truthy(os.Getenv("DEBUG")) {
		ancli.PrintOK(fmt.Sprintf("userConf: %v\n", debug.IndentedJsonFmt(userConf)))
	}
	toolBox, ok := any(modelConf).(models.ToolBox)
	if ok && userConf.UseTools {
		if misc.Truthy(os.Getenv("DEBUG")) {
			ancli.PrintOK(fmt.Sprintf("Registering tools on type: %T\n", modelConf))
		}
		// If usetools and no specific tools chocen, assume all are valid
		if len(userConf.Tools) == 0 {
			for _, tool := range tools.Tools {
				if misc.Truthy(os.Getenv("DEBUG")) {
					ancli.PrintOK(fmt.Sprintf("\tadding tool: %T\n", tool))
				}
				toolBox.RegisterTool(tool)
			}
		} else {
			for _, t := range userConf.Tools {
				tool, exists := tools.Tools[t]
				if !exists {
					ancli.PrintWarn(fmt.Sprintf("attempted to find tool: '%v', which doesn't exist, skipping", tool))
					continue
				}

				if misc.Truthy(os.Getenv("DEBUG")) {
					ancli.PrintOK(fmt.Sprintf("\tadding tool: %T\n", tool))
				}
				toolBox.RegisterTool(tool)
			}
		}
	}

	err = modelConf.Setup()
	if err != nil {
		return Querier[C]{}, fmt.Errorf("failed to setup model: %w", err)
	}

	termWidth, err := utils.TermWidth()
	if err == nil {
		querier.termWidth = termWidth
	} else {
		ancli.PrintWarn(fmt.Sprintf("failed to get terminal size: %v\n", err))
	}
	currentUser, err := user.Current()
	if err == nil {
		querier.username = currentUser.Username
	} else {
		querier.username = "user"
	}
	querier.Model = modelConf
	if misc.Truthy(os.Getenv("DEBUG")) {
		ancli.PrintOK(fmt.Sprintf("querier: %v,\n===\nmodels: %v\n", debug.IndentedJsonFmt(querier), debug.IndentedJsonFmt(modelConf)))
	}
	querier.chat = userConf.InitialPrompt
	if misc.Truthy(os.Getenv("DEBUG")) || misc.Truthy(os.Getenv("TEXT_QUERIER_DEBUG")) {
		querier.debug = true
	}
	querier.Raw = userConf.Raw
	querier.cmdMode = userConf.CmdMode
	querier.shouldSaveReply = !userConf.ChatMode && userConf.SaveReplyAsConv
	querier.tokenWarnLimit = userConf.TokenWarnLimit
	return querier, nil
}
