package mcpauth

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	pkgtools "github.com/baalimago/clai/pkg/tools"

	pub_models "github.com/baalimago/clai/pkg/text/models"
)

func serverWithAuth(auth *pub_models.McpServerAuth) pub_models.McpServer {
	return pub_models.McpServer{Name: "srv", Url: "https://example.invalid/mcp", Auth: auth}
}

func TestCredentialCommandSuppliesToken(t *testing.T) {
	authz, _ := newTestAuthorizer(t)
	server := serverWithAuth(&pub_models.McpServerAuth{TokenCommand: []string{"sh", "-c", "echo mytoken"}})

	dec, source, ok, err := authz.ResolveCached(t.Context(), server)
	if err != nil {
		t.Fatalf("ResolveCached: %v", err)
	}
	if !ok || source != SourceTokenCommand {
		t.Fatalf("ok=%v source=%v, want ok=true source=%v", ok, source, SourceTokenCommand)
	}
	req, _ := http.NewRequest(http.MethodGet, "http://example.invalid", nil)
	dec(req)
	if got := req.Header.Get("Authorization"); got != "Bearer mytoken" {
		t.Errorf("Authorization = %q, want %q", got, "Bearer mytoken")
	}
}

func TestCredentialCommandFailureIsTypedError(t *testing.T) {
	authz, _ := newTestAuthorizer(t)
	server := serverWithAuth(&pub_models.McpServerAuth{TokenCommand: []string{"sh", "-c", "echo oops >&2; exit 1"}})

	_, _, _, err := authz.ResolveCached(t.Context(), server)
	var credErr *CredentialSourceError
	if !errors.As(err, &credErr) {
		t.Fatalf("got %v (%T), want *CredentialSourceError", err, err)
	}
	if credErr.Source != SourceTokenCommand {
		t.Errorf("source = %v, want %v", credErr.Source, SourceTokenCommand)
	}
}

func TestCredentialCommandEmptyOutputIsTypedError(t *testing.T) {
	authz, _ := newTestAuthorizer(t)
	server := serverWithAuth(&pub_models.McpServerAuth{TokenCommand: []string{"sh", "-c", ":"}})

	_, _, _, err := authz.ResolveCached(t.Context(), server)
	var credErr *CredentialSourceError
	if !errors.As(err, &credErr) {
		t.Fatalf("got %v (%T), want *CredentialSourceError", err, err)
	}
}

func TestCredentialCommandBannedIsNeverSpawned(t *testing.T) {
	authz, _ := newTestAuthorizer(t)
	markerPath := filepath.Join(t.TempDir(), "marker")
	server := serverWithAuth(&pub_models.McpServerAuth{TokenCommand: []string{"sh", "-c", "touch " + markerPath}})

	ctx := pkgtools.WithCmdBanContext(t.Context(), []string{"sh"})
	_, _, _, err := authz.ResolveCached(ctx, server)
	var credErr *CredentialSourceError
	if !errors.As(err, &credErr) {
		t.Fatalf("got %v (%T), want *CredentialSourceError", err, err)
	}
	if _, statErr := os.Stat(markerPath); !os.IsNotExist(statErr) {
		t.Fatalf("banned command still ran: marker file exists")
	}
}

func TestStaticBearerFromEnvAndEnvfile(t *testing.T) {
	t.Run("process environment", func(t *testing.T) {
		authz, _ := newTestAuthorizer(t)
		t.Setenv("MCPAUTH_TEST_TOKEN", "from-env")
		server := serverWithAuth(&pub_models.McpServerAuth{TokenEnv: "MCPAUTH_TEST_TOKEN"})

		dec, source, ok, err := authz.ResolveCached(t.Context(), server)
		if err != nil || !ok || source != SourceTokenEnv {
			t.Fatalf("ResolveCached = (ok=%v source=%v err=%v)", ok, source, err)
		}
		req, _ := http.NewRequest(http.MethodGet, "http://example.invalid", nil)
		dec(req)
		if got := req.Header.Get("Authorization"); got != "Bearer from-env" {
			t.Errorf("Authorization = %q, want %q", got, "Bearer from-env")
		}
	})

	t.Run("envfile", func(t *testing.T) {
		authz, _ := newTestAuthorizer(t)
		t.Setenv("MCPAUTH_TEST_TOKEN_FILE", "")
		os.Unsetenv("MCPAUTH_TEST_TOKEN_FILE")
		envPath := filepath.Join(t.TempDir(), "mcp.env")
		if err := os.WriteFile(envPath, []byte("MCPAUTH_TEST_TOKEN_FILE=from-envfile\n"), 0o600); err != nil {
			t.Fatalf("write envfile: %v", err)
		}
		server := pub_models.McpServer{
			Name:    "srv",
			Url:     "https://example.invalid/mcp",
			EnvFile: envPath,
			Auth:    &pub_models.McpServerAuth{TokenEnv: "MCPAUTH_TEST_TOKEN_FILE"},
		}

		dec, source, ok, err := authz.ResolveCached(t.Context(), server)
		if err != nil || !ok || source != SourceTokenEnv {
			t.Fatalf("ResolveCached = (ok=%v source=%v err=%v)", ok, source, err)
		}
		req, _ := http.NewRequest(http.MethodGet, "http://example.invalid", nil)
		dec(req)
		if got := req.Header.Get("Authorization"); got != "Bearer from-envfile" {
			t.Errorf("Authorization = %q, want %q", got, "Bearer from-envfile")
		}
	})
}

