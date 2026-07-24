package main

import (
	"strings"
	"testing"

	"github.com/baalimago/clai/internal/setup"
	"github.com/baalimago/go_away_boilerplate/pkg/table"
	"github.com/baalimago/go_away_boilerplate/pkg/testboil"
)

func Test_run_setup_returns_success_on_user_exit(t *testing.T) {
	_ = setupMainTestConfigDir(t)

	restoreInput := useReadUserInputForTests(func() (string, error) {
		return "", table.ErrUserInitiatedExit
	})
	t.Cleanup(restoreInput)

	var gotStatus int
	stdout := testboil.CaptureStdout(t, func(t *testing.T) {
		gotStatus = run([]string{"setup"})
	})

	testboil.FailTestIfDiff(t, gotStatus, 0)
	if stdout == "" {
		t.Fatal("expected setup command to print interactive output before quit")
	}
}

func Test_setup_initcmd_user_exit_is_not_an_error_regression(t *testing.T) {
	_ = setupMainTestConfigDir(t)

	restoreInput := useReadUserInputForTests(func() (string, error) {
		return "", table.ErrUserInitiatedExit
	})
	t.Cleanup(restoreInput)

	err := setup.InitCmd()
	if err == nil {
		t.Fatal("expected setup.InitCmd to return user exit sentinel")
	}
	if err != table.ErrUserInitiatedExit {
		t.Fatalf("expected user exit sentinel, got: %v", err)
	}
}

func Test_run_setup_shell_context_ufe_back_does_not_duplicate_back_hotkey(t *testing.T) {
	_ = setupMainTestConfigDir(t)

	inputs := []string{"4", "0", "ufe", "b", "b"}
	inputIdx := 0
	restoreInput := useReadUserInputForTests(func() (string, error) {
		if inputIdx >= len(inputs) {
			return "", table.ErrUserInitiatedExit
		}
		ret := inputs[inputIdx]
		inputIdx++
		return ret, nil
	})
	t.Cleanup(restoreInput)

	var gotStatus int
	stderr := testboil.CaptureStderr(t, func(t *testing.T) {
		gotStatus = run([]string{"setup"})
	})

	if gotStatus != 0 {
		t.Fatalf("expected success status, got %d, stderr: %q", gotStatus, stderr)
	}
	if strings.Contains(stderr, `duplicate table action hotkey "b"`) {
		t.Fatalf("unexpected duplicate back hotkey error: %q", stderr)
	}
}
