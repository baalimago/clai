package mcpauth

import (
	"context"
	"errors"
	"net"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/baalimago/clai/internal/tools/mcp/oauthtestserver"
	"github.com/baalimago/clai/pkg/claierr"
	pub_models "github.com/baalimago/clai/pkg/text/models"
)

func testServer(url string) pub_models.McpServer {
	return pub_models.McpServer{Name: "srv", Url: url}
}

func newTestAuthorizer(t *testing.T, opts ...Option) (*Authorizer, *TokenStore) {
	t.Helper()
	store := NewTokenStore(t.TempDir())
	// WithInteractive(true): every test here simulates a terminal-attached
	// run. AuthorizeInteractive refuses outright without it (sign-off
	// review, B2), which TestOauthNonInteractiveRunIsRefused pins.
	base := append([]Option{WithBrowserOpener(oauthtestserver.NewAutoFollowOpener()), WithEnvFileLoader(testEnvFileLoader), WithInteractive(true)}, opts...)
	return NewAuthorizer(store, base...), store
}

func TestOauthDiscoversProtectedResourceMetadataFromChallenge(t *testing.T) {
	as := oauthtestserver.New()
	defer as.Close()

	doc, err := discoverProtectedResource(t.Context(), http.DefaultClient, "srv", as.ProtectedResourceURL())
	if err != nil {
		t.Fatalf("discoverProtectedResource: %v", err)
	}
	if len(doc.AuthorizationServers) != 1 || doc.AuthorizationServers[0] != as.IssuerURL() {
		t.Fatalf("authorization_servers = %v, want [%s]", doc.AuthorizationServers, as.IssuerURL())
	}
	if len(doc.ScopesSupported) == 0 {
		t.Fatalf("scopes_supported is empty")
	}
}

func TestOauthResourceMetadataFailureIsTypedError(t *testing.T) {
	as := oauthtestserver.New()
	defer as.Close()

	_, err := discoverProtectedResource(t.Context(), http.DefaultClient, "srv", as.URL+"/does-not-exist")
	var discErr *DiscoveryError
	if !errors.As(err, &discErr) {
		t.Fatalf("got %v (%T), want *DiscoveryError", err, err)
	}
	if discErr.Stage != "protected-resource" {
		t.Fatalf("stage = %q, want protected-resource", discErr.Stage)
	}
}

func TestOauthDiscoversAuthorizationServerMetadata(t *testing.T) {
	as := oauthtestserver.New()
	defer as.Close()

	meta, err := discoverAuthorizationServer(t.Context(), http.DefaultClient, "srv", as.IssuerURL())
	if err != nil {
		t.Fatalf("discoverAuthorizationServer: %v", err)
	}
	if meta.RegistrationEndpoint != as.RegistrationEndpoint() {
		t.Errorf("registration_endpoint = %q, want %q", meta.RegistrationEndpoint, as.RegistrationEndpoint())
	}
	if meta.AuthorizationEndpoint != as.AuthorizationEndpoint() {
		t.Errorf("authorization_endpoint = %q, want %q", meta.AuthorizationEndpoint, as.AuthorizationEndpoint())
	}
	if meta.TokenEndpoint != as.TokenEndpoint() {
		t.Errorf("token_endpoint = %q, want %q", meta.TokenEndpoint, as.TokenEndpoint())
	}
	if !containsString(meta.CodeChallengeMethodsSupported, "S256") {
		t.Errorf("code_challenge_methods_supported = %v, want S256", meta.CodeChallengeMethodsSupported)
	}
	if !containsString(meta.GrantTypesSupported, "refresh_token") {
		t.Errorf("grant_types_supported = %v, want refresh_token", meta.GrantTypesSupported)
	}
}

func TestOauthMetadataMissingRequiredFieldIsTypedError(t *testing.T) {
	as := oauthtestserver.New()
	defer as.Close()
	as.Configure(func(c *oauthtestserver.Config) { c.OmitMetadataField = "registration_endpoint" })

	_, err := discoverAuthorizationServer(t.Context(), http.DefaultClient, "srv", as.IssuerURL())
	var missErr *MetadataMissingFieldError
	if !errors.As(err, &missErr) {
		t.Fatalf("got %v (%T), want *MetadataMissingFieldError", err, err)
	}
	if missErr.Field != "registration_endpoint" {
		t.Errorf("field = %q, want registration_endpoint", missErr.Field)
	}
}

