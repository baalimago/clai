package openai

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/baalimago/clai/internal/models"
	pub_models "github.com/baalimago/clai/pkg/text/models"
)

type respErrBody struct{ err error }

func (b respErrBody) Read([]byte) (int, error) { return 0, b.err }
func (respErrBody) Close() error               { return nil }

type readCloserFunc struct {
	read  func([]byte) (int, error)
	close func() error
}

func (f readCloserFunc) Read(p []byte) (int, error) { return f.read(p) }
func (f readCloserFunc) Close() error {
	if f.close != nil {
		return f.close()
	}
	return nil
}

func contextOrDone() <-chan struct{} { return make(chan struct{}) }

func captureOpenAIStdout(t *testing.T, fn func()) string {
	t.Helper()
	original := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("create stdout pipe: %v", err)
	}
	os.Stdout = w
	fn()
	if err := w.Close(); err != nil {
		os.Stdout = original
		_ = r.Close()
		t.Fatalf("close stdout writer: %v", err)
	}
	os.Stdout = original
	defer r.Close()
	var output bytes.Buffer
	if _, err := io.Copy(&output, r); err != nil {
		t.Fatalf("read captured stdout: %v", err)
	}
	return output.String()
}

func TestResponsesToolCallStateLifecycle(t *testing.T) {
	done := contextOrDone()

	st := &toolCallState{}
	st.beginFromItem(nil)
	st.beginFromItem(&responsesOutputItem{ID: "item-1"})
	if st.callID != "item-1" {
		t.Fatalf("callID = %q, want the item.id fallback", st.callID)
	}
	if err := st.appendArgs(""); err != nil {
		t.Fatalf("appendArgs empty: %v", err)
	}

	st.callEmitted = true
	if err := st.emitCall(done, make(chan models.CompletionEvent, 1), nil); err != nil {
		t.Fatalf("already emitted should be a no-op, got %v", err)
	}

	missingName := &toolCallState{callID: "c"}
	if err := missingName.emitCall(done, make(chan models.CompletionEvent, 1), nil); err == nil {
		t.Fatal("expected an error for a call with no tool name")
	}

	missingID := &toolCallState{toolName: "t"}
	if err := missingID.emitCall(done, make(chan models.CompletionEvent, 1), nil); err == nil {
		t.Fatal("expected an error for a call with no call id")
	}
}

func TestResponsesEventCallKeyFallbacks(t *testing.T) {
	idx := 3
	if got := (responsesStreamEvent{OutputIndex: &idx}).callKey(); got != "idx:3" {
		t.Fatalf("callKey = %q, want idx:3", got)
	}
	if got := (responsesStreamEvent{Item: &responsesOutputItem{ID: "i"}}).callKey(); got != "i" {
		t.Fatalf("callKey = %q, want i", got)
	}
	if got := (responsesStreamEvent{}).callKey(); got != "" {
		t.Fatalf("callKey = %q, want empty", got)
	}
}

func TestParseResponsesLineTable(t *testing.T) {
	tests := []struct {
		name    string
		line    string
		wantOK  bool
		wantErr bool
	}{
		{"blank", "\n", false, false},
		{"non-data", "event: created\n", false, false},
		{"done sentinel", "data: [DONE]\n", true, false},
		{"empty payload", "data: \n", false, false},
		{"undecodable", "data: {bad\n", false, true},
		{"missing type", "data: {\"delta\":\"x\"}\n", false, false},
		{"valid", "data: {\"type\":\"response.created\"}\n", true, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			evt, ok, err := parseResponsesLine([]byte(tc.line))
			if ok != tc.wantOK || (err != nil) != tc.wantErr {
				t.Fatalf("parseResponsesLine(%q) = %#v, %v, %v", tc.line, evt, ok, err)
			}
		})
	}
}

func TestParseStreamLineDebug(t *testing.T) {
	s := &responsesStreamer{debug: true}
	evt, ok, err := s.parseStreamLine([]byte("data: {\"type\":\"response.created\"}\n"))
	if err != nil || !ok || evt.Type != "response.created" {
		t.Fatalf("parseStreamLine = %#v, %v, %v", evt, ok, err)
	}
}

