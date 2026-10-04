package text

import (
	"errors"
	"testing"

	"github.com/baalimago/clai/pkg/claierr"
)

// TestIsUnknownToolFailureRequiresUnknownMarkerFor32602InvalidParams pins
// R1-20: -32602 is the ordinary invalid-params code a server returns for a
// validation error from the tool itself, so merely containing the tool name
// must not be enough to invalidate the entry; only -32601 alone, or -32602
// paired with an unknown/not-found marker in the message, counts.
func TestIsUnknownToolFailureRequiresUnknownMarkerFor32602InvalidParams(t *testing.T) {
	err := claierr.NewMcpRPCError("fixture", -32602, "invalid params: create_issue requires title")
	if isUnknownToolFailure(nil, err, "create_issue") {
		t.Fatal("an ordinary validation error must not be classified as the unknown-tool signal")
	}
}

// TestIsUnknownToolFailureAcceptsMethodNotFoundAlone pins that -32601 naming
// the tool is sufficient on its own: method-not-found has no other ordinary
// meaning for a tools/call.
func TestIsUnknownToolFailureAcceptsMethodNotFoundAlone(t *testing.T) {
	err := claierr.NewMcpRPCError("fixture", -32601, "method not found: ghost")
	if !isUnknownToolFailure(nil, err, "ghost") {
		t.Fatal("expected -32601 naming the tool to be classified as the unknown-tool signal")
	}
}

// TestIsUnknownToolFailureAcceptsInvalidParamsWithUnknownMarker pins the
// surviving -32602 case: a message that both names the tool and carries an
// unknown/not-found marker is still the unknown-tool signal.
func TestIsUnknownToolFailureAcceptsInvalidParamsWithUnknownMarker(t *testing.T) {
	err := claierr.NewMcpRPCError("fixture", -32602, "unknown tool: ghost")
	if !isUnknownToolFailure(nil, err, "ghost") {
		t.Fatal("expected -32602 with an unknown marker naming the tool to be classified as the unknown-tool signal")
	}
}

// TestIsUnknownToolFailureIgnoresUnrelatedErrorType pins that an error which
// is not *claierr.McpRPCError (e.g. a plain transport failure) never counts,
// regardless of its text naming the tool and "unknown".
func TestIsUnknownToolFailureIgnoresUnrelatedErrorType(t *testing.T) {
	err := claierr.NewMcpTransport("fixture", "https://example.invalid", errors.New("unknown tool: ghost"))
	if isUnknownToolFailure(nil, err, "ghost") {
		t.Fatal("a non-RPC error must never be classified as the unknown-tool signal")
	}
}
