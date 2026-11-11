// Package e2etest is the shared support of the e2e test packages: a harness
// that drives cli.Run in-process, and the fixtures every package builds on.
package e2etest

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/baalimago/clai/internal/cli"
	"github.com/baalimago/clai/internal/models"
	"github.com/baalimago/clai/internal/utils"
	"github.com/baalimago/go_away_boilerplate/pkg/cmd"
	"github.com/baalimago/go_away_boilerplate/pkg/testboil"
)

// Harness holds one test package's CLI collaborators; every run reads Deps at call time.
type Harness struct {
	Deps cli.Deps
}

// NewHarness starts from the production collaborators. The retry schedule stays
// production's; only its waiting is skipped, so a failing request cannot spend
// the test gate's timeout.
func NewHarness() *Harness {
	deps := cli.DefaultDeps()
	deps.StepRetry.Wait = func(context.Context, time.Duration) error { return nil }
	return &Harness{Deps: deps}
}

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

// RunOneStreams is RunOne with stderr captured too: diagnostics such as the
// config upgrade announcement are on stderr (F1).
func (h *Harness) RunOneStreams(t *testing.T, cwd string, args string) (string, string, int) {
	t.Helper()
	var stdout string
	var status int
	stderr := testboil.CaptureStderr(t, func(t *testing.T) {
		stdout, status = h.RunOne(t, cwd, args)
	})
	return stdout, stderr, status
}

// CompleteE2E runs one completion request, captured on stdout.
func (h *Harness) CompleteE2E(t *testing.T, words ...string) (int, string) {
	t.Helper()
	var status int
	stdout := testboil.CaptureStdout(t, func(t *testing.T) {
		status = h.Run(append([]string{"__complete", "clai"}, words...))
	})
	return status, stdout
}

// CompleteLinesE2E is CompleteE2E split into its candidate lines; an empty
// stdout is no candidates.
func (h *Harness) CompleteLinesE2E(t *testing.T, words ...string) []string {
	t.Helper()
	status, stdout := h.CompleteE2E(t, words...)
	testboil.FailTestIfDiff(t, status, 0)
	if stdout == "" {
		return nil
	}
	return strings.Split(strings.TrimSuffix(stdout, "\n"), "\n")
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
func RefusingSummarizer(context.Context, string) (models.Summarizer, error) {
	return nil, errors.New("real summarizer is off in the test binary; opt in through setupSummaryE2E")
}
