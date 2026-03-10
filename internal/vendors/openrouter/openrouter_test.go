package openrouter

import "testing"

func TestSetupConfigMapping(t *testing.T) {
	v := Default
	v.Model = "or:openai/gpt-5.2"
	fp := 0.21
	v.FrequencyPenalty = fp
	mt := 4096
	v.MaxTokens = &mt
	v.PresencePenalty = 0.11
	v.Temperature = 0.55
	v.TopP = 0.66

	t.Setenv("OPENROUTER_API_KEY", "key")
	if err := v.Setup(); err != nil {
		t.Fatalf("setup failed: %v", err)
	}

	if v.StreamCompleter.Model != "openai/gpt-5.2" {
		t.Fatalf("model: got %q want %q", v.StreamCompleter.Model, "openai/gpt-5.2")
	}
	if v.StreamCompleter.ExtraHeaders["HTTP-Referer"] != "clai" {
		t.Fatalf("http-referer header mismatch: got %q want %q", v.StreamCompleter.ExtraHeaders["HTTP-Referer"], "clai")
	}
	if v.StreamCompleter.ExtraHeaders["X-OpenRouter-Title"] != "clai" {
		t.Fatalf("x-openrouter-title header mismatch: got %q want %q", v.StreamCompleter.ExtraHeaders["X-OpenRouter-Title"], "clai")
	}
}
