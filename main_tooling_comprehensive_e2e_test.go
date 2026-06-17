package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	pkgtools "github.com/baalimago/clai/pkg/tools"
)

// --- Sync Command Scenarios ---

// Test_e2e_sync_cmd_basic: simple sync command completes
func Test_e2e_sync_cmd_basic(t *testing.T) {
	oldArgs := os.Args
	t.Cleanup(func() { os.Args = oldArgs })
	_ = setupMainTestConfigDir(t)
	t.Setenv("CLAI_MOCK_CMD_COMMAND", `printf "sync-ok"`)

	var gotStatus int
	stdout, stderr := captureStdoutStderr(t, func() {
		gotStatus = run(strings.Split("-r -cm mock_test -t=cmd q tool_cmd", " "))
	})
	if gotStatus != 0 {
		t.Fatalf("expected success, got %d stdout=%q stderr=%q", gotStatus, stdout, stderr)
	}
	combined := stdout + stderr
	if !strings.Contains(combined, "sync-ok") {
		t.Fatalf("expected sync-ok in output, got %q", combined)
	}
}

// Test_e2e_sync_cmd_freetext: freetext_cmd works as sync
func Test_e2e_sync_cmd_freetext(t *testing.T) {
	oldArgs := os.Args
	t.Cleanup(func() { os.Args = oldArgs })
	_ = setupMainTestConfigDir(t)
	t.Setenv("CLAI_MOCK_CMD_COMMAND", `printf "freetext-ok"`)

	// freetext_cmd and cmd share the same mock env var
	var gotStatus int
	stdout, stderr := captureStdoutStderr(t, func() {
		gotStatus = run(strings.Split("-r -cm mock_test -t=freetext_command q tool_freetext_command", " "))
	})
	if gotStatus != 0 {
		t.Fatalf("expected success, got %d stdout=%q stderr=%q", gotStatus, stdout, stderr)
	}
	combined := stdout + stderr
	if !strings.Contains(combined, "freetext-ok") {
		t.Fatalf("expected freetext-ok in output, got %q", combined)
	}
}

// --- Async Command Scenarios ---

// Test_e2e_async_await_timeout: slow command + short timeout = timed_out
func Test_e2e_async_await_timeout(t *testing.T) {
	oldArgs := os.Args
	t.Cleanup(func() { os.Args = oldArgs })
	_ = setupMainTestConfigDir(t)
	pkgtools.ResetAsyncCmdManagerForTests()

	asyncCmdFile := filepath.Join(t.TempDir(), "async_cmd_id.txt")
	pkgtools.SetAsyncSpawnObserverForTests(func(id string) { _ = os.WriteFile(asyncCmdFile, []byte(id), 0o644) })
	t.Setenv("CLAI_MOCK_ASYNC_CMD_RUN_COMMAND", "sh")
	t.Setenv("CLAI_MOCK_ASYNC_CMD_RUN_ARGS", `-c "sleep 5"`)
	t.Setenv("CLAI_MOCK_ASYNC_CMD_ID_FILE", asyncCmdFile)
	// Await timeout very short; command takes 5s, so it must time out
	t.Setenv("CLAI_MOCK_ASYNC_CMD_AWAIT_TIMEOUT_SECONDS", "0.2")

	var gotStatus int
	stdout, stderr := captureStdoutStderr(t, func() {
		gotStatus = run(strings.Split("-r -cm mock_test -t=async_cmd_run,async_cmd_await q tool_async_cmd_run tool_async_cmd_await", " "))
	})
	if gotStatus != 0 {
		t.Fatalf("expected success, got %d stdout=%q stderr=%q", gotStatus, stdout, stderr)
	}
	combined := stdout + stderr
	if !strings.Contains(combined, `"result":"timed_out"`) {
		t.Fatalf("expected timed_out await result, got %q", combined)
	}
}

// Test_e2e_async_long_running_logs: spawn longer command, check logs multiple times, then await
func Test_e2e_async_long_running_logs(t *testing.T) {
	oldArgs := os.Args
	t.Cleanup(func() { os.Args = oldArgs })
	_ = setupMainTestConfigDir(t)
	pkgtools.ResetAsyncCmdManagerForTests()

	asyncCmdFile := filepath.Join(t.TempDir(), "async_cmd_id.txt")
	pkgtools.SetAsyncSpawnObserverForTests(func(id string) { _ = os.WriteFile(asyncCmdFile, []byte(id), 0o644) })
	t.Setenv("CLAI_MOCK_ASYNC_CMD_RUN_COMMAND", "sh")
	t.Setenv("CLAI_MOCK_ASYNC_CMD_RUN_ARGS", `-c "printf mid-output; sleep 0.5; printf final-output"`)
	t.Setenv("CLAI_MOCK_ASYNC_CMD_ID_FILE", asyncCmdFile)
	t.Setenv("CLAI_MOCK_ASYNC_CMD_AWAIT_TIMEOUT_SECONDS", "3")

	var gotStatus int
	stdout, stderr := captureStdoutStderr(t, func() {
		gotStatus = run(strings.Split("-r -cm mock_test -t=async_cmd_run,async_cmd_logs,async_cmd_await,async_cmd_logs q tool_async_cmd_run tool_async_cmd_logs tool_async_cmd_await tool_async_cmd_logs", " "))
	})
	if gotStatus != 0 {
		t.Fatalf("expected success, got %d stdout=%q stderr=%q", gotStatus, stdout, stderr)
	}
	combined := stdout + stderr
	if !strings.Contains(combined, `"result":"completed"`) {
		t.Fatalf("expected completed await result, got %q", combined)
	}
	if !strings.Contains(combined, `"preview":"mid-outputfinal-output"`) {
		t.Fatalf("expected final logs preview to contain mid-outputfinal-output, got %q", combined)
	}
}

