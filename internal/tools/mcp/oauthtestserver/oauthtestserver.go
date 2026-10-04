// Package oauthtestserver is the fake OAuth 2.1 authorization server the
// authorization phase introduces (worklog 2026-10-02-mcp-connection-cost,
// phase 5, README shared interfaces: "Phase 5 introduces the fake OAuth
// authorization server only"). It also serves the RFC 9728 protected-resource
// metadata document, since that document is part of the discovery chain
// rather than part of the MCP server: the transport phase's
// httptestserver.Server is configured to point its challenge's
// resource_metadata at this fixture, so the two fakes compose into one
// discovery chain with no second MCP-over-HTTP fake introduced here.
//
// It is configurable per test to omit any required authorization-server
// metadata field, to serve that metadata only at the RFC 8414 inserted
// well-known form, only at the OIDC-style appended form, at both, or at
// neither, to reject registration, to reject a code exchange, to reject a
// refresh grant, to answer an authorization request with an OAuth error
// instead of a code, and to count refresh requests.
//
// Its zero configuration is deliberately hostile (sign-off review, B2): it
// requires RFC 8707 `resource` on the authorization request and on every
// token request, and it compares the authorization-code token request's
// `redirect_uri` against the one the code was issued against (RFC 6749
// section 4.1.3), so a client that omits either is rejected rather than
// silently accepted. It can additionally declare a mismatched `issuer`, a
// mismatched or absent `resource`, an arbitrary (including plain-http,
// off-host) endpoint URL, and a redirecting token or registration
// endpoint, so each trust check clai makes has a case that fails without
// it.
package oauthtestserver

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"sync/atomic"
)

const (
	protectedResourcePath  = "/.well-known/oauth-protected-resource"
	wellKnownAuthServerSeg = "/.well-known/oauth-authorization-server"
	registerPath           = "/register"
	authorizePath          = "/authorize"
	tokenPath              = "/token"
)

// MetadataAvailability selects where the authorization-server metadata
// document answers, driving the RFC 8414 inserted-form-first,
// appended-form-fallback composition rule's three outcomes.
type MetadataAvailability string

const (
	MetadataAtInserted MetadataAvailability = "inserted"
	MetadataAtAppended MetadataAvailability = "appended"
	MetadataAtBoth     MetadataAvailability = "both"
	MetadataAtNeither  MetadataAvailability = "neither"
)

// Config is the fixture's mutable behaviour, read and written only through
// Server.Configure and Server.snapshot, both under Server's mutex.
type Config struct {
	// IssuerPath is appended to the server's own base URL to form the
	// issuer the protected-resource document names. A non-empty path is
	// what makes the inserted and appended well-known forms differ (RFC
	// 8414 vs OIDC-style), exercising the composition rule.
	IssuerPath string
	// MetadataAt selects where the authorization-server metadata document
	// answers. Empty behaves as MetadataAtBoth.
	MetadataAt MetadataAvailability
	// OmitMetadataField, when non-empty, drops that field from an
	// otherwise-complete authorization-server metadata document: one of
	// "registration_endpoint", "authorization_endpoint", "token_endpoint",
	// "code_challenge_methods_supported", "grant_types_supported".
	OmitMetadataField string
	// ScopesSupported is the protected-resource document's scopes_supported.
	ScopesSupported []string

	IssueClientSecret   bool
	RegistrationRejects bool
	RegistrationReason  string

	// RedirectError, when set, makes the authorize endpoint answer with this
	// OAuth error instead of issuing a code, simulating a denied consent or
	// a redirect carrying an error.
	RedirectError string

	ExchangeRejects bool
	ExchangeReason  string
	RefreshRejects  bool
	RefreshReason   string

	// FixedAccessToken, when set, is returned as every issued access token
	// instead of a random one, so a caller's resource-server fixture
	// (httptestserver's RequireBearerToken) can be pre-configured to
	// require exactly the token this fixture is about to issue.
	FixedAccessToken string

	// Resource overrides the protected-resource document's `resource`
	// identifier. Empty declares this fixture's own base URL. A composed
	// chain, whose MCP resource server is a separate fixture on another
	// port, sets this to that server's URL, which is what RFC 9728 makes
	// the resource identifier.
	Resource string
	// OmitResource drops `resource` from the protected-resource document
	// entirely, although RFC 9728 requires it.
	OmitResource bool
	// IssuerOverride replaces the authorization-server document's `issuer`
	// with a value that is not the issuer the document was fetched from,
	// which RFC 8414 section 3.3 forbids.
	IssuerOverride string
	// RegistrationEndpointOverride, AuthorizationEndpointOverride and
	// TokenEndpointOverride replace an advertised endpoint with an
	// arbitrary URL, including an off-host plain-http one.
	RegistrationEndpointOverride  string
	AuthorizationEndpointOverride string
	TokenEndpointOverride         string
	// RegistrationRedirectTo and TokenRedirectTo answer that endpoint with
	// a 308 or 307 to this URL instead of handling the request, the shape
	// that makes Go resend a POST body (and so the PKCE verifier, the
	// refresh token or the client secret) to the target.
	RegistrationRedirectTo string
	TokenRedirectTo        string
	// AllowMissingResourceParam relaxes this fixture's RFC 8707
	// requirement that the authorization request and every token request
	// carry a `resource` matching the declared resource identifier. The
	// zero value requires it.
	AllowMissingResourceParam bool
}

