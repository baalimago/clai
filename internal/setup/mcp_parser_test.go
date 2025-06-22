package setup

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/baalimago/clai/internal/tools"
)

func TestValidateMcpServerConfig(t *testing.T) {
	tests := []struct {
		name        string
		config      string
		expectError bool
	}{
		{
			name:        "empty config",
			config:      "",
			expectError: true,
		},
		{
			name:        "invalid JSON",
			config:      `{"invalid": json}`,
			expectError: true,
		},
		{
			name: "valid config",
			config: `{
				"mcpServers": {
					"browsermcp": {
						"command": "npx",
						"args": ["@browsermcp/mcp@latest"]
					}
				}
			}`,
			expectError: false,
		},
		{
			name: "missing command",
			config: `{
				"mcpServers": {
					"test": {
						"args": ["something"]
					}
				}
			}`,
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateMcpServerConfig(tt.config)
			if tt.expectError && err == nil {
				t.Error("expected error but got none")
			}
			if !tt.expectError && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

func TestConvertToInternalFormat(t *testing.T) {
	external := McpServerExternal{
		Command: "npx",
		Args:    []string{"@browsermcp/mcp@latest"},
		Env:     map[string]string{"NODE_ENV": "production"},
	}

	internal := convertToInternalFormat(external)

	if internal.Command != "npx" {
		t.Errorf("expected command 'npx', got '%s'", internal.Command)
	}
	if len(internal.Args) != 1 || internal.Args[0] != "@browsermcp/mcp@latest" {
		t.Errorf("expected args ['@browsermcp/mcp@latest'], got %v", internal.Args)
	}
	if internal.Env["NODE_ENV"] != "production" {
		t.Errorf("expected env NODE_ENV=production, got %v", internal.Env)
	}
}

func TestConvertToInternalFormatNilEnv(t *testing.T) {
	external := McpServerExternal{
		Command: "echo",
		Args:    []string{"hello"},
	}

	internal := convertToInternalFormat(external)

	if internal.Env == nil {
		t.Error("expected env to be initialized as empty map")
	}
}

func TestParseAndAddMcpServer(t *testing.T) {
	tempDir := t.TempDir()

	config := `{
		"mcpServers": {
			"browsermcp": {
				"command": "npx",
				"args": ["@browsermcp/mcp@latest"]
			},
			"filesystem": {
				"command": "npx",
				"args": ["-y", "@modelcontextprotocol/server-filesystem", "/tmp"],
				"env": {"DEBUG": "true"}
			}
		}
	}`

	got, err := ParseAndAddMcpServer(tempDir, config)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []string{"browsermcp", "filesystem"}
	bothOk := true
	for _, k := range want {
		if !slices.Contains(got, k) {
			bothOk = false
		}
	}
	if !bothOk {
		t.Fatalf("expected: %v, to have: %v", got, want)
	}

	// Check that files were created
	browsermcpPath := filepath.Join(tempDir, "browsermcp.json")
	filesystemPath := filepath.Join(tempDir, "filesystem.json")

	if _, statErr := os.Stat(browsermcpPath); os.IsNotExist(statErr) {
		t.Error("browsermcp.json was not created")
	}
	if _, statErr := os.Stat(filesystemPath); os.IsNotExist(statErr) {
		t.Error("filesystem.json was not created")
	}

	// Verify content of one file
	data, err := os.ReadFile(browsermcpPath)
	if err != nil {
		t.Fatalf("failed to read browsermcp.json: %v", err)
	}

	var server tools.McpServer
	if err := json.Unmarshal(data, &server); err != nil {
		t.Fatalf("failed to unmarshal server config: %v", err)
	}

	if server.Command != "npx" {
		t.Errorf("expected command 'npx', got '%s'", server.Command)
	}
}
