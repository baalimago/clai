package mcpauth

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/baalimago/clai/internal/tools/mcp/oauthtestserver"
	pub_models "github.com/baalimago/clai/pkg/text/models"
	"github.com/baalimago/go_away_boilerplate/pkg/debug"
	"github.com/baalimago/go_away_boilerplate/pkg/testboil"
)

// assertNoSecret fails the test if any of secrets appears in haystack.
func assertNoSecret(t *testing.T, label, haystack string, secrets []string) {
	t.Helper()
	for _, secret := range secrets {
		if secret == "" {
			continue
		}
		if strings.Contains(haystack, secret) {
			t.Fatalf("%s leaks a secret: %q contains %q", label, haystack, secret)
		}
	}
}

// liveSecrets drives a full interactive flow and a subsequent rejected
// refresh against as, returning every secret value the redaction table
// governs plus the two errors produced along the way.
func liveSecrets(t *testing.T, as *oauthtestserver.Server) (secrets []string, refreshErr error) {
	t.Helper()
	as.Configure(func(c *oauthtestserver.Config) { c.IssueClientSecret = true })
	_, store, _, err := fullInteractiveFlow(t, as)
	if err != nil {
		t.Fatalf("fullInteractiveFlow: %v", err)
	}
	entry, ok := store.Load("srv")
	if !ok {
		t.Fatal("no entry stored")
	}
	secrets = []string{entry.AccessToken, entry.RefreshToken, entry.ClientSecret}

	as.Configure(func(c *oauthtestserver.Config) { c.RefreshRejects = true })
	authz := NewAuthorizer(store, WithClock(func() time.Time { return time.Now().Add(time.Hour) }))
	_, refreshErr = authz.refresh(t.Context(), testServer("https://example.invalid/mcp"), entry)
	return secrets, refreshErr
}

func TestCredentialNeverAppearsInError(t *testing.T) {
	as := oauthtestserver.New()
	defer as.Close()
	secrets, refreshErr := liveSecrets(t, as)
	if refreshErr == nil {
		t.Fatal("expected a refresh error")
	}
	assertNoSecret(t, "refresh error", refreshErr.Error(), secrets)

	credErr := &CredentialSourceError{ServerName: "srv", Source: SourceTokenCommand, Cause: fmt.Errorf("boom")}
	assertNoSecret(t, "credential source error", credErr.Error(), secrets)
}

func TestCredentialNeverAppearsInLog(t *testing.T) {
	as := oauthtestserver.New()
	defer as.Close()
	secrets, refreshErr := liveSecrets(t, as)

	// A "log line" is whatever a caller formats an error into, e.g. the
	// ancli.Warnf pattern this repository uses at every call site that
	// degrades on a non-fatal failure.
	logLine := fmt.Sprintf("failed to refresh mcp credential for %q: %v\n", "srv", refreshErr)
	assertNoSecret(t, "log line", logLine, secrets)
}

// TestCredentialRedactedInDebugOutput pins the server-config dump this
// repository's DEBUG flag already prints
// (debug.IndentedJsonFmt(mcpServers) in querier_setup_tools.go): it serializes
// the config struct, which never carries a resolved secret in the first
// place — the auth block holds a command or an env var name, never the
// token that command or variable produces. Running a real flow first
// proves the secret values exist somewhere at runtime; dumping the config
// struct that produced them proves none of those values leaked into it.
func TestCredentialRedactedInDebugOutput(t *testing.T) {
	as := oauthtestserver.New()
	defer as.Close()
	as.Configure(func(c *oauthtestserver.Config) { c.IssueClientSecret = true })
	_, store, _, err := fullInteractiveFlow(t, as)
	if err != nil {
		t.Fatalf("fullInteractiveFlow: %v", err)
	}
	entry, _ := store.Load("srv")
	secrets := []string{entry.AccessToken, entry.RefreshToken, entry.ClientSecret}

	server := serverWithAuth(&pub_models.McpServerAuth{
		TokenCommand: []string{"sh", "-c", "echo should-not-appear"},
		TokenEnv:     "SOME_VAR",
		Scopes:       []string{"read"},
	})
	dumped := debug.IndentedJsonFmt(server)
	assertNoSecret(t, "debug dump of the server config", dumped, secrets)
}

