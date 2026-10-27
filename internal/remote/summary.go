package remote

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/baalimago/clai/internal/look"
)

// Server is one MCP server a profile starts or reaches.
type Server struct {
	Name string
	// Runs is the command line, or the url of a remote server.
	Runs string
	// Unpinned marks a package runner (npx, pnpx, bunx, uvx) fetching a
	// package with no @version: it can change under the profile.
	Unpinned bool
	// TokenCommand is the command line a url server runs on this machine
	// for its token (auth.token_command), or "".
	TokenCommand string
	// Env are the server's env entries as KEY=value, sorted by key.
	Env []string
}

// EnvNeed is one environment variable a server needs.
type EnvNeed struct {
	Name   string
	Server string
}

// ToolAccess is which tools a profile gives the model.
type ToolAccess int

const (
	// ToolsOff: no tools and no MCP servers.
	ToolsOff ToolAccess = iota
	// ToolsListed: the profile's tools globs and its own servers' tools.
	ToolsListed
)

// Summary is what a profile asks of the machine that runs it: what the
// trust prompt and `clai profiles trust` show before anything runs. Its
// strings are the profile's own; String makes them printable.
type Summary struct {
	Model    string
	Access   ToolAccess
	Tools    []string
	Servers  []Server
	EnvFiles []string
	// Needs are the variables the servers read from the environment, in
	// server order: auth.required_env, plus auth.token_env when no
	// auth.token_command takes precedence over it.
	Needs   []EnvNeed
	Ignored []string
	// Skills are the profile's skill targets, fetched and trusted with it.
	Skills []Target
	// Prompt is the profile's system prompt.
	Prompt string
}

var fetchers = map[string]bool{"npx": true, "pnpx": true, "bunx": true, "uvx": true}

// Summarize reads a validated profile body.
func Summarize(body []byte, checked Checked) (Summary, error) {
	var p struct {
		Model      string   `json:"model"`
		Prompt     string   `json:"prompt"`
		UseTools   bool     `json:"use_tools"`
		Tools      []string `json:"tools"`
		McpServers map[string]struct {
			Command string            `json:"command"`
			Args    []string          `json:"args"`
			URL     string            `json:"url"`
			EnvFile string            `json:"envfile"`
			Env     map[string]string `json:"env"`
			Auth    *struct {
				TokenCommand []string `json:"token_command"`
				TokenEnv     string   `json:"token_env"`
				RequiredEnv  []string `json:"required_env"`
			} `json:"auth"`
		} `json:"mcp_servers"`
	}
	if err := json.Unmarshal(body, &p); err != nil {
		return Summary{}, fmt.Errorf("read profile summary: %w", err)
	}
	skills, err := ProfileSkills(body)
	if err != nil {
		return Summary{}, err
	}
	s := Summary{Model: p.Model, Prompt: p.Prompt, Tools: p.Tools, Ignored: checked.Ignored, Skills: skills}
	s.Access = toolAccess(p.UseTools, len(p.Tools), len(p.McpServers))
	for _, name := range sortedKeys(p.McpServers) {
		srv := p.McpServers[name]
		runs := srv.URL
		if srv.Command != "" {
			runs = commandLine(append([]string{srv.Command}, srv.Args...))
		}
		server := Server{Name: name, Runs: runs, Unpinned: unpinned(srv.Command, srv.Args)}
		for _, k := range sortedKeys(srv.Env) {
			server.Env = append(server.Env, k+"="+srv.Env[k])
		}
		if srv.EnvFile != "" {
			s.EnvFiles = append(s.EnvFiles, srv.EnvFile)
		}
		if srv.Auth != nil {
			if len(srv.Auth.TokenCommand) > 0 {
				server.TokenCommand = commandLine(srv.Auth.TokenCommand)
			}
			s.Needs = append(s.Needs, envNeeds(name, srv.Auth.RequiredEnv, srv.Auth.TokenEnv, srv.Auth.TokenCommand)...)
		}
		s.Servers = append(s.Servers, server)
	}
	return s, nil
}

// toolAccess mirrors text.ProfileOverrides: a fetched profile gets only
// the tools it lists and its own servers' tools.
func toolAccess(useTools bool, tools, servers int) ToolAccess {
	if servers == 0 && (!useTools || tools == 0) {
		return ToolsOff
	}
	return ToolsListed
}

