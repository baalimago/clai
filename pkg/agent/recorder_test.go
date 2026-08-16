package agent

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/baalimago/clai/pkg/text/models"
	pkgtools "github.com/baalimago/clai/pkg/tools"
)

// recordingUsageRecorder is a thread-safe fake CallUsageRecorder. err, when
// set, is returned from every Record to exercise the error-absorption path.
type recordingUsageRecorder struct {
	mu    sync.Mutex
	calls []models.CompletedModelCall
	err   error
}

func (r *recordingUsageRecorder) Record(_ context.Context, call models.CompletedModelCall) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, call)
	return r.err
}

func (r *recordingUsageRecorder) recorded() []models.CompletedModelCall {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]models.CompletedModelCall(nil), r.calls...)
}

// recordingToolRecorder is a thread-safe fake ToolCallRecorder. err, when
// set, is returned from every RecordToolCall to exercise the
// error-absorption path.
type recordingToolRecorder struct {
	mu    sync.Mutex
	calls []models.ToolCall
	err   error
}

func (r *recordingToolRecorder) RecordToolCall(_ context.Context, call models.ToolCall) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, call)
	return r.err
}

func (r *recordingToolRecorder) recorded() []models.ToolCall {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]models.ToolCall(nil), r.calls...)
}

// The pkg/agent aliases must satisfy the canonical interfaces: the recorder
// surface is usable through a single pkg/agent import (worklog
// 26-08-11-clai-prometheus-metrics).
var (
	_ CallUsageRecorder = (*recordingUsageRecorder)(nil)
	_ ToolCallRecorder  = (*recordingToolRecorder)(nil)
)

// TestAgent_WithUsageRecorder proves the usage-recorder seam end to end: a
// fake recorder registered via WithUsageRecorder receives one
// CompletedModelCall per model step of a real agent run (mock vendor), with
// ordered timestamps, usage, and the EndedWith* flags populated. The mock
// vendor does not implement ModelNamer, so the recorded model name is empty;
// the recorder contract must not depend on it.
func TestAgent_WithUsageRecorder(t *testing.T) {
	t.Setenv("CLAI_DISABLE_COST_ERR_LOG_GOROUTINE", "1")

	rec := &recordingUsageRecorder{}
	a := newCmdBanAgent(t,
		WithModel("mock_test"),
		WithUsageRecorder(rec),
	)

	queryCmdBanAgent(t, &a, "please reply")

	calls := rec.recorded()
	if len(calls) != 1 {
		t.Fatalf("expected exactly 1 recorded model call, got %d", len(calls))
	}
	call := calls[0]
	if call.Usage == nil || call.Usage.TotalTokens == 0 {
		t.Fatalf("expected non-zero usage, got %+v", call.Usage)
	}
	// The mock vendor terminates every step with a stop event, so the
	// recorded step reports EndedWithStop (not EndedWithReply).
	if !call.EndedWithStop {
		t.Fatalf("expected EndedWithStop on the mock's reply step, got %+v", call)
	}
	if call.EndedWithTool || call.EndedWithReply {
		t.Fatalf("expected EndedWithTool/EndedWithReply false on the mock's reply step, got %+v", call)
	}
	if call.StepIndex != 0 {
		t.Fatalf("expected step index 0, got %d", call.StepIndex)
	}
	if call.StartedAt.IsZero() || call.FinishedAt.IsZero() || !call.StartedAt.Before(call.FinishedAt) {
		t.Fatalf("expected ordered timestamps, got started=%v finished=%v", call.StartedAt, call.FinishedAt)
	}
	if call.Model != "" {
		t.Fatalf("expected empty model name for the mock vendor, got %q", call.Model)
	}
}

// TestAgent_WithToolCallRecorder proves the tool-call hook end to end: a
// scripted run that invokes one registered tool records exactly one ToolCall
// with name, ordered timestamps, positive duration, and nil error, while the
// usage recorder observes both model steps (tool-call step + final reply
// step).
func TestAgent_WithToolCallRecorder(t *testing.T) {
	t.Setenv("CLAI_DISABLE_COST_ERR_LOG_GOROUTINE", "1")

	usageRec := &recordingUsageRecorder{}
	toolRec := &recordingToolRecorder{}
	a := newCmdBanAgent(t,
		WithModel("mock_test"),
		WithToolGlobs("ls"),
		WithUsageRecorder(usageRec),
		WithToolCallRecorder(toolRec),
	)

	queryCmdBanAgent(t, &a, "please tool_ls")

	calls := usageRec.recorded()
	if len(calls) != 2 {
		t.Fatalf("expected 2 recorded model calls (tool step + reply step), got %d", len(calls))
	}
	if !calls[0].EndedWithTool {
		t.Fatalf("expected step 0 to end with a tool call, got %+v", calls[0])
	}
	// The mock vendor terminates the final step with a stop event.
	if !calls[1].EndedWithStop {
		t.Fatalf("expected step 1 to end with a stop event, got %+v", calls[1])
	}

	toolCalls := toolRec.recorded()
	if len(toolCalls) != 1 {
		t.Fatalf("expected exactly 1 recorded tool call, got %d", len(toolCalls))
	}
	tc := toolCalls[0]
	if tc.Name != "ls" {
		t.Fatalf("expected tool name ls, got %q", tc.Name)
	}
	if tc.Err != nil {
		t.Fatalf("expected nil error for a successful ls, got %v", tc.Err)
	}
	if tc.StartedAt.IsZero() || tc.FinishedAt.IsZero() || tc.StartedAt.After(tc.FinishedAt) {
		t.Fatalf("expected ordered timestamps, got started=%v finished=%v", tc.StartedAt, tc.FinishedAt)
	}
	if d := tc.FinishedAt.Sub(tc.StartedAt); d <= 0 {
		t.Fatalf("expected positive duration, got %v", d)
	}
}

