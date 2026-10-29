package e2e

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/baalimago/clai/e2e/e2etest"
	"github.com/baalimago/clai/internal/text"
	"github.com/baalimago/clai/internal/utils"
)

// modelAliasFixture seeds a config dir whose textConfig.json names an alias
// ladder and nothing else, and returns that dir plus the working directory.
func modelAliasFixture(t *testing.T, model string, aliases map[string][]string) string {
	t.Helper()
	confDir := e2etest.SetupMainTestConfigDir(t)
	writeTextConfig(t, confDir, model, aliases)
	return confDir
}

func writeTextConfig(t *testing.T, confDir, model string, aliases map[string][]string) {
	t.Helper()
	conf := text.Default
	conf.Model = model
	conf.ModelAliases = aliases
	if err := utils.CreateFile(filepath.Join(confDir, "textConfig.json"), &conf); err != nil {
		t.Fatalf("CreateFile(textConfig.json): %v", err)
	}
}

// fallbackNotice returns the one fallback notice stderr carries, failing the
// test when there is none or when more than one model was reported.
func fallbackNotice(t *testing.T, stderr string) string {
	t.Helper()
	var notices []string
	for line := range strings.SplitSeq(stderr, "\n") {
		if strings.HasPrefix(line, "▸ model ") && strings.Contains(line, " unavailable · ") {
			notices = append(notices, line)
		}
	}
	if len(notices) != 1 {
		t.Fatalf("stderr = %q, want exactly one fallback notice", stderr)
	}
	return notices[0]
}

// TestModelAlias_FallsBackToTheNextModel pins the feature end to end: a model
// this machine cannot route fails, the next model of the alias answers, and
// the fallback is reported on stderr without touching the reply on stdout.
func TestModelAlias_FallsBackToTheNextModel(t *testing.T) {
	modelAliasFixture(t, "strong", map[string][]string{
		"strong": {"no-such-vendor-model", "test"},
	})

	var status int
	stdout, stderr := e2etest.CaptureStdoutStderr(t, func() {
		status = harness.Run([]string{"-r", "query", "hello", "alias"})
	})
	if status != 0 {
		t.Fatalf("status = %d, stderr = %q, want 0", status, stderr)
	}
	if want := "hello alias\n"; stdout != want {
		t.Fatalf("stdout = %q, want %q", stdout, want)
	}
	want := "▸ model no-such-vendor-model unavailable · model not found · trying test"
	if got := fallbackNotice(t, stderr); got != want {
		t.Fatalf("notice = %q, want %q", got, want)
	}
}