func TestHandleResponsesStreamEventBranches(t *testing.T) {
	done := contextOrDone()

	t.Run("default is a no-op", func(t *testing.T) {
		out := make(chan models.CompletionEvent, 1)
		doneFlag, err := handleResponsesStreamEvent(done, out, newToolCallTracker(), responsesStreamEvent{Type: "response.unknown"}, nil)
		if err != nil || doneFlag {
			t.Fatalf("done=%v err=%v", doneFlag, err)
		}
		if _, ok := (<-out).(models.NoopEvent); !ok {
			t.Fatal("expected a NoopEvent for an unknown event")
		}
	})

	t.Run("failed event is a vocabulary error", func(t *testing.T) {
		out := make(chan models.CompletionEvent, 1)
		_, err := handleResponsesStreamEvent(done, out, newToolCallTracker(), responsesStreamEvent{Type: "response.failed"}, nil)
		if err == nil {
			t.Fatal("expected an error for a failed response")
		}
	})

	t.Run("usage setter failure surfaces", func(t *testing.T) {
		out := make(chan models.CompletionEvent, 1)
		evt := responsesStreamEvent{
			Type:     "response.completed",
			Response: &responsesResponse{Usage: &responsesUsage{TotalTokens: 1}},
		}
		_, err := handleResponsesStreamEvent(done, out, newToolCallTracker(), evt, func(*pub_models.Usage) error {
			return errors.New("boom")
		})
		if err == nil {
			t.Fatal("expected the usage setter error to surface")
		}
	})

	t.Run("incomplete is terminal", func(t *testing.T) {
		out := make(chan models.CompletionEvent, 1)
		doneFlag, err := handleResponsesStreamEvent(done, out, newToolCallTracker(), responsesStreamEvent{Type: "response.incomplete"}, nil)
		if err != nil || !doneFlag {
			t.Fatalf("done=%v err=%v", doneFlag, err)
		}
	})
}

func TestMaybeSetUsageAndMapUsage(t *testing.T) {
	if err := maybeSetUsage(responsesStreamEvent{}, func(*pub_models.Usage) error { return nil }); err != nil {
		t.Fatalf("nil response: %v", err)
	}
	if err := maybeSetUsage(responsesStreamEvent{Response: &responsesResponse{}}, nil); err != nil {
		t.Fatalf("nil setter: %v", err)
	}
	if err := maybeSetUsage(responsesStreamEvent{Response: &responsesResponse{Usage: &responsesUsage{}}}, func(*pub_models.Usage) error {
		return errors.New("boom")
	}); err == nil {
		t.Fatal("expected the setter error to surface")
	}

	if mapUsage(nil) != nil {
		t.Fatal("mapUsage(nil) should be nil")
	}
	mapped := mapUsage(&responsesUsage{
		InputTokens:         1,
		OutputTokens:        2,
		TotalTokens:         3,
		InputTokensDetails:  &responsesInputTokensDetails{CachedTokens: 4, AudioTokens: 5},
		OutputTokensDetails: &responsesOutputTokensDetails{ReasoningTokens: 6, AudioTokens: 7, AcceptedPredictionTokens: 8, RejectedPredictionTokens: 9},
	})
	if mapped.PromptTokens != 1 || mapped.CompletionTokens != 2 || mapped.TotalTokens != 3 {
		t.Fatalf("mapped tokens = %#v", mapped)
	}
	if mapped.PromptTokensDetails.CachedTokens != 4 || mapped.CompletionTokensDetails.ReasoningTokens != 6 {
		t.Fatalf("mapped details = %#v", mapped)
	}
}

func TestResponsesDeltaEmitters(t *testing.T) {
	done := contextOrDone()
	out := make(chan models.CompletionEvent, 2)
	if err := emitTextDelta(done, out, ""); err != nil {
		t.Fatalf("emitTextDelta: %v", err)
	}
	if err := emitReasoningDelta(done, out, ""); err != nil {
		t.Fatalf("emitReasoningDelta: %v", err)
	}
	if _, ok := (<-out).(models.NoopEvent); !ok {
		t.Fatal("expected a NoopEvent for an empty text delta")
	}
	if _, ok := (<-out).(models.NoopEvent); !ok {
		t.Fatal("expected a NoopEvent for an empty reasoning delta")
	}
}

