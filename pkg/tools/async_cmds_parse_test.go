package tools

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	pub_models "github.com/baalimago/clai/pkg/text/models"
)

func extractCmdID(t *testing.T, spawnJSON string) string {
	t.Helper()
	var spawned struct {
		CmdID string `json:"async_cmd_id"`
	}
	if err := json.Unmarshal([]byte(spawnJSON), &spawned); err != nil {
		t.Fatalf("unmarshal spawn: %v", err)
	}
	if spawned.CmdID == "" {
		t.Fatal("spawn returned an empty async_cmd_id")
	}
	return spawned.CmdID
}

func TestAsyncCmdRunRejectsBadInput(t *testing.T) {
	tests := []struct {
		name  string
		input pub_models.Input
		want  string
	}{
		{"missing command", pub_models.Input{}, "command must be a non-empty string"},
		{"non-string command", pub_models.Input{"command": 7}, "command must be a non-empty string"},
		{"bad args", pub_models.Input{"command": "sh", "args": "not-an-array"}, "args:"},
		{"non-string arg", pub_models.Input{"command": "sh", "args": []any{"ok", 2}}, "only strings"},
		{"bad cwd", pub_models.Input{"command": "sh", "cwd": 7}, "cwd must be a string"},
		{"bad env", pub_models.Input{"command": "sh", "env": "not-a-map"}, "env:"},
		{"non-string env value", pub_models.Input{"command": "sh", "env": map[string]any{"K": 7}}, "only string values"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := AsyncCmdRun.CallWithContext(t.Context(), tc.input)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want substring %q", err, tc.want)
			}
		})
	}
}

func TestAsyncCmdToolsRejectMissingID(t *testing.T) {
	for _, tool := range []pub_models.LLMTool{AsyncCmdStatus, AsyncCmdLogs, AsyncCmdCancel} {
		if _, err := tool.Call(pub_models.Input{}); err == nil || !strings.Contains(err.Error(), "async_cmd_id") {
			t.Fatalf("%s: err = %v, want async_cmd_id error", tool.Specification().Name, err)
		}
	}
}

func TestAsyncCmdAwaitRejectsBadInput(t *testing.T) {
	tests := []struct {
		name  string
		input pub_models.Input
		want  string
	}{
		{"missing ids", pub_models.Input{"timeout_seconds": 1}, "async_cmd_ids is required"},
		{"empty ids", pub_models.Input{"async_cmd_ids": []any{}, "timeout_seconds": 1}, "async_cmd_ids must not be empty"},
		{"bad ids type", pub_models.Input{"async_cmd_ids": "nope", "timeout_seconds": 1}, "async_cmd_ids:"},
		{"bad timeout", pub_models.Input{"async_cmd_ids": []any{"x"}, "timeout_seconds": "soon"}, "timeout_seconds must be numeric"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := AsyncCmdAwait.Call(tc.input); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want substring %q", err, tc.want)
			}
		})
	}
}

func TestAsyncCmdAwaitTimesOut(t *testing.T) {
	ResetAsyncCmdManagerForTests()
	ctx := t.Context()

	out, err := AsyncCmdRun.CallWithContext(ctx, pub_models.Input{
		"command": os.Args[0],
		"args":    []any{"-test.run=TestAsyncCmdAwaitChildProcess"},
		"env":     map[string]any{"CLAI_ASYNC_CMD_TEST_CHILD": "1"},
	})
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}
	cmdID := extractCmdID(t, out)
	t.Cleanup(func() { _, _ = AsyncCmdCancel.Call(pub_models.Input{"async_cmd_id": cmdID}) })

	awaitJSON, err := AsyncCmdAwait.Call(pub_models.Input{"async_cmd_ids": []any{cmdID}, "timeout_seconds": 0.02})
	if err != nil {
		t.Fatalf("await: %v", err)
	}
	if !strings.Contains(awaitJSON, `"result":"timed_out"`) {
		t.Fatalf("expected timed_out, got %s", awaitJSON)
	}
}

func TestAsyncCmdAwaitChildProcess(t *testing.T) {
	if os.Getenv("CLAI_ASYNC_CMD_TEST_CHILD") == "1" {
		select {}
	}
}

func TestParseStringSliceAndMap(t *testing.T) {
	if got, err := parseStringSlice([]string{"a", "b"}); err != nil || len(got) != 2 {
		t.Fatalf("[]string: %v %v", got, err)
	}
	if got, err := parseStringMap(map[string]string{"K": "V"}); err != nil || got["K"] != "V" {
		t.Fatalf("map[string]string: %v %v", got, err)
	}
	if got, err := parseStringMap(map[string]any{"K": "V"}); err != nil || got["K"] != "V" {
		t.Fatalf("map[string]any: %v %v", got, err)
	}
}

func TestParseTimeoutSecondsKinds(t *testing.T) {
	tests := []struct {
		raw  any
		want float64
	}{
		{int(2), 2},
		{int32(2), 2},
		{int64(2), 2},
		{float32(2.5), 2.5},
		{float64(2.5), 2.5},
	}
	for _, tc := range tests {
		got, err := parseTimeoutSeconds(tc.raw)
		if err != nil {
			t.Fatalf("parseTimeoutSeconds(%T): %v", tc.raw, err)
		}
		if got != tc.want {
			t.Fatalf("parseTimeoutSeconds(%T) = %v, want %v", tc.raw, got, tc.want)
		}
	}
}

func TestPreviewBufferTruncates(t *testing.T) {
	b := &previewBuffer{}
	if _, err := b.Write(make([]byte, asyncLogPreviewBytes+10)); err != nil {
		t.Fatalf("Write: %v", err)
	}
	preview, truncated := b.Snapshot()
	if !truncated || len(preview) != asyncLogPreviewBytes {
		t.Fatalf("truncated=%v len=%d", truncated, len(preview))
	}

	b2 := &previewBuffer{}
	if _, err := b2.Write(make([]byte, asyncLogPreviewBytes)); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if _, err := b2.Write([]byte("overflow")); err != nil {
		t.Fatalf("Write overflow: %v", err)
	}
	if _, truncated := b2.Snapshot(); !truncated {
		t.Fatal("expected truncation after the cap is reached")
	}
}

func TestSpawnRejectsEmptyCommand(t *testing.T) {
	if _, err := asyncCmdManager.Spawn(context.Background(), "test", asyncCmdRunSpec{}); err == nil {
		t.Fatal("expected error for empty command")
	}
}

func TestExitCodeFromErrFallsBackToMinusOne(t *testing.T) {
	if got := exitCodeFromErr(errors.New("not an exit error")); got != -1 {
		t.Fatalf("exitCodeFromErr = %d, want -1", got)
	}
}

func TestMustJSONStringSurfacesMarshalError(t *testing.T) {
	if _, err := mustJSONString(make(chan int)); err == nil {
		t.Fatal("expected marshal error for an unmarshalable value")
	}
}
