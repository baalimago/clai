package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	pkgtools "github.com/baalimago/clai/pkg/tools"
)

func Test_e2e_tooling_async_basic_spawn_await_and_logs(t *testing.T) {
	oldArgs := os.Args
	t.Cleanup(func() { os.Args = oldArgs })

	_ = setupMainTestConfigDir(t)
	pkgtools.ResetAsyncCmdManagerForTests()

	asyncCmdFile := filepath.Join(t.TempDir(), "async_cmd_id.txt")
	pkgtools.SetAsyncSpawnObserverForTests(func(id string) { _ = os.WriteFile(asyncCmdFile, []byte(id), 0o644) })
	t.Setenv("CLAI_MOCK_ASYNC_CMD_RUN_COMMAND", "sh")
	t.Setenv("CLAI_MOCK_ASYNC_CMD_RUN_ARGS", `-c "sleep 0.2 && printf done"`)
	t.Setenv("CLAI_MOCK_ASYNC_CMD_ID_FILE", asyncCmdFile)
	t.Setenv("CLAI_MOCK_ASYNC_CMD_AWAIT_TIMEOUT_SECONDS", "2")

	var gotStatus int
	stdout, stderr := captureStdoutStderr(t, func() {
		gotStatus = run(strings.Split("-r -cm mock_test -t=async_cmd_run,async_cmd_await,async_cmd_logs q tool_async_cmd_run tool_async_cmd_await tool_async_cmd_logs", " "))
	})
	if gotStatus != 0 {
		t.Fatalf("expected success, got %d stdout=%q stderr=%q", gotStatus, stdout, stderr)
	}
	combined := stdout + stderr
	if !strings.Contains(combined, `"async_cmd_id":"async_cmd_`) {
		t.Fatalf("expected spawned async command json in output, got %q", combined)
	}
	if !strings.Contains(combined, `"result":"completed"`) {
		t.Fatalf("expected completed await result, got %q", combined)
	}
	if !strings.Contains(combined, `"preview":"done"`) {
		t.Fatalf("expected final logs preview to contain done, got %q", combined)
	}
}

func Test_e2e_tooling_async_live_logs_and_cancel(t *testing.T) {
	oldArgs := os.Args
	t.Cleanup(func() { os.Args = oldArgs })

	_ = setupMainTestConfigDir(t)
	pkgtools.ResetAsyncCmdManagerForTests()

	asyncCmdFile := filepath.Join(t.TempDir(), "async_cmd_id.txt")
	pkgtools.SetAsyncSpawnObserverForTests(func(id string) { _ = os.WriteFile(asyncCmdFile, []byte(id), 0o644) })
	t.Setenv("CLAI_MOCK_ASYNC_CMD_RUN_COMMAND", "sh")
	t.Setenv("CLAI_MOCK_ASYNC_CMD_RUN_ARGS", `-c "printf start; sleep 0.3; sleep 5"`)
	t.Setenv("CLAI_MOCK_ASYNC_CMD_ID_FILE", asyncCmdFile)
	t.Setenv("CLAI_MOCK_ASYNC_CMD_LOGS_DELAY_MS", "350")

	var gotStatus int
	stdout, stderr := captureStdoutStderr(t, func() {
		gotStatus = run(strings.Split("-r -cm mock_test -t=async_cmd_run,async_cmd_logs,async_cmd_cancel,async_cmd_status q tool_async_cmd_run tool_async_cmd_logs tool_async_cmd_cancel tool_async_cmd_status", " "))
	})
	if gotStatus != 0 {
		t.Fatalf("expected success, got %d stdout=%q stderr=%q", gotStatus, stdout, stderr)
	}
	combined := stdout + stderr
	if !strings.Contains(combined, `"preview":"start"`) {
		t.Fatalf("expected live preview with start, got %q", combined)
	}
	if !strings.Contains(combined, `"status":"cancelled"`) {
		t.Fatalf("expected cancelled status in output, got %q", combined)
	}
}

