package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	pub_models "github.com/baalimago/clai/pkg/text/models"
)

func TestFreetextCmdTool_Call_PreservesQuotedArguments(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test requires a POSIX shell")
	}

	out, err := FreetextCmd.Call(pub_models.Input{"command": `printf '%s' "hello world"`})
	if err != nil {
		t.Fatalf("freetext command failed: %v", err)
	}

	if got, want := out, "hello world"; got != want {
		t.Fatalf("unexpected output: got %q want %q", got, want)
	}
}

func TestFreetextCmdTool_Call_BadType(t *testing.T) {
	_, err := FreetextCmd.Call(pub_models.Input{"command": 123})
	if err == nil {
		t.Fatal("expected error for bad command type")
	}
	if !strings.Contains(err.Error(), "command") {
		t.Fatalf("expected contextual error mentioning command, got %v", err)
	}
}

func TestCmdTool_Call_PreservesQuotedArguments(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test requires a POSIX shell")
	}

	out, err := Cmd.Call(pub_models.Input{"command": `printf '%s' "hello world"`})
	if err != nil {
		t.Fatalf("cmd failed: %v", err)
	}

	if got, want := out, "hello world"; got != want {
		t.Fatalf("unexpected output: got %q want %q", got, want)
	}
}

func TestCmdTool_Specification_HasCmdName(t *testing.T) {
	if got, want := Cmd.Specification().Name, "cmd"; got != want {
		t.Fatalf("unexpected cmd tool name: got %q want %q", got, want)
	}
}

func TestFreetextCmdTool_Specification_HasLegacyName(t *testing.T) {
	if got, want := FreetextCmd.Specification().Name, "freetext_command"; got != want {
		t.Fatalf("unexpected freetext command tool name: got %q want %q", got, want)
	}
}

func TestCmdTool_Specification_HasTimeoutInput(t *testing.T) {
	prop, ok := Cmd.Specification().Inputs.Properties["timeout_seconds"]
	if !ok {
		t.Fatal("expected timeout_seconds input")
	}
	if got, want := prop.Type, "number"; got != want {
		t.Fatalf("unexpected timeout_seconds type: got %q want %q", got, want)
	}
}

func TestFreetextCmdTool_Call_TimesOutClearlyAndSuggestsAsync(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test requires a POSIX shell")
	}

	oldDefault, oldKill := defaultCmdTimeout, defaultCmdKillAfter
	defaultCmdTimeout = 100 * time.Millisecond
	defaultCmdKillAfter = 250 * time.Millisecond
	t.Cleanup(func() {
		defaultCmdTimeout = oldDefault
		defaultCmdKillAfter = oldKill
	})

	dir := t.TempDir()
	marker := filepath.Join(dir, "got-term")
	command := fmt.Sprintf(`trap '' INT; trap 'echo trapped > %s' TERM; while true; do sleep 1; done`, shellQuote(marker))

	_, err := FreetextCmd.Call(pub_models.Input{"command": command})
	if err == nil {
		t.Fatal("expected timeout error")
	}
	errStr := strings.ToLower(err.Error())
	for _, want := range []string{"timed out", "interrupt", "async_cmd_run"} {
		if !strings.Contains(errStr, want) {
			t.Fatalf("expected timeout error to mention %q, got %q", want, err)
		}
	}
	if _, statErr := os.Stat(marker); !os.IsNotExist(statErr) {
		t.Fatalf("expected hard kill path, stat err=%v", statErr)
	}
}

func TestFreetextCmdTool_Call_TimeoutErrorReflectsCustomTimeoutAndKillWindow(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test requires a POSIX shell")
	}

	oldDefault, oldKill := defaultCmdTimeout, defaultCmdKillAfter
	defaultCmdTimeout = 100 * time.Millisecond
	defaultCmdKillAfter = 250 * time.Millisecond
	t.Cleanup(func() {
		defaultCmdTimeout = oldDefault
		defaultCmdKillAfter = oldKill
	})

	_, err := FreetextCmd.Call(pub_models.Input{
		"command":         `trap '' INT; while true; do sleep 1; done`,
		"timeout_seconds": 0.2,
	})
	if err == nil {
		t.Fatal("expected timeout error")
	}
	errStr := err.Error()
	for _, want := range []string{"0.2", "0.25", "async_cmd_run"} {
		if !strings.Contains(errStr, want) {
			t.Fatalf("expected timeout error to mention %q, got %q", want, errStr)
		}
	}
}

func TestFreetextCmdTool_Call_TimeoutErrorReportsInterruptExit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test requires a POSIX shell")
	}

	_, err := FreetextCmd.Call(pub_models.Input{
		"command":         `trap "exit 0" INT; while true; do sleep 1; done`,
		"timeout_seconds": 0.1,
	})
	if err == nil {
		t.Fatal("expected timeout error")
	}
	errStr := strings.ToLower(err.Error())
	if !strings.Contains(errStr, "stopped after interrupt") {
		t.Fatalf("expected interrupt shutdown detail, got %q", errStr)
	}
}

func TestFreetextCmdTool_Call_TimeoutErrorReportsHardKill(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test requires a POSIX shell")
	}

	oldKill := defaultCmdKillAfter
	defaultCmdKillAfter = 250 * time.Millisecond
	t.Cleanup(func() { defaultCmdKillAfter = oldKill })

	_, err := FreetextCmd.Call(pub_models.Input{
		"command":         `trap "" INT; while true; do sleep 1; done`,
		"timeout_seconds": 0.1,
	})
	if err == nil {
		t.Fatal("expected timeout error")
	}
	errStr := strings.ToLower(err.Error())
	if !strings.Contains(errStr, "required a hard-kill") {
		t.Fatalf("expected hard kill detail, got %q", errStr)
	}
}

func TestFreetextCmdTool_CallWithContext_CancelsOnContextDone(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := FreetextCmd.CallWithContext(ctx, pub_models.Input{"command": "sleep 10"})
	if err == nil {
		t.Fatal("expected error for cancelled context, got nil")
	}
	if !strings.Contains(err.Error(), "cancelled by session") {
		t.Fatalf("expected 'cancelled by session', got: %v", err)
	}
}

func TestFreetextCmdTool_CallWithContext_TimesOutProperly(t *testing.T) {
	ctx := context.Background()
	_, err := FreetextCmd.CallWithContext(ctx, pub_models.Input{"command": "sleep 10", "timeout_seconds": float64(0.1)})
	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
	if !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("expected 'timed out', got: %v", err)
	}
}

func TestFreetextCmdTool_CallWithContext_ReturnsOutputOnSuccess(t *testing.T) {
	ctx := context.Background()
	out, err := FreetextCmd.CallWithContext(ctx, pub_models.Input{"command": "echo hello", "timeout_seconds": float64(10)})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "hello") {
		t.Fatalf("expected 'hello' in output, got: %q", out)
	}
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}
