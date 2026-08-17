package text

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/baalimago/clai/internal/models"
	inttools "github.com/baalimago/clai/internal/tools"
	pub_models "github.com/baalimago/clai/pkg/text/models"
)

// countingStubTool records invocations so tests can prove a tool's side effect
// never ran (R2-01).
type countingStubTool struct {
	name   string
	calls  *int
	output string
}

func (t countingStubTool) Call(pub_models.Input) (string, error) {
	*t.calls++
	return t.output, nil
}

func (t countingStubTool) Specification() pub_models.Specification {
	return pub_models.Specification{Name: t.name}
}

// countingSkillLoader records LoadSkill invocations so tests can prove a skill
// was not loaded after handover (R2-01).
type countingSkillLoader struct {
	loads *int
}

func (l countingSkillLoader) LoadSkill(_ context.Context, name, _ string, _ map[string]pub_models.LLMTool) (LoadedSkillRuntime, error) {
	*l.loads++
	return LoadedSkillRuntime{Name: name, RenderedBody: "loaded skill body"}, nil
}

func newStoplossRunner(q *Querier[*MockQuerier]) sessionRunner[*MockQuerier] {
	return sessionRunner[*MockQuerier]{
		querier:      q,
		recorder:     &recordingCallUsageRecorder{},
		finalizer:    &countingFinalizer{},
		toolExecutor: toolExecutor[*MockQuerier]{querier: q},
	}
}

// assertValidToolExchanges fails when an assistant tool-call message has no
// immediately following tool result, or when the consecutive tool results do
// not cover every declared call (R2-02: no dangling calls, R11-01: one result
// per declared call).
func assertValidToolExchanges(t *testing.T, messages []pub_models.Message) {
	t.Helper()
	for i, m := range messages {
		if m.Role != "assistant" || len(m.ToolCalls) == 0 {
			continue
		}
		results := 0
		for j := i + 1; j < len(messages) && messages[j].Role == "tool"; j++ {
			results++
		}
		if results != len(m.ToolCalls) {
			t.Fatalf("assistant tool-call at index %d declares %d calls but %d tool results follow; transcript: %+v", i, len(m.ToolCalls), results, messages)
		}
	}
}

// Test_sessionRunner_Run_StoplossCrossingInjectsHandoverAfterToolResults pins
// acceptance criterion 3: a multi-step agent loop crosses max-tokens, the
// handover user message lands AFTER the crossing step's tool results, the
// follow-up step is the summary, and the run ends with the summary as
// FinalAssistantText.
func Test_sessionRunner_Run_StoplossCrossingInjectsHandoverAfterToolResults(t *testing.T) {
	model := &MockQuerier{}
	callCount := 0
	model.streamFn = func(_ context.Context, chat pub_models.Chat) (chan models.CompletionEvent, error) {
		callCount++
		out := make(chan models.CompletionEvent, 2)
		switch callCount {
		case 1:
			model.usage = &pub_models.Usage{PromptTokens: 60, CompletionTokens: 60}
			out <- pub_models.Call{ID: "call-1", Name: "missing_probe", Inputs: &pub_models.Input{}}
		default:
			if len(chat.Messages) != 4 {
				t.Errorf("expected user + tool exchange + handover message, got %d messages", len(chat.Messages))
			} else if m := chat.Messages[3]; m.Role != "user" || !strings.Contains(m.Content, "wrap up") {
				t.Errorf("expected handover user message as the last chat message, got %+v", m)
			}
			model.usage = &pub_models.Usage{PromptTokens: 5, CompletionTokens: 5}
			out <- "summary"
		}
		close(out)
		return out, nil
	}

	q := &Querier[*MockQuerier]{
		out:      &strings.Builder{},
		Model:    model,
		stoploss: &Stoploss{MaxTokens: 100, MaxTokensHandoverMsg: "wrap up"},
	}
	session := &QuerySession{Chat: pub_models.Chat{Messages: []pub_models.Message{{Role: "user", Content: "hello"}}}}
	runner := newStoplossRunner(q)

	if err := runner.Run(context.Background(), session); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if session.FinalAssistantText != "summary" {
		t.Fatalf("expected summary as final assistant text, got %q", session.FinalAssistantText)
	}
	if !session.HandoverRequested {
		t.Fatal("expected HandoverRequested after crossing")
	}
	// user, assistant tool-call, tool result, handover user msg
	if len(session.Chat.Messages) != 4 {
		t.Fatalf("expected user + tool exchange + handover message, got %d messages", len(session.Chat.Messages))
	}
	if m := session.Chat.Messages[2]; m.Role != "tool" {
		t.Fatalf("expected the crossing tool result before the handover message, got %+v", m)
	}
	if m := session.Chat.Messages[3]; m.Role != "user" || m.Content != "wrap up" {
		t.Fatalf("expected the handover user message last, got %+v", m)
	}
}