// TestConfiguredEnvVarAbsentIsTypedError covers the configured-but-failing
// half of the token_env source: the field itself is set (so D15's "unset"
// case does not apply), but the variable it names is absent from both the
// process environment and the envfile.
func TestConfiguredEnvVarAbsentIsTypedError(t *testing.T) {
	authz, _ := newTestAuthorizer(t)
	server := serverWithAuth(&pub_models.McpServerAuth{TokenEnv: "MCPAUTH_DEFINITELY_UNSET_VAR"})

	_, _, _, err := authz.ResolveCached(t.Context(), server)
	var credErr *CredentialSourceError
	if !errors.As(err, &credErr) {
		t.Fatalf("got %v (%T), want *CredentialSourceError", err, err)
	}
	if credErr.Source != SourceTokenEnv {
		t.Errorf("source = %v, want %v", credErr.Source, SourceTokenEnv)
	}
}

func TestUnsetCredentialSourceIsSkipped(t *testing.T) {
	authz, _ := newTestAuthorizer(t)

	t.Run("nil auth block", func(t *testing.T) {
		server := pub_models.McpServer{Name: "srv", Url: "https://example.invalid/mcp"}
		_, _, ok, err := authz.ResolveCached(t.Context(), server)
		if err != nil || ok {
			t.Fatalf("ResolveCached = (ok=%v err=%v), want (false, nil)", ok, err)
		}
	})

	t.Run("auth block with both sources unset", func(t *testing.T) {
		server := serverWithAuth(&pub_models.McpServerAuth{})
		_, _, ok, err := authz.ResolveCached(t.Context(), server)
		if err != nil || ok {
			t.Fatalf("ResolveCached = (ok=%v err=%v), want (false, nil)", ok, err)
		}
	})
}

func TestCredentialSourcePrecedence(t *testing.T) {
	t.Run("token_command wins over token_env and the store", func(t *testing.T) {
		authz, store := newTestAuthorizer(t)
		if err := store.Save("srv", TokenEntry{AccessToken: "from-store"}); err != nil {
			t.Fatalf("seed store: %v", err)
		}
		t.Setenv("MCPAUTH_PRECEDENCE_ENV", "from-env")
		server := serverWithAuth(&pub_models.McpServerAuth{
			TokenCommand: []string{"sh", "-c", "echo from-command"},
			TokenEnv:     "MCPAUTH_PRECEDENCE_ENV",
		})

		_, source, ok, err := authz.ResolveCached(t.Context(), server)
		if err != nil || !ok || source != SourceTokenCommand {
			t.Fatalf("ResolveCached = (ok=%v source=%v err=%v), want token_command", ok, source, err)
		}
	})

	t.Run("token_env wins over the store when token_command is unset", func(t *testing.T) {
		authz, store := newTestAuthorizer(t)
		if err := store.Save("srv", TokenEntry{AccessToken: "from-store"}); err != nil {
			t.Fatalf("seed store: %v", err)
		}
		t.Setenv("MCPAUTH_PRECEDENCE_ENV2", "from-env")
		server := serverWithAuth(&pub_models.McpServerAuth{TokenEnv: "MCPAUTH_PRECEDENCE_ENV2"})

		_, source, ok, err := authz.ResolveCached(t.Context(), server)
		if err != nil || !ok || source != SourceTokenEnv {
			t.Fatalf("ResolveCached = (ok=%v source=%v err=%v), want token_env", ok, source, err)
		}
	})

	t.Run("a configured but failing source never falls through to the store", func(t *testing.T) {
		authz, store := newTestAuthorizer(t)
		if err := store.Save("srv", TokenEntry{AccessToken: "from-store"}); err != nil {
			t.Fatalf("seed store: %v", err)
		}
		server := serverWithAuth(&pub_models.McpServerAuth{TokenCommand: []string{"sh", "-c", "exit 1"}})

		_, _, ok, err := authz.ResolveCached(t.Context(), server)
		if err == nil || ok {
			t.Fatalf("ResolveCached = (ok=%v err=%v), want a typed error with no fall-through to the store", ok, err)
		}
	})
}
