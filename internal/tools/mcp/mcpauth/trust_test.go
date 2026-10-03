package mcpauth

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/baalimago/clai/internal/tools/mcp/oauthtestserver"
	"github.com/baalimago/clai/pkg/claierr"
)

// countingTransport answers nothing and only counts: a test proving a trust
// check refused before anything was fetched asserts zero round trips, which
// a fixture cannot show because the refusal means it is never reached.
type countingTransport struct{ roundTrips atomic.Int64 }

func (c *countingTransport) RoundTrip(*http.Request) (*http.Response, error) {
	c.roundTrips.Add(1)
	return nil, errors.New("countingTransport: no request should have been issued")
}

// recordingOpener counts browser hand-offs without performing one, so a
// refusal can be shown to have happened before the URL left the process.
type recordingOpener struct{ opens atomic.Int64 }

func (o *recordingOpener) Open(string) error { o.opens.Add(1); return nil }

// refusingAuthorizer builds an Authorizer whose transport refuses every
// request, for the checks that must fire before the first fetch.
func refusingAuthorizer(t *testing.T) (*Authorizer, *countingTransport, *recordingOpener) {
	t.Helper()
	transport := &countingTransport{}
	opener := &recordingOpener{}
	authz := NewAuthorizer(NewTokenStore(t.TempDir()),
		WithHTTPClient(&http.Client{Transport: transport}),
		WithBrowserOpener(opener),
		WithEnvFileLoader(testEnvFileLoader),
		WithInteractive(true),
	)
	return authz, transport, opener
}

// TestOauthSendsResourceOnAuthorizationAndTokenRequests pins RFC 8707,
// which the MCP authorization specification makes a MUST on the
// authorization request and on every token request: it is what stops a
// token minted for one MCP server being replayed against another behind
// the same authorization server. Before this fix "resource" appeared
// nowhere in the package but a struct tag, and the fixture never asked for
// it.
func TestOauthSendsResourceOnAuthorizationAndTokenRequests(t *testing.T) {
	as := oauthtestserver.New()
	defer as.Close()

	_, _, _, err := fullInteractiveFlow(t, as)
	if err != nil {
		t.Fatalf("AuthorizeInteractive: %v", err)
	}
	if got := as.LastAuthorizeResource(); got != as.ResourceID() {
		t.Errorf("authorization request resource = %q, want %q", got, as.ResourceID())
	}
	if got := as.LastTokenResource(); got != as.ResourceID() {
		t.Errorf("token request resource = %q, want %q", got, as.ResourceID())
	}
}

// TestOauthRefreshGrantSendsResource covers the other token request: the
// refresh grant, whose stored entry is what carries the resource forward.
func TestOauthRefreshGrantSendsResource(t *testing.T) {
	as := oauthtestserver.New()
	defer as.Close()
	now := time.Now()
	authz, store := newTestAuthorizer(t, WithClock(func() time.Time { return now }), WithRefreshSkew(60*time.Second))
	seeded := TokenEntry{
		Issuer:       as.IssuerURL(),
		Resource:     as.ResourceID(),
		ClientID:     "c1",
		AccessToken:  "stale",
		RefreshToken: "r1",
		ExpiresAt:    now.Add(30 * time.Second),
	}
	if err := store.Save("srv", seeded); err != nil {
		t.Fatalf("seed store: %v", err)
	}

	if _, _, ok, err := authz.ResolveCached(t.Context(), testServer(as.URL)); err != nil || !ok {
		t.Fatalf("ResolveCached: ok=%v err=%v", ok, err)
	}
	if got := as.LastTokenResource(); got != as.ResourceID() {
		t.Errorf("refresh request resource = %q, want %q", got, as.ResourceID())
	}
	refreshed, _ := store.Load("srv")
	if refreshed.Resource != as.ResourceID() {
		t.Errorf("stored entry resource = %q, want %q", refreshed.Resource, as.ResourceID())
	}
}

