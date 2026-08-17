package text

import pub_models "github.com/baalimago/clai/pkg/text/models"

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
	// run is the per-run tool dispatch table. It is populated once at
	// NewQuerier and never mutated; dispatch resolves here instead of the
	// process-global registry so concurrent agents cannot overwrite each
	// other's tool instances.
	run map[string]pub_models.LLMTool
	// registered records which tool names have been registered on the model
	// tool box so a skill enabling an already-registered tool does not
	// double-register it.
	registered map[string]struct{}
}