// TestSecretsNeverLeakIntoUncoveredErrorTypes closes R1-10's remaining gap:
// the four redaction tests above covered one error type plus one
// hand-built CredentialSourceError. Nothing covered ExchangeRejectedError,
// RegistrationRejectedError, TokenStoreWriteError, DiscoveryError,
// MetadataURLError or RedirectError. Each is triggered here through the
// real fakes (never hand-built), and checked against secrets minted by a
// separate, successful flow — the realistic multi-tenant risk invariant 6
// actually guards against: a server's own failure must never leak a
// credential a different server's successful flow produced and which is
// sitting in the very same token store or process.
func TestSecretsNeverLeakIntoUncoveredErrorTypes(t *testing.T) {
	livingAs := oauthtestserver.New()
	defer livingAs.Close()
	secrets, refreshErr := liveSecrets(t, livingAs)
	if refreshErr == nil {
		t.Fatal("expected a refresh error from liveSecrets")
	}

	t.Run("ExchangeRejectedError", func(t *testing.T) {
		as := oauthtestserver.New()
		defer as.Close()
		as.Configure(func(c *oauthtestserver.Config) { c.ExchangeRejects = true })
		_, _, _, err := fullInteractiveFlow(t, as)
		var exErr *ExchangeRejectedError
		if !errors.As(err, &exErr) {
			t.Fatalf("got %v (%T), want *ExchangeRejectedError", err, err)
		}
		assertNoSecret(t, "ExchangeRejectedError", exErr.Error(), secrets)
	})

	t.Run("RegistrationRejectedError", func(t *testing.T) {
		as := oauthtestserver.New()
		defer as.Close()
		as.Configure(func(c *oauthtestserver.Config) { c.RegistrationRejects = true })
		_, err := registerDynamicClient(t.Context(), http.DefaultClient, "srv", as.RegistrationEndpoint(), "http://127.0.0.1:0/callback")
		var regErr *RegistrationRejectedError
		if !errors.As(err, &regErr) {
			t.Fatalf("got %v (%T), want *RegistrationRejectedError", err, err)
		}
		assertNoSecret(t, "RegistrationRejectedError", regErr.Error(), secrets)
	})

	t.Run("TokenStoreWriteError", func(t *testing.T) {
		// A path through a regular file, not a directory, so MkdirAll
		// fails deterministically regardless of uid (mcpauth's own
		// TestTokenStoreUnwritableIsTypedError trick).
		blocker := t.TempDir() + "-blocker-file"
		if err := os.WriteFile(blocker, []byte("not a directory"), 0o644); err != nil {
			t.Fatalf("write blocker file: %v", err)
		}
		brokenStore := NewTokenStore(blocker + "/mcpAuth")
		err := brokenStore.Save("srv", TokenEntry{AccessToken: secrets[0], RefreshToken: secrets[1]})
		var writeErr *TokenStoreWriteError
		if !errors.As(err, &writeErr) {
			t.Fatalf("got %v (%T), want *TokenStoreWriteError", err, err)
		}
		assertNoSecret(t, "TokenStoreWriteError", writeErr.Error(), secrets)
	})

	t.Run("DiscoveryError", func(t *testing.T) {
		_, err := discoverProtectedResource(t.Context(), http.DefaultClient, "srv", "http://127.0.0.1:0/.well-known/oauth-protected-resource")
		var discErr *DiscoveryError
		if !errors.As(err, &discErr) {
			t.Fatalf("got %v (%T), want *DiscoveryError", err, err)
		}
		assertNoSecret(t, "DiscoveryError", discErr.Error(), secrets)
	})

	t.Run("MetadataURLError", func(t *testing.T) {
		as := oauthtestserver.New()
		defer as.Close()
		as.Configure(func(c *oauthtestserver.Config) { c.MetadataAt = oauthtestserver.MetadataAtNeither })
		_, err := discoverAuthorizationServer(t.Context(), http.DefaultClient, "srv", as.IssuerURL())
		var urlErr *MetadataURLError
		if !errors.As(err, &urlErr) {
			t.Fatalf("got %v (%T), want *MetadataURLError", err, err)
		}
		assertNoSecret(t, "MetadataURLError", urlErr.Error(), secrets)
	})

	t.Run("RedirectError", func(t *testing.T) {
		as := oauthtestserver.New()
		defer as.Close()
		as.Configure(func(c *oauthtestserver.Config) { c.RedirectError = "access_denied" })
		_, _, _, err := fullInteractiveFlow(t, as)
		var redirErr *RedirectError
		if !errors.As(err, &redirErr) {
			t.Fatalf("got %v (%T), want *RedirectError", err, err)
		}
		assertNoSecret(t, "RedirectError", redirErr.Error(), secrets)
	})
}

// TestCredentialCommandOutputNeverEchoed pins the redaction table's
// "Credential command output | Captured, used, never echoed" row, which
// TestCredentialCommandSuppliesToken only ever asserted the "used" half
// of. The token is captured here as its own real process stdout, so a
// regression that fmt.Println'd or ancli.Okf'd it on the way to the
// Authorization header would show up on this process's own stdout.
func TestCredentialCommandOutputNeverEchoed(t *testing.T) {
	authz, _ := newTestAuthorizer(t)
	const token = "never-echoed-credential-command-token"
	server := serverWithAuth(&pub_models.McpServerAuth{TokenCommand: []string{"sh", "-c", "echo " + token}})

	var dec func(*http.Request)
	stdout := testboil.CaptureStdout(t, func(t *testing.T) {
		d, _, ok, err := authz.ResolveCached(t.Context(), server)
		if err != nil || !ok {
			t.Fatalf("ResolveCached = (ok=%v err=%v)", ok, err)
		}
		dec = d
	})
	if strings.Contains(stdout, token) {
		t.Fatalf("credential command token echoed to stdout: %q", stdout)
	}
	req, _ := http.NewRequest(http.MethodGet, "http://example.invalid", nil)
	dec(req)
	if got := req.Header.Get("Authorization"); got != "Bearer "+token {
		t.Errorf("Authorization = %q, want Bearer %s", got, token)
	}
}
