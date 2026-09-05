package vendors_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/baalimago/clai/internal/vendors"
	"github.com/baalimago/clai/internal/vendors/deepseek"
	"github.com/baalimago/clai/internal/vendors/openai"
	"github.com/baalimago/clai/internal/vendors/openrouter"
	"github.com/baalimago/clai/internal/vendors/xai"
	"github.com/baalimago/clai/pkg/claierr"
	pub_models "github.com/baalimago/clai/pkg/text/models"
)

// Test_AllVendors_InsufficientCredits_OneMatcher pins the worklog's first
// definition-of-success item (2026-09-05-error-propagation): a consumer
// writes errors.Is(err, claierr.ErrLikelyInsufficientCredits) ONCE and it
// matches every vendor's spelling of an empty account — deepseek's 402,
// openrouter's 402, xai's 403, openai's 429+insufficient_quota — with no
// per-vendor matching knowledge. Each scenario drives the vendor's real
// completer against its checked-in fixture (D14 reconstructions, see each
// vendor's testdata/README.md).
func Test_AllVendors_InsufficientCredits_OneMatcher(t *testing.T) {
	chat := pub_models.Chat{Messages: []pub_models.Message{{Role: "user", Content: "hi"}}}
	serve := func(t *testing.T, status int, fixturePath string) *httptest.Server {
		t.Helper()
		body, err := os.ReadFile(filepath.FromSlash(fixturePath))
		if err != nil {
			t.Fatalf("read fixture %v: %v", fixturePath, err)
		}
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(status)
			_, _ = w.Write(body)
		}))
		t.Cleanup(srv.Close)
		return srv
	}

	cases := []struct {
		name    string
		fixture string
		status  int
		run     func(t *testing.T, url string) error
	}{
		{
			name:    "openai 429 insufficient_quota",
			fixture: "openai/testdata/insufficient_quota_429.json",
			status:  http.StatusTooManyRequests,
			run: func(t *testing.T, url string) error {
				t.Setenv("OPENAI_API_KEY", "test-key")
				g := &openai.ChatGPT{Model: "gpt-4.1-mini", URL: url + "/v1/chat/completions"}
				if err := g.Setup(); err != nil {
					t.Fatalf("setup: %v", err)
				}
				_, err := g.StreamCompletions(context.Background(), chat)
				return err
			},
		},
		{
			name:    "deepseek 402 insufficient balance",
			fixture: "deepseek/testdata/insufficient_balance_402.json",
			status:  http.StatusPaymentRequired,
			run: func(t *testing.T, url string) error {
				t.Setenv("DEEPSEEK_API_KEY", "test-key")
				v := deepseek.Default
				if err := v.Setup(); err != nil {
					t.Fatalf("setup: %v", err)
				}
				v.StreamCompleter.URL = url
				_, err := v.StreamCompletions(context.Background(), chat)
				return err
			},
		},
		{
			name:    "xai 403 drained credits",
			fixture: "xai/testdata/drained_credits_403.json",
			status:  http.StatusForbidden,
			run: func(t *testing.T, url string) error {
				t.Setenv("XAI_API_KEY", "test-key")
				v := xai.Default
				if err := v.Setup(); err != nil {
					t.Fatalf("setup: %v", err)
				}
				v.StreamCompleter.URL = url
				_, err := v.StreamCompletions(context.Background(), chat)
				return err
			},
		},
		{
			name:    "openrouter 402 credits exhausted",
			fixture: "openrouter/testdata/credits_402.json",
			status:  http.StatusPaymentRequired,
			run: func(t *testing.T, url string) error {
				t.Setenv("OPENROUTER_API_KEY", "test-key")
				o := openrouter.Default
				if err := o.Setup(); err != nil {
					t.Fatalf("setup: %v", err)
				}
				o.StreamCompleter.URL = url
				_, err := o.StreamCompletions(context.Background(), chat)
				return err
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := serve(t, tc.status, tc.fixture)
			err := tc.run(t, srv.URL)
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			// The single matcher — the whole point.
			if !errors.Is(err, claierr.ErrLikelyInsufficientCredits) {
				t.Fatalf("the one matcher missed %v's spelling: %v", tc.name, err)
			}
		})
	}
}