// Test_sessionRunner_Run_PostHandoverToolExecutesByDefault pins the
// post-handover tool budget contract: with no max-tool-calls-after-handover
// configured (the default), a tool call after the handover fires EXECUTES —
// the agent can summarize context into a file — and the run ends with the
// summary.
func Test_sessionRunner_Run_PostHandoverToolExecutesByDefault(t *testing.T) {
	inttools.WithTestRegistry(t, func() {
		invocations := 0
		inttools.Registry.Set("stoploss_probe", countingStubTool{name: "stoploss_probe", calls: &invocations, output: "probe side effect"})

		model := &MockQuerier{}
		callCount := 0
		model.streamFn = func(_ context.Context, _ pub_models.Chat) (chan models.CompletionEvent, error) {
			callCount++
			out := make(chan models.CompletionEvent, 2)
			model.usage = &pub_models.Usage{PromptTokens: 60, CompletionTokens: 60}
			switch callCount {
			case 1, 2:
				out <- pub_models.Call{ID: fmt.Sprintf("call-%d", callCount), Name: "stoploss_probe", Inputs: &pub_models.Input{}}
			default:
				out <- "done"
			}
			close(out)
			return out, nil
		}

		q := &Querier[*MockQuerier]{
			out:      &strings.Builder{},
			Model:    model,
			stoploss: &Stoploss{MaxTokens: 100, MaxTokensHandoverMsg: "wrap up"},
		}
		session := &QuerySession{Chat: pub_models.Chat{Messages: []pub_models.Message{{Role: "user", Content: "hello"}}}}
		runner := newStoplossRunner(q)

		if err := runner.Run(context.Background(), session); err != nil {
			t.Fatalf("Run: %v", err)
		}
		if invocations != 2 {
			t.Fatalf("expected both the crossing and the post-handover tool to run, got %d invocations", invocations)
		}
		if session.FinalAssistantText != "done" {
			t.Fatalf("expected summary as final assistant text, got %q", session.FinalAssistantText)
		}
		// user, crossing pair, handover, post-handover pair
		if len(session.Chat.Messages) != 6 {
			t.Fatalf("expected user + crossing pair + handover + post-handover pair, got %d messages", len(session.Chat.Messages))
		}
		last := session.Chat.Messages[5]
		if last.Role != "tool" || !strings.Contains(last.Content, "probe side effect") {
			t.Fatalf("expected the post-handover tool result to carry real output, got %+v", last)
		}
		assertValidToolExchanges(t, session.Chat.Messages)
	})
}

// Test_sessionRunner_Run_PostHandoverBudgetExhaustionEndsCleanly pins the
// bounded wrap-up contract: with max-tool-calls-after-handover 1 the agent
// gets exactly one post-handover execution, persisting past the final warning
// returns io.EOF, Run returns nil, and the transcript still contains a valid
// assistant/tool exchange for every call.
func Test_sessionRunner_Run_PostHandoverBudgetExhaustionEndsCleanly(t *testing.T) {
	inttools.WithTestRegistry(t, func() {
		invocations := 0
		inttools.Registry.Set("stoploss_probe", countingStubTool{name: "stoploss_probe", calls: &invocations, output: "probe side effect"})

		model := &MockQuerier{}
		callCount := 0
		model.streamFn = func(_ context.Context, _ pub_models.Chat) (chan models.CompletionEvent, error) {
			callCount++
			out := make(chan models.CompletionEvent, 2)
			model.usage = &pub_models.Usage{PromptTokens: 60, CompletionTokens: 60}
			out <- pub_models.Call{ID: fmt.Sprintf("call-%d", callCount), Name: "stoploss_probe", Inputs: &pub_models.Input{}}
			close(out)
			return out, nil
		}

		q := &Querier[*MockQuerier]{
			out:      &strings.Builder{},
			Model:    model,
			stoploss: &Stoploss{MaxTokens: 100, MaxTokensHandoverMsg: "wrap up", MaxToolCallsAfterHandover: 1},
		}
		session := &QuerySession{Chat: pub_models.Chat{Messages: []pub_models.Message{{Role: "user", Content: "hello"}}}}
		runner := newStoplossRunner(q)

		if err := runner.Run(context.Background(), session); err != nil {
			t.Fatalf("expected clean end past the final warning, got %v", err)
		}
		// crossing (1) + post-handover allowed (1) + plain refusal + HARD SHUT
		// DOWN + LAST WARNING + io.EOF = 6 model steps.
		if callCount != 6 {
			t.Fatalf("expected 6 model steps, got %d", callCount)
		}
		if invocations != 2 {
			t.Fatalf("expected the crossing and the first post-handover call to run, got %d invocations", invocations)
		}
		if !session.HandoverRequested {
			t.Fatal("expected handover requested")
		}
		// user + crossing pair + handover + 5 post-handover pairs
		if len(session.Chat.Messages) != 14 {
			t.Fatalf("expected 14 messages, got %d", len(session.Chat.Messages))
		}
		assertValidToolExchanges(t, session.Chat.Messages)
		if last := session.Chat.Messages[13]; last.Role != "tool" || !strings.Contains(last.Content, "LAST WARNING") {
			t.Fatalf("expected the final refusal to carry the last-warning text, got %+v", last)
		}
	})
}

