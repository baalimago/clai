package openai

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/baalimago/clai/internal/models"
	"github.com/baalimago/clai/internal/tools"
	"github.com/baalimago/clai/pkg/claierr"
	pub_models "github.com/baalimago/clai/pkg/text/models"
)

func TestHandleResponsesStreamEvent_TopLevelErrorSurfacesTyped(t *testing.T) {
	t.Parallel()

	out := make(chan models.CompletionEvent, 1)
	tracker := newToolCallTracker()

	raw := []byte(`{"type":"error","code":"server_error","message":"boom"}`)
	done, err := handleResponsesStreamEvent(nil, out, tracker, responsesStreamEvent{
		Type:    "error",
		Code:    "server_error",
		Message: "boom",
		raw:     raw,
	}, nil)
	if err == nil {
		t.Fatalf("expected error")
	}
	if done {
		t.Fatalf("error event should not report done")
	}
	// The event is unrecognized by the openai decoder, so it degrades to the
	// typed catch-all with the whole frame as facts (worklog
	// 2026-09-05-error-propagation, phase 5).
	if !errors.Is(err, claierr.ErrUnexpectedProviderResponse) {
		t.Fatalf("expected ErrUnexpectedProviderResponse, got: %v", err)
	}
	var apiErr claierr.APIErrorer
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected facts, got: %T %v", err, err)
	}
	if apiErr.API().StatusCode != http.StatusOK || !strings.Contains(apiErr.API().Body, "boom") {
		t.Fatalf("frame facts mismatch: %+v", apiErr.API())
	}
}

// TestResponsesStreamer_TopLevelErrorEventSurfaced verifies a mid-stream error frame
// aborts the query with the API message instead of ending on a silent EOF.
func TestResponsesStreamer_TopLevelErrorEventSurfaced(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"partial\"}\n\n")
		_, _ = io.WriteString(w, "data: {\"type\":\"error\",\"code\":\"server_error\",\"message\":\"upstream exploded\"}\n\n")
		// Connection closes with no response.completed / [DONE].
	}))
	t.Cleanup(srv.Close)

	s := &responsesStreamer{apiKey: "k", url: srv.URL + "/v1/responses", model: "gpt-test", client: srv.Client()}
	ch, err := s.stream(context.Background(), pub_models.Chat{Messages: []pub_models.Message{{Role: "user", Content: "hi"}}})
	if err != nil {
		t.Fatalf("stream: %v", err)
	}

	var gotErr error
	var gotStop bool
	for ev := range ch {
		switch v := ev.(type) {
		case error:
			gotErr = v
		case models.StopEvent:
			gotStop = true
		}
	}
	if gotErr == nil {
		t.Fatalf("expected the stream error to be surfaced")
	}
	// Typed since phase 5 (worklog 2026-09-05-error-propagation): the frame
	// rides the decode chain, so the message travels as facts, not wording.
	if !errors.Is(gotErr, claierr.ErrUnexpectedProviderResponse) {
		t.Fatalf("expected ErrUnexpectedProviderResponse, got: %v", gotErr)
	}
	var apiErr claierr.APIErrorer
	if !errors.As(gotErr, &apiErr) {
		t.Fatalf("expected facts, got: %T %v", gotErr, gotErr)
	}
	if !strings.Contains(apiErr.API().Body, "upstream exploded") {
		t.Fatalf("expected API message in facts, got %+v", apiErr.API())
	}
	if gotStop {
		t.Fatalf("a failed stream must not also emit a StopEvent")
	}
}

// TestToolCallState_EmitCall_EmptyArgsDefaultsToEmptyObject covers a zero-argument
// tool call that arrives with no argument deltas: the buffer is empty and must be
// treated as "{}" rather than failing to unmarshal.
func TestToolCallState_EmitCall_EmptyArgsDefaultsToEmptyObject(t *testing.T) {
	t.Parallel()

	tools.WithTestRegistry(t, func() {
		tools.Registry.Set("pwd", fakeTool{spec: pub_models.Specification{Name: "pwd"}})

		var st toolCallState
		st.callID = "call_1"
		st.toolName = "pwd"

		out := make(chan models.CompletionEvent, 1)
		if err := st.emitCall(nil, out, nil); err != nil {
			t.Fatalf("emitCall: %v", err)
		}
		ev := <-out
		call, ok := ev.(pub_models.Call)
		if !ok {
			t.Fatalf("expected Call, got %T", ev)
		}
		if call.Function.Arguments != "{}" {
			t.Fatalf("arguments: got %q want {}", call.Function.Arguments)
		}
		if call.Inputs == nil || len(*call.Inputs) != 0 {
			t.Fatalf("inputs: got %#v want empty", call.Inputs)
		}
	})
}
