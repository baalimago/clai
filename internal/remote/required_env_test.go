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
		{"invalid name", `{"command":"x","auth":{"required_env":["A","ghp-s3cr3t"]}}`, `mcp_servers.s.auth.required_env[1] is not a valid environment variable name`},
		{"duplicate name", `{"url":"https://x.example","auth":{"required_env":["A","B","A"]}}`, `mcp_servers.s.auth.required_env[2] repeats required_env[0]`},
		{"name also in env", `{"command":"x","env":{"A":"v"},"auth":{"required_env":["A"]}}`, `mcp_servers.s.auth.required_env[0] is also a key of mcp_servers.s.env, so the environment never supplies it`},
		{"token_env not a string", `{"url":"https://x.example","auth":{"token_env":["A"]}}`, `mcp_servers.s.auth.token_env: needs a variable name`},
		{"token_env not a name", `{"url":"https://x.example","auth":{"token_env":"A\n\u001b[31mB"}}`, `mcp_servers.s.auth.token_env is not a valid environment variable name`},
		{"token_command empty", `{"url":"https://x.example","auth":{"token_command":[]}}`, `mcp_servers.s.auth.token_command: needs a non-empty array of strings`},
		{"token_command not strings", `{"url":"https://x.example","auth":{"token_command":"vault read"}}`, `mcp_servers.s.auth.token_command: needs a non-empty array of strings`},
		{"scopes not strings", `{"url":"https://x.example","auth":{"scopes":"read"}}`, `mcp_servers.s.auth.scopes: needs an array of strings`},
		{"near-miss auth key", `{"command":"x","auth":{"requried_env":["A"]}}`, `mcp_servers.s.auth: "requried_env": unknown key (did you mean "required_env"?)`},
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
			for _, leak := range []string{"s3cr3t", "\x1b"} {
				if strings.Contains(err.Error(), leak) {
					t.Fatalf("err %q echoes an invalid entry", err)
				}
			}
		})
	}
}

func TestValidate_listsAnUnknownAuthKeyAsIgnored(t *testing.T) {
	got, err := Validate("t", []byte(`{"mcp_servers":{"s":{"url":"https://x.example","auth":{"headers":{"X":"y"}}}}}`))
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if strings.Join(got.Ignored, "|") != "mcp_servers.s.auth.headers" {
		t.Fatalf("Ignored = %v", got.Ignored)
	}
}

// Whatever Validate accepts, Summarize can read: every field it decodes is
// type-checked first.
func TestValidate_rejectsEveryShapeSummarizeCannotRead(t *testing.T) {
	for _, body := range []string{
		`{"model":5}`,
		`{"tools":"mcp_x*"}`,
		`{"tools":[1]}`,
		`{"mcp_servers":{"s":{"command":5}}}`,
		`{"mcp_servers":{"s":{"url":true}}}`,
		`{"mcp_servers":{"s":{"command":"x","args":"-y pkg"}}}`,
		`{"mcp_servers":{"s":{"command":"x","args":[1]}}}`,
		`{"mcp_servers":{"s":{"command":"x","envfile":["/a"]}}}`,
		`{"mcp_servers":{"s":{"url":"https://x.example","auth":{"token_env":5}}}}`,
		`{"mcp_servers":{"s":{"url":"https://x.example","auth":{"token_command":[1]}}}}`,
		`{"mcp_servers":{"s":{"url":"https://x.example","auth":{"required_env":{"A":1}}}}}`,
		`{"use_tools":"yes"}`,
		`{"prompt":["a"]}`,
		`{"mcp_servers":{"s":{"command":"x","env":["A=1"]}}}`,
		`{"mcp_servers":{"s":{"command":"x","env":{"A":1}}}}`,
	} {
		checked, err := Validate("t", []byte(body))
		if err == nil {
			if _, sumErr := Summarize([]byte(body), checked); sumErr != nil {
				t.Errorf("Validate accepted %s but Summarize failed: %v", body, sumErr)
			}
			continue
		}
		var invalid *InvalidProfileError
		if !errors.As(err, &invalid) {
			t.Errorf("Validate(%s) = %v, want *InvalidProfileError", body, err)
		}
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