// Test_sessionRunner_Run_PostHandoverLoadSkillLoadsByDefault pins R2-01
// (load_skill) under the new default: without a post-handover budget a
// post-handover load_skill call loads normally.
func Test_sessionRunner_Run_PostHandoverLoadSkillLoadsByDefault(t *testing.T) {
	loads := 0
	model := &MockQuerier{}
	callCount := 0
	model.streamFn = func(_ context.Context, _ pub_models.Chat) (chan models.CompletionEvent, error) {
		callCount++
		out := make(chan models.CompletionEvent, 2)
		model.usage = &pub_models.Usage{PromptTokens: 60, CompletionTokens: 60}
		switch callCount {
		case 1:
			out <- pub_models.Call{ID: "call-1", Name: "missing_probe", Inputs: &pub_models.Input{}}
		case 2:
			inputs := pub_models.Input{"skill": "test"}
			out <- pub_models.Call{ID: "call-2", Name: string(pub_models.LoadSkillTool), Inputs: &inputs}
		default:
			out <- "done"
		}
		close(out)
		return out, nil
	}

	q := &Querier[*MockQuerier]{
		out:      &strings.Builder{},
		Model:    model,
		stoploss: &Stoploss{MaxTokens: 100, MaxTokensHandoverMsg: "wrap up"},
		tooling:  tooling{skillLoader: countingSkillLoader{loads: &loads}},
	}
	session := &QuerySession{Chat: pub_models.Chat{Messages: []pub_models.Message{{Role: "user", Content: "hello"}}}}
	runner := newStoplossRunner(q)

	if err := runner.Run(context.Background(), session); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if loads != 1 {
		t.Fatalf("expected load_skill to load after handover by default, got %d loads", loads)
	}
	last := session.Chat.Messages[len(session.Chat.Messages)-1]
	if last.Role != "tool" || !strings.Contains(last.Content, "loaded skill body") {
		t.Fatalf("expected the skill body as the post-handover tool result, got %+v", last)
	}
}

// Test_sessionRunner_Run_PostHandoverLoadSkillRefusedWhenBudgetExhausted pins
// that once the post-handover budget is exhausted, load_skill is refused
// before the skill loader runs.
func Test_sessionRunner_Run_PostHandoverLoadSkillRefusedWhenBudgetExhausted(t *testing.T) {
	loads := 0
	model := &MockQuerier{}
	callCount := 0
	model.streamFn = func(_ context.Context, _ pub_models.Chat) (chan models.CompletionEvent, error) {
		callCount++
		out := make(chan models.CompletionEvent, 2)
		model.usage = &pub_models.Usage{PromptTokens: 60, CompletionTokens: 60}
		switch callCount {
		case 1, 2:
			out <- pub_models.Call{ID: fmt.Sprintf("call-%d", callCount), Name: "stoploss_probe", Inputs: &pub_models.Input{}}
		case 3:
			inputs := pub_models.Input{"skill": "test"}
			out <- pub_models.Call{ID: "call-3", Name: string(pub_models.LoadSkillTool), Inputs: &inputs}
		default:
			out <- "done"
		}
		close(out)
		return out, nil
	}

	inttools.WithTestRegistry(t, func() {
		invocations := 0
		inttools.Registry.Set("stoploss_probe", countingStubTool{name: "stoploss_probe", calls: &invocations, output: "probe side effect"})

		q := &Querier[*MockQuerier]{
			out:      &strings.Builder{},
			Model:    model,
			stoploss: &Stoploss{MaxTokens: 100, MaxTokensHandoverMsg: "wrap up", MaxToolCallsAfterHandover: 1},
			tooling:  tooling{skillLoader: countingSkillLoader{loads: &loads}},
		}
		session := &QuerySession{Chat: pub_models.Chat{Messages: []pub_models.Message{{Role: "user", Content: "hello"}}}}
		runner := newStoplossRunner(q)

		if err := runner.Run(context.Background(), session); err != nil {
			t.Fatalf("Run: %v", err)
		}
		// Crossing probe + post-handover probe consume the wrap-up budget; the
		// load_skill on step 3 is refused before loading.
		if invocations != 2 {
			t.Fatalf("expected the two probes to run, got %d invocations", invocations)
		}
		if loads != 0 {
			t.Fatalf("load_skill must not load once the post-handover budget is exhausted, got %d loads", loads)
		}
		last := session.Chat.Messages[len(session.Chat.Messages)-1]
		if last.Role != "tool" || !strings.Contains(last.Content, "No more tool calls allowed") {
			t.Fatalf("expected refusal ladder text for load_skill, got %+v", last)
		}
	})
}

// Test_sessionRunner_Run_PreHandoverLoadSkillLoads is the positive control for
// the load_skill instrumentation: without handover the same fake loader runs.
func Test_sessionRunner_Run_PreHandoverLoadSkillLoads(t *testing.T) {
	loads := 0
	model := &MockQuerier{}
	callCount := 0
	model.streamFn = func(_ context.Context, _ pub_models.Chat) (chan models.CompletionEvent, error) {
		callCount++
		out := make(chan models.CompletionEvent, 2)
		model.usage = &pub_models.Usage{PromptTokens: 5, CompletionTokens: 5}
		if callCount == 1 {
			inputs := pub_models.Input{"skill": "test"}
			out <- pub_models.Call{ID: "call-1", Name: string(pub_models.LoadSkillTool), Inputs: &inputs}
		} else {
			out <- "done"
		}
		close(out)
		return out, nil
	}

	q := &Querier[*MockQuerier]{
		out:      &strings.Builder{},
		Model:    model,
		stoploss: &Stoploss{MaxTokens: 100, MaxTokensHandoverMsg: "wrap up"},
		tooling:  tooling{skillLoader: countingSkillLoader{loads: &loads}},
	}
	session := &QuerySession{Chat: pub_models.Chat{Messages: []pub_models.Message{{Role: "user", Content: "hello"}}}}
	runner := newStoplossRunner(q)

	if err := runner.Run(context.Background(), session); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if loads != 1 {
		t.Fatalf("expected the fake skill loader to run pre-handover, got %d loads", loads)
	}
}

