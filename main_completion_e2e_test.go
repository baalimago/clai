package main

import (
	"os"
	"strings"
	"testing"

	"github.com/baalimago/go_away_boilerplate/pkg/testboil"
)

func TestCompletionCommandBashPrintsWrapper(t *testing.T) {
	oldArgs := os.Args
	t.Cleanup(func() {
		os.Args = oldArgs
	})

	_ = setupMainTestConfigDir(t)

	var gotStatus int
	stdout := testboil.CaptureStdout(t, func(t *testing.T) {
		gotStatus = run(strings.Split("completion bash", " "))
	})

	testboil.FailTestIfDiff(t, gotStatus, 0)
	testboil.AssertStringContains(t, stdout, "__complete")
	testboil.AssertStringContains(t, stdout, "complete -F")
}

func TestCompletionCommandZshPrintsWrapper(t *testing.T) {
	oldArgs := os.Args
	t.Cleanup(func() {
		os.Args = oldArgs
	})

	_ = setupMainTestConfigDir(t)

	var gotStatus int
	stdout := testboil.CaptureStdout(t, func(t *testing.T) {
		gotStatus = run(strings.Split("completion zsh", " "))
	})

	testboil.FailTestIfDiff(t, gotStatus, 0)
	testboil.AssertStringContains(t, stdout, "__complete")
	testboil.AssertStringContains(t, stdout, "#compdef clai")
}

func TestHiddenCompletionOutputsExpectedFormat(t *testing.T) {
	oldArgs := os.Args
	t.Cleanup(func() {
		os.Args = oldArgs
	})

	_ = setupMainTestConfigDir(t)

	var gotStatus int
	stdout := testboil.CaptureStdout(t, func(t *testing.T) {
		gotStatus = run([]string{"__complete", "clai", "chat", ""})
	})

	testboil.FailTestIfDiff(t, gotStatus, 0)
	testboil.FailTestIfDiff(t, stdout, "continue\tplain\ndelete\tplain\nhelp\tplain\nlist\tplain\n")
}
