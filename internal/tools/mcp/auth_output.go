package mcp

import (
	"fmt"
	"io"
	"strings"

	"github.com/baalimago/clai/internal/diag"
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

// plainAuthMessage is an authorization stage as a JSON record carries it:
// no marker, colour or indent (F12).
func plainAuthMessage(message string) string { return "mcp.auth: " + message }

// WriteAuthMessage renders an authorization stage without shortening URLs.
func WriteAuthMessage(out io.Writer, message string) error {
	return writeAuthMessage(out, message, false)
}

func writeAuthMessage(out io.Writer, message string, plain bool) error {
	text := formatAuthMessage(message)
	if plain {
		text = plainAuthMessage(message)
	}
	if _, err := fmt.Fprintln(out, text); err != nil {
		return diag.Errorf("write MCP authorization output: %w", err)
	}
	return nil
}

// AuthorizerConfig carries the run's authorization storage and display policy.
type AuthorizerConfig struct {
	Store       *mcpauth.TokenStore
	Input       io.Reader
	Output      io.Writer
	Interactive bool
	// Plain writes the stages without the terminal look, for output that
	// JSON records carry (F12).
	Plain bool
	// PasteQuestion asks for a pasted authorization code before it is read;
	// nil reports the wait as a stage.
	PasteQuestion func(string) error
}

// NewAuthorizer wires the same credential loading and themed progress output
// for explicit authorization, startup handshakes and lazy tool calls.
func NewAuthorizer(config AuthorizerConfig) *mcpauth.Authorizer {
	output := config.Output
	if output == nil {
		output = io.Discard
	}
	options := []mcpauth.Option{
		mcpauth.WithPasteInput(config.Input),
		mcpauth.WithInteractive(config.Interactive),
		mcpauth.WithEnvFileLoader(LoadEnvFile),
		mcpauth.WithPreregisteredClients(mcpauth.BuiltinClients()),
		mcpauth.WithProgress(func(message string) error { return writeAuthMessage(output, message, config.Plain) }),
	}
	if config.PasteQuestion != nil {
		options = append(options, mcpauth.WithPasteQuestion(config.PasteQuestion))
	}
	return mcpauth.NewAuthorizer(config.Store, options...)
}
