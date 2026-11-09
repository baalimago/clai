package tooling

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/baalimago/clai/e2e/e2etest"
	"github.com/baalimago/clai/internal/cli"
	"github.com/baalimago/clai/internal/text"
	"github.com/baalimago/clai/internal/tools"
	"github.com/baalimago/clai/internal/utils"
)

// f14Fields is README F14: every record type and the fields it may carry.
var f14Fields = map[string][]string{
	"reply_delta":     {"text"},
	"reasoning_delta": {"text"},
	"reply":           {"text"},
	"tool_call":       {"name", "inputs"},
	"tool_result":     {"name", "ok", "output", "error"},
	"progress":        {"title", "row", "state", "detail"},
	"prompt":          {"kind", "text", "choices"},
	"diagnostic":      {"name", "subsystem", "level", "message", "facts"},
	"error":           {"message", "causes"},
	"child_output":    {"source", "stream", "text"},
	"output":          {"stream", "text"},
}

type jsonRecord map[string]any

func (r jsonRecord) typ() string { s, _ := r["type"].(string); return s }

func (r jsonRecord) str(key string) string { s, _ := r[key].(string); return s }

// streamRun is one in-process run over explicit streams.
type streamRun struct {
	status         int
	stdout, stderr string
}

type streamOpts struct {
	stdin          string
	stdoutTerminal bool
	stderrTerminal bool
}

func runOnStreams(args []string, o streamOpts) streamRun {
	var stdout, stderr bytes.Buffer
	status := cli.RunWithStreams(args, harness.Deps, cli.Streams{
		Stdin:          strings.NewReader(o.stdin),
		Stdout:         &stdout,
		Stderr:         &stderr,
		StdoutTerminal: o.stdoutTerminal,
		StderrTerminal: o.stderrTerminal,
	})
	return streamRun{status: status, stdout: stdout.String(), stderr: stderr.String()}
}

// parseRecords checks F12 for one destination: every line is a JSON object
// ending in "\n" with a string "type" first, of an F14 type with only F14
// fields, and no raw ESC or "\r" anywhere.
func parseRecords(t *testing.T, stream, out string) []jsonRecord {
	t.Helper()
	if strings.ContainsAny(out, "\x1b\r") {
		t.Fatalf("%s holds a raw ESC or \\r: %q", stream, out)
	}
	if out != "" && !strings.HasSuffix(out, "\n") {
		t.Fatalf("%s does not end in a newline: %q", stream, out)
	}
	var records []jsonRecord
	for line := range strings.SplitSeq(strings.TrimSuffix(out, "\n"), "\n") {
		if out == "" {
			break
		}
		if !strings.HasPrefix(line, `{"type":"`) {
			t.Fatalf("%s line does not start with a string type: %q", stream, line)
		}
		var rec jsonRecord
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("%s line is not one JSON object: %q: %v", stream, line, err)
		}
		fields, ok := f14Fields[rec.typ()]
		if !ok {
			t.Fatalf("%s record type %q is not in F14: %q", stream, rec.typ(), line)
		}
		for key := range rec {
			if key != "type" && !slices.Contains(fields, key) {
				t.Fatalf("%s record %q carries field %q outside F14: %q", stream, rec.typ(), key, line)
			}
		}
		if text := rec.str("text"); strings.Contains(text, "▸") {
			t.Fatalf("%s record text carries a glyph decoration: %q", stream, line)
		}
		if s := rec.str("stream"); (rec.typ() == "output" || rec.typ() == "child_output") && s != stream {
			t.Fatalf("%s carries a record for stream %q: %q", stream, s, line)
		}
		records = append(records, rec)
	}
	return records
}

func ofType(records []jsonRecord, typ string) []jsonRecord {
	var out []jsonRecord
	for _, r := range records {
		if r.typ() == typ {
			out = append(out, r)
		}
	}
	return out
}

