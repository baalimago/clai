package serverconfig

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	pub_models "github.com/baalimago/clai/pkg/text/models"
)

// TestValidateTransport_RequiresExactlyOneOfCommandOrUrl pins the exported
// entry point a server with no config file uses (D43, R2-07): the same XOR
// FindConfiguredServers enforces on the file path.
func TestValidateTransport_RequiresExactlyOneOfCommandOrUrl(t *testing.T) {
	both := pub_models.McpServer{Name: "both", Command: "node", Url: "https://mcp.example.com/mcp"}
	if err := ValidateTransport(both.Name, both); err == nil || !strings.Contains(err.Error(), "both") {
		t.Fatalf("both set: err = %v, want an error naming %q", err, "both")
	}

	neither := pub_models.McpServer{Name: "neither"}
	if err := ValidateTransport(neither.Name, neither); err == nil || !strings.Contains(err.Error(), "neither") {
		t.Fatalf("neither set: err = %v, want an error naming %q", err, "neither")
	}

	ok := pub_models.McpServer{Name: "ok", Command: "node"}
	if err := ValidateTransport(ok.Name, ok); err != nil {
		t.Fatalf("command only: unexpected error: %v", err)
	}
}

// TestFindConfiguredServers_ParsesTimeoutSeconds pins the ported behaviour
// unchanged by relocation: a server's name is derived from its config
// file's base name, and timeout_seconds survives the parse.
func TestFindConfiguredServers_ParsesTimeoutSeconds(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "playwright.json")
	if err := os.WriteFile(path, []byte(`{
		"command": "npm",
		"args": ["exec", "@playwright/mcp"],
		"timeout_seconds": 300
	}`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	servers, err := FindConfiguredServers([]string{path})
	if err != nil {
		t.Fatalf("FindConfiguredServers: %v", err)
	}
	if len(servers) != 1 {
		t.Fatalf("expected 1 server, got %d", len(servers))
	}
	if servers[0].TimeoutSeconds != 300 {
		t.Errorf("TimeoutSeconds = %d, want 300", servers[0].TimeoutSeconds)
	}
	if servers[0].Name != "playwright" {
		t.Errorf("Name = %q, want playwright (derived from filename)", servers[0].Name)
	}
}

// TestFindConfiguredServers_EnvFileHomeResolution pins the ported envfile
// normalisation: a relative path joins the config file's own directory, a
// tilde or $HOME resolves against the home directory, and an absolute path
// is untouched.
func TestFindConfiguredServers_EnvFileHomeResolution(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	confDir := t.TempDir()

	tests := []struct {
		name    string
		envfile string
		want    string
	}{
		{name: "tilde resolves to home", envfile: "~/.envfile", want: filepath.Join(home, ".envfile")},
		{name: "HOME var resolves to home", envfile: "$HOME/.envfile", want: filepath.Join(home, ".envfile")},
		{name: "relative joins config dir", envfile: ".envfile", want: filepath.Join(confDir, ".envfile")},
		{name: "absolute untouched", envfile: "/etc/clai/.envfile", want: "/etc/clai/.envfile"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(confDir, "srv.json")
			cfg := fmt.Sprintf(`{"command":"echo","envfile":%q}`, tt.envfile)
			if err := os.WriteFile(path, []byte(cfg), 0o644); err != nil {
				t.Fatalf("write config: %v", err)
			}
			servers, err := FindConfiguredServers([]string{path})
			if err != nil {
				t.Fatalf("FindConfiguredServers: %v", err)
			}
			if len(servers) != 1 {
				t.Fatalf("expected 1 server, got %d", len(servers))
			}
			if servers[0].EnvFile != tt.want {
				t.Fatalf("EnvFile = %q, want %q", servers[0].EnvFile, tt.want)
			}
		})
	}
}

// TestFindConfiguredServers_RequiresExactlyOneOfCommandOrUrl pins the
// config's own XOR, both halves, naming the file.
func TestFindConfiguredServers_RequiresExactlyOneOfCommandOrUrl(t *testing.T) {
	dir := t.TempDir()

	both := filepath.Join(dir, "both.json")
	if err := os.WriteFile(both, []byte(`{"command":"node","url":"https://mcp.example.com/mcp"}`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if _, err := FindConfiguredServers([]string{both}); err == nil || !strings.Contains(err.Error(), both) {
		t.Fatalf("both set: err = %v, want an error naming %q", err, both)
	}

	neither := filepath.Join(dir, "neither.json")
	if err := os.WriteFile(neither, []byte(`{"env":{"K":"V"}}`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if _, err := FindConfiguredServers([]string{neither}); err == nil || !strings.Contains(err.Error(), neither) {
		t.Fatalf("neither set: err = %v, want an error naming %q", err, neither)
	}
}

// TestFindConfiguredServers_RejectsNonAbsoluteEndpoint pins url validation:
// anything that is not an absolute http or https URL is a parse error
// naming the file and the url field.
func TestFindConfiguredServers_RejectsNonAbsoluteEndpoint(t *testing.T) {
	for _, bad := range []string{"not-a-url", "/relative/path", "ftp://mcp.example.com/mcp"} {
		dir := t.TempDir()
		path := filepath.Join(dir, "bad.json")
		if err := os.WriteFile(path, []byte(`{"url":"`+bad+`"}`), 0o644); err != nil {
			t.Fatalf("write config: %v", err)
		}
		_, err := FindConfiguredServers([]string{path})
		if err == nil {
			t.Fatalf("url %q: expected an error, got none", bad)
		}
		if !strings.Contains(err.Error(), path) || !strings.Contains(err.Error(), "url") {
			t.Errorf("url %q: err = %v, want it to name the file and the url field", bad, err)
		}
	}
}

// TestFindConfiguredServers_SkipsUnreadableFile pins that a file that
// cannot even be read (as opposed to one that fails to parse) is silently
// skipped rather than reported: findConfiguredMcpServers's caller already
// gates this at the glob stage, so the only unreadable case here is a path
// that never existed.
func TestFindConfiguredServers_SkipsUnreadableFile(t *testing.T) {
	servers, err := FindConfiguredServers([]string{filepath.Join(t.TempDir(), "missing.json")})
	if err != nil {
		t.Fatalf("FindConfiguredServers: %v", err)
	}
	if len(servers) != 0 {
		t.Fatalf("expected no servers, got %v", servers)
	}
}
