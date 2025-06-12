package text

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path"

	"github.com/baalimago/clai/internal/models"
	"github.com/baalimago/clai/internal/tools"
	"github.com/baalimago/clai/internal/tools/mcp"
	"github.com/baalimago/go_away_boilerplate/pkg/ancli"
	"github.com/baalimago/go_away_boilerplate/pkg/misc"
)

// addMcpTools by loading os.GetConfigDir()/.clai/mcpServerConfig.json
// Each MCP server is then spawned as a context aware subprocess and if successfully
// started the tools it hosts are queried + appended to the tools.Tools list with the
// MCP server's name as prefix
// If the config is missing, return an error highlighting this
func addMcpTools(ctx context.Context, mcpServerConfigPath string) error {
	if _, err := os.Stat(mcpServerConfigPath); os.IsNotExist(err) {
		return fmt.Errorf("MCP server config not found at %s.\nIf you want MCP server support, create one using 'clai setup' and select option 3", mcpServerConfigPath)
	}

	configData, err := os.ReadFile(mcpServerConfigPath)
	if err != nil {
		return fmt.Errorf("failed to read MCP server config: %w", err)
	}

	var mcpServerConfig tools.McpServerConfig
	if err := json.Unmarshal(configData, &mcpServerConfig); err != nil {
		return fmt.Errorf("failed to unmarshal MCP server config: %w", err)
	}

	controlChannel := make(chan mcp.ControlEvent)
	statusChan := make(chan error, 1)

	go mcp.Manager(ctx, controlChannel, statusChan)

	for serverName, mcpServer := range mcpServerConfig {
		inputChan, outputChan, err := mcp.Client(ctx, mcpServer)
		if err != nil {
			continue
		}

		controlChannel <- mcp.ControlEvent{
			ServerName: serverName,
			Server:     mcpServer,
			InputChan:  inputChan,
			OutputChan: outputChan,
		}
	}

	select {
	case err := <-statusChan:
		if err != nil {
			return fmt.Errorf("MCP client manager failed: %w", err)
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func setupTooling[C models.StreamCompleter](ctx context.Context, modelConf C, userConf Configurations) {
	toolBox, ok := any(modelConf).(models.ToolBox)
	if ok && userConf.UseTools {
		if misc.Truthy(os.Getenv("DEBUG")) {
			ancli.PrintOK(fmt.Sprintf("Registering tools on type: %T\n", modelConf))
		}
		err := addMcpTools(ctx, path.Join(userConf.ConfigDir, "mcpServerConfig.json"))
		if err != nil {
			ancli.Warnf("failed to add mcp tools: %v", err)
		}
		// If usetools and no specific tools chocen, assume all are valid
		if len(userConf.Tools) == 0 {
			for _, tool := range tools.Tools.All() {
				if misc.Truthy(os.Getenv("DEBUG")) {
					ancli.PrintOK(fmt.Sprintf("\tadding tool: %T\n", tool))
				}
				toolBox.RegisterTool(tool)
			}
		} else {
			for _, t := range userConf.Tools {
				tool, exists := tools.Tools.Get(t)
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
}