func TestOauthMetadataUrlCompositionFallsBackToAppendedForm(t *testing.T) {
	as := oauthtestserver.New()
	defer as.Close()
	as.Configure(func(c *oauthtestserver.Config) {
		c.IssuerPath = "/tenant1"
		c.MetadataAt = oauthtestserver.MetadataAtAppended
	})

	meta, err := discoverAuthorizationServer(t.Context(), http.DefaultClient, "srv", as.IssuerURL())
	if err != nil {
		t.Fatalf("discoverAuthorizationServer (appended form): %v", err)
	}
	if meta.TokenEndpoint != as.TokenEndpoint() {
		t.Errorf("token_endpoint = %q, want %q", meta.TokenEndpoint, as.TokenEndpoint())
	}

	as.Configure(func(c *oauthtestserver.Config) { c.MetadataAt = oauthtestserver.MetadataAtNeither })
	_, err = discoverAuthorizationServer(t.Context(), http.DefaultClient, "srv", as.IssuerURL())
	var urlErr *MetadataURLError
	if !errors.As(err, &urlErr) {
		t.Fatalf("got %v (%T), want *MetadataURLError", err, err)
	}
	if urlErr.Inserted == "" || urlErr.Appended == "" || urlErr.Inserted == urlErr.Appended {
		t.Errorf("expected two distinct non-empty candidates, got inserted=%q appended=%q", urlErr.Inserted, urlErr.Appended)
	}
}

func TestOauthDynamicClientRegistration(t *testing.T) {
	as := oauthtestserver.New()
	defer as.Close()
	as.Configure(func(c *oauthtestserver.Config) { c.IssueClientSecret = true })

	reg, err := registerDynamicClient(t.Context(), http.DefaultClient, "srv", as.RegistrationEndpoint(), "http://127.0.0.1:0/callback")
	if err != nil {
		t.Fatalf("registerDynamicClient: %v", err)
	}
	if reg.ClientID == "" {
		t.Fatal("client_id is empty")
	}
	if reg.ClientSecret == "" {
		t.Fatal("client_secret is empty, want one since the fixture issues one")
	}
}

func TestOauthRegistrationRejectionIsTypedError(t *testing.T) {
	as := oauthtestserver.New()
	defer as.Close()
	as.Configure(func(c *oauthtestserver.Config) {
		c.RegistrationRejects = true
		c.RegistrationReason = "client metadata invalid"
	})

	_, err := registerDynamicClient(t.Context(), http.DefaultClient, "srv", as.RegistrationEndpoint(), "http://127.0.0.1:0/callback")
	var regErr *RegistrationRejectedError
	if !errors.As(err, &regErr) {
		t.Fatalf("got %v (%T), want *RegistrationRejectedError", err, err)
	}
	if regErr.Reason != "client metadata invalid" {
		t.Errorf("reason = %q, want %q", regErr.Reason, "client metadata invalid")
	}
}

// fullInteractiveFlow drives AuthorizeInteractive end to end against as,
// using the auto-follow opener to simulate the operator's browser.
func fullInteractiveFlow(t *testing.T, as *oauthtestserver.Server) (*Authorizer, *TokenStore, func(*http.Request), error) {
	t.Helper()
	authz, store := newTestAuthorizer(t)
	challenge := &claierr.AuthChallengeError{ServerName: "srv", ResourceMetadata: as.ProtectedResourceURL()}
	dec, err := authz.AuthorizeInteractive(t.Context(), testServer(as.URL), challenge)
	return authz, store, dec, err
}

func TestOauthPkceChallengeIsS256(t *testing.T) {
	as := oauthtestserver.New()
	defer as.Close()

	_, _, _, err := fullInteractiveFlow(t, as)
	if err != nil {
		t.Fatalf("AuthorizeInteractive: %v", err)
	}
	if got := as.LastCodeChallengeMethod(); got != "S256" {
		t.Errorf("code_challenge_method = %q, want S256", got)
	}
}