// TestNormalizeToolCallSequence_AdjacentMerge verifies two consecutive
// tool-call-only assistant messages are merged into one.
func TestNormalizeToolCallSequence_AdjacentMerge(t *testing.T) {
	msgs := []pub_models.Message{
		{Role: "assistant", ToolCalls: []pub_models.Call{{ID: "A", Function: pub_models.Specification{Name: "read"}}}},
		{Role: "assistant", ToolCalls: []pub_models.Call{{ID: "B", Function: pub_models.Specification{Name: "write"}}}},
	}
	got := vendors.NormalizeToolCallSequence(msgs)
	if len(got) != 1 {
		t.Fatalf("expected 1 merged message, got %d", len(got))
	}
	if len(got[0].ToolCalls) != 2 {
		t.Fatalf("expected 2 tool calls, got %d", len(got[0].ToolCalls))
	}
	if got[0].ToolCalls[0].ID != "A" {
		t.Fatalf("expected first tool call ID A, got %q", got[0].ToolCalls[0].ID)
	}
	if got[0].ToolCalls[1].ID != "B" {
		t.Fatalf("expected second tool call ID B, got %q", got[0].ToolCalls[1].ID)
	}
}

// TestNormalizeToolCallSequence_InterleavedMerge verifies interleaved
// tool-call-only assistants merge into the prior pending batch.
// Input: assistant(tool_use A), assistant(tool_use B), tool_result A,
// assistant(tool_use C), tool_result B, tool_result C
// Output: assistant(tool_calls [A, B, C]), tool_result A, tool_result B, tool_result C
func TestNormalizeToolCallSequence_InterleavedMerge(t *testing.T) {
	msgs := []pub_models.Message{
		{Role: "assistant", ToolCalls: []pub_models.Call{{ID: "A", Function: pub_models.Specification{Name: "f"}}}},
		{Role: "assistant", ToolCalls: []pub_models.Call{{ID: "B", Function: pub_models.Specification{Name: "f"}}}},
		{Role: "tool", ToolCallID: "A", Content: "result-A"},
		{Role: "assistant", ToolCalls: []pub_models.Call{{ID: "C", Function: pub_models.Specification{Name: "f"}}}},
		{Role: "tool", ToolCallID: "B", Content: "result-B"},
		{Role: "tool", ToolCallID: "C", Content: "result-C"},
	}
	got := vendors.NormalizeToolCallSequence(msgs)
	// Expected: assistant[A,B,C], tool:A, tool:B, tool:C
	if len(got) != 4 {
		t.Fatalf("expected 4 messages, got %d", len(got))
	}
	if got[0].Role != "assistant" {
		t.Fatalf("expected first msg assistant, got %q", got[0].Role)
	}
	if len(got[0].ToolCalls) != 3 {
		t.Fatalf("expected 3 tool calls in merged assistant, got %d", len(got[0].ToolCalls))
	}
	ids := []string{got[0].ToolCalls[0].ID, got[0].ToolCalls[1].ID, got[0].ToolCalls[2].ID}
	want := []string{"A", "B", "C"}
	for i, id := range ids {
		if id != want[i] {
			t.Fatalf("tool call %d: got %q, want %q", i, id, want[i])
		}
	}
}

// TestNormalizeToolCallSequence_NoMergeWhenTextSeparates verifies that when a
// text-content assistant separates two tool-call-only assistants, they are not merged.
func TestNormalizeToolCallSequence_NoMergeWhenTextSeparates(t *testing.T) {
	msgs := []pub_models.Message{
		{Role: "assistant", ToolCalls: []pub_models.Call{{ID: "A", Function: pub_models.Specification{Name: "f"}}}},
		{Role: "assistant", Content: "hello"},
		{Role: "assistant", ToolCalls: []pub_models.Call{{ID: "B", Function: pub_models.Specification{Name: "f"}}}},
	}
	got := vendors.NormalizeToolCallSequence(msgs)
	if len(got) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(got))
	}
	if got[0].Role != "assistant" || len(got[0].ToolCalls) != 1 {
		t.Fatal("expected first msg to be untouched assistant with 1 tool call")
	}
	if got[1].Role != "assistant" || got[1].Content != "hello" {
		t.Fatal("expected middle text assistant preserved")
	}
	if got[2].Role != "assistant" || len(got[2].ToolCalls) != 1 {
		t.Fatal("expected last msg to be untouched assistant with 1 tool call")
	}
}