// stubOpenAI is an OpenAI-shaped provider for model gpt-stub. Its first
// answer to a chat without a tool result streams reasoning and calls tool
// with inputs; an answer to a chat with one streams THE-REPLY. With unauthorized it refuses every request with a 401.
func stubOpenAI(t *testing.T, confDir, tool, inputs string, unauthorized bool) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		answered := bytes.Contains(body, []byte(`"role":"tool"`))
		if unauthorized {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":{"message":"Incorrect API key provided","type":"invalid_request_error","code":"invalid_api_key"}}`))
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		if !answered && tool != "" {
			args, _ := json.Marshal(inputs)
			fmt.Fprint(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"reasoning_content\":\"pondering\"}}]}\n\n")
			fmt.Fprintf(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_1\",\"type\":\"function\",\"function\":{\"name\":%q,\"arguments\":%s}}]}}]}\n\n", tool, args)
			fmt.Fprint(w, "data: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"tool_calls\"}]}\n\ndata: [DONE]\n\n")
			return
		}
		fmt.Fprint(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"THE-\"}}]}\n\n")
		fmt.Fprint(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"REPLY\"}}]}\n\ndata: [DONE]\n\n")
	}))
	t.Cleanup(server.Close)
	writeStubModel(t, confDir, server.URL)
}

