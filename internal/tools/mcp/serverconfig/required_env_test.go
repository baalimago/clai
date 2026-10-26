package serverconfig

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/baalimago/clai/pkg/claierr"
	pub_models "github.com/baalimago/clai/pkg/text/models"
)

func TestValidEnvName(t *testing.T) {
	for _, name := range []string{"A", "_", "SENTRY_ACCESS_TOKEN", "_x1", "lower_case"} {
		if !ValidEnvName(name) {
			t.Errorf("ValidEnvName(%q) = false, want true", name)
		}
	}
	for _, name := range []string{"", "1A", "A-B", "A B", "A=B", "$A", "Ä"} {
		if ValidEnvName(name) {
			t.Errorf("ValidEnvName(%q) = true, want false", name)
		}
	}
}

func TestValidate_RejectsInvalidRequiredEnvNamesOnBothKinds(t *testing.T) {
	for _, server := range []pub_models.McpServer{
		{Name: "local", Command: "node"},
		{Name: "remote", Url: "https://mcp.example.com/mcp"},
	} {
		server.Auth = &pub_models.McpServerAuth{RequiredEnv: []string{"GOOD", "1BAD", "ALSO-BAD"}}
		err := Validate(server.Name, server)
		if err == nil {
			t.Fatalf("%s: Validate = nil, want an error", server.Name)
		}
		for _, want := range []string{server.Name, "auth.required_env", `"1BAD"`, `"ALSO-BAD"`} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("%s: err %q lacks %q", server.Name, err, want)
			}
		}
		if strings.Contains(err.Error(), `"GOOD"`) {
			t.Errorf("%s: err %q names a valid variable", server.Name, err)
		}

		server.Auth.RequiredEnv = []string{"GOOD", "_ALSO_GOOD"}
		if err := Validate(server.Name, server); err != nil {
			t.Fatalf("%s: valid names: Validate = %v", server.Name, err)
		}
	}
}

func TestFindConfiguredServers_RejectsInvalidRequiredEnvName(t *testing.T) {
	dir := t.TempDir()
	bad := filepath.Join(dir, "bad.json")
	good := filepath.Join(dir, "good.json")
	if err := os.WriteFile(bad, []byte(`{"command":"node","auth":{"required_env":["NOT-A-NAME"]}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(good, []byte(`{"command":"node","auth":{"required_env":["API_KEY"]}}`), 0o644); err != nil {
		t.Fatal(err)
	}

	servers, err := FindConfiguredServers([]string{bad, good})
	if err == nil || !strings.Contains(err.Error(), bad) || !strings.Contains(err.Error(), `"NOT-A-NAME"`) {
		t.Fatalf("err = %v, want one naming %q and the invalid name", err, bad)
	}
	if len(servers) != 1 || servers[0].Name != "good" {
		t.Fatalf("servers = %+v, want only good", servers)
	}
	if !slices.Equal(servers[0].Auth.RequiredEnv, []string{"API_KEY"}) {
		t.Fatalf("RequiredEnv = %v, want [API_KEY]", servers[0].Auth.RequiredEnv)
	}
}

func lookupIn(env map[string]string) func(string) string {
	return func(k string) string { return env[k] }
}

func TestCheckRequiredEnv_PassesWhenNothingIsRequiredOrAllIsSet(t *testing.T) {
	if err := CheckRequiredEnv(pub_models.McpServer{Name: "x", Command: "node"}, lookupIn(nil)); err != nil {
		t.Fatalf("no auth: %v", err)
	}
	server := pub_models.McpServer{Name: "x", Command: "node", Auth: &pub_models.McpServerAuth{TokenEnv: "T"}}
	if err := CheckRequiredEnv(server, lookupIn(nil)); err != nil {
		t.Fatalf("token_env alone is not required_env: %v", err)
	}
	server.Auth.RequiredEnv = []string{"A", "B"}
	if err := CheckRequiredEnv(server, lookupIn(map[string]string{"A": "1", "B": "2"})); err != nil {
		t.Fatalf("all set: %v", err)
	}
}

func TestCheckRequiredEnv_NamesEveryMissingOrEmptyVariableSortedWithoutValues(t *testing.T) {
	server := pub_models.McpServer{Name: "sentry", Command: "npx", Auth: &pub_models.McpServerAuth{
		RequiredEnv: []string{"SENTRY_ACCESS_TOKEN", "SET_ONE", "GITHUB_TOKEN", "EMPTY_ONE"},
	}}
	err := CheckRequiredEnv(server, lookupIn(map[string]string{"SET_ONE": "s3cr3t-value", "EMPTY_ONE": ""}))

	var missing *MissingEnvError
	if !errors.As(err, &missing) {
		t.Fatalf("err = %v, want *MissingEnvError", err)
	}
	if missing.ServerName != "sentry" || !slices.Equal(missing.Missing, []string{"EMPTY_ONE", "GITHUB_TOKEN", "SENTRY_ACCESS_TOKEN"}) {
		t.Fatalf("MissingEnvError = %+v", missing)
	}
	want := `mcp server "sentry" needs EMPTY_ONE, GITHUB_TOKEN, SENTRY_ACCESS_TOKEN set in the environment`
	if missing.Error() != want {
		t.Fatalf("Error() = %q, want %q", missing.Error(), want)
	}
	if strings.Contains(err.Error(), "s3cr3t-value") {
		t.Fatalf("err %q leaks a value", err)
	}

	// A failed requirement is a startup failure at the env stage, so the
	// explicit-server funnel keeps it as-is and an SDK consumer matching
	// *claierr.McpServerStartupError still sees which server failed.
	var startup *claierr.McpServerStartupError
	if !errors.As(err, &startup) || startup.Stage != "env" || startup.ServerName != "sentry" {
		t.Fatalf("err = %v, want *claierr.McpServerStartupError at stage env", err)
	}
	if !errors.Is(err, claierr.ErrMcpServerStartup) {
		t.Fatal("err does not match claierr.ErrMcpServerStartup")
	}
	if _, ok := any(missing).(interface{ BlockedOutsideRun() bool }); ok {
		t.Fatal("MissingEnvError must be a run-scoped failure, not blocked outside the run")
	}
}
