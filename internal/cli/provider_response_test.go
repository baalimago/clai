package cli

import (
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"
)

func plainBody(contentType, body string) providerStep {
	return func(w http.ResponseWriter, _ *http.Request, _ int32) {
		w.Header().Set("Content-Type", contentType)
		fmt.Fprint(w, body)
	}
}

// assertRunFailed checks a failed run in both formats: a non-zero exit, no
// reply, and one error naming want.
func assertRunFailed(t *testing.T, model, want string) {
	t.Helper()
	text := runProcess(t, "-r -cm "+model+" q hi", nil)
	if text.status != 1 || !strings.HasPrefix(text.stderr, "error: ") || strings.Count(text.stderr, "\n") != 1 || !strings.Contains(text.stderr, want) {
		t.Fatalf("text: got %+v, want exit 1 and one error naming %q", text, want)
	}
	json := runProcess(t, "-json -cm "+model+" q hi", nil)
	if json.status != 1 || slices.Contains(recordTypes(t, json.stdout), "reply") {
		t.Fatalf("json: got %+v, want exit 1 and no reply record", json)
	}
	if types := recordTypes(t, json.stderr); !slices.Contains(types, "error") || !strings.Contains(json.stderr, want) {
		t.Fatalf("json: stderr %q, want an error record naming %q", json.stderr, want)
	}
}

// TestProviderSuccessStatusWithoutCompletion pins that an HTTP 200 which
// carries no completion fails the run in text and under -json.
func TestProviderSuccessStatusWithoutCompletion(t *testing.T) {
	const noCompletion = "the provider returned no completion"
	for name, step := range map[string]providerStep{
		"html page":       plainBody("text/html", "<html><body>Captive portal login</body></html>"),
		"empty body":      plainBody("text/plain", ""),
		"non-stream json": plainBody("application/json", `{"id":"x","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"non-stream reply"},"finish_reason":"stop"}]}`),
		"no events":       func(w http.ResponseWriter, _ *http.Request, _ int32) { sse(w) },
		"unparseable sse": func(w http.ResponseWriter, _ *http.Request, _ int32) {
			sse(w, "data: {not json at all\n\n", chatFinishStop, chatDone)
		},
		"only a finish marker": func(w http.ResponseWriter, _ *http.Request, _ int32) { sse(w, chatFinishStop, chatDone) },
	} {
		t.Run("openai chat "+name, func(t *testing.T) {
			useConfigDir(t)
			scriptedProvider(t, "odd", step)
			assertRunFailed(t, "gpt-odd", noCompletion)
		})
	}
	for name, step := range map[string]providerStep{
		"empty body":      plainBody("text/plain", ""),
		"non-stream json": plainBody("application/json", `{"id":"x","object":"chat.completion","choices":[]}`),
		"no events":       func(w http.ResponseWriter, _ *http.Request, _ int32) { sse(w) },
	} {
		t.Run("anthropic "+name, func(t *testing.T) {
			useConfigDir(t)
			scriptedAnthropic(t, "odd", step)
			assertRunFailed(t, "claude-odd", noCompletion)
		})
	}
}

// TestTruncatedStreamKeepsWhatArrived pins that a stream which ends before
// its finish marker fails the run with a truncation error, after the text
// that arrived was shown and persisted.
func TestTruncatedStreamKeepsWhatArrived(t *testing.T) {
	anthropicStart := "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":1,\"output_tokens\":1}}}\n\n" +
		"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n" +
		"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"Hello wor\"}}\n\n"
	for name, tc := range map[string]struct {
		serve func(*testing.T)
		model string
	}{
		"openai chat": {model: "gpt-cut", serve: func(t *testing.T) {
			scriptedProvider(t, "cut", func(w http.ResponseWriter, _ *http.Request, _ int32) { sse(w, chatDelta("Hello wor")) })
		}},
		"anthropic": {model: "claude-cut", serve: func(t *testing.T) {
			scriptedAnthropic(t, "cut", func(w http.ResponseWriter, _ *http.Request, _ int32) { sse(w, anthropicStart) })
		}},
	} {
		t.Run(name, func(t *testing.T) {
			useConfigDir(t)
			tc.serve(t)
			got := runProcess(t, "-r -cm "+tc.model+" q hi", nil)
			if got.status != 1 || got.stdout != "Hello wor" || strings.Count(got.stderr, "the reply is truncated") != 1 {
				t.Fatalf("got %+v, want exit 1, the partial reply and one truncation error", got)
			}
			if saved := savedMessages(t); !slices.Equal(saved, []string{"user:hi", "assistant:Hello wor"}) {
				t.Fatalf("persisted %q, want the prompt and the partial reply", saved)
			}
		})
	}
}

