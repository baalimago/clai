package remote

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Server is one MCP server a profile starts or reaches.
type Server struct {
	Name string
	// Runs is the command line, or the url of a remote server.
	Runs string
	// Unpinned marks a package runner (npx, pnpx, bunx, uvx) fetching a
	// package with no @version: it can change under the profile.
	Unpinned bool
}

// Summary is what a profile asks of the machine that runs it: what the
// trust prompt and `clai profiles trust` show before anything runs.
type Summary struct {
	Model    string
	Tools    []string
	Servers  []Server
	EnvFiles []string
	Ignored  []string
}

var fetchers = map[string]bool{"npx": true, "pnpx": true, "bunx": true, "uvx": true}

// Summarize reads a validated profile body.
func Summarize(body []byte, checked Checked) (Summary, error) {
	var p struct {
		Model      string   `json:"model"`
		Tools      []string `json:"tools"`
		McpServers map[string]struct {
			Command string   `json:"command"`
			Args    []string `json:"args"`
			URL     string   `json:"url"`
			EnvFile string   `json:"envfile"`
		} `json:"mcp_servers"`
	}
	if err := json.Unmarshal(body, &p); err != nil {
		return Summary{}, fmt.Errorf("read profile summary: %w", err)
	}
	s := Summary{Model: p.Model, Tools: p.Tools, Ignored: checked.Ignored}
	for _, name := range sortedKeys(p.McpServers) {
		srv := p.McpServers[name]
		runs := srv.URL
		if srv.Command != "" {
			runs = strings.Join(append([]string{srv.Command}, srv.Args...), " ")
		}
		s.Servers = append(s.Servers, Server{Name: name, Runs: runs, Unpinned: unpinned(srv.Command, srv.Args)})
		if srv.EnvFile != "" {
			s.EnvFiles = append(s.EnvFiles, srv.EnvFile)
		}
	}
	return s, nil
}

func unpinned(command string, args []string) bool {
	if !fetchers[command] {
		return false
	}
	for _, a := range args {
		if strings.HasPrefix(a, "-") {
			continue
		}
		// A scoped package starts with @, so its version is a second @.
		return !strings.Contains(strings.TrimPrefix(a, "@"), "@")
	}
	return false
}

// String renders the summary as the trust prompt shows it.
func (s Summary) String() string {
	var b strings.Builder
	b.WriteString("This agent runs on your machine:\n")
	if len(s.Servers) == 0 {
		b.WriteString("  no MCP servers\n")
	}
	width := 0
	for _, srv := range s.Servers {
		width = max(width, len(srv.Name))
	}
	for _, srv := range s.Servers {
		fmt.Fprintf(&b, "  %-*s  %s", width, srv.Name, srv.Runs)
		if srv.Unpinned {
			b.WriteString("  (not pinned)")
		}
		b.WriteString("\n")
	}
	if len(s.Tools) > 0 {
		fmt.Fprintf(&b, "  tools: %s\n", strings.Join(s.Tools, ", "))
	}
	if len(s.EnvFiles) > 0 {
		fmt.Fprintf(&b, "It reads: %s\n", strings.Join(s.EnvFiles, ", "))
	}
	model := s.Model
	if model == "" {
		model = "your default"
	}
	fmt.Fprintf(&b, "Model: %s\n", model)
	if len(s.Ignored) > 0 {
		fmt.Fprintf(&b, "Not read by clai: %s\n", strings.Join(s.Ignored, ", "))
	}
	return b.String()
}
