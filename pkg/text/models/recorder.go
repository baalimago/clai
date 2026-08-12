package models

import (
	"context"
	"time"
)

// CompletedModelCall is a record of one finished model step in a session.
// The session runner invokes CallUsageRecorder.Record once per model
// round-trip, before any tool execution the step requested. Embedded
// consumers (e.g. sakfraga) implement CallUsageRecorder to emit per-step
// telemetry.
type CompletedModelCall struct {
	StepIndex      int
	Model          string
	StartedAt      time.Time
	FinishedAt     time.Time
	Usage          *Usage
	EndedWithTool  bool
	EndedWithReply bool
	EndedWithStop  bool
}

// CallUsageRecorder observes completed model calls. A nil recorder keeps
// clai's noop path. A Record error is logged by the session runner and
// never aborts the agent loop: telemetry must never break the run.
type CallUsageRecorder interface {
	Record(context.Context, CompletedModelCall) error
}

// ToolCall is a record of one tool invocation executed by a session.
// Err is nil for a successful invocation; tools.Invoke folds failures
// into its output using the "ERROR: " convention, which the tool executor
// surfaces on this field.
type ToolCall struct {
	Name       string
	StartedAt  time.Time
	FinishedAt time.Time
	Err        error
}

// ToolCallRecorder observes tool invocations. A nil recorder keeps the
// noop path. A RecordToolCall error is logged and never aborts the agent
// loop.
type ToolCallRecorder interface {
	RecordToolCall(context.Context, ToolCall) error
}
