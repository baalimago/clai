package mcpauth

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

	pkgtools "github.com/baalimago/clai/pkg/tools"

	pub_models "github.com/baalimago/clai/pkg/text/models"
)

// EnvFileLoader resolves path into a key/value map. Injected rather than
// imported directly: the production loader is mcp.LoadEnvFile, but this
// package cannot import internal/tools/mcp, which imports this package for
// its own mcp/cmd.go subcommand, and an import the other way would cycle.
type EnvFileLoader func(path string) (map[string]string, error)

// CredentialSource identifies which source supplied, or attempted to
// supply, an access token, per the credential-precedence parameter.
type CredentialSource string

const (
	SourceTokenCommand CredentialSource = "token_command"
	SourceTokenEnv     CredentialSource = "token_env"
	SourceTokenStore   CredentialSource = "token_store"
	SourceInteractive  CredentialSource = "interactive"
)

// stderrCaptureLimit bounds the credential command's captured standard
// error, included in the typed error on failure as the only diagnostic the
// operator gets.
const stderrCaptureLimit = 4096

// ResolveStaticCredential tries auth.token_command then auth.token_env, in
// that order, both ahead of the token store (credential-precedence
// parameter). ok is false with a nil err when both are unset, so the
// caller falls through to the token store. A configured source that fails
// is returned as a typed *CredentialSourceError with no fall-through (D15).
// Exported so a caller that wants only the static half of the chain (no
// token store, no interactive flow — the eager HTTP path's posture) does
// not have to construct a full Authorizer to reach it.
func ResolveStaticCredential(ctx context.Context, server pub_models.McpServer, loadEnvFile EnvFileLoader) (token string, source CredentialSource, ok bool, err error) {
	if server.Auth == nil {
		return "", "", false, nil
	}
	if len(server.Auth.TokenCommand) > 0 {
		tok, runErr := runCredentialCommand(ctx, server.Name, server.Auth.TokenCommand)
		if runErr != nil {
			return "", SourceTokenCommand, false, runErr
		}
		return tok, SourceTokenCommand, true, nil
	}
	if server.Auth.TokenEnv != "" {
		tok, envErr := resolveEnvToken(server, loadEnvFile)
		if envErr != nil {
			return "", SourceTokenEnv, false, envErr
		}
		return tok, SourceTokenEnv, true, nil
	}
	return "", "", false, nil
}

// runCredentialCommand executes argv under ctx (so cancellation reaps it),
// subject to the same command-ban policy a tool-call context carries
// (pkgtools.ValidateCmdNotBanned): a banned command is never spawned. Its
// trimmed standard output is the token; a non-zero exit or a command not
// found is reported with the command's bounded standard error as the only
// diagnostic, and empty output is a distinct typed error so a silent helper
// is never mistaken for an unset source.
func runCredentialCommand(ctx context.Context, serverName string, argv []string) (string, error) {
	if len(argv) == 0 {
		return "", &CredentialSourceError{ServerName: serverName, Source: SourceTokenCommand, Cause: fmt.Errorf("token_command is empty")}
	}
	if err := pkgtools.ValidateCmdNotBanned(ctx, argv[0], argv[1:]); err != nil {
		return "", &CredentialSourceError{ServerName: serverName, Source: SourceTokenCommand, Cause: err}
	}

	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Env = os.Environ()
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		errMsg := strings.TrimSpace(boundedTail(stderr.String(), stderrCaptureLimit))
		return "", &CredentialSourceError{
			ServerName: serverName,
			Source:     SourceTokenCommand,
			Cause:      fmt.Errorf("run %q: %w (stderr: %s)", strings.Join(argv, " "), err, errMsg),
		}
	}
	token := strings.TrimSpace(stdout.String())
	if token == "" {
		return "", &CredentialSourceError{
			ServerName: serverName,
			Source:     SourceTokenCommand,
			Cause:      fmt.Errorf("command %q produced no output", strings.Join(argv, " ")),
		}
	}
	return token, nil
}

// resolveEnvToken reads server.Auth.TokenEnv from the process environment
// first, then from server.EnvFile when configured, matching the "resolved
// from the process environment or the configured envfile" source shape. An
// unset variable in both places is a typed error, since the source itself
// is configured (token_env is set) even though the variable it names is
// not.
func resolveEnvToken(server pub_models.McpServer, loadEnvFile EnvFileLoader) (string, error) {
	name := server.Auth.TokenEnv
	if v := os.Getenv(name); v != "" {
		return v, nil
	}
	if server.EnvFile != "" && loadEnvFile != nil {
		env, err := loadEnvFile(server.EnvFile)
		if err != nil {
			return "", &CredentialSourceError{ServerName: server.Name, Source: SourceTokenEnv, Cause: err}
		}
		if v, ok := env[name]; ok && v != "" {
			return v, nil
		}
	}
	return "", &CredentialSourceError{
		ServerName: server.Name,
		Source:     SourceTokenEnv,
		Cause:      fmt.Errorf("environment variable %q is not set in the process environment or envfile", name),
	}
}

// boundedTail returns at most limit bytes from the end of s, the most
// relevant part of a command's error output when it overflows the bound.
func boundedTail(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	return s[len(s)-limit:]
}
