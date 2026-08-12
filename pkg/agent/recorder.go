package agent

import (
	"github.com/baalimago/clai/pkg/text/models"
)

// The recorder types are canonical in pkg/text/models; these aliases let
// embedded consumers (e.g. sakfraga) reference the whole recorder surface
// through a single pkg/agent import (worklog
// 26-08-11-clai-prometheus-metrics).
type (
	CallUsageRecorder  = models.CallUsageRecorder
	CompletedModelCall = models.CompletedModelCall
	ToolCallRecorder   = models.ToolCallRecorder
	ToolCall           = models.ToolCall
)