func TestOauthAuthorizationCodeExchangeStoresTokens(t *testing.T) {
	as := oauthtestserver.New()
	defer as.Close()

	_, store, dec, err := fullInteractiveFlow(t, as)
	if err != nil {
		t.Fatalf("AuthorizeInteractive: %v", err)
	}
	entry, ok := store.Load("srv")
	if !ok {
		t.Fatal("no entry stored")
	}
	if entry.AccessToken == "" || entry.RefreshToken == "" || entry.ClientID == "" {
		t.Fatalf("entry incomplete: %+v", entry)
	}
	req, _ := http.NewRequest(http.MethodGet, "http://example.invalid", nil)
	dec(req)
	if got := req.Header.Get("Authorization"); got != "Bearer "+entry.AccessToken {
		t.Errorf("Authorization header = %q, want Bearer %s", got, entry.AccessToken)
	}
}

func TestOauthExchangeRejectionWritesNothing(t *testing.T) {
	as := oauthtestserver.New()
	defer as.Close()
	as.Configure(func(c *oauthtestserver.Config) { c.ExchangeRejects = true })

	_, store, _, err := fullInteractiveFlow(t, as)
	var exErr *ExchangeRejectedError
	if !errors.As(err, &exErr) {
		t.Fatalf("got %v (%T), want *ExchangeRejectedError", err, err)
	}
	if _, ok := store.Load("srv"); ok {
		t.Fatal("entry written despite a rejected exchange")
	}
}

func TestOauthChallengeWithoutResourceMetadataIsTypedError(t *testing.T) {
	authz, _ := newTestAuthorizer(t)
	_, err := authz.AuthorizeInteractive(t.Context(), testServer("https://mcp.example.invalid/mcp"), &claierr.AuthChallengeError{ServerName: "srv"})
	var missErr *ChallengeMissingResourceMetadataError
	if !errors.As(err, &missErr) {
		t.Fatalf("got %v (%T), want *ChallengeMissingResourceMetadataError", err, err)
	}
}

func TestOauthRedirectErrorIsTypedError(t *testing.T) {
	as := oauthtestserver.New()
	defer as.Close()
	as.Configure(func(c *oauthtestserver.Config) { c.RedirectError = "access_denied" })

	_, _, _, err := fullInteractiveFlow(t, as)
	var redirErr *RedirectError
	if !errors.As(err, &redirErr) {
		t.Fatalf("got %v (%T), want *RedirectError", err, err)
	}
	if redirErr.Reason != "access_denied" {
		t.Errorf("reason = %q, want access_denied", redirErr.Reason)
	}
}

// forgedStateOpener simulates a forged redirect: instead of driving the
// real authorization server's /authorize endpoint, it submits a code
// straight to the registered redirect_uri with a state value that never
// came from this flow's own prepareAuthorization call, exactly what an
// attacker steering a victim to a crafted link would do (R1-11).
type forgedStateOpener struct{ client *http.Client }

func (o *forgedStateOpener) Open(authURL string) error {
	redirectURI := extractQueryParam(authURL, "redirect_uri")
	go func() {
		resp, err := o.client.Get(redirectURI + "?code=forged-code&state=not-the-real-state")
		if err == nil {
			resp.Body.Close()
		}
	}()
	return nil
}

// TestOauthLoopbackStateMismatchIsTypedError pins R1-11: a redirect whose
// state does not match the one this flow minted is a typed error, not a
// silently accepted code. Before the fix, redirectResult.state had zero
// readers in the package, so this exact forged redirect would have been
// accepted and exchanged.
func TestOauthLoopbackStateMismatchIsTypedError(t *testing.T) {
	as := oauthtestserver.New()
	defer as.Close()

	authz, _ := newTestAuthorizer(t, WithBrowserOpener(&forgedStateOpener{client: &http.Client{Timeout: 5 * time.Second}}))
	challenge := &claierr.AuthChallengeError{ServerName: "srv", ResourceMetadata: as.ProtectedResourceURL()}
	_, err := authz.AuthorizeInteractive(t.Context(), testServer(as.URL), challenge)
	var redirErr *RedirectError
	if !errors.As(err, &redirErr) {
		t.Fatalf("got %v (%T), want *RedirectError", err, err)
	}
	if redirErr.Reason != "state parameter mismatch" {
		t.Errorf("reason = %q, want %q", redirErr.Reason, "state parameter mismatch")
	}
}

