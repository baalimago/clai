package text

import (
	"github.com/baalimago/clai/internal/tools"
	pub_models "github.com/baalimago/clai/pkg/text/models"
)

// tooling groups the per-run tool runtime state the querier carries for tool
// dispatch, budgeting, skill activation, and telemetry. It is one field on
// the querier instead of one field per concern.
type tooling struct {
	// outputRuneLimit limits the amount of runes a tool may return before
	// clai truncates the output. Zero means no limit.
	outputRuneLimit int
	// maxCalls is the pre-handover tool-call budget; nil or <= 0 means no limit.
	maxCalls *int
	// calls counts the tool calls consumed this run, carried across query steps.
	calls int
	// callRecorder receives one ToolCall per tool invocation. Nil keeps the
	// noop path.
	callRecorder ToolCallRecorder
	// skillLoader resolves and activates load_skill requests. Nil keeps the
	// noop path.
	skillLoader SkillLoader
	// base is the tool catalog handed to SkillLoader as the selectable set; a
	// trusted skill replaces it with its active tools.
	base map[string]pub_models.LLMTool
	// offered is what this run has offered its model, and the only table a
	// model's call dispatches through. Every registration on the model's tool
	// box goes through offer, so the two cannot differ.
	offered *tools.Offered
}

func (t *tooling) offer(box tools.ToolBox, tool pub_models.LLMTool) {
	if t.offered == nil {
		t.offered = tools.NewOffered()
	}
	t.offered.Offer(box, tool)
}