type issuedCode struct {
	challenge   string
	method      string
	redirectURI string
}

type issuedToken struct {
	refreshToken string
}

// Server is the fixture.
type Server struct {
	URL string
	srv *httptest.Server

	mu  sync.Mutex
	cfg Config

	codesMu sync.Mutex
	codes   map[string]issuedCode
	tokens  map[string]issuedToken

	obsMu                    sync.Mutex
	lastCodeChallenge        string
	lastCodeChallengeMeth    string
	lastRegistrationBody     string
	lastAuthorizeResource    string
	lastAuthorizeRedirectURI string
	lastTokenResource        string
	lastExchangeRedirectURI  string

	refreshCount      atomic.Int64
	registrationCount atomic.Int64
	requestCount      atomic.Int64
}

// New builds and starts a Server with a default configuration: no issuer
// path (inserted and appended forms coincide), metadata served at both
// forms, no client secret issued, nothing rejected.
func New() *Server {
	s := &Server{
		cfg:    Config{MetadataAt: MetadataAtBoth, ScopesSupported: []string{"read", "write"}},
		codes:  make(map[string]issuedCode),
		tokens: make(map[string]issuedToken),
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handle)
	s.srv = httptest.NewServer(mux)
	s.URL = s.srv.URL
	return s
}

// Close shuts the fixture down.
func (s *Server) Close() { s.srv.Close() }

// Configure runs fn under the config's lock.
func (s *Server) Configure(fn func(*Config)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	fn(&s.cfg)
}

func (s *Server) snapshot() Config {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cfg
}

// IssuerURL is the issuer this fixture's protected-resource document names:
// the base URL plus the configured IssuerPath.
func (s *Server) IssuerURL() string {
	return s.URL + s.snapshot().IssuerPath
}

// ProtectedResourceURL is the RFC 9728 document's own URL, the one a
// resource server's challenge should point resource_metadata at.
func (s *Server) ProtectedResourceURL() string { return s.URL + protectedResourcePath }

// RegistrationEndpoint, AuthorizationEndpoint and TokenEndpoint are this
// fixture's three OAuth endpoints, exposed so a test can assert a
// discovered document named them correctly.
func (s *Server) RegistrationEndpoint() string { return s.URL + registerPath }

func (s *Server) AuthorizationEndpoint() string { return s.URL + authorizePath }

func (s *Server) TokenEndpoint() string { return s.URL + tokenPath }

// RefreshCount reports how many refresh-grant token requests this fixture
// has answered, so a test can assert exactly one refresh for several
// concurrent callers.
func (s *Server) RefreshCount() int { return int(s.refreshCount.Load()) }