func TestResponsesItemHandlers(t *testing.T) {
	done := contextOrDone()
	out := make(chan models.CompletionEvent, 32)
	tracker := newToolCallTracker()

	if err := handleOutputItemAdded(done, out, tracker, responsesStreamEvent{Item: nil}); err != nil {
		t.Fatalf("handleOutputItemAdded(nil): %v", err)
	}
	if err := handleOutputItemAdded(done, out, tracker, responsesStreamEvent{Item: &responsesOutputItem{Type: "message"}}); err != nil {
		t.Fatalf("handleOutputItemAdded(message): %v", err)
	}

	if err := handleFunctionCallArgumentsDelta(done, out, tracker, responsesStreamEvent{Delta: ""}); err != nil {
		t.Fatalf("handleFunctionCallArgumentsDelta(empty): %v", err)
	}

	if err := handleFunctionCallArgumentsDone(done, out, tracker, responsesStreamEvent{ItemID: "unknown"}); err == nil {
		t.Fatal("expected an error for a done event with no preceding item")
	}

	if err := handleOutputItemDone(done, out, tracker, responsesStreamEvent{Item: &responsesOutputItem{Type: "message"}}); err != nil {
		t.Fatalf("handleOutputItemDone(message): %v", err)
	}
	if err := handleOutputItemDone(done, out, tracker, responsesStreamEvent{Item: &responsesOutputItem{Type: "reasoning"}}); err != nil {
		t.Fatalf("handleOutputItemDone(reasoning without content): %v", err)
	}
	if err := handleOutputItemDone(done, out, tracker, responsesStreamEvent{Item: &responsesOutputItem{
		Type:             "reasoning",
		ID:               "r1",
		EncryptedContent: "sealed",
		Summary:          []responsesSummaryPart{{Text: "why"}, {Text: ""}},
	}}); err != nil {
		t.Fatalf("handleOutputItemDone(reasoning): %v", err)
	}
	if len(tracker.reasoningItems) != 1 || len(tracker.reasoningItems[0].Summary) != 1 {
		t.Fatalf("reasoning items = %#v", tracker.reasoningItems)
	}
}

func TestMapChatToResponsesInputErrors(t *testing.T) {
	_, err := mapChatToResponsesInput(pub_models.Chat{Messages: []pub_models.Message{{Role: "tool"}}}, false)
	if err == nil {
		t.Fatal("expected an error for a tool message without tool_call_id")
	}

	_, err = mapChatToResponsesInput(pub_models.Chat{Messages: []pub_models.Message{{
		Role:      "assistant",
		ToolCalls: []pub_models.Call{{Name: "t"}},
	}}}, false)
	if err == nil {
		t.Fatal("expected an error for an assistant tool call without an id")
	}

	_, err = mapChatToResponsesInput(pub_models.Chat{Messages: []pub_models.Message{{
		Role:      "assistant",
		ToolCalls: []pub_models.Call{{ID: "c"}},
	}}}, false)
	if err == nil {
		t.Fatal("expected an error for an assistant tool call without a name")
	}
}

func TestMapMessageToResponsesInputItemsShapes(t *testing.T) {
	items, err := mapMessageToResponsesInputItems(pub_models.Message{
		Role:      "assistant",
		ToolCalls: []pub_models.Call{{ID: "c", Function: pub_models.Specification{Name: "legacy", Arguments: "{}"}}},
	}, false)
	if err != nil {
		t.Fatalf("legacy function name: %v", err)
	}
	if len(items) != 1 || items[0].Name != "legacy" {
		t.Fatalf("items = %#v", items)
	}

	empty, err := mapMessageToResponsesInputItems(pub_models.Message{Role: "assistant"}, false)
	if err != nil {
		t.Fatalf("empty assistant: %v", err)
	}
	if len(empty) != 1 || empty[0].Type != "message" {
		t.Fatalf("empty assistant items = %#v", empty)
	}

	withReasoning, err := mapMessageToResponsesInputItems(pub_models.Message{
		Role:           "assistant",
		ReasoningItems: []pub_models.ReasoningItem{{ID: "r", EncryptedContent: "sealed", Summary: []string{"s"}}},
	}, true)
	if err != nil {
		t.Fatalf("assistant with reasoning: %v", err)
	}
	if len(withReasoning) == 0 || withReasoning[0].Type != "reasoning" {
		t.Fatalf("items = %#v", withReasoning)
	}
}

