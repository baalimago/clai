package oauthtestserver

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"maps"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

// noRedirect returns the fixture's redirects to the caller instead of
// following them, which is what lets a test read the code, state or error
// an authorization response carried.
var noRedirect = &http.Client{
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
}

func newFixture(t *testing.T) *Server {
	t.Helper()
	s := New()
	t.Cleanup(s.Close)
	return s
}

func get(t *testing.T, rawURL string) *http.Response {
	t.Helper()
	resp, err := noRedirect.Get(rawURL)
	if err != nil {
		t.Fatalf("GET %s: %v", rawURL, err)
	}
	return resp
}

func postForm(t *testing.T, rawURL string, form url.Values) *http.Response {
	t.Helper()
	resp, err := noRedirect.PostForm(rawURL, form)
	if err != nil {
		t.Fatalf("POST %s: %v", rawURL, err)
	}
	return resp
}

func decode(t *testing.T, resp *http.Response) map[string]any {
	t.Helper()
	defer resp.Body.Close()
	var m map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&m); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	return m
}

func pkcePair(verifier string) (challenge string) {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func TestProtectedResourceDocument(t *testing.T) {
	t.Run("default declares itself as the resource", func(t *testing.T) {
		s := newFixture(t)
		resp := get(t, s.ProtectedResourceURL())
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want 200", resp.StatusCode)
		}
		doc := decode(t, resp)
		if got := doc["resource"]; got != s.URL {
			t.Errorf("resource = %v, want %q", got, s.URL)
		}
		servers, ok := doc["authorization_servers"].([]any)
		if !ok || len(servers) != 1 || servers[0] != s.URL {
			t.Errorf("authorization_servers = %v, want [%s]", doc["authorization_servers"], s.URL)
		}
		scopes, ok := doc["scopes_supported"].([]any)
		if !ok || len(scopes) != 2 {
			t.Errorf("scopes_supported = %v, want two entries", doc["scopes_supported"])
		}
	})

	t.Run("issuer path builds the authorization server URL", func(t *testing.T) {
		s := newFixture(t)
		s.Configure(func(c *Config) { c.IssuerPath = "/tenant" })
		doc := decode(t, get(t, s.ProtectedResourceURL()))
		servers := doc["authorization_servers"].([]any)
		if servers[0] != s.URL+"/tenant" {
			t.Errorf("authorization_servers[0] = %v, want %q", servers[0], s.URL+"/tenant")
		}
		if s.IssuerURL() != s.URL+"/tenant" {
			t.Errorf("IssuerURL = %q", s.IssuerURL())
		}
	})

	t.Run("resource override and omission", func(t *testing.T) {
		s := newFixture(t)
		s.Configure(func(c *Config) {
			c.Resource = "https://mcp.example.com"
			c.ScopesSupported = nil
		})
		if got := s.ResourceID(); got != "https://mcp.example.com" {
			t.Errorf("ResourceID = %q", got)
		}
		doc := decode(t, get(t, s.ProtectedResourceURL()))
		if doc["resource"] != "https://mcp.example.com" {
			t.Errorf("resource = %v", doc["resource"])
		}

		s2 := newFixture(t)
		s2.Configure(func(c *Config) { c.OmitResource = true })
		if doc := decode(t, get(t, s2.ProtectedResourceURL())); doc["resource"] != nil {
			t.Errorf("resource present despite OmitResource: %v", doc["resource"])
		}
	})
}

func TestAuthServerMetadataAvailability(t *testing.T) {
	s := newFixture(t)
	s.Configure(func(c *Config) { c.IssuerPath = "/tenant" })
	inserted := s.URL + "/.well-known/oauth-authorization-server/tenant"
	appended := s.URL + "/tenant/.well-known/oauth-authorization-server"

	testCases := []struct {
		name               string
		at                 MetadataAvailability
		insertedStatusCode int
		appendedStatusCode int
	}{
		{name: "empty behaves as both", at: "", insertedStatusCode: 200, appendedStatusCode: 200},
		{name: "both", at: MetadataAtBoth, insertedStatusCode: 200, appendedStatusCode: 200},
		{name: "inserted only", at: MetadataAtInserted, insertedStatusCode: 200, appendedStatusCode: 404},
		{name: "appended only", at: MetadataAtAppended, insertedStatusCode: 404, appendedStatusCode: 200},
		{name: "neither", at: MetadataAtNeither, insertedStatusCode: 404, appendedStatusCode: 404},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			c := tc
			s.Configure(func(cfg *Config) { cfg.MetadataAt = c.at })

			resp := get(t, inserted)
			resp.Body.Close()
			if resp.StatusCode != c.insertedStatusCode {
				t.Errorf("inserted status = %d, want %d", resp.StatusCode, c.insertedStatusCode)
			}

			resp = get(t, appended)
			resp.Body.Close()
			if resp.StatusCode != c.appendedStatusCode {
				t.Errorf("appended status = %d, want %d", resp.StatusCode, c.appendedStatusCode)
			}
		})
	}
}

