package gemini

import "testing"

func TestSetupConfigMapping(t *testing.T) {
	v := Default
	mt := 2048
	v.MaxTokens = &mt
	v.Temperature = 0.33
	v.TopP = 0.44
	v.Model = "gemini-custom"

	t.Setenv("GEMINI_API_KEY", "key")
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

func TestSetupSetsDefaultEnvWhenMissing(t *testing.T) {
	v := Default
	// ensure env missing
	t.Setenv("GEMINI_API_KEY", "")
	if err := v.Setup(); err != nil {
		t.Fatalf("setup failed: %v", err)
	}
	if got := v.StreamCompleter; got.Model == "" {
		t.Fatalf("expected setup to have initialized model and env fallback; model empty implies setup not run correctly")
	}
}