// TestOauthTokenRequestRepeatsRedirectUri pins RFC 6749 section 4.1.3: the
// authorization request carried a redirect_uri, so the token request must
// repeat it. A conformant server answers invalid_grant otherwise, which is
// why its absence strongly suggested the flow had never completed against
// a real authorization server. The fixture now compares the two
// unconditionally, where before it recorded the issued value and never
// read it.
func TestOauthTokenRequestRepeatsRedirectUri(t *testing.T) {
	as := oauthtestserver.New()
	defer as.Close()

	_, _, _, err := fullInteractiveFlow(t, as)
	if err != nil {
		t.Fatalf("AuthorizeInteractive: %v", err)
	}
	authorized := as.LastAuthorizeRedirectURI()
	if authorized == "" {
		t.Fatal("the authorization request carried no redirect_uri")
	}
	if got := as.LastExchangeRedirectURI(); got != authorized {
		t.Errorf("token request redirect_uri = %q, want the authorization request's %q", got, authorized)
	}
}

// TestOauthIssuerMismatchIsRefused pins RFC 8414 section 3.3. Before the
// fix AuthorizationServerMetadata.Issuer had no readers in the tree, so a
// document reachable at a well-known path could name another authorization
// server's endpoints and clai would register a client against them: the
// unfixed code reached registration (observed: one registration) on this
// exact fixture.
func TestOauthIssuerMismatchIsRefused(t *testing.T) {
	as := oauthtestserver.New()
	defer as.Close()
	as.Configure(func(c *oauthtestserver.Config) { c.IssuerOverride = "https://evil.example.invalid" })

	_, store, _, err := fullInteractiveFlow(t, as)
	var mismatch *IssuerMismatchError
	if !errors.As(err, &mismatch) {
		t.Fatalf("got %v (%T), want *IssuerMismatchError", err, err)
	}
	if mismatch.Declared != "https://evil.example.invalid" || mismatch.Requested != as.IssuerURL() {
		t.Errorf("error names requested=%q declared=%q, want %q and the override", mismatch.Requested, mismatch.Declared, as.IssuerURL())
	}
	if as.RegistrationCount() != 0 {
		t.Errorf("registered a client against a mismatched issuer (%d registrations)", as.RegistrationCount())
	}
	if _, ok := store.Load("srv"); ok {
		t.Error("token stored despite a mismatched issuer")
	}
}

// TestOauthResourceMismatchIsRefused pins RFC 9728 section 3.3:
// ProtectedResourceMetadata.Resource had no readers either, so a server
// could point clai at a document describing a different resource and the
// token clai obtained would be minted for that one. The unfixed code
// registered a client here too.
func TestOauthResourceMismatchIsRefused(t *testing.T) {
	as := oauthtestserver.New()
	defer as.Close()
	as.Configure(func(c *oauthtestserver.Config) { c.Resource = "https://other.example.invalid/mcp" })

	_, store, _, err := fullInteractiveFlow(t, as)
	var mismatch *ResourceMismatchError
	if !errors.As(err, &mismatch) {
		t.Fatalf("got %v (%T), want *ResourceMismatchError", err, err)
	}
	if mismatch.Declared != "https://other.example.invalid/mcp" {
		t.Errorf("error names declared=%q, want the override", mismatch.Declared)
	}
	if as.RegistrationCount() != 0 {
		t.Errorf("registered a client against a mismatched resource (%d registrations)", as.RegistrationCount())
	}
	if _, ok := store.Load("srv"); ok {
		t.Error("token stored despite a mismatched resource")
	}
}

// TestOauthAbsentResourceIdentifierIsRefused covers the same check's other
// half: RFC 9728 requires the field, so a document without one cannot be
// checked against anything and is refused rather than waved through.
func TestOauthAbsentResourceIdentifierIsRefused(t *testing.T) {
	as := oauthtestserver.New()
	defer as.Close()
	as.Configure(func(c *oauthtestserver.Config) { c.OmitResource = true })

	_, _, _, err := fullInteractiveFlow(t, as)
	var mismatch *ResourceMismatchError
	if !errors.As(err, &mismatch) {
		t.Fatalf("got %v (%T), want *ResourceMismatchError", err, err)
	}
	if mismatch.Declared != "" {
		t.Errorf("declared = %q, want empty", mismatch.Declared)
	}
}

