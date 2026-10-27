package text

import (
	"testing"

	pub_models "github.com/baalimago/clai/pkg/text/models"
)

// AuthConfigured reports a credential source, so an auth block holding only
// required_env (the shape a command-based server uses) is not one.
func TestRedactMcpServersForDebug_AuthConfiguredFollowsTheTokenFields(t *testing.T) {
	servers := []pub_models.McpServer{
		{Name: "none", Command: "node"},
		{Name: "stdio-required-env", Command: "npx", Auth: &pub_models.McpServerAuth{RequiredEnv: []string{"SENTRY_ACCESS_TOKEN"}}},
		{Name: "scopes-only", Url: "https://mcp.example.com", Auth: &pub_models.McpServerAuth{Scopes: []string{"read"}}},
		{Name: "token-env", Url: "https://mcp.example.com", Auth: &pub_models.McpServerAuth{TokenEnv: "GITHUB_TOKEN"}},
		{Name: "token-command", Url: "https://mcp.example.com", Auth: &pub_models.McpServerAuth{TokenCommand: []string{"vault", "read"}}},
	}
	want := []bool{false, false, false, true, true}
	for i, got := range redactMcpServersForDebug(servers) {
		if got.AuthConfigured != want[i] {
			t.Errorf("%s: AuthConfigured = %v, want %v", servers[i].Name, got.AuthConfigured, want[i])
		}
	}
}
