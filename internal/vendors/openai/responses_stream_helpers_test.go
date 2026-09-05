package openai

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"testing"

	"github.com/baalimago/clai/internal/models"
	"github.com/baalimago/clai/internal/tools"
	"github.com/baalimago/clai/pkg/claierr"
	pub_models "github.com/baalimago/clai/pkg/text/models"
)

func TestParseResponsesLine_Done(t *testing.T) {
	t.Parallel()

	evt, ok, err := parseResponsesLine([]byte("data: [DONE]\n"))
	if err != nil {
		t.Fatalf("parseResponsesLine: %v", err)
	}
	if !ok {
		t.Fatalf("expected ok")
	}
	if evt.Type != "response.completed" {
		t.Fatalf("type: got %q want %q", evt.Type, "response.completed")
	}
}

func TestParseResponsesLine_IgnoresNonDataLines(t *testing.T) {
	t.Parallel()

	_, ok, err := parseResponsesLine([]byte("event: ping\n"))
	if err != nil {
		t.Fatalf("parseResponsesLine: %v", err)
	}
	if ok {
		t.Fatalf("expected not ok")
	}
}

func TestValidateResponsesHTTPResponse_Non200IsTypedWithBodyFacts(t *testing.T) {
	t.Parallel()

	res := &http.Response{
		StatusCode: http.StatusBadRequest,
		Status:     "400 Bad Request",
		Body:       io.NopCloser(bytes.NewBufferString("nope")),
	}

	err := validateResponsesHTTPResponse(res)
	if err == nil {
		t.Fatalf("expected error")
	}
	// Typed since phase 5 (worklog 2026-09-05-error-propagation): a 400 with
	// no vendor meaning degrades to the catch-all, status and body as facts.
	if !errors.Is(err, claierr.ErrUnexpectedProviderResponse) {
		t.Fatalf("expected ErrUnexpectedProviderResponse, got: %v", err)
	}
	var apiErr claierr.APIErrorer
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected facts, got: %T %v", err, err)
	}
	if apiErr.API().StatusCode != http.StatusBadRequest || apiErr.API().Body != "nope" {
		t.Fatalf("facts mismatch: %+v", apiErr.API())
	}
}

// TestToolCallState_EmitCall_UnregisteredToolStillEmits proves that a tool name
// absent from the global registry does NOT abort the stream. Internally-dispatched
// tools (lookback search_conversations/inspect_conversation/read_message, load_skill)
// and any toolBox-only custom tool are advertised to the model but never live in the
// global registry, so ToolFromName can't resolve them. The call must still be emitted
// with its name and arguments preserved so the executor can dispatch by Call.Name
// (mirroring the Chat Completions path). This is the regression guard for the
// "resolve tool from name \"search_conversations\": tool not found" abort.
func TestToolCallState_EmitCall_UnregisteredToolStillEmits(t *testing.T) {
	t.Parallel()

	tools.WithTestRegistry(t, func() {
		// Deliberately do not register the tool: emulate a lookback tool that lives
		// on the querier toolBox but not the global registry.
		var st toolCallState
		st.callID = "call_1"
		st.toolName = string(pub_models.SearchConversationsTool)
		if err := st.appendArgs(`{"query":"needle"}`); err != nil {
			t.Fatalf("appendArgs: %v", err)
		}

		out := make(chan models.CompletionEvent, 1)
		if err := st.emitCall(nil, out, nil); err != nil {
			t.Fatalf("emitCall must tolerate an unregistered tool, got error: %v", err)
		}

		ev := <-out
		call, ok := ev.(pub_models.Call)
		if !ok {
			t.Fatalf("expected a pub_models.Call, got %T (%v)", ev, ev)
		}
		if call.Name != string(pub_models.SearchConversationsTool) {
			t.Fatalf("call name: got %q want %q", call.Name, pub_models.SearchConversationsTool)
		}
		if call.ID != "call_1" {
			t.Fatalf("call id: got %q want %q", call.ID, "call_1")
		}
		if got := call.Function.Arguments; got != `{"query":"needle"}` {
			t.Fatalf("call args: got %q want %q", got, `{"query":"needle"}`)
		}
		if call.Inputs == nil || (*call.Inputs)["query"] != "needle" {
			t.Fatalf("expected parsed inputs to carry query=needle, got %#v", call.Inputs)
		}
	})
}