// TestModelAlias_FallsBackWhenTheRunFails pins the other half of the ladder: a
// model that answers with an error produced nothing, so the next model runs. A
// build failure already fell back; a run failure must too, or the ladder is dead
// for exactly the failures it exists for.
func TestModelAlias_FallsBackWhenTheRunFails(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"type":"error","error":{"type":"not_found_error","message":"model: no such model"}}`))
	}))
	t.Cleanup(srv.Close)

	confDir := modelAliasFixture(t, "strong", map[string][]string{
		"strong": {"claude-dead", "test"},
	})
	t.Setenv("ANTHROPIC_API_KEY", "test-key")
	modelConf, err := json.Marshal(map[string]any{
		"model":      "claude-dead",
		"url":        srv.URL,
		"max_tokens": 16,
	})
	if err != nil {
		t.Fatalf("Marshal(model config): %v", err)
	}
	if err := os.WriteFile(filepath.Join(confDir, "anthropic_claude_claude-dead.json"), modelConf, 0o644); err != nil {
		t.Fatalf("WriteFile(model config): %v", err)
	}

	var status int
	stdout, stderr := e2etest.CaptureStdoutStderr(t, func() {
		status = harness.Run([]string{"-r", "query", "hello", "alias"})
	})
	if status != 0 {
		t.Fatalf("status = %d, stderr = %q, want 0", status, stderr)
	}
	if want := "hello alias\n"; stdout != want {
		t.Fatalf("stdout = %q, want %q", stdout, want)
	}
	want := "▸ model claude-dead unavailable · model not found · trying test"
	if got := fallbackNotice(t, stderr); got != want {
		t.Fatalf("notice = %q, want %q", got, want)
	}
}

// TestModelAlias_ExhaustedChainNamesEveryModel pins that a chain whose every
// model fails reports each model and its reason, so the user can fix the
// ladder instead of guessing.
func TestModelAlias_ExhaustedChainNamesEveryModel(t *testing.T) {
	modelAliasFixture(t, "strong", map[string][]string{
		"strong": {"no-such-vendor-model", "another-unknown-model"},
	})

	var status int
	_, stderr := e2etest.CaptureStdoutStderr(t, func() {
		status = harness.Run([]string{"-r", "query", "hello"})
	})
	if status == 0 {
		t.Fatalf("status = 0, want a failure with every model exhausted")
	}
	for _, want := range []string{"no-such-vendor-model", "another-unknown-model"} {
		if !strings.Contains(stderr, want) {
			t.Fatalf("stderr = %q, want it to name %q", stderr, want)
		}
	}
}

// TestModelAlias_FlagModelResolvesTheAlias pins that the alias is resolved
// wherever the model is named, so a run that overrides the model with -cm still
// falls back through the configured ladder.
func TestModelAlias_FlagModelResolvesTheAlias(t *testing.T) {
	modelAliasFixture(t, "test", map[string][]string{
		"strong": {"no-such-vendor-model", "test"},
	})

	var status int
	stdout, stderr := e2etest.CaptureStdoutStderr(t, func() {
		status = harness.Run([]string{"-r", "-cm", "strong", "query", "hello"})
	})
	if status != 0 {
		t.Fatalf("status = %d, stderr = %q, want 0", status, stderr)
	}
	if stdout != "hello\n" {
		t.Fatalf("stdout = %q, want %q", stdout, "hello\n")
	}
	want := "▸ model no-such-vendor-model unavailable · model not found · trying test"
	if got := fallbackNotice(t, stderr); got != want {
		t.Fatalf("notice = %q, want %q", got, want)
	}
}

// TestModelAlias_PlainModelPrintsNoNotice pins that a model the table does not
// name runs exactly as it did before fallback existed.
func TestModelAlias_PlainModelPrintsNoNotice(t *testing.T) {
	modelAliasFixture(t, "test", map[string][]string{
		"strong": {"no-such-vendor-model", "test"},
	})

	var status int
	stdout, stderr := e2etest.CaptureStdoutStderr(t, func() {
		status = harness.Run([]string{"-r", "query", "hello"})
	})
	if status != 0 {
		t.Fatalf("status = %d, stderr = %q, want 0", status, stderr)
	}
	if stdout != "hello\n" {
		t.Fatalf("stdout = %q, want %q", stdout, "hello\n")
	}
	if strings.Contains(stderr, "▸ model ") {
		t.Fatalf("stderr = %q, want no fallback notice", stderr)
	}
}

// TestModelAlias_ProfileModelResolvesTheAlias pins the distributed-profile use
// case: a shared profile names a verb, and each machine resolves it against its
// own ladder.
func TestModelAlias_ProfileModelResolvesTheAlias(t *testing.T) {
	confDir := modelAliasFixture(t, "test", map[string][]string{
		"strong": {"no-such-vendor-model", "test"},
	})
	profile := map[string]any{
		"name":   "reviewer",
		"model":  "strong",
		"tools":  []string{},
		"prompt": "You review code.",
	}
	body, err := json.Marshal(profile)
	if err != nil {
		t.Fatalf("Marshal(profile): %v", err)
	}
	profilePath := filepath.Join(confDir, "profiles", "reviewer.json")
	if err := os.WriteFile(profilePath, body, 0o644); err != nil {
		t.Fatalf("WriteFile(%v): %v", profilePath, err)
	}

	var status int
	stdout, stderr := e2etest.CaptureStdoutStderr(t, func() {
		status = harness.Run([]string{"-r", "-p", "reviewer", "query", "hello"})
	})
	if status != 0 {
		t.Fatalf("status = %d, stderr = %q, want 0", status, stderr)
	}
	if stdout != "hello\n" {
		t.Fatalf("stdout = %q, want %q", stdout, "hello\n")
	}
	want := "▸ model no-such-vendor-model unavailable · model not found · trying test"
	if got := fallbackNotice(t, stderr); got != want {
		t.Fatalf("notice = %q, want %q", got, want)
	}
}

// TestModelAlias_QuerySummaryPrintsOnceOnRuntimeFallback pins that a candidate
// which failed with a model-unavailable error before emitting any output does
// not print its own "▸ query stopped" summary before the next model runs.
func TestModelAlias_QuerySummaryPrintsOnceOnRuntimeFallback(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"type":"error","error":{"type":"not_found_error","message":"model: no such model"}}`))
	}))
	t.Cleanup(srv.Close)

	confDir := modelAliasFixture(t, "strong", map[string][]string{
		"strong": {"claude-dead", "test"},
	})
	t.Setenv("ANTHROPIC_API_KEY", "test-key")
	modelConf, err := json.Marshal(map[string]any{
		"model":      "claude-dead",
		"url":        srv.URL,
		"max_tokens": 16,
	})
	if err != nil {
		t.Fatalf("Marshal(model config): %v", err)
	}
	if err := os.WriteFile(filepath.Join(confDir, "anthropic_claude_claude-dead.json"), modelConf, 0o644); err != nil {
		t.Fatalf("WriteFile(model config): %v", err)
	}

	var status int
	stdout, stderr := e2etest.CaptureStdoutStderr(t, func() {
		status = harness.Run([]string{"-r", "-qs", "query", "hello", "alias"})
	})
	if status != 0 {
		t.Fatalf("status = %d, stderr = %q, want 0", status, stderr)
	}
	if want := "hello alias\n"; stdout != want {
		t.Fatalf("stdout = %q, want %q", stdout, want)
	}
	if strings.Contains(stderr, "▸ query stopped") {
		t.Fatalf("stderr = %q, want no '▸ query stopped' summary for the unavailable candidate", stderr)
	}
	if got := strings.Count(stderr, "▸ query "); got != 1 {
		t.Fatalf("stderr = %q, want exactly 1 query summary, got %d", stderr, got)
	}
}

