package mcpauth

import (
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
)

// maxDiscoveryRedirects bounds how far a discovery GET may be redirected.
// Every hop still has to satisfy requireSecureURL.
const maxDiscoveryRedirects = 3

// URLStage names the link in the authorization chain a URL belongs to, so
// a refusal says what clai was about to fetch, post to, or open.
type URLStage string

const (
	StageResourceMetadata  URLStage = "resource_metadata location"
	StageIssuer            URLStage = "issuer"
	StageRegistration      URLStage = "registration_endpoint"
	StageAuthorization     URLStage = "authorization_endpoint"
	StageToken             URLStage = "token_endpoint"
	StageAuthorizationURL  URLStage = "authorization URL"
	StageServerEndpoint    URLStage = "server url"
	StageDiscoveryRedirect URLStage = "discovery redirect target"
)

// requireSecureURL refuses any authorization-chain URL that is not https,
// or http on a loopback host. A plain-http hop anywhere on this chain lets
// an on-path attacker choose the authorization server, the client
// registration and the token endpoint; the loopback exception is what
// keeps a local MCP server, and this repository's own fixtures, usable.
func requireSecureURL(serverName string, stage URLStage, raw string) error {
	u, err := url.Parse(raw)
	if err != nil || !u.IsAbs() || u.Host == "" {
		return &InsecureURLError{ServerName: serverName, Stage: stage, URL: raw, Reason: "not an absolute URL with a host"}
	}
	switch strings.ToLower(u.Scheme) {
	case "https":
		return nil
	case "http":
		if isLoopbackHost(u.Hostname()) {
			return nil
		}
	}
	return &InsecureURLError{ServerName: serverName, Stage: stage, URL: raw, Reason: "only https, or http on a loopback host, may carry an authorization exchange"}
}

func isLoopbackHost(hostname string) bool {
	if strings.EqualFold(hostname, "localhost") {
		return true
	}
	ip := net.ParseIP(strings.Trim(hostname, "[]"))
	return ip != nil && ip.IsLoopback()
}

// requireMetadataFromServerHost refuses a resource_metadata location that
// does not live on the MCP server's own hostname. The challenge is
// attacker-controlled whenever the 401 is, so the only thing making the
// document it points at trustworthy is that it comes from the server being
// authorized. The port is deliberately not compared: a resource server may
// front its metadata elsewhere on the same host, and this repository's
// composed fixtures do.
func requireMetadataFromServerHost(serverName, metadataURL, endpoint string) error {
	meta, metaErr := url.Parse(metadataURL)
	srv, srvErr := url.Parse(endpoint)
	if metaErr != nil || srvErr != nil || meta.Hostname() == "" || srv.Hostname() == "" || !strings.EqualFold(meta.Hostname(), srv.Hostname()) {
		return &UntrustedMetadataHostError{ServerName: serverName, MetadataURL: metadataURL, Endpoint: endpoint}
	}
	return nil
}

// requireIssuerMatch is RFC 8414 section 3.3: the issuer an
// authorization-server metadata document declares must be the issuer whose
// well-known URL it was fetched from. Without it, any document reachable
// at a well-known path can name someone else's endpoints.
func requireIssuerMatch(serverName, requested, declared string) error {
	if declared == "" || strings.TrimRight(requested, "/") != strings.TrimRight(declared, "/") {
		return &IssuerMismatchError{ServerName: serverName, Requested: requested, Declared: declared}
	}
	return nil
}

// requireResourceCovers is RFC 9728 section 3.3: the resource identifier a
// protected-resource document declares must be the resource clai is
// authorizing. Without it, a malicious server points clai at a document
// describing a different resource, and the token clai then obtains is
// minted for that one.
func requireResourceCovers(serverName, declared, endpoint string) error {
	declaredURL, declaredErr := url.Parse(declared)
	endpointURL, endpointErr := url.Parse(endpoint)
	if declared == "" || declaredErr != nil || endpointErr != nil ||
		!sameOrigin(declaredURL, endpointURL) || !pathCovers(declaredURL.Path, endpointURL.Path) {
		return &ResourceMismatchError{ServerName: serverName, Declared: declared, Endpoint: endpoint}
	}
	return nil
}

func sameOrigin(a, b *url.URL) bool {
	return strings.EqualFold(a.Scheme, b.Scheme) && strings.EqualFold(originHostPort(a), originHostPort(b))
}

func originHostPort(u *url.URL) string {
	port := u.Port()
	if port == "" {
		switch strings.ToLower(u.Scheme) {
		case "https":
			port = "443"
		case "http":
			port = "80"
		}
	}
	return u.Hostname() + ":" + port
}

func pathCovers(resourcePath, endpointPath string) bool {
	resource := strings.TrimRight(resourcePath, "/")
	if resource == "" {
		return true
	}
	endpoint := strings.TrimRight(endpointPath, "/")
	return endpoint == resource || strings.HasPrefix(endpoint, resource+"/")
}

// refuseRedirect is the CheckRedirect policy for the token and
// registration endpoints. Go re-sends a POST body across a 307 or 308, and
// never strips it the way it strips a cross-domain Authorization header, so
// following one hands the PKCE verifier, the refresh token or the client
// secret to whatever the target is.
func refuseRedirect(req *http.Request, via []*http.Request) error {
	from := ""
	if len(via) > 0 {
		from = via[len(via)-1].URL.String()
	}
	return &RedirectRefusedError{From: from, To: req.URL.String()}
}

// secureHopRedirect is the CheckRedirect policy for the discovery GETs,
// which carry no credential and which a real issuer may legitimately
// redirect (a trailing-slash normalisation, most often). Bounded, and
// every hop must still be an acceptable authorization-chain URL.
func secureHopRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= maxDiscoveryRedirects {
		return fmt.Errorf("mcpauth: discovery fetch exceeded %d redirects", maxDiscoveryRedirects)
	}
	return requireSecureURL("", StageDiscoveryRedirect, req.URL.String())
}

// withRedirectPolicy copies c, never mutating a client the caller owns, and
// installs policy on the copy.
func withRedirectPolicy(c *http.Client, policy func(*http.Request, []*http.Request) error) *http.Client {
	copied := *c
	copied.CheckRedirect = policy
	return &copied
}
