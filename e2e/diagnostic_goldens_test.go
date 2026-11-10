package e2e

import (
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/baalimago/clai/e2e/e2etest"
)

var updateDiagnosticGoldens = flag.Bool("update-diagnostic-goldens", false, "rewrite e2e/testdata/diagnostics/*.golden from the current output")

// diagnosticGolden is one deterministic run whose stdout, stderr and exit
// status are pinned in e2e/testdata/diagnostics/<name>.golden.
type diagnosticGolden struct {
	name string
	env  map[string]string
	args string
	stub stubProvider
	// profile, when set, is written to the profiles directory first.
	profile string
}

// stubProvider selects the answer of the local OpenAI-shaped provider a
// golden talks to as model gpt-stub.
type stubProvider int

const (
	noStub stubProvider = iota
	stubUnauthorized
	stubToolCall
)

func diagnosticGoldens() []diagnosticGolden {
	return []diagnosticGolden{
		{name: "success", env: map[string]string{"DEBUG_LOOKBACK": "1"}, args: "-r -cm test -lb q hello"},
		{name: "warning", args: "-r -cm test -t no_such_tool q hello"},
		{name: "error", args: "-r -cm gpt-stub q hi", stub: stubUnauthorized},
		{name: "buffered", env: map[string]string{"DEBUG_CALL": "1"}, args: "-r -cm gpt-stub -t pwd q hi", stub: stubToolCall},
		{name: "help", args: "help"},
		{name: "json-query", args: "-json -r -cm gpt-stub -t pwd q hi", stub: stubToolCall},
		{name: "json-error", args: "-json -r -cm gpt-stub q hi", stub: stubUnauthorized},
		{name: "json-output", args: "-json profiles list", profile: "writer"},
	}
}

// TestDiagnosticOutputGoldens pins stdout, stderr and the exit status of the
// diagnostic scenarios. Regenerate with
// go test ./e2e -run '^TestDiagnosticOutputGoldens$' -args -update-diagnostic-goldens
func TestDiagnosticOutputGoldens(t *testing.T) {
	for _, tc := range diagnosticGoldens() {
		t.Run(tc.name, func(t *testing.T) {
			got := runDiagnosticGolden(t, tc)
			path := filepath.Join(e2etest.ModuleRoot(t), "e2e", "testdata", "diagnostics", tc.name+".golden")
			if *updateDiagnosticGoldens {
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatalf("create golden dir: %v", err)
				}
				if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
					t.Fatalf("write golden: %v", err)
				}
				return
			}
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read golden %s (regenerate with -update-diagnostic-goldens): %v", path, err)
			}
			if got != string(want) {
				t.Fatalf("%s differs from the current output:\n--- want\n%s\n--- got\n%s", path, want, got)
			}
		})
	}
}

func runDiagnosticGolden(t *testing.T, tc diagnosticGolden) string {
	t.Helper()
	confDir := e2etest.SetupMainTestConfigDir(t)
	for k, v := range tc.env {
		t.Setenv(k, v)
	}
	if tc.profile != "" {
		body := []byte(`{"model":"test","prompt":"You write short answers."}`)
		if err := os.WriteFile(filepath.Join(confDir, "profiles", tc.profile+".json"), body, 0o644); err != nil {
			t.Fatalf("write profile: %v", err)
		}
	}
	if tc.stub != noStub {
		startGoldenProvider(t, confDir, tc.stub)
	}
	var status int
	stdout, stderr := e2etest.CaptureStdoutStderr(t, func() {
		status = harness.Run(strings.Split(tc.args, " "))
	})
	home, _ := os.UserHomeDir()
	normalize := strings.NewReplacer(confDir, "$CONFIG", home, "$HOME").Replace
	return fmt.Sprintf("# clai %s\n# env: %s\n== status\n%d\n== stdout\n%s\n== stderr\n%s\n",
		tc.args, goldenEnv(tc.env), status, normalize(stdout), normalize(stderr))
}

func goldenEnv(env map[string]string) string {
	if len(env) == 0 {
		return "-"
	}
	parts := make([]string, 0, len(env))
	for k, v := range env {
		parts = append(parts, k+"="+v)
	}
	return strings.Join(parts, " ")
}

func startGoldenProvider(t *testing.T, confDir string, stub stubProvider) {
	t.Helper()
	original := http.DefaultTransport
	http.DefaultTransport = loopbackOnly{base: original}
	t.Cleanup(func() { http.DefaultTransport = original })
	var calls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		switch {
		case stub == stubUnauthorized:
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":{"message":"Incorrect API key provided","type":"invalid_request_error","code":"invalid_api_key"}}`))
		case calls == 1:
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_1\",\"type\":\"function\",\"function\":{\"name\":\"pwd\",\"arguments\":\"{}\"}}]}}]}\n\n")
			fmt.Fprint(w, "data: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"tool_calls\"}]}\n\ndata: [DONE]\n\n")
		default:
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"THE-REPLY\"}}]}\n\ndata: [DONE]\n\n")
		}
	}))
	t.Cleanup(server.Close)
	t.Setenv("OPENAI_API_KEY", "stub-key")
	conf, err := json.Marshal(map[string]any{"model": "gpt-stub", "url": server.URL + "/v1/chat/completions"})
	if err != nil {
		t.Fatalf("marshal model config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(confDir, "openai_gpt_gpt-stub.json"), conf, 0o644); err != nil {
		t.Fatalf("write model config: %v", err)
	}
}
