// Package mcpauth is clai's OAuth 2.1 client for an endpoint-based MCP
// server: discovery, dynamic client registration, PKCE, the authorization
// code grant and refresh, a token store, and the credential-precedence
// chain ahead of all of it (worklog 2026-10-02-mcp-connection-cost, phase
// 5). The whole client is built from the standard library; no OAuth
// package is added (README invariant 4).
package mcpauth

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/baalimago/clai/pkg/claierr"
	pub_models "github.com/baalimago/clai/pkg/text/models"
)

// DefaultRefreshSkew is the refresh-skew parameter: an access token nearing
// expiry by less than this margin is refreshed before use.
const DefaultRefreshSkew = 60 * time.Second

// DefaultLoopbackHost is the loopback-host parameter.
const DefaultLoopbackHost = "127.0.0.1"

// Authorizer resolves a RequestDecorator for an endpoint-based MCP server,
// trying the credential-precedence chain (credential command, environment
// variable, token store, interactive flow) before any connection is made,
// and the interactive flow itself when a bare connect attempt is
// challenged. Construct with NewAuthorizer; the zero value is not usable.
type Authorizer struct {
	store      *TokenStore
	httpclient *http.Client
	// discoveryclient and strictclient are httpclient with a redirect
	// policy installed: a discovery GET carries no credential and may
	// follow a bounded, still-secure redirect, while a registration or
	// token POST must never follow one, because Go re-sends the body.
	discoveryclient *http.Client
	strictclient    *http.Client
	clock           func() time.Time
	opener          BrowserOpener
	printURLFn      func(string)
	loadEnvFile     EnvFileLoader

	// PasteInput is the trusted input reader the repository already
	// threads through setup, used to read a pasted authorization code when
	// the loopback redirect cannot complete.
	PasteInput io.Reader
	// Interactive reports whether this run may fall back to re-authorizing
	// after a rejected refresh (error coverage: "interactive run may
	// re-authorize, headless run returns").
	Interactive bool

	loopbackHostV string
	loopbackPortV int
	refreshSkew   time.Duration

	refreshMu sync.Mutex
	inflight  map[string]*refreshCall

	// testJoined and testBarrier give TestOauthRefreshIsSingleFlightPerServer
	// a deterministic join point instead of racing the goroutine scheduler
	// against a real HTTP round trip. Both nil in production; set directly
	// on the struct by that test, in this package.
	testJoined  func()
	testBarrier chan struct{}
}

type refreshCall struct {
	done  chan struct{}
	entry TokenEntry
	err   error
}

// Option configures an Authorizer at construction.
type Option func(*Authorizer)

// WithHTTPClient overrides the http.Client used for every discovery,
// registration and token request.
func WithHTTPClient(c *http.Client) Option { return func(a *Authorizer) { a.httpclient = c } }

// WithClock overrides the clock used to judge refresh-skew and to stamp a
// stored entry's expiry. Production code never needs this; a test that
// cares injects one instead of sleeping.
func WithClock(clock func() time.Time) Option { return func(a *Authorizer) { a.clock = clock } }

// WithBrowserOpener overrides the production browser opener.
func WithBrowserOpener(o BrowserOpener) Option { return func(a *Authorizer) { a.opener = o } }

// WithPasteInput sets the reader the printed-URL fallback reads a pasted
// authorization code from.
func WithPasteInput(r io.Reader) Option {
	return func(a *Authorizer) { a.PasteInput = r }
}

// WithPrintURL overrides how the printed-URL fallback presents the
// authorization URL to the operator. Default prints to standard output.
func WithPrintURL(fn func(string)) Option { return func(a *Authorizer) { a.printURLFn = fn } }

// WithLoopback overrides the loopback-host and loopback-port parameters.
func WithLoopback(host string, port int) Option {
	return func(a *Authorizer) { a.loopbackHostV = host; a.loopbackPortV = port }
}

