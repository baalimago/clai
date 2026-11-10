package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/baalimago/clai/internal/models"
	"github.com/baalimago/clai/internal/runenv"
	"github.com/baalimago/clai/pkg/diagnostic"
)

type cliRun struct {
	status         int
	stdout, stderr string
}

type cliOpts struct {
	stderrTerminal bool
	stderr         *failingWriter
}

func scenarioDeps() Deps {
	deps := DefaultDeps()
	deps.NewSummarizer = func(context.Context, string) (models.Summarizer, error) {
		return nil, errors.New("the summarizer is off in this test binary")
	}
	return deps
}

// runCLI runs clai in-process over buffers from a fresh working directory,
// so skills discovery reads nothing of the repository.
func runCLI(t *testing.T, args string, o cliOpts) cliRun {
	t.Helper()
	t.Chdir(t.TempDir())
	var stdout, stderr bytes.Buffer
	p := osProcess()
	p.stdin = strings.NewReader("")
	p.stdout = &stdout
	p.stderr = &stderr
	if o.stderr != nil {
		p.stderr = o.stderr
	}
	p.stdinTerminal, p.stdoutTerminal, p.stderrTerminal = false, false, o.stderrTerminal
	status := run(strings.Fields(args), scenarioDeps(), p)
	return cliRun{status: status, stdout: stdout.String(), stderr: stderr.String()}
}

// stubProvider serves model gpt-stub. unauthorized refuses with a 401;
// otherwise the first answer calls pwd once per pad and later answers
// stream THE-REPLY.
func stubProvider(t *testing.T, unauthorized bool, pads ...string) {
	t.Helper()
	confDir := os.Getenv("CLAI_CONFIG_DIR")
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		n := calls.Add(1)
		if unauthorized {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":{"message":"Incorrect API key provided","code":"invalid_api_key"}}`))
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		if n == 1 && len(pads) > 0 {
			for i, pad := range pads {
				args, _ := json.Marshal(fmt.Sprintf(`{"pad":%q}`, pad))
				fmt.Fprintf(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":%d,\"id\":\"call_%d\",\"type\":\"function\",\"function\":{\"name\":\"pwd\",\"arguments\":%s}}]}}]}\n\n", i, i, args)
			}
			fmt.Fprint(w, "data: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"tool_calls\"}]}\n\ndata: [DONE]\n\n")
			return
		}
		fmt.Fprint(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"THE-REPLY\"}}]}\n\ndata: [DONE]\n\n")
	}))
	t.Cleanup(server.Close)
	t.Setenv("OPENAI_API_KEY", "stub-key")
	conf, _ := json.Marshal(map[string]any{"model": "gpt-stub", "url": server.URL + "/v1/chat/completions"})
	if err := os.MkdirAll(confDir, 0o755); err != nil {
		t.Fatalf("create config dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(confDir, "openai_gpt_gpt-stub.json"), conf, 0o644); err != nil {
		t.Fatalf("write model config: %v", err)
	}
}

func useConfigDir(t *testing.T) {
	t.Helper()
	t.Setenv("OPENROUTER_API_KEY", "")
	t.Setenv("CLAI_CONFIG_DIR", filepath.Join(t.TempDir(), "config"))
	t.Setenv("CLAI_CACHE_DIR", filepath.Join(t.TempDir(), "cache"))
}

func assertNoDiagnosticsIn(t *testing.T, stdout string) {
	t.Helper()
	for _, marker := range []string{"warn [", "debug [", "trace [", "info [", "error ["} {
		if strings.Contains(stdout, marker) {
			t.Fatalf("stdout carries diagnostic %q: %q", marker, stdout)
		}
	}
}

const unknownToolWarning = "warn [text_querier] attempted to select unknown tool 'no_such_tool'"

