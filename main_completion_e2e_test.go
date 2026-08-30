package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/baalimago/clai/internal/tools"
	"github.com/baalimago/go_away_boilerplate/pkg/testboil"
)

var builtClai struct {
	once sync.Once
	dir  string
	err  error
}

// builtClaiDir builds the clai binary once per test process (repeated
// -count runs share it) and returns the directory holding it.
func builtClaiDir(t *testing.T) string {
	t.Helper()
	builtClai.once.Do(func() {
		dir, err := os.MkdirTemp("", "clai-e2e-bin")
		if err != nil {
			builtClai.err = err
			return
		}
		builtClai.dir = dir
		out, err := exec.Command("go", "build", "-o", filepath.Join(dir, "clai"), ".").CombinedOutput()
		if err != nil {
			builtClai.err = fmt.Errorf("go build: %w\n%s", err, out)
		}
	})
	if builtClai.err != nil {
		t.Fatal(builtClai.err)
	}
	return builtClai.dir
}

func completeE2E(t *testing.T, words ...string) (int, string) {
	t.Helper()
	var status int
	stdout := testboil.CaptureStdout(t, func(t *testing.T) {
		status = run(append([]string{"__complete", "clai"}, words...))
	})
	return status, stdout
}

func completeLinesE2E(t *testing.T, words ...string) []string {
	t.Helper()
	status, stdout := completeE2E(t, words...)
	testboil.FailTestIfDiff(t, status, 0)
	if stdout == "" {
		return nil
	}
	return strings.Split(strings.TrimSuffix(stdout, "\n"), "\n")
}

func assertContainsLines(t *testing.T, lines []string, want ...string) {
	t.Helper()
	for _, w := range want {
		found := slices.Contains(lines, w)
		if !found {
			t.Fatalf("expected line %q in %v", w, lines)
		}
	}
}

func TestCompletionCommandBashPrintsWrapper(t *testing.T) {
	_ = setupMainTestConfigDir(t)

	var gotStatus int
	stdout := testboil.CaptureStdout(t, func(t *testing.T) {
		gotStatus = run(strings.Split("completion bash", " "))
	})

	testboil.FailTestIfDiff(t, gotStatus, 0)
	testboil.AssertStringContains(t, stdout, "clai __complete")
	testboil.AssertStringContains(t, stdout, "complete -F")
}

func TestCompletionCommandZshPrintsWrapper(t *testing.T) {
	_ = setupMainTestConfigDir(t)

	var gotStatus int
	stdout := testboil.CaptureStdout(t, func(t *testing.T) {
		gotStatus = run(strings.Split("completion zsh", " "))
	})

	testboil.FailTestIfDiff(t, gotStatus, 0)
	testboil.AssertStringContains(t, stdout, "clai __complete")
	testboil.AssertStringContains(t, stdout, "#compdef clai")
}