func Test_e2e_tooling_wildcard_cmd_allows_cmd_and_async_cmd_tools(t *testing.T) {
	oldArgs := os.Args
	t.Cleanup(func() { os.Args = oldArgs })

	_ = setupMainTestConfigDir(t)
	pkgtools.ResetAsyncCmdManagerForTests()

	asyncCmdFile := filepath.Join(t.TempDir(), "async_cmd_id.txt")
	pkgtools.SetAsyncSpawnObserverForTests(func(id string) { _ = os.WriteFile(asyncCmdFile, []byte(id), 0o644) })
	t.Setenv("CLAI_MOCK_CMD_COMMAND", `printf wildcard-cmd`)
	t.Setenv("CLAI_MOCK_ASYNC_CMD_RUN_COMMAND", "sh")
	t.Setenv("CLAI_MOCK_ASYNC_CMD_RUN_ARGS", `-c "sleep 0.2 && printf wildcard-async"`)
	t.Setenv("CLAI_MOCK_ASYNC_CMD_ID_FILE", asyncCmdFile)
	t.Setenv("CLAI_MOCK_ASYNC_CMD_AWAIT_TIMEOUT_SECONDS", "2")

	var gotStatus int
	stdout, stderr := captureStdoutStderr(t, func() {
		gotStatus = run(strings.Split("-r -cm mock_test -t=*cmd* q tool_cmd tool_async_cmd_run tool_async_cmd_await tool_async_cmd_logs", " "))
	})
	if gotStatus != 0 {
		t.Fatalf("expected success, got %d stdout=%q stderr=%q", gotStatus, stdout, stderr)
	}
	combined := stdout + stderr
	if !strings.Contains(combined, "wildcard-cmd") {
		t.Fatalf("expected cmd output in transcript, got %q", combined)
	}
	if !strings.Contains(combined, `"async_cmd_id":"async_cmd_`) {
		t.Fatalf("expected async_cmd_run output in transcript, got %q", combined)
	}
	if !strings.Contains(combined, `"result":"completed"`) {
		t.Fatalf("expected async_cmd_await completion in transcript, got %q", combined)
	}
	if !strings.Contains(combined, `"preview":"wildcard-async"`) {
		t.Fatalf("expected async_cmd_logs preview in transcript, got %q", combined)
	}
}

func Test_e2e_tooling_async_unknown_async_cmd_ids_fail_deterministically(t *testing.T) {
	oldArgs := os.Args
	t.Cleanup(func() { os.Args = oldArgs })

	_ = setupMainTestConfigDir(t)
	pkgtools.ResetAsyncCmdManagerForTests()

	t.Setenv("CLAI_MOCK_ASYNC_CMD_STATUS_ASYNC_CMD_ID", "async_cmd_missing")

	var gotStatus int
	stdout, stderr := captureStdoutStderr(t, func() {
		gotStatus = run(strings.Split("-r -cm mock_test -t=async_cmd_status q tool_async_cmd_status", " "))
	})
	if gotStatus != 0 {
		t.Fatalf("expected query success with tool error surfaced in transcript, got %d stdout=%q stderr=%q", gotStatus, stdout, stderr)
	}
	combined := stdout + stderr
	if !strings.Contains(combined, `ERROR: failed to run tool: async_cmd_status`) || !strings.Contains(combined, `async_cmd not found`) {
		t.Fatalf("expected structured not-found tool error, got %q", combined)
	}
}

func Test_async_session_cleanup_cancels_orphanable_processes(t *testing.T) {
	pkgtools.ResetAsyncCmdManagerForTests()

	ctx, cancel := context.WithCancel(context.Background())
	asyncCmdID, err := pkgtools.SpawnAsyncCmdForTests(ctx, "sh", []string{"-c", "trap 'exit 0' INT TERM; sleep 30"}, "", nil)
	if err != nil {
		t.Fatalf("spawn test async command: %v", err)
	}
	cancel()
	deadline := time.Now().Add(5 * time.Second)
	var snap pkgtools.AsyncCmdSnapshot
	for time.Now().Before(deadline) {
		snap = pkgtools.AsyncCmdSnapshotForTests()[asyncCmdID]
		if snap.Status == "cancelled" || snap.Status == "failed" {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if snap.Status != "cancelled" && snap.Status != "failed" {
		b, _ := json.Marshal(snap)
		t.Fatalf("expected session cleanup terminal cancellation path, got %s", string(b))
	}
}
