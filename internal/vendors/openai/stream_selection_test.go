package openai

import "testing"

func TestSelectOpenAIURL_ResponsesIsDefault(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name         string
		model        string
		currentURL   string
		wantURL      string
		wantResponse bool
	}{
		{
			name:         "empty url defaults to responses",
			model:        "gpt-5.2",
			currentURL:   "",
			wantURL:      ResponsesURL,
			wantResponse: true,
		},
		{
			name:         "non-reasoning empty url defaults to responses",
			model:        "gpt-4.1-mini",
			currentURL:   "",
			wantURL:      ResponsesURL,
			wantResponse: true,
		},
		{
			name:         "explicit responses url stays responses",
			model:        "gpt-4.1-mini",
			currentURL:   ResponsesURL,
			wantURL:      ResponsesURL,
			wantResponse: true,
		},
		{
			name:         "explicit chat completions url opts back to legacy",
			model:        "gpt-4.1-mini",
			currentURL:   ChatURL,
			wantURL:      ChatURL,
			wantResponse: false,
		},
		{
			name:         "codex ignores chat completions opt-out and uses responses",
			model:        "gpt-4.1-codex",
			currentURL:   ChatURL,
			wantURL:      ResponsesURL,
			wantResponse: true,
		},
		{
			name:         "bare canonical openai host defaults to responses",
			model:        "gpt-4.1-mini",
			currentURL:   "https://api.openai.com",
			wantURL:      ResponsesURL,
			wantResponse: true,
		},
		{
			name:         "canonical v1 base path is normalized to responses",
			model:        "gpt-4.1-mini",
			currentURL:   "https://api.openai.com/v1/",
			wantURL:      ResponsesURL,
			wantResponse: true,
		},
		{
			name:         "bare custom proxy host stays on legacy chat completions",
			model:        "gpt-4.1-mini",
			currentURL:   "https://proxy.internal",
			wantURL:      "https://proxy.internal/v1/chat/completions",
			wantResponse: false,
		},
		{
			name:         "custom proxy naming responses path uses responses",
			model:        "gpt-4.1-mini",
			currentURL:   "https://proxy.internal/v1/responses",
			wantURL:      "https://proxy.internal/v1/responses",
			wantResponse: true,
		},
		{
			name:         "proxy chat completions path opts back to legacy",
			model:        "gpt-4.1-mini",
			currentURL:   "https://proxy.internal/v1/chat/completions",
			wantURL:      "https://proxy.internal/v1/chat/completions",
			wantResponse: false,
		},
		{
			name:         "codex on proxy chat completions path is redirected to responses",
			model:        "gpt-4.1-codex",
			currentURL:   "https://proxy.internal/v1/chat/completions",
			wantURL:      "https://proxy.internal/v1/responses",
			wantResponse: true,
		},
		{
			name:         "codex on bare custom host is normalized to responses",
			model:        "gpt-4.1-codex",
			currentURL:   "https://proxy.internal",
			wantURL:      "https://proxy.internal/v1/responses",
			wantResponse: true,
		},
		{
			name:         "unknown custom proxy path stays on legacy (no forced migration)",
			model:        "gpt-4.1-mini",
			currentURL:   "https://proxy.internal/custom/route",
			wantURL:      "https://proxy.internal/custom/route",
			wantResponse: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			gotURL, gotResponse := selectOpenAIURL(tc.model, tc.currentURL)
			if gotURL != tc.wantURL {
				t.Fatalf("url: got %q want %q", gotURL, tc.wantURL)
			}
			if gotResponse != tc.wantResponse {
				t.Fatalf("useResponses: got %v want %v", gotResponse, tc.wantResponse)
			}
		})
	}
}

func TestIsReasoningModel(t *testing.T) {
	t.Parallel()

	cases := map[string]bool{
		"gpt-5.2":           true,
		"gpt-5":             true,
		"gpt-5-chat-latest": false,
		"gpt-4.1-codex":     true,
		"o1-preview":        true,
		"o3-mini":           true,
		"o4-mini":           true,
		"gpt-4.1-mini":      false,
		"gpt-4o":            false,
		"gpt-4-turbo":       false,
		// Provider-qualified and fine-tuned names must normalize before matching,
		// otherwise the HasPrefix(o-series) checks miss them and sampling params are
		// wrongly forwarded (the model then rejects the request).
		"openai/o3-mini":             true,
		"openai/ft:o3-mini:acme::id": true,
		"openai/gpt-5.2":             true,
		"openai/gpt-5-chat-latest":   false,
		"openai/gpt-4.1-mini":        false,
		"ft:o3-mini:acme::abc123":    true,
		"ft:o4-mini:acme::abc123":    true,
		"ft:gpt-4o-mini:acme::abc":   false,
		"ft:gpt-4.1-codex:acme::ab":  true,
	}

	for model, want := range cases {
		if got := isReasoningModel(model); got != want {
			t.Fatalf("isReasoningModel(%q): got %v want %v", model, got, want)
		}
	}
}
