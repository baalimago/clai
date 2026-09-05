package text

import (
	"testing"

	"github.com/baalimago/clai/internal/vendors/anthropic"
	"github.com/baalimago/clai/internal/vendors/berget"
	"github.com/baalimago/clai/internal/vendors/deepseek"
	"github.com/baalimago/clai/internal/vendors/gemini"
	"github.com/baalimago/clai/internal/vendors/inception"
	"github.com/baalimago/clai/internal/vendors/mistral"
	"github.com/baalimago/clai/internal/vendors/novita"
	"github.com/baalimago/clai/internal/vendors/ollama"
	"github.com/baalimago/clai/internal/vendors/openai"
	"github.com/baalimago/clai/internal/vendors/openrouter"
	"github.com/baalimago/clai/internal/vendors/xai"
)

// Test_vendorDefaultModelsRoute pins a self-consistency invariant: every
// vendor package's default model must reach that same vendor through
// vendorType. It asserts nothing about *which* model a vendor defaults to —
// that is a moving target owned by the provider — only that whatever is
// chosen is reachable, so defaults stay free to change.
//
// Vendors differ in what their default holds. Most carry a canonical model
// string that routes on its own. The prefix vendors carry only the
// model-version part, which reaches the vendor as "<prefix><default>".
//
// This exists because inception's default was "murcury", a typo for
// "mercury". vendorType matches the substring "mercury", so the default
// routed nowhere: vendorType("murcury") returned
// `failed to find vendor for: murcury` (2026-09-05).
func Test_vendorDefaultModelsRoute(t *testing.T) {
	cases := []struct {
		vendor string // the vendor name vendorType must return
		prefix string // "" when the default is already a canonical string
		model  string // that vendor package's default model string
	}{
		{"anthropic", "", anthropic.Default.Model},
		{"deepseek", "", deepseek.Default.Model},
		{"google", "", gemini.Default.Model},
		{"inception", "", inception.Default.Model},
		{"mistral", "", mistral.Default.Model},
		{"openai", "", openai.GptDefault.Model},
		{"xai", "", xai.Default.Model},
		{"berget", "berget:", berget.Default.Model},
		{"novita", "novita:", novita.Default.Model},
		{"ollama", "ollama:", ollama.Default.Model},
		{"openrouter", "or:", openrouter.Default.Model},
	}

	for _, tc := range cases {
		t.Run(tc.vendor, func(t *testing.T) {
			if tc.model == "" {
				t.Fatalf("%s default model is empty", tc.vendor)
			}
			full := tc.prefix + tc.model
			got, _, _, err := vendorType(full)
			if err != nil {
				t.Fatalf("%s default model %q does not route: %v", tc.vendor, full, err)
			}
			if got != tc.vendor {
				t.Fatalf("%s default model %q routed to vendor %q, want %q",
					tc.vendor, full, got, tc.vendor)
			}
		})
	}
}

// Test_vendorType_MockSelectionDoesNotCaptureRealModels pins that the mock
// vendor is selected deliberately, not by accidental substring overlap.
//
// vendorType matched `strings.Contains(fromModel, "test")` before any real
// vendor, so every model whose name contains "test" — which includes the
// entire "-latest" naming convention — silently resolved to the mock vendor
// and returned fabricated output with no error. mistral's own default,
// "mistral-large-latest", was affected (2026-09-05).
func Test_vendorType_MockSelectionDoesNotCaptureRealModels(t *testing.T) {
	realModels := []struct{ model, wantVendor string }{
		{"mistral-large-latest", "mistral"},
		{"gpt-4o-latest", "openai"},
		{"claude-3-latest", "anthropic"},
		{"grok-latest", "xai"},
		{"gemini-flash-latest", "google"},
		{"deepseek-chat-latest", "deepseek"},
	}
	for _, tc := range realModels {
		t.Run(tc.model, func(t *testing.T) {
			got, _, _, err := vendorType(tc.model)
			if err != nil {
				t.Fatalf("vendorType(%q): %v", tc.model, err)
			}
			if got != tc.wantVendor {
				t.Fatalf("vendorType(%q) = %q, want %q (mock must not capture it)",
					tc.model, got, tc.wantVendor)
			}
		})
	}

	// The deliberate selections the test suite relies on must keep working.
	mockModels := []string{"test", "test-model", "test-mcp", "mock"}
	for _, m := range mockModels {
		t.Run("mock/"+m, func(t *testing.T) {
			got, _, _, err := vendorType(m)
			if err != nil {
				t.Fatalf("vendorType(%q): %v", m, err)
			}
			if got != "mock" {
				t.Fatalf("vendorType(%q) = %q, want mock", m, got)
			}
		})
	}
}
