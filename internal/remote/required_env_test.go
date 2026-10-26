package remote

import (
	"errors"
	"strings"
	"testing"
)

func TestValidate_acceptsRequiredEnvOnBothServerKinds(t *testing.T) {
	body := `{"mcp_servers":{
		"sentry":{"command":"npx","args":["-y","@sentry/mcp-server@0.42.0"],"env":{"SENTRY_HOST":"sentry.io"},"auth":{"required_env":["SENTRY_ACCESS_TOKEN"]}},
		"github":{"url":"https://api.githubcopilot.com/mcp/","auth":{"token_env":"GITHUB_TOKEN","required_env":["_GH_ORG"]}}}}`
	if _, err := Validate("t", []byte(body)); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestValidate_checksAuthAndRequiredEnv(t *testing.T) {
	for _, tc := range []struct {
		name   string
		server string
		want   string
	}{
		{"auth not an object", `{"command":"x","auth":["A"]}`, `mcp_servers.s.auth: needs an object`},
		{"auth null", `{"command":"x","auth":null}`, `mcp_servers.s.auth: needs an object`},
		{"required_env not an array", `{"command":"x","auth":{"required_env":"A"}}`, `mcp_servers.s.auth.required_env: needs an array of variable names`},
		{"required_env holds a number", `{"command":"x","auth":{"required_env":["A",1]}}`, `mcp_servers.s.auth.required_env: needs an array of variable names`},
		{"invalid name", `{"command":"x","auth":{"required_env":["A-B"]}}`, `mcp_servers.s.auth.required_env: "A-B" is not a valid environment variable name`},
		{"duplicate name", `{"url":"https://x.example","auth":{"required_env":["A","B","A"]}}`, `mcp_servers.s.auth.required_env: "A" is listed twice`},
		{"name also in env", `{"command":"x","env":{"A":"v"},"auth":{"required_env":["A"]}}`, `mcp_servers.s.auth.required_env: "A" is also set in mcp_servers.s.env, so the environment never supplies it`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Validate("t", []byte(`{"mcp_servers":{"s":`+tc.server+`}}`))
			var invalid *InvalidProfileError
			if !errors.As(err, &invalid) {
				t.Fatalf("err = %v, want *InvalidProfileError", err)
			}
			if strings.Join(invalid.Problems, "|") != tc.want {
				t.Fatalf("Problems = %q, want [%q]", invalid.Problems, tc.want)
			}
		})
	}
}

func TestSummary_listsTheVariablesItNeeds(t *testing.T) {
	body := []byte(`{"model":"gpt-6.1-sol","mcp_servers":{
		"sentry":{"command":"npx","args":["-y","@sentry/mcp-server@0.42.0"],"auth":{"required_env":["SENTRY_ACCESS_TOKEN"]}},
		"github":{"url":"https://api.githubcopilot.com/mcp/","auth":{"token_env":"GITHUB_TOKEN","required_env":["GITHUB_TOKEN"]}},
		"vault":{"url":"https://mcp.example.com","auth":{"token_command":["vault","read"],"token_env":"NOT_READ","required_env":["VAULT_ADDR"]}},
		"memory":{"command":"npx","args":["-y","@modelcontextprotocol/server-memory@2.0.0"]}}}`)
	checked, err := Validate("t", body)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	s, err := Summarize(body, checked)
	if err != nil {
		t.Fatalf("Summarize: %v", err)
	}
	want := "It needs: GITHUB_TOKEN (github), SENTRY_ACCESS_TOKEN (sentry), VAULT_ADDR (vault)\n"
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
	if strings.Contains(none.String(), "It needs") {
		t.Fatalf("summary without variables has a needs line:\n%s", none)
	}
}