// TestDiagnosticCLIScenarios proves criteria 1-3 and 5: results stay on
// stdout and diagnostics on stderr in every output mode, terminal failures
// appear once, registered types use their mode and spill files, and levels,
// colors and every Legacy controls row follow the root facts.
func TestDiagnosticCLIScenarios(t *testing.T) {
	// R1-01: producers that ran on a context without the run's Env were
	// silent in clai; each reaches the run's stderr now.
	t.Run("config migration notice", func(t *testing.T) {
		useConfigDir(t)
		confDir := os.Getenv("CLAI_CONFIG_DIR")
		if err := os.MkdirAll(confDir, 0o755); err != nil {
			t.Fatalf("create config dir: %v", err)
		}
		if err := os.WriteFile(filepath.Join(confDir, "chatConfig.json"), []byte(`{"model":"test","system_prompt":"be brief"}`), 0o644); err != nil {
			t.Fatalf("write old chat config: %v", err)
		}
		got := runCLI(t, "-r -cm test q hello", cliOpts{})
		if got.status != 0 || got.stdout != "hello\n" || strings.Count(got.stderr, "migrating old chat config") != 1 {
			t.Fatalf("got %+v, want the reply on stdout and the migration notice once on stderr", got)
		}
	})
	t.Run("DEBUG_SUMMARY traces an unavailable summarizer", func(t *testing.T) {
		useConfigDir(t)
		confDir := os.Getenv("CLAI_CONFIG_DIR")
		if err := os.MkdirAll(confDir, 0o755); err != nil {
			t.Fatalf("create config dir: %v", err)
		}
		if err := os.WriteFile(filepath.Join(confDir, "textConfig.json"), []byte(`{"model":"test","summarize-conversations":true}`), 0o644); err != nil {
			t.Fatalf("write text config: %v", err)
		}
		t.Setenv("DEBUG_SUMMARY", "1")
		got := runCLI(t, "-r -cm test q hello", cliOpts{})
		if got.status != 0 || got.stdout != "hello\n" || !strings.Contains(got.stderr, "[DEBUG_SUMMARY] summarizer unavailable") {
			t.Fatalf("got %+v, want the reply on stdout and the summary trace on stderr", got)
		}
		assertNoDiagnosticsIn(t, got.stdout)
	})
	t.Run("success", func(t *testing.T) {
		useConfigDir(t)
		got := runCLI(t, "-r -cm test q hello", cliOpts{})
		if got.status != 0 || got.stdout != "hello\n" || got.stderr != "" {
			t.Fatalf("got %+v, want hello on stdout only", got)
		}
	})
	t.Run("warning once on stderr", func(t *testing.T) {
		useConfigDir(t)
		got := runCLI(t, "-r -cm test -t no_such_tool q hello", cliOpts{})
		if got.status != 0 || got.stdout != "hello\n" || strings.Count(got.stderr, unknownToolWarning) != 1 {
			t.Fatalf("got %+v, want the reply on stdout and the warning once on stderr", got)
		}
	})
	t.Run("setup error once", func(t *testing.T) {
		useConfigDir(t)
		got := runCLI(t, "-r -cm test -p no_such_profile q hi", cliOpts{})
		if got.status != 1 || got.stdout != "" || strings.Count(got.stderr, "no_such_profile") == 0 || strings.Count(got.stderr, "\nerror") > 0 {
			t.Fatalf("got %+v, want exit 1 and one error naming the profile", got)
		}
		if strings.Count(got.stderr, "error") != strings.Count(firstLine(got.stderr), "error") {
			t.Fatalf("the error is reported more than once: %q", got.stderr)
		}
	})
	// R2-01: a failure of the built-in completion command is its own, so it
	// prints the error once and no usage, as HEAD did.
	for _, args := range []string{"completion fish", "completion"} {
		t.Run("built-in "+args+" error without usage", func(t *testing.T) {
			useConfigDir(t)
			got := runCLI(t, args, cliOpts{})
			if got.status != 1 || got.stdout != "" || strings.Count(got.stderr, "completion <bash|zsh>") != 1 {
				t.Fatalf("got %+v, want exit 1, empty stdout and the error once on stderr", got)
			}
		})
	}
	t.Run("run error once with its cause", func(t *testing.T) {
		useConfigDir(t)
		stubProvider(t, true)
		got := runCLI(t, "-r -cm gpt-stub q hi", cliOpts{})
		if got.status != 1 || got.stdout != "" || strings.Count(got.stderr, "authentication failed") != 1 || !strings.Contains(got.stderr, "401") {
			t.Fatalf("got %+v, want exit 1 and the 401 once on stderr", got)
		}
	})
	for _, mode := range []struct{ name, args, stdout string }{
		{"raw", "-r -cm test -t no_such_tool q hello", "hello\n"},
		{"reply-only", "-cm test -t no_such_tool -reply-only q hello", "hello\n"},
		{"json", "-json -cm test -t no_such_tool q hello", `{"type":"reply_delta","text":"hello"}` + "\n" + `{"type":"reply","text":"hello"}` + "\n"},
	} {
		t.Run("mode "+mode.name, func(t *testing.T) {
			useConfigDir(t)
			got := runCLI(t, mode.args, cliOpts{})
			if got.status != 0 || got.stdout != mode.stdout || !strings.Contains(got.stderr, "no_such_tool") {
				t.Fatalf("got %+v, want stdout %q and the warning on stderr", got, mode.stdout)
			}
			assertNoDiagnosticsIn(t, got.stdout)
		})
	}
	for _, levels := range []struct {
		logLevel string
		shown    bool
	}{
		{"error,text_querier=warn", true},
		{"error,text_querier=warning", true},
		{"warn,text_querier=error", false},
		{"debug,vendor=error,text_querier=info", true},
	} {
		t.Run("nested LOG_LEVEL="+levels.logLevel, func(t *testing.T) {
			useConfigDir(t)
			t.Setenv("LOG_LEVEL", levels.logLevel)
			got := runCLI(t, "-r -cm test -t no_such_tool q hello", cliOpts{})
			if got.status != 0 || strings.Contains(got.stderr, unknownToolWarning) != levels.shown {
				t.Fatalf("got %+v, want the warning shown=%v", got, levels.shown)
			}
		})
	}
	t.Run("invalid LOG_LEVEL", func(t *testing.T) {
		useConfigDir(t)
		t.Setenv("LOG_LEVEL", "loud")
		got := runCLI(t, "-r -cm test q hello", cliOpts{})
		if got.status != 1 || got.stdout != "" || !strings.Contains(got.stderr, "loud") {
			t.Fatalf("got %+v, want exit 1 naming the entry", got)
		}
	})
	t.Run("invalid legacy control", func(t *testing.T) {
		useConfigDir(t)
		t.Setenv("LOG_LEVEL_SKILLS", "loud")
		got := runCLI(t, "-r -cm test q hello", cliOpts{})
		if got.status != 1 || !strings.Contains(got.stderr, "loud") {
			t.Fatalf("got %+v, want exit 1 naming the entry", got)
		}
	})
	t.Run("legacy controls", testLegacyControls)
	t.Run("colors", func(t *testing.T) {
		for _, noColor := range []string{"", "1"} {
			useConfigDir(t)
			t.Setenv("NO_COLOR", noColor)
			got := runCLI(t, "-r -cm test -t no_such_tool q hello", cliOpts{stderrTerminal: true})
			if colored := strings.Contains(got.stderr, "\x1b["); colored != (noColor == "") {
				t.Fatalf("NO_COLOR=%q: stderr %q colored=%v", noColor, got.stderr, colored)
			}
			piped := runCLI(t, "-r -cm test -t no_such_tool q hello", cliOpts{})
			if strings.Contains(piped.stderr, "\x1b") {
				t.Fatalf("a piped stderr is colored: %q", piped.stderr)
			}
		}
	})
	t.Run("spill files", func(t *testing.T) {
		useConfigDir(t)
		t.Setenv("DEBUG_CALL", "1")
		pad := strings.Repeat("SPILLED-PAD", 50_000)
		stubProvider(t, false, pad, pad, pad)
		got := runCLI(t, "-r -cm gpt-stub -t pwd q hi", cliOpts{})
		if got.status != 0 || !strings.Contains(got.stdout, "THE-REPLY") {
			t.Fatalf("got status %d stdout %q", got.status, got.stdout)
		}
		assertNoDiagnosticsIn(t, got.stdout)
		dir := filepath.Join(os.Getenv("CLAI_CACHE_DIR"), spillDirName)
		entries, err := os.ReadDir(dir)
		if err != nil || len(entries) == 0 {
			t.Fatalf("no spill files in %s: %v", dir, err)
		}
		for _, e := range entries {
			path := filepath.Join(dir, e.Name())
			if !strings.Contains(got.stderr, path) {
				t.Fatalf("stderr does not list spill file %s: %.400q", path, got.stderr)
			}
			body, err := os.ReadFile(path)
			if err != nil || !strings.Contains(string(body), "SPILLED-PAD") {
				t.Fatalf("spill file %s lacks the buffered events: %v", path, err)
			}
		}
		if strings.Count(got.stderr, "SPILLED-PAD") >= 3*50_000 {
			t.Fatalf("stderr prints the spill contents")
		}
		if !strings.Contains(got.stderr, "debug [call]") {
			t.Fatalf("stderr does not show the remaining buffer: %.400q", got.stderr)
		}
	})
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	return line
}

