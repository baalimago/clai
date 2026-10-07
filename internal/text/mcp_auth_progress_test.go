package text

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/baalimago/clai/internal/tools/mcp"
	"github.com/baalimago/clai/internal/tools/mcp/httptestserver"
	"github.com/baalimago/clai/internal/tools/mcp/mcpauth"
	"github.com/baalimago/clai/internal/tools/mcp/oauthtestserver"
	"github.com/baalimago/clai/pkg/claierr"
	pub_models "github.com/baalimago/clai/pkg/text/models"
	"github.com/baalimago/go_away_boilerplate/pkg/testboil"
)

type progressCheckingBrowser func(string) error

func (f progressCheckingBrowser) Open(url string) error { return f(url) }

func TestQueryAuthProgressAtStartupAndLazyToolCall(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	for _, mode := range []mcpLogMode{mcpLogRolling, mcpLogPlainDirect, mcpLogRawStructured} {
		for _, lazy := range []bool{false, true} {
			t.Run(fmt.Sprintf("mode=%d/lazy=%t", mode, lazy), func(t *testing.T) {
				as := oauthtestserver.New()
				defer as.Close()
				hs := httptestserver.New()
				defer hs.Close()
				as.Configure(func(c *oauthtestserver.Config) { c.Resource = hs.URL; c.FixedAccessToken = "progress-secret-token" })
				hs.Configure(func(c *httptestserver.Config) {
					c.RequireBearerToken = "progress-secret-token"
					c.ChallengeResourceMetaURL = as.ProtectedResourceURL()
				})
				ctx, cancel := httpSetupTestContext()
				defer cancel()
				var diagnostic bytes.Buffer
				sink := newMcpLogSinkTo(mode, &diagnostic)
				sink.termWidth = func() int { return 120 }
				sink.termHeight = func() int { return 40 }
				authz := newMcpAuthorizer(t.TempDir(), strings.NewReader(""), true, sink)
				autoBrowser := oauthtestserver.NewAutoFollowOpener()
				mcpauth.WithBrowserOpener(progressCheckingBrowser(func(url string) error {
					if output := diagnostic.String(); !strings.Contains(output, url) || !strings.Contains(output, "open this URL manually") {
						t.Errorf("before browser wait: missing manual URL in %q", output)
					}
					return autoBrowser.Open(url)
				}))(authz)
				server := pub_models.McpServer{Name: "httpecho", Url: hs.URL}
				stdout := testboil.CaptureStdout(t, func(t *testing.T) {
					if !lazy {
						conn, _, _, err := handshakeHttpServerWithAuth(ctx, ctx, authz, server, sink)
						if err != nil {
							t.Fatalf("startup auth: %v", err)
						}
						defer conn.Close()
						return
					}
					// Warm-cache tools resolve only when the model calls them.
					sink.setupSucceeded()
					sink.attach()
					connector := newAuthenticatingHttpConnector(ctx, authz, server, sink)
					tool := mcp.NewTool(connector, "echo", pub_models.Specification{Name: "mcp_httpecho_echo"}, 0, server.Name, httpChallengeResolver(authz, server), 5*time.Second)
					q := &Querier[*MockQuerier]{out: &bytes.Buffer{}, mcpSink: sink, tooling: tooling{run: map[string]pub_models.LLMTool{"mcp_httpecho_echo": tool}}}
					e := toolExecutor[*MockQuerier]{querier: q}
					if result := e.invokeToolCall(ctx, pub_models.Call{Name: "mcp_httpecho_echo", Inputs: &pub_models.Input{"text": "hello"}}); result != "hello" {
						t.Fatalf("lazy tool call = %q, want hello", result)
					}
				})
				if stdout != "" {
					t.Errorf("auth diagnostics escaped to stdout: %q", stdout)
				}
				output := diagnostic.String()
				for _, stage := range []string{"▸ mcp.auth", "Discovering", "Registering", "Waiting for browser authorization", "Exchanging", "Saving"} {
					if !strings.Contains(output, stage) {
						t.Errorf("diagnostics lack %q: %q", stage, output)
					}
				}
				if strings.Contains(output, "progress-secret-token") {
					t.Fatal("diagnostics leaked the access token")
				}
			})
		}
	}
}

func TestQueryHeadlessAuthNeverPrintsOrOpensBrowser(t *testing.T) {
	var output bytes.Buffer
	authz := newMcpAuthorizer(t.TempDir(), strings.NewReader(""), false, newMcpLogSinkTo(mcpLogRawStructured, &output))
	mcpauth.WithBrowserOpener(progressCheckingBrowser(func(string) error { t.Fatal("headless auth opened a browser"); return nil }))(authz)
	_, err := authz.AuthorizeInteractive(context.Background(), pub_models.McpServer{Name: "test", Url: "https://example.invalid/mcp"}, &claierr.AuthChallengeError{})
	if err == nil || output.Len() != 0 {
		t.Fatalf("headless auth: error %v, output %q", err, output.String())
	}
}

func TestQueryAuthShowsManualLinkWhenBrowserDoesNotRedirect(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	as := oauthtestserver.New()
	defer as.Close()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	var output bytes.Buffer
	authz := newMcpAuthorizer(t.TempDir(), strings.NewReader(""), true, newMcpLogSinkTo(mcpLogRolling, &output))
	mcpauth.WithBrowserOpener(progressCheckingBrowser(func(url string) error {
		if !strings.Contains(output.String(), url) {
			t.Error("manual authorization URL was not shown before the wait")
		}
		cancel()
		return nil
	}))(authz)
	err := httpChallengeResolver(authz, pub_models.McpServer{Name: "test", Url: as.URL}).ResolveChallenge(ctx, &claierr.AuthChallengeError{ServerName: "test", ResourceMetadata: as.ProtectedResourceURL()})
	if !errors.Is(err, context.Canceled) || !strings.Contains(output.String(), "Waiting for browser authorization") {
		t.Fatalf("cancelled wait: error %v, output %q", err, output.String())
	}
}