// TestOauthPlainHttpDiscoveryUrlIsRefused pins the scheme requirement. The
// metadata location is on the server's own host here, so only the scheme
// is under test, and the refusal must happen before the request: the
// transport counts round trips and expects none.
func TestOauthPlainHttpDiscoveryUrlIsRefused(t *testing.T) {
	authz, transport, opener := refusingAuthorizer(t)

	_, err := authz.AuthorizeInteractive(t.Context(), testServer("https://mcp.example.invalid/mcp"),
		&claierr.AuthChallengeError{ServerName: "srv", ResourceMetadata: "http://mcp.example.invalid/.well-known/oauth-protected-resource"})
	var insecure *InsecureURLError
	if !errors.As(err, &insecure) {
		t.Fatalf("got %v (%T), want *InsecureURLError", err, err)
	}
	if insecure.Stage != StageResourceMetadata {
		t.Errorf("stage = %q, want %q", insecure.Stage, StageResourceMetadata)
	}
	if transport.roundTrips.Load() != 0 {
		t.Errorf("issued %d requests to a plain-http discovery URL, want 0", transport.roundTrips.Load())
	}
	if opener.opens.Load() != 0 {
		t.Errorf("opened a browser %d times, want 0", opener.opens.Load())
	}
}

// TestOauthCrossHostResourceMetadataIsRefused pins the host check. The
// challenge is attacker-controlled whenever the 401 is, so a
// resource_metadata location on another host is the whole composed attack's
// entry point: it is what lets an attacker choose the issuer, the
// registration endpoint and the token endpoint.
func TestOauthCrossHostResourceMetadataIsRefused(t *testing.T) {
	authz, transport, _ := refusingAuthorizer(t)

	_, err := authz.AuthorizeInteractive(t.Context(), testServer("https://mcp.example.invalid/mcp"),
		&claierr.AuthChallengeError{ServerName: "srv", ResourceMetadata: "https://evil.example.invalid/.well-known/oauth-protected-resource"})
	var untrusted *UntrustedMetadataHostError
	if !errors.As(err, &untrusted) {
		t.Fatalf("got %v (%T), want *UntrustedMetadataHostError", err, err)
	}
	if transport.roundTrips.Load() != 0 {
		t.Errorf("fetched %d off-host metadata documents, want 0", transport.roundTrips.Load())
	}
}

// TestOauthInsecureAuthorizationEndpointIsNeverOpened pins the scheme
// requirement on the URL handed to a browser: xdg-open on an
// attacker-chosen plain-http endpoint is the step that gets a victim to
// consent to the attacker's own authorization server.
func TestOauthInsecureAuthorizationEndpointIsNeverOpened(t *testing.T) {
	as := oauthtestserver.New()
	defer as.Close()
	as.Configure(func(c *oauthtestserver.Config) {
		c.AuthorizationEndpointOverride = "http://evil.example.invalid/authorize"
	})
	opener := &recordingOpener{}
	authz, _ := newTestAuthorizer(t, WithBrowserOpener(opener))

	_, err := authz.AuthorizeInteractive(t.Context(), testServer(as.URL),
		&claierr.AuthChallengeError{ServerName: "srv", ResourceMetadata: as.ProtectedResourceURL()})
	var insecure *InsecureURLError
	if !errors.As(err, &insecure) {
		t.Fatalf("got %v (%T), want *InsecureURLError", err, err)
	}
	if insecure.Stage != StageAuthorization {
		t.Errorf("stage = %q, want %q", insecure.Stage, StageAuthorization)
	}
	if opener.opens.Load() != 0 {
		t.Errorf("opened a browser %d times on an insecure endpoint, want 0", opener.opens.Load())
	}
	if as.RegistrationCount() != 0 {
		t.Errorf("registered a client before refusing the endpoint (%d registrations)", as.RegistrationCount())
	}
}

