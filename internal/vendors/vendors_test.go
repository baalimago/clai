package vendors_test

import (
	"testing"

	"github.com/baalimago/clai/internal/models"
	"github.com/baalimago/clai/internal/vendors/anthropic"
	"github.com/baalimago/clai/internal/vendors/deepseek"
	"github.com/baalimago/clai/internal/vendors/mistral"
	"github.com/baalimago/clai/internal/vendors/novita"
	"github.com/baalimago/clai/internal/vendors/ollama"
	"github.com/baalimago/clai/internal/vendors/openai"
)

// vendorFactory returns a fresh instance of the vendor implementing the
// StreamCompleter interface.
type vendorFactory struct {
	name        string
	envVar      string
	requiresEnv bool
	newVendor   func() models.StreamCompleter
}

func Test_VendorSetup(t *testing.T) {
	vendors := []vendorFactory{
		{
			name:        "openai",
			envVar:      "OPENAI_API_KEY",
			requiresEnv: true,
			newVendor: func() models.StreamCompleter {
				v := openai.GPT_DEFAULT
				return &v
			},
		},
		{
			name:        "anthropic",
			envVar:      "ANTHROPIC_API_KEY",
			requiresEnv: true,
			newVendor: func() models.StreamCompleter {
				v := anthropic.ClaudeDefault
				return &v
			},
		},
		{
			name:        "mistral",
			envVar:      "MISTRAL_API_KEY",
			requiresEnv: true,
			newVendor: func() models.StreamCompleter {
				v := mistral.MistralDefault
				return &v
			},
		},
		{
			name:        "deepseek",
			envVar:      "DEEPSEEK_API_KEY",
			requiresEnv: false,
			newVendor: func() models.StreamCompleter {
				v := deepseek.DEEPSEEK_DEFAULT
				return &v
			},
		},
		{
			name:        "novita",
			envVar:      "NOVITA_API_KEY",
			requiresEnv: false,
			newVendor: func() models.StreamCompleter {
				v := novita.NOVITA_DEFAULT
				return &v
			},
		},
		{
			name:        "ollama",
			envVar:      "OLLAMA_API_KEY",
			requiresEnv: false,
			newVendor: func() models.StreamCompleter {
				v := ollama.OLLAMA_DEFAULT
				return &v
			},
		},
	}

	for _, vf := range vendors {
		t.Run(vf.name+"_with_env", func(t *testing.T) {
			vendor := vf.newVendor()
			t.Setenv(vf.envVar, "some-key")
			if err := vendor.Setup(); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})

		if vf.requiresEnv {
			t.Run(vf.name+"_no_env", func(t *testing.T) {
				vendor := vf.newVendor()
				t.Setenv(vf.envVar, "")
				if err := vendor.Setup(); err == nil {
					t.Fatalf("expected error when %s unset", vf.envVar)
				}
			})
		}
	}
}