func TestAuthServerMetadataFields(t *testing.T) {
	s := newFixture(t)
	s.Configure(func(c *Config) {
		c.MetadataAt = MetadataAtInserted
		c.OmitMetadataField = "token_endpoint"
	})
	doc := decode(t, get(t, s.URL+"/.well-known/oauth-authorization-server"))
	if _, present := doc["token_endpoint"]; present {
		t.Errorf("token_endpoint present despite omission")
	}
	if doc["issuer"] != s.URL {
		t.Errorf("issuer = %v, want %q", doc["issuer"], s.URL)
	}
	if doc["registration_endpoint"] != s.RegistrationEndpoint() {
		t.Errorf("registration_endpoint = %v", doc["registration_endpoint"])
	}
	if doc["authorization_endpoint"] != s.AuthorizationEndpoint() {
		t.Errorf("authorization_endpoint = %v", doc["authorization_endpoint"])
	}

	s2 := newFixture(t)
	s2.Configure(func(c *Config) {
		c.IssuerOverride = "https://evil.example.com"
		c.RegistrationEndpointOverride = "http://plain.example.com/reg"
		c.AuthorizationEndpointOverride = "http://plain.example.com/auth"
		c.TokenEndpointOverride = "http://plain.example.com/token"
	})
	doc = decode(t, get(t, s2.URL+"/.well-known/oauth-authorization-server"))
	for field, want := range map[string]string{
		"issuer":                 "https://evil.example.com",
		"registration_endpoint":  "http://plain.example.com/reg",
		"authorization_endpoint": "http://plain.example.com/auth",
		"token_endpoint":         "http://plain.example.com/token",
	} {
		if doc[field] != want {
			t.Errorf("%s = %v, want %q", field, doc[field], want)
		}
	}
}

func TestRegistration(t *testing.T) {
	t.Run("plain registration mints a client id", func(t *testing.T) {
		s := newFixture(t)
		resp := postForm(t, s.RegistrationEndpoint(), url.Values{"client_name": {"clai"}})
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want 200", resp.StatusCode)
		}
		body := decode(t, resp)
		if id, ok := body["client_id"].(string); !ok || !strings.HasPrefix(id, "test-client-") {
			t.Errorf("client_id = %v", body["client_id"])
		}
		if _, present := body["client_secret"]; present {
			t.Errorf("client_secret present without IssueClientSecret")
		}
		if got := s.LastRegistrationBody(); !strings.Contains(got, "clai") {
			t.Errorf("LastRegistrationBody = %q, want it to contain the posted body", got)
		}
		if s.RegistrationCount() != 1 {
			t.Errorf("RegistrationCount = %d, want 1", s.RegistrationCount())
		}
	})

	t.Run("issuing a client secret", func(t *testing.T) {
		s := newFixture(t)
		s.Configure(func(c *Config) { c.IssueClientSecret = true })
		body := decode(t, postForm(t, s.RegistrationEndpoint(), url.Values{}))
		secret, ok := body["client_secret"].(string)
		if !ok || !strings.HasPrefix(secret, "test-secret-") {
			t.Errorf("client_secret = %v", body["client_secret"])
		}
	})

	t.Run("rejecting registration", func(t *testing.T) {
		s := newFixture(t)
		s.Configure(func(c *Config) {
			c.RegistrationRejects = true
			c.RegistrationReason = "policy"
		})
		resp := postForm(t, s.RegistrationEndpoint(), url.Values{})
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", resp.StatusCode)
		}
		body := decode(t, resp)
		if body["error"] != "invalid_client_metadata" || body["error_description"] != "policy" {
			t.Errorf("error body = %v", body)
		}
		if s.RegistrationCount() != 1 {
			t.Errorf("RegistrationCount = %d, want 1", s.RegistrationCount())
		}
	})

	t.Run("rejecting registration without a reason uses the default", func(t *testing.T) {
		s := newFixture(t)
		s.Configure(func(c *Config) { c.RegistrationRejects = true })
		body := decode(t, postForm(t, s.RegistrationEndpoint(), url.Values{}))
		if body["error_description"] != "registration refused" {
			t.Errorf("error_description = %v", body["error_description"])
		}
	})

	t.Run("redirecting registration", func(t *testing.T) {
		s := newFixture(t)
		s.Configure(func(c *Config) { c.RegistrationRedirectTo = "https://elsewhere.example.com/register" })
		resp := postForm(t, s.RegistrationEndpoint(), url.Values{})
		if resp.StatusCode != http.StatusPermanentRedirect {
			t.Fatalf("status = %d, want 308", resp.StatusCode)
		}
		if loc := resp.Header.Get("Location"); loc != "https://elsewhere.example.com/register" {
			t.Errorf("Location = %q", loc)
		}
		if s.RegistrationCount() != 0 {
			t.Errorf("RegistrationCount = %d, want 0 for a redirected registration", s.RegistrationCount())
		}
	})
}

