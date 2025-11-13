package inception

import "testing"

func TestSetupConfigMapping(t *testing.T) {
	v := Default
	fp := 0.1
	v.FrequencyPenalty = fp
	mt := 777
	v.MaxTokens = &mt
	v.Temperature = 0.9
	v.Model = "inc-custom"

	t.Setenv("INCEPTION_API_KEY", "k")
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
	if v.ToolChoice == nil || *v.ToolChoice != "auto" {
		t.Errorf("tool choice expected 'auto', got %#v", v.ToolChoice)
	}
}