func TestMapMessageToResponsesContent(t *testing.T) {
	plain, err := mapMessageToResponsesContent(pub_models.Message{Content: "hi"})
	if err != nil || len(plain) != 1 || plain[0].Text != "hi" {
		t.Fatalf("plain = %#v, %v", plain, err)
	}

	img := &pub_models.ImageURL{URL: "data:image/png;base64,AAAA", Detail: "low"}
	parts, err := mapMessageToResponsesContent(pub_models.Message{ContentParts: []pub_models.ImageOrTextInput{
		{ImageB64: img},
		{Text: "caption"},
	}})
	if err != nil {
		t.Fatalf("content parts: %v", err)
	}
	if len(parts) != 2 || parts[0].Type != "input_image" || parts[1].Text != "caption" {
		t.Fatalf("parts = %#v", parts)
	}

	if _, err := mapMessageToResponsesContent(pub_models.Message{ContentParts: []pub_models.ImageOrTextInput{{Type: "weird"}}}); err == nil {
		t.Fatal("expected an error for an unsupported content part")
	}
}

func TestValidateResponsesHTTPResponse(t *testing.T) {
	if err := validateResponsesHTTPResponse(&http.Response{StatusCode: http.StatusOK}); err != nil {
		t.Fatalf("ok status: %v", err)
	}
	err := validateResponsesHTTPResponse(&http.Response{StatusCode: http.StatusInternalServerError, Body: respErrBody{errors.New("boom")}})
	if err == nil {
		t.Fatal("expected a transport error when the error body cannot be read")
	}
}

func TestResponsesStreamCreateRequestError(t *testing.T) {
	s := &responsesStreamer{url: "://bad", client: &http.Client{}}
	if _, err := s.stream(context.Background(), pub_models.Chat{}); err == nil {
		t.Fatal("expected the create-request error to surface")
	}
}

func TestReadResponsesStreamCanceledMidRead(t *testing.T) {
	tests := []struct {
		name  string
		first string
	}{
		{"read error", ""},
		{"parse error", "data: {bad\n"},
		{"handle event error", "data: {\"type\":\"response.failed\"}\n"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			first := true
			closed := false
			body := readCloserFunc{read: func(p []byte) (int, error) {
				if first {
					first = false
					cancel()
					if tc.first == "" {
						return 0, errors.New("boom")
					}
					return copy(p, tc.first), nil
				}
				return 0, io.EOF
			}, close: func() error {
				closed = true
				return nil
			}}

			// Unbuffered out with no consumer: once the context is cancelled by
			// the body, emitResponses takes the done branch and the reader stops.
			out := make(chan models.CompletionEvent)
			(&responsesStreamer{}).readResponsesStream(ctx, body, out)
			if !closed {
				t.Fatal("cancelled response stream did not close its body")
			}
			for evt := range out {
				t.Errorf("cancelled response stream emitted event %T", evt)
			}
		})
	}
}

func TestCreateRequestDebug(t *testing.T) {
	s := &responsesStreamer{url: "https://example.com", model: "gpt-5", debug: true}
	var req *http.Request
	output := captureOpenAIStdout(t, func() {
		var err error
		req, err = s.createRequest(context.Background(), pub_models.Chat{})
		if err != nil {
			t.Errorf("createRequest: %v", err)
		}
	})
	if req == nil {
		t.Fatal("createRequest returned no request")
	}
	for _, want := range []string{"openai responses request (tools redacted):", "https://example.com", `"model": "gpt-5"`} {
		if !strings.Contains(output, want) {
			t.Errorf("debug output does not contain %q: %s", want, output)
		}
	}
}

func TestNormalizeResponsesCallID(t *testing.T) {
	short := "call_1"
	if got := normalizeResponsesCallID(short); got != short {
		t.Fatalf("short id changed: %q", got)
	}
	long := make([]byte, responsesMaxCallIDLength+10)
	for i := range long {
		long[i] = 'a'
	}
	got := normalizeResponsesCallID(string(long))
	if len(got) != responsesMaxCallIDLength {
		t.Fatalf("long id length = %d, want %d", len(got), responsesMaxCallIDLength)
	}
}
