package jev

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/baalimago/clai/internal/models"
	"github.com/baalimago/clai/internal/text/generic"
	pub_models "github.com/baalimago/clai/pkg/text/models"
)

func TestSetupRequiresAPIKey(t *testing.T) {
	t.Setenv("TYPESAFE_API_KEY", "")
	if err := Default.Setup(); err == nil || !strings.Contains(err.Error(), "TYPESAFE_API_KEY") {
		t.Fatalf("Setup error = %v, want missing key error", err)
	}
}

func TestSetupConfig(t *testing.T) {
	t.Setenv("TYPESAFE_API_KEY", "test-key")
	v := Default
	if err := v.Setup(); err != nil {
		t.Fatalf("Setup: %v", err)
	}
	if v.Model != "jev-latest" || v.URL != SystemOneURL {
		t.Fatalf("configured model/url = %q/%q", v.Model, v.URL)
	}
}

func TestStreamCompletionsSendsQuestionsAndOnlyUserState(t *testing.T) {
	t.Setenv("TYPESAFE_API_KEY", "secret")
	var gotRequest systemOneRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/systemone" {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer secret" {
			t.Errorf("Authorization = %q", got)
		}
		if got := r.Header.Get("Content-Type"); got != "application/json" {
			t.Errorf("Content-Type = %q", got)
		}
		if err := json.NewDecoder(r.Body).Decode(&gotRequest); err != nil {
			t.Errorf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"model":"jev-1.13.0","answers":{"urgent":{"type":"noul","noul":0.9}},"usage":{"input_tokens":20,"output_tokens":4}}`)
	}))
	t.Cleanup(server.Close)

	v := Default
	if err := v.Setup(); err != nil {
		t.Fatalf("Setup: %v", err)
	}
	v.URL = server.URL + "/v1/systemone"
	v.client = server.Client()
	v.SetResponseFormat(&generic.ResponseFormat{
		Type: "json_schema",
		JSONSchema: &generic.JSONSchemaSpec{
			Schema: map[string]any{
				"urgent": map[string]any{
					"type": "noul", "instructions": "Does this message express urgency?",
				},
			},
		},
	})

	chat := pub_models.Chat{Messages: []pub_models.Message{
		{Role: "system", Content: "must not reach Jev"},
		{Role: "user", Content: "first user state"},
		{Role: "assistant", Content: "must not reach Jev"},
		{Role: "user", Content: "second user state"},
	}}
	events, err := v.StreamCompletions(context.Background(), chat)
	if err != nil {
		t.Fatalf("StreamCompletions: %v", err)
	}
	var got []models.CompletionEvent
	for event := range events {
		got = append(got, event)
	}
	if len(got) != 2 {
		t.Fatalf("events = %#v, want response and stop", got)
	}
	body, ok := got[0].(string)
	if !ok || !strings.Contains(body, `"answers"`) {
		t.Fatalf("response event = %#v", got[0])
	}
	if _, ok := got[1].(models.StopEvent); !ok {
		t.Fatalf("terminal event = %#v, want StopEvent", got[1])
	}
	if gotRequest.Model != "jev-latest" {
		t.Errorf("model = %q", gotRequest.Model)
	}
	state, ok := gotRequest.State.([]any)
	if !ok || len(state) != 2 || state[0] != "first user state" || state[1] != "second user state" {
		t.Errorf("state = %#v, want only user messages", gotRequest.State)
	}
	if len(gotRequest.Questions) != 1 || gotRequest.Questions["urgent"] == nil {
		t.Errorf("questions = %#v", gotRequest.Questions)
	}
	usage := v.TokenUsage()
	if usage == nil || usage.PromptTokens != 20 || usage.CompletionTokens != 4 || usage.TotalTokens != 24 {
		t.Errorf("usage = %#v", usage)
	}
}

func TestStreamCompletionsRequiresQuestionSchema(t *testing.T) {
	t.Setenv("TYPESAFE_API_KEY", "key")
	v := Default
	if err := v.Setup(); err != nil {
		t.Fatalf("Setup: %v", err)
	}
	tests := []struct {
		name   string
		format *generic.ResponseFormat
		want   string
	}{
		{name: "missing format", want: "response format"},
		{name: "wrong format type", format: &generic.ResponseFormat{Type: "json_object"}, want: "json_schema"},
		{name: "missing schema", format: &generic.ResponseFormat{Type: "json_schema"}, want: "questions"},
		{name: "empty questions", format: &generic.ResponseFormat{Type: "json_schema", JSONSchema: &generic.JSONSchemaSpec{Schema: map[string]any{}}}, want: "at least one question"},
		{name: "unknown question type", format: formatWith(map[string]any{"q": map[string]any{"type": "freeform", "instructions": "Question?"}}), want: "unsupported question type"},
		{name: "question not object", format: formatWith(map[string]any{"q": "question"}), want: "must be an object"},
		{name: "missing type", format: formatWith(map[string]any{"q": map[string]any{"instructions": "Question?"}}), want: "missing type"},
		{name: "missing instructions", format: formatWith(map[string]any{"q": map[string]any{"type": "noul"}}), want: "instructions"},
		{name: "choice criteria absent", format: formatWith(map[string]any{"q": map[string]any{"type": "choice", "instructions": "Choose"}}), want: "criteria"},
		{name: "score criteria absent", format: formatWith(map[string]any{"q": map[string]any{"type": "score", "instructions": "Rate"}}), want: "criteria"},
		{name: "bad choice criteria", format: formatWith(map[string]any{"q": map[string]any{"type": "choice", "instructions": "Choose", "criteria": "one"}}), want: "criteria must be a non-empty object"},
		{name: "bad score criteria", format: formatWith(map[string]any{"q": map[string]any{"type": "score", "instructions": "Rate", "criteria": []any{"only one"}}}), want: "at least two"},
		{name: "noul criteria must be object", format: formatWith(map[string]any{"q": map[string]any{"type": "noul", "instructions": "Question?", "criteria": []any{"yes"}}}), want: "noul question \"q\" criteria must be an object"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v.SetResponseFormat(tt.format)
			_, err := v.StreamCompletions(context.Background(), pub_models.Chat{Messages: []pub_models.Message{{Role: "user", Content: "hello"}}})
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want substring %q", err, tt.want)
			}
		})
	}
}

func TestNoulQuestionMayOmitCriteria(t *testing.T) {
	questions, err := questionsFromResponseFormat(formatWith(map[string]any{
		"q": map[string]any{"type": "noul", "instructions": "Is this urgent?"},
	}))
	if err != nil {
		t.Fatalf("questionsFromResponseFormat: %v", err)
	}
	if len(questions) != 1 {
		t.Fatalf("questions = %#v", questions)
	}
}

func TestStreamCompletionsRequiresUserState(t *testing.T) {
	t.Setenv("TYPESAFE_API_KEY", "key")
	v := Default
	if err := v.Setup(); err != nil {
		t.Fatalf("Setup: %v", err)
	}
	v.SetResponseFormat(formatWith(map[string]any{"q": map[string]any{"type": "noul", "instructions": "Question?"}}))
	_, err := v.StreamCompletions(context.Background(), pub_models.Chat{Messages: []pub_models.Message{{Role: "system", Content: "system only"}}})
	if err == nil || !strings.Contains(err.Error(), "user message") {
		t.Fatalf("error = %v, want missing user message", err)
	}
}

func TestStateFromChatTextPartsAndRejectsImages(t *testing.T) {
	state, err := stateFromChat(pub_models.Chat{Messages: []pub_models.Message{{
		Role:         "user",
		ContentParts: []pub_models.ImageOrTextInput{{Type: "text", Text: "ticket text"}},
	}}})
	if err != nil || state != "ticket text" {
		t.Fatalf("stateFromChat text parts = %#v, %v", state, err)
	}
	_, err = stateFromChat(pub_models.Chat{Messages: []pub_models.Message{{
		Role:         "user",
		ContentParts: []pub_models.ImageOrTextInput{{Type: string(pub_models.Image)}},
	}}})
	if err == nil || !strings.Contains(err.Error(), "unsupported non-text content") {
		t.Fatalf("image error = %v", err)
	}
	_, err = stateFromChat(pub_models.Chat{Messages: []pub_models.Message{{Role: "user", Content: "  "}}})
	if err == nil || !strings.Contains(err.Error(), "non-empty user message") {
		t.Fatalf("empty state error = %v", err)
	}
}

func TestStreamCompletionsReturnsProviderAndDecodeErrors(t *testing.T) {
	t.Setenv("TYPESAFE_API_KEY", "key")
	for _, tc := range []struct {
		name   string
		status int
		body   string
		want   string
	}{
		{name: "provider status", status: http.StatusUnprocessableEntity, body: `{"error":{"message":"bad question"}}`, want: "422"},
		{name: "invalid response", status: http.StatusOK, body: `{`, want: "decode"},
		{name: "missing model", status: http.StatusOK, body: `{"answers":{"q":{"type":"noul","noul":1}},"usage":{"input_tokens":1,"output_tokens":1}}`, want: "missing model"},
		{name: "missing answers", status: http.StatusOK, body: `{"model":"jev","answers":{},"usage":{"input_tokens":1,"output_tokens":1}}`, want: "missing answers"},
		{name: "missing usage", status: http.StatusOK, body: `{"model":"jev","answers":{"q":{"type":"noul","noul":1}}}`, want: "usage"},
		{name: "incomplete usage", status: http.StatusOK, body: `{"model":"jev","answers":{"q":{"type":"noul","noul":1}},"usage":{"input_tokens":1}}`, want: "missing input_tokens or output_tokens"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, tc.body)
			}))
			defer server.Close()
			v := Default
			if err := v.Setup(); err != nil {
				t.Fatalf("Setup: %v", err)
			}
			v.URL = server.URL
			v.client = server.Client()
			v.SetResponseFormat(formatWith(map[string]any{"q": map[string]any{"type": "noul", "instructions": "Question?"}}))
			_, err := v.StreamCompletions(context.Background(), pub_models.Chat{Messages: []pub_models.Message{{Role: "user", Content: "hello"}}})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want substring %q", err, tc.want)
			}
		})
	}
}

func TestStreamCompletionsPreservesContextError(t *testing.T) {
	t.Setenv("TYPESAFE_API_KEY", "key")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer server.Close()
	v := Default
	if err := v.Setup(); err != nil {
		t.Fatalf("Setup: %v", err)
	}
	v.URL = server.URL
	v.client = server.Client()
	v.SetResponseFormat(formatWith(map[string]any{"q": map[string]any{"type": "noul", "instructions": "Question?"}}))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := v.StreamCompletions(ctx, pub_models.Chat{Messages: []pub_models.Message{{Role: "user", Content: "hello"}}})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
}

func formatWith(questions map[string]any) *generic.ResponseFormat {
	return &generic.ResponseFormat{Type: "json_schema", JSONSchema: &generic.JSONSchemaSpec{Schema: questions}}
}