// Test_e2e_async_cancel_then_status: spawn, cancel, then check status
func Test_e2e_async_cancel_then_status(t *testing.T) {
	oldArgs := os.Args
	t.Cleanup(func() { os.Args = oldArgs })
	_ = setupMainTestConfigDir(t)
	pkgtools.ResetAsyncCmdManagerForTests()

	asyncCmdFile := filepath.Join(t.TempDir(), "async_cmd_id.txt")
	pkgtools.SetAsyncSpawnObserverForTests(func(id string) { _ = os.WriteFile(asyncCmdFile, []byte(id), 0o644) })
	t.Setenv("CLAI_MOCK_ASYNC_CMD_RUN_COMMAND", "sh")
	t.Setenv("CLAI_MOCK_ASYNC_CMD_RUN_ARGS", `-c "sleep 30"`)
	t.Setenv("CLAI_MOCK_ASYNC_CMD_ID_FILE", asyncCmdFile)

	var gotStatus int
	stdout, stderr := captureStdoutStderr(t, func() {
		gotStatus = run(strings.Split("-r -cm mock_test -t=async_cmd_run,async_cmd_cancel,async_cmd_status q tool_async_cmd_run tool_async_cmd_cancel tool_async_cmd_status", " "))
	})
	if gotStatus != 0 {
		t.Fatalf("expected success, got %d stdout=%q stderr=%q", gotStatus, stdout, stderr)
	}
	combined := stdout + stderr
	if !strings.Contains(combined, `"status":"cancelled"`) {
		t.Fatalf("expected cancelled status in output, got %q", combined)
	}
}

// Test_e2e_async_failed_command: spawn a failing command and see it in status
func Test_e2e_async_failed_command(t *testing.T) {
	oldArgs := os.Args
	t.Cleanup(func() { os.Args = oldArgs })
	_ = setupMainTestConfigDir(t)
	pkgtools.ResetAsyncCmdManagerForTests()

	asyncCmdFile := filepath.Join(t.TempDir(), "async_cmd_id.txt")
	pkgtools.SetAsyncSpawnObserverForTests(func(id string) { _ = os.WriteFile(asyncCmdFile, []byte(id), 0o644) })
	t.Setenv("CLAI_MOCK_ASYNC_CMD_RUN_COMMAND", "sh")
	t.Setenv("CLAI_MOCK_ASYNC_CMD_RUN_ARGS", `-c "exit 42"`)
	t.Setenv("CLAI_MOCK_ASYNC_CMD_ID_FILE", asyncCmdFile)
	t.Setenv("CLAI_MOCK_ASYNC_CMD_AWAIT_TIMEOUT_SECONDS", "3")

	var gotStatus int
	stdout, stderr := captureStdoutStderr(t, func() {
		gotStatus = run(strings.Split("-r -cm mock_test -t=async_cmd_run,async_cmd_await,async_cmd_status q tool_async_cmd_run tool_async_cmd_await tool_async_cmd_status", " "))
	})
	if gotStatus != 0 {
		t.Fatalf("expected success, got %d stdout=%q stderr=%q", gotStatus, stdout, stderr)
	}
	combined := stdout + stderr
	if !strings.Contains(combined, `"status":"failed"`) {
		t.Fatalf("expected failed status, got %q", combined)
	}
}

// --- Combined Sync + Async Scenarios ---