// TestAgent_WithToolCallRecorder_Error proves the recorder observes tool
// failures: a banned command is refused by the cmd tool, the ToolCall
// carries the derived error, and the run still completes normally.
func TestAgent_WithToolCallRecorder_Error(t *testing.T) {
	t.Setenv("CLAI_DISABLE_COST_ERR_LOG_GOROUTINE", "1")
	t.Cleanup(pkgtools.ResetCmdBanListForTests)

	marker := filepath.Join(t.TempDir(), "banned-marker")
	t.Setenv("CLAI_MOCK_CMD_COMMAND", "touch "+marker)

	toolRec := &recordingToolRecorder{}
	a := newCmdBanAgent(t,
		WithModel("mock_test"),
		WithToolGlobs("cmd"),
		WithCmdBanList("touch"),
		WithToolCallRecorder(toolRec),
	)

	chat := queryCmdBanAgent(t, &a, "please tool_cmd")
	assertCmdBanRefusal(t, chat, "touch")

	toolCalls := toolRec.recorded()
	if len(toolCalls) != 1 {
		t.Fatalf("expected exactly 1 recorded tool call, got %d", len(toolCalls))
	}
	tc := toolCalls[0]
	if tc.Name != "cmd" {
		t.Fatalf("expected tool name cmd, got %q", tc.Name)
	}
	if tc.Err == nil {
		t.Fatal("expected a derived error for the refused command")
	}
	if !strings.Contains(tc.Err.Error(), "banned by policy") {
		t.Fatalf("expected refusal text in the derived error, got %v", tc.Err)
	}
	if d := tc.FinishedAt.Sub(tc.StartedAt); d <= 0 {
		t.Fatalf("expected positive duration, got %v", d)
	}
	assertMarkerAbsent(t, marker)
}

// TestAgent_RecorderErrorAbsorption proves recorder errors never break the
// agent loop: both Record and RecordToolCall return errors, and the run
// still completes (the errors are logged and swallowed).
func TestAgent_RecorderErrorAbsorption(t *testing.T) {
	t.Setenv("CLAI_DISABLE_COST_ERR_LOG_GOROUTINE", "1")

	usageRec := &recordingUsageRecorder{err: errors.New("usage record failed")}
	toolRec := &recordingToolRecorder{err: errors.New("tool record failed")}
	a := newCmdBanAgent(t,
		WithModel("mock_test"),
		WithToolGlobs("ls"),
		WithUsageRecorder(usageRec),
		WithToolCallRecorder(toolRec),
	)

	// A recorder error must not surface from Setup or Query.
	queryCmdBanAgent(t, &a, "please tool_ls")

	if len(usageRec.recorded()) != 2 {
		t.Fatalf("expected 2 recorded model calls, got %d", len(usageRec.recorded()))
	}
	if len(toolRec.recorded()) != 1 {
		t.Fatalf("expected 1 recorded tool call, got %d", len(toolRec.recorded()))
	}
}

// TestAgent_NoRecorder_Noop proves an agent built without recorder options
// behaves exactly as before: the run completes and no recorder state exists.
func TestAgent_NoRecorder_Noop(t *testing.T) {
	t.Setenv("CLAI_DISABLE_COST_ERR_LOG_GOROUTINE", "1")

	a := newCmdBanAgent(t,
		WithModel("mock_test"),
		WithToolGlobs("ls"),
	)

	queryCmdBanAgent(t, &a, "please tool_ls")
}

// TestAgent_RecorderOptions_PropagateToInternalConfig proves both recorder
// options reach text.Configurations via AgentSettings (worklog 2026-08-15-agent-slog-output, D7), and that the
// defaults stay nil.
func TestAgent_RecorderOptions_PropagateToInternalConfig(t *testing.T) {
	usageRec := &recordingUsageRecorder{}
	toolRec := &recordingToolRecorder{}
	a := New(WithUsageRecorder(usageRec), WithToolCallRecorder(toolRec))
	conf := a.asInternalConfig()
	if conf.AgentSettings == nil {
		t.Fatal("expected AgentSettings in internal config")
	}
	if conf.AgentSettings.UsageRecorder != usageRec {
		t.Fatalf("expected AgentSettings.UsageRecorder to propagate, got %v", conf.AgentSettings.UsageRecorder)
	}
	if conf.AgentSettings.ToolCallRecorder != toolRec {
		t.Fatalf("expected AgentSettings.ToolCallRecorder to propagate, got %v", conf.AgentSettings.ToolCallRecorder)
	}

	plain := New()
	if plain.asInternalConfig().AgentSettings.UsageRecorder != nil || plain.asInternalConfig().AgentSettings.ToolCallRecorder != nil {
		t.Fatal("expected nil recorders in the internal config by default")
	}
}
