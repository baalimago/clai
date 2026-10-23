package summary

import (
	"strconv"

	"github.com/baalimago/clai/internal/models"
)

const systemPrompt = "You are a conversation labeller. You receive a transcript and must call " + ToolName +
	" exactly once with a short imperative title and a two-sentence summary stating what was asked and, when visible, the outcome. " +
	"Do not answer the conversation. If the tool rejects a field, correct the named field and call the tool again."

// loopPhaseSystemPrompt is the instruction set of a loop-phase summary. The
// rune bound is the one the tool enforces, so the instruction and the validator
// cannot disagree.
func loopPhaseSystemPrompt() string {
	return "You summarise progress in a goal-driven loop. You receive the goal and the previous iteration's handover. " +
		"Call " + ToolName + " exactly once. title: a short imperative label of what the previous iteration achieved. " +
		"summary: one short line, at most " + strconv.Itoa(models.LoopPhaseSummaryRunes) +
		" runes, naming what the previous iteration achieved and what remains toward the goal. " +
		"Do not restate the goal and do not answer the conversation. If the tool rejects a field, correct the named field and call the tool again."
}

// loopPlanSystemPrompt is the instruction set of the plan summary that names
// what the first loop iteration intends to do.
func loopPlanSystemPrompt() string {
	return "You summarise the plan of a goal-driven loop. You receive the goal. " +
		"Call " + ToolName + " exactly once. title: a short imperative label of the approach. " +
		"summary: one short line, at most " + strconv.Itoa(models.LoopPhaseSummaryRunes) +
		" runes, naming the approach the loop will take toward the goal. " +
		"Do not restate the goal and do not answer it. If the tool rejects a field, correct the named field and call the tool again."
}