// RegistrationCount and RequestCount report how many dynamic-client
// registrations and how many requests of any kind this fixture has
// answered, so a test proving a trust check refused before the chain was
// walked can assert that nothing was reached at all.
func (s *Server) RegistrationCount() int { return int(s.registrationCount.Load()) }

func (s *Server) RequestCount() int { return int(s.requestCount.Load()) }

// ResourceID is the RFC 9728 resource identifier this fixture declares,
// which is also the RFC 8707 `resource` value it requires on the
// authorization request and on every token request.
func (s *Server) ResourceID() string { return s.resourceID(s.snapshot()) }

func (s *Server) resourceID(cfg Config) string {
	if cfg.Resource != "" {
		return cfg.Resource
	}
	return s.URL
}

// LastAuthorizeResource and LastAuthorizeRedirectURI report the `resource`
// and `redirect_uri` the most recent authorization request carried.
func (s *Server) LastAuthorizeResource() string {
	s.obsMu.Lock()
	defer s.obsMu.Unlock()
	return s.lastAuthorizeResource
}

func (s *Server) LastAuthorizeRedirectURI() string {
	s.obsMu.Lock()
	defer s.obsMu.Unlock()
	return s.lastAuthorizeRedirectURI
}

// LastTokenResource and LastExchangeRedirectURI report the `resource` the
// most recent token request of any grant carried, and the `redirect_uri`
// the most recent authorization-code grant carried.
func (s *Server) LastTokenResource() string {
	s.obsMu.Lock()
	defer s.obsMu.Unlock()
	return s.lastTokenResource
}

func (s *Server) LastExchangeRedirectURI() string {
	s.obsMu.Lock()
	defer s.obsMu.Unlock()
	return s.lastExchangeRedirectURI
}

// LastCodeChallengeMethod reports the code_challenge_method the most
// recent authorization request carried, so a test can assert it is S256.
func (s *Server) LastCodeChallengeMethod() string {
	s.obsMu.Lock()
	defer s.obsMu.Unlock()
	return s.lastCodeChallengeMeth
}

// LastRegistrationBody reports the raw body of the most recent registration
// request, for a test asserting what the client sent.
func (s *Server) LastRegistrationBody() string {
	s.obsMu.Lock()
	defer s.obsMu.Unlock()
	return s.lastRegistrationBody
}

