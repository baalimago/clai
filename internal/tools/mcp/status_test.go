package mcp

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/baalimago/clai/pkg/claierr"
	pub_models "github.com/baalimago/clai/pkg/text/models"
)

// fakeLauncher puts an executable named bin on PATH so a bound or label test
// never depends on the host's toolchain.
func fakeLauncher(t *testing.T, bin string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, bin), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write fake %q: %v", bin, err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// TestConnectBoundOfScalesWithInstallEvidence pins the whole point of the
// install bound: a command line with no local evidence of its content is
// bounded as a download, everything else as a handshake, and an operator's own
// connect_timeout_seconds wins over both.
func TestConnectBoundOfScalesWithInstallEvidence(t *testing.T) {
	fakeLauncher(t, "uvx")
	tests := []struct {
		name   string
		server pub_models.McpServer
		want   time.Duration
	}{
		{
			name:   "direct binary is a handshake",
			server: pub_models.McpServer{Command: "sh", Args: []string{"-c", "true"}},
			want:   mcpConnectBound,
		},
		{
			name:   "unevidenced runner is a download",
			server: pub_models.McpServer{Command: "uvx", Args: []string{"duckduckgo-mcp-server==0.7.0"}},
			want:   mcpInstallBound,
		},
		{
			name:   "configured bound wins over the install bound",
			server: pub_models.McpServer{Command: "uvx", Args: []string{"pkg"}, ConnectTimeoutSeconds: 7},
			want:   7 * time.Second,
		},
	}
	for _, tt := range tests {
		if got := ConnectBoundOf(tt.server); got != tt.want {
			t.Errorf("%s: ConnectBoundOf = %v, want %v", tt.name, got, tt.want)
		}
	}
}