// Test_toolExecutor_PostHandoverLookbackExecutesByDefault pins the lookback
// tools under the new default: after handover the lookback dispatch runs and
// the tool result carries real output.
func Test_toolExecutor_PostHandoverLookbackExecutesByDefault(t *testing.T) {
	q := &Querier[*MockQuerier]{
		out:         &strings.Builder{},
		useLookback: true,
		configDir:   t.TempDir(),
	}
	session := &QuerySession{HandoverRequested: true, Chat: pub_models.Chat{}}
	call := pub_models.Call{ID: "lb-1", Name: string(pub_models.SearchConversationsTool), Inputs: &pub_models.Input{}}

	err := toolExecutor[*MockQuerier]{querier: q}.ExecuteBatch(context.Background(), session, []pub_models.Call{call})
	if err != nil {
		t.Fatalf("ExecuteBatch: %v", err)
	}
	last := session.Chat.Messages[len(session.Chat.Messages)-1]
	if last.Role != "tool" || strings.Contains(last.Content, "No more tool calls allowed") {
		t.Fatalf("expected the lookback tool to run after handover by default, got %+v", last)
	}
}

// Test_toolExecutor_PostHandoverLookbackRefusedWhenBudgetExhausted pins that
// once the post-handover budget is exhausted, the lookback dispatch never
// runs; the tool result carries the ladder text.
func Test_toolExecutor_PostHandoverLookbackRefusedWhenBudgetExhausted(t *testing.T) {
	q := &Querier[*MockQuerier]{
		out:         &strings.Builder{},
		useLookback: true,
		configDir:   t.TempDir(),
		stoploss:    &Stoploss{MaxToolCallsAfterHandover: 1},
	}
	session := &QuerySession{HandoverRequested: true, PostHandoverToolCallsUsed: 1, Chat: pub_models.Chat{}}
	call := pub_models.Call{ID: "lb-1", Name: string(pub_models.SearchConversationsTool), Inputs: &pub_models.Input{}}

	err := toolExecutor[*MockQuerier]{querier: q}.ExecuteBatch(context.Background(), session, []pub_models.Call{call})
	if err != nil {
		t.Fatalf("ExecuteBatch: %v", err)
	}
	last := session.Chat.Messages[len(session.Chat.Messages)-1]
	if last.Role != "tool" || !strings.Contains(last.Content, "No more tool calls allowed") {
		t.Fatalf("expected refusal ladder text in the lookback tool result, got %+v", last)
	}
}

// Test_toolExecutor_PreHandoverLookbackExecutes is the positive control for
// the lookback instrumentation: without handover the lookback dispatch runs.
func Test_toolExecutor_PreHandoverLookbackExecutes(t *testing.T) {
	q := &Querier[*MockQuerier]{
		out:         &strings.Builder{},
		useLookback: true,
		configDir:   t.TempDir(),
	}
	session := &QuerySession{Chat: pub_models.Chat{}}
	call := pub_models.Call{ID: "lb-1", Name: string(pub_models.SearchConversationsTool), Inputs: &pub_models.Input{}}

	err := toolExecutor[*MockQuerier]{querier: q}.ExecuteBatch(context.Background(), session, []pub_models.Call{call})
	if err != nil {
		t.Fatalf("ExecuteBatch: %v", err)
	}
	last := session.Chat.Messages[len(session.Chat.Messages)-1]
	if last.Role != "tool" || strings.Contains(last.Content, "No more tool calls allowed") {
		t.Fatalf("expected the lookback tool to run pre-handover, got %+v", last)
	}
}

// Test_toolExecutor_ExecuteBatch_RefusedCallHasNoSideEffect pins R3-02 for a
// positive budget: a batch with one allowed and one refused call decides both
// calls before invoking any tool; the refused side effect never runs and both
// tool results are emitted in order.
func Test_toolExecutor_ExecuteBatch_RefusedCallHasNoSideEffect(t *testing.T) {
	inttools.WithTestRegistry(t, func() {
		allowedCalls := 0
		refusedCalls := 0
		inttools.Registry.Set("allowed_probe", countingStubTool{name: "allowed_probe", calls: &allowedCalls, output: "allowed output"})
		inttools.Registry.Set("refused_probe", countingStubTool{name: "refused_probe", calls: &refusedCalls, output: "must not run"})

		maxCalls := 1
		q := &Querier[*MockQuerier]{out: &strings.Builder{}, tooling: tooling{maxCalls: &maxCalls}}
		session := &QuerySession{Chat: pub_models.Chat{}}
		executor := toolExecutor[*MockQuerier]{querier: q}

		err := executor.ExecuteBatch(context.Background(), session, []pub_models.Call{
			{ID: "a", Name: "allowed_probe", Inputs: &pub_models.Input{}},
			{ID: "b", Name: "refused_probe", Inputs: &pub_models.Input{}},
		})
		if err != nil {
			t.Fatalf("ExecuteBatch: %v", err)
		}
		if allowedCalls != 1 || refusedCalls != 0 {
			t.Fatalf("expected only the allowed tool to run, got allowed=%d refused=%d", allowedCalls, refusedCalls)
		}
		if len(session.Chat.Messages) != 3 {
			t.Fatalf("expected assistant tool-calls + 2 tool results, got %d messages", len(session.Chat.Messages))
		}
		if !strings.Contains(session.Chat.Messages[1].Content, "Tool calls remaining: 1") {
			t.Fatalf("expected remaining-count prefix on the allowed result, got %q", session.Chat.Messages[1].Content)
		}
		if !strings.Contains(session.Chat.Messages[2].Content, "No more tool calls allowed") {
			t.Fatalf("expected refusal ladder text on the refused result, got %q", session.Chat.Messages[2].Content)
		}
	})
}

