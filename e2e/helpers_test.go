package e2e

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"testing"

	"github.com/baalimago/clai/internal/chat"
	"github.com/baalimago/clai/internal/cli"
	"github.com/baalimago/clai/internal/setup"
	"github.com/baalimago/clai/internal/skills"
	"github.com/baalimago/clai/internal/summary"
	"github.com/baalimago/clai/internal/text"
	"github.com/baalimago/clai/internal/utils"
	"github.com/baalimago/go_away_boilerplate/pkg/cmd"
)

const cpuProfileFileName = cli.CPUProfileFileName

// The test binary owns the two collaborators the real entry point injects, so
// a row can force a refusal without mutating state in internal/cli. Every
// helper below rebuilds Deps from them at call time.
var (
	newSummarizer = summary.NewAgentSummarizer
	foreignCache  = chat.DefaultForeignCache
)

func testDeps() cli.Deps {
	deps := cli.DefaultDeps()
	deps.NewSummarizer = newSummarizer
	deps.ForeignCache = foreignCache
	return deps
}

func run(args []string) int { return cli.Run(args, testDeps()) }

func runProfiled(args []string) int { return cli.RunProfiled(args, testDeps()) }

func commands() map[string]cmd.Command { return cli.Commands(testDeps()) }

// moduleRoot returns the repository root from this file's own location, so
// rows that read tracked files (architecture docs, examples) do not depend on
// the process working directory.
func moduleRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed, cannot locate the module root")
	}
	return filepath.Dir(filepath.Dir(file))
}

// useReadUserInputForTests bridges the old table.UseReadUserInputForTests
// pattern with the new setup.Input mechanism for the e2e tests.
func useReadUserInputForTests(fn func() (string, error)) func() {
	old := setup.Input
	r, w := io.Pipe()
	go func() {
		defer w.Close()
		for {
			s, err := fn()
			if err != nil {
				w.CloseWithError(err)
				return
			}
			if _, writeErr := io.WriteString(w, s+"\n"); writeErr != nil {
				return
			}
		}
	}()
	setup.Input = r
	return func() {
		setup.Input = old
		r.Close()
	}
}

func setupMainTestConfigDir(t *testing.T) string {
	t.Helper()
	// A developer's key would make every query run fetch the live OpenRouter
	// catalog; keyless CI never pays that, so neither should local runs.
	t.Setenv("OPENROUTER_API_KEY", "")

	confDir := t.TempDir()
	required := []string{
		"conversations",
		"profiles",
		"mcpServers",
		"conversations/dirs",
		"shellContexts",
		"skills",
	}
	for _, dir := range required {
		if err := os.MkdirAll(filepath.Join(confDir, dir), 0o755); err != nil {
			t.Fatalf("MkdirAll(%q): %v", dir, err)
		}
	}

	themeContent := `{
  "primary": "",
  "secondary": "",
  "breadtext": "",
  "roleSystem": "",
  "roleUser": "",
  "roleTool": "",
  "roleReasoning": "",
  "roleOther": "",
  "notificationBell": true,
  "tableItems": 10,
  "toolOutputRows": 6,
  "rollingOutput": {
    "enabled": true,
    "windowCellHeight": 30
  }
}`
	if err := os.WriteFile(filepath.Join(confDir, "theme.json"), []byte(themeContent), 0o644); err != nil {
		t.Fatalf("WriteFile(theme.json): %v", err)
	}

	priceConfig := map[string]any{
		"price": map[string]any{
			"input_usd_per_token":        0.001,
			"input_cached_usd_per_token": 0.0005,
			"output_usd_per_token":       0.002,
		},
	}
	priceBytes, err := json.Marshal(priceConfig)
	if err != nil {
		t.Fatalf("Marshal(price config): %v", err)
	}
	priceFiles := []string{
		"mock_test_test.json",
		"mock_test_mock_test.json",
	}
	for _, name := range priceFiles {
		if err := os.WriteFile(filepath.Join(confDir, name), priceBytes, 0o644); err != nil {
			t.Fatalf("WriteFile(%q): %v", name, err)
		}
	}

	// The shared fixture runs with the in-flight summarizer off so the
	// pre-existing e2e rows cost what they cost on main; the summary rows
	// opt in through setupSummaryE2E (worklog
	// 2026-09-09-conversation-summaries, phase 8 notes).
	textConf := text.Default
	textConf.SummarizeConversations = false
	if err := utils.CreateFile(filepath.Join(confDir, "textConfig.json"), &textConf); err != nil {
		t.Fatalf("CreateFile(textConfig.json): %v", err)
	}
	if err := utils.CreateFile(filepath.Join(confDir, "skills.json"), &skills.Config{
		Enabled:            false,
		GlobalSkillDirs:    []string{},
		ProjectSkillDirs:   []string{"./agents/skills", ".claude/skills"},
		TrustAllSkills:     false,
		MaxActivatedSkills: 10,
	}); err != nil {
		t.Fatalf("CreateFile(skills.json): %v", err)
	}

	t.Setenv("CLAI_CONFIG_DIR", confDir)
	// setupTooling resolves the production schema cache through
	// utils.GetClaiCacheDir; without this, every e2e test that reaches
	// it resolves the developer's real cache directory the moment any of
	// them configures a non-empty mcpServers directory (R2-20).
	t.Setenv("CLAI_CACHE_DIR", filepath.Join(confDir, "cache"))

	return confDir
}

// testServerBinary compiles the shared stdio fixture exactly once per test
// binary run and returns its path, mirroring internal/text's and
// internal/tools/mcp's own helpers of the same name: an e2e test's
// connect bound must never enclose a go run compile, or a cold Go build
// cache measures the build rather than the code under test (D41, invariant
// 15; R2-19/R2-01, phase 8).
func testServerBinary(t *testing.T) string {
	t.Helper()
	testServerBinOnce.Do(func() {
		f, err := os.CreateTemp("", "clai-mcp-testserver-main-*")
		if err != nil {
			testServerBinErr = fmt.Errorf("create temp file: %w", err)
			return
		}
		f.Close()
		cmd := exec.Command("go", "build", "-o", f.Name(), "github.com/baalimago/clai/internal/tools/mcp/testserver")
		if out, buildErr := cmd.CombinedOutput(); buildErr != nil {
			testServerBinErr = fmt.Errorf("go build testserver: %w: %s", buildErr, out)
			return
		}
		testServerBinPath = f.Name()
	})
	if testServerBinErr != nil {
		t.Fatalf("build testserver binary: %v", testServerBinErr)
	}
	return testServerBinPath
}

var (
	testServerBinOnce sync.Once
	testServerBinPath string
	testServerBinErr  error
)

// blankDebugAndVendorKeys keeps a fixture host-insensitive: no debug
// chatter on the captured streams and no developer key that could select a
// paid vendor.
func blankDebugAndVendorKeys(t *testing.T) {
	t.Helper()
	for _, key := range []string{"DEBUG", "DEBUG_SUMMARY", "DEBUG_CHAT", "DEBUG_STOPLOSS", "OPENROUTER_API_KEY", "OPENAI_API_KEY", "ANTHROPIC_API_KEY"} {
		t.Setenv(key, "")
	}
}
