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
