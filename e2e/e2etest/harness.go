// Package e2etest is the shared support of the e2e test packages: a harness
// that drives cli.Run in-process, and the fixtures every package builds on.
package e2etest

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/baalimago/clai/internal/cli"
	"github.com/baalimago/clai/internal/models"
	"github.com/baalimago/clai/internal/utils"
	"github.com/baalimago/go_away_boilerplate/pkg/cmd"
	"github.com/baalimago/go_away_boilerplate/pkg/testboil"
)

// SetupPTYHelperEnv marks the child process of the interactive setup PTY test.
const SetupPTYHelperEnv = "CLAI_SETUP_PTY_HELPER"

// Harness holds one test package's CLI collaborators; every run reads Deps at call time.
type Harness struct {
	Deps cli.Deps
}

// NewHarness starts from the production collaborators.
func NewHarness() *Harness { return &Harness{Deps: cli.DefaultDeps()} }

func (h *Harness) Run(args []string) int { return cli.Run(args, h.Deps) }

func (h *Harness) RunProfiled(args []string) int { return cli.RunProfiled(args, h.Deps) }

func (h *Harness) Commands() map[string]cmd.Command { return cli.Commands(h.Deps) }

// RunOne runs args from cwd with stdout captured. Args split on spaces, and
// an empty-quote token is an explicit empty argument (shell-style).
func (h *Harness) RunOne(t *testing.T, cwd string, args string) (string, int) {
	t.Helper()
	oldWd, wdErr := os.Getwd()
	t.Cleanup(func() {
		if wdErr == nil {
			_ = os.Chdir(oldWd)
		}
		utils.Live = true
		utils.ReadonlyConfig = false
		utils.NoCreateConfig = false
	})

	if chDirErr := os.Chdir(cwd); chDirErr != nil {
		t.Fatalf("Chdir(%q): %v", cwd, chDirErr)
	}

	parsed := splitArgsForTest(args)

	var status int
	stdout := testboil.CaptureStdout(t, func(t *testing.T) {
		status = h.Run(parsed)
	})
	return stdout, status
}

func splitArgsForTest(args string) []string {
	parts := strings.Split(args, " ")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p == `""` || p == `''` {
			out = append(out, "")
		} else {
			out = append(out, p)
		}
	}
	return out
}

// RunAudio returns status, stdout and stderr, restoring os.Args afterwards.
func (h *Harness) RunAudio(t *testing.T, args string) (int, string, string) {
	t.Helper()
	oldArgs := os.Args
	t.Cleanup(func() { os.Args = oldArgs })
	var status int
	var stdout string
	stderr := testboil.CaptureStderr(t, func(t *testing.T) {
		stdout = testboil.CaptureStdout(t, func(t *testing.T) {
			status = h.Run(strings.Split(args, " "))
		})
	})
	return status, stdout, stderr
}

// RefusingSummarizer is what every e2e TestMain injects; only summary rows opt out.
func RefusingSummarizer(string) (models.Summarizer, error) {
	return nil, errors.New("real summarizer is off in the test binary; opt in through setupSummaryE2E")
}

// DetachTTY points the CLI's terminal at /dev/null, except in the PTY helper child.
func DetachTTY() {
	if os.Getenv(SetupPTYHelperEnv) == "" {
		_ = os.Setenv("TTY", "/dev/null")
	}
}
