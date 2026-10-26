package text

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/baalimago/clai/internal/chat"
	"github.com/baalimago/clai/internal/models"
	"github.com/baalimago/clai/internal/utils"
	pub_models "github.com/baalimago/clai/pkg/text/models"
)

func Test_QueryCommand(t *testing.T) {
	t.Run("wires prep and the real querier path with the mock vendor", func(t *testing.T) {
		confDir := t.TempDir()
		writeMockPriceFile(t, confDir)
		t.Setenv("CLAI_CONFIG_DIR", confDir)
		t.Setenv("HOME", t.TempDir())
		deps := QueryCommandDeps{
			ConfigPrep: func() (string, error) { return confDir, nil },
		}
		c := QueryCommand(deps)
		if c.Describe() == "" || !strings.Contains(c.Help(), "query <text>") {
			t.Fatalf("describe/help incomplete: %q / %q", c.Describe(), c.Help())
		}
		for _, name := range []string{"cm", "t", "g", "re", "dre", "s", "rf", "asc", "r"} {
			if c.Flagset().Lookup(name) == nil {
				t.Fatalf("expected flag %q registered", name)
			}
		}
		if err := c.Flagset().Parse([]string{"-cm", "test", "hello", "there"}); err != nil {
			t.Fatalf("Parse: %v", err)
		}
		if err := c.Setup(t.Context()); err != nil {
			t.Fatalf("Setup: %v", err)
		}
	})

	t.Run("prep error propagates", func(t *testing.T) {
		wantErr := errors.New("prep boom")
		c := QueryCommand(QueryCommandDeps{ConfigPrep: func() (string, error) { return "", wantErr }})
		if err := c.Flagset().Parse(nil); err != nil {
			t.Fatalf("Parse: %v", err)
		}
		if err := c.Setup(context.Background()); !errors.Is(err, wantErr) {
			t.Fatalf("expected prep error, got: %v", err)
		}
	})
}

// TestQueryCommand_newSummarizerError pins D26 at the command boundary: a
// failing NewSummarizer attaches nothing, traces only under DEBUG_SUMMARY,
// and the query runs and persists exactly as without a summarizer; a
// working constructor is attached through the setter and labels the
// persisted conversation.
func TestQueryCommand_newSummarizerError(t *testing.T) {
	t.Setenv("CLAI_DISABLE_COST_ERR_LOG_GOROUTINE", "1")
	t.Setenv("OPENROUTER_API_KEY", "")
	t.Setenv("DEBUG", "")
	runCommand := func(t *testing.T, confDir string, newSummarizer func(string) (models.Summarizer, error)) (string, string) {
		t.Helper()
		// Command.Setup derives the session globals from -r; restore them so
		// later tests in the package see the defaults.
		readonly, noCreate, live := utils.ReadonlyConfig, utils.NoCreateConfig, utils.Live
		t.Cleanup(func() { utils.ReadonlyConfig, utils.NoCreateConfig, utils.Live = readonly, noCreate, live })
		t.Setenv("CLAI_CONFIG_DIR", confDir)
		t.Setenv("HOME", t.TempDir())
		writeMockPriceFile(t, confDir)
		seedEmptyChatIndex(t, confDir)
		// A plain query launches its labeller up front only when it waits
		// for the label.
		c := QueryCommand(QueryCommandDeps{
			ConfigPrep:         func() (string, error) { return confDir, nil },
			NewSummarizer:      newSummarizer,
			SummaryJoinTimeout: 5 * time.Second,
		})
		if err := c.Flagset().Parse([]string{"-r", "-cm", "test", "hello", "there"}); err != nil {
			t.Fatalf("Parse: %v", err)
		}
		var runErr error
		stdout, stderr := captureStdoutStderrText(t, func() {
			if err := c.Setup(context.Background()); err != nil {
				t.Errorf("Setup: %v", err)
				return
			}
			runErr = c.Run(context.Background())
		})
		if runErr != nil {
			t.Fatalf("Run: %v", runErr)
		}
		return stdout, stderr
	}
	savedChat := func(t *testing.T, confDir string) pub_models.Chat {
		t.Helper()
		got, err := chat.LoadPrevQuery(confDir)
		if err != nil {
			t.Fatalf("LoadPrevQuery: %v", err)
		}
		return got
	}

	t.Run("constructor error attaches nothing and stays silent", func(t *testing.T) {
		t.Setenv("DEBUG_SUMMARY", "")
		confDir := t.TempDir()
		stdout, stderr := runCommand(t, confDir, func(string) (models.Summarizer, error) {
			return nil, errors.New("constructor boom")
		})
		if !strings.Contains(stdout, "hello there") {
			t.Fatalf("stdout = %q, want the answer", stdout)
		}
		if stderr != "" {
			t.Fatalf("stderr = %q, want empty", stderr)
		}
		if got := savedChat(t, confDir); got.Title != "" || got.Summary != "" {
			t.Fatalf("persisted chat labelled without a summarizer: %+v", got)
		}
	})

	t.Run("constructor error traces under DEBUG_SUMMARY", func(t *testing.T) {
		t.Setenv("DEBUG_SUMMARY", "1")
		stdout, stderr := runCommand(t, t.TempDir(), func(string) (models.Summarizer, error) {
			return nil, errors.New("constructor boom")
		})
		// Traces go to stderr so they never enter a -r answer stream (R3-17).
		if !strings.Contains(stderr, "[DEBUG_SUMMARY]") || !strings.Contains(stderr, "constructor boom") {
			t.Fatalf("stdout=%q stderr=%q, want the constructor error traced on stderr", stdout, stderr)
		}
		if strings.Contains(stdout, "[DEBUG_SUMMARY]") {
			t.Fatalf("stdout=%q, want no trace in the answer stream", stdout)
		}
	})

	t.Run("constructor result is attached and launched", func(t *testing.T) {
		t.Setenv("DEBUG_SUMMARY", "")
		confDir := t.TempDir()
		var seen string
		fake := instantSummarizer()
		_, stderr := runCommand(t, confDir, func(dir string) (models.Summarizer, error) {
			seen = dir
			return fake, nil
		})
		if seen != confDir {
			t.Fatalf("constructor got %q, want %q", seen, confDir)
		}
		if stderr != "" {
			t.Fatalf("stderr = %q, want empty", stderr)
		}
		// Labelling is best effort: the run never waits for the label, so
		// only the launch is certain here. finalizer_test pins the stamping.
		waitClosed(t, fake.started, "summarizer launch")
	})
}

