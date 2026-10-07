package mcp

import (
	"fmt"
	"io"
	"strings"

	"github.com/baalimago/clai/internal/tools/mcp/mcpauth"
	"github.com/baalimago/clai/internal/utils"
	"github.com/baalimago/go_away_boilerplate/pkg/table"
)

func formatAuthMessage(message string) string {
	theme := utils.TableTheme()
	lines := strings.Split(message, "\n")
	lines[0] = table.Colorize(theme.Primary, "▸ mcp.auth") + "  " + table.Colorize(theme.Breadtext, lines[0])
	for i := 1; i < len(lines); i++ {
		lines[i] = "  " + table.Colorize(theme.Secondary, lines[i])
	}
	return strings.Join(lines, "\n")
}

// WriteAuthMessage renders an authorization stage without shortening URLs.
func WriteAuthMessage(out io.Writer, message string) error {
	if _, err := fmt.Fprintln(out, formatAuthMessage(message)); err != nil {
		return fmt.Errorf("write MCP authorization output: %w", err)
	}
	return nil
}

// AuthorizerConfig carries the run's authorization storage and display policy.
type AuthorizerConfig struct {
	Store       *mcpauth.TokenStore
	Input       io.Reader
	Output      io.Writer
	Interactive bool
}

// NewAuthorizer wires the same credential loading and themed progress output
// for explicit authorization, startup handshakes and lazy tool calls.
func NewAuthorizer(config AuthorizerConfig) *mcpauth.Authorizer {
	output := config.Output
	if output == nil {
		output = io.Discard
	}
	return mcpauth.NewAuthorizer(config.Store,
		mcpauth.WithPasteInput(config.Input),
		mcpauth.WithInteractive(config.Interactive),
		mcpauth.WithEnvFileLoader(LoadEnvFile),
		mcpauth.WithProgress(func(message string) error { return WriteAuthMessage(output, message) }),
	)
}
