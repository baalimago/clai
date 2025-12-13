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

	pub_models "github.com/baalimago/clai/pkg/text/models"
)

// filterMcpServersByProfile filters MCP server files based on whether their tools are needed by the profile
func filterMcpServersByProfile(mcpServerPaths []string, userConf Configurations) []string {
	// If no specific tools are configured, load all servers (existing behavior)
	if len(userConf.Tools) == 0 {
		return mcpServerPaths
	}

	var filteredFiles []string
	for _, file := range mcpServerPaths {
		serverName := strings.TrimSuffix(filepath.Base(file), filepath.Ext(file))

	INNER:
		for _, tool := range userConf.Tools {
			toolSplit := strings.Split(tool, "_")
			// It can't have mcp prefix
			if len(toolSplit) < 2 {
				continue
			}
			// Its not a mcp server nor tool
			if toolSplit[0] != "mcp" {
				continue
			}
			toolServer := toolSplit[1]
			hit := tools.WildcardMatch(toolServer, serverName)
			if hit {
				filteredFiles = append(filteredFiles, file)
				break INNER
			}
		}
	}

	return filteredFiles
}

// addMcpTools loads MCP server configurations from a directory.
// Each file inside the directory should contain a single MCP server configuration.
// Every server is started and its tools registered with a prefix of the filename.
// If the directory is missing, an error is returned.
func AddMcpTools(ctx context.Context, mcpServersDir string, userConf Configurations) error {
	if _, err := os.Stat(mcpServersDir); os.IsNotExist(err) {
		return fmt.Errorf("MCP servers directory not found at %s. If you want MCP server support, create one using 'clai setup' and select option 3", mcpServersDir)
	}

	files, err := filepath.Glob(filepath.Join(mcpServersDir, "*.json"))
	if err != nil {
		return fmt.Errorf("failed to list mcp server configs: %w", err)
	}
	// Filter MCP servers based on profile tools
	filteredFiles := filterMcpServersByProfile(files, userConf)
	controlChannel := make(chan mcp.ControlEvent)
	statusChan := make(chan error, 1)

	toolWg := sync.WaitGroup{}
	toolWg.Add(len(filteredFiles))
	go mcp.Manager(ctx, controlChannel, statusChan, &toolWg)

	for _, file := range filteredFiles {
		data, err := os.ReadFile(file)
		if err != nil {
			continue
		}
		var mcpServer pub_models.McpServer
		if unmarshalErr := json.Unmarshal(data, &mcpServer); unmarshalErr != nil {
			ancli.Warnf("failed to unmarshal: '%s', error: %v", file, unmarshalErr)
			continue
		}
		serverName := strings.TrimSuffix(filepath.Base(file), filepath.Ext(file))
		mcpServer.Name = serverName
		// No context leak here as it's a child of the root context, which will cascade the cancel
		// for all other code paths
		clientContext, clientContextCancel := context.WithCancel(ctx)
		inputChan, outputChan, err := mcp.Client(clientContext, mcpServer)
		if err != nil {
			ancli.Warnf("failed to setup: '%v', err: %v\n", serverName, err)
			toolWg.Done()
			clientContextCancel()
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
	if !ok || !userConf.UseTools {
		return
	}
	tools.Init()
	// Only setup MCP tools if they're there's a chance of using tools
	err := AddMcpTools(ctx, path.Join(userConf.ConfigDir, "mcpServers"), userConf)
	if misc.Truthy(os.Getenv("DEBUG")) {
		ancli.Okf("Registering tools on querier of type: %T\n", modelConf)
	}
	if err != nil {
		ancli.Warnf("failed to add mcp tools: %v", err)
		return
	}
	// If usetools and no specific tools chocen, assume all are valid
	if len(userConf.Tools) == 0 {
		for _, tool := range tools.Registry.All() {
			toolBox.RegisterTool(tool)
		}
		return
	}
	toAdd := make([]tools.LLMTool, 0)
	for _, t := range userConf.Tools {
		if strings.Contains(t, "*") {
			matchingTools := tools.Registry.WildcardGet(t)
			if len(matchingTools) == 0 {
				ancli.Warnf("attempted to find tools using wildcard search: '%v', found none\n", t)
			}
			toAdd = append(toAdd, matchingTools...)
			continue
		}
		tool, exists := tools.Registry.Get(t)
		if !exists {
			ancli.Warnf("attempted to find tool: '%v', which doesn't exist, skipping\n", t)
			continue
		}
		toAdd = append(toAdd, tool)
	}

	for _, tool := range toAdd {
		if misc.Truthy(os.Getenv("DEBUG")) {
			ancli.PrintOK(fmt.Sprintf("\tname: %v, desc: %v\n", tool.Specification().Name, tool.Specification().Description))
		}
		toolBox.RegisterTool(tool)
	}
}
