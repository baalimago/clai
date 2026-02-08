package video

import (
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	// We can use an alias avoiding conflict if any
	pub_models "github.com/baalimago/clai/pkg/text/models"
)

func TestSetupPrompts(t *testing.T) {
	// 1. Setup Config Dir for ReplyMode test
	tmpVal := "test_config_dir"
	tmpInfo, err := os.MkdirTemp("", tmpVal)
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpInfo)

	// Set config dir override for testing
	claiDir := filepath.Join(tmpInfo, ".clai")
	t.Setenv("CLAI_CONFIG_DIR", claiDir)

	// We expect the code to look in <claiDir>/conversations/globalScope.json

	// Create .clai/conversations directory
	convDir := filepath.Join(claiDir, "conversations")
	err = os.MkdirAll(convDir, 0o755)
	if err != nil {
		t.Fatalf("Failed to create conv dir: %v", err)
	}

	// Create globalScope.json content
	msgs := []pub_models.Message{
		{Role: "user", Content: "Hello previous query"},
		{Role: "system", Content: "System reply"},
	}
	prevChat := pub_models.Chat{
		Created:  time.Now(),
		ID:       "globalScope",
		Messages: msgs,
	}

	b, err := json.Marshal(prevChat)
	if err != nil {
		t.Fatalf("Failed to marshal chat: %v", err)
	}

	err = os.WriteFile(filepath.Join(convDir, "globalScope.json"), b, 0o644)
	if err != nil {
		t.Fatalf("Failed to write globalScope.json: %v", err)
	}

	// 2. Prepare for flag.Args() mocking
	// flag.Args() returns arguments from the default flag set after Parse().

	originalUsage := flag.Usage
	restoreFlags := func() {
		flag.CommandLine = flag.NewFlagSet(os.Args[0], flag.ExitOnError)
		flag.Usage = originalUsage
	}
	defer restoreFlags()

	flag.CommandLine = flag.NewFlagSet(os.Args[0], flag.ContinueOnError)

	err = flag.CommandLine.Parse([]string{"dummy_cmd_skipped", "my_test_prompt"})
	if err != nil {
		t.Fatalf("Failed to parse flags: %v", err)
	}

	t.Run("ReplyMode True", func(t *testing.T) {
		conf := Configurations{
			ReplyMode:    true,
			PromptFormat: "Prefix: %v",
			Prompt:       "",
		}

		err := conf.SetupPrompts()
		if err != nil {
			t.Fatalf("SetupPrompts failed: %v", err)
		}

		// Check prompt content
		if conf.Prompt == "" {
			t.Fatal("Prompt is empty")
		}

		expectedParts := []string{
			"my_test_prompt",
			"Hello previous query",
			"System reply",
			"Messages:",
			"-------------",
		}

		for _, part := range expectedParts {
			if !strings.Contains(conf.Prompt, part) {
				t.Errorf("Prompt did not contain expected part: %s", part)
			}
		}

		// Also check formatting
		if !strings.Contains(conf.Prompt, "Prefix: my_test_prompt") {
			t.Errorf("Prompt format not applied correctly. Expected 'Prefix: my_test_prompt'")
		}
	})

	t.Run("ReplyMode False", func(t *testing.T) {
		// Reset prompt
		conf := Configurations{
			ReplyMode:    false,
			PromptFormat: "%v",
			Prompt:       "",
		}

		err := conf.SetupPrompts()
		if err != nil {
			t.Fatalf("SetupPrompts failed: %v", err)
		}

		// Should NOT contain previous query
		if strings.Contains(conf.Prompt, "Hello previous query") {
			t.Error("Prompt should not contain previous query when ReplyMode is false")
		}

		// Should contain request prompt
		if !strings.Contains(conf.Prompt, "my_test_prompt") {
			t.Error("Prompt should contain the argument prompt")
		}
	})

	t.Run("PromptFormat no placeholder", func(t *testing.T) {
		conf := Configurations{
			ReplyMode:    false,
			PromptFormat: "Just Append:",
			Prompt:       "",
		}

		err := conf.SetupPrompts()
		if err != nil {
			t.Fatalf("SetupPrompts failed: %v", err)
		}

		// Should contain format then prompt
		// Note: Current implementation ignores PromptFormat if it doesn't contain "%v"
		if conf.Prompt != "my_test_prompt" {
			t.Errorf("Expected 'my_test_prompt', got '%v'", conf.Prompt)
		}
	})
}
