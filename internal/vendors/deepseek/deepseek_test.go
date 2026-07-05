package deepseek

import (
	"os"
	"testing"
)

func TestSetupConfigMapping(t *testing.T) {
	v := Default
	// customize some values to ensure mapping is from struct to embedded StreamCompleter
	tmp := 0.42
	v.FrequencyPenalty = tmp
	mt := 1234
	v.MaxTokens = &mt
	v.Temperature = 0.77
	v.TopP = 0.88
	v.Model = "deepseek-test-model"

	t.Setenv("DEEPSEEK_API_KEY", "any-key")
	if err := v.Setup(); err != nil {
		t.Fatalf("setup failed: %v", err)
	}
	// Assert fields mapped into embedded generic.StreamCompleter
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

func TestSetupSetsDefaultEnvWhenMissing(t *testing.T) {
	v := Default
	// ensure env missing
	t.Setenv("DEEPSEEK_API_KEY", "")
	if err := v.Setup(); err != nil {
		t.Fatalf("setup failed: %v", err)
	}
	if got := os.Getenv("DEEPSEEK_API_KEY"); got == "" {
		t.Fatalf("expected DEEPSEEK_API_KEY to be set, got empty")
	}
}
