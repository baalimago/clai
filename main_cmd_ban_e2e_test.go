package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	pkgtools "github.com/baalimago/clai/pkg/tools"
)

// assertCmdBanE2E runs the CLI once and asserts the refusal contract (D7,
// D14): the run exits 0 (no hard stop), the transcript names the matched
// entry and the rule, and — when a marker path is given — the banned command
// never spawned (marker absent).
func assertCmdBanE2E(t *testing.T, args []string, entry, marker string) {
	t.Helper()
	var gotStatus int
	stdout, stderr := captureStdoutStderr(t, func() {
		gotStatus = run(args)
	})
	combined := stdout + stderr
	if gotStatus != 0 {
		t.Fatalf("expected exit 0 (refusal must not hard-stop), got %d stdout=%q stderr=%q", gotStatus, stdout, stderr)
	}
	for _, want := range []string{"banned by policy", `matched entry "` + entry + `"`} {
		if !strings.Contains(combined, want) {
			t.Fatalf("expected %q in output, got %q", want, combined)
		}
	}
	if marker != "" {
		if _, err := os.Stat(marker); !os.IsNotExist(err) {
			t.Fatalf("banned command must never spawn, marker exists: %v", err)
		}
	}
}

// Test_e2e_cmd_ban_flag_path proves the -cmd-ban flag reaches the spawn-point
// check through the real CLI: the mock fabricates `touch <marker>`, the
// freetext tool refuses it, and the marker is never created.
func Test_e2e_cmd_ban_flag_path(t *testing.T) {
	oldArgs := os.Args
	t.Cleanup(func() { os.Args = oldArgs })
	_ = setupMainTestConfigDir(t)
	pkgtools.ResetCmdBanListForTests()
	t.Cleanup(pkgtools.ResetCmdBanListForTests)

	marker := filepath.Join(t.TempDir(), "flag-banned-marker")
	t.Setenv("CLAI_MOCK_CMD_COMMAND", "touch "+marker)

	assertCmdBanE2E(t, []string{"-r", "-cm", "mock_test", "-t=cmd", "-cmd-ban=touch", "q", "tool_cmd"}, "touch", marker)
}

// Test_e2e_cmd_ban_config_file_path proves the textConfig.json `cmd-ban` list
// reaches the spawn-point check. The rest of the config is filled from
// defaults by LoadConfigFromFile.
func Test_e2e_cmd_ban_config_file_path(t *testing.T) {
	oldArgs := os.Args
	t.Cleanup(func() { os.Args = oldArgs })
	confDir := setupMainTestConfigDir(t)
	pkgtools.ResetCmdBanListForTests()
	t.Cleanup(pkgtools.ResetCmdBanListForTests)

	writeJSONFileAny(t, filepath.Join(confDir, "textConfig.json"), map[string]any{"cmd-ban": []string{"touch"}})

	marker := filepath.Join(t.TempDir(), "config-banned-marker")
	t.Setenv("CLAI_MOCK_CMD_COMMAND", "touch "+marker)

	assertCmdBanE2E(t, []string{"-r", "-cm", "mock_test", "-t=cmd", "q", "tool_cmd"}, "touch", marker)
}

// initGitRepo creates a temp git repository with one commit, so `git log`
// succeeds when the CLI runs inside it (used by the profile-path test).
func initGitRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := t.TempDir()
	runGit := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v, output=%s", args, err, out)
		}
	}
	runGit("init")
	runGit("config", "user.email", "cmd-ban-e2e@example.com")
	runGit("config", "user.name", "cmd-ban e2e")
	if err := os.WriteFile(filepath.Join(dir, "file.txt"), []byte("content"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	runGit("add", ".")
	runGit("commit", "-m", "init")
	return dir
}

// Test_e2e_cmd_ban_profile_path proves the profile `cmd_ban` list reaches the
// spawn-point check: `git commit` is refused while `git log` still executes
// (the ban is phrase-specific, not tool-wide).
func Test_e2e_cmd_ban_profile_path(t *testing.T) {
	oldArgs := os.Args
	t.Cleanup(func() { os.Args = oldArgs })
	confDir := setupMainTestConfigDir(t)
	pkgtools.ResetCmdBanListForTests()
	t.Cleanup(pkgtools.ResetCmdBanListForTests)

	writeJSONFileAny(t, filepath.Join(confDir, "profiles", "ban.json"), map[string]any{
		"name":    "ban",
		"model":   "mock_test",
		"cmd-ban": []string{"git commit"},
	})

	t.Run("git commit refused", func(t *testing.T) {
		t.Setenv("CLAI_MOCK_CMD_COMMAND", "git commit -m x")
		assertCmdBanE2E(t, []string{"-r", "-cm", "mock_test", "-t=cmd", "-p", "ban", "q", "tool_cmd"}, "git commit", "")
	})

	t.Run("git log allowed", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("test requires a POSIX shell and git")
		}
		repoDir := initGitRepo(t)
		oldWd, err := os.Getwd()
		if err != nil {
			t.Fatalf("Getwd: %v", err)
		}
		t.Cleanup(func() { _ = os.Chdir(oldWd) })
		if err := os.Chdir(repoDir); err != nil {
			t.Fatalf("Chdir(%q): %v", repoDir, err)
		}
		t.Setenv("CLAI_MOCK_CMD_COMMAND", "git log")

		var gotStatus int
		stdout, stderr := captureStdoutStderr(t, func() {
			gotStatus = run([]string{"-r", "-cm", "mock_test", "-t=cmd", "-p", "ban", "q", "tool_cmd"})
		})
		combined := stdout + stderr
		if gotStatus != 0 {
			t.Fatalf("expected exit 0, got %d stdout=%q stderr=%q", gotStatus, stdout, stderr)
		}
		if !strings.Contains(combined, "init") {
			t.Fatalf("expected git log output to pass through, got %q", combined)
		}
		if strings.Contains(combined, "banned by policy") {
			t.Fatalf("git log must not be banned by entry \"git commit\", got %q", combined)
		}
	})
}

