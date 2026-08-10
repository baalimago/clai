package text

import (
	"context"
	"errors"
	"strings"
	"testing"

	pub_models "github.com/baalimago/clai/pkg/text/models"
	"github.com/baalimago/go_away_boilerplate/pkg/testboil"
)

// countingTokenModel is a MockQuerier that also implements
// models.InputTokenCounter so CheckContextBudget can exercise the estimate
// fallback (R3-01).
type countingTokenModel struct {
	*MockQuerier
	countFn func(context.Context, pub_models.Chat) (int, error)
}

func (m *countingTokenModel) CountInputTokens(ctx context.Context, chat pub_models.Chat) (int, error) {
	return m.countFn(ctx, chat)
}

func Test_stoploss_CheckContextBudget(t *testing.T) {
	noopModel := &MockQuerier{}

	t.Run("disabled when maxTokens <= 0", func(t *testing.T) {
		ctrl := &stoploss{maxTokens: 0}
		session := &QuerySession{Chat: pub_models.Chat{}}

		just, err := ctrl.CheckContextBudget(context.Background(), noopModel, session, &pub_models.Usage{PromptTokens: 1000})
		if err != nil {
			t.Fatalf("CheckContextBudget: %v", err)
		}
		if just {
			t.Fatal("expected no injection for a disabled stoploss")
		}
		if session.HandoverRequested || len(session.Chat.Messages) != 0 {
			t.Fatal("expected no handover state for a disabled stoploss")
		}
	})

	t.Run("non-crossing usage is a no-op", func(t *testing.T) {
		ctrl := &stoploss{maxTokens: 100, maxTokensHandoverMsg: "wrap up"}
		session := &QuerySession{Chat: pub_models.Chat{}}

		just, err := ctrl.CheckContextBudget(context.Background(), noopModel, session, &pub_models.Usage{PromptTokens: 40, CompletionTokens: 20})
		if err != nil {
			t.Fatalf("CheckContextBudget: %v", err)
		}
		if just || session.HandoverRequested || len(session.Chat.Messages) != 0 {
			t.Fatal("expected no injection below max-tokens")
		}
	})

	t.Run("crossing injects once and prints one notice", func(t *testing.T) {
		ctrl := &stoploss{maxTokens: 100, maxTokensHandoverMsg: "wrap up"}
		session := &QuerySession{Chat: pub_models.Chat{}}
		usage := &pub_models.Usage{PromptTokens: 60, CompletionTokens: 60}

		var firstJust, secondJust bool
		out := testboil.CaptureStdout(t, func(t *testing.T) {
			var err error
			firstJust, err = ctrl.CheckContextBudget(context.Background(), noopModel, session, usage)
			if err != nil {
				t.Fatalf("first CheckContextBudget: %v", err)
			}
			secondJust, err = ctrl.CheckContextBudget(context.Background(), noopModel, session, usage)
			if err != nil {
				t.Fatalf("second CheckContextBudget: %v", err)
			}
		})
		if !firstJust || secondJust {
			t.Fatalf("expected injection on first crossing only, got %t then %t", firstJust, secondJust)
		}
		if !session.HandoverRequested {
			t.Fatal("expected HandoverRequested after crossing")
		}
		if len(session.Chat.Messages) != 1 {
			t.Fatalf("expected exactly one handover message, got %d", len(session.Chat.Messages))
		}
		m := session.Chat.Messages[0]
		if m.Role != "user" || m.Content != "wrap up" {
			t.Fatalf("expected configured handover user message, got %+v", m)
		}
		if strings.Count(out, "injecting handover") != 1 {
			t.Fatalf("expected exactly one stoploss notice, got output: %q", out)
		}
	})

	t.Run("total-tokens fallback when prompt and completion are zero", func(t *testing.T) {
		q := &Querier[*MockQuerier]{stoploss: &Stoploss{MaxTokens: 100}}
		ctrl := q.newStoploss()
		session := &QuerySession{Chat: pub_models.Chat{}}

		just, err := ctrl.CheckContextBudget(context.Background(), noopModel, session, &pub_models.Usage{TotalTokens: 120})
		if err != nil {
			t.Fatalf("CheckContextBudget: %v", err)
		}
		if !just {
			t.Fatal("expected crossing via total_tokens")
		}
		if got := session.Chat.Messages[0].Content; got != DefaultHandoverInstructions {
			t.Fatalf("expected default handover message, got %q", got)
		}
	})

	t.Run("uses prompt plus completion when only one is non-zero", func(t *testing.T) {
		q := &Querier[*MockQuerier]{stoploss: &Stoploss{MaxTokens: 100}}
		ctrl := q.newStoploss()
		session := &QuerySession{Chat: pub_models.Chat{}}

		just, err := ctrl.CheckContextBudget(context.Background(), noopModel, session, &pub_models.Usage{CompletionTokens: 120})
		if err != nil {
			t.Fatalf("CheckContextBudget: %v", err)
		}
		if !just || len(session.Chat.Messages) != 1 {
			t.Fatalf("expected crossing via the non-zero completion side, got just=%t messages=%d", just, len(session.Chat.Messages))
		}
	})

	t.Run("nil usage falls back to InputTokenCounter", func(t *testing.T) {
		model := &countingTokenModel{
			MockQuerier: &MockQuerier{},
			countFn:     func(context.Context, pub_models.Chat) (int, error) { return 150, nil },
		}
		q := &Querier[*MockQuerier]{stoploss: &Stoploss{MaxTokens: 100}}
		ctrl := q.newStoploss()
		session := &QuerySession{Chat: pub_models.Chat{}}

		just, err := ctrl.CheckContextBudget(context.Background(), model, session, nil)
		if err != nil {
			t.Fatalf("CheckContextBudget: %v", err)
		}
		if !just || len(session.Chat.Messages) != 1 {
			t.Fatalf("expected crossing via the estimate, got just=%t messages=%d", just, len(session.Chat.Messages))
		}
	})

	t.Run("all-zero usage falls back to InputTokenCounter", func(t *testing.T) {
		model := &countingTokenModel{
			MockQuerier: &MockQuerier{},
			countFn:     func(context.Context, pub_models.Chat) (int, error) { return 150, nil },
		}
		q := &Querier[*MockQuerier]{stoploss: &Stoploss{MaxTokens: 100}}
		ctrl := q.newStoploss()
		session := &QuerySession{Chat: pub_models.Chat{}}

		just, err := ctrl.CheckContextBudget(context.Background(), model, session, &pub_models.Usage{})
		if err != nil {
			t.Fatalf("CheckContextBudget: %v", err)
		}
		if !just || len(session.Chat.Messages) != 1 {
			t.Fatalf("expected crossing via the estimate, got just=%t messages=%d", just, len(session.Chat.Messages))
		}
	})

	t.Run("skips when neither usage nor counter is available", func(t *testing.T) {
		q := &Querier[*MockQuerier]{stoploss: &Stoploss{MaxTokens: 100}}
		ctrl := q.newStoploss()
		session := &QuerySession{Chat: pub_models.Chat{}}

		just, err := ctrl.CheckContextBudget(context.Background(), noopModel, session, nil)
		if err != nil {
			t.Fatalf("CheckContextBudget: %v", err)
		}
		if just || session.HandoverRequested || len(session.Chat.Messages) != 0 {
			t.Fatal("expected the check to be skipped without panic")
		}
	})

	t.Run("propagates CountInputTokens errors", func(t *testing.T) {
		model := &countingTokenModel{
			MockQuerier: &MockQuerier{},
			countFn:     func(context.Context, pub_models.Chat) (int, error) { return 0, errors.New("count failed") },
		}
		q := &Querier[*MockQuerier]{stoploss: &Stoploss{MaxTokens: 100}}
		ctrl := q.newStoploss()
		session := &QuerySession{Chat: pub_models.Chat{}}

		_, err := ctrl.CheckContextBudget(context.Background(), model, session, nil)
		if err == nil || !strings.Contains(err.Error(), "count failed") {
			t.Fatalf("expected propagated counter error, got %v", err)
		}
	})

	t.Run("no-op once handover already requested", func(t *testing.T) {
		ctrl := &stoploss{maxTokens: 100, maxTokensHandoverMsg: "wrap up"}
		session := &QuerySession{HandoverRequested: true, Chat: pub_models.Chat{}}

		just, err := ctrl.CheckContextBudget(context.Background(), noopModel, session, &pub_models.Usage{PromptTokens: 500})
		if err != nil {
			t.Fatalf("CheckContextBudget: %v", err)
		}
		if just || len(session.Chat.Messages) != 0 {
			t.Fatal("expected no re-injection after handover")
		}
	})
}

