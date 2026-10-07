package mcp

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/baalimago/clai/internal/tools/mcp/mcpauth"
	"github.com/baalimago/clai/internal/tools/mcp/oauthtestserver"
	"github.com/baalimago/clai/internal/utils"
	"github.com/baalimago/clai/pkg/claierr"
	pub_models "github.com/baalimago/clai/pkg/text/models"
	"github.com/baalimago/go_away_boilerplate/pkg/testboil"
)

func TestAuthMessageUsesThemeAndPreservesURL(t *testing.T) {
	restoreDir := t.TempDir()
	t.Cleanup(func() {
		if err := utils.LoadTheme(restoreDir); err != nil {
			t.Errorf("restore default theme: %v", err)
		}
	})
	confDir := t.TempDir()
	t.Setenv("CLAI_CONFIG_DIR", confDir)
	t.Setenv("NO_COLOR", "")
	if err := os.WriteFile(filepath.Join(confDir, "theme.json"), []byte(`{
		"primary":"\u001b[31m", "secondary":"\u001b[32m", "breadtext":"\u001b[33m"
	}`), 0o600); err != nil {
		t.Fatal(err)
	}
	// A missing server exercises the production theme preparation without
	// reaching the network or opening a browser.
	var authErr error
	testboil.CaptureStdout(t, func(t *testing.T) {
		authErr = RunAuth(t.Context(), "missing", strings.NewReader(""))
	})
	var configErr *McpAuthServerConfigError
	if !errors.As(authErr, &configErr) {
		t.Fatalf("RunAuth: %v, want missing-server config error", authErr)
	}
	if got := utils.TableTheme(); got.Primary != "\x1b[31m" || got.Secondary != "\x1b[32m" || got.Breadtext != "\x1b[33m" {
		t.Fatalf("command did not load custom theme: %+v", got)
	}

	url := "https://example.com/authorize?state=test&code_challenge=" + strings.Repeat("x", 150)
	message := "Open this URL manually:\n" + url
	output := formatAuthMessage(message)
	want := "\x1b[31m▸ mcp.auth\x1b[0m  \x1b[33mOpen this URL manually:\x1b[0m\n  \x1b[32m" + url + "\x1b[0m"
	if output != want {
		t.Fatalf("themed output:\n got %q\nwant %q", output, want)
	}

	t.Setenv("NO_COLOR", "1")
	want = "▸ mcp.auth  Open this URL manually:\n  " + url
	if got := formatAuthMessage(message); got != want {
		t.Fatalf("NO_COLOR output:\n got %q\nwant %q", got, want)
	}
}

func TestWriteAuthMessageUsesSameStyleForEveryStage(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	for _, message := range []string{"Connecting...", "Waiting for authorization...", "Authorized. Token stored."} {
		var output bytes.Buffer
		if err := WriteAuthMessage(&output, message); err != nil {
			t.Fatal(err)
		}
		if want := "▸ mcp.auth  " + message + "\n"; output.String() != want {
			t.Fatalf("got %q, want %q", output.String(), want)
		}
	}
}

type failedAuthWriter struct{ err error }

func (w failedAuthWriter) Write([]byte) (int, error) { return 0, w.err }

func TestWriteAuthMessageReturnsOutputError(t *testing.T) {
	want := errors.New("closed output")
	if err := WriteAuthMessage(failedAuthWriter{want}, "Waiting..."); !errors.Is(err, want) {
		t.Fatalf("got %v, want %v", err, want)
	}
}

func TestSharedAuthorizerWithoutOutputNeverUsesStdout(t *testing.T) {
	as := oauthtestserver.New()
	defer as.Close()
	store := mcpauth.NewTokenStore(t.TempDir())
	authz := NewAuthorizer(AuthorizerConfig{Store: store, Interactive: true})
	mcpauth.WithBrowserOpener(oauthtestserver.NewAutoFollowOpener())(authz)
	stdout := testboil.CaptureStdout(t, func(t *testing.T) {
		_, err := authz.AuthorizeInteractive(t.Context(), pub_models.McpServer{Name: "test", Url: as.URL}, &claierr.AuthChallengeError{ServerName: "test", ResourceMetadata: as.ProtectedResourceURL()})
		if err != nil {
			t.Fatalf("authorize: %v", err)
		}
	})
	if stdout != "" {
		t.Fatalf("authorizer used process stdout: %q", stdout)
	}
	if _, ok := store.Load("test"); !ok {
		t.Fatal("authorization did not complete")
	}
}