// WithRefreshSkew overrides the refresh-skew parameter.
func WithRefreshSkew(d time.Duration) Option { return func(a *Authorizer) { a.refreshSkew = d } }

// WithInteractive sets whether this run may re-authorize after a rejected
// refresh.
func WithInteractive(interactive bool) Option {
	return func(a *Authorizer) { a.Interactive = interactive }
}

// WithEnvFileLoader installs the loader auth.token_env's envfile fallback
// uses. Production callers pass mcp.LoadEnvFile; this package cannot import
// it directly (see EnvFileLoader's doc). Omitted, the envfile fallback is
// unavailable and the process environment is the only place token_env is
// resolved from.
func WithEnvFileLoader(loader EnvFileLoader) Option {
	return func(a *Authorizer) { a.loadEnvFile = loader }
}

// NewAuthorizer builds an Authorizer backed by store. store is required.
func NewAuthorizer(store *TokenStore, opts ...Option) *Authorizer {
	a := &Authorizer{
		store:         store,
		httpclient:    &http.Client{},
		clock:         time.Now,
		opener:        DefaultBrowserOpener(),
		loopbackHostV: DefaultLoopbackHost,
		refreshSkew:   DefaultRefreshSkew,
		inflight:      make(map[string]*refreshCall),
	}
	for _, opt := range opts {
		opt(a)
	}
	a.discoveryclient = withRedirectPolicy(a.httpclient, secureHopRedirect)
	a.strictclient = withRedirectPolicy(a.httpclient, refuseRedirect)
	return a
}

func (a *Authorizer) httpClient() *http.Client { return a.discoveryclient }

// strictHTTPClient is the client every registration and token request uses:
// it refuses redirects outright.
func (a *Authorizer) strictHTTPClient() *http.Client { return a.strictclient }

func (a *Authorizer) browserOpener() BrowserOpener { return a.opener }

func (a *Authorizer) loopbackHost() string { return a.loopbackHostV }

func (a *Authorizer) loopbackPort() int { return a.loopbackPortV }

func (a *Authorizer) printURL() func(string) {
	if a.printURLFn != nil {
		return a.printURLFn
	}
	return defaultPrintURL()
}

// bearerDecorator attaches token as a bearer Authorization header to every
// outgoing request.
func bearerDecorator(token string) func(*http.Request) {
	return func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+token) }
}

// ResolveCached tries every credential source ahead of the interactive
// flow: auth.token_command, then auth.token_env, then the token store
// (refreshing it first if it is inside the refresh margin). ok is false
// with a nil err when every source is unset or missed, so the caller knows
// to attempt a bare connect and drive the interactive flow from whatever
// challenge it produces. A configured source that fails is returned with
// no fall-through (D15); the token store is a cache, so its own absence or
// corruption is silently treated as a miss here, exactly as Load defines.
func (a *Authorizer) ResolveCached(ctx context.Context, server pub_models.McpServer) (decorator func(*http.Request), source CredentialSource, ok bool, err error) {
	token, src, staticOK, staticErr := ResolveStaticCredential(ctx, server, a.loadEnvFile)
	if staticErr != nil {
		return nil, src, false, staticErr
	}
	if staticOK {
		return bearerDecorator(token), src, true, nil
	}

	entry, hit := a.store.Load(server.Name)
	if !hit {
		return nil, "", false, nil
	}
	if a.needsRefresh(entry) {
		refreshed, refreshErr := a.refresh(ctx, server, entry)
		if refreshErr != nil {
			var writeErr *TokenStoreWriteError
			if errors.As(refreshErr, &writeErr) && refreshed.AccessToken != "" {
				// R1-09: the refresh succeeded; only the store write
				// failed. The run continues with the token already held
				// in memory (error-coverage row), with the write failure
				// still returned so the caller can surface the degraded
				// state rather than it being swallowed.
				return bearerDecorator(refreshed.AccessToken), SourceTokenStore, true, writeErr
			}
			return nil, SourceTokenStore, false, refreshErr
		}
		entry = refreshed
	}
	return bearerDecorator(entry.AccessToken), SourceTokenStore, true, nil
}