// Test_toolExecutor_ExecuteBatch_PostHandoverBatchExecutesByDefault pins the
// R3-02 example under the new default: a post-handover batch runs every call
// when no max-tool-calls-after-handover is configured.
func Test_toolExecutor_ExecuteBatch_PostHandoverBatchExecutesByDefault(t *testing.T) {
	inttools.WithTestRegistry(t, func() {
		cmdCalls := 0
		otherCalls := 0
		inttools.Registry.Set("probe_cmd", countingStubTool{name: "probe_cmd", calls: &cmdCalls, output: "cmd side effect"})
		inttools.Registry.Set("probe_other", countingStubTool{name: "probe_other", calls: &otherCalls, output: "other side effect"})

		q := &Querier[*MockQuerier]{out: &strings.Builder{}}
		session := &QuerySession{HandoverRequested: true, Chat: pub_models.Chat{}}
		executor := toolExecutor[*MockQuerier]{querier: q}

		err := executor.ExecuteBatch(context.Background(), session, []pub_models.Call{
			{ID: "cmd", Name: "probe_cmd", Inputs: &pub_models.Input{}},
			{ID: "other", Name: "probe_other", Inputs: &pub_models.Input{}},
		})
		if err != nil {
			t.Fatalf("ExecuteBatch: %v", err)
		}
		if cmdCalls != 1 || otherCalls != 1 {
			t.Fatalf("expected both post-handover tools to run by default, got cmd=%d other=%d", cmdCalls, otherCalls)
		}
		if len(session.Chat.Messages) != 3 {
			t.Fatalf("expected assistant tool-calls + 2 tool results, got %d messages", len(session.Chat.Messages))
		}
		if !strings.Contains(session.Chat.Messages[1].Content, "cmd side effect") {
			t.Fatalf("expected real cmd output, got %q", session.Chat.Messages[1].Content)
		}
		if !strings.Contains(session.Chat.Messages[2].Content, "other side effect") {
			t.Fatalf("expected real other output, got %q", session.Chat.Messages[2].Content)
		}
		assertValidToolExchanges(t, session.Chat.Messages)
	})
}

// Test_toolExecutor_ExecuteBatch_PostHandoverBatchRefusesAfterBudget pins the
// bounded wrap-up: with max-tool-calls-after-handover 1 the first call of a
// post-handover batch executes and the second is refused before invocation,
// with one tool result per declared call.
func Test_toolExecutor_ExecuteBatch_PostHandoverBatchRefusesAfterBudget(t *testing.T) {
	inttools.WithTestRegistry(t, func() {
		cmdCalls := 0
		otherCalls := 0
		inttools.Registry.Set("probe_cmd", countingStubTool{name: "probe_cmd", calls: &cmdCalls, output: "cmd side effect"})
		inttools.Registry.Set("probe_other", countingStubTool{name: "probe_other", calls: &otherCalls, output: "other side effect"})

		q := &Querier[*MockQuerier]{out: &strings.Builder{}, stoploss: &Stoploss{MaxToolCallsAfterHandover: 1}}
		session := &QuerySession{HandoverRequested: true, Chat: pub_models.Chat{}}
		executor := toolExecutor[*MockQuerier]{querier: q}

		err := executor.ExecuteBatch(context.Background(), session, []pub_models.Call{
			{ID: "cmd", Name: "probe_cmd", Inputs: &pub_models.Input{}},
			{ID: "other", Name: "probe_other", Inputs: &pub_models.Input{}},
		})
		if err != nil {
			t.Fatalf("ExecuteBatch: %v", err)
		}
		if cmdCalls != 1 || otherCalls != 0 {
			t.Fatalf("expected only the within-budget post-handover tool to run, got cmd=%d other=%d", cmdCalls, otherCalls)
		}
		if len(session.Chat.Messages) != 3 {
			t.Fatalf("expected assistant tool-calls + 2 tool results, got %d messages", len(session.Chat.Messages))
		}
		if !strings.Contains(session.Chat.Messages[1].Content, "cmd side effect") {
			t.Fatalf("expected the first post-handover call to execute, got %q", session.Chat.Messages[1].Content)
		}
		if !strings.Contains(session.Chat.Messages[2].Content, "No more tool calls allowed") {
			t.Fatalf("expected refusal ladder text on the second call, got %q", session.Chat.Messages[2].Content)
		}
		assertValidToolExchanges(t, session.Chat.Messages)
	})
}

// mixedBatchQuerier builds a querier with a registered ordinary probe tool and
// a counting skill loader for the R9-01 mixed-batch pairing tests.
func mixedBatchQuerier(t *testing.T, cmdCalls, loads *int) *Querier[*MockQuerier] {
	t.Helper()
	inttools.Registry.Set("mixed_probe", countingStubTool{name: "mixed_probe", calls: cmdCalls, output: "cmd output"})
	return &Querier[*MockQuerier]{
		out:     &strings.Builder{},
		tooling: tooling{skillLoader: countingSkillLoader{loads: loads}},
	}
}