func authorizeURL(s *Server, extra url.Values) string {
	q := url.Values{
		"redirect_uri":          {"http://127.0.0.1:9999/callback"},
		"state":                 {"xyz"},
		"code_challenge":        {pkcePair("verifier")},
		"code_challenge_method": {"S256"},
		"resource":              {s.ResourceID()},
	}
	maps.Copy(q, extra)
	return s.AuthorizationEndpoint() + "?" + q.Encode()
}

func TestAuthorize(t *testing.T) {
	t.Run("happy path issues a code", func(t *testing.T) {
		s := newFixture(t)
		resp := get(t, authorizeURL(s, nil))
		if resp.StatusCode != http.StatusFound {
			t.Fatalf("status = %d, want 302", resp.StatusCode)
		}
		loc, err := url.Parse(resp.Header.Get("Location"))
		if err != nil {
			t.Fatalf("parse Location: %v", err)
		}
		if loc.Query().Get("code") == "" || loc.Query().Get("state") != "xyz" {
			t.Errorf("redirect query = %v", loc.Query())
		}
		if s.LastCodeChallengeMethod() != "S256" {
			t.Errorf("LastCodeChallengeMethod = %q", s.LastCodeChallengeMethod())
		}
		if s.LastAuthorizeResource() != s.ResourceID() {
			t.Errorf("LastAuthorizeResource = %q", s.LastAuthorizeResource())
		}
		if s.LastAuthorizeRedirectURI() != "http://127.0.0.1:9999/callback" {
			t.Errorf("LastAuthorizeRedirectURI = %q", s.LastAuthorizeRedirectURI())
		}
	})

	t.Run("missing resource is rejected through the redirect", func(t *testing.T) {
		s := newFixture(t)
		resp := get(t, authorizeURL(s, url.Values{"resource": {""}}))
		if resp.StatusCode != http.StatusFound {
			t.Fatalf("status = %d, want 302", resp.StatusCode)
		}
		loc, _ := url.Parse(resp.Header.Get("Location"))
		if loc.Query().Get("error") != "invalid_target" {
			t.Errorf("error = %q, want invalid_target", loc.Query().Get("error"))
		}
	})

	t.Run("relaxed resource check issues a code", func(t *testing.T) {
		s := newFixture(t)
		s.Configure(func(c *Config) { c.AllowMissingResourceParam = true })
		resp := get(t, authorizeURL(s, url.Values{"resource": {""}}))
		loc, _ := url.Parse(resp.Header.Get("Location"))
		if loc.Query().Get("code") == "" {
			t.Errorf("no code issued: %v", loc.Query())
		}
	})

	t.Run("configured redirect error", func(t *testing.T) {
		s := newFixture(t)
		s.Configure(func(c *Config) { c.RedirectError = "access_denied" })
		resp := get(t, authorizeURL(s, nil))
		loc, _ := url.Parse(resp.Header.Get("Location"))
		if loc.Query().Get("error") != "access_denied" {
			t.Errorf("error = %q", loc.Query().Get("error"))
		}
	})

	t.Run("empty redirect_uri fails closed", func(t *testing.T) {
		s := newFixture(t)
		resp := get(t, authorizeURL(s, url.Values{"redirect_uri": {""}}))
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", resp.StatusCode)
		}
	})
}

// issueCode drives the authorize endpoint and returns the code it issued.
func issueCode(t *testing.T, s *Server, verifier string) string {
	t.Helper()
	resp := get(t, authorizeURL(s, url.Values{"code_challenge": {pkcePair(verifier)}}))
	loc, err := url.Parse(resp.Header.Get("Location"))
	if err != nil {
		t.Fatalf("parse Location: %v", err)
	}
	code := loc.Query().Get("code")
	if code == "" {
		t.Fatalf("no code issued: %v", loc.Query())
	}
	return code
}

