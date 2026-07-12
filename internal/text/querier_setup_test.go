package text

import "testing"

func TestVendorType_OpenRouter(t *testing.T) {
	vendor, model, modelVersion, err := vendorType("or:openai/gpt-5.2")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if vendor != "openrouter" {
		t.Fatalf("vendor mismatch: got %q want %q", vendor, "openrouter")
	}
	if model != "chat" {
		t.Fatalf("model mismatch: got %q want %q", model, "chat")
	}
	if modelVersion != "openai/gpt-5.2" {
		t.Fatalf("modelVersion mismatch: got %q want %q", modelVersion, "openai/gpt-5.2")
	}
}

func TestVendorType_Berget(t *testing.T) {
	vendor, model, modelVersion, err := vendorType("berget:zai-org/GLM-4.7-FP8")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if vendor != "berget" {
		t.Fatalf("vendor mismatch: got %q want %q", vendor, "berget")
	}
	if model != "zai-org" {
		t.Fatalf("model mismatch: got %q want %q", model, "zai-org")
	}
	if modelVersion != "GLM-4.7-FP8" {
		t.Fatalf("modelVersion mismatch: got %q want %q", modelVersion, "GLM-4.7-FP8")
	}
}

func TestVendorType_Berget_NoOrg(t *testing.T) {
	vendor, model, modelVersion, err := vendorType("berget:gemma-4-31B-it")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if vendor != "berget" {
		t.Fatalf("vendor mismatch: got %q want %q", vendor, "berget")
	}
	if model != "berget" {
		t.Fatalf("model mismatch: got %q want %q", model, "berget")
	}
	if modelVersion != "gemma-4-31B-it" {
		t.Fatalf("modelVersion mismatch: got %q want %q", modelVersion, "gemma-4-31B-it")
	}
}

func TestCanonicalModelString_RoundTrip(t *testing.T) {
	tests := []struct {
		name  string
		model string
	}{
		{"openai gpt", "gpt-5.2"},
		{"anthropic claude", "claude-sonnet-4"},
		{"openrouter", "or:gpt-4.1"},
		{"openrouter with slash", "or:openai/gpt-5.2"},
		{"berget with org", "berget:zai-org/GLM-4.7-FP8"},
		{"berget without org", "berget:gemma-4-31B-it"},
		{"ollama with prefix", "ollama:llama3"},
		{"ollama bare", "ollama"},
		{"novita with org", "novita:gryphe/some-model"},
		{"novita bare", "novita"},
		{"huggingface", "hf:model:provider"},
		{"deepseek", "deepseek-chat"},
		{"mistral", "mistral-large"},
		{"gemini", "gemini-2.0-flash"},
		{"grok", "grok-3"},
		{"mercury", "mercury-coder"},
		{"mock", "mock"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			vendor, family, modelVersion, err := vendorType(tt.model)
			if err != nil {
				t.Fatalf("vendorType(%q): %v", tt.model, err)
			}
			got := CanonicalModelString(vendor, family, modelVersion)
			if got != tt.model {
				t.Fatalf("round-trip broken: %q → vendorType → CanonicalModelString → %q", tt.model, got)
			}

			v2, f2, mv2, err2 := vendorType(got)
			if err2 != nil {
				t.Fatalf("vendorType(%q) after round-trip: %v", got, err2)
			}
			if v2 != vendor || f2 != family || mv2 != modelVersion {
				t.Fatalf("second pass mismatch: (%q, %q, %q) != (%q, %q, %q)",
					v2, f2, mv2, vendor, family, modelVersion)
			}
		})
	}
}

func TestCanonicalModelString_FromConfigFilename(t *testing.T) {
	tests := []struct {
		vendor, family, modelVersion string
		want                         string
	}{
		{"openai", "gpt", "gpt-4.1", "gpt-4.1"},
		{"anthropic", "claude", "sonnet-4", "sonnet-4"},
		{"openrouter", "chat", "gpt-4.1", "or:gpt-4.1"},
		{"berget", "zai-org", "GLM-4.7-FP8", "berget:zai-org/GLM-4.7-FP8"},
		{"berget", "berget", "gemma-4-31B-it", "berget:gemma-4-31B-it"},
		{"ollama", "llama3", "ollama:llama3", "ollama:llama3"},
		{"ollama", "llama3", "ollama", "ollama"},
		{"novita", "gryphe", "some-model", "novita:gryphe/some-model"},
		{"novita", "", "novita", "novita"},
		{"hf", "provider", "model", "hf:model:provider"},
	}

	for _, tt := range tests {
		got := CanonicalModelString(tt.vendor, tt.family, tt.modelVersion)
		if got != tt.want {
			t.Errorf("CanonicalModelString(%q, %q, %q) = %q, want %q", tt.vendor, tt.family, tt.modelVersion, got, tt.want)
		}
	}
}