func Test_e2e_complete_engine(t *testing.T) {
	confDir := setupMainTestConfigDir(t)
	writeJSONFileAny(t, filepath.Join(confDir, "profiles", "gopher.json"), map[string]any{
		"name": "gopher",
	})
	writeJSONFileAny(t, filepath.Join(confDir, "anthropic_claude_sonnet-x.json"), map[string]any{})
	writeJSONFileAny(t, filepath.Join(confDir, "shellContexts", "minimal.json"), map[string]any{})

	t.Run("top-level commands", func(t *testing.T) {
		lines := completeLinesE2E(t, "")
		assertContainsLines(t, lines,
			"query\tplain", "q\tplain", "chat\tplain", "c\tplain", "completion\tplain")
		for _, l := range lines {
			if strings.Contains(l, "__complete") {
				t.Fatalf("__complete must stay hidden, got %v", lines)
			}
		}
	})

	t.Run("per-command flags", func(t *testing.T) {
		lines := completeLinesE2E(t, "q", "-")
		assertContainsLines(t, lines, "-cm\tplain", "-t\tplain", "-mt\tplain")
		// -am/-af are query flags now (they configure the audio_transcribe
		// tool); -pm and -parallelism stay owned by other levels.
		assertContainsLines(t, lines, "-am\tplain", "-af\tplain")
		for _, forbidden := range []string{"-pm\tplain", "-parallelism\tplain"} {
			for _, l := range lines {
				if l == forbidden {
					t.Fatalf("forbidden %q in %v", forbidden, lines)
				}
			}
		}
	})

	t.Run("profile values", func(t *testing.T) {
		assertContainsLines(t, completeLinesE2E(t, "q", "-p", ""), "gopher\tplain")
	})

	t.Run("shell context values", func(t *testing.T) {
		assertContainsLines(t, completeLinesE2E(t, "q", "-asc", ""), "minimal\tplain")
	})

	t.Run("model history", func(t *testing.T) {
		assertContainsLines(t, completeLinesE2E(t, "q", "-cm", ""), "sonnet-x\tplain")
	})

	t.Run("tool comma-split keeps prefix", func(t *testing.T) {
		lines := completeLinesE2E(t, "q", "-t", "website_text,we")
		if len(lines) == 0 {
			t.Fatal("expected comma-continuation suggestions")
		}
		for _, l := range lines {
			if !strings.HasPrefix(l, "website_text,web") {
				t.Fatalf("expected prefix-preserving continuation, got %v", lines)
			}
		}
	})

	t.Run("file and dir kinds", func(t *testing.T) {
		lines := completeLinesE2E(t, "q", "-prp", "")
		if len(lines) != 1 || !strings.HasSuffix(lines[0], "\tfile") {
			t.Fatalf("expected one file-kinded line, got %v", lines)
		}
		lines = completeLinesE2E(t, "photo", "-pd", "")
		if len(lines) != 1 || !strings.HasSuffix(lines[0], "\tdir") {
			t.Fatalf("expected one dir-kinded line, got %v", lines)
		}
	})

	t.Run("chat subs from tree", func(t *testing.T) {
		lines := completeLinesE2E(t, "chat", "")
		assertContainsLines(t, lines, "list\tplain", "l\tplain", "dirv2\tplain", "continue\tplain")
	})

	// The engine reads the hook off the deepest resolved command, so every
	// sub owning a value flag must carry the value source too.
	t.Run("chat sub flag values", func(t *testing.T) {
		assertContainsLines(t, completeLinesE2E(t, "chat", "continue", "-p", ""), "gopher\tplain")
		assertContainsLines(t, completeLinesE2E(t, "c", "c", "-p", ""), "gopher\tplain")
	})

	// The chat tree runs no model, so it offers only the flags it reads.
	t.Run("chat flags", func(t *testing.T) {
		lines := completeLinesE2E(t, "chat", "-")
		assertContainsLines(t, lines, "-r\tplain", "-n\tplain", "-p\tplain")
		for _, forbidden := range []string{"-cm\tplain", "-t\tplain", "-mt\tplain", "-am\tplain", "-af\tplain"} {
			for _, l := range lines {
				if l == forbidden {
					t.Fatalf("forbidden %q in %v", forbidden, lines)
				}
			}
		}
	})

	t.Run("prompt suppression", func(t *testing.T) {
		if lines := completeLinesE2E(t, "q", "hello", ""); len(lines) != 0 {
			t.Fatalf("expected no suggestions inside prompt, got %v", lines)
		}
	})

	t.Run("completion shell names", func(t *testing.T) {
		assertContainsLines(t, completeLinesE2E(t, "completion", ""), "bash\tplain", "zsh\tplain")
	})
}

// Test_e2e_complete_no_config_dir pins the side-effect-free property: value
// completion with no config dir yields nothing, exits 0, and creates no
// files.
func Test_e2e_complete_no_config_dir(t *testing.T) {
	confDir := filepath.Join(t.TempDir(), "missing", ".clai")
	t.Setenv("CLAI_CONFIG_DIR", confDir)
	t.Setenv("HOME", t.TempDir())

	status, stdout := completeE2E(t, "q", "-p", "")
	testboil.FailTestIfDiff(t, status, 0)
	testboil.FailTestIfDiff(t, stdout, "")
	if _, err := os.Stat(confDir); !os.IsNotExist(err) {
		t.Fatalf("completion must not create the config dir, stat err=%v", err)
	}
}

// Test_e2e_complete_tool_init_is_lazy proves tools.Init runs only when a
// tool-value hook actually fires.
func Test_e2e_complete_tool_init_is_lazy(t *testing.T) {
	_ = setupMainTestConfigDir(t)

	tools.WithTestRegistry(t, func() {
		status, _ := completeE2E(t, "q", "-")
		testboil.FailTestIfDiff(t, status, 0)
		if got := len(tools.Registry.All()); got != 0 {
			t.Fatalf("flag-name completion must not init tools, registry has %d entries", got)
		}

		status, _ = completeE2E(t, "q", "-t", "")
		testboil.FailTestIfDiff(t, status, 0)
		if got := len(tools.Registry.All()); got == 0 {
			t.Fatal("tool-value completion should have initialized the registry")
		}
	})
}

// Test_e2e_completion_script_drives_bash sources the emitted bash script and
// asks the shell to complete a real invocation against a built clai binary.
func Test_e2e_completion_script_drives_bash(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not installed; script-driven completion not checked")
	}
	_ = setupMainTestConfigDir(t)

	binDir := builtClaiDir(t)

	script := `set -e
export PATH="` + binDir + `:$PATH"
source <(clai completion bash)
COMP_WORDS=(clai q -c)
COMP_CWORD=2
_clai_completion
printf '%s\n' "${COMPREPLY[@]}"
`
	out, err := exec.Command("bash", "-c", script).CombinedOutput()
	if err != nil {
		t.Fatalf("bash-driven completion failed: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "-cm") {
		t.Fatalf("expected the shell to offer -cm, got:\n%s", out)
	}
}
