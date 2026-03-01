package openai

import (
	"bytes"
	"io"
	"net/http"
	"testing"

	"github.com/baalimago/clai/internal/models"
	"github.com/baalimago/clai/internal/tools"
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

func TestValidateResponsesHTTPResponse_Non200IncludesBody(t *testing.T) {
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
	if got := err.Error(); got == "" || !containsAll(got, []string{"400", "nope"}) {
		t.Fatalf("expected status+body in error, got %q", got)
	}
}

func TestToolCallState_EmitCall_ToolNotFound(t *testing.T) {
	t.Parallel()

	orig := tools.Registry
	tools.Registry = tools.NewRegistry()
	t.Cleanup(func() { tools.Registry = orig })

	// Deliberately do not register tool.
	var st toolCallState
	st.callID = "call_1"
	st.toolName = "missing"
	if err := st.appendArgs("{}"); err != nil {
		t.Fatalf("appendArgs: %v", err)
	}

	out := make(chan models.CompletionEvent, 1)
	err := st.emitCall(out)
	if err == nil {
		t.Fatalf("expected error")
	}
	if got := err.Error(); got == "" || !containsAll(got, []string{"resolve tool", "tool not found"}) {
		t.Fatalf("expected resolve/tool not found context, got %q", got)
	}
}

func TestToolCallState_EmitCall_InvalidJSONArgs(t *testing.T) {
	t.Parallel()

	orig := tools.Registry
	tools.Registry = tools.NewRegistry()
	tools.Registry.Set("rg", fakeTool{spec: pub_models.Specification{Name: "rg"}})
	t.Cleanup(func() { tools.Registry = orig })

	var st toolCallState
	st.callID = "call_1"
	st.toolName = "rg"
	if err := st.appendArgs("{"); err != nil {
		t.Fatalf("appendArgs: %v", err)
	}

	out := make(chan models.CompletionEvent, 1)
	err := st.emitCall(out)
	if err == nil {
		t.Fatalf("expected error")
	}
	if got := err.Error(); got == "" || !containsAll(got, []string{"unmarshal tool args", "call_1", "rg"}) {
		t.Fatalf("expected unmarshal context, got %q", got)
	}
}

func TestHandleResponsesStreamEvent_FailedReturnsErrorMessage(t *testing.T) {
	t.Parallel()

	out := make(chan models.CompletionEvent, 1)
	var st toolCallState

	err := handleResponsesStreamEvent(out, &st, responsesStreamEvent{Type: "response.failed", Error: &responsesStreamErrBody{Message: "boom"}}, nil)
	if err == nil {
		t.Fatalf("expected error")
	}
	if err.Error() != "boom" {
		t.Fatalf("expected error %q got %q", "boom", err.Error())
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
