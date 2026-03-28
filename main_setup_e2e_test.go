package main

import (
	"testing"

	"github.com/baalimago/clai/internal/setup"
	"github.com/baalimago/clai/internal/utils"
	"github.com/baalimago/go_away_boilerplate/pkg/testboil"
)

func Test_run_setup_returns_success_on_user_exit(t *testing.T) {
	_ = setupMainTestConfigDir(t)

	restoreInput := utils.UseReadUserInputForTests(func() (string, error) {
		return "", utils.ErrUserInitiatedExit
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

	restoreInput := utils.UseReadUserInputForTests(func() (string, error) {
		return "", utils.ErrUserInitiatedExit
	})
	t.Cleanup(restoreInput)

	err := setup.InitCmd()
	if err == nil {
		t.Fatal("expected setup.InitCmd to return user exit sentinel")
	}
	if err != utils.ErrUserInitiatedExit {
		t.Fatalf("expected user exit sentinel, got: %v", err)
	}
}
