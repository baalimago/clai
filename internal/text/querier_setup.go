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
	"github.com/baalimago/go_away_boilerplate/pkg/misc"
)

func vendorType(fromModel string) (string, string, string) {
	if strings.Contains(fromModel, "gpt") {
		return "openai", "gpt", fromModel
	}
	if strings.Contains(fromModel, "claude") {
		return "anthropic", "claude", fromModel
	}
	if strings.Contains(fromModel, "mistral") || strings.Contains(fromModel, "mixtral") {
		return "mistral", "mistral", fromModel
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

	toolBox, ok := any(modelConf).(models.ToolBox)
	if ok && userConf.UseTools {
		ancli.PrintOK(fmt.Sprintf("Registering tools, type: %T\n", modelConf))
		toolBox.RegisterTool(tools.FileTree)
		toolBox.RegisterTool(tools.Cat)
		toolBox.RegisterTool(tools.FileType)
		toolBox.RegisterTool(tools.LS)
		toolBox.RegisterTool(tools.Find)
	}

	err = modelConf.Setup()
	if err != nil {
		return Querier[C]{}, fmt.Errorf("failed to setup model: %w", err)
	}

	termWidth, err := utils.TermWidth()
	querier.termWidth = termWidth
	if err != nil {
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
		ancli.PrintOK(fmt.Sprintf("querier: %+v, models: %+v", querier, modelConf))
	}
	querier.chat = userConf.InitialPrompt
	if misc.Truthy(os.Getenv("DEBUG")) || misc.Truthy(os.Getenv("TEXT_QUERIER_DEBUG")) {
		querier.debug = true
	}
	querier.Raw = userConf.Raw
	querier.shouldSaveReply = !userConf.ChatMode
	querier.tokenWarnLimit = userConf.TokenWarnLimit
	return querier, nil
}