// TestOauthInsecureTokenEndpointIsRefused covers the same check on the
// endpoint the credentials are POSTed to.
func TestOauthInsecureTokenEndpointIsRefused(t *testing.T) {
	as := oauthtestserver.New()
	defer as.Close()
	as.Configure(func(c *oauthtestserver.Config) {
		c.TokenEndpointOverride = "http://evil.example.invalid/token"
	})

	_, _, _, err := fullInteractiveFlow(t, as)
	var insecure *InsecureURLError
	if !errors.As(err, &insecure) {
		t.Fatalf("got %v (%T), want *InsecureURLError", err, err)
	}
	if insecure.Stage != StageToken {
		t.Errorf("stage = %q, want %q", insecure.Stage, StageToken)
	}
}

// credentialSink records whether a request body reaching it carried a
// credential, which is what makes a followed redirect on a token or
// registration endpoint a leak rather than an inconvenience.
type credentialSink struct {
	srv      *httptest.Server
	hits     atomic.Int64
	leaked   atomic.Bool
	leakedAs atomic.Value
}

func newCredentialSink(t *testing.T) *credentialSink {
	t.Helper()
	sink := &credentialSink{}
	sink.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sink.hits.Add(1)
		if err := r.ParseForm(); err == nil {
			for _, name := range []string{"code_verifier", "refresh_token", "client_secret"} {
				if r.Form.Get(name) != "" {
					sink.leaked.Store(true)
					sink.leakedAs.Store(name)
				}
			}
		}
		w.WriteHeader(http.StatusBadRequest)
	}))
	t.Cleanup(sink.srv.Close)
	return sink
}

// TestOauthRedirectingTokenEndpointIsRefused pins the CheckRedirect policy.
// Go re-sends a POST body across a 307 or 308 and only ever strips the
// Authorization header, never the body; against the unfixed code this exact
// fixture delivered the PKCE verifier to the redirect target (observed:
// one hit, verifier present).
func TestOauthRedirectingTokenEndpointIsRefused(t *testing.T) {
	sink := newCredentialSink(t)
	as := oauthtestserver.New()
	defer as.Close()
	as.Configure(func(c *oauthtestserver.Config) { c.TokenRedirectTo = sink.srv.URL })

	_, store, _, err := fullInteractiveFlow(t, as)
	var refused *RedirectRefusedError
	if !errors.As(err, &refused) {
		t.Fatalf("got %v (%T), want it to unwrap to *RedirectRefusedError", err, err)
	}
	var rejected *ExchangeRejectedError
	if !errors.As(err, &rejected) {
		t.Errorf("got %v, want the refusal wrapped in *ExchangeRejectedError", err)
	}
	if sink.hits.Load() != 0 {
		t.Errorf("the redirect target was reached %d times, want 0 (leaked %v)", sink.hits.Load(), sink.leakedAs.Load())
	}
	if sink.leaked.Load() {
		t.Errorf("a credential (%v) was resent to the redirect target", sink.leakedAs.Load())
	}
	if _, ok := store.Load("srv"); ok {
		t.Error("token stored despite a refused exchange")
	}
}

// TestOauthRedirectingRegistrationEndpointIsRefused covers the other POST
// endpoint named by the review.
func TestOauthRedirectingRegistrationEndpointIsRefused(t *testing.T) {
	sink := newCredentialSink(t)
	as := oauthtestserver.New()
	defer as.Close()
	as.Configure(func(c *oauthtestserver.Config) { c.RegistrationRedirectTo = sink.srv.URL })

	_, _, _, err := fullInteractiveFlow(t, as)
	var refused *RedirectRefusedError
	if !errors.As(err, &refused) {
		t.Fatalf("got %v (%T), want it to unwrap to *RedirectRefusedError", err, err)
	}
	var rejected *RegistrationRejectedError
	if !errors.As(err, &rejected) {
		t.Errorf("got %v, want the refusal wrapped in *RegistrationRejectedError", err)
	}
	if sink.hits.Load() != 0 {
		t.Errorf("the redirect target was reached %d times, want 0", sink.hits.Load())
	}
}

