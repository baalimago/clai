package ollama

import (
	"os"
	"testing"
)

func TestSetup_Default_SetsFields(t *testing.T) {
	v := Default
	t.Setenv("OLLAMA_API_KEY", "")
	if err := v.Setup(); err != nil {
		t.Fatalf("setup failed: %v", err)
	}
	if v.ToolChoice == nil {
		t.Fatalf("toolchoice nil")
	}
	if *v.ToolChoice != "auto" {
		t.Fatalf("toolchoice got %q want %q", *v.ToolChoice, "auto")
	}
	if v.StreamCompleter.Temperature == nil {
		t.Fatalf("temperature ptr nil")
	}
	if v.StreamCompleter.TopP == nil {
		t.Fatalf("top_p ptr nil")
	}
	if v.StreamCompleter.FrequencyPenalty == nil {
		t.Fatalf("freq ptr nil")
	}
	// should keep model when no prefix is present
	if v.StreamCompleter.Model != "llama3" {
		t.Fatalf("model got %q want %q",
			v.StreamCompleter.Model, "llama3")
	}
}

func TestSetup_TrimsOllamaPrefix(t *testing.T) {
	v := Default
	v.Model = "ollama:deepseek-r1:8b"
	t.Setenv("OLLAMA_API_KEY", "")
	if err := v.Setup(); err != nil {
		t.Fatalf("setup failed: %v", err)
	}
	want := "deepseek-r1:8b"
	if v.StreamCompleter.Model != want {
		t.Fatalf("model got %q want %q",
			v.StreamCompleter.Model, want)
	}
}

func TestSetup_RespectsExistingAPIKey(t *testing.T) {
	v := Default
	t.Setenv("OLLAMA_API_KEY", "some-key")
	if err := v.Setup(); err != nil {
		t.Fatalf("setup failed: %v", err)
	}
	if got := os.Getenv("OLLAMA_API_KEY"); got != "some-key" {
		t.Fatalf("api key got %q want %q", got, "some-key")
	}
}

func TestSetupConfigMapping(t *testing.T) {
	v := Default
	fp := 0.11
	v.FrequencyPenalty = fp
	mt := 123
	v.MaxTokens = &mt
	v.Temperature = 0.22
	v.TopP = 0.33
	v.Model = "llama3:custom"

	t.Setenv("OLLAMA_API_KEY", "k")
	if err := v.Setup(); err != nil {
		t.Fatalf("setup failed: %v", err)
	}
	if v.StreamCompleter.Model != v.Model {
		t.Errorf("expected Model %q, got %q", v.Model, v.StreamCompleter.Model)
	}
	if v.StreamCompleter.FrequencyPenalty == nil || *v.StreamCompleter.FrequencyPenalty != v.FrequencyPenalty {
		t.Errorf("frequency penalty not mapped, got %#v want %v", v.StreamCompleter.FrequencyPenalty, v.FrequencyPenalty)
	}
	if v.StreamCompleter.MaxTokens == nil || *v.StreamCompleter.MaxTokens != *v.MaxTokens {
		t.Errorf("max tokens not mapped, got %#v want %v", v.StreamCompleter.MaxTokens, *v.MaxTokens)
	}
	if v.StreamCompleter.Temperature == nil || *v.StreamCompleter.Temperature != v.Temperature {
		t.Errorf("temperature not mapped, got %#v want %v", v.StreamCompleter.Temperature, v.Temperature)
	}
	if v.StreamCompleter.TopP == nil || *v.StreamCompleter.TopP != v.TopP {
		t.Errorf("top_p not mapped, got %#v want %v", v.StreamCompleter.TopP, v.TopP)
	}
	if v.ToolChoice == nil || *v.ToolChoice != "auto" {
		t.Errorf("tool choice expected 'auto', got %#v", v.ToolChoice)
	}
}

func TestSetupSetsDefaultEnvWhenMissingOLLAMA(t *testing.T) {
	v := Default
	t.Setenv("OLLAMA_API_KEY", "")
	if err := v.Setup(); err != nil {
		t.Fatalf("setup failed: %v", err)
	}
	if got := os.Getenv("OLLAMA_API_KEY"); got == "" {
		t.Fatalf("expected OLLAMA_API_KEY to be set by Setup")
	}
}