// Test_toolExecutor_ExecuteBatch_MixedBatchLoadSkillFirst pins R9-01: a batch
// [load_skill, cmd] must keep immediate assistant→tool pairing in the model's
// emission order — the load_skill pair first, then the cmd pair — with no two
// consecutive assistant tool-call messages.
func Test_toolExecutor_ExecuteBatch_MixedBatchLoadSkillFirst(t *testing.T) {
	inttools.WithTestRegistry(t, func() {
		cmdCalls := 0
		loads := 0
		q := mixedBatchQuerier(t, &cmdCalls, &loads)
		session := &QuerySession{Chat: pub_models.Chat{}}
		executor := toolExecutor[*MockQuerier]{querier: q}

		inputs := pub_models.Input{"skill": "test"}
		err := executor.ExecuteBatch(context.Background(), session, []pub_models.Call{
			{ID: "skill-1", Name: string(pub_models.LoadSkillTool), Inputs: &inputs},
			{ID: "cmd-1", Name: "mixed_probe", Inputs: &pub_models.Input{}},
		})
		if err != nil {
			t.Fatalf("ExecuteBatch: %v", err)
		}
		if loads != 1 || cmdCalls != 1 {
			t.Fatalf("expected both tools to run, got loads=%d cmd=%d", loads, cmdCalls)
		}
		if len(session.Chat.Messages) != 4 {
			t.Fatalf("expected 2 assistant tool-call turns + 2 tool results, got %d messages", len(session.Chat.Messages))
		}
		assertValidToolExchanges(t, session.Chat.Messages)
		if got := session.Chat.Messages[0].ToolCalls[0].Name; got != string(pub_models.LoadSkillTool) {
			t.Fatalf("expected the load_skill pair first, got %q", got)
		}
		if got := session.Chat.Messages[2].ToolCalls[0].Name; got != "mixed_probe" {
			t.Fatalf("expected the cmd pair second, got %q", got)
		}
	})
}

// Test_toolExecutor_ExecuteBatch_MixedBatchLoadSkillLast pins R9-01 for the
// reversed batch [cmd, load_skill]: the cmd pair first, then the load_skill
// pair, with valid assistant→tool pairing throughout.
func Test_toolExecutor_ExecuteBatch_MixedBatchLoadSkillLast(t *testing.T) {
	inttools.WithTestRegistry(t, func() {
		cmdCalls := 0
		loads := 0
		q := mixedBatchQuerier(t, &cmdCalls, &loads)
		session := &QuerySession{Chat: pub_models.Chat{}}
		executor := toolExecutor[*MockQuerier]{querier: q}

		inputs := pub_models.Input{"skill": "test"}
		err := executor.ExecuteBatch(context.Background(), session, []pub_models.Call{
			{ID: "cmd-1", Name: "mixed_probe", Inputs: &pub_models.Input{}},
			{ID: "skill-1", Name: string(pub_models.LoadSkillTool), Inputs: &inputs},
		})
		if err != nil {
			t.Fatalf("ExecuteBatch: %v", err)
		}
		if loads != 1 || cmdCalls != 1 {
			t.Fatalf("expected both tools to run, got loads=%d cmd=%d", loads, cmdCalls)
		}
		if len(session.Chat.Messages) != 4 {
			t.Fatalf("expected 2 assistant tool-call turns + 2 tool results, got %d messages", len(session.Chat.Messages))
		}
		assertValidToolExchanges(t, session.Chat.Messages)
		if got := session.Chat.Messages[0].ToolCalls[0].Name; got != "mixed_probe" {
			t.Fatalf("expected the cmd pair first, got %q", got)
		}
		if got := session.Chat.Messages[2].ToolCalls[0].Name; got != string(pub_models.LoadSkillTool) {
			t.Fatalf("expected the load_skill pair second, got %q", got)
		}
	})
}

// Test_toolExecutor_ExecuteBatch_MixedBatchLoadSkillMiddle pins the segment
// split for [cmd, load_skill, cmd]: each call keeps its own assistant→tool
// pair in emission order, so the non-skill calls no longer share one assistant
// turn when a load_skill interrupts them (R9-01).
func Test_toolExecutor_ExecuteBatch_MixedBatchLoadSkillMiddle(t *testing.T) {
	inttools.WithTestRegistry(t, func() {
		cmdCalls := 0
		loads := 0
		q := mixedBatchQuerier(t, &cmdCalls, &loads)
		session := &QuerySession{Chat: pub_models.Chat{}}
		executor := toolExecutor[*MockQuerier]{querier: q}

		inputs := pub_models.Input{"skill": "test"}
		err := executor.ExecuteBatch(context.Background(), session, []pub_models.Call{
			{ID: "cmd-1", Name: "mixed_probe", Inputs: &pub_models.Input{}},
			{ID: "skill-1", Name: string(pub_models.LoadSkillTool), Inputs: &inputs},
			{ID: "cmd-2", Name: "mixed_probe", Inputs: &pub_models.Input{}},
		})
		if err != nil {
			t.Fatalf("ExecuteBatch: %v", err)
		}
		if loads != 1 || cmdCalls != 2 {
			t.Fatalf("expected all three tools to run, got loads=%d cmd=%d", loads, cmdCalls)
		}
		if len(session.Chat.Messages) != 6 {
			t.Fatalf("expected 3 assistant tool-call turns + 3 tool results, got %d messages", len(session.Chat.Messages))
		}
		assertValidToolExchanges(t, session.Chat.Messages)
		names := []string{
			session.Chat.Messages[0].ToolCalls[0].Name,
			session.Chat.Messages[2].ToolCalls[0].Name,
			session.Chat.Messages[4].ToolCalls[0].Name,
		}
		want := []string{"mixed_probe", string(pub_models.LoadSkillTool), "mixed_probe"}
		for i := range want {
			if names[i] != want[i] {
				t.Fatalf("expected emission order %v, got %v", want, names)
			}
		}
	})
}