// TestOauthRedirectingRefreshEndpointIsRefused covers the refresh grant's
// own token POST, whose body carries the refresh token itself.
func TestOauthRedirectingRefreshEndpointIsRefused(t *testing.T) {
	sink := newCredentialSink(t)
	as := oauthtestserver.New()
	defer as.Close()
	as.Configure(func(c *oauthtestserver.Config) { c.TokenRedirectTo = sink.srv.URL })

	now := time.Now()
	authz, store := newTestAuthorizer(t, WithClock(func() time.Time { return now }))
	if err := store.Save("srv", TokenEntry{
		Issuer: as.IssuerURL(), Resource: as.ResourceID(), ClientID: "c1",
		AccessToken: "stale", RefreshToken: "r1", ExpiresAt: now.Add(time.Second),
	}); err != nil {
		t.Fatalf("seed store: %v", err)
	}

	_, _, ok, err := authz.ResolveCached(t.Context(), testServer(as.URL))
	if ok {
		t.Error("ResolveCached reported a usable credential from a refused refresh")
	}
	var refused *RedirectRefusedError
	if !errors.As(err, &refused) {
		t.Fatalf("got %v (%T), want it to unwrap to *RedirectRefusedError", err, err)
	}
	if sink.hits.Load() != 0 || sink.leaked.Load() {
		t.Errorf("the redirect target was reached %d times (leaked %v)", sink.hits.Load(), sink.leakedAs.Load())
	}
}

// TestOauthNonInteractiveRunIsRefused pins the gate at its root. Against
// the unfixed code this test does not fail, it hangs: a headless run bound
// a loopback listener in its own process and blocked on a redirect that
// nothing was ever going to drive (observed: the probe ran out the full
// 60s test bound inside awaitRedirect).
func TestOauthNonInteractiveRunIsRefused(t *testing.T) {
	as := oauthtestserver.New()
	defer as.Close()
	opener := &recordingOpener{}
	authz := NewAuthorizer(NewTokenStore(t.TempDir()),
		WithBrowserOpener(opener),
		WithEnvFileLoader(testEnvFileLoader),
	)
	if authz.Interactive {
		t.Fatal("an Authorizer built without WithInteractive reports itself interactive")
	}

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	_, err := authz.AuthorizeInteractive(ctx, testServer(as.URL),
		&claierr.AuthChallengeError{ServerName: "srv", ResourceMetadata: as.ProtectedResourceURL()})
	var disabled *InteractiveAuthDisabledError
	if !errors.As(err, &disabled) {
		t.Fatalf("got %v (%T), want *InteractiveAuthDisabledError", err, err)
	}
	if opener.opens.Load() != 0 {
		t.Errorf("opened a browser %d times on a headless run, want 0", opener.opens.Load())
	}
	if as.RequestCount() != 0 {
		t.Errorf("issued %d requests on a headless run, want 0", as.RequestCount())
	}
}

func TestRequireSecureURLAcceptsLoopbackAndHttps(t *testing.T) {
	for _, accepted := range []string{
		"https://mcp.example.invalid/mcp",
		"http://127.0.0.1:8080/mcp",
		"http://localhost:8080/mcp",
		"http://[::1]:8080/mcp",
	} {
		if err := requireSecureURL("srv", StageToken, accepted); err != nil {
			t.Errorf("requireSecureURL(%q) = %v, want nil", accepted, err)
		}
	}
	for _, refused := range []string{
		"http://10.0.0.1/mcp",
		"http://evil.example.invalid/mcp",
		"ftp://mcp.example.invalid/mcp",
		"/relative/path",
		"",
	} {
		var insecure *InsecureURLError
		if err := requireSecureURL("srv", StageToken, refused); !errors.As(err, &insecure) {
			t.Errorf("requireSecureURL(%q) = %v, want *InsecureURLError", refused, err)
		}
	}
}