func (a *Authorizer) needsRefresh(entry TokenEntry) bool {
	if entry.ExpiresAt.IsZero() {
		return false
	}
	return a.clock().Add(a.refreshSkew).After(entry.ExpiresAt)
}

// AuthorizeInteractive runs the full discovery-through-exchange flow driven
// by challenge, stores the resulting tokens, and returns a decorator
// attaching the new access token. Only the challenge's ResourceMetadata is
// read from it, and nothing is parsed out of a response body or guessed
// from the endpoint URL — but nothing on the chain is trusted either: the
// metadata location must be on the server's own host, the document's
// resource identifier must cover the server, and the authorization server's
// own metadata must declare the issuer it was fetched for.
//
// A run that may not open a browser refuses here rather than in each
// caller: the loopback listener and the browser hand-off are process-wide
// effects a library consumer must never observe (sign-off review, B2; the
// setup path's own gate was R2-08).
func (a *Authorizer) AuthorizeInteractive(ctx context.Context, server pub_models.McpServer, challenge *claierr.AuthChallengeError) (func(*http.Request), error) {
	if !a.Interactive {
		return nil, &InteractiveAuthDisabledError{ServerName: server.Name}
	}
	if challenge == nil || challenge.ResourceMetadata == "" {
		return nil, &ChallengeMissingResourceMetadataError{ServerName: server.Name}
	}
	// The access token this flow mints is only ever sent to server.Url, so
	// clai will not mint one for an endpoint that would carry it in clear.
	// Config parsing still admits a plain-http endpoint, which a run using
	// a static credential for a LAN server legitimately needs.
	if err := requireSecureURL(server.Name, StageServerEndpoint, server.Url); err != nil {
		return nil, err
	}
	if err := requireMetadataFromServerHost(server.Name, challenge.ResourceMetadata, server.Url); err != nil {
		return nil, err
	}
	prm, err := discoverProtectedResource(ctx, a.httpClient(), server.Name, challenge.ResourceMetadata)
	if err != nil {
		return nil, err
	}
	if err := requireResourceCovers(server.Name, prm.Resource, server.Url); err != nil {
		return nil, err
	}
	if len(prm.AuthorizationServers) == 0 {
		return nil, &DiscoveryError{ServerName: server.Name, Stage: "protected-resource", Cause: fmt.Errorf("document carries no authorization_servers")}
	}
	issuer := prm.AuthorizationServers[0]
	asMeta, err := discoverAuthorizationServer(ctx, a.httpClient(), server.Name, issuer)
	if err != nil {
		return nil, err
	}

	scopes := requestedScopes(server, prm)
	flow, err := a.authorizeViaFlow(ctx, server, asMeta, scopes, prm.Resource)
	if err != nil {
		return nil, err
	}
	if flow.code == "" {
		return nil, &RedirectError{ServerName: server.Name, Reason: "no authorization code returned"}
	}

	entry, err := a.exchangeCode(ctx, server, codeExchange{
		asMeta:      asMeta,
		reg:         flow.reg,
		code:        flow.code,
		verifier:    flow.verifier,
		redirectURI: flow.redirectURI,
		issuer:      issuer,
		resource:    prm.Resource,
		scopes:      scopes,
	})
	if err != nil {
		return nil, err
	}
	if saveErr := a.store.Save(server.Name, entry); saveErr != nil {
		// R1-09: the exchange itself succeeded, so entry.AccessToken is
		// valid; only persisting it failed. The decorator is still
		// returned so the caller can proceed with the token already held
		// in memory, with the write failure surfaced rather than
		// discarded.
		return bearerDecorator(entry.AccessToken), saveErr
	}
	return bearerDecorator(entry.AccessToken), nil
}