// TestNormalizeToolCallSequence_EmptyInput returns empty output.
func TestNormalizeToolCallSequence_EmptyInput(t *testing.T) {
	got := vendors.NormalizeToolCallSequence(nil)
	if got != nil {
		t.Fatalf("expected nil, got %v", got)
	}
	got = vendors.NormalizeToolCallSequence([]pub_models.Message{})
	if len(got) != 0 {
		t.Fatalf("expected empty, got %d", len(got))
	}
}

// TestNormalizeToolCallSequence_SingleElementNoOp verifies a single-element
// slice passes through unchanged.
func TestNormalizeToolCallSequence_SingleElementNoOp(t *testing.T) {
	msgs := []pub_models.Message{
		{Role: "user", Content: "hi"},
	}
	got := vendors.NormalizeToolCallSequence(msgs)
	if len(got) != 1 {
		t.Fatalf("expected 1 msg, got %d", len(got))
	}
	if got[0].Content != "hi" {
		t.Fatalf("expected content hi, got %q", got[0].Content)
	}
}

// TestNormalizeToolCallSequence_ReasoningPreservedDuringMerge verifies that
// ReasoningContent is joined with "\n" when merging tool-call-only assistants.
func TestNormalizeToolCallSequence_ReasoningPreservedDuringMerge(t *testing.T) {
	msgs := []pub_models.Message{
		{Role: "assistant", ReasoningContent: "think A", ToolCalls: []pub_models.Call{{ID: "A", Function: pub_models.Specification{Name: "f"}}}},
		{Role: "assistant", ReasoningContent: "think B", ToolCalls: []pub_models.Call{{ID: "B", Function: pub_models.Specification{Name: "f"}}}},
	}
	got := vendors.NormalizeToolCallSequence(msgs)
	if len(got) != 1 {
		t.Fatalf("expected 1 msg, got %d", len(got))
	}
	if got[0].ReasoningContent != "think A\nthink B" {
		t.Fatalf("expected joined reasoning, got %q", got[0].ReasoningContent)
	}
}

// TestNormalizeToolCallSequence_TextFollowedByToolCallOnlyNoMerge verifies that
// an assistant with text content followed by a tool-call-only assistant does NOT
// merge. This guards the text-content boundary logic in both readers.
func TestNormalizeToolCallSequence_TextFollowedByToolCallOnlyNoMerge(t *testing.T) {
	msgs := []pub_models.Message{
		{Role: "assistant", Content: "some text reply"},
		{Role: "assistant", ToolCalls: []pub_models.Call{{ID: "T1", Function: pub_models.Specification{Name: "f"}}}},
	}
	got := vendors.NormalizeToolCallSequence(msgs)
	if len(got) != 2 {
		t.Fatalf("expected 2 messages (no merge), got %d", len(got))
	}
	if got[0].Content != "some text reply" {
		t.Fatalf("expected first assistant to keep text, got %q", got[0].Content)
	}
	if len(got[1].ToolCalls) != 1 || got[1].ToolCalls[0].ID != "T1" {
		t.Fatal("expected second assistant tool call preserved")
	}
}

// TestNormalizeToolCallSequence_ReasoningPreservedDuringInterleavedMerge verifies
// that reasoning content from an interleaved tool-call-only assistant is preserved
// when merging into a pending batch.
func TestNormalizeToolCallSequence_ReasoningPreservedDuringInterleavedMerge(t *testing.T) {
	msgs := []pub_models.Message{
		{Role: "assistant", ReasoningContent: "plan A", ToolCalls: []pub_models.Call{{ID: "A", Function: pub_models.Specification{Name: "f"}}}},
		{Role: "assistant", ReasoningContent: "plan B", ToolCalls: []pub_models.Call{{ID: "B", Function: pub_models.Specification{Name: "f"}}}},
		{Role: "tool", ToolCallID: "A", Content: "result-A"},
		{Role: "assistant", ReasoningContent: "plan C", ToolCalls: []pub_models.Call{{ID: "C", Function: pub_models.Specification{Name: "f"}}}},
		{Role: "tool", ToolCallID: "B", Content: "result-B"},
		{Role: "tool", ToolCallID: "C", Content: "result-C"},
	}
	got := vendors.NormalizeToolCallSequence(msgs)
	if len(got) != 4 {
		t.Fatalf("expected 4 messages, got %d", len(got))
	}
	if got[0].ReasoningContent != "plan A\nplan B\nplan C" {
		t.Fatalf("expected joined reasoning, got %q", got[0].ReasoningContent)
	}
}