func exchangeForm(s *Server, code, verifier string) url.Values {
	return url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"code_verifier": {verifier},
		"redirect_uri":  {"http://127.0.0.1:9999/callback"},
		"resource":      {s.ResourceID()},
	}
}

func TestToken(t *testing.T) {
	t.Run("authorization code exchange", func(t *testing.T) {
		s := newFixture(t)
		code := issueCode(t, s, "verifier")
		resp := postForm(t, s.TokenEndpoint(), exchangeForm(s, code, "verifier"))
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want 200", resp.StatusCode)
		}
		body := decode(t, resp)
		if !strings.HasPrefix(body["access_token"].(string), "access-") {
			t.Errorf("access_token = %v", body["access_token"])
		}
		if !strings.HasPrefix(body["refresh_token"].(string), "refresh-") {
			t.Errorf("refresh_token = %v", body["refresh_token"])
		}
		if body["token_type"] != "Bearer" {
			t.Errorf("token_type = %v", body["token_type"])
		}
		if s.LastExchangeRedirectURI() != "http://127.0.0.1:9999/callback" {
			t.Errorf("LastExchangeRedirectURI = %q", s.LastExchangeRedirectURI())
		}
		if s.LastTokenResource() != s.ResourceID() {
			t.Errorf("LastTokenResource = %q", s.LastTokenResource())
		}
	})

	t.Run("fixed access token", func(t *testing.T) {
		s := newFixture(t)
		s.Configure(func(c *Config) { c.FixedAccessToken = "fixed-token" })
		code := issueCode(t, s, "verifier")
		body := decode(t, postForm(t, s.TokenEndpoint(), exchangeForm(s, code, "verifier")))
		if body["access_token"] != "fixed-token" {
			t.Errorf("access_token = %v, want fixed-token", body["access_token"])
		}
	})

	t.Run("missing resource is rejected", func(t *testing.T) {
		s := newFixture(t)
		code := issueCode(t, s, "verifier")
		form := exchangeForm(s, code, "verifier")
		form.Set("resource", "")
		resp := postForm(t, s.TokenEndpoint(), form)
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", resp.StatusCode)
		}
		if got := decode(t, resp)["error"]; got != "invalid_target" {
			t.Errorf("error = %v", got)
		}
	})

	t.Run("unknown grant type is rejected", func(t *testing.T) {
		s := newFixture(t)
		resp := postForm(t, s.TokenEndpoint(), url.Values{
			"grant_type": {"password"},
			"resource":   {s.ResourceID()},
		})
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", resp.StatusCode)
		}
	})

	t.Run("rejected exchange uses its reason", func(t *testing.T) {
		s := newFixture(t)
		s.Configure(func(c *Config) { c.ExchangeRejects = true })
		resp := postForm(t, s.TokenEndpoint(), url.Values{
			"grant_type": {"authorization_code"},
			"resource":   {s.ResourceID()},
		})
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", resp.StatusCode)
		}
		if got := decode(t, resp)["error_description"]; got != "exchange refused" {
			t.Errorf("error_description = %v", got)
		}
	})

	t.Run("unknown code is rejected", func(t *testing.T) {
		s := newFixture(t)
		resp := postForm(t, s.TokenEndpoint(), exchangeForm(s, "no-such-code", "verifier"))
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", resp.StatusCode)
		}
		if got := decode(t, resp)["error_description"]; got != "unknown code" {
			t.Errorf("error_description = %v", got)
		}
	})

	t.Run("mismatched redirect_uri is rejected", func(t *testing.T) {
		s := newFixture(t)
		code := issueCode(t, s, "verifier")
		form := exchangeForm(s, code, "verifier")
		form.Set("redirect_uri", "http://127.0.0.1:9999/other")
		resp := postForm(t, s.TokenEndpoint(), form)
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", resp.StatusCode)
		}
		if desc, _ := decode(t, resp)["error_description"].(string); !strings.Contains(desc, "redirect_uri") {
			t.Errorf("error_description = %q, want it to mention redirect_uri", desc)
		}
	})

	t.Run("failing PKCE is rejected", func(t *testing.T) {
		s := newFixture(t)
		code := issueCode(t, s, "verifier")
		resp := postForm(t, s.TokenEndpoint(), exchangeForm(s, code, "wrong-verifier"))
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", resp.StatusCode)
		}
		if got := decode(t, resp)["error_description"]; got != "pkce verification failed" {
			t.Errorf("error_description = %v", got)
		}
	})

	t.Run("refresh grant", func(t *testing.T) {
		s := newFixture(t)
		resp := postForm(t, s.TokenEndpoint(), url.Values{
			"grant_type":    {"refresh_token"},
			"refresh_token": {"whatever"},
			"resource":      {s.ResourceID()},
		})
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want 200", resp.StatusCode)
		}
		if body := decode(t, resp); !strings.HasPrefix(body["access_token"].(string), "access-") {
			t.Errorf("access_token = %v", body["access_token"])
		}
		if s.RefreshCount() != 1 {
			t.Errorf("RefreshCount = %d, want 1", s.RefreshCount())
		}
	})

	t.Run("rejected refresh uses its reason", func(t *testing.T) {
		s := newFixture(t)
		s.Configure(func(c *Config) {
			c.RefreshRejects = true
			c.RefreshReason = "expired"
		})
		resp := postForm(t, s.TokenEndpoint(), url.Values{
			"grant_type": {"refresh_token"},
			"resource":   {s.ResourceID()},
		})
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", resp.StatusCode)
		}
		if got := decode(t, resp)["error_description"]; got != "expired" {
			t.Errorf("error_description = %v", got)
		}
		if s.RefreshCount() != 0 {
			t.Errorf("RefreshCount = %d, want 0", s.RefreshCount())
		}
	})

	t.Run("redirecting token endpoint", func(t *testing.T) {
		s := newFixture(t)
		s.Configure(func(c *Config) { c.TokenRedirectTo = "https://elsewhere.example.com/token" })
		resp := postForm(t, s.TokenEndpoint(), url.Values{})
		if resp.StatusCode != http.StatusTemporaryRedirect {
			t.Fatalf("status = %d, want 307", resp.StatusCode)
		}
		if loc := resp.Header.Get("Location"); loc != "https://elsewhere.example.com/token" {
			t.Errorf("Location = %q", loc)
		}
	})
}

