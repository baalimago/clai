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
// authPrintWriter capability mcpLogSink exposes, so authOutputFor can be
// tested without the real sink's buffering/rolling-output machinery.
type fakeAuthPrintWriterSink struct {
	buf bytes.Buffer
}

func (f *fakeAuthPrintWriterSink) AppendServerLog(string, string) {}
func (f *fakeAuthPrintWriterSink) ServerExited(string)            {}
func (f *fakeAuthPrintWriterSink) AuthPrintWriter() io.Writer     { return &f.buf }

func TestAuthOutputRoutesThroughSinkNotStdout(t *testing.T) {
	sink := &fakeAuthPrintWriterSink{}
	if _, err := io.WriteString(authOutputFor(sink), "https://example.invalid/authorize?state=x"); err != nil {
		t.Fatal(err)
	}
	if got := sink.buf.String(); !strings.Contains(got, "https://example.invalid/authorize?state=x") {
		t.Errorf("sink captured %q, want it to contain the authorization URL", got)
	}
}

func TestAuthOutputWithoutConfiguredWriterNeverUsesProcessStreams(t *testing.T) {
	if got := authOutputFor(nil); got != io.Discard {
		t.Error("authOutputFor(nil) must discard output")
	}
	if got := authOutputFor(&recordingSuccessSink{}); got != io.Discard {
		t.Error("authOutputFor(plain sink) must discard output")
	}
}
