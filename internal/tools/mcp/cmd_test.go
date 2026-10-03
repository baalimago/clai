package mcp

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/baalimago/clai/internal/tools/mcp/httptestserver"
	"github.com/baalimago/clai/internal/tools/mcp/mcpauth"
	"github.com/baalimago/clai/internal/tools/mcp/oauthtestserver"
)

func writeNamedServerConfig(t *testing.T, confDir, name, url string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(confDir, mcpServersSubdir), 0o755); err != nil {
		t.Fatalf("mkdir mcpServers: %v", err)
	}
	conf := fmt.Sprintf(`{"url":%q}`, url)
	if err := os.WriteFile(filepath.Join(confDir, mcpServersSubdir, name+".json"), []byte(conf), 0o644); err != nil {
		t.Fatalf("write server config %q: %v", name, err)
	}
}

// TestMcpAuthCommandWritesTokenStore drives runAuthWith, the `clai mcp
// auth` subcommand's testable core, against a challenged endpoint-based
// server and asserts it writes a usable, restrictively-moded token store
// entry and reports it. RunAuth itself (the production entry point)
// differs only in which Authorizer it constructs; this test substitutes a
// browser opener for the one RunAuth builds, since the production opener
// shells out to a real browser command this test host has none of.
func TestMcpAuthCommandWritesTokenStore(t *testing.T) {
	as := oauthtestserver.New()
	defer as.Close()
	as.Configure(func(c *oauthtestserver.Config) { c.FixedAccessToken = "mcp-auth-cmd-token" })

	hs := httptestserver.New()
	defer hs.Close()
	as.Configure(func(c *oauthtestserver.Config) { c.Resource = hs.URL })
	hs.Configure(func(c *httptestserver.Config) {
		c.RequireBearerToken = "mcp-auth-cmd-token"
		c.ChallengeResourceMetaURL = as.ProtectedResourceURL()
	})

	confDir := t.TempDir()
	writeNamedServerConfig(t, confDir, "httpecho", hs.URL)

	store := mcpauth.NewTokenStore(filepath.Join(confDir, mcpauth.TokenStoreDirName))
	authz := mcpauth.NewAuthorizer(store,
		mcpauth.WithBrowserOpener(oauthtestserver.NewAutoFollowOpener()),
		mcpauth.WithInteractive(true),
	)

	// No cancel-before-Close ordering is needed here (unlike the
	// transport phase's streaming tests): runAuthWith's only connect
	// attempt is the bare, challenged one, which never reaches a
	// successful initialize, so no server-initiated stream is ever
	// opened.
	ctx := t.Context()
	if err := runAuthWith(ctx, "httpecho", confDir, authz); err != nil {
		t.Fatalf("runAuthWith: %v", err)
	}

	entry, ok := store.Load("httpecho")
	if !ok {
		t.Fatal("no token store entry written")
	}
	if entry.AccessToken != "mcp-auth-cmd-token" {
		t.Errorf("access token = %q, want %q", entry.AccessToken, "mcp-auth-cmd-token")
	}

	info, err := os.Stat(store.Path("httpecho"))
	if err != nil {
		t.Fatalf("stat token store entry: %v", err)
	}
	if got := info.Mode().Perm(); got != mcpauth.TokenStoreFileMode {
		t.Errorf("token store mode = %o, want %o", got, mcpauth.TokenStoreFileMode)
	}
}

// TestMcpAuthCommandMissingServerIsTypedError pins the obvious misuse
// case: a server name with no config file. R1-24: the test's own name
// promises a typed error, so it must assert the type, not just err != nil.
func TestMcpAuthCommandMissingServerIsTypedError(t *testing.T) {
	confDir := t.TempDir()
	store := mcpauth.NewTokenStore(filepath.Join(confDir, mcpauth.TokenStoreDirName))
	authz := mcpauth.NewAuthorizer(store)

	err := runAuthWith(t.Context(), "does-not-exist", confDir, authz)
	var cfgErr *McpAuthServerConfigError
	if !errors.As(err, &cfgErr) {
		t.Fatalf("err = %v (%T), want *McpAuthServerConfigError", err, err)
	}
	if cfgErr.Stage != "read" {
		t.Errorf("Stage = %q, want %q", cfgErr.Stage, "read")
	}
}

