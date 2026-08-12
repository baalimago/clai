package text

import (
	"context"

	pub_models "github.com/baalimago/clai/pkg/text/models"
)

// The recorder types are canonical in pkg/text/models (worklog
// 26-08-11-clai-prometheus-metrics). The aliases keep internal references
// unchanged while exposing the seam to embedded consumers through the
// public package.
type (
	CallUsageRecorder  = pub_models.CallUsageRecorder
	CompletedModelCall = pub_models.CompletedModelCall
	ToolCallRecorder   = pub_models.ToolCallRecorder
	ToolCall           = pub_models.ToolCall
)

// noopCallUsageRecorder keeps today's behavior when no recorder is configured.
type noopCallUsageRecorder struct{}

func (noopCallUsageRecorder) Record(context.Context, CompletedModelCall) error {
	return nil
}
