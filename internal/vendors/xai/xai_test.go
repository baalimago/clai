package xai

import (
	"os"
	"testing"
)

func TestSetupConfigMapping(t *testing.T) {
	v := Default
	mt := 8192
	v.MaxTokens = &mt
	v.Temperature = 0.12
	v.TopP = 0.34
	v.Model = "xai-custom"

	t.Setenv("XAI_API_KEY", "k")
	if err := v.Setup(); err != nil {
		t.Fatalf("setup failed: %v", err)
	}
	if v.StreamCompleter.Model != v.Model {
		t.Errorf("expected Model %q, got %q", v.Model, v.StreamCompleter.Model)
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

func TestSetupSetsDefaultEnvWhenMissingXAI(t *testing.T) {
	v := Default
	t.Setenv("XAI_API_KEY", "")
	if err := v.Setup(); err != nil {
		t.Fatalf("setup failed: %v", err)
	}
	if os.Getenv("XAI_API_KEY") == "" {
		t.Fatalf("expected XAI_API_KEY to be set by Setup when missing")
	}
}
