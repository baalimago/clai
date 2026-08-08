package text

import (
	"context"
	"fmt"

	"github.com/baalimago/clai/internal/debugflags"
	"github.com/baalimago/clai/internal/models"
	pub_models "github.com/baalimago/clai/pkg/text/models"
	"github.com/baalimago/go_away_boilerplate/pkg/ancli"
)

// stoploss owns both run budgets: the token stoploss (max-tokens + handover
// injection) and the tool-call budget (max-tool-calls). It composes them so
// that once a handover has been requested, further tool calls are treated as
// over-budget and run the existing refusal ladder.
type stoploss struct {
	maxTokens            int    // <= 0 disables the token stoploss
	maxToolCalls         *int   // nil or <= 0 means no tool-call limit
	maxTokensHandoverMsg string // effective handover message, resolved once at construction
}

// newStoploss builds the controller from the querier's configured policies.
// The handover message is resolved once here via Stoploss.HandoverInstructions
// (configured message, else DefaultHandoverInstructions), so the controller
// has exactly one message-resolution site (R9-03).
func (q *Querier[C]) newStoploss() *stoploss {
	ctrl := &stoploss{maxToolCalls: q.maxToolCalls}
	if q.stoploss != nil {
		ctrl.maxTokens = q.stoploss.MaxTokens
		ctrl.maxTokensHandoverMsg = q.stoploss.HandoverInstructions()
	}
	return ctrl
}

// effectiveBudget returns the tool-call budget in effect for the session: 0
// after a handover (every tool call is over budget), the configured positive
// limit, or -1 when unlimited.
func (s *stoploss) effectiveBudget(session *QuerySession) int {
	if session.HandoverRequested {
		return 0
	}
	if s.maxToolCalls != nil && *s.maxToolCalls > 0 {
		return *s.maxToolCalls
	}
	return -1
}

// ladderText builds the escalating refusal for an over-budget tool call at the
// given persistence and reports whether the run must end with io.EOF after the
// refusal tool result is emitted.
func ladderText(persistence int) (string, bool) {
	refusal := "ERROR: No more tool calls allowed. "
	if persistence > 0 {
		refusal += "You will be HARD SHUT DOWN if you persist. "
	}
	if persistence > 1 {
		refusal += "This is your LAST WARNING. "
	}
	return refusal, persistence > 2
}

// toolCallBudgetPlan is the side-effect-free preflight decision for one tool
// call in a batch.
type toolCallBudgetPlan struct {
	call     pub_models.Call
	allowed  bool   // when true the tool implementation may run
	prefix   string // within-budget remaining-count prefix for allowed calls
	out      string // refusal ladder text for refused calls
	hardStop bool   // emit the refusal result, then end the run with io.EOF
}

// PreflightToolCallBudget decides every call in a batch before any tool is
// invoked (R3-02). It is side-effect-free apart from reserving budget slots on
// the session. Refused calls carry the ladder text and are emitted as tool
// results without invoking their implementations (R2-01).
func (s *stoploss) PreflightToolCallBudget(session *QuerySession, calls []pub_models.Call) []toolCallBudgetPlan {
	plans := make([]toolCallBudgetPlan, 0, len(calls))
	for _, call := range calls {
		plans = append(plans, s.preflightToolCall(session, call))
	}
	return plans
}

func (s *stoploss) preflightToolCall(session *QuerySession, call pub_models.Call) toolCallBudgetPlan {
	// load_skill is exempt from the positive max-tool-calls budget, but never
	// from the post-handover refusal: no tool, including load_skill, executes
	// after handover.
	if call.Name == string(pub_models.LoadSkillTool) && !session.HandoverRequested {
		return toolCallBudgetPlan{call: call, allowed: true}
	}
	budget := s.effectiveBudget(session)
	if budget < 0 {
		return toolCallBudgetPlan{call: call, allowed: true}
	}
	used := session.ToolCallsUsed
	debugStoplossf("tool call %q: budget=%d used=%d", call.Name, budget, used)
	if used >= budget {
		refusal, hardStop := ladderText(used - budget)
		plan := toolCallBudgetPlan{call: call, out: refusal, hardStop: hardStop}
		debugStoplossf("tool call %q refused (persistence %d, hardStop %v)", call.Name, used-budget, hardStop)
		if !hardStop {
			session.ToolCallsUsed = used + 1
		}
		return plan
	}
	session.ToolCallsUsed = used + 1
	debugStoplossf("tool call %q allowed, %d remaining", call.Name, budget-used-1)
	return toolCallBudgetPlan{
		call:    call,
		allowed: true,
		prefix:  fmt.Sprintf("[ Tool calls remaining: %v ] ", budget-used),
	}
}

// CheckContextBudget computes the latest request footprint: the usage's
// prompt+completion tokens, total_tokens when both are zero, or the
// InputTokenCounter estimate of the current chat when the usage is
// unavailable (nil or all-zero). On the first crossing of max-tokens it
// appends the handover user message, sets session.HandoverRequested, and
// prints a human-facing notice. Later crossings are no-ops. Returns whether
// the handover message was injected.
func (s *stoploss) CheckContextBudget(ctx context.Context, model models.StreamCompleter, session *QuerySession, usage *pub_models.Usage) (bool, error) {
	if s.maxTokens <= 0 {
		return false, nil
	}
	if session == nil || session.HandoverRequested {
		return false, nil
	}
	footprint := usageFootprint(usage)
	if footprint <= 0 {
		counter, ok := any(model).(models.InputTokenCounter)
		if !ok {
			// Neither the vendor usage nor an estimate is available: skip the
			// check for this step.
			debugStoplossf("no vendor usage and no input token counter; skipping check")
			return false, nil
		}
		counted, err := counter.CountInputTokens(ctx, session.Chat)
		if err != nil {
			return false, err
		}
		footprint = counted
		debugStoplossf("vendor usage unavailable; counted %d input tokens", footprint)
	}
	if footprint <= 0 || footprint < s.maxTokens {
		debugStoplossf("footprint %d below max-tokens %d; no handover", footprint, s.maxTokens)
		return false, nil
	}
	debugStoplossf("footprint %d reached max-tokens %d; injecting handover", footprint, s.maxTokens)
	session.HandoverRequested = true
	session.Chat.Messages = append(session.Chat.Messages, pub_models.Message{
		Role:    "user",
		Content: s.maxTokensHandoverMsg,
	})
	ancli.Warnf("stoploss: context usage ~%d tokens reached max-tokens (%d); injecting handover instructions", footprint, s.maxTokens)
	return true, nil
}

// usageFootprint returns the latest request footprint: prompt+completion
// tokens when either is non-zero, else total_tokens, else 0 (unavailable).
func usageFootprint(usage *pub_models.Usage) int {
	if usage == nil {
		return 0
	}
	if usage.PromptTokens != 0 || usage.CompletionTokens != 0 {
		return usage.PromptTokens + usage.CompletionTokens
	}
	return usage.TotalTokens
}

// debugStoplossf prints stoploss internals when DEBUG_STOPLOSS (or plain
// DEBUG) is truthy. The human-facing handover notice stays an unconditional
// warning; this is the opt-in detail layer for budget decisions.
func debugStoplossf(format string, args ...any) {
	if !debugflags.Enabled("STOPLOSS") {
		return
	}
	ancli.Noticef("[DEBUG_STOPLOSS] "+format+"\n", args...)
}