// TestQueryCommand_summarizerNotBuiltWhenOff pins that an opted-out run
// never constructs the summarizer, while -summarize over a false config
// does (phase 8 notes: the e2e suite's margin under make qa).
func TestQueryCommand_summarizerNotBuiltWhenOff(t *testing.T) {
	t.Setenv("CLAI_DISABLE_COST_ERR_LOG_GOROUTINE", "1")
	t.Setenv("OPENROUTER_API_KEY", "")
	t.Setenv("DEBUG", "")
	run := func(t *testing.T, args ...string) int {
		t.Helper()
		conf := Default
		conf.SummarizeConversations = false
		built := 0
		runQueryCommand(t, conf, QueryCommandDeps{
			NewSummarizer: func(string) (models.Summarizer, error) {
				built++
				return instantSummarizer(), nil
			},
		}, args...)
		return built
	}
	if got := run(t, "hello"); got != 0 {
		t.Fatalf("summarizer built %d times with summarize-conversations off, want 0", got)
	}
	if got := run(t, "-summarize", "hello"); got != 1 {
		t.Fatalf("summarizer built %d times with -summarize over a false config, want 1", got)
	}
}

// runQueryCommand runs "query -r -cm test args..." against a fresh config
// dir holding conf, filling deps.ConfigPrep, and returns what it printed.
func runQueryCommand(t *testing.T, conf Configurations, deps QueryCommandDeps, args ...string) (string, string) {
	t.Helper()
	readonly, noCreate, live := utils.ReadonlyConfig, utils.NoCreateConfig, utils.Live
	t.Cleanup(func() { utils.ReadonlyConfig, utils.NoCreateConfig, utils.Live = readonly, noCreate, live })
	confDir := t.TempDir()
	t.Setenv("CLAI_CONFIG_DIR", confDir)
	t.Setenv("HOME", t.TempDir())
	writeMockPriceFile(t, confDir)
	seedEmptyChatIndex(t, confDir)
	if err := utils.CreateFile(filepath.Join(confDir, "textConfig.json"), &conf); err != nil {
		t.Fatalf("CreateFile: %v", err)
	}
	deps.ConfigPrep = func() (string, error) { return confDir, nil }
	c := QueryCommand(deps)
	if err := c.Flagset().Parse(append([]string{"-r", "-cm", "test"}, args...)); err != nil {
		t.Fatalf("Parse: %v", err)
	}
	return captureStdoutStderrText(t, func() {
		if err := c.Setup(context.Background()); err != nil {
			t.Errorf("Setup: %v", err)
			return
		}
		if err := c.Run(context.Background()); err != nil {
			t.Errorf("Run: %v", err)
		}
	})
}

// TestQueryCommand_querySummary pins the opt-in run summary of a plain query:
// off by default, on through query-summary in textConfig.json or -qs, and
// -qs=false wins over the file.
func TestQueryCommand_querySummary(t *testing.T) {
	t.Setenv("CLAI_DISABLE_COST_ERR_LOG_GOROUTINE", "1")
	t.Setenv("OPENROUTER_API_KEY", "")
	t.Setenv("DEBUG", "")
	run := func(t *testing.T, fromFile bool, args ...string) (string, string) {
		t.Helper()
		conf := Default
		conf.SummarizeConversations = false
		conf.QuerySummary = fromFile
		return runQueryCommand(t, conf, QueryCommandDeps{}, args...)
	}
	const title = "▸ query done in"
	for _, tc := range []struct {
		desc     string
		fromFile bool
		args     []string
		want     bool
	}{
		{"off by default", false, []string{"hello"}, false},
		{"query-summary in textConfig.json", true, []string{"hello"}, true},
		{"-qs flag", false, []string{"-qs", "hello"}, true},
		{"-query-summary flag", false, []string{"-query-summary", "hello"}, true},
		{"-qs=false over the file", true, []string{"-qs=false", "hello"}, false},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			stdout, stderr := run(t, tc.fromFile, tc.args...)
			if strings.Contains(stdout, title) {
				t.Fatalf("stdout = %q, -r keeps the summary off stdout", stdout)
			}
			if got := strings.Contains(stderr, title); got != tc.want {
				t.Fatalf("summary on stderr = %t, want %t; stderr=%q", got, tc.want, stderr)
			}
		})
	}
}
