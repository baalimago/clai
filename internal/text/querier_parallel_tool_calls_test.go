package text

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/baalimago/clai/internal/models"
	"github.com/baalimago/clai/internal/tools"
	pub_models "github.com/baalimago/clai/pkg/text/models"
)

type recorderCompleter struct {
	chats []pub_models.Chat
}

type staticTool struct{ name string }

func (r *recorderCompleter) Setup() error { return nil }

func (r *recorderCompleter) StreamCompletions(ctx context.Context, chat pub_models.Chat) (chan models.CompletionEvent, error) {
	r.chats = append(r.chats, chat)
	ch := make(chan models.CompletionEvent)
	close(ch)
	return ch, nil
}

func (s staticTool) Call(input pub_models.Input) (string, error) {
	return s.name + "-out", nil
}

func (s staticTool) Specification() pub_models.Specification {
	return pub_models.Specification{Name: s.name}
}

type blockingTool struct {
	started chan string
	release <-chan struct{}
	result  string
	active  *atomic.Int32
	maxSeen *atomic.Int32
}

func (b blockingTool) Call(input pub_models.Input) (string, error) {
	current := b.active.Add(1)
	for {
		prev := b.maxSeen.Load()
		if current <= prev {
			break
		}
		if b.maxSeen.CompareAndSwap(prev, current) {
			break
		}
	}
	defer b.active.Add(-1)
	if b.started != nil {
		b.started <- b.result
	}
	<-b.release
	return b.result, nil
}

func (b blockingTool) Specification() pub_models.Specification {
	return pub_models.Specification{Name: b.result}
}

func TestHandleCompletion_ToolCallsEvent_AppendsMessagesAndQueriesOnce(t *testing.T) {
	orig := tools.Registry
	tools.Registry = tools.NewRegistry()
	defer func() { tools.Registry = orig }()

	tools.Registry.Set("tool_a", staticTool{name: "tool_a"})
	tools.Registry.Set("tool_b", staticTool{name: "tool_b"})

	model := &recorderCompleter{}
	q := Querier[*recorderCompleter]{
		Raw:   true,
		Model: model,
		out:   &strings.Builder{},
		chat: pub_models.Chat{
			Messages: []pub_models.Message{{Role: "user", Content: "hi"}},
		},
		fullMsg: "thinking",
	}

	err := q.handleCompletion(context.Background(), models.ToolCallsEvent{Calls: []pub_models.Call{
		{ID: "id1", Name: "tool_a", Inputs: &pub_models.Input{"x": "1"}},
		{ID: "id2", Name: "tool_b", Inputs: &pub_models.Input{"y": "2"}},
	}})
	if err != nil {
		t.Fatalf("handleCompletion err: %v", err)
	}

	if len(model.chats) != 1 {
		t.Fatalf("expected exactly one follow-up query, got %d", len(model.chats))
	}
	if got := len(q.chat.Messages); got != 5 {
		t.Fatalf("expected 5 chat messages, got %d: %+v", got, q.chat.Messages)
	}
	if q.chat.Messages[1].Content != "thinking" {
		t.Fatalf("expected assistant text to be post-processed before tool calls, got: %+v", q.chat.Messages[1])
	}
	if len(q.chat.Messages[2].ToolCalls) != 2 {
		t.Fatalf("expected assistant tool-call message to contain both calls, got: %+v", q.chat.Messages[2])
	}
	if q.chat.Messages[3].Role != "tool" || q.chat.Messages[3].ToolCallID != "id1" {
		t.Fatalf("unexpected first tool output message: %+v", q.chat.Messages[3])
	}
	if q.chat.Messages[4].Role != "tool" || q.chat.Messages[4].ToolCallID != "id2" {
		t.Fatalf("unexpected second tool output message: %+v", q.chat.Messages[4])
	}
}

func TestHandleToolCalls_RunsInParallelAndPreservesOrder(t *testing.T) {
	orig := tools.Registry
	tools.Registry = tools.NewRegistry()
	defer func() { tools.Registry = orig }()

	release := make(chan struct{})
	started := make(chan string, 2)
	var active atomic.Int32
	var maxSeen atomic.Int32

	tools.Registry.Set("tool_slow_first", blockingTool{
		started: started,
		release: release,
		result:  "first-result",
		active:  &active,
		maxSeen: &maxSeen,
	})
	tools.Registry.Set("tool_slow_second", blockingTool{
		started: started,
		release: release,
		result:  "second-result",
		active:  &active,
		maxSeen: &maxSeen,
	})

	model := &recorderCompleter{}
	q := Querier[*recorderCompleter]{
		Raw:   true,
		Model: model,
		out:   &strings.Builder{},
		chat: pub_models.Chat{
			Messages: []pub_models.Message{{Role: "user", Content: "hi"}},
		},
	}

	done := make(chan error, 1)
	go func() {
		done <- q.handleToolCalls(context.Background(), []pub_models.Call{
			{ID: "id1", Name: "tool_slow_first", Inputs: &pub_models.Input{}},
			{ID: "id2", Name: "tool_slow_second", Inputs: &pub_models.Input{}},
		})
	}()

	timeout := time.After(2 * time.Second)
	for range 2 {
		select {
		case <-started:
		case <-timeout:
			t.Fatal("timeout waiting for both tools to start")
		}
	}
	if got := maxSeen.Load(); got < 2 {
		t.Fatalf("expected parallel execution, max concurrency got %d", got)
	}
	close(release)

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("handleToolCalls err: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for parallel tool calls to finish")
	}

	if len(model.chats) != 1 {
		t.Fatalf("expected exactly one model follow-up after batch, got %d", len(model.chats))
	}
	if got := len(q.chat.Messages); got != 4 {
		t.Fatalf("expected 4 persisted chat messages after follow-up reset, got %d: %+v", got, q.chat.Messages)
	}
	if got := model.chats[0].Messages[1].ToolCalls[0].ID; got != "id1" {
		t.Fatalf("expected assistant tool-call order preserved in follow-up chat, got first id %q", got)
	}
	if got := q.chat.Messages[2].ToolCallID; got != "id1" {
		t.Fatalf("expected first tool output to match first call, got %q", got)
	}
	if got := q.chat.Messages[3].ToolCallID; got != "id2" {
		t.Fatalf("expected second tool output to match second call, got %q", got)
	}
}

