package remote

import (
	"strings"
	"testing"
)

func TestSummary_listsTheVariablesItNeeds(t *testing.T) {
	body := []byte(`{"model":"gpt-6.1-sol","mcp_servers":{
		"sentry":{"command":"npx","args":["-y","@sentry/mcp-server@0.42.0"],"auth":{"required_env":["SENTRY_ACCESS_TOKEN"]}},
		"github":{"url":"https://api.githubcopilot.com/mcp/","auth":{"token_env":"GITHUB_TOKEN","required_env":["GITHUB_TOKEN"]}},
		"vault":{"url":"https://mcp.example.com","auth":{"token_command":["vault","read"],"token_env":"NOT_READ","required_env":["VAULT_ADDR"]}},
		"memory":{"command":"npx","args":["-y","@modelcontextprotocol/server-memory@2.0.0"]}}}`)
	s, err := Summarize(body, Checked{})
	if err != nil {
		t.Fatalf("Summarize: %v", err)
	}
	want := "▸ needs · GITHUB_TOKEN for github, SENTRY_ACCESS_TOKEN for sentry, VAULT_ADDR for vault\n"
	if !strings.Contains(s.String(), want) {
		t.Fatalf("summary:\n%s\nlacks %q", s, want)
	}
	if strings.Contains(s.String(), "NOT_READ") {
		t.Fatalf("summary names token_env although token_command takes precedence:\n%s", s)
	}

	none, err := Summarize([]byte(`{"mcp_servers":{"memory":{"command":"npx"}}}`), Checked{})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(none.String(), "▸ needs") {
		t.Fatalf("summary without variables has a needs line:\n%s", none)
	}
}