// TestLoopbackHandlerIgnoresOtherPaths pins R1-11's second half: a request
// to the ephemeral port on any path other than the redirect URI's own must
// not resolve the wait, so an unrelated request racing the real redirect
// cannot be mistaken for it.
func TestLoopbackHandlerIgnoresOtherPaths(t *testing.T) {
	ln, err := listenLoopback(DefaultLoopbackHost, 0)
	if err != nil {
		t.Fatalf("listenLoopback: %v", err)
	}
	defer ln.Close()

	addr := ln.Addr().String()
	go func() {
		//nolint:errcheck
		http.Get("http://" + addr + "/not-the-callback-path?code=should-be-ignored")
	}()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := awaitRedirect(ctx, ln); err == nil {
		t.Fatal("awaitRedirect resolved on a request to an unrelated path")
	}
}

// capturingPrintURL records the URL it was asked to print and, on the first
// call, synchronously drives the real redirect through client, simulating
// an operator who copies the printed link into any browser by hand and
// pastes back the resulting code. Writing the extracted code into buf
// before returning means a subsequent read from buf (the paste input)
// never blocks.
func capturingPrintURL(t *testing.T, buf *pipeBuffer) func(string) {
	t.Helper()
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	return func(authURL string) {
		resp, err := client.Get(authURL)
		if err != nil {
			t.Fatalf("drive printed authorization url: %v", err)
		}
		defer resp.Body.Close()
		loc := resp.Header.Get("Location")
		code := extractQueryParam(loc, "code")
		if code == "" {
			t.Fatalf("printed-url redirect carried no code: %q", loc)
		}
		buf.writeLine(code)
	}
}

func TestLoopbackRedirectFallsBackToPasteFlow(t *testing.T) {
	t.Run("browser cannot be opened", func(t *testing.T) {
		as := oauthtestserver.New()
		defer as.Close()
		buf := newPipeBuffer()
		authz := NewAuthorizer(NewTokenStore(t.TempDir()),
			WithBrowserOpener(oauthtestserver.FailingOpener{}),
			WithPasteInput(buf),
			WithPrintURL(capturingPrintURL(t, buf)),
			WithInteractive(true),
		)
		challenge := &claierr.AuthChallengeError{ServerName: "srv", ResourceMetadata: as.ProtectedResourceURL()}
		dec, err := authz.AuthorizeInteractive(t.Context(), testServer(as.URL), challenge)
		if err != nil {
			t.Fatalf("AuthorizeInteractive: %v", err)
		}
		if dec == nil {
			t.Fatal("expected a decorator from the printed-url path")
		}
	})

	t.Run("loopback listener cannot bind", func(t *testing.T) {
		as := oauthtestserver.New()
		defer as.Close()
		occupying, err := listenLoopback(DefaultLoopbackHost, 0)
		if err != nil {
			t.Fatalf("occupy a loopback port: %v", err)
		}
		defer occupying.Close()
		port := occupying.Addr().(*net.TCPAddr).Port

		buf := newPipeBuffer()
		authz := NewAuthorizer(NewTokenStore(t.TempDir()),
			WithBrowserOpener(oauthtestserver.NewAutoFollowOpener()),
			WithPasteInput(buf),
			WithPrintURL(capturingPrintURL(t, buf)),
			WithLoopback(DefaultLoopbackHost, port),
			WithInteractive(true),
		)
		challenge := &claierr.AuthChallengeError{ServerName: "srv", ResourceMetadata: as.ProtectedResourceURL()}
		dec, err := authz.AuthorizeInteractive(t.Context(), testServer(as.URL), challenge)
		if err != nil {
			t.Fatalf("AuthorizeInteractive: %v", err)
		}
		if dec == nil {
			t.Fatal("expected a decorator from the printed-url path")
		}
	})
}

