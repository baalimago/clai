package mcpauth

import "fmt"

// ChallengeMissingResourceMetadataError means an AuthChallengeError carried
// no resource_metadata parameter, so discovery has nowhere to start.
type ChallengeMissingResourceMetadataError struct {
	ServerName string
}

func (e *ChallengeMissingResourceMetadataError) Error() string {
	return fmt.Sprintf("mcp server %q: authorization challenge carried no resource_metadata location", e.ServerName)
}

// DiscoveryError means a discovery document (the protected-resource or the
// authorization-server metadata) could not be fetched or parsed as JSON.
type DiscoveryError struct {
	ServerName string
	Stage      string // "protected-resource" or "authorization-server"
	Cause      error
}

func (e *DiscoveryError) Error() string {
	return fmt.Sprintf("mcp server %q: discover %s metadata: %v", e.ServerName, e.Stage, e.Cause)
}

func (e *DiscoveryError) Unwrap() error { return e.Cause }

// MetadataMissingFieldError means the authorization-server metadata
// document omitted one of the five fields the external-specifications
// table requires.
type MetadataMissingFieldError struct {
	ServerName string
	Field      string
}

func (e *MetadataMissingFieldError) Error() string {
	return fmt.Sprintf("mcp server %q: authorization-server metadata is missing required field %q", e.ServerName, e.Field)
}

// MetadataURLError means neither the RFC 8414 inserted well-known form nor
// the OIDC-style appended form answered with a usable document.
type MetadataURLError struct {
	ServerName string
	Inserted   string
	Appended   string
}

func (e *MetadataURLError) Error() string {
	return fmt.Sprintf("mcp server %q: authorization-server metadata absent at both %q and %q", e.ServerName, e.Inserted, e.Appended)
}

// RegistrationRejectedError means the authorization server refused dynamic
// client registration (RFC 7591).
type RegistrationRejectedError struct {
	ServerName string
	Reason     string
	Cause      error
}

func (e *RegistrationRejectedError) Error() string {
	return fmt.Sprintf("mcp server %q: dynamic client registration rejected: %s", e.ServerName, e.Reason)
}

func (e *RegistrationRejectedError) Unwrap() error { return e.Cause }

// RedirectError means the loopback redirect, or the pasted response, carried
// an OAuth error parameter instead of a code.
type RedirectError struct {
	ServerName string
	Reason     string
}

func (e *RedirectError) Error() string {
	return fmt.Sprintf("mcp server %q: authorization redirect carried an error: %s", e.ServerName, e.Reason)
}

// ExchangeRejectedError means the authorization server refused the
// authorization-code exchange. Nothing is written to the token store when
// this is returned.
type ExchangeRejectedError struct {
	ServerName string
	Reason     string
	Cause      error
}

func (e *ExchangeRejectedError) Error() string {
	return fmt.Sprintf("mcp server %q: authorization code exchange rejected: %s", e.ServerName, e.Reason)
}

func (e *ExchangeRejectedError) Unwrap() error { return e.Cause }

// RefreshError is the typed authorization error a rejected or impossible
// refresh grant produces. An interactive caller may choose to re-authorize;
// a headless caller returns it to its own caller untouched.
type RefreshError struct {
	ServerName string
	Reason     string
	Cause      error
}

func (e *RefreshError) Error() string {
	return fmt.Sprintf("mcp server %q: token refresh failed: %s", e.ServerName, e.Reason)
}

func (e *RefreshError) Unwrap() error { return e.Cause }

// CredentialSourceError means a configured credential source
// (auth.token_command or auth.token_env) failed. Per D15, a configured
// source that fails is returned with no fall-through to the next source.
type CredentialSourceError struct {
	ServerName string
	Source     CredentialSource
	Cause      error
}

func (e *CredentialSourceError) Error() string {
	return fmt.Sprintf("mcp server %q: credential source %q failed: %v", e.ServerName, e.Source, e.Cause)
}

func (e *CredentialSourceError) Unwrap() error { return e.Cause }

