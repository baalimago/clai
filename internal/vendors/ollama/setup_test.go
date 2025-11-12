package ollama

import (
	"os"
	"testing"

	"github.com/baalimago/clai/internal/models"
	"github.com/baalimago/clai/internal/vendors/vendorstest"
)

func TestSetup(t *testing.T) {
	vendorstest.RunSetupTests(t, "OLLAMA_API_KEY", false, func() models.StreamCompleter {
		v := Default
		return &v
	})
}

func TestSetup_Default_SetsFields(t *testing.T) {
	v := Default
	t.Setenv("OLLAMA_API_KEY", "")
	if err := v.Setup(); err != nil {
		t.Fatalf("setup failed: %v", err)
	}
	if v.ToolChoice == nil {
		t.Fatalf("toolchoice nil")
	}
	if *v.ToolChoice != "auto" {
		t.Fatalf("toolchoice got %q want %q", *v.ToolChoice, "auto")
	}
	if v.StreamCompleter.Temperature == nil {
		t.Fatalf("temperature ptr nil")
	}
	if v.StreamCompleter.TopP == nil {
		t.Fatalf("top_p ptr nil")
	}
	if v.StreamCompleter.FrequencyPenalty == nil {
		t.Fatalf("freq ptr nil")
	}
	// should keep model when no prefix is present
	if v.StreamCompleter.Model != "llama3" {
		t.Fatalf("model got %q want %q",
			v.StreamCompleter.Model, "llama3")
	}
}

func TestSetup_TrimsOllamaPrefix(t *testing.T) {
	v := Default
	v.Model = "ollama:deepseek-r1:8b"
	t.Setenv("OLLAMA_API_KEY", "")
	if err := v.Setup(); err != nil {
		t.Fatalf("setup failed: %v", err)
	}
	want := "deepseek-r1:8b"
	if v.StreamCompleter.Model != want {
		t.Fatalf("model got %q want %q",
			v.StreamCompleter.Model, want)
	}
}

func TestSetup_RespectsExistingAPIKey(t *testing.T) {
	v := Default
	t.Setenv("OLLAMA_API_KEY", "some-key")
	if err := v.Setup(); err != nil {
		t.Fatalf("setup failed: %v", err)
	}
	if got := os.Getenv("OLLAMA_API_KEY"); got != "some-key" {
		t.Fatalf("api key got %q want %q", got, "some-key")
	}
}