// writeStubModel points model gpt-stub at the OpenAI-shaped server at url.
func writeStubModel(t *testing.T, confDir, url string) {
	t.Helper()
	t.Setenv("OPENAI_API_KEY", "stub-key")
	conf, err := json.Marshal(map[string]any{"model": "gpt-stub", "url": url + "/v1/chat/completions"})
	if err != nil {
		t.Fatalf("marshal model config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(confDir, "openai_gpt_gpt-stub.json"), conf, 0o644); err != nil {
		t.Fatalf("write model config: %v", err)
	}
}

// withFakeFFmpeg puts script on PATH as ffmpeg and registers the audio tools
// against it for fn.
func withFakeFFmpeg(t *testing.T, script []byte, fn func()) {
	t.Helper()
	binDir := t.TempDir()
	if script == nil {
		e2etest.InstallFakeFFmpeg(t, binDir)
	} else {
		e2etest.WriteExecutable(t, binDir, "ffmpeg", script)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	tools.WithTestRegistry(t, func() {
		tools.Init(context.Background())
		fn()
	})
}

type jsonScenario struct {
	name string
	args string
	// jsonArgs, when set, replaces args for the -json run.
	jsonArgs string
	opts     streamOpts
	setup    func(t *testing.T, confDir string)
	// wrap runs both runs of the scenario, for fixtures scoped to a callback.
	wrap  func(t *testing.T, run func())
	check func(t *testing.T, json streamRun, stdout, stderr []jsonRecord)
}

func jsonScenarios() []jsonScenario {
	return []jsonScenario{
		{
			name:  "streamed query with reasoning and a tool call",
			args:  "-cm gpt-stub -t pwd q hi",
			setup: func(t *testing.T, confDir string) { stubOpenAI(t, confDir, "pwd", "{}", false) },
			check: func(t *testing.T, _ streamRun, stdout, _ []jsonRecord) {
				var types []string
				for _, r := range stdout {
					if r.typ() != "output" {
						types = append(types, r.typ())
					}
				}
				want := []string{"reasoning_delta", "tool_call", "tool_result", "reply_delta", "reply_delta", "reply"}
				if !slices.Equal(types, want) {
					t.Fatalf("stdout record types = %v, want %v", types, want)
				}
				call := ofType(stdout, "tool_call")[0]
				if call.str("name") != "pwd" {
					t.Fatalf("tool_call = %v", call)
				}
				if inputs, ok := call["inputs"].(map[string]any); !ok || len(inputs) != 0 {
					t.Fatalf("tool_call inputs = %v, want {}", call["inputs"])
				}
				result := ofType(stdout, "tool_result")[0]
				if result["ok"] != true || result.str("output") == "" {
					t.Fatalf("tool_result = %v", result)
				}
				if reply := ofType(stdout, "reply")[0]; reply.str("text") != "THE-REPLY" {
					t.Fatalf("reply = %v", reply)
				}
			},
		},
		{
			name: "failing tool",
			args: "-cm gpt-stub -t audio_sources q hi",
			setup: func(t *testing.T, confDir string) {
				stubOpenAI(t, confDir, "audio_sources", "{}", false)
			},
			wrap: func(t *testing.T, run func()) {
				withFakeFFmpeg(t, []byte("#!/bin/sh\nexit 127\n"), run)
			},
			check: func(t *testing.T, _ streamRun, stdout, _ []jsonRecord) {
				results := ofType(stdout, "tool_result")
				if len(results) != 1 {
					t.Fatalf("tool_result records = %v, want one", results)
				}
				r := results[0]
				if r["ok"] != false || !strings.HasPrefix(r.str("output"), "ERROR: ") || r.str("error") == "" {
					t.Fatalf("failed tool_result = %v, want ok false, ERROR: output and an error", r)
				}
				if first, _, _ := strings.Cut(strings.TrimPrefix(r.str("output"), "ERROR: "), "\n"); first != r.str("error") {
					t.Fatalf("error %q is not the rest of the first output line %q", r.str("error"), r.str("output"))
				}
			},
		},
		{
			// R3-01: text mode prints the failed tool at debug level, so
			// -json writes its tool_call and tool_result records too.
			name: "failing tool at debug level",
			args: "-cm test -t cmd q tool_cmd please",
			setup: func(t *testing.T, _ string) {
				t.Setenv("LOG_LEVEL", "debug")
				t.Setenv("CLAI_MOCK_CMD_COMMAND", "exit 127")
			},
			check: func(t *testing.T, _ streamRun, stdout, _ []jsonRecord) {
				calls, results := ofType(stdout, "tool_call"), ofType(stdout, "tool_result")
				if len(calls) != 1 || calls[0].str("name") != "cmd" {
					t.Fatalf("tool_call records = %v, want one for cmd", calls)
				}
				if len(results) != 1 || results[0]["ok"] != false || !strings.Contains(results[0].str("error"), "127") {
					t.Fatalf("tool_result records = %v, want one failed result naming exit code 127", results)
				}
			},
		},
		{
			// R3-02: summaries are diagnostic records with facts, not text.
			name:  "query summary",
			args:  "-qs -cm test q hi",
			check: runSummary("query done in ", "outcome", "done"),
		},
		{
			name:  "loop summary",
			args:  "-r -cm test -l q run tool_complete_task",
			check: runSummary("loop achieved after 1/", "end", "achieved"),
		},
		{
			name: "step retry",
			args: "-cm gpt-stub q hi",
			setup: func(t *testing.T, confDir string) {
				var failed atomic.Bool
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					if failed.CompareAndSwap(false, true) {
						w.WriteHeader(http.StatusServiceUnavailable)
						return
					}
					w.Header().Set("Content-Type", "text/event-stream")
					fmt.Fprint(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"THE-REPLY\"}}]}\n\ndata: [DONE]\n\n")
					failed.Store(false)
				}))
				t.Cleanup(server.Close)
				writeStubModel(t, confDir, server.URL)
			},
			check: func(t *testing.T, _ streamRun, stdout, stderr []jsonRecord) {
				progress := ofType(stderr, "progress")
				if len(progress) != 1 || progress[0].str("title") != "retry" || progress[0].str("row") != "gpt-stub" ||
					progress[0].str("state") != "update" || !strings.Contains(progress[0].str("detail"), "1/4") ||
					!strings.Contains(progress[0].str("detail"), "provider unavailable (503)") {
					t.Fatalf("stderr = %v, want one retry progress record for gpt-stub", stderr)
				}
				if len(ofType(stdout, "reply")) != 1 {
					t.Fatalf("stdout = %v, want the reply after the retry", stdout)
				}
			},
		},
		{
			name: "model fallback",
			args: "q hello alias",
			setup: func(t *testing.T, confDir string) {
				conf := text.Default
				conf.Model = "strong"
				conf.ModelAliases = map[string][]string{"strong": {"no-such-vendor-model", "test"}}
				if err := utils.CreateFile(filepath.Join(confDir, "textConfig.json"), &conf); err != nil {
					t.Fatalf("write text config: %v", err)
				}
			},
			check: func(t *testing.T, _ streamRun, _, stderr []jsonRecord) {
				progress := ofType(stderr, "progress")
				if len(progress) != 1 || progress[0].str("title") != "model fallback" || progress[0].str("row") != "test" ||
					progress[0].str("state") != "start" || progress[0].str("detail") != "no-such-vendor-model unavailable: model not found" {
					t.Fatalf("stderr = %v, want one model fallback progress record", stderr)
				}
			},
		},
		{
			name:  "provider 401",
			args:  "-cm gpt-stub q hi",
			setup: func(t *testing.T, confDir string) { stubOpenAI(t, confDir, "", "", true) },
			check: func(t *testing.T, run streamRun, _, stderr []jsonRecord) {
				errs := ofType(stderr, "error")
				if run.status != 1 || len(errs) != 1 {
					t.Fatalf("status %d, error records %v; want 1 and one", run.status, errs)
				}
				causes, _ := errs[0]["causes"].([]any)
				var status float64
				var providerCode string
				for _, c := range causes {
					cause := c.(map[string]any)
					if s, ok := cause["status"].(float64); ok {
						status = s
						providerCode, _ = cause["provider_code"].(string)
					}
				}
				if status != 401 || providerCode != "invalid_api_key" {
					t.Fatalf("error causes %v carry no status 401 with provider code invalid_api_key", causes)
				}
			},
		},
		{
			name:  "warning",
			args:  "-cm test -t no_such_tool q hello",
			check: diagnosticNamed("warn", "no_such_tool"),
		},
		{
			name: "buffered diagnostic",
			args: "-cm gpt-stub -t pwd q hi",
			setup: func(t *testing.T, confDir string) {
				t.Setenv("DEBUG_CALL", "1")
				stubOpenAI(t, confDir, "pwd", "{}", false)
			},
			check: func(t *testing.T, _ streamRun, _, stderr []jsonRecord) {
				for _, r := range ofType(stderr, "diagnostic") {
					if r.str("subsystem") == "call" && r.str("level") == "debug" {
						return
					}
				}
				t.Fatalf("no buffered call debug diagnostic on stderr: %v", stderr)
			},
		},
		{
			name:  "reply-only",
			args:  "-cm gpt-stub -t pwd -reply-only q hi",
			setup: func(t *testing.T, confDir string) { stubOpenAI(t, confDir, "pwd", "{}", false) },
			check: func(t *testing.T, _ streamRun, stdout, stderr []jsonRecord) {
				if len(stdout) != 1 || stdout[0].typ() != "reply" || stdout[0].str("text") != "THE-REPLY" {
					t.Fatalf("reply-only stdout = %v, want one reply record", stdout)
				}
				if len(ofType(stderr, "tool_call")) != 1 || len(ofType(stderr, "tool_result")) != 1 {
					t.Fatalf("reply-only stderr = %v, want the tool call and result", stderr)
				}
			},
		},
		{
			name:  "raw",
			args:  "-r -cm gpt-stub q hi",
			setup: func(t *testing.T, confDir string) { stubOpenAI(t, confDir, "", "", false) },
			check: func(t *testing.T, _ streamRun, stdout, _ []jsonRecord) {
				if reply := ofType(stdout, "reply"); len(reply) != 1 || reply[0].str("text") != "THE-REPLY" {
					t.Fatalf("raw stdout = %v, want the reply record", stdout)
				}
			},
		},
		{
			name: "MCP server writing stderr",
			// One MCP server per run: a server's stderr is relayed by the
			// process that started it.
			args:     "-r -cm test -p noisytext q tool_mcp_noisytext_echo",
			jsonArgs: "-r -cm test -p noisyjson q tool_mcp_noisyjson_echo",
			setup: func(t *testing.T, confDir string) {
				for _, name := range []string{"noisytext", "noisyjson"} {
					profile := fmt.Sprintf(`{"model":"test","use_tools":true,"mcp_servers":{%q:{"command":%q,"env":{"TEST_SERVER_STDERR":"1"}}}}`, name, testServerBinary(t))
					if err := os.WriteFile(filepath.Join(confDir, "profiles", name+".json"), []byte(profile), 0o644); err != nil {
						t.Fatalf("write profile: %v", err)
					}
				}
			},
			check: func(t *testing.T, _ streamRun, _, stderr []jsonRecord) {
				for _, r := range ofType(stderr, "child_output") {
					if r.str("source") == "mcp_noisyjson" && r.str("stream") == "stderr" && strings.Contains(r.str("text"), "an error occurred") {
						return
					}
				}
				t.Fatalf("no child_output record of mcp_noisy on stderr: %v", stderr)
			},
		},
		{
			name: "audio_record status on a terminal stderr",
			args: "-cm mock_test -t audio_record q tool_audio_record",
			opts: streamOpts{stderrTerminal: true},
			setup: func(t *testing.T, confDir string) {
				e2etest.WriteJSONFileAny(t, filepath.Join(confDir, "audioConfig.json"), map[string]any{
					"capture": map[string]any{"dir": t.TempDir(), "max-seconds": 30, "silence-timeout": 1},
				})
			},
			wrap: func(t *testing.T, run func()) { withFakeFFmpeg(t, nil, run) },
			check: func(t *testing.T, _ streamRun, _, stderr []jsonRecord) {
				progress := ofType(stderr, "progress")
				if len(progress) == 0 {
					t.Fatalf("no progress records on stderr: %v", stderr)
				}
				for i := 1; i < len(progress); i++ {
					if fmt.Sprint(progress[i]) == fmt.Sprint(progress[i-1]) {
						t.Fatalf("progress record repeated without a state change: %v", progress[i])
					}
				}
				if last := progress[len(progress)-1].str("state"); last != "done" && last != "failed" {
					t.Fatalf("last progress state = %q, want done or failed", last)
				}
			},
		},
		{
			name: "profiles list",
			args: "profiles list",
			setup: func(t *testing.T, confDir string) {
				if err := os.WriteFile(filepath.Join(confDir, "profiles", "writer.json"), []byte(`{"model":"test","prompt":"You write."}`), 0o644); err != nil {
					t.Fatalf("write profile: %v", err)
				}
			},
			check: onlyOutput,
		},
		{name: "help", args: "help", check: onlyOutput},
		// R1-05: completion output is a result, so under -json it is output
		// records on stdout.
		{name: "completion script", args: "completion bash", check: onlyOutput},
		{
			name: "completion candidates",
			args: "__complete clai q",
			check: func(t *testing.T, run streamRun, stdout, stderr []jsonRecord) {
				onlyOutput(t, run, stdout, stderr)
				for _, r := range stdout {
					if strings.HasPrefix(r.str("text"), "query\t") {
						return
					}
				}
				t.Fatalf("no candidate line for query: %v", stdout)
			},
		},
		{
			name: "unknown argument",
			args: "no-such-command",
			check: func(t *testing.T, run streamRun, _, stderr []jsonRecord) {
				if run.status == 0 || len(ofType(stderr, "error")) != 1 {
					t.Fatalf("status %d stderr %v; want a failure with one error record", run.status, stderr)
				}
			},
		},
		{
			name:  "built-in completion with an unsupported shell",
			args:  "completion fish",
			check: onlyOneError,
		},
		{
			name:  "built-in completion without a shell",
			args:  "completion",
			check: onlyOneError,
		},
		{
			name: "glow on PATH",
			args: "-cm test q hello",
			opts: streamOpts{stdoutTerminal: true, stderrTerminal: true},
			setup: func(t *testing.T, _ string) {
				binDir := t.TempDir()
				marker := filepath.Join(t.TempDir(), "glow-started")
				e2etest.WriteExecutable(t, binDir, "glow", []byte("#!/bin/sh\ntouch "+marker+"\ncat\n"))
				t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
				t.Cleanup(func() {
					if _, err := os.Stat(marker); err == nil {
						t.Errorf("glow was started under -json")
					}
				})
			},
			check: func(t *testing.T, _ streamRun, stdout, _ []jsonRecord) {
				if len(ofType(stdout, "reply")) != 1 {
					t.Fatalf("stdout = %v, want one reply record", stdout)
				}
			},
		},
	}
}