// TestModelAlias_LoopSummaryPrintsOnceOnRuntimeFallback pins that a loop
// candidate failing on iteration 1 with a model-unavailable error before
// producing any output does not print "▸ loop stopped" before falling back.
func TestModelAlias_LoopSummaryPrintsOnceOnRuntimeFallback(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"type":"error","error":{"type":"not_found_error","message":"model: no such model"}}`))
	}))
	t.Cleanup(srv.Close)

	confDir := modelAliasFixture(t, "strong", map[string][]string{
		"strong": {"claude-dead", "test"},
	})
	t.Setenv("ANTHROPIC_API_KEY", "test-key")
	modelConf, err := json.Marshal(map[string]any{
		"model":      "claude-dead",
		"url":        srv.URL,
		"max_tokens": 16,
	})
	if err != nil {
		t.Fatalf("Marshal(model config): %v", err)
	}
	if err := os.WriteFile(filepath.Join(confDir, "anthropic_claude_claude-dead.json"), modelConf, 0o644); err != nil {
		t.Fatalf("WriteFile(model config): %v", err)
	}

	var status int
	_, stderr := e2etest.CaptureStdoutStderr(t, func() {
		status = harness.Run([]string{"-r", "-loop", "-max-iterations", "1", "query", "hello", "alias"})
	})
	if status == 0 {
		t.Fatalf("status = 0, want non-zero for exhausted 1-iteration mock loop")
	}
	if strings.Contains(stderr, "▸ loop stopped") {
		t.Fatalf("stderr = %q, want no '▸ loop stopped' summary from the unavailable first model", stderr)
	}
	if got := strings.Count(stderr, "▸ loop "); got != 1 {
		t.Fatalf("stderr = %q, want exactly 1 loop summary, got %d", stderr, got)
	}
}

// TestModelAlias_ExplicitMcpFailureStopsWithoutFallback pins that an explicit
// MCP startup failure ends the run immediately without retrying the broken MCP
// server on the remaining models of the alias ladder.
func TestModelAlias_ExplicitMcpFailureStopsWithoutFallback(t *testing.T) {
	confDir := modelAliasFixture(t, "test", map[string][]string{
		"strong": {"test", "mock_test"},
	})
	profile := map[string]any{
		"name":      "broken-mcp",
		"model":     "strong",
		"use_tools": true,
		"tools":     []string{},
		"prompt":    "You review code.",
		"mcp_servers": map[string]any{
			"broken-srv": map[string]any{
				"command": "/nonexistent-clai-test-binary",
			},
		},
	}
	body, err := json.Marshal(profile)
	if err != nil {
		t.Fatalf("Marshal(profile): %v", err)
	}
	profilePath := filepath.Join(confDir, "profiles", "broken-mcp.json")
	if err := os.WriteFile(profilePath, body, 0o644); err != nil {
		t.Fatalf("WriteFile(%v): %v", profilePath, err)
	}

	var status int
	_, stderr := e2etest.CaptureStdoutStderr(t, func() {
		status = harness.Run([]string{"-r", "-p", "broken-mcp", "query", "hello"})
	})
	if status == 0 {
		t.Fatalf("status = 0, want failure when explicit MCP server cannot start")
	}
	if strings.Contains(stderr, "trying mock_test") {
		t.Fatalf("stderr = %q, want no fallback attempt when an explicit MCP server fails", stderr)
	}
	if !strings.Contains(stderr, "broken-srv") {
		t.Fatalf("stderr = %q, want it to name broken-srv", stderr)
	}
}

// TestModelAlias_MultipleExplicitMcpFailuresReportAllServersWithoutFallback
// pins that when two explicit MCP servers fail to start, both server names are
// preserved in the error without falling back through the alias ladder.
func TestModelAlias_MultipleExplicitMcpFailuresReportAllServersWithoutFallback(t *testing.T) {
	confDir := modelAliasFixture(t, "test", map[string][]string{
		"strong": {"test", "mock_test"},
	})
	profile := map[string]any{
		"name":      "two-broken-mcp",
		"model":     "strong",
		"use_tools": true,
		"tools":     []string{},
		"prompt":    "You review code.",
		"mcp_servers": map[string]any{
			"broken-srv-a": map[string]any{
				"command": "/nonexistent-clai-test-binary-a",
			},
			"broken-srv-b": map[string]any{
				"command": "/nonexistent-clai-test-binary-b",
			},
		},
	}
	body, err := json.Marshal(profile)
	if err != nil {
		t.Fatalf("Marshal(profile): %v", err)
	}
	profilePath := filepath.Join(confDir, "profiles", "two-broken-mcp.json")
	if err := os.WriteFile(profilePath, body, 0o644); err != nil {
		t.Fatalf("WriteFile(%v): %v", profilePath, err)
	}

	var status int
	_, stderr := e2etest.CaptureStdoutStderr(t, func() {
		status = harness.Run([]string{"-r", "-p", "two-broken-mcp", "query", "hello"})
	})
	if status == 0 {
		t.Fatalf("status = 0, want failure when explicit MCP servers cannot start")
	}
	if strings.Contains(stderr, "trying mock_test") {
		t.Fatalf("stderr = %q, want no fallback attempt when explicit MCP servers fail", stderr)
	}
	for _, want := range []string{"broken-srv-a", "broken-srv-b"} {
		if !strings.Contains(stderr, want) {
			t.Fatalf("stderr = %q, want it to name %q", stderr, want)
		}
	}
}

// TestModelAlias_CorruptPhotoConfigFailsWithoutFallback pins that a post-build
// configuration error in buildTextRun (such as a malformed photoConfig.json)
// stops immediately instead of retrying across every model in the alias ladder.
func TestModelAlias_CorruptPhotoConfigFailsWithoutFallback(t *testing.T) {
	confDir := modelAliasFixture(t, "strong", map[string][]string{
		"strong": {"test", "mock_test"},
	})
	if err := os.WriteFile(filepath.Join(confDir, "photoConfig.json"), []byte(`{"model":`), 0o644); err != nil {
		t.Fatalf("WriteFile(photoConfig.json): %v", err)
	}

	var status int
	_, stderr := e2etest.CaptureStdoutStderr(t, func() {
		status = harness.Run([]string{"-r", "query", "hello"})
	})
	if status == 0 {
		t.Fatalf("status = 0, want failure when photoConfig.json is corrupt")
	}
	if strings.Contains(stderr, "trying mock_test") {
		t.Fatalf("stderr = %q, want no fallback attempt when photoConfig.json is corrupt", stderr)
	}
}

// TestModelAlias_ReplyModeUnavailableChainLeavesConversationIntact pins that
// when a -re query fails because the alias candidates are unavailable before
// producing output, it does not mint an unanswered conversation file or
// overwrite globalScope.json.
func TestModelAlias_ReplyModeUnavailableChainLeavesConversationIntact(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"type":"error","error":{"type":"not_found_error","message":"model: no such model"}}`))
	}))
	t.Cleanup(srv.Close)

	confDir := modelAliasFixture(t, "test", map[string][]string{
		"strong": {"claude-dead", "no-such-vendor-model"},
	})
	t.Setenv("ANTHROPIC_API_KEY", "test-key")
	modelConf, err := json.Marshal(map[string]any{
		"model":      "claude-dead",
		"url":        srv.URL,
		"max_tokens": 16,
	})
	if err != nil {
		t.Fatalf("Marshal(model config): %v", err)
	}
	if err := os.WriteFile(filepath.Join(confDir, "anthropic_claude_claude-dead.json"), modelConf, 0o644); err != nil {
		t.Fatalf("WriteFile(model config): %v", err)
	}

	// Seed a first turn in globalScope.json.
	var status int
	e2etest.CaptureStdoutStderr(t, func() {
		status = harness.Run([]string{"-r", "-cm", "test", "query", "first", "turn"})
	})
	if status != 0 {
		t.Fatalf("first turn status = %d, want 0", status)
	}

	// Reply with -re using an alias whose candidates fail before producing output.
	e2etest.CaptureStdoutStderr(t, func() {
		status = harness.Run([]string{"-r", "-re", "-cm", "strong", "query", "second", "turn"})
	})
	if status == 0 {
		t.Fatalf("second turn status = 0, want failure when alias chain is exhausted")
	}

	entries, err := os.ReadDir(filepath.Join(confDir, "conversations"))
	if err != nil {
		t.Fatalf("ReadDir(conversations): %v", err)
	}
	var promoted []string
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".json") && entry.Name() != "globalScope.json" {
			promoted = append(promoted, entry.Name())
		}
	}
	if len(promoted) != 1 {
		t.Fatalf("promoted conversations = %v, want only the first turn's conversation file", promoted)
	}
}