// TokenStoreWriteError means the token store could not be written. The run
// continues with whatever token it already holds; this error is reported to
// the caller so the degraded state is visible.
type TokenStoreWriteError struct {
	ServerName string
	Path       string
	Cause      error
}

func (e *TokenStoreWriteError) Error() string {
	return fmt.Sprintf("mcp server %q: write token store entry %q: %v", e.ServerName, e.Path, e.Cause)
}

func (e *TokenStoreWriteError) Unwrap() error { return e.Cause }

// InsecureURLError means a URL on the authorization chain is neither https
// nor http on a loopback host, so clai refused to fetch it, post to it, or
// hand it to a browser. ServerName is empty when the refusal happened
// inside a redirect policy, which is shared across servers; the caller's
// own typed error names the server in that case.
type InsecureURLError struct {
	ServerName string
	Stage      URLStage
	URL        string
	Reason     string
}

func (e *InsecureURLError) Error() string {
	if e.ServerName == "" {
		return fmt.Sprintf("mcpauth: %s %q refused: %s", e.Stage, e.URL, e.Reason)
	}
	return fmt.Sprintf("mcp server %q: %s %q refused: %s", e.ServerName, e.Stage, e.URL, e.Reason)
}

// UntrustedMetadataHostError means a challenge's resource_metadata location
// does not live on the MCP server's own hostname, so the document it points
// at is not that server speaking about itself.
type UntrustedMetadataHostError struct {
	ServerName  string
	MetadataURL string
	Endpoint    string
}

func (e *UntrustedMetadataHostError) Error() string {
	return fmt.Sprintf("mcp server %q: challenge pointed resource_metadata at %q, which is not on the server's own host (%q)", e.ServerName, e.MetadataURL, e.Endpoint)
}

// IssuerMismatchError means an authorization-server metadata document
// declared an issuer other than the one whose well-known URL it was
// fetched from, which RFC 8414 section 3.3 forbids.
type IssuerMismatchError struct {
	ServerName string
	Requested  string
	Declared   string
}

func (e *IssuerMismatchError) Error() string {
	return fmt.Sprintf("mcp server %q: authorization-server metadata fetched for issuer %q declares issuer %q", e.ServerName, e.Requested, e.Declared)
}

// ResourceMismatchError means a protected-resource document declared a
// resource identifier that does not cover the MCP server being authorized,
// or declared none at all, which RFC 9728 section 3.3 requires.
type ResourceMismatchError struct {
	ServerName string
	Declared   string
	Endpoint   string
}

func (e *ResourceMismatchError) Error() string {
	if e.Declared == "" {
		return fmt.Sprintf("mcp server %q: protected-resource metadata declares no resource identifier, so it cannot be checked against %q", e.ServerName, e.Endpoint)
	}
	return fmt.Sprintf("mcp server %q: protected-resource metadata declares resource %q, which does not cover %q", e.ServerName, e.Declared, e.Endpoint)
}

// RedirectRefusedError means a token or registration endpoint answered with
// a redirect. Go re-sends a POST body across one, so following it would
// hand the PKCE verifier, the refresh token or the client secret to the
// target.
type RedirectRefusedError struct {
	From string
	To   string
}

func (e *RedirectRefusedError) Error() string {
	return fmt.Sprintf("mcpauth: refused to follow a redirect from %q to %q: the request body carries a credential", e.From, e.To)
}

// InteractiveAuthDisabledError means an authorization challenge could only
// be resolved by the browser flow, but this run may not open a browser or
// bind a loopback listener: its output is not a terminal, which every
// pkg/agent consumer is.
type InteractiveAuthDisabledError struct {
	ServerName string
}

func (e *InteractiveAuthDisabledError) Error() string {
	return fmt.Sprintf("mcp server %q: interactive authorization is disabled for this run because its output is not a terminal; run \"clai mcp auth %s\" from a terminal to authorize it once", e.ServerName, e.ServerName)
}