// runSummary checks the run's summary diagnostic: its message, its end
// fact, and the tally facts every summary carries.
func runSummary(message, endFact, end string) func(*testing.T, streamRun, []jsonRecord, []jsonRecord) {
	return func(t *testing.T, _ streamRun, _, stderr []jsonRecord) {
		t.Helper()
		var summaries []jsonRecord
		for _, r := range ofType(stderr, "diagnostic") {
			if r.str("name") == "clai.run_summary" {
				summaries = append(summaries, r)
			}
		}
		if len(summaries) != 1 {
			t.Fatalf("stderr = %v, want one clai.run_summary diagnostic", stderr)
		}
		s := summaries[0]
		if !strings.HasPrefix(s.str("message"), message) || s.str("level") != "info" {
			t.Fatalf("summary = %v, want an info message starting %q", s, message)
		}
		facts, _ := s["facts"].(map[string]any)
		if facts[endFact] != end {
			t.Fatalf("summary facts = %v, want %s %q", facts, endFact, end)
		}
		for _, key := range []string{"duration_ms", "tokens_in", "tokens_out", "tool_calls", "tools"} {
			if _, ok := facts[key]; !ok {
				t.Fatalf("summary facts = %v, want %s", facts, key)
			}
		}
	}
}

func diagnosticNamed(level, text string) func(*testing.T, streamRun, []jsonRecord, []jsonRecord) {
	return func(t *testing.T, _ streamRun, stdout, stderr []jsonRecord) {
		for _, r := range ofType(stderr, "diagnostic") {
			if r.str("level") == level && strings.Contains(r.str("message"), text) {
				if len(ofType(stdout, "diagnostic")) != 0 {
					t.Fatalf("diagnostic on stdout: %v", stdout)
				}
				return
			}
		}
		t.Fatalf("no %s diagnostic naming %q on stderr: %v", level, text, stderr)
	}
}