// TestModelAlias_CloudPublishAliasAndRunRemoteViaFlagAndProfile exercises
// publishing a local model alias via `clai cloud publish alias strong`, then
// resolving and falling back through that remote model list via `-cm cchub:...`
// and via a profile's `"model": "cchub:..."` without requiring trust.json.
func TestModelAlias_CloudPublishAliasAndRunRemoteViaFlagAndProfile(t *testing.T) {
	env := setupRemoteE2E(t)
	api := httptest.NewServer(env.hub)
	t.Cleanup(api.Close)
	hubURL := env.hub.srv.URL
	t.Setenv("CLAI_CLOUD_TOKEN", fakeHubToken)

	writeTextConfig(t, env.confDir, "test", map[string][]string{
		"strong": {"claude-opus-4-7", "test", "claude-opus-4-7"},
	})
	if err := os.WriteFile(filepath.Join(env.confDir, "photoConfig.json"), []byte(`{"model-aliases":{"strong":["dall-e-3","gpt-image-1"]}}`), 0o644); err != nil {
		t.Fatal(err)
	}

	// 1a. Publish the photo mode's `strong` alias via macro mode (`clai cloud publish alias strong photo`).
	t.Setenv("CLAI_CLOUD_URL", api.URL)
	status, stdout, stderr := runRemote(t, "cloud publish alias strong photo -name strong-photo -handle ada -summary Photo.")
	if status != 0 || !strings.Contains(stdout, "Published cchub:ada/strong-photo@1\n") {
		t.Fatalf("cloud publish alias strong photo: status %d stdout %q stderr %q", status, stdout, stderr)
	}
	photoDigest := digestOf(`["dall-e-3","gpt-image-1"]`)
	if !strings.Contains(stdout, "clai -photo-model cchub:ada/strong-photo@1#sha256="+photoDigest) {
		t.Fatalf("cloud publish alias strong photo output missing pinned -photo-model hint: %q", stdout)
	}

	// 1b. Publish the text mode's `strong` alias via macro mode (`clai cloud publish alias strong text`).
	status, stdout, stderr = runRemote(t, "cloud publish alias strong text -handle ada -summary Frontier.")
	if status != 0 || !strings.Contains(stdout, "Published cchub:ada/strong@1\n") {
		t.Fatalf("cloud publish alias strong text: status %d stdout %q stderr %q", status, stdout, stderr)
	}
	v1Digest := digestOf(`["claude-opus-4-7","test"]`)
	if !strings.Contains(stdout, "clai -chat-model cchub:ada/strong@1#sha256="+v1Digest) {
		t.Fatalf("cloud publish alias strong text output missing pinned -chat-model hint: %q", stdout)
	}

	// Remove the local `strong` alias from textConfig.json to prove the run
	// resolves the remote model list directly rather than importing into local config.
	writeTextConfig(t, env.confDir, "test", nil)

	// 2. Run with `-cm cchub:ada/strong@1` (non-interactive, no trust.json entry):
	// claude-opus-4-7 has no ANTHROPIC_API_KEY, so it falls back to `test`.
	t.Setenv("CLAI_CLOUD_URL", hubURL)
	status, stdout, stderr = runRemote(t, "-r -cm cchub:ada/strong@1 q hello remote alias")
	if status != 0 || stdout != "hello remote alias\n" {
		t.Fatalf("-cm remote alias: status %d stdout %q stderr %q", status, stdout, stderr)
	}
	if !strings.Contains(stderr, "model claude-opus-4-7 unavailable") || !strings.Contains(stderr, "trying test") {
		t.Fatalf("-cm remote alias stderr = %q, want fallback notice from claude-opus-4-7 to test", stderr)
	}

	// 3. Reference `cchub:ada/strong@1` as `"model"` inside a local profile.
	profileBody := `{"name":"remote-tier","model":"cchub:ada/strong@1","prompt":"You are helpful."}`
	if err := os.WriteFile(filepath.Join(env.confDir, "profiles", "remote-tier.json"), []byte(profileBody), 0o644); err != nil {
		t.Fatal(err)
	}
	status, stdout, stderr = runRemote(t, "-r -p remote-tier q hello profile remote alias")
	if status != 0 || stdout != "hello profile remote alias\n" {
		t.Fatalf("profile remote alias: status %d stdout %q stderr %q", status, stdout, stderr)
	}

	// 4. Verify a wrong #sha256= on a remote model list is refused with exit status 3.
	status, _, stderr = runRemote(t, "-r -cm cchub:ada/strong@1#sha256="+strings.Repeat("0", 64)+" q hello")
	if status != 3 || !strings.Contains(stderr, "does not match its #sha256=") {
		t.Fatalf("wrong sha256: status %d stderr %q, want exit 3", status, stderr)
	}
}
