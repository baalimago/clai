package mcp

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/baalimago/clai/internal/utils"
)

// LoadEnvFile parses envFile into a key/value map. Exported so the
// authorization phase's static-bearer source (internal/tools/mcp/mcpauth)
// can resolve auth.token_env from the same envfile a server's process
// spawn already reads, with no second parser (R1-32: this used to be a
// private loadEnvFile with a one-line exported wrapper giving one function
// two names).
func LoadEnvFile(envFile string) (map[string]string, error) {
	envFile = strings.TrimSpace(envFile)
	if envFile == "" {
		return nil, nil
	}
	resolved, err := utils.ExpandUserPath(envFile)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(resolved)
	if err != nil {
		return nil, fmt.Errorf("read envfile %q: %w", resolved, err)
	}
	parsed, err := parseEnvFileContent(string(data))
	if err != nil {
		return nil, fmt.Errorf("parse envfile %q: %w", resolved, err)
	}
	return parsed, nil
}

func parseEnvFileContent(content string) (map[string]string, error) {
	env := make(map[string]string)
	scanner := bufio.NewScanner(strings.NewReader(content))
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if after, ok := strings.CutPrefix(line, "export "); ok {
			line = strings.TrimSpace(after)
		}
		before, after, ok := strings.Cut(line, "=")
		if !ok {
			return nil, fmt.Errorf("line %d missing '='", lineNo)
		}
		key := strings.TrimSpace(before)
		if key == "" {
			return nil, fmt.Errorf("line %d has empty key", lineNo)
		}
		val := strings.TrimSpace(after)
		if len(val) >= 2 {
			if (val[0] == '"' && val[len(val)-1] == '"') || (val[0] == '\'' && val[len(val)-1] == '\'') {
				val = val[1 : len(val)-1]
			}
		}
		env[key] = val
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan envfile: %w", err)
	}
	return env, nil
}
