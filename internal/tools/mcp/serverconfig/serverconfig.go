// Package serverconfig parses and validates MCP server config files into
// pub_models.McpServer values. It is a leaf package deliberately kept out
// of internal/tools/mcp itself: that package's own tests import
// internal/tools, and internal/tools (the tools listing, phase 7) must call
// this same parsing primitive setup already uses, which would be an import
// cycle through mcp's test binary if this lived there instead (worklog
// 2026-10-02-mcp-connection-cost, phase 7, discovered necessary; relocated
// from internal/text/querier_setup_tools.go, where it was unexported).
package serverconfig

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/baalimago/clai/internal/utils"
	pub_models "github.com/baalimago/clai/pkg/text/models"
)

// FindConfiguredServers reads and validates every MCP server config named by
// filePaths, deriving each server's name from its base filename. It is the
// single parsing primitive setup's directory scan
// (internal/text/querier_setup_tools.go) and the tools listing's cache-only
// identity lookup (internal/tools/cmd.go) both call, so the two can never
// resolve a server's identity differently.
//
// A per-file parse or validation failure is collected rather than stopping
// the scan; the servers that did parse are returned alongside the joined
// error, exactly as before relocation.
func FindConfiguredServers(filePaths []string) ([]pub_models.McpServer, error) {
	ret := make([]pub_models.McpServer, 0)
	errs := make([]error, 0)
	for _, file := range filePaths {
		data, err := os.ReadFile(file)
		if err != nil {
			continue
		}
		var mcpServer pub_models.McpServer
		if unmarshalErr := json.Unmarshal(data, &mcpServer); unmarshalErr != nil {
			errs = append(errs, fmt.Errorf("failed to unmarshal: '%s', error: %w", file, unmarshalErr))
			continue
		}
		if validateErr := validateTransport(file, mcpServer); validateErr != nil {
			errs = append(errs, validateErr)
			continue
		}
		if mcpServer.EnvFile != "" {
			expanded, expandErr := utils.ExpandUserPath(mcpServer.EnvFile)
			if expandErr != nil {
				errs = append(errs, fmt.Errorf("failed to expand envfile %q in %q: %w", mcpServer.EnvFile, file, expandErr))
				continue
			}
			if !filepath.IsAbs(expanded) {
				expanded = filepath.Join(filepath.Dir(file), expanded)
			}
			mcpServer.EnvFile = expanded
		}
		serverName := strings.TrimSuffix(filepath.Base(file), filepath.Ext(file))
		mcpServer.Name = serverName
		ret = append(ret, mcpServer)
	}
	return ret, errors.Join(errs...)
}

// ValidateTransport is validateTransport exported for a server that never
// came from a config file: one passed through agent.WithMcpServers (D43,
// R2-07). Without it, a struct with both command and url set silently
// preferred url, and one with neither set reached exec.CommandContext(ctx,
// ""). identifier names the server in the resulting error; a config-file
// caller keeps using validateTransport, which passes the file path.
func ValidateTransport(identifier string, server pub_models.McpServer) error {
	return validateTransport(identifier, server)
}

// validateTransport enforces the config's own XOR: exactly one of command
// and url is set, and a configured url is an absolute http or https URL
// (phase 4, README shared interfaces: "transport selection is a parse-time
// property of the server config, not a runtime guess").
func validateTransport(file string, server pub_models.McpServer) error {
	hasCommand := server.Command != ""
	hasURL := server.Url != ""
	switch {
	case hasCommand && hasURL:
		// Distinguishable from the neither-set branch below (R2-18): a
		// regression collapsing the two branches, or reporting the wrong
		// one, must not pass both tests by sharing one message.
		return fmt.Errorf("mcp server config %q sets both \"command\" and \"url\"; exactly one is required", file)
	case !hasCommand && !hasURL:
		return fmt.Errorf("mcp server config %q sets neither \"command\" nor \"url\"; exactly one is required", file)
	}
	if !hasURL {
		return nil
	}
	parsed, err := url.Parse(server.Url)
	if err != nil || !parsed.IsAbs() || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return fmt.Errorf("mcp server config %q: \"url\" %q is not an absolute http or https URL", file, server.Url)
	}
	return nil
}