// TestMcpAuthCommandNonEndpointServerIsTypedError pins the server.Url == ""
// branch, which had no test at all (R1-24).
func TestMcpAuthCommandNonEndpointServerIsTypedError(t *testing.T) {
	confDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(confDir, mcpServersSubdir), 0o755); err != nil {
		t.Fatalf("mkdir mcpServers: %v", err)
	}
	conf := []byte(`{"command":"echo"}`)
	if err := os.WriteFile(filepath.Join(confDir, mcpServersSubdir, "cmdbased.json"), conf, 0o644); err != nil {
		t.Fatalf("write server config: %v", err)
	}
	store := mcpauth.NewTokenStore(filepath.Join(confDir, mcpauth.TokenStoreDirName))
	authz := mcpauth.NewAuthorizer(store)

	err := runAuthWith(t.Context(), "cmdbased", confDir, authz)
	var notEndpoint *McpAuthNotEndpointBasedError
	if !errors.As(err, &notEndpoint) {
		t.Fatalf("err = %v (%T), want *McpAuthNotEndpointBasedError", err, err)
	}
}

// TestMcpAuthCommandNoChallengeIsTypedError pins the "answered with no
// authorization challenge" branch, which had no test at all (R1-24), and
// proves the probe connection it opens is closed rather than leaked
// (R1-29): a second run against the same fixture must succeed identically,
// which a connection or listener held open by the first run would not
// allow if the fixture only accepts one connection at a time.
func TestMcpAuthCommandNoChallengeIsTypedError(t *testing.T) {
	hs := httptestserver.New()
	defer hs.Close()

	confDir := t.TempDir()
	writeNamedServerConfig(t, confDir, "unchallenged", hs.URL)
	store := mcpauth.NewTokenStore(filepath.Join(confDir, mcpauth.TokenStoreDirName))
	authz := mcpauth.NewAuthorizer(store)

	for i := range 2 {
		err := runAuthWith(t.Context(), "unchallenged", confDir, authz)
		var noChallenge *McpAuthNoChallengeError
		if !errors.As(err, &noChallenge) {
			t.Fatalf("run %d: err = %v (%T), want *McpAuthNoChallengeError", i, err, err)
		}
	}
}

// TestLoadNamedServerRejectsBothCommandAndUrl pins R1-19: loadNamedServer
// used to unmarshal the config file itself, accepting both "command" and
// "url" set, which serverconfig.FindConfiguredServers (the parser setup
// uses) rejects. The two must never resolve a server's identity
// differently.
func TestLoadNamedServerRejectsBothCommandAndUrl(t *testing.T) {
	confDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(confDir, mcpServersSubdir), 0o755); err != nil {
		t.Fatalf("mkdir mcpServers: %v", err)
	}
	conf := []byte(`{"command":"echo","url":"https://example.invalid/mcp"}`)
	if err := os.WriteFile(filepath.Join(confDir, mcpServersSubdir, "both.json"), conf, 0o644); err != nil {
		t.Fatalf("write server config: %v", err)
	}

	_, err := loadNamedServer(confDir, "both")
	var cfgErr *McpAuthServerConfigError
	if !errors.As(err, &cfgErr) {
		t.Fatalf("err = %v (%T), want *McpAuthServerConfigError", err, err)
	}
}

// TestLoadNamedServerExpandsEnvfileRelativeToConfigDir pins R1-19's second
// half: loadNamedServer's hand-rolled unmarshal left a relative envfile
// unexpanded against the process CWD instead of the config file's own
// directory, which serverconfig.FindConfiguredServers already resolves
// correctly for every other caller.
func TestLoadNamedServerExpandsEnvfileRelativeToConfigDir(t *testing.T) {
	confDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(confDir, mcpServersSubdir), 0o755); err != nil {
		t.Fatalf("mkdir mcpServers: %v", err)
	}
	if err := os.WriteFile(filepath.Join(confDir, mcpServersSubdir, "relative.env"), []byte("X=1"), 0o600); err != nil {
		t.Fatalf("write envfile: %v", err)
	}
	conf := []byte(`{"url":"https://example.invalid/mcp","envfile":"relative.env"}`)
	if err := os.WriteFile(filepath.Join(confDir, mcpServersSubdir, "relative.json"), conf, 0o644); err != nil {
		t.Fatalf("write server config: %v", err)
	}

	server, err := loadNamedServer(confDir, "relative")
	if err != nil {
		t.Fatalf("loadNamedServer: %v", err)
	}
	want := filepath.Join(confDir, mcpServersSubdir, "relative.env")
	if server.EnvFile != want {
		t.Errorf("EnvFile = %q, want %q (resolved against the config file's directory, not the process CWD)", server.EnvFile, want)
	}
}