func TestOauthRefreshBeforeSkew(t *testing.T) {
	as := oauthtestserver.New()
	defer as.Close()
	now := time.Now()
	clock := func() time.Time { return now }
	authz, store := newTestAuthorizer(t, WithClock(clock), WithRefreshSkew(60*time.Second))
	entry := TokenEntry{Issuer: as.IssuerURL(), ClientID: "c1", AccessToken: "stale", RefreshToken: "r1", ExpiresAt: now.Add(30 * time.Second)}
	if err := store.Save("srv", entry); err != nil {
		t.Fatalf("seed store: %v", err)
	}

	dec, _, ok, err := authz.ResolveCached(t.Context(), testServer(as.URL))
	if err != nil {
		t.Fatalf("ResolveCached: %v", err)
	}
	if !ok {
		t.Fatal("expected a cached decorator")
	}
	if got := as.RefreshCount(); got != 1 {
		t.Fatalf("refresh count = %d, want 1", got)
	}
	refreshed, _ := store.Load("srv")
	req, _ := http.NewRequest(http.MethodGet, "http://example.invalid", nil)
	dec(req)
	if got := req.Header.Get("Authorization"); got != "Bearer "+refreshed.AccessToken {
		t.Errorf("decorator used %q, want the refreshed token %q", got, refreshed.AccessToken)
	}
}

// TestOauthRefreshIsSingleFlightPerServer proves the single-flight
// contract deterministically rather than racing the goroutine scheduler
// against a real HTTP round trip: every one of n concurrent callers is
// made to join refresh()'s locked winner-or-follower check (counted by
// testJoined) before the winner is released past testBarrier to do any
// real work, so a second refresh can only happen if the mechanism itself
// is broken, never because of timing.
func TestOauthRefreshIsSingleFlightPerServer(t *testing.T) {
	as := oauthtestserver.New()
	defer as.Close()
	now := time.Now()
	authz, store := newTestAuthorizer(t, WithClock(func() time.Time { return now }), WithRefreshSkew(60*time.Second))
	entry := TokenEntry{Issuer: as.IssuerURL(), ClientID: "c1", AccessToken: "stale", RefreshToken: "r1", ExpiresAt: now.Add(10 * time.Second)}
	if err := store.Save("srv", entry); err != nil {
		t.Fatalf("seed store: %v", err)
	}

	const n = 8
	var joined atomic.Int64
	authz.testJoined = func() { joined.Add(1) }
	barrier := make(chan struct{})
	authz.testBarrier = barrier

	errCh := make(chan error, n)
	for range n {
		go func() {
			_, _, _, err := authz.ResolveCached(t.Context(), testServer(as.URL))
			errCh <- err
		}()
	}
	for joined.Load() < n {
		time.Sleep(time.Millisecond)
	}
	close(barrier)

	for range n {
		if err := <-errCh; err != nil {
			t.Fatalf("ResolveCached: %v", err)
		}
	}
	if got := as.RefreshCount(); got != 1 {
		t.Fatalf("refresh count = %d, want exactly 1 for %d concurrent callers", got, n)
	}
}

func TestOauthRefreshFailureReturnsTypedAuthError(t *testing.T) {
	as := oauthtestserver.New()
	defer as.Close()
	as.Configure(func(c *oauthtestserver.Config) { c.RefreshRejects = true })
	now := time.Now()
	authz, store := newTestAuthorizer(t, WithClock(func() time.Time { return now }), WithRefreshSkew(60*time.Second))
	entry := TokenEntry{Issuer: as.IssuerURL(), ClientID: "c1", AccessToken: "stale", RefreshToken: "r1", ExpiresAt: now.Add(10 * time.Second)}
	if err := store.Save("srv", entry); err != nil {
		t.Fatalf("seed store: %v", err)
	}

	_, _, _, err := authz.ResolveCached(t.Context(), testServer(as.URL))
	var refErr *RefreshError
	if !errors.As(err, &refErr) {
		t.Fatalf("got %v (%T), want *RefreshError", err, err)
	}
}