// Test_e2e_sync_plus_async: cmd (sync) + async_cmd_run + async_cmd_await + async_cmd_logs
func Test_e2e_sync_plus_async(t *testing.T) {
	oldArgs := os.Args
	t.Cleanup(func() { os.Args = oldArgs })
	_ = setupMainTestConfigDir(t)
	pkgtools.ResetAsyncCmdManagerForTests()

	asyncCmdFile := filepath.Join(t.TempDir(), "async_cmd_id.txt")
	pkgtools.SetAsyncSpawnObserverForTests(func(id string) { _ = os.WriteFile(asyncCmdFile, []byte(id), 0o644) })
	t.Setenv("CLAI_MOCK_CMD_COMMAND", `printf "sync-result"`)
	t.Setenv("CLAI_MOCK_ASYNC_CMD_RUN_COMMAND", "sh")
	t.Setenv("CLAI_MOCK_ASYNC_CMD_RUN_ARGS", `-c "sleep 0.2 && printf async-result"`)
	t.Setenv("CLAI_MOCK_ASYNC_CMD_ID_FILE", asyncCmdFile)
	t.Setenv("CLAI_MOCK_ASYNC_CMD_AWAIT_TIMEOUT_SECONDS", "3")

	var gotStatus int
	stdout, stderr := captureStdoutStderr(t, func() {
		gotStatus = run(strings.Split("-r -cm mock_test -t=cmd,async_cmd_run,async_cmd_await,async_cmd_logs q tool_cmd tool_async_cmd_run tool_async_cmd_await tool_async_cmd_logs", " "))
	})
	if gotStatus != 0 {
		t.Fatalf("expected success, got %d stdout=%q stderr=%q", gotStatus, stdout, stderr)
	}
	combined := stdout + stderr
	if !strings.Contains(combined, "sync-result") {
		t.Fatalf("expected sync-result in output, got %q", combined)
	}
	if !strings.Contains(combined, `"async_cmd_id":"async_cmd_`) {
		t.Fatalf("expected async_cmd_run output, got %q", combined)
	}
	if !strings.Contains(combined, `"result":"completed"`) {
		t.Fatalf("expected completed await result, got %q", combined)
	}
	if !strings.Contains(combined, `"preview":"async-result"`) {
		t.Fatalf("expected async-result in logs preview, got %q", combined)
	}
}

// --- Complex Scenario: wildcard tool selection with mixed tools ---

// Test_e2e_wildcard_all_tools: wildcard "*" enables all built-in tools including cmd, async*, mcp*
func Test_e2e_wildcard_all_tools(t *testing.T) {
	oldArgs := os.Args
	t.Cleanup(func() { os.Args = oldArgs })
	_ = setupMainTestConfigDir(t)
	pkgtools.ResetAsyncCmdManagerForTests()

	asyncCmdFile := filepath.Join(t.TempDir(), "async_cmd_id.txt")
	pkgtools.SetAsyncSpawnObserverForTests(func(id string) { _ = os.WriteFile(asyncCmdFile, []byte(id), 0o644) })
	t.Setenv("CLAI_MOCK_CMD_COMMAND", `printf "wildcard-all-cmd"`)
	t.Setenv("CLAI_MOCK_ASYNC_CMD_RUN_COMMAND", "sh")
	t.Setenv("CLAI_MOCK_ASYNC_CMD_RUN_ARGS", `-c "sleep 0.2 && printf wildcard-all-async"`)
	t.Setenv("CLAI_MOCK_ASYNC_CMD_ID_FILE", asyncCmdFile)
	t.Setenv("CLAI_MOCK_ASYNC_CMD_AWAIT_TIMEOUT_SECONDS", "3")

	var gotStatus int
	stdout, stderr := captureStdoutStderr(t, func() {
		gotStatus = run(strings.Split("-r -cm mock_test -t=* q tool_cmd tool_async_cmd_run tool_async_cmd_await tool_async_cmd_logs", " "))
	})
	if gotStatus != 0 {
		t.Fatalf("expected success, got %d stdout=%q stderr=%q", gotStatus, stdout, stderr)
	}
	combined := stdout + stderr
	if !strings.Contains(combined, "wildcard-all-cmd") {
		t.Fatalf("expected cmd output in transcript, got %q", combined)
	}
	if !strings.Contains(combined, `"result":"completed"`) {
		t.Fatalf("expected completed await, got %q", combined)
	}
	if !strings.Contains(combined, `"preview":"wildcard-all-async"`) {
		t.Fatalf("expected async logs preview, got %q", combined)
	}
}

// Test_e2e_async_status_live: check status while command is running
func Test_e2e_async_status_live(t *testing.T) {
	oldArgs := os.Args
	t.Cleanup(func() { os.Args = oldArgs })
	_ = setupMainTestConfigDir(t)
	pkgtools.ResetAsyncCmdManagerForTests()

	asyncCmdFile := filepath.Join(t.TempDir(), "async_cmd_id.txt")
	pkgtools.SetAsyncSpawnObserverForTests(func(id string) { _ = os.WriteFile(asyncCmdFile, []byte(id), 0o644) })
	t.Setenv("CLAI_MOCK_ASYNC_CMD_RUN_COMMAND", "sh")
	t.Setenv("CLAI_MOCK_ASYNC_CMD_RUN_ARGS", `-c "sleep 0.5 && printf live-ok"`)
	t.Setenv("CLAI_MOCK_ASYNC_CMD_ID_FILE", asyncCmdFile)

	var gotStatus int
	stdout, stderr := captureStdoutStderr(t, func() {
		gotStatus = run(strings.Split("-r -cm mock_test -t=async_cmd_run,async_cmd_status,async_cmd_await q tool_async_cmd_run tool_async_cmd_status tool_async_cmd_await", " "))
	})
	if gotStatus != 0 {
		t.Fatalf("expected success, got %d stdout=%q stderr=%q", gotStatus, stdout, stderr)
	}
	combined := stdout + stderr
	if !strings.Contains(combined, `"status":"running"`) || !strings.Contains(combined, `"status":"succeeded"`) {
		t.Fatalf("expected running then succeeded status, got %q", combined)
	}
}