// requestedScopes is the auth.scopes parameter when set, else whatever the
// protected-resource document advertises (the parameter's documented
// default).
func requestedScopes(server pub_models.McpServer, prm ProtectedResourceMetadata) []string {
	if server.Auth != nil && len(server.Auth.Scopes) > 0 {
		return server.Auth.Scopes
	}
	return prm.ScopesSupported
}

// authorizationFlow is what one completed authorization request yields.
// redirectURI is carried out of here because RFC 6749 section 4.1.3 makes
// the token request repeat the value the authorization request used, and
// the two paths below settle on different ones.
type authorizationFlow struct {
	code        string
	verifier    string
	redirectURI string
	reg         ClientRegistration
}

// authorizeViaFlow decides between the loopback redirect and the
// printed-URL fallback, registers a client against the redirect URI that
// decision settles on, and returns the resulting code, PKCE verifier and
// that redirect URI.
func (a *Authorizer) authorizeViaFlow(ctx context.Context, server pub_models.McpServer, asMeta AuthorizationServerMetadata, scopes []string, resource string) (authorizationFlow, error) {
	scopeStr := strings.Join(scopes, " ")

	ln, bindErr := listenLoopback(a.loopbackHost(), a.loopbackPort())
	if bindErr == nil {
		redirectURI := fmt.Sprintf("http://%s/callback", ln.Addr().String())
		authURL, verifier2, state2, reg2, prepErr := a.prepareAuthorization(ctx, server, asMeta, scopeStr, redirectURI, resource)
		if prepErr == nil {
			if openErr := a.browserOpener().Open(authURL); openErr == nil {
				res, waitErr := awaitRedirect(ctx, ln)
				ln.Close()
				if waitErr != nil {
					return authorizationFlow{}, fmt.Errorf("mcpauth: await loopback redirect: %w", waitErr)
				}
				if res.err != "" {
					return authorizationFlow{}, &RedirectError{ServerName: server.Name, Reason: res.err}
				}
				// R1-11: state was minted, sent and captured but never
				// read, so the CSRF defence the code claimed to implement
				// was absent in fact. PKCE still mitigates authorization-
				// code injection (an injected code was issued against a
				// different code_challenge, so the exchange below would
				// fail regardless), but a captured-then-discarded value is
				// a defence that exists only in name.
				if res.state != state2 {
					return authorizationFlow{}, &RedirectError{ServerName: server.Name, Reason: "state parameter mismatch"}
				}
				return authorizationFlow{code: res.code, verifier: verifier2, redirectURI: redirectURI, reg: reg2}, nil
			}
		}
		ln.Close()
	}

	// Printed-URL fallback: the loopback listener could not bind, or the
	// browser could not be opened. A fresh registration against the
	// out-of-band redirect URI, since the loopback one is no longer
	// reachable by anything. No state check here: the operator pastes the
	// code directly, so there is no redirect request for a third party to
	// forge.
	const oobRedirectURI = "urn:ietf:wg:oauth:2.0:oob"
	authURL, verifier2, _, reg2, prepErr := a.prepareAuthorization(ctx, server, asMeta, scopeStr, oobRedirectURI, resource)
	if prepErr != nil {
		return authorizationFlow{}, prepErr
	}
	a.printURL()(authURL)
	line, readErr := readPastedLine(a.PasteInput)
	if readErr != nil {
		return authorizationFlow{}, fmt.Errorf("mcpauth: read pasted authorization code: %w", readErr)
	}
	return authorizationFlow{code: line, verifier: verifier2, redirectURI: oobRedirectURI, reg: reg2}, nil
}

