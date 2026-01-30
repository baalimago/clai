package huggingface

import "testing"

func TestSetup_ConfigMapping(t *testing.T) {
	t.Setenv(EnvAPITokenKey, "token")

	maxToks := 123
	h := HuggingFaceChat{
		Model:       "Qwen/Qwen2.5-72B-Instruct",
		MaxTokens:   &maxToks,
		Temperature: 0.25,
		TopP:        0.9,
		URL:         "https://example.com/v1/chat/completions",
	}
	if err := h.Setup(); err != nil {
		t.Fatalf("Setup() error: %v", err)
	}

	if h.StreamCompleter.URL != h.URL {
		t.Fatalf("URL mapping mismatch: got %q want %q", h.StreamCompleter.URL, h.URL)
	}
	if h.StreamCompleter.Model != h.Model {
		t.Fatalf("Model mapping mismatch: got %q want %q", h.StreamCompleter.Model, h.Model)
	}
	if h.StreamCompleter.MaxTokens == nil || *h.StreamCompleter.MaxTokens != *h.MaxTokens {
		t.Fatalf("MaxTokens mapping mismatch: got %+v want %+v", h.StreamCompleter.MaxTokens, h.MaxTokens)
	}
	if h.StreamCompleter.Temperature == nil || *h.StreamCompleter.Temperature != h.Temperature {
		t.Fatalf("Temperature mapping mismatch: got %+v want %v", h.StreamCompleter.Temperature, h.Temperature)
	}
	if h.StreamCompleter.TopP == nil || *h.StreamCompleter.TopP != h.TopP {
		t.Fatalf("TopP mapping mismatch: got %+v want %v", h.StreamCompleter.TopP, h.TopP)
	}
	if h.StreamCompleter.ToolChoice == nil || *h.StreamCompleter.ToolChoice != "auto" {
		t.Fatalf("ToolChoice mismatch: got %+v want %q", h.StreamCompleter.ToolChoice, "auto")
	}
}

func TestSetup_DefaultURL(t *testing.T) {
	t.Setenv(EnvAPITokenKey, "token")

	h := DefaultChat
	h.URL = ""
	if err := h.Setup(); err != nil {
		t.Fatalf("Setup() error: %v", err)
	}
	if h.StreamCompleter.URL != DefaultChatURL {
		t.Fatalf("default URL mismatch: got %q want %q", h.StreamCompleter.URL, DefaultChatURL)
	}
}