func TestHandleCompletion_ToolCallsEvent_PrintsParallelToolCallsBanner(t *testing.T) {
	orig := tools.Registry
	tools.Registry = tools.NewRegistry()
	defer func() { tools.Registry = orig }()

	tools.Registry.Set("tool_a", staticTool{name: "tool_a"})
	tools.Registry.Set("tool_b", staticTool{name: "tool_b"})

	out := &strings.Builder{}
	model := &recorderCompleter{}
	q := Querier[*recorderCompleter]{
		Raw:   true,
		Model: model,
		out:   out,
		chat: pub_models.Chat{
			Messages: []pub_models.Message{{Role: "user", Content: "hi"}},
		},
	}

	err := q.handleCompletion(context.Background(), models.ToolCallsEvent{Calls: []pub_models.Call{
		{ID: "id1", Name: "tool_a", Inputs: &pub_models.Input{}},
		{ID: "id2", Name: "tool_b", Inputs: &pub_models.Input{}},
	}})
	if err != nil {
		t.Fatalf("handleCompletion err: %v", err)
	}

	got := out.String()
	if !strings.Contains(got, `parallel tool calls: 2, tools: ["tool_a", "tool_b"]`) {
		t.Fatalf("expected parallel tool call banner, got: %q", got)
	}
}

func TestHandleCompletion_ToolCallsEvent_SingleCall_PrintsRegularCallWithInputs(t *testing.T) {
	orig := tools.Registry
	tools.Registry = tools.NewRegistry()
	defer func() { tools.Registry = orig }()

	tools.Registry.Set("tool_a", staticTool{name: "tool_a"})

	out := &strings.Builder{}
	model := &recorderCompleter{}
	q := Querier[*recorderCompleter]{
		Raw:   true,
		Model: model,
		out:   out,
		chat: pub_models.Chat{
			Messages: []pub_models.Message{{Role: "user", Content: "hi"}},
		},
	}

	err := q.handleCompletion(context.Background(), models.ToolCallsEvent{Calls: []pub_models.Call{
		{ID: "id1", Name: "tool_a", Inputs: &pub_models.Input{"path": ".", "long": true}},
	}})
	if err != nil {
		t.Fatalf("handleCompletion err: %v", err)
	}

	got := out.String()
	if strings.Contains(got, "parallel tool calls: 1") {
		t.Fatalf("did not expect parallel banner for single tool call, got: %q", got)
	}
	if !strings.Contains(got, "Call: 'tool_a'") {
		t.Fatalf("expected regular single-call banner, got: %q", got)
	}
	if !strings.Contains(got, "'path': '.'") {
		t.Fatalf("expected inputs to be printed, got: %q", got)
	}
	if !strings.Contains(got, "'long': 'true'") {
		t.Fatalf("expected boolean input to be printed, got: %q", got)
	}
}

func TestFormatParallelToolCallsBanner_SingleCall(t *testing.T) {
	got := formatParallelToolCallsBanner([]pub_models.Call{
		{Name: "ls", Inputs: &pub_models.Input{"path": ".", "all": true}},
	})
	if strings.Contains(got, "parallel tool calls") {
		t.Fatalf("did not expect parallel banner for single call, got: %q", got)
	}
	if !strings.Contains(got, "Call: 'ls'") {
		t.Fatalf("expected single call pretty print, got: %q", got)
	}
}

func TestFormatParallelToolCallsBanner_DebugOrderPreserved(t *testing.T) {
	got := formatParallelToolCallsBanner([]pub_models.Call{
		{Name: "tool_b"},
		{Name: "tool_a"},
		{Name: "tool_c"},
	})
	want := `parallel tool calls: 3, tools: ["tool_b", "tool_a", "tool_c"]`
	if got != want {
		t.Fatalf("unexpected banner, got %q want %q", got, want)
	}
}