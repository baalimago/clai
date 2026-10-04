package text

import (
	"bytes"
	"io"
	"strings"
	"testing"
	"time"

	pub_models "github.com/baalimago/clai/pkg/text/models"
)

// TestNonTerminalSessionDefaultsToFailFast pins the auth-timeout parameter's
// D22 default: a server with no explicit auth_timeout_seconds fails fast
// (0) when the session's output is not a terminal, and waits up to the
// 120-second default when it is. An explicit value overrides either way,
// including an explicit 0 that would otherwise get the terminal default.
func TestNonTerminalSessionDefaultsToFailFast(t *testing.T) {
	tests := []struct {
		name             string
		server           pub_models.McpServer
		outputIsTerminal bool
		want             time.Duration
	}{
		{"unset, non-terminal", pub_models.McpServer{}, false, 0},
		{"unset, terminal", pub_models.McpServer{}, true, defaultAuthTimeoutOnTerminal},
		{"explicit value, non-terminal", pub_models.McpServer{AuthTimeoutSeconds: new(30)}, false, 30 * time.Second},
		{"explicit value, terminal", pub_models.McpServer{AuthTimeoutSeconds: new(30)}, true, 30 * time.Second},
		{"explicit zero, terminal overrides the default", pub_models.McpServer{AuthTimeoutSeconds: new(0)}, true, 0},
		{"explicit zero, non-terminal", pub_models.McpServer{AuthTimeoutSeconds: new(0)}, false, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := resolveAuthTimeout(tt.server, tt.outputIsTerminal); got != tt.want {
				t.Errorf("resolveAuthTimeout(%+v, %v) = %v, want %v", tt.server, tt.outputIsTerminal, got, tt.want)
			}
		})
	}
}

// fakeAuthPrintWriterSink implements both mcp.ServerLogSink and the
// authPrintWriter capability mcpLogSink exposes, so authPrintURLFor can be
// tested without the real sink's buffering/rolling-output machinery.
type fakeAuthPrintWriterSink struct {
	buf bytes.Buffer
}

func (f *fakeAuthPrintWriterSink) AppendServerLog(string, string) {}
func (f *fakeAuthPrintWriterSink) ServerExited(string)            {}
func (f *fakeAuthPrintWriterSink) AuthPrintWriter() io.Writer     { return &f.buf }

// TestAuthPrintURLRoutesThroughSinkNotStdout pins R2-08's third fact: the
// printed-URL fallback must write through the sink's own injectable writer
// rather than the process's raw stdout, which a library consumer's own
// output must never be written to.
func TestAuthPrintURLRoutesThroughSinkNotStdout(t *testing.T) {
	sink := &fakeAuthPrintWriterSink{}
	printURL := authPrintURLFor(sink)
	if printURL == nil {
		t.Fatal("authPrintURLFor returned nil for a sink implementing AuthPrintWriter")
	}
	printURL("https://example.invalid/authorize?state=x")
	if got := sink.buf.String(); !strings.Contains(got, "https://example.invalid/authorize?state=x") {
		t.Errorf("sink captured %q, want it to contain the authorization URL", got)
	}
}

// TestAuthPrintURLFallsBackToDefaultForAPlainSink pins the other half: a
// sink with no AuthPrintWriter capability (or none at all) leaves the
// Authorizer's own default in place rather than panicking or silently
// dropping the URL — defaultPrintURL's own test already pins where that
// default writes to.
func TestAuthPrintURLFallsBackToDefaultForAPlainSink(t *testing.T) {
	if got := authPrintURLFor(nil); got != nil {
		t.Error("authPrintURLFor(nil) is non-nil, want nil so the Authorizer keeps its own default")
	}
	if got := authPrintURLFor(&recordingSuccessSink{}); got != nil {
		t.Error("authPrintURLFor(plain sink) is non-nil, want nil so the Authorizer keeps its own default")
	}
}
