package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	pub_models "github.com/baalimago/clai/pkg/text/models"
)

func TestAsyncCmdManager_SpawnAwaitLogsStatusCancelAndNotFound(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test requires POSIX process semantics")
	}
	ResetAsyncCmdManagerForTests()
	ctx := t.Context()

	out, err := AsyncCmdRun.CallWithContext(ctx, pub_models.Input{
		"command": "sh",
		"args":    []any{"-c", "printf ready; sleep 0.1; printf done >&2"},
	})
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}
	var spawned struct {
		CmdID string `json:"async_cmd_id"`
	}
	if err := json.Unmarshal([]byte(out), &spawned); err != nil {
		t.Fatalf("unmarshal spawn: %v", err)
	}
	if spawned.CmdID == "" {
		t.Fatal("expected async cmd id")
	}

	statusJSON, err := AsyncCmdStatus.Call(pub_models.Input{"async_cmd_id": spawned.CmdID})
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if !strings.Contains(statusJSON, `"status":"starting"`) && !strings.Contains(statusJSON, `"status":"running"`) && !strings.Contains(statusJSON, `"status":"succeeded"`) {
		t.Fatalf("unexpected status payload: %s", statusJSON)
	}

	awaitJSON, err := AsyncCmdAwait.CallWithContext(ctx, pub_models.Input{"async_cmd_ids": []any{spawned.CmdID}, "timeout_seconds": 2})
	if err != nil {
		t.Fatalf("await: %v", err)
	}
	if !strings.Contains(awaitJSON, `"result":"completed"`) || !strings.Contains(awaitJSON, `"async_cmds":[`) {
		t.Fatalf("unexpected await payload: %s", awaitJSON)
	}

	logsJSON, err := AsyncCmdLogs.Call(pub_models.Input{"async_cmd_id": spawned.CmdID})
	if err != nil {
		t.Fatalf("logs: %v", err)
	}
	if !strings.Contains(logsJSON, `"preview":"ready"`) || !strings.Contains(logsJSON, `"preview":"done"`) {
		t.Fatalf("unexpected logs payload: %s", logsJSON)
	}

	for _, tool := range []pub_models.LLMTool{AsyncCmdStatus, AsyncCmdLogs, AsyncCmdAwait, AsyncCmdCancel} {
		var input pub_models.Input
		switch tool {
		case AsyncCmdAwait:
			input = pub_models.Input{"async_cmd_ids": []any{"async_cmd_missing"}, "timeout_seconds": 0.1}
		default:
			input = pub_models.Input{"async_cmd_id": "async_cmd_missing"}
		}
		var err error
		if ct, ok := tool.(interface {
			CallWithContext(context.Context, pub_models.Input) (string, error)
		}); ok {
			_, err = ct.CallWithContext(ctx, input)
		} else {
			_, err = tool.Call(input)
		}
		if err == nil || !strings.Contains(err.Error(), "async_cmd not found") {
			t.Fatalf("expected not found error from %s, got %v", tool.Specification().Name, err)
		}
	}

	longOut, err := AsyncCmdRun.CallWithContext(ctx, pub_models.Input{
		"command": "sh",
		"args":    []any{"-c", "trap 'exit 0' INT TERM; printf live; sleep 30"},
	})
	if err != nil {
		t.Fatalf("spawn long: %v", err)
	}
	var longSpawned struct {
		CmdID string `json:"async_cmd_id"`
	}
	if err := json.Unmarshal([]byte(longOut), &longSpawned); err != nil {
		t.Fatalf("unmarshal long spawn: %v", err)
	}
	liveLogs, err := AsyncCmdLogs.Call(pub_models.Input{"async_cmd_id": longSpawned.CmdID})
	if err != nil {
		t.Fatalf("live logs: %v", err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for !strings.Contains(liveLogs, `"preview":"live"`) && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
		liveLogs, err = AsyncCmdLogs.Call(pub_models.Input{"async_cmd_id": longSpawned.CmdID})
		if err != nil {
			t.Fatalf("live logs retry: %v", err)
		}
	}
	if !strings.Contains(liveLogs, `"preview":"live"`) {
		t.Fatalf("expected live preview, got %s", liveLogs)
	}
	cancelJSON, err := AsyncCmdCancel.Call(pub_models.Input{"async_cmd_id": longSpawned.CmdID})
	if err != nil {
		t.Fatalf("cancel: %v", err)
	}
	if !strings.Contains(cancelJSON, `"status":"cancelled"`) && !strings.Contains(cancelJSON, `"status":"running"`) {
		t.Fatalf("unexpected cancel payload: %s", cancelJSON)
	}
	deadline = time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		finalStatus, err := AsyncCmdStatus.Call(pub_models.Input{"async_cmd_id": longSpawned.CmdID})
		if err != nil {
			t.Fatalf("final status: %v", err)
		}
		if strings.Contains(finalStatus, `"status":"cancelled"`) {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatal("expected cancelled terminal status")
}

func TestAsyncCmdRun_EnvAndCWD(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test requires POSIX shell")
	}
	ResetAsyncCmdManagerForTests()

	dir := t.TempDir()
	outFile := filepath.Join(dir, "out.txt")
	_, err := AsyncCmdRun.Call(pub_models.Input{
		"command": "sh",
		"args":    []any{"-c", "printf '%s:%s' \"$PWD\" \"$SPECIAL\" > " + outFile},
		"cwd":     dir,
		"env":     map[string]any{"SPECIAL": "value"},
	})
	if err != nil {
		t.Fatalf("spawn cwd/env: %v", err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(outFile); err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	data, err := os.ReadFile(outFile)
	if err != nil {
		t.Fatalf("read output file: %v", err)
	}
	if got := string(data); got != dir+":value" {
		t.Fatalf("unexpected output: %q", got)
	}
}

func TestAsyncCmdRun_BindsAsyncCmdToSessionContext(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test requires POSIX shell")
	}
	ResetAsyncCmdManagerForTests()

	ctx, cancel := context.WithCancel(context.Background())
	out, err := AsyncCmdRun.CallWithContext(ctx, pub_models.Input{
		"command": "sh",
		"args":    []any{"-c", "trap 'exit 0' INT TERM; sleep 30"},
	})
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}
	var spawned struct {
		CmdID string `json:"async_cmd_id"`
	}
	if err := json.Unmarshal([]byte(out), &spawned); err != nil {
		t.Fatalf("unmarshal spawn: %v", err)
	}
	cancel()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		finalStatus, err := AsyncCmdStatus.Call(pub_models.Input{"async_cmd_id": spawned.CmdID})
		if err != nil {
			t.Fatalf("status: %v", err)
		}
		if strings.Contains(finalStatus, `"status":"cancelled"`) {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatal("expected cancelled terminal status after session cancel")
}

func TestAsyncCmdCancel_PreservesNaturalSuccessRace(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test requires POSIX shell")
	}
	ResetAsyncCmdManagerForTests()

	out, err := AsyncCmdRun.Call(pub_models.Input{
		"command": "sh",
		"args":    []any{"-c", "exit 0"},
	})
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}
	var spawned struct {
		CmdID string `json:"async_cmd_id"`
	}
	if err := json.Unmarshal([]byte(out), &spawned); err != nil {
		t.Fatalf("unmarshal spawn: %v", err)
	}
	time.Sleep(50 * time.Millisecond)
	if _, err := AsyncCmdCancel.Call(pub_models.Input{"async_cmd_id": spawned.CmdID}); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	statusJSON, err := AsyncCmdStatus.Call(pub_models.Input{"async_cmd_id": spawned.CmdID})
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if !strings.Contains(statusJSON, `"status":"succeeded"`) {
		t.Fatalf("expected success to win race, got %s", statusJSON)
	}
}

func TestAsyncCmdRun_FailedStartDoesNotRegisterOrLeakLogs(t *testing.T) {
	ResetAsyncCmdManagerForTests()

	logDir := t.TempDir()
	asyncLogDir = logDir

	_, err := AsyncCmdRun.Call(pub_models.Input{
		"command": filepath.Join(t.TempDir(), "does-not-exist"),
	})
	if err == nil {
		t.Fatal("expected spawn failure")
	}
	if got := AsyncCmdSnapshotForTests(); len(got) != 0 {
		t.Fatalf("expected no registered cmds after failed start, got %+v", got)
	}

	leaked, _ := filepath.Glob(filepath.Join(logDir, "clai-async-cmd-*"))
	if len(leaked) != 0 {
		t.Fatalf("expected no leaked log files in isolated dir, got %d: %v", len(leaked), leaked)
	}
}