// TestReportBoundExpiryNamesInstallWhenADownloadMayBeRunning pins the reported
// failure: an install-shaped command line that outlives its bound says so, and
// states the wait it was given and the knob that raises it.
func TestReportBoundExpiryNamesInstallWhenADownloadMayBeRunning(t *testing.T) {
	fakeLauncher(t, "uvx")
	server := pub_models.McpServer{Name: "ddg", Command: "uvx", Args: []string{"duckduckgo-mcp-server==0.7.0"}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := ReportBoundExpiry(context.DeadlineExceeded, ctx, server)

	var startupErr *claierr.McpServerStartupError
	if !errors.As(err, &startupErr) {
		t.Fatalf("err = %v, want *claierr.McpServerStartupError", err)
	}
	if startupErr.Stage != claierr.McpStageInstall {
		t.Errorf("Stage = %q, want %q", startupErr.Stage, claierr.McpStageInstall)
	}
	for _, want := range []string{"10m0s", "connect_timeout_seconds", "first-run install"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("error %v lost its cause", err)
	}
}

// TestReportBoundExpiryNamesConnectForAnEvidencedServer pins the other half:
// a server clai can see is not an install, so its expiry stays a connect
// stage and the cause survives untouched.
func TestReportBoundExpiryNamesConnectForAnEvidencedServer(t *testing.T) {
	server := pub_models.McpServer{Name: "fs", Command: "sh", Args: []string{"-c", "true"}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := ReportBoundExpiry(context.DeadlineExceeded, ctx, server)

	var startupErr *claierr.McpServerStartupError
	if !errors.As(err, &startupErr) {
		t.Fatalf("err = %v, want *claierr.McpServerStartupError", err)
	}
	if startupErr.Stage != claierr.McpStageConnect {
		t.Errorf("Stage = %q, want %q", startupErr.Stage, claierr.McpStageConnect)
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("error %v lost its cause", err)
	}
}

// TestReportBoundExpiryLeavesALiveBoundAlone pins that a failure which is not
// the bound's doing keeps the shape its producer gave it.
func TestReportBoundExpiryLeavesALiveBoundAlone(t *testing.T) {
	server := pub_models.McpServer{Name: "ddg", Command: "uvx"}
	want := errors.New("tools/list: boom")
	if got := ReportBoundExpiry(want, context.Background(), server); !errors.Is(got, want) {
		t.Errorf("err = %v, want the original failure unchanged", got)
	}
}

// TestStageHandshakeFailureTypesARawError pins that a handshake error without a
// stage of its own is named as an initialize failure rather than reaching the
// caller's funnel, which labelled every raw error a credential failure.
func TestStageHandshakeFailureTypesARawError(t *testing.T) {
	server := pub_models.McpServer{Name: "svc", Command: "sh"}
	got := stageHandshakeFailure(context.Background(), server, errors.New("stdin closed"))
	var startupErr *claierr.McpServerStartupError
	if !errors.As(got, &startupErr) {
		t.Fatalf("err = %v, want *claierr.McpServerStartupError", got)
	}
	if startupErr.Stage != claierr.McpStageInitialize {
		t.Errorf("Stage = %q, want %q", startupErr.Stage, claierr.McpStageInitialize)
	}
}

// TestStageHandshakeFailureKeepsATypedStage pins that a failure which already
// names its stage is not renamed.
func TestStageHandshakeFailureKeepsATypedStage(t *testing.T) {
	server := pub_models.McpServer{Name: "svc", Command: "sh"}
	want := claierr.NewMcpServerStartup("svc", "tools/list", errors.New("bad shape"))
	got := stageHandshakeFailure(context.Background(), server, want)
	var startupErr *claierr.McpServerStartupError
	if !errors.As(got, &startupErr) || startupErr.Stage != "tools/list" {
		t.Errorf("err = %v, want the producer's own stage kept", got)
	}
}

// recordingConnectSink records the labels it is told a connect is starting
// with, and how often the finish function was called.
type recordingConnectSink struct {
	labels  []string
	servers []string
	done    int
}

func (s *recordingConnectSink) AppendServerLog(string, string) {}
func (s *recordingConnectSink) ServerExited(string)            {}

func (s *recordingConnectSink) ConnectStarted(server, label string) func() {
	s.servers = append(s.servers, server)
	s.labels = append(s.labels, label)
	return func() { s.done++ }
}

// silentSink implements only ServerLogSink, so announcement must be a no-op
// rather than a panic or a silent nil function.
type silentSink struct{}

func (silentSink) AppendServerLog(string, string) {}
func (silentSink) ServerExited(string)            {}

// TestAnnounceConnectLabelsWhatTheWaitIsFor pins the label a status line
// draws: an installing wait names the package when the command line spells it
// out, and a plain start names nothing it cannot see.
func TestAnnounceConnectLabelsWhatTheWaitIsFor(t *testing.T) {
	fakeLauncher(t, "uvx")
	sink := &recordingConnectSink{}
	installing := pub_models.McpServer{Name: "ddg", Command: "uvx", Args: []string{"duckduckgo-mcp-server==0.7.0"}}
	started := pub_models.McpServer{Name: "fs", Command: "sh", Args: []string{"-c", "true"}}

	AnnounceConnect(sink, installing)()
	AnnounceConnect(sink, started)()

	want := []string{"installing duckduckgo-mcp-server", "starting"}
	if len(sink.labels) != len(want) {
		t.Fatalf("labels = %v, want %v", sink.labels, want)
	}
	for i, w := range want {
		if sink.labels[i] != w {
			t.Errorf("labels[%d] = %q, want %q", i, sink.labels[i], w)
		}
	}
	if sink.done != 2 {
		t.Errorf("done calls = %d, want 2", sink.done)
	}
}

// TestAnnounceConnectIsANoopForAPlainSink pins that a sink which shows nothing
// still yields a function the caller may defer.
func TestAnnounceConnectIsANoopForAPlainSink(t *testing.T) {
	sink := silentSink{}
	if done := AnnounceConnect(sink, pub_models.McpServer{Name: "fs", Command: "sh"}); done == nil {
		t.Fatal("AnnounceConnect returned nil done for a plain sink")
	} else {
		done()
	}
}
