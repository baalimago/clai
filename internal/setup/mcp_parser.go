package setup

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/baalimago/clai/internal/utils"
	pub_models "github.com/baalimago/clai/pkg/text/models"
	"github.com/baalimago/go_away_boilerplate/pkg/ancli"
)

// McpServerInput represents the external format that users might paste
type McpServerInput struct {
	McpServers map[string]McpServerExternal `json:"mcpServers"`
}

// McpServerExternal represents various external MCP server formats
type McpServerExternal struct {
	Command string            `json:"command"`
	Args    []string          `json:"args"`
	Env     map[string]string `json:"env,omitempty"`
	EnvFile string            `json:"envfile,omitempty"`
}

// ParseAndAddMcpServer parses pasted MCP server configuration and adds it to the system
func ParseAndAddMcpServer(mcpServersDir, pastedConfig string) ([]string, error) {
	// Try to parse as the external format first
	var input McpServerInput
	if err := json.Unmarshal([]byte(pastedConfig), &input); err != nil {
		return nil, fmt.Errorf("failed to parse MCP server configuration: %w", err)
	}

	if len(input.McpServers) == 0 {
		return nil, fmt.Errorf("no MCP servers found in configuration")
	}

	ret := make([]string, 0)

	// Convert and save each server
	for serverName, externalServer := range input.McpServers {
		internalServer := convertToInternalFormat(externalServer)

		// Save to individual file
		serverPath := filepath.Join(mcpServersDir, fmt.Sprintf("%s.json", serverName))
		if err := utils.CreateFile(serverPath, &internalServer); err != nil {
			return nil, fmt.Errorf("failed to create server file for %s: %w", serverName, err)
		}

		ancli.Noticef("Added MCP server: %s\n", serverName)
		ret = append(ret, serverName)
	}

	return ret, nil
}

// convertToInternalFormat converts external format to internal pub_models.McpServer format
func convertToInternalFormat(external McpServerExternal) pub_models.McpServer {
	internal := pub_models.McpServer{
		Command: external.Command,
		Args:    external.Args,
		Env:     external.Env,
		EnvFile: external.EnvFile,
	}

	// Initialize empty env map if nil
	if internal.Env == nil {
		internal.Env = make(map[string]string)
	}

	return internal
}

// ValidateMcpServerConfig validates that the pasted config is valid
func ValidateMcpServerConfig(pastedConfig string) error {
	pastedConfig = strings.TrimSpace(pastedConfig)
	if pastedConfig == "" {
		return fmt.Errorf("empty configuration provided")
	}

	// Try to parse as JSON
	var input McpServerInput
	if err := json.Unmarshal([]byte(pastedConfig), &input); err != nil {
		return fmt.Errorf("invalid JSON format: %w", err)
	}

	if len(input.McpServers) == 0 {
		return fmt.Errorf("no 'mcpServers' section found or it's empty")
	}

	// Validate each server
	for serverName, server := range input.McpServers {
		if serverName == "" {
			return fmt.Errorf("server name cannot be empty")
		}
		if server.Command == "" {
			return fmt.Errorf("command cannot be empty for server '%s'", serverName)
		}
	}

	return nil
}
