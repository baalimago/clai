package mcp

import (
	"errors"
	"testing"
)

// TestMcpAuthErrorMessages pins the user-facing text and the Unwrap chain of
// the typed `clai mcp auth` errors, so callers can keep matching on them with
// errors.Is/As while the messages stay stable for humans.
func TestMcpAuthErrorMessages(t *testing.T) {
	cause := errors.New("boom")

	tests := []struct {
		name      string
		err       error
		wantError string
		wantCause error
	}{
		{
			name:      "usage",
			err:       &McpAuthUsageError{},
			wantError: "mcp auth: a server name is required, e.g. 'clai mcp auth <server>'",
		},
		{
			name:      "config dir",
			err:       &McpAuthConfigDirError{Cause: cause},
			wantError: "mcp auth: resolve clai config dir: boom",
			wantCause: cause,
		},
		{
			name:      "server config",
			err:       &McpAuthServerConfigError{Name: "linear", Path: "/cfg/linear.json", Stage: "read", Cause: cause},
			wantError: `mcp auth: read server config "/cfg/linear.json": boom`,
			wantCause: cause,
		},
		{
			name:      "not endpoint based",
			err:       &McpAuthNotEndpointBasedError{Name: "linear"},
			wantError: `mcp auth: server "linear" has no "url"; only an endpoint-based server can be authorized interactively`,
		},
		{
			name:      "no challenge",
			err:       &McpAuthNoChallengeError{Name: "linear"},
			wantError: `mcp auth: server "linear" answered with no authorization challenge; nothing to authorize`,
		},
		{
			name:      "connect",
			err:       &McpAuthConnectError{Name: "linear", Cause: cause},
			wantError: `mcp auth: connect to "linear": boom`,
			wantCause: cause,
		},
		{
			name:      "flow",
			err:       &McpAuthFlowError{Name: "linear", Cause: cause},
			wantError: `mcp auth: authorize "linear": boom`,
			wantCause: cause,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.err.Error(); got != tc.wantError {
				t.Fatalf("Error() = %q, want %q", got, tc.wantError)
			}
			if tc.wantCause == nil {
				return
			}
			if got := errors.Unwrap(tc.err); !errors.Is(got, tc.wantCause) {
				t.Fatalf("Unwrap() = %v, want %v", got, tc.wantCause)
			}
		})
	}
}