// testLegacyControls walks every row of the README Legacy controls table
// through the composition's level resolution.
func testLegacyControls(t *testing.T) {
	type probe struct {
		subsystem string
		level     diagnostic.Level
		enabled   bool
	}
	rows := []struct {
		name   string
		env    map[string]string
		probes []probe
	}{
		{"none", nil, []probe{{"chat", diagnostic.LevelInfo, true}, {"chat", diagnostic.LevelDebug, false}}},
		{"NO_WARNINGS", map[string]string{"NO_WARNINGS": "1"}, []probe{{"chat", diagnostic.LevelWarn, false}, {"chat", diagnostic.LevelError, true}}},
		{"DEBUG", map[string]string{"DEBUG": "true"}, []probe{{"chat", diagnostic.LevelDebug, true}, {"vendor.openai", diagnostic.LevelDebug, true}}},
		{"LOG_LEVEL replaces DEBUG", map[string]string{"DEBUG": "1", "LOG_LEVEL": "warn"}, []probe{{"chat", diagnostic.LevelDebug, false}, {"chat", diagnostic.LevelInfo, false}}},
		{"LOG_LEVEL_SKILLS", map[string]string{"LOG_LEVEL_SKILLS": "trace"}, []probe{{"skills", diagnostic.LevelTrace, true}, {"chat", diagnostic.LevelDebug, false}}},
		{"DEBUG_CPU", map[string]string{"DEBUG_CPU": "1"}, []probe{{"chat", diagnostic.LevelDebug, false}}},
		{"DEBUG_OUTPUT_FILE", map[string]string{"DEBUG_OUTPUT_FILE": "/tmp/x"}, []probe{{"text_querier", diagnostic.LevelDebug, false}}},
		{"NO_COLOR", map[string]string{"NO_COLOR": "1"}, []probe{{"chat", diagnostic.LevelInfo, true}, {"chat", diagnostic.LevelDebug, false}}},
		{"SILENT", map[string]string{"SILENT": "true"}, []probe{{"chat", diagnostic.LevelInfo, true}}},
		{"ANCLI_NEWLINE", map[string]string{"ANCLI_NEWLINE": "true"}, []probe{{"chat", diagnostic.LevelInfo, true}}},
	}
	for _, sub := range []string{"CALL", "CHAT", "CLAUDIFIED_MSGS", "COST_MANAGER", "DIRSCOPE", "LOOKBACK", "OPENROUTER_MODEL_CATALOG", "PROFILES", "REPLY_MODE", "SKILLS", "STOPLOSS", "SUMMARY", "TEXT_QUERIER", "TOOLS_REGISTRY_SET", "VERBOSE"} {
		name := strings.ToLower(sub)
		rows = append(rows, struct {
			name   string
			env    map[string]string
			probes []probe
		}{"DEBUG_" + sub, map[string]string{"DEBUG_" + sub: "1"}, []probe{{name, diagnostic.LevelDebug, true}, {"clai", diagnostic.LevelDebug, false}}})
	}
	for env, sub := range map[string]string{"DEBUG_OPENAI": "vendor.openai", "DEBUG_OPENROUTER": "vendor.openrouter", "DEBUG_MISTRAL": "vendor.mistral", "DEBUG_HUGGINGFACE": "vendor.huggingface", "ANTHROPIC_DEBUG": "vendor.anthropic"} {
		rows = append(rows, struct {
			name   string
			env    map[string]string
			probes []probe
		}{env, map[string]string{env: "1"}, []probe{{sub, diagnostic.LevelDebug, true}, {"vendor.xai", diagnostic.LevelDebug, false}}})
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			p := osProcess()
			p.stdin, p.stdout, p.stderr = strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{}
			p.getenv = func(key string) string { return row.env[key] }
			c, err := compose(p, 0)
			if err != nil {
				t.Fatalf("compose: %v", err)
			}
			for _, pr := range row.probes {
				if got := c.env.Enabled(pr.subsystem, pr.level); got != pr.enabled {
					t.Errorf("%s at %v enabled = %v, want %v", pr.subsystem, pr.level, got, pr.enabled)
				}
			}
		})
	}
}

