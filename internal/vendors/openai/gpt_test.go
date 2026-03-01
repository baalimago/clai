package openai

import "testing"

func TestSetupConfigMapping(t *testing.T) {
	v := GptDefault
	v.Model = "gpt-custom"
	fp := 0.21
	v.FrequencyPenalty = fp
	mt := 4096
	v.MaxTokens = &mt
	v.Temperature = 0.55
	v.TopP = 0.66

	t.Setenv("OPENAI_API_KEY", "key")
	if err := v.Setup(); err != nil {
		t.Fatalf("setup failed: %v", err)
	}

	if v.apiKey != "key" {
		t.Fatalf("api key: got %q want %q", v.apiKey, "key")
	}
	if v.Model != "gpt-custom" {
		t.Fatalf("model: got %q want %q", v.Model, "gpt-custom")
	}
	if v.URL == "" {
		t.Fatalf("url not set")
	}
}