// TestAnthropicStreamError pins that an error event inside a 200 stream is
// decoded like a non-OK response: the run fails with its status and provider
// code after the text that arrived.
func TestAnthropicStreamError(t *testing.T) {
	overloaded := "event: error\ndata: {\"type\":\"error\",\"error\":{\"type\":\"overloaded_error\",\"message\":\"Overloaded\"}}\n\n"
	text := "event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"Hello wor\"}}\n\n"
	t.Run("after text", func(t *testing.T) {
		useConfigDir(t)
		scriptedAnthropic(t, "over", func(w http.ResponseWriter, _ *http.Request, _ int32) { sse(w, text, overloaded) })
		got := runProcess(t, "-json -cm claude-over q hi", func(_ *process, deps *Deps) { deps.StepRetry.Delays = nil })
		if got.status != 1 || !strings.Contains(got.stderr, `"provider_code":"overloaded_error"`) || !strings.Contains(got.stderr, `"status":529`) {
			t.Fatalf("got %+v, want exit 1 and an error record with the provider code and status", got)
		}
		if types := recordTypes(t, got.stdout); !slices.Equal(types, []string{"reply_delta"}) {
			t.Fatalf("stdout records %v, want the one delta and no reply", types)
		}
	})
	t.Run("as the first event it is an unavailable provider", func(t *testing.T) {
		useConfigDir(t)
		requests := scriptedAnthropic(t, "over", func(w http.ResponseWriter, _ *http.Request, n int32) {
			if n == 1 {
				sse(w, overloaded)
				return
			}
			sse(w, text, "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
		})
		got := runProcess(t, "-r -cm claude-over q hi", nil)
		if got.status != 1 || !strings.Contains(got.stderr, "provider unavailable") || requests.Load() != 1 {
			t.Fatalf("got %+v after %d requests, want the provider-unavailable error", got, requests.Load())
		}
	})
}

// TestToolCallWithInvalidArguments pins that a tool call whose arguments are
// not a JSON object gets a failed tool result the model can correct, is
// shown like any failed call, and the run continues.
func TestToolCallWithInvalidArguments(t *testing.T) {
	for name, arguments := range map[string]string{"broken json": "{not json", "empty": "", "array": "[1,2]", "string": `"str"`} {
		t.Run(name, func(t *testing.T) {
			useConfigDir(t)
			requests := scriptedProvider(t, "bad", func(w http.ResponseWriter, _ *http.Request, n int32) {
				if n == 1 {
					sse(w, chatToolCall("pwd", arguments), chatFinishTools, chatDone)
					return
				}
				sse(w, chatDelta("corrected"), chatFinishStop, chatDone)
			})
			got := runProcess(t, "-json -cm gpt-bad -t pwd q hi", nil)
			if got.status != 0 || requests.Load() != 2 {
				t.Fatalf("got %+v after %d requests, want the run to continue to a second request", got, requests.Load())
			}
			all := got.stdout + got.stderr
			if !strings.Contains(all, `"type":"tool_call","name":"pwd"`) || !strings.Contains(all, `"type":"tool_result","name":"pwd","ok":false`) || !strings.Contains(all, "not a JSON object") {
				t.Fatalf("records %q, want the call and its failed result naming the problem", all)
			}
			if !strings.Contains(got.stdout, `{"type":"reply","text":"corrected"}`) {
				t.Fatalf("stdout %q, want the reply after the corrected step", got.stdout)
			}
		})
	}
}