type failingWriter struct{ writes atomic.Int32 }

var errStderrClosed = errors.New("stderr is closed")

func (w *failingWriter) Write([]byte) (int, error) {
	w.writes.Add(1)
	return 0, errStderrClosed
}

// reportSpill reports buffered events past the Shared buffer size while an
// independent operation runs, and returns the operation's result.
func reportSpill(ctx context.Context, c composition) (string, []error) {
	result := make(chan string, 1)
	go func() { result <- "operation finished" }()
	var errs []error
	chunk := strings.Repeat("x", int(sharedBufferSize)/3)
	for i := range 4 {
		if err := c.reporter.Report(ctx, diagnostic.Event{Type: runenv.TypeSchemaCacheFailed, Message: fmt.Sprintf("event %d %s", i, chunk)}); err != nil {
			errs = append(errs, err)
		}
	}
	return <-result, errs
}

// TestDiagnosticOutputFailure proves criteria 4 and 11: a failed spill
// delivers the buffer on stderr and leaves the operation running; when
// stderr fails too the run aborts with a panic naming both failures; an
// operation error survives a failing output.
func TestDiagnosticOutputFailure(t *testing.T) {
	notADir := filepath.Join(t.TempDir(), "cache-file")
	if err := os.WriteFile(notADir, nil, 0o644); err != nil {
		t.Fatalf("write cache file: %v", err)
	}
	t.Setenv("CLAI_CACHE_DIR", notADir)

	t.Run("spill fails alone", func(t *testing.T) {
		var stderr bytes.Buffer
		p := osProcess()
		p.stdin, p.stdout, p.stderr = strings.NewReader(""), &bytes.Buffer{}, &stderr
		c, err := compose(p, 0)
		if err != nil {
			t.Fatalf("compose: %v", err)
		}
		result, errs := reportSpill(context.Background(), c)
		if result != "operation finished" || len(errs) != 0 {
			t.Fatalf("result %q errors %v, want the operation to finish and no Report error", result, errs)
		}
		c.finish(context.Background(), func() {})
		for i := range 4 {
			if !strings.Contains(stderr.String(), fmt.Sprintf("event %d ", i)) {
				t.Fatalf("stderr lacks buffered event %d", i)
			}
		}
	})
	t.Run("spill and stderr fail", func(t *testing.T) {
		stderr := &failingWriter{}
		p := osProcess()
		p.stdin, p.stdout, p.stderr = strings.NewReader(""), &bytes.Buffer{}, stderr
		c, err := compose(p, 0)
		if err != nil {
			t.Fatalf("compose: %v", err)
		}
		var recovered any
		func() {
			defer func() { recovered = recover() }()
			_, _ = reportSpill(context.Background(), c)
		}()
		msg := fmt.Sprint(recovered)
		if recovered == nil || !strings.Contains(msg, "spill") || !strings.Contains(msg, errStderrClosed.Error()) {
			t.Fatalf("recovered %q, want a panic naming the spill and the stderr failure", msg)
		}
		if n := stderr.writes.Load(); n > 8 {
			t.Fatalf("stderr was tried %d times for 4 events, want a bounded number", n)
		}
	})
	t.Run("operation and output fail", func(t *testing.T) {
		useConfigDir(t)
		got := runCLI(t, "-r -cm test -p no_such_profile q hi", cliOpts{stderr: &failingWriter{}})
		if got.status != 1 {
			t.Fatalf("status %d, want the operation's failure status 1", got.status)
		}
	})
}