// Test_toolExecutor_ExecuteBatch_PostHandoverMixedBatchExecutesWithPairing pins
// R9-01 under the new default: a post-handover [cmd, load_skill] batch keeps
// immediate pairing — the cmd pair first, then the load_skill pair — with
// both tools running.
func Test_toolExecutor_ExecuteBatch_PostHandoverMixedBatchExecutesWithPairing(t *testing.T) {
	inttools.WithTestRegistry(t, func() {
		cmdCalls := 0
		loads := 0
		q := mixedBatchQuerier(t, &cmdCalls, &loads)
		session := &QuerySession{HandoverRequested: true, Chat: pub_models.Chat{}}
		executor := toolExecutor[*MockQuerier]{querier: q}

		inputs := pub_models.Input{"skill": "test"}
		err := executor.ExecuteBatch(context.Background(), session, []pub_models.Call{
			{ID: "cmd-1", Name: "mixed_probe", Inputs: &pub_models.Input{}},
			{ID: "skill-1", Name: string(pub_models.LoadSkillTool), Inputs: &inputs},
		})
		if err != nil {
			t.Fatalf("ExecuteBatch: %v", err)
		}
		if cmdCalls != 1 || loads != 1 {
			t.Fatalf("expected both tools to run after handover by default, got cmd=%d loads=%d", cmdCalls, loads)
		}
		if len(session.Chat.Messages) != 4 {
			t.Fatalf("expected 2 assistant tool-call turns + 2 tool results, got %d messages", len(session.Chat.Messages))
		}
		assertValidToolExchanges(t, session.Chat.Messages)
	})
}

// Test_toolExecutor_ExecuteBatch_PostHandoverMixedBatchRefusesWithPairing pins
// R9-01 for the refused path: once the post-handover budget is exhausted, a
// [cmd, load_skill] batch keeps immediate pairing — the cmd refusal pair, then
// the load_skill refusal pair — with the ladder escalating in emission order
// and no side effect running.
func Test_toolExecutor_ExecuteBatch_PostHandoverMixedBatchRefusesWithPairing(t *testing.T) {
	inttools.WithTestRegistry(t, func() {
		cmdCalls := 0
		loads := 0
		q := mixedBatchQuerier(t, &cmdCalls, &loads)
		q.stoploss = &Stoploss{MaxToolCallsAfterHandover: 1}
		session := &QuerySession{HandoverRequested: true, PostHandoverToolCallsUsed: 1, Chat: pub_models.Chat{}}
		executor := toolExecutor[*MockQuerier]{querier: q}

		inputs := pub_models.Input{"skill": "test"}
		err := executor.ExecuteBatch(context.Background(), session, []pub_models.Call{
			{ID: "cmd-1", Name: "mixed_probe", Inputs: &pub_models.Input{}},
			{ID: "skill-1", Name: string(pub_models.LoadSkillTool), Inputs: &inputs},
		})
		if err != nil {
			t.Fatalf("ExecuteBatch: %v", err)
		}
		if cmdCalls != 0 || loads != 0 {
			t.Fatalf("no post-handover side effect may run past the budget, got cmd=%d loads=%d", cmdCalls, loads)
		}
		if len(session.Chat.Messages) != 4 {
			t.Fatalf("expected 2 refusal pairs, got %d messages", len(session.Chat.Messages))
		}
		assertValidToolExchanges(t, session.Chat.Messages)
		if !strings.Contains(session.Chat.Messages[1].Content, "No more tool calls allowed") {
			t.Fatalf("expected plain refusal for the first call, got %q", session.Chat.Messages[1].Content)
		}
		if !strings.Contains(session.Chat.Messages[3].Content, "HARD SHUT DOWN") {
			t.Fatalf("expected escalated refusal for the second call, got %q", session.Chat.Messages[3].Content)
		}
	})
}