// Test_e2e_cmd_ban_profile_merges_onto_file_base proves the purely additive
// cascade (R1-04 revision): an active profile with cmd_ban merges onto the
// textConfig.json base — the file-base ban survives and the profile ban adds.
func Test_e2e_cmd_ban_profile_merges_onto_file_base(t *testing.T) {
	oldArgs := os.Args
	t.Cleanup(func() { os.Args = oldArgs })
	confDir := setupMainTestConfigDir(t)
	pkgtools.ResetCmdBanListForTests()
	t.Cleanup(pkgtools.ResetCmdBanListForTests)

	writeJSONFileAny(t, filepath.Join(confDir, "textConfig.json"), map[string]any{"cmd-ban": []string{"touch"}})
	writeJSONFileAny(t, filepath.Join(confDir, "profiles", "ban.json"), map[string]any{
		"name":    "ban",
		"model":   "mock_test",
		"cmd-ban": []string{"git commit"},
	})

	t.Run("git commit refused via profile", func(t *testing.T) {
		t.Setenv("CLAI_MOCK_CMD_COMMAND", "git commit -m x")
		assertCmdBanE2E(t, []string{"-r", "-cm", "mock_test", "-t=cmd", "-p", "ban", "q", "tool_cmd"}, "git commit", "")
	})

	t.Run("touch still refused via file base", func(t *testing.T) {
		marker := filepath.Join(t.TempDir(), "additive-marker")
		t.Setenv("CLAI_MOCK_CMD_COMMAND", "touch "+marker)
		assertCmdBanE2E(t, []string{"-r", "-cm", "mock_test", "-t=cmd", "-p", "ban", "q", "tool_cmd"}, "touch", marker)
	})
}

// Test_e2e_cmd_ban_quoted_bypass_refused proves the Phase 1 single-sided
// quote-strip (rule 2, Review 1 R1-01): sh -c 'git commit -m x' flattens into
// tokens containing the phrase, so entry `git commit` refuses it. This test
// fails under a both-sides literal reading of rule 2.
func Test_e2e_cmd_ban_quoted_bypass_refused(t *testing.T) {
	oldArgs := os.Args
	t.Cleanup(func() { os.Args = oldArgs })
	_ = setupMainTestConfigDir(t)
	pkgtools.ResetCmdBanListForTests()
	t.Cleanup(pkgtools.ResetCmdBanListForTests)

	t.Setenv("CLAI_MOCK_CMD_COMMAND", "sh -c 'git commit -m x'")

	assertCmdBanE2E(t, []string{"-r", "-cm", "mock_test", "-t=cmd", "-cmd-ban=git commit", "q", "tool_cmd"}, "git commit", "")
}

// Test_e2e_cmd_ban_async_no_spawn proves the async path refuses a banned
// command before Spawn: the async manager snapshot stays empty and the run
// completes normally.
func Test_e2e_cmd_ban_async_no_spawn(t *testing.T) {
	oldArgs := os.Args
	t.Cleanup(func() { os.Args = oldArgs })
	_ = setupMainTestConfigDir(t)
	pkgtools.ResetAsyncCmdManagerForTests()
	pkgtools.ResetCmdBanListForTests()
	t.Cleanup(pkgtools.ResetCmdBanListForTests)

	t.Setenv("CLAI_MOCK_ASYNC_CMD_RUN_COMMAND", "sh")

	assertCmdBanE2E(t, []string{"-r", "-cm", "mock_test", "-t=async_cmd", "-cmd-ban=sh", "q", "tool_async_cmd"}, "sh", "")
	if got := pkgtools.AsyncCmdSnapshotForTests(); len(got) != 0 {
		t.Fatalf("banned async command must never spawn, snapshot=%+v", got)
	}
}

// Test_e2e_cmd_ban_permissive_default is the regression guard for D4: with no
// ban configuration the freetext tool behaves exactly as before.
func Test_e2e_cmd_ban_permissive_default(t *testing.T) {
	oldArgs := os.Args
	t.Cleanup(func() { os.Args = oldArgs })
	_ = setupMainTestConfigDir(t)
	pkgtools.ResetCmdBanListForTests()
	t.Cleanup(pkgtools.ResetCmdBanListForTests)

	t.Setenv("CLAI_MOCK_CMD_COMMAND", "printf ok")

	var gotStatus int
	stdout, stderr := captureStdoutStderr(t, func() {
		gotStatus = run([]string{"-r", "-cm", "mock_test", "-t=cmd", "q", "tool_cmd"})
	})
	combined := stdout + stderr
	if gotStatus != 0 {
		t.Fatalf("expected success, got %d stdout=%q stderr=%q", gotStatus, stdout, stderr)
	}
	if !strings.Contains(combined, "ok") {
		t.Fatalf("expected mock command output in transcript, got %q", combined)
	}
	if strings.Contains(combined, "banned by policy") {
		t.Fatalf("default must stay permissive, got %q", combined)
	}
}
