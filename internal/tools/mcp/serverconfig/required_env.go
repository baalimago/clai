package serverconfig

import (
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/baalimago/clai/pkg/claierr"
	pub_models "github.com/baalimago/clai/pkg/text/models"
)

// RequiredEnvStage is the startup stage a failed auth.required_env check
// reports, so the explicit-server funnel keeps it rather than renaming it.
const RequiredEnvStage = "env"

var envName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// ValidEnvName reports whether name is an environment variable name a
// shell can export: a letter or underscore, then letters, digits and
// underscores.
func ValidEnvName(name string) bool {
	return envName.MatchString(name)
}

// MissingEnvError means a server's auth.required_env names variables that
// are unset or empty. It carries names only, never a value. It is a
// run-scoped failure: a lazily connected server memoises it rather than
// dialling again, since nothing in the run can set the variable.
type MissingEnvError struct {
	ServerName string
	// Missing is sorted.
	Missing []string
}

func (e *MissingEnvError) Error() string {
	return fmt.Sprintf("mcp server %q needs %s set in the environment", e.ServerName, strings.Join(e.Missing, ", "))
}

// CheckRequiredEnv returns a *claierr.McpServerStartupError at the "env"
// stage, wrapping a *MissingEnvError, when lookup yields an empty value for
// any name in server.Auth.RequiredEnv. lookup is the environment the
// server will actually see; the caller decides which that is.
func CheckRequiredEnv(server pub_models.McpServer, lookup func(string) string) error {
	if server.Auth == nil {
		return nil
	}
	var missing []string
	for _, name := range server.Auth.RequiredEnv {
		if lookup(name) == "" && !slices.Contains(missing, name) {
			missing = append(missing, name)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	slices.Sort(missing)
	return claierr.NewMcpServerStartup(server.Name, RequiredEnvStage, &MissingEnvError{ServerName: server.Name, Missing: missing})
}

// ValidateRequiredEnv rejects an auth.required_env entry that is not a
// valid variable name, naming every such entry by index at once. Validate includes
// it; it is exported for a loader whose transport rules differ, such as a
// profile's mcp_servers.
func ValidateRequiredEnv(identifier string, server pub_models.McpServer) error {
	if server.Auth == nil {
		return nil
	}
	var invalid []string
	for i, name := range server.Auth.RequiredEnv {
		if !ValidEnvName(name) {
			invalid = append(invalid, fmt.Sprintf("auth.required_env[%d]", i))
		}
	}
	if len(invalid) == 0 {
		return nil
	}
	// By index: an entry that is not a name may be a pasted secret.
	return fmt.Errorf("mcp server config %q: %s: not a valid environment variable name (want letters, digits and underscores, not starting with a digit)",
		identifier, strings.Join(invalid, ", "))
}
