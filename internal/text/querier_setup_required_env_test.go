package text

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync/atomic"
	"testing"

	"github.com/baalimago/clai/internal/tools"
	"github.com/baalimago/clai/internal/tools/mcp/mcpauth"
	"github.com/baalimago/clai/internal/tools/mcp/serverconfig"
	"github.com/baalimago/clai/pkg/claierr"
	pub_models "github.com/baalimago/clai/pkg/text/models"
)

// An eager server, command or url, stops at a missing auth.required_env
// before spawning or sending anything, and an explicit one reports it
// typed, at the env stage. (Strict startup resolves every explicit server
// eagerly; the lazy paths are below.)
func Test_setupMcpManager_MissingRequiredEnvFailsEagerServersBeforeConnecting(t *testing.T) {
	var requests int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&requests, 1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	needs := &pub_models.McpServerAuth{RequiredEnv: []string{"CLAI_RE_SETUP_UNSET"}}
	conf := Configurations{
		AgentSettings: &AgentSettings{StrictMcpStartup: true},
		McpServers: []pub_models.McpServer{
			// The test server would start and handshake fine: only the
			// check can fail these.
			{Name: "eager-stdio", Command: testServerBinary(t), Startup: pub_models.StartupEager, Auth: needs},
			{Name: "eager-url", Url: srv.URL, Startup: pub_models.StartupEager, Auth: needs},
		},
	}

	got, err := setupMcpManager(t.Context(), t.TempDir(), conf, &recordingSuccessSink{}, testSchemaCache(t), nil)
	if len(got) != 0 {
		t.Errorf("servers missing a variable registered tools: %v", got)
	}
	joined, ok := err.(interface{ Unwrap() []error })
	if !ok {
		t.Fatalf("err = %v, want the joined explicit failures", err)
	}
	var failed []string
	for _, e := range joined.Unwrap() {
		var startup *claierr.McpServerStartupError
		var missing *serverconfig.MissingEnvError
		if !errors.As(e, &startup) || startup.Stage != "env" || !errors.As(e, &missing) {
			t.Errorf("err %v: want a *claierr.McpServerStartupError at stage env wrapping *serverconfig.MissingEnvError", e)
			continue
		}
		failed = append(failed, startup.ServerName)
	}
	slices.Sort(failed)
	if !slices.Equal(failed, []string{"eager-stdio", "eager-url"}) {
		t.Errorf("failed servers = %v", failed)
	}
	if n := atomic.LoadInt32(&requests); n != 0 {
		t.Errorf("endpoint received %d requests, want 0", n)
	}
}

// The lazy cache-miss connect, command or url, stops at the same check.
func TestResolveLazyServerViaCache_MissingRequiredEnvBeforeConnecting(t *testing.T) {
	var requests int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&requests, 1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	needs := &pub_models.McpServerAuth{RequiredEnv: []string{"CLAI_RE_SETUP_UNSET"}}

	stdio := pub_models.McpServer{Name: "lazy-stdio", Command: testServerBinary(t), Auth: needs}
	err := resolveLazyServerViaCache(t.Context(), testSchemaCache(t), stdio, &recordingSuccessSink{}, tools.NewRegistry(), false)
	var missing *serverconfig.MissingEnvError
	if !errors.As(err, &missing) || missing.ServerName != "lazy-stdio" {
		t.Fatalf("stdio: err = %v, want *serverconfig.MissingEnvError", err)
	}

	endpoint := pub_models.McpServer{Name: "lazy-url", Url: srv.URL, Auth: needs}
	err = resolveLazyHttpServerViaCache(t.Context(), testSchemaCache(t), endpoint, &recordingSuccessSink{}, tools.NewRegistry(), nil, false)
	if !errors.As(err, &missing) || missing.ServerName != "lazy-url" {
		t.Fatalf("url: err = %v, want *serverconfig.MissingEnvError", err)
	}
	if n := atomic.LoadInt32(&requests); n != 0 {
		t.Errorf("endpoint received %d requests, want 0", n)
	}
}

// The lazy endpoint connector memoises a missing variable: setting it
// mid-run does not make a later tool call dial again, and nothing is sent.
func TestAuthenticatingHttpConnector_MissingRequiredEnvDialsOnce(t *testing.T) {
	for _, tc := range []struct {
		name  string
		authz func(t *testing.T) *mcpauth.Authorizer
	}{
		{"with an authorizer", func(t *testing.T) *mcpauth.Authorizer { return newMcpAuthorizer(t.TempDir(), nil, false, nil) }},
		{"without an authorizer", func(*testing.T) *mcpauth.Authorizer { return nil }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var requests int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				atomic.AddInt32(&requests, 1)
				w.WriteHeader(http.StatusInternalServerError)
			}))
			defer srv.Close()
			server := pub_models.McpServer{Name: "lazy-url", Url: srv.URL, Auth: &pub_models.McpServerAuth{RequiredEnv: []string{"CLAI_RE_LAZY_HTTP"}}}
			connector := newAuthenticatingHttpConnector(t.Context(), tc.authz(t), server, &recordingSuccessSink{})

			var missing *serverconfig.MissingEnvError
			if _, err := connector.Conn(t.Context()); !errors.As(err, &missing) {
				t.Fatalf("first call: err = %v, want *serverconfig.MissingEnvError", err)
			}
			// Were the connector to dial again, it would now pass the check
			// and reach the endpoint.
			t.Setenv("CLAI_RE_LAZY_HTTP", "set-too-late")
			for i := range 3 {
				if _, err := connector.Conn(t.Context()); !errors.As(err, &missing) {
					t.Fatalf("call %d: err = %v, want the memoised *serverconfig.MissingEnvError", i+2, err)
				}
			}
			if n := atomic.LoadInt32(&requests); n != 0 {
				t.Fatalf("endpoint received %d requests, want 0: the connector dialled again", n)
			}
		})
	}
}