func (s *Server) handle(w http.ResponseWriter, r *http.Request) {
	s.requestCount.Add(1)
	cfg := s.snapshot()
	insertedPath := wellKnownAuthServerSeg + cfg.IssuerPath
	appendedPath := cfg.IssuerPath + wellKnownAuthServerSeg

	switch {
	case r.Method == http.MethodGet && r.URL.Path == protectedResourcePath:
		s.handleProtectedResource(w, cfg)
	case r.Method == http.MethodGet && r.URL.Path == insertedPath:
		s.handleAuthServerMetadata(w, cfg, MetadataAtInserted)
	case r.Method == http.MethodGet && r.URL.Path == appendedPath && appendedPath != insertedPath:
		s.handleAuthServerMetadata(w, cfg, MetadataAtAppended)
	case r.Method == http.MethodPost && r.URL.Path == registerPath:
		s.handleRegister(w, r, cfg)
	case r.Method == http.MethodGet && r.URL.Path == authorizePath:
		s.handleAuthorize(w, r, cfg)
	case r.Method == http.MethodPost && r.URL.Path == tokenPath:
		s.handleToken(w, r, cfg)
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func (s *Server) handleProtectedResource(w http.ResponseWriter, cfg Config) {
	doc := map[string]any{
		"authorization_servers": []string{s.IssuerURL()},
		"scopes_supported":      cfg.ScopesSupported,
	}
	if !cfg.OmitResource {
		doc["resource"] = s.resourceID(cfg)
	}
	writeJSON(w, doc)
}

func (s *Server) handleAuthServerMetadata(w http.ResponseWriter, cfg Config, at MetadataAvailability) {
	served := cfg.MetadataAt
	if served == "" {
		served = MetadataAtBoth
	}
	if served != MetadataAtBoth && served != at {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	doc := map[string]any{
		"issuer":                           firstNonEmpty(cfg.IssuerOverride, s.IssuerURL()),
		"registration_endpoint":            firstNonEmpty(cfg.RegistrationEndpointOverride, s.RegistrationEndpoint()),
		"authorization_endpoint":           firstNonEmpty(cfg.AuthorizationEndpointOverride, s.AuthorizationEndpoint()),
		"token_endpoint":                   firstNonEmpty(cfg.TokenEndpointOverride, s.TokenEndpoint()),
		"code_challenge_methods_supported": []string{"S256"},
		"grant_types_supported":            []string{"authorization_code", "refresh_token"},
	}
	if cfg.OmitMetadataField != "" {
		delete(doc, cfg.OmitMetadataField)
	}
	writeJSON(w, doc)
}

func (s *Server) handleRegister(w http.ResponseWriter, r *http.Request, cfg Config) {
	if cfg.RegistrationRedirectTo != "" {
		http.Redirect(w, r, cfg.RegistrationRedirectTo, http.StatusPermanentRedirect)
		return
	}
	s.registrationCount.Add(1)
	body := readAll(r)
	s.obsMu.Lock()
	s.lastRegistrationBody = body
	s.obsMu.Unlock()

	if cfg.RegistrationRejects {
		reason := cfg.RegistrationReason
		if reason == "" {
			reason = "registration refused"
		}
		w.WriteHeader(http.StatusBadRequest)
		writeJSON(w, map[string]any{"error": "invalid_client_metadata", "error_description": reason})
		return
	}
	resp := map[string]any{"client_id": "test-client-" + randomID()}
	if cfg.IssueClientSecret {
		resp["client_secret"] = "test-secret-" + randomID()
	}
	writeJSON(w, resp)
}

func (s *Server) handleAuthorize(w http.ResponseWriter, r *http.Request, cfg Config) {
	q := r.URL.Query()
	redirectURI := q.Get("redirect_uri")
	state := q.Get("state")
	challenge := q.Get("code_challenge")
	method := q.Get("code_challenge_method")

	s.obsMu.Lock()
	s.lastCodeChallenge = challenge
	s.lastCodeChallengeMeth = method
	s.lastAuthorizeResource = q.Get("resource")
	s.lastAuthorizeRedirectURI = redirectURI
	s.obsMu.Unlock()

	target, err := redirectTarget(redirectURI)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	// RFC 8707: a client that does not name the resource it wants a token
	// for gets invalid_target, not a code. Redirected rather than answered
	// 400 so the client observes it through its own redirect handling, the
	// way a real authorization server reports it.
	if !cfg.AllowMissingResourceParam && q.Get("resource") != s.resourceID(cfg) {
		target.RawQuery = fmt.Sprintf("error=invalid_target&state=%s", state)
		http.Redirect(w, r, target.String(), http.StatusFound)
		return
	}

	if cfg.RedirectError != "" {
		target.RawQuery = fmt.Sprintf("error=%s&state=%s", cfg.RedirectError, state)
		http.Redirect(w, r, target.String(), http.StatusFound)
		return
	}

	code := randomID()
	s.codesMu.Lock()
	s.codes[code] = issuedCode{challenge: challenge, method: method, redirectURI: redirectURI}
	s.codesMu.Unlock()

	target.RawQuery = fmt.Sprintf("code=%s&state=%s", code, state)
	http.Redirect(w, r, target.String(), http.StatusFound)
}

func (s *Server) handleToken(w http.ResponseWriter, r *http.Request, cfg Config) {
	if cfg.TokenRedirectTo != "" {
		http.Redirect(w, r, cfg.TokenRedirectTo, http.StatusTemporaryRedirect)
		return
	}
	if err := r.ParseForm(); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	s.obsMu.Lock()
	s.lastTokenResource = r.Form.Get("resource")
	s.obsMu.Unlock()
	if !cfg.AllowMissingResourceParam && r.Form.Get("resource") != s.resourceID(cfg) {
		w.WriteHeader(http.StatusBadRequest)
		writeJSON(w, map[string]any{"error": "invalid_target", "error_description": "resource parameter absent or not this resource"})
		return
	}
	switch r.Form.Get("grant_type") {
	case "authorization_code":
		s.handleExchange(w, r, cfg)
	case "refresh_token":
		s.handleRefresh(w, r, cfg)
	default:
		w.WriteHeader(http.StatusBadRequest)
	}
}

func (s *Server) handleExchange(w http.ResponseWriter, r *http.Request, cfg Config) {
	if cfg.ExchangeRejects {
		reason := cfg.ExchangeReason
		if reason == "" {
			reason = "exchange refused"
		}
		w.WriteHeader(http.StatusBadRequest)
		writeJSON(w, map[string]any{"error": "invalid_grant", "error_description": reason})
		return
	}
	code := r.Form.Get("code")
	verifier := r.Form.Get("code_verifier")
	s.obsMu.Lock()
	s.lastExchangeRedirectURI = r.Form.Get("redirect_uri")
	s.obsMu.Unlock()
	s.codesMu.Lock()
	issued, ok := s.codes[code]
	delete(s.codes, code)
	s.codesMu.Unlock()
	if !ok {
		w.WriteHeader(http.StatusBadRequest)
		writeJSON(w, map[string]any{"error": "invalid_grant", "error_description": "unknown code"})
		return
	}
	// RFC 6749 section 4.1.3: the authorization request carried a
	// redirect_uri, so the token request MUST repeat it identically.
	if r.Form.Get("redirect_uri") != issued.redirectURI {
		w.WriteHeader(http.StatusBadRequest)
		writeJSON(w, map[string]any{"error": "invalid_grant", "error_description": "redirect_uri absent or does not match the authorization request"})
		return
	}
	if !verifyPKCE(issued.challenge, verifier) {
		w.WriteHeader(http.StatusBadRequest)
		writeJSON(w, map[string]any{"error": "invalid_grant", "error_description": "pkce verification failed"})
		return
	}
	access := cfg.FixedAccessToken
	if access == "" {
		access = "access-" + randomID()
	}
	refresh := "refresh-" + randomID()
	s.codesMu.Lock()
	s.tokens[access] = issuedToken{refreshToken: refresh}
	s.codesMu.Unlock()
	writeJSON(w, map[string]any{
		"access_token":  access,
		"refresh_token": refresh,
		"token_type":    "Bearer",
		"expires_in":    3600,
	})
}

func (s *Server) handleRefresh(w http.ResponseWriter, r *http.Request, cfg Config) {
	if cfg.RefreshRejects {
		reason := cfg.RefreshReason
		if reason == "" {
			reason = "refresh refused"
		}
		w.WriteHeader(http.StatusBadRequest)
		writeJSON(w, map[string]any{"error": "invalid_grant", "error_description": reason})
		return
	}
	s.refreshCount.Add(1)
	access := cfg.FixedAccessToken
	if access == "" {
		access = "access-" + randomID()
	}
	refresh := "refresh-" + randomID()
	writeJSON(w, map[string]any{
		"access_token":  access,
		"refresh_token": refresh,
		"token_type":    "Bearer",
		"expires_in":    3600,
	})
}

func verifyPKCE(challenge, verifier string) bool {
	if challenge == "" || verifier == "" {
		return false
	}
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:]) == challenge
}

// redirectTarget parses redirectURI, failing closed on an invalid one rather
// than redirecting anywhere unexpected.
func redirectTarget(redirectURI string) (*url.URL, error) {
	if redirectURI == "" {
		return nil, fmt.Errorf("oauthtestserver: empty redirect_uri")
	}
	return url.Parse(redirectURI)
}

func firstNonEmpty(override, fallback string) string {
	if override != "" {
		return override
	}
	return fallback
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	data, err := json.Marshal(v)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	w.Write(data)
}

func readAll(r *http.Request) string {
	data, _ := io.ReadAll(r.Body)
	return string(data)
}

func randomID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
