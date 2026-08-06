package text

import (
	"strings"
	"testing"

	pub_models "github.com/baalimago/clai/pkg/text/models"
)

// Test_applyToolCallBudget pins the -max-tool-calls contract enforced by the
// stoploss controller's preflight: within budget the result is prefixed with
// the remaining count, over budget the result is replaced with an escalating
// warning, and persisting past the final warning hard-stops the run. A nil or
// 0 limit passes calls through untouched (0 = unlimited, D5). The escalation
// ladder lives only in PreflightToolCallBudget (R9-02); each row drives it
// with a single-call slice, and the io.EOF propagation from a hardStop plan
// is pinned by the runner suite.
func Test_applyToolCallBudget(t *testing.T) {
	t.Run("no budget passes output through untouched", func(t *testing.T) {
		q := &Querier[*MockQuerier]{}
		ctrl := q.newStoploss()
		session := &QuerySession{}

		plan := ctrl.PreflightToolCallBudget(session, []pub_models.Call{{ID: "a"}})[0]
		if !plan.allowed || plan.prefix != "" || plan.out != "" || plan.hardStop {
			t.Fatalf("expected an unlimited call without prefix, got %+v", plan)
		}
		if session.ToolCallsUsed != 0 {
			t.Fatalf("expected no increment without budget, got %d", session.ToolCallsUsed)
		}
	})

	t.Run("within budget prefixes remaining count and increments", func(t *testing.T) {
		maxCalls := 3
		q := &Querier[*MockQuerier]{maxToolCalls: &maxCalls}
		ctrl := q.newStoploss()
		session := &QuerySession{}

		plan := ctrl.PreflightToolCallBudget(session, []pub_models.Call{{ID: "a"}})[0]
		if !plan.allowed || !strings.Contains(plan.prefix, "Tool calls remaining: 3") {
			t.Fatalf("expected remaining-count prefix, got %+v", plan)
		}
		if session.ToolCallsUsed != 1 {
			t.Fatalf("expected ToolCallsUsed 1, got %d", session.ToolCallsUsed)
		}
	})

	t.Run("zero budget means unlimited", func(t *testing.T) {
		zero := 0
		q := &Querier[*MockQuerier]{maxToolCalls: &zero}
		ctrl := q.newStoploss()
		session := &QuerySession{}

		plan := ctrl.PreflightToolCallBudget(session, []pub_models.Call{{ID: "a"}})[0]
		if !plan.allowed || plan.prefix != "" {
			t.Fatalf("expected an unlimited call with 0 limit, got %+v", plan)
		}
		if session.ToolCallsUsed != 0 {
			t.Fatalf("expected no increment with 0 limit, got %d", session.ToolCallsUsed)
		}
	})

	t.Run("over budget escalates and hard stops past the final warning", func(t *testing.T) {
		maxCalls := 1
		q := &Querier[*MockQuerier]{maxToolCalls: &maxCalls}
		ctrl := q.newStoploss()
		session := &QuerySession{ToolCallsUsed: 1}

		first := ctrl.PreflightToolCallBudget(session, []pub_models.Call{{ID: "a"}})[0]
		if first.allowed || !strings.Contains(first.out, "No more tool calls allowed") || first.hardStop {
			t.Fatalf("expected a plain warning without hard stop, got %+v", first)
		}

		var last toolCallBudgetPlan
		for range 4 {
			last = ctrl.PreflightToolCallBudget(session, []pub_models.Call{{ID: "a"}})[0]
		}
		if !last.hardStop || !strings.Contains(last.out, "LAST WARNING") {
			t.Fatalf("expected the final warning to hard stop, got %+v", last)
		}
	})
}