func Test_stoploss_PreflightToolCallBudget(t *testing.T) {
	t.Run("nil max-tool-calls means unlimited", func(t *testing.T) {
		ctrl := &stoploss{}
		session := &QuerySession{}

		plans := ctrl.PreflightToolCallBudget(session, []pub_models.Call{{ID: "a"}, {ID: "b"}})
		for _, p := range plans {
			if !p.allowed || p.prefix != "" || p.hardStop || p.out != "" {
				t.Fatalf("expected unlimited call allowed without prefix, got %+v", p)
			}
		}
		if session.ToolCallsUsed != 0 {
			t.Fatalf("expected no budget slots reserved, got %d", session.ToolCallsUsed)
		}
	})

	t.Run("zero max-tool-calls means unlimited", func(t *testing.T) {
		zero := 0
		ctrl := &stoploss{maxToolCalls: &zero}
		session := &QuerySession{}

		plans := ctrl.PreflightToolCallBudget(session, []pub_models.Call{{ID: "a"}})
		if !plans[0].allowed || plans[0].prefix != "" {
			t.Fatalf("expected 0 limit to behave as unlimited, got %+v", plans[0])
		}
		if session.ToolCallsUsed != 0 {
			t.Fatalf("expected no increment for unlimited budget, got %d", session.ToolCallsUsed)
		}
	})

	t.Run("within-budget batch reserves slots in order", func(t *testing.T) {
		maxCalls := 2
		ctrl := &stoploss{maxToolCalls: &maxCalls}
		session := &QuerySession{}

		plans := ctrl.PreflightToolCallBudget(session, []pub_models.Call{{ID: "a"}, {ID: "b"}})
		if !plans[0].allowed || plans[0].prefix != "[ Tool calls remaining: 2 ] " {
			t.Fatalf("expected first call prefixed with 2 remaining, got %+v", plans[0])
		}
		if !plans[1].allowed || plans[1].prefix != "[ Tool calls remaining: 1 ] " {
			t.Fatalf("expected second call prefixed with 1 remaining, got %+v", plans[1])
		}
		if session.ToolCallsUsed != 2 {
			t.Fatalf("expected both slots reserved, got %d", session.ToolCallsUsed)
		}
	})

	t.Run("over-budget calls are refused with the escalation ladder", func(t *testing.T) {
		maxCalls := 1
		ctrl := &stoploss{maxToolCalls: &maxCalls}
		session := &QuerySession{}

		plans := ctrl.PreflightToolCallBudget(session, []pub_models.Call{{ID: "a"}, {ID: "b"}, {ID: "c"}, {ID: "d"}, {ID: "e"}})
		if !plans[0].allowed {
			t.Fatalf("expected first call within budget, got %+v", plans[0])
		}
		if plans[1].allowed || plans[1].out != "ERROR: No more tool calls allowed. " {
			t.Fatalf("expected plain refusal for the first over-budget call, got %+v", plans[1])
		}
		if plans[2].allowed || !strings.Contains(plans[2].out, "HARD SHUT DOWN") {
			t.Fatalf("expected hard-shutdown warning, got %+v", plans[2])
		}
		if plans[3].allowed || !strings.Contains(plans[3].out, "LAST WARNING") || plans[3].hardStop {
			t.Fatalf("expected last warning without hard stop, got %+v", plans[3])
		}
		if plans[4].allowed || !strings.Contains(plans[4].out, "LAST WARNING") || !plans[4].hardStop {
			t.Fatalf("expected last-warning hard stop, got %+v", plans[4])
		}
		if session.ToolCallsUsed != 4 {
			t.Fatalf("expected no slot reserved for the io.EOF refusal, got %d", session.ToolCallsUsed)
		}
	})

	t.Run("default (no post-handover budget) allows every call", func(t *testing.T) {
		ctrl := &stoploss{}
		session := &QuerySession{HandoverRequested: true}

		plans := ctrl.PreflightToolCallBudget(session, []pub_models.Call{
			{ID: "a", Name: "cmd"},
			{ID: "b", Name: string(pub_models.LoadSkillTool)},
		})
		for _, p := range plans {
			if !p.allowed || p.prefix != "" || p.hardStop || p.out != "" {
				t.Fatalf("expected unlimited post-handover call allowed, got %+v", p)
			}
		}
		if session.PostHandoverToolCallsUsed != 0 {
			t.Fatalf("expected no slots reserved post-handover, got %d", session.PostHandoverToolCallsUsed)
		}
	})

	t.Run("post-handover budget is a fresh allowance", func(t *testing.T) {
		maxCalls := 1
		ctrl := &stoploss{maxToolCalls: &maxCalls, maxToolCallsAfterHandover: 2}
		// The pre-handover budget is exhausted; the wrap-up allowance must not
		// be eaten by pre-handover consumption.
		session := &QuerySession{ToolCallsUsed: 1, HandoverRequested: true}

		plans := ctrl.PreflightToolCallBudget(session, []pub_models.Call{{ID: "a"}, {ID: "b"}})
		if !plans[0].allowed || plans[0].prefix != "[ Tool calls remaining: 2 ] " {
			t.Fatalf("expected first post-handover call allowed with a fresh budget, got %+v", plans[0])
		}
		if !plans[1].allowed || plans[1].prefix != "[ Tool calls remaining: 1 ] " {
			t.Fatalf("expected second post-handover call allowed, got %+v", plans[1])
		}
		if session.PostHandoverToolCallsUsed != 2 {
			t.Fatalf("expected post-handover slots on the fresh counter, got %d", session.PostHandoverToolCallsUsed)
		}
		if session.ToolCallsUsed != 1 {
			t.Fatalf("expected the pre-handover counter untouched, got %d", session.ToolCallsUsed)
		}
	})

	t.Run("post-handover budget escalates the ladder when exhausted", func(t *testing.T) {
		ctrl := &stoploss{maxToolCallsAfterHandover: 1}
		session := &QuerySession{HandoverRequested: true}

		plans := ctrl.PreflightToolCallBudget(session, []pub_models.Call{{ID: "a"}, {ID: "b"}, {ID: "c"}, {ID: "d"}, {ID: "e"}})
		if !plans[0].allowed {
			t.Fatalf("expected the first post-handover call within budget, got %+v", plans[0])
		}
		if plans[1].allowed || plans[1].out != "ERROR: No more tool calls allowed. " {
			t.Fatalf("expected plain refusal for the first over-budget call, got %+v", plans[1])
		}
		if plans[2].allowed || !strings.Contains(plans[2].out, "HARD SHUT DOWN") {
			t.Fatalf("expected hard-shutdown warning, got %+v", plans[2])
		}
		if plans[3].allowed || !strings.Contains(plans[3].out, "LAST WARNING") || plans[3].hardStop {
			t.Fatalf("expected last warning without hard stop, got %+v", plans[3])
		}
		if plans[4].allowed || !strings.Contains(plans[4].out, "LAST WARNING") || !plans[4].hardStop {
			t.Fatalf("expected last-warning hard stop, got %+v", plans[4])
		}
		if session.PostHandoverToolCallsUsed != 4 {
			t.Fatalf("expected no slot reserved for the io.EOF refusal, got %d", session.PostHandoverToolCallsUsed)
		}
	})

	t.Run("zero post-handover budget means unlimited", func(t *testing.T) {
		ctrl := &stoploss{maxToolCallsAfterHandover: 0}
		session := &QuerySession{HandoverRequested: true}

		plans := ctrl.PreflightToolCallBudget(session, []pub_models.Call{{ID: "a"}})
		if !plans[0].allowed || plans[0].prefix != "" {
			t.Fatalf("expected 0 post-handover limit to behave as unlimited, got %+v", plans[0])
		}
	})

	t.Run("load_skill allowed post-handover while budget has room, refused when exhausted", func(t *testing.T) {
		ctrl := &stoploss{maxToolCallsAfterHandover: 1}
		session := &QuerySession{HandoverRequested: true}

		skill := pub_models.Call{ID: "s", Name: string(pub_models.LoadSkillTool)}
		cmd := pub_models.Call{ID: "c", Name: "cmd"}

		first := ctrl.PreflightToolCallBudget(session, []pub_models.Call{skill})[0]
		if !first.allowed || first.prefix != "" {
			t.Fatalf("expected post-handover load_skill allowed without a prefix, got %+v", first)
		}
		cmdPlan := ctrl.PreflightToolCallBudget(session, []pub_models.Call{cmd})[0]
		if !cmdPlan.allowed {
			t.Fatalf("expected the cmd call to consume the budget, got %+v", cmdPlan)
		}
		refused := ctrl.PreflightToolCallBudget(session, []pub_models.Call{skill})[0]
		if refused.allowed || !strings.Contains(refused.out, "No more tool calls allowed") {
			t.Fatalf("expected load_skill refused once the post-handover budget is exhausted, got %+v", refused)
		}
		// The plain refusal (persistence 0) still reserves a ladder slot, so
		// the counter moves past the exhausted budget; load_skill itself never
		// reserves a slot.
		if session.PostHandoverToolCallsUsed != 2 {
			t.Fatalf("expected the refusal slot on the post-handover counter, got %d", session.PostHandoverToolCallsUsed)
		}
	})

	t.Run("load_skill is exempt from the positive budget before handover", func(t *testing.T) {
		maxCalls := 1
		ctrl := &stoploss{maxToolCalls: &maxCalls}
		session := &QuerySession{}

		plans := ctrl.PreflightToolCallBudget(session, []pub_models.Call{{ID: "a", Name: string(pub_models.LoadSkillTool)}})
		if !plans[0].allowed || plans[0].prefix != "" {
			t.Fatalf("expected load_skill exempt from the budget, got %+v", plans[0])
		}
		if session.ToolCallsUsed != 0 {
			t.Fatalf("expected load_skill to reserve no slot, got %d", session.ToolCallsUsed)
		}
	})

	t.Run("pre-handover load_skill exemption survives budget exhaustion", func(t *testing.T) {
		maxCalls := 1
		ctrl := &stoploss{maxToolCalls: &maxCalls}
		session := &QuerySession{ToolCallsUsed: 1} // budget exhausted pre-handover

		plan := ctrl.PreflightToolCallBudget(session, []pub_models.Call{{ID: "s", Name: string(pub_models.LoadSkillTool)}})[0]
		if !plan.allowed {
			t.Fatalf("expected pre-handover load_skill exempt even when the budget is exhausted, got %+v", plan)
		}
	})
}