func envNeeds(server string, required []string, tokenEnv string, tokenCommand []string) []EnvNeed {
	names := append([]string(nil), required...)
	if tokenEnv != "" && len(tokenCommand) == 0 {
		names = append(names, tokenEnv)
	}
	var needs []EnvNeed
	seen := make(map[string]bool, len(names))
	for _, n := range names {
		if seen[n] {
			continue
		}
		seen[n] = true
		needs = append(needs, EnvNeed{Name: n, Server: server})
	}
	return needs
}

func unpinned(command string, args []string) bool {
	if !fetchers[command] {
		return false
	}
	for _, a := range args {
		if strings.HasPrefix(a, "-") {
			continue
		}
		// uvx also pins with ==, as pkg==1.0.
		if command == "uvx" && strings.Contains(a, "==") {
			return false
		}
		// A scoped package starts with @, so its version is a second @.
		return !strings.Contains(strings.TrimPrefix(a, "@"), "@")
	}
	return false
}

// String renders the summary as `clai profiles trust` prints it, without
// colour.
func (s Summary) String() string { return render(s.Lines(look.Style{})) }

// Lines are the summary's sections in clai's look. Every string from the
// profile is made printable first.
func (s Summary) Lines(st look.Style) []string {
	var lines []string
	switch {
	case s.Access == ToolsOff:
		lines = append(lines, st.Head("tools", "none", "no MCP servers"))
	case len(s.Servers) == 0:
		lines = append(lines, st.Head("MCP servers", "none of its own"))
	default:
		lines = append(lines, st.Head("MCP servers", strconv.Itoa(len(s.Servers))))
		lines = append(lines, look.Grid("  ", serverRows(s.Servers), st)...)
	}
	switch {
	case s.Access == ToolsOff:
	case len(s.Tools) > 0:
		lines = append(lines, st.Head("tools", printableList(s.Tools)))
	default:
		lines = append(lines, st.Head("tools", "its MCP servers' only"))
	}
	if len(s.Skills) > 0 {
		lines = append(lines, st.Head("skills", strconv.Itoa(len(s.Skills))))
		lines = append(lines, look.Grid("  ", skillRows(s.Skills), st)...)
	}
	if len(s.EnvFiles) > 0 {
		lines = append(lines, st.Head("reads", printableList(s.EnvFiles)))
	}
	if len(s.Needs) > 0 {
		needs := make([]string, 0, len(s.Needs))
		for _, n := range s.Needs {
			needs = append(needs, n.Name+" for "+n.Server)
		}
		lines = append(lines, st.Head("needs", printableList(needs)))
	}
	model := Printable(s.Model)
	if model == "" {
		model = "your default"
	}
	lines = append(lines, st.Head("model", model))
	lines = append(lines, promptLines(st, "prompt", s.Prompt)...)
	if len(s.Ignored) > 0 {
		lines = append(lines, st.Head("not read by clai", printableList(s.Ignored)))
	}
	return lines
}

func serverRows(servers []Server) [][]string {
	var rows [][]string
	for _, srv := range servers {
		row := []string{Printable(srv.Name), Printable(srv.Runs)}
		if srv.Unpinned {
			row = append(row, "! not pinned")
		}
		rows = append(rows, row)
		if srv.TokenCommand != "" {
			rows = append(rows, []string{"", "runs here for a token: " + Printable(srv.TokenCommand)})
		}
		for _, kv := range srv.Env {
			rows = append(rows, []string{"", "env " + Printable(kv)})
		}
	}
	return rows
}

func skillRows(skills []Target) [][]string {
	rows := make([][]string, 0, len(skills))
	for _, t := range skills {
		row := []string{Printable(t.String())}
		if !t.Pinned() && t.SHA256 == "" {
			row = append(row, "! not pinned")
		}
		rows = append(rows, row)
	}
	return rows
}

// promptLines heads a text the model will read and quotes its preview
// under a bar, so it never reads as clai's own output.
func promptLines(st look.Style, title, text string, details ...string) []string {
	preview := Preview(text)
	if len(preview) == 0 {
		return []string{st.Head(title, append(details, "none")...)}
	}
	n := utf8.RuneCountInString(strings.TrimSpace(text))
	lines := []string{st.Head(title, append(details, strconv.Itoa(n)+" characters")...)}
	for _, l := range preview {
		lines = append(lines, "  "+st.Secondary("│")+" "+l)
	}
	return lines
}

func render(lines []string) string { return strings.Join(lines, "\n") + "\n" }

func printableList(items []string) string {
	return Printable(strings.Join(items, ", "))
}