// prepareAuthorization registers a dynamic client against redirectURI and
// builds the authorization URL, generating a fresh PKCE verifier/challenge
// and state for this attempt. state is returned so the loopback caller can
// compare it against what the redirect actually carries back (R1-11); the
// value only has anything to compare against on the loopback path.
func (a *Authorizer) prepareAuthorization(ctx context.Context, server pub_models.McpServer, asMeta AuthorizationServerMetadata, scopeStr, redirectURI, resource string) (authURL, verifier, state string, reg ClientRegistration, err error) {
	reg, err = registerDynamicClient(ctx, a.strictHTTPClient(), server.Name, asMeta.RegistrationEndpoint, redirectURI)
	if err != nil {
		return "", "", "", ClientRegistration{}, err
	}
	verifier, challenge, err := newPKCE()
	if err != nil {
		return "", "", "", ClientRegistration{}, err
	}
	state, err = newState()
	if err != nil {
		return "", "", "", ClientRegistration{}, err
	}
	v := url.Values{}
	v.Set("response_type", "code")
	v.Set("client_id", reg.ClientID)
	if scopeStr != "" {
		v.Set("scope", scopeStr)
	}
	v.Set("state", state)
	v.Set("code_challenge", challenge)
	v.Set("code_challenge_method", CodeChallengeMethod)
	v.Set("redirect_uri", redirectURI)
	v.Set("resource", resource)
	authURL = asMeta.AuthorizationEndpoint + "?" + v.Encode()
	if err := requireSecureURL(server.Name, StageAuthorizationURL, authURL); err != nil {
		return "", "", "", ClientRegistration{}, err
	}
	return authURL, verifier, state, reg, nil
}

// codeExchange is everything one authorization-code grant needs, bundled
// rather than passed as eight positional strings.
type codeExchange struct {
	asMeta      AuthorizationServerMetadata
	reg         ClientRegistration
	code        string
	verifier    string
	redirectURI string
	issuer      string
	resource    string
	scopes      []string
}

// exchangeCode performs the authorization-code grant (RFC 6749 section
// 4.1.3) and builds the token store entry. redirect_uri is repeated here
// because the authorization request carried one, which that section makes a
// MUST; resource is RFC 8707's, which the MCP authorization specification
// makes a MUST on every token request. Nothing is written to the store when
// this fails.
func (a *Authorizer) exchangeCode(ctx context.Context, server pub_models.McpServer, ex codeExchange) (TokenEntry, error) {
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", ex.code)
	form.Set("code_verifier", ex.verifier)
	form.Set("client_id", ex.reg.ClientID)
	form.Set("redirect_uri", ex.redirectURI)
	form.Set("resource", ex.resource)
	if ex.reg.ClientSecret != "" {
		form.Set("client_secret", ex.reg.ClientSecret)
	}

	tok, err := postTokenRequest(ctx, a.strictHTTPClient(), ex.asMeta.TokenEndpoint, form)
	if err != nil {
		return TokenEntry{}, &ExchangeRejectedError{ServerName: server.Name, Reason: err.Error(), Cause: err}
	}
	return TokenEntry{
		Issuer:       ex.issuer,
		Resource:     ex.resource,
		ClientID:     ex.reg.ClientID,
		ClientSecret: ex.reg.ClientSecret,
		AccessToken:  tok.AccessToken,
		RefreshToken: tok.RefreshToken,
		ExpiresAt:    a.clock().Add(time.Duration(tok.ExpiresIn) * time.Second),
		Scopes:       ex.scopes,
	}, nil
}

// readPastedLine reads one line from r without pulling in a TTY fallback:
// the authorization phase never reads the process standard input directly.
func readPastedLine(r io.Reader) (string, error) {
	if r == nil {
		return "", fmt.Errorf("mcpauth: no paste input reader configured")
	}
	var buf []byte
	b := make([]byte, 1)
	for {
		n, err := r.Read(b)
		if n > 0 {
			if b[0] == '\n' {
				break
			}
			buf = append(buf, b[0])
		}
		if err != nil {
			if len(buf) > 0 {
				break
			}
			return "", err
		}
	}
	return strings.TrimSpace(string(buf)), nil
}
