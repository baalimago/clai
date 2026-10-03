package mcpauth

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
)

// discoveryReadLimit bounds a discovery document's body: these are small,
// fixed-shape JSON documents, never a reason to read without a bound.
const discoveryReadLimit = 64 * 1024

// ProtectedResourceMetadata is the RFC 9728 document at
// /.well-known/oauth-protected-resource.
type ProtectedResourceMetadata struct {
	Resource             string   `json:"resource"`
	AuthorizationServers []string `json:"authorization_servers"`
	ScopesSupported      []string `json:"scopes_supported"`
}

// AuthorizationServerMetadata is the RFC 8414 document. The five fields
// clai requires are named in the README's external-specifications table.
type AuthorizationServerMetadata struct {
	Issuer                        string   `json:"issuer"`
	RegistrationEndpoint          string   `json:"registration_endpoint"`
	AuthorizationEndpoint         string   `json:"authorization_endpoint"`
	TokenEndpoint                 string   `json:"token_endpoint"`
	CodeChallengeMethodsSupported []string `json:"code_challenge_methods_supported"`
	GrantTypesSupported           []string `json:"grant_types_supported"`
}

// discoverProtectedResource fetches the RFC 9728 document with GET at
// metadataURL, the location an AuthChallengeError's ResourceMetadata
// carries. That location is attacker-controlled whenever the challenge is,
// so it is not taken on trust: AuthorizeInteractive has already required it
// to be on the MCP server's own host, fetchJSON requires it to be https or
// loopback, and the caller checks the document's own resource identifier
// against the server before anything in it is acted on.
func discoverProtectedResource(ctx context.Context, client *http.Client, serverName, metadataURL string) (ProtectedResourceMetadata, error) {
	var doc ProtectedResourceMetadata
	if err := fetchJSON(ctx, client, serverName, StageResourceMetadata, metadataURL, &doc); err != nil {
		return ProtectedResourceMetadata{}, &DiscoveryError{ServerName: serverName, Stage: "protected-resource", Cause: err}
	}
	return doc, nil
}

// discoverAuthorizationServer fetches the RFC 8414 document for issuer,
// trying the inserted well-known form first (RFC 8414's own rule: insert
// the well-known segment between the issuer's authority and its path) and
// the OIDC-style appended form second. Both absent is a typed error naming
// both candidate URLs. A document present at either URL but missing a
// required field, declaring an issuer other than the one it was fetched
// for (RFC 8414 section 3.3), or naming an endpoint clai will not use, is a
// typed error before any further request is issued.
func discoverAuthorizationServer(ctx context.Context, client *http.Client, serverName, issuer string) (AuthorizationServerMetadata, error) {
	if err := requireSecureURL(serverName, StageIssuer, issuer); err != nil {
		return AuthorizationServerMetadata{}, err
	}
	inserted, appended, err := wellKnownCandidates(issuer)
	if err != nil {
		return AuthorizationServerMetadata{}, &DiscoveryError{ServerName: serverName, Stage: "authorization-server", Cause: err}
	}

	var doc AuthorizationServerMetadata
	insertedErr := fetchJSON(ctx, client, serverName, StageIssuer, inserted, &doc)
	if insertedErr != nil {
		appendedErr := fetchJSON(ctx, client, serverName, StageIssuer, appended, &doc)
		if appendedErr != nil {
			return AuthorizationServerMetadata{}, &MetadataURLError{ServerName: serverName, Inserted: inserted, Appended: appended}
		}
	}
	if missing := missingRequiredField(doc); missing != "" {
		return AuthorizationServerMetadata{}, &MetadataMissingFieldError{ServerName: serverName, Field: missing}
	}
	if err := requireIssuerMatch(serverName, issuer, doc.Issuer); err != nil {
		return AuthorizationServerMetadata{}, err
	}
	if err := requireSecureEndpoints(serverName, doc); err != nil {
		return AuthorizationServerMetadata{}, err
	}
	return doc, nil
}

// requireSecureEndpoints checks the three endpoint URLs the document names
// before any of them is used, so an insecure or off-host one is refused
// here rather than after a client has already been registered against it.
func requireSecureEndpoints(serverName string, doc AuthorizationServerMetadata) error {
	for _, pair := range []struct {
		stage URLStage
		raw   string
	}{
		{StageRegistration, doc.RegistrationEndpoint},
		{StageAuthorization, doc.AuthorizationEndpoint},
		{StageToken, doc.TokenEndpoint},
	} {
		if err := requireSecureURL(serverName, pair.stage, pair.raw); err != nil {
			return err
		}
	}
	return nil
}

// wellKnownCandidates computes the two well-known URLs RFC 8414's
// composition rule and its OIDC-style fallback produce for issuer: insert
// the well-known segment between the authority and the path (the inserted
// form), or append it after the path (the appended form). The two coincide
// whenever issuer has no path component.
func wellKnownCandidates(issuer string) (inserted, appended string, err error) {
	u, err := url.Parse(issuer)
	if err != nil {
		return "", "", fmt.Errorf("mcpauth: parse issuer %q: %w", issuer, err)
	}
	const wellKnown = "/.well-known/oauth-authorization-server"

	insertedURL := *u
	insertedURL.Path = wellKnown + u.Path
	appendedURL := *u
	appendedURL.Path = strings.TrimRight(u.Path, "/") + wellKnown
	return insertedURL.String(), appendedURL.String(), nil
}

// missingRequiredField reports the first of the five required
// authorization-server metadata fields that is absent, or "" when all five
// are present.
func missingRequiredField(doc AuthorizationServerMetadata) string {
	switch {
	case doc.RegistrationEndpoint == "":
		return "registration_endpoint"
	case doc.AuthorizationEndpoint == "":
		return "authorization_endpoint"
	case doc.TokenEndpoint == "":
		return "token_endpoint"
	case !containsString(doc.CodeChallengeMethodsSupported, CodeChallengeMethod):
		return "code_challenge_methods_supported"
	case !containsString(doc.GrantTypesSupported, "refresh_token"):
		return "grant_types_supported"
	default:
		return ""
	}
}

func containsString(ss []string, want string) bool {
	return slices.Contains(ss, want)
}

// fetchJSON performs a bounded GET and decodes the JSON body into v,
// refusing a URL that is not https or loopback before issuing anything. A
// non-2xx status, a transport failure or an undecodable body are all
// reported as plain errors; callers wrap them with the stage-specific typed
// error.
func fetchJSON(ctx context.Context, client *http.Client, serverName string, stage URLStage, u string, v any) error {
	if err := requireSecureURL(serverName, stage, u); err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return fmt.Errorf("build request for %q: %w", u, err)
	}
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("fetch %q: %w", u, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("fetch %q: status %d", u, resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, discoveryReadLimit))
	if err != nil {
		return fmt.Errorf("read %q: %w", u, err)
	}
	if err := json.Unmarshal(data, v); err != nil {
		return fmt.Errorf("decode %q: %w", u, err)
	}
	return nil
}