// onlyOneError is R2-01: a dispatched command's own failure writes one
// error record and no usage.
func onlyOneError(t *testing.T, run streamRun, stdout, stderr []jsonRecord) {
	t.Helper()
	if run.status != 1 || len(stdout) != 0 || len(stderr) != 1 || len(ofType(stderr, "error")) != 1 {
		t.Fatalf("status %d stdout %v stderr %v; want exit 1, no stdout and one error record", run.status, stdout, stderr)
	}
}

func onlyOutput(t *testing.T, run streamRun, stdout, stderr []jsonRecord) {
	t.Helper()
	if run.status != 0 || len(stdout) == 0 {
		t.Fatalf("status %d stdout %v; want success with output", run.status, stdout)
	}
	for _, r := range append(slices.Clone(stdout), stderr...) {
		if r.typ() != "output" {
			t.Fatalf("record %v is not an output record", r)
		}
	}
}

// TestJSONModeScenarios proves criteria 12 and 13: under -json every line of
// both destinations is an F14 record, the producers write the records F14
// names, and each destination is used exactly when the same run without
// -json uses it, with the same exit status.
func TestJSONModeScenarios(t *testing.T) {
	for _, sc := range jsonScenarios() {
		t.Run(sc.name, func(t *testing.T) {
			confDir := e2etest.SetupMainTestConfigDir(t)
			if sc.setup != nil {
				sc.setup(t, confDir)
			}
			var text, asJSON streamRun
			both := func() {
				text = runOnStreams(strings.Split(sc.args, " "), sc.opts)
				jsonArgs := sc.args
				if sc.jsonArgs != "" {
					jsonArgs = sc.jsonArgs
				}
				asJSON = runOnStreams(append([]string{"-json"}, strings.Split(jsonArgs, " ")...), sc.opts)
			}
			if sc.wrap != nil {
				sc.wrap(t, both)
			} else {
				both()
			}
			stdout := parseRecords(t, "stdout", asJSON.stdout)
			stderr := parseRecords(t, "stderr", asJSON.stderr)
			if text.status != asJSON.status {
				t.Fatalf("status %d under -json, %d without; json stderr %q", asJSON.status, text.status, asJSON.stderr)
			}
			if (text.stdout == "") != (asJSON.stdout == "") || (text.stderr == "") != (asJSON.stderr == "") {
				t.Fatalf("destinations differ: text stdout %q stderr %q; json stdout %q stderr %q", text.stdout, text.stderr, asJSON.stdout, asJSON.stderr)
			}
			sc.check(t, asJSON, stdout, stderr)
		})
	}
	// A failed record write is handled as the failed write it replaces.
	t.Run("record write fails", func(t *testing.T) {
		e2etest.SetupMainTestConfigDir(t)
		run := func(args []string) int {
			return cli.RunWithStreams(args, harness.Deps, cli.Streams{Stdin: strings.NewReader(""), Stdout: failingWriter{}, Stderr: failingWriter{}})
		}
		args := strings.Fields("-cm test -t no_such_tool q hello")
		text, asJSON := run(args), run(append([]string{"-json"}, args...))
		if text != asJSON {
			t.Fatalf("status %d under -json, %d without, when every write fails", asJSON, text)
		}
	})
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("closed destination") }