func TestToolCallState_EmitCall_InvalidJSONArgs(t *testing.T) {
	t.Parallel()

	tools.WithTestRegistry(t, func() {
		tools.Registry.Set("rg", fakeTool{spec: pub_models.Specification{Name: "rg"}})

		var st toolCallState
		st.callID = "call_1"
		st.toolName = "rg"
		if err := st.appendArgs("{"); err != nil {
			t.Fatalf("appendArgs: %v", err)
		}

		out := make(chan models.CompletionEvent, 1)
		err := st.emitCall(nil, out, nil)
		if err == nil {
			t.Fatalf("expected error")
		}
		if got := err.Error(); got == "" || !containsAll(got, []string{"unmarshal tool args", "call_1", "rg"}) {
			t.Fatalf("expected unmarshal context, got %q", got)
		}
	})
}

func TestHandleResponsesStreamEvent_FailedReturnsTypedError(t *testing.T) {
	t.Parallel()

	out := make(chan models.CompletionEvent, 1)
	tracker := newToolCallTracker()

	raw := []byte(`{"type":"response.failed","error":{"message":"boom"}}`)
	done, err := handleResponsesStreamEvent(nil, out, tracker, responsesStreamEvent{Type: "response.failed", Error: &responsesStreamErrBody{Message: "boom"}, raw: raw}, nil)
	if err == nil {
		t.Fatalf("expected error")
	}
	if done {
		t.Fatalf("failed event should not report done")
	}
	// Unrecognized failure: typed catch-all, frame as facts (phase 5).
	if !errors.Is(err, claierr.ErrUnexpectedProviderResponse) {
		t.Fatalf("expected ErrUnexpectedProviderResponse, got: %v", err)
	}
	var apiErr claierr.APIErrorer
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected facts, got: %T %v", err, err)
	}
	if !bytes.Contains([]byte(apiErr.API().Body), []byte("boom")) {
		t.Fatalf("expected message in facts, got %+v", apiErr.API())
	}
}

func TestHandleResponsesStreamEvent_FailedDecodesNestedResponseError(t *testing.T) {
	t.Parallel()

	out := make(chan models.CompletionEvent, 1)
	tracker := newToolCallTracker()

	// response.failed carries the actionable error nested under
	// response.error; the decoder must read that nested shape, so a quota
	// exhaustion reported this way still speaks the vocabulary.
	raw := []byte(`{"type":"response.failed","response":{"error":{"message":"nested quota out","code":"insufficient_quota"}}}`)
	evt := responsesStreamEvent{
		Type:     "response.failed",
		Response: &responsesResponse{Error: &responsesStreamErrBody{Message: "nested quota out", Code: "insufficient_quota"}},
		raw:      raw,
	}
	done, err := handleResponsesStreamEvent(nil, out, tracker, evt, nil)
	if err == nil {
		t.Fatalf("expected error")
	}
	if done {
		t.Fatalf("failed event should not report done")
	}
	if !errors.Is(err, claierr.ErrLikelyInsufficientCredits) {
		t.Fatalf("expected ErrLikelyInsufficientCredits from the nested shape, got: %v", err)
	}
}

func TestHandleResponsesStreamEvent_CompletedReportsDone(t *testing.T) {
	t.Parallel()

	out := make(chan models.CompletionEvent, 1)
	tracker := newToolCallTracker()

	done, err := handleResponsesStreamEvent(nil, out, tracker, responsesStreamEvent{Type: "response.completed"}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !done {
		t.Fatalf("completed event should report done")
	}
	if _, ok := (<-out).(models.StopEvent); !ok {
		t.Fatalf("expected StopEvent to be emitted")
	}
}

func TestHandleResponsesStreamEvent_IncompleteReportsDoneAndUsage(t *testing.T) {
	t.Parallel()

	out := make(chan models.CompletionEvent, 1)
	tracker := newToolCallTracker()

	var captured *pub_models.Usage
	usageSetter := func(u *pub_models.Usage) error {
		captured = u
		return nil
	}
	evt := responsesStreamEvent{
		Type:     "response.incomplete",
		Response: &responsesResponse{Usage: &responsesUsage{InputTokens: 10, OutputTokens: 20, TotalTokens: 30}},
	}

	done, err := handleResponsesStreamEvent(nil, out, tracker, evt, usageSetter)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !done {
		t.Fatalf("incomplete event should report done")
	}
	if _, ok := (<-out).(models.StopEvent); !ok {
		t.Fatalf("expected StopEvent to be emitted")
	}
	if captured == nil || captured.TotalTokens != 30 {
		t.Fatalf("expected usage captured on incomplete, got %#v", captured)
	}
}

func containsAll(s string, subs []string) bool {
	for _, sub := range subs {
		if !bytes.Contains([]byte(s), []byte(sub)) {
			return false
		}
	}
	return true
}
