package novita

import (
	"os"
	"testing"
)

func TestSetupConfigMappingAndModelPrefixTrim(t *testing.T) {
	v := Default
	fp := 0.5
	v.FrequencyPenalty = fp
	mt := 321
	v.MaxTokens = &mt
	v.Temperature = 0.7
	v.TopP = 0.8
	v.Model = "novita:gryphe/some-model"

	t.Setenv("NOVITA_API_KEY", "k")
	if err := v.Setup(); err != nil {
		t.Fatalf("setup failed: %v", err)
	}
	if v.StreamCompleter.Model != "gryphe/some-model" {
		t.Errorf("expected model to be trimmed of novita: prefix, got %q", v.StreamCompleter.Model)
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

func TestSetupSetsDefaultEnvWhenMissingNOVITA(t *testing.T) {
	v := Default
	t.Setenv("NOVITA_API_KEY", "")
	if err := v.Setup(); err != nil {
		t.Fatalf("setup failed: %v", err)
	}
	if got := os.Getenv("NOVITA_API_KEY"); got == "" {
		t.Fatalf("expected NOVITA_API_KEY to be set by Setup")
	}
}
