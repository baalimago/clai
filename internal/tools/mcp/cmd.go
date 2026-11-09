package mcp

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"

	"github.com/baalimago/clai/internal"
	"github.com/baalimago/clai/internal/diag"
	"github.com/baalimago/clai/internal/display"
	"github.com/baalimago/clai/internal/runenv"
	"github.com/baalimago/clai/internal/stdio"
	"github.com/baalimago/clai/internal/tools/mcp/mcpauth"
	"github.com/baalimago/clai/internal/tools/mcp/serverconfig"
	"github.com/baalimago/clai/pkg/claierr"
	pub_models "github.com/baalimago/clai/pkg/text/models"
	"github.com/baalimago/go_away_boilerplate/pkg/cmd"
)

// mcpServersSubdir is the subdirectory of the clai config dir one server's
// JSON file lives under, matching the name querier_setup_tools.go's
// setupTooling already builds by convention.
const mcpServersSubdir = "mcpServers"

// Command builds the mcp command tree. Its only subcommand today is auth
// (phase 5); it lives in this domain package and is injected from internal/cli,
// matching the placement convention internal/tools/cmd.go sets.
func Command() *internal.Command {
	c := &internal.Command{
		Name: "mcp",
		Desc: "Manage MCP server state",
		HelpText: `mcp <subcommand>.

Subcommands:
  auth <server>   Authorize a configured endpoint-based server interactively
                  and write its token store entry.`,
	}
	auth := &internal.Command{
		Name: "auth",
		Desc: "Authorize a configured MCP server interactively and store its token",
		HelpText: `mcp auth <server>. Runs the OAuth 2.1 interactive flow (discovery,
dynamic client registration, PKCE, authorization and exchange) against the
named server's configured url, and writes the resulting token to the token
store. The store's path is printed on success so its mode can be inspected.
It performs no model call and spends nothing.

Examples:
  clai mcp auth linear`,
	}
	auth.OnRun = func(ctx context.Context, c *internal.Command) error {
		args := c.Args()
		if len(args) < 2 {
			return &McpAuthUsageError{}
		}
		return RunAuth(ctx, args[1], runenv.From(ctx).Stdin())
	}
	// c has Subs but no default action of its own, so a bare invocation
	// must not fall through to internal.Command.Run's querier.Query path,
	// which has no querier here and panics (R1-04). Printing help mirrors
	// a usage error without inventing a model-callable default action.
	c.OnRun = func(ctx context.Context, c *internal.Command) error {
		if _, err := fmt.Fprintln(runenv.From(ctx).Stdout(), c.Help()); err != nil {
			return diag.Errorf("print mcp help: %w", err)
		}
		return nil
	}
	c.Subs = map[string]cmd.Command{"auth": auth}
	return c
}

// RunAuth authorizes the named server (read from
// <clai-config-dir>/mcpServers/<name>.json) interactively and writes its
// token store entry. trustInput feeds the printed-URL fallback's pasted
// authorization code when the loopback redirect cannot complete.
func RunAuth(ctx context.Context, serverName string, trustInput io.Reader) error {
	confDir, err := internal.PrepTheme(ctx)
	if err != nil {
		return &McpAuthConfigDirError{Cause: err}
	}
	store := mcpauth.NewTokenStore(path.Join(confDir, mcpauth.TokenStoreDirName))
	return runAuthWith(ctx, serverName, confDir, NewAuthorizer(runAuthConfig(ctx, store, trustInput)))
}

// runAuthConfig is RunAuth's authorizer configuration for the run on ctx:
// under -json the stages are plain and the paste question is a select
// prompt record on stdout (F12, F13).
func runAuthConfig(ctx context.Context, store *mcpauth.TokenStore, trustInput io.Reader) AuthorizerConfig {
	env := runenv.From(ctx)
	config := AuthorizerConfig{Store: store, Input: trustInput, Output: env.Stdout(), Interactive: true, Plain: env.JSON()}
	if env.JSON() {
		config.PasteQuestion = func(question string) error {
			return env.ShowPrompt(stdio.Stdout, display.Prompt{Kind: "select", Text: question})
		}
	}
	return config
}

// runAuthWith is RunAuth's testable core: it takes an already-constructed
// Authorizer, so a test can inject a fake browser opener in place of
// DefaultBrowserOpener, which shells out to a real browser command a test
// host has none of.
func runAuthWith(ctx context.Context, serverName, confDir string, authz *mcpauth.Authorizer) error {
	server, err := loadNamedServer(confDir, serverName)
	if err != nil {
		return err
	}
	if server.Url == "" {
		return &McpAuthNotEndpointBasedError{Name: serverName}
	}
	if err := mcpauth.CheckEndpointRequiredEnv(server, LoadEnvFile); err != nil {
		if missing, ok := errors.AsType[*serverconfig.MissingEnvError](err); ok {
			return &McpAuthMissingEnvError{Name: serverName, Missing: missing.Missing, Cause: err}
		}
		return &McpAuthConnectError{Name: serverName, Cause: err}
	}

	env := runenv.From(ctx)
	if err := writeAuthMessage(env.Stdout(), fmt.Sprintf("Connecting to MCP server %q at %s...", serverName, server.Url), env.JSON()); err != nil {
		return err
	}
	connector := NewHttpConnector(ctx, server, nil)
	probe, connErr := connector.Conn(ctx)
	if connErr == nil {
		// A probe connection that needed no authorization: close it, since
		// nothing further is done with it (R1-29).
		probe.Close()
		return &McpAuthNoChallengeError{Name: serverName}
	}
	var challenge *claierr.AuthChallengeError
	if !errors.As(connErr, &challenge) {
		return &McpAuthConnectError{Name: serverName, Cause: connErr}
	}
	if _, authErr := authz.AuthorizeInteractive(ctx, server, challenge); authErr != nil {
		return &McpAuthFlowError{Name: serverName, Cause: authErr}
	}

	store := mcpauth.NewTokenStore(path.Join(confDir, mcpauth.TokenStoreDirName))
	return writeAuthMessage(env.Stdout(), fmt.Sprintf("Authorized %q. Token stored at %s (mode %#o).", serverName, store.Path(serverName), mcpauth.TokenStoreFileMode), env.JSON())
}

// loadNamedServer reads and validates the named server's config file
// through serverconfig.FindConfiguredServers, the single parsing
// primitive setup's directory scan and the tools listing both call (R1-19):
// a hand-rolled json.Unmarshal here previously accepted a config setting
// both "command" and "url", which setup rejects, and left a relative
// envfile unexpanded against the process CWD instead of the config file's
// directory.
func loadNamedServer(confDir, name string) (pub_models.McpServer, error) {
	p := filepath.Join(confDir, mcpServersSubdir, name+".json")
	if _, err := os.Stat(p); err != nil {
		return pub_models.McpServer{}, &McpAuthServerConfigError{Name: name, Path: p, Stage: "read", Cause: err}
	}
	servers, err := serverconfig.FindConfiguredServers([]string{p})
	if err != nil {
		return pub_models.McpServer{}, &McpAuthServerConfigError{Name: name, Path: p, Stage: "parse", Cause: err}
	}
	if len(servers) == 0 {
		return pub_models.McpServer{}, &McpAuthServerConfigError{Name: name, Path: p, Stage: "parse", Cause: fmt.Errorf("config produced no server")}
	}
	return servers[0], nil
}