// Test_toolExecutor_ExecuteBatch_HardStopMidBatchEmitsAllResults pins R11-01:
// a hardStop plan that is not the last plan of its assistant turn must not
// skip the remaining plans. The assistant message already declared every call
// in the batch, so each one must receive a tool result before io.EOF is
// returned; otherwise the persisted transcript contains a dangling tool_call
// that a follow-up replay request cannot send to the vendor.
func Test_toolExecutor_ExecuteBatch_HardStopMidBatchEmitsAllResults(t *testing.T) {
	q := &Querier[*MockQuerier]{out: &strings.Builder{}, stoploss: &Stoploss{MaxToolCallsAfterHandover: 1}}
	session := &QuerySession{HandoverRequested: true, Chat: pub_models.Chat{}}
	executor := toolExecutor[*MockQuerier]{querier: q}

	// Post-handover budget is 1, so the ladder persists on the 4th call and
	// hardStops on the 5th: the hardStop plan is the last one. Use six calls
	// so the hardStop lands mid-batch (calls 4 and 5 are hardStop, call 6
	// follows the first hardStop).
	calls := make([]pub_models.Call, 0, 6)
	for i := 1; i <= 6; i++ {
		calls = append(calls, pub_models.Call{ID: fmt.Sprintf("call-%d", i), Name: "missing_probe", Inputs: &pub_models.Input{}})
	}

	err := executor.ExecuteBatch(context.Background(), session, calls)
	if !errors.Is(err, io.EOF) {
		t.Fatalf("expected io.EOF from the deferred hardStop, got %v", err)
	}
	// Assistant tool-calls + 6 ladder results: no declared call may lack a result.
	if len(session.Chat.Messages) != 7 {
		t.Fatalf("expected assistant tool-calls + 6 tool results, got %d messages", len(session.Chat.Messages))
	}
	assertValidToolExchanges(t, session.Chat.Messages)
}

// Test_toolExecutor_ExecuteBatch_PostHandoverMixedBatchLoadSkillFirstExecutesWithPairing
// pins R9-01 under the new default with load_skill leading: a post-handover
// [load_skill, cmd] batch keeps immediate pairing — the load_skill pair first,
// then the cmd pair — with both tools running.
func Test_toolExecutor_ExecuteBatch_PostHandoverMixedBatchLoadSkillFirstExecutesWithPairing(t *testing.T) {
	inttools.WithTestRegistry(t, func() {
		cmdCalls := 0
		loads := 0
		q := mixedBatchQuerier(t, &cmdCalls, &loads)
		session := &QuerySession{HandoverRequested: true, Chat: pub_models.Chat{}}
		executor := toolExecutor[*MockQuerier]{querier: q}

		inputs := pub_models.Input{"skill": "test"}
		err := executor.ExecuteBatch(context.Background(), session, []pub_models.Call{
			{ID: "skill-1", Name: string(pub_models.LoadSkillTool), Inputs: &inputs},
			{ID: "cmd-1", Name: "mixed_probe", Inputs: &pub_models.Input{}},
		})
		if err != nil {
			t.Fatalf("ExecuteBatch: %v", err)
		}
		if cmdCalls != 1 || loads != 1 {
			t.Fatalf("expected both tools to run after handover by default, got cmd=%d loads=%d", cmdCalls, loads)
		}
		if len(session.Chat.Messages) != 4 {
			t.Fatalf("expected 2 assistant tool-call turns + 2 tool results, got %d messages", len(session.Chat.Messages))
		}
		assertValidToolExchanges(t, session.Chat.Messages)
		if got := session.Chat.Messages[0].ToolCalls[0].Name; got != string(pub_models.LoadSkillTool) {
			t.Fatalf("expected the load_skill pair first, got %q", got)
		}
	})
}

// Test_toolExecutor_ExecuteBatch_PostHandoverMixedBatchLoadSkillFirstRefusesWithPairing
// pins R9-01 for the refused path with load_skill leading: once the
// post-handover budget is exhausted, a [load_skill, cmd] batch keeps immediate
// pairing — the load_skill refusal pair first, then the cmd refusal pair —
// with the ladder escalating in emission order.
func Test_toolExecutor_ExecuteBatch_PostHandoverMixedBatchLoadSkillFirstRefusesWithPairing(t *testing.T) {
	inttools.WithTestRegistry(t, func() {
		cmdCalls := 0
		loads := 0
		q := mixedBatchQuerier(t, &cmdCalls, &loads)
		q.stoploss = &Stoploss{MaxToolCallsAfterHandover: 1}
		session := &QuerySession{HandoverRequested: true, PostHandoverToolCallsUsed: 1, Chat: pub_models.Chat{}}
		executor := toolExecutor[*MockQuerier]{querier: q}

		inputs := pub_models.Input{"skill": "test"}
		err := executor.ExecuteBatch(context.Background(), session, []pub_models.Call{
			{ID: "skill-1", Name: string(pub_models.LoadSkillTool), Inputs: &inputs},
			{ID: "cmd-1", Name: "mixed_probe", Inputs: &pub_models.Input{}},
		})
		if err != nil {
			t.Fatalf("ExecuteBatch: %v", err)
		}
		if cmdCalls != 0 || loads != 0 {
			t.Fatalf("no post-handover side effect may run past the budget, got cmd=%d loads=%d", cmdCalls, loads)
		}
		if len(session.Chat.Messages) != 4 {
			t.Fatalf("expected 2 refusal pairs, got %d messages", len(session.Chat.Messages))
		}
		assertValidToolExchanges(t, session.Chat.Messages)
		if got := session.Chat.Messages[0].ToolCalls[0].Name; got != string(pub_models.LoadSkillTool) {
			t.Fatalf("expected the load_skill pair first, got %q", got)
		}
		if !strings.Contains(session.Chat.Messages[1].Content, "No more tool calls allowed") {
			t.Fatalf("expected plain refusal for the first call, got %q", session.Chat.Messages[1].Content)
		}
		if !strings.Contains(session.Chat.Messages[3].Content, "HARD SHUT DOWN") {
			t.Fatalf("expected escalated refusal for the second call, got %q", session.Chat.Messages[3].Content)
		}
	})
}
