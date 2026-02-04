package mcp

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func loadEnvFile(envFile string) (map[string]string, error) {
	envFile = strings.TrimSpace(envFile)
	if envFile == "" {
		return nil, nil
	}
	resolved, err := expandUserPath(envFile)
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

func expandUserPath(p string) (string, error) {
	if p == "" || p[0] != '~' {
		return p, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home dir: %w", err)
	}
	if p == "~" {
		return home, nil
	}
	if strings.HasPrefix(p, "~/") {
		return filepath.Join(home, p[2:]), nil
	}
	// Don't attempt to expand ~user paths.
	return p, nil
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
		if strings.HasPrefix(line, "export ") {
			line = strings.TrimSpace(strings.TrimPrefix(line, "export "))
		}
		idx := strings.IndexByte(line, '=')
		if idx < 0 {
			return nil, fmt.Errorf("line %d missing '='", lineNo)
		}
		key := strings.TrimSpace(line[:idx])
		if key == "" {
			return nil, fmt.Errorf("line %d has empty key", lineNo)
		}
		val := strings.TrimSpace(line[idx+1:])
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
