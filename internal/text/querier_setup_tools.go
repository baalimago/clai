package text

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"

	"github.com/baalimago/clai/internal/models"
	"github.com/baalimago/clai/internal/tools"
	"github.com/baalimago/clai/internal/tools/mcp"
	"github.com/baalimago/go_away_boilerplate/pkg/ancli"
	"github.com/baalimago/go_away_boilerplate/pkg/misc"
)

// addMcpTools loads MCP server configurations from a directory.
// Each file inside the directory should contain a single MCP server configuration.
// Every server is started and its tools registered with a prefix of the filename.
// If the directory is missing, an error is returned.
func addMcpTools(ctx context.Context, mcpServersDir string) error {
	if _, err := os.Stat(mcpServersDir); os.IsNotExist(err) {
		return fmt.Errorf("MCP servers directory not found at %s.\nIf you want MCP server support, create one using 'clai setup' and select option 3", mcpServersDir)
	}

	files, err := filepath.Glob(filepath.Join(mcpServersDir, "*.json"))
	if err != nil {
		return fmt.Errorf("failed to list mcp server configs: %w", err)
	}

	controlChannel := make(chan mcp.ControlEvent)
	statusChan := make(chan error, 1)

	toolWg := sync.WaitGroup{}
	toolWg.Add(len(files))
	go mcp.Manager(ctx, controlChannel, statusChan, &toolWg)

	for _, file := range files {
		data, err := os.ReadFile(file)
		if err != nil {
			continue
		}
		var mcpServer tools.McpServer
		if unmarshalErr := json.Unmarshal(data, &mcpServer); unmarshalErr != nil {
			ancli.Warnf("failed to unmarshal: '%s', error: %v", file, unmarshalErr)
			continue
		}
		serverName := strings.TrimSuffix(filepath.Base(file), filepath.Ext(file))
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
	go func() {
		toolWg.Wait()
		statusChan <- nil
	}()

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
		err := addMcpTools(ctx, path.Join(userConf.ConfigDir, "mcpServers"))
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
					ancli.Warnf("attempted to find tool: '%v', which doesn't exist, skipping\n", t)
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