func TestRequireResourceCoversOriginAndPath(t *testing.T) {
	cases := []struct {
		declared string
		endpoint string
		want     bool
	}{
		{"https://mcp.example.invalid", "https://mcp.example.invalid/mcp", true},
		{"https://mcp.example.invalid/mcp", "https://mcp.example.invalid/mcp", true},
		{"https://mcp.example.invalid/mcp/", "https://mcp.example.invalid/mcp/v1", true},
		{"https://mcp.example.invalid:443", "https://mcp.example.invalid/mcp", true},
		{"https://mcp.example.invalid/other", "https://mcp.example.invalid/mcp", false},
		{"https://mcp.example.invalid/mcp2", "https://mcp.example.invalid/mcp", false},
		{"https://evil.example.invalid", "https://mcp.example.invalid/mcp", false},
		{"http://mcp.example.invalid", "https://mcp.example.invalid/mcp", false},
		{"https://mcp.example.invalid:8443", "https://mcp.example.invalid/mcp", false},
		{"", "https://mcp.example.invalid/mcp", false},
	}
	for _, c := range cases {
		err := requireResourceCovers("srv", c.declared, c.endpoint)
		if (err == nil) != c.want {
			t.Errorf("requireResourceCovers(%q, %q) = %v, want covered=%v", c.declared, c.endpoint, err, c.want)
		}
	}
}

func TestRequireIssuerMatchToleratesOnlyATrailingSlash(t *testing.T) {
	if err := requireIssuerMatch("srv", "https://as.example.invalid/", "https://as.example.invalid"); err != nil {
		t.Errorf("trailing-slash difference refused: %v", err)
	}
	var mismatch *IssuerMismatchError
	if err := requireIssuerMatch("srv", "https://as.example.invalid", "https://as.example.invalid/tenant"); !errors.As(err, &mismatch) {
		t.Errorf("got %v, want *IssuerMismatchError for a different path", err)
	}
	if err := requireIssuerMatch("srv", "https://as.example.invalid", ""); !errors.As(err, &mismatch) {
		t.Errorf("got %v, want *IssuerMismatchError for an absent issuer", err)
	}
}

func TestSecureHopRedirectBoundsAndChecksEveryHop(t *testing.T) {
	req, err := http.NewRequest(http.MethodGet, "https://as.example.invalid/next", nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	if err := secureHopRedirect(req, nil); err != nil {
		t.Errorf("first secure hop refused: %v", err)
	}
	if err := secureHopRedirect(req, make([]*http.Request, maxDiscoveryRedirects)); err == nil {
		t.Error("an unbounded redirect chain was accepted")
	}

	insecureReq, err := http.NewRequest(http.MethodGet, "http://evil.example.invalid/next", nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	var insecure *InsecureURLError
	if err := secureHopRedirect(insecureReq, nil); !errors.As(err, &insecure) {
		t.Errorf("got %v, want *InsecureURLError for a plain-http hop", err)
	}
}

// TestOauthPlainHttpServerEndpointIsRefused covers the endpoint the minted
// token is actually sent to: clai will not run a flow for a non-loopback
// plain-http MCP endpoint, because the access token it obtains would travel
// in clear. Config parsing still admits such an endpoint, which a static
// credential against a LAN server legitimately needs, so this refusal is
// scoped to the credential clai mints itself.
func TestOauthPlainHttpServerEndpointIsRefused(t *testing.T) {
	authz, transport, opener := refusingAuthorizer(t)

	_, err := authz.AuthorizeInteractive(t.Context(), testServer("http://mcp.example.invalid/mcp"),
		&claierr.AuthChallengeError{ServerName: "srv", ResourceMetadata: "https://mcp.example.invalid/.well-known/oauth-protected-resource"})
	var insecure *InsecureURLError
	if !errors.As(err, &insecure) {
		t.Fatalf("got %v (%T), want *InsecureURLError", err, err)
	}
	if insecure.Stage != StageServerEndpoint {
		t.Errorf("stage = %q, want %q", insecure.Stage, StageServerEndpoint)
	}
	if transport.roundTrips.Load() != 0 || opener.opens.Load() != 0 {
		t.Errorf("reached the network (%d) or a browser (%d), want neither", transport.roundTrips.Load(), opener.opens.Load())
	}
}
