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
