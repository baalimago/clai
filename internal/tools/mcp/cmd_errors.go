package mcp

import "fmt"

// McpAuthUsageError means "clai mcp auth" was invoked with no server name.
type McpAuthUsageError struct{}

func (e *McpAuthUsageError) Error() string {
	return "mcp auth: a server name is required, e.g. 'clai mcp auth <server>'"
}

// McpAuthConfigDirError means the clai config directory could not be
// resolved, so no server config could even be looked up.
type McpAuthConfigDirError struct{ Cause error }

func (e *McpAuthConfigDirError) Error() string {
	return fmt.Sprintf("mcp auth: resolve clai config dir: %v", e.Cause)
}

func (e *McpAuthConfigDirError) Unwrap() error { return e.Cause }

// McpAuthServerConfigError means the named server's config file could not
// be read, parsed or validated. Stage names which of those failed.
type McpAuthServerConfigError struct {
	Name  string
	Path  string
	Stage string // "read" or "parse"
	Cause error
}

func (e *McpAuthServerConfigError) Error() string {
	return fmt.Sprintf("mcp auth: %s server config %q: %v", e.Stage, e.Path, e.Cause)
}

func (e *McpAuthServerConfigError) Unwrap() error { return e.Cause }

// McpAuthNotEndpointBasedError means the named server has no "url", so it
// cannot be authorized interactively: only an endpoint-based server speaks
// OAuth.
type McpAuthNotEndpointBasedError struct{ Name string }

func (e *McpAuthNotEndpointBasedError) Error() string {
	return fmt.Sprintf("mcp auth: server %q has no \"url\"; only an endpoint-based server can be authorized interactively", e.Name)
}

// McpAuthNoChallengeError means a bare connect to the named server
// succeeded with no authorization challenge, so there is nothing for this
// subcommand to authorize.
type McpAuthNoChallengeError struct{ Name string }

func (e *McpAuthNoChallengeError) Error() string {
	return fmt.Sprintf("mcp auth: server %q answered with no authorization challenge; nothing to authorize", e.Name)
}

// McpAuthConnectError means a bare connect to the named server failed for a
// reason other than an authorization challenge.
type McpAuthConnectError struct {
	Name  string
	Cause error
}

func (e *McpAuthConnectError) Error() string {
	return fmt.Sprintf("mcp auth: connect to %q: %v", e.Name, e.Cause)
}

func (e *McpAuthConnectError) Unwrap() error { return e.Cause }

// McpAuthFlowError means the interactive authorization flow itself failed
// after a challenge was received.
type McpAuthFlowError struct {
	Name  string
	Cause error
}

func (e *McpAuthFlowError) Error() string {
	return fmt.Sprintf("mcp auth: authorize %q: %v", e.Name, e.Cause)
}

func (e *McpAuthFlowError) Unwrap() error { return e.Cause }
