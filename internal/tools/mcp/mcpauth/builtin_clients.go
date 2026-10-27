package mcpauth

// GitHubIssuer is GitHub's OAuth authorization server, the one GitHub's
// hosted MCP server names. It offers no dynamic client registration.
const GitHubIssuer = "https://github.com/login/oauth"

// clai's GitHub OAuth App. GitHub requires the client secret at the token
// endpoint even with PKCE; in a client anyone can download it is not
// confidential, and PKCE is what binds a code to the attempt that asked for
// it. Empty until the app is registered.
const (
	githubClientID     = ""
	githubClientSecret = ""
)

// BuiltinClients is the pre-registered clients clai ships, keyed by issuer,
// for WithPreregisteredClients.
func BuiltinClients() map[string]ClientRegistration {
	return builtinClients(githubClientID, githubClientSecret)
}

func builtinClients(githubID, githubSecret string) map[string]ClientRegistration {
	clients := map[string]ClientRegistration{}
	if githubID != "" {
		clients[GitHubIssuer] = ClientRegistration{ClientID: githubID, ClientSecret: githubSecret}
	}
	return clients
}