func TestDefaultsAndUnknownRoutes(t *testing.T) {
	s := newFixture(t)
	s.Configure(func(c *Config) {
		c.RegistrationReason = ""
		c.ExchangeReason = ""
		c.RefreshReason = ""
	})

	resp := get(t, s.URL+"/nope")
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("unknown GET status = %d, want 404", resp.StatusCode)
	}

	resp = postForm(t, s.AuthorizationEndpoint(), url.Values{})
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("POST to authorize status = %d, want 404", resp.StatusCode)
	}

	if s.RequestCount() == 0 {
		t.Errorf("RequestCount = 0 after requests")
	}
	if s.ResourceID() != s.URL {
		t.Errorf("ResourceID = %q, want %q", s.ResourceID(), s.URL)
	}
}

func TestWriteJSONMarshalFailure(t *testing.T) {
	rec := httptest.NewRecorder()
	writeJSON(rec, make(chan int))
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", rec.Code)
	}
}

func TestVerifyPKCE(t *testing.T) {
	challenge := pkcePair("verifier")
	if !verifyPKCE(challenge, "verifier") {
		t.Errorf("verifyPKCE rejected a correct pair")
	}
	if verifyPKCE(challenge, "") {
		t.Errorf("verifyPKCE accepted an empty verifier")
	}
	if verifyPKCE("", "verifier") {
		t.Errorf("verifyPKCE accepted an empty challenge")
	}
	if verifyPKCE(challenge, "other") {
		t.Errorf("verifyPKCE accepted a wrong verifier")
	}
}

func TestAutoFollowOpener(t *testing.T) {
	t.Run("drives the URL in the background", func(t *testing.T) {
		hit := make(chan struct{}, 1)
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			hit <- struct{}{}
		}))
		defer srv.Close()

		if err := NewAutoFollowOpener().Open(srv.URL); err != nil {
			t.Fatalf("Open: %v", err)
		}
		select {
		case <-hit:
		case <-time.After(3 * time.Second):
			t.Fatalf("opener did not reach the URL")
		}
	})

	t.Run("a failing GET does not block or error the caller", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
		srv.Close()
		if err := NewAutoFollowOpener().Open(srv.URL); err != nil {
			t.Fatalf("Open: %v", err)
		}
	})
}

func TestFailingOpener(t *testing.T) {
	err := FailingOpener{}.Open("https://example.com")
	if err == nil || err.Error() != "no display" {
		t.Fatalf("Open error = %v, want the default reason", err)
	}
	err = FailingOpener{Reason: "headless"}.Open("https://example.com")
	if err == nil || err.Error() != "headless" {
		t.Fatalf("Open error = %v, want headless", err)
	}
}

func TestReadAll(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("body-bytes"))
	if got := readAll(req); got != "body-bytes" {
		t.Errorf("readAll = %q", got)
	}
	if got := randomID(); len(got) != 32 {
		t.Errorf("randomID = %q, want 32 hex characters", got)
	}
}
