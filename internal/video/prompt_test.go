package video

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	pub_models "github.com/baalimago/clai/pkg/text/models"
)

func TestSetupPrompts(t *testing.T) {
	tmpInfo, err := os.MkdirTemp("", "test_config_dir")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpInfo)

	claiDir := filepath.Join(tmpInfo, ".clai")
	t.Setenv("CLAI_CONFIG_DIR", claiDir)

	convDir := filepath.Join(claiDir, "conversations")
	if err := os.MkdirAll(convDir, 0o755); err != nil {
		t.Fatalf("Failed to create conv dir: %v", err)
	}

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

	if err := os.WriteFile(filepath.Join(convDir, "globalScope.json"), b, 0o644); err != nil {
		t.Fatalf("Failed to write globalScope.json: %v", err)
	}

	args := []string{"dummy_cmd_skipped", "my_test_prompt"}

	t.Run("ReplyMode True", func(t *testing.T) {
		conf := Configurations{
			ReplyMode:    true,
			PromptFormat: "Prefix: %v",
			Prompt:       "",
		}

		if err := conf.SetupPrompts(args); err != nil {
			t.Fatalf("SetupPrompts failed: %v", err)
		}

		if conf.Prompt == "" {
			t.Fatal("Prompt is empty")
		}

		expectedParts := []string{
			"my_test_prompt",
			"Hello previous query",
			"System reply",
			"Messages:",
			"-------------",
			"Prefix: my_test_prompt",
		}

		for _, part := range expectedParts {
			if !strings.Contains(conf.Prompt, part) {
				t.Errorf("Prompt did not contain expected part: %s", part)
			}
		}
	})

	t.Run("ReplyMode False", func(t *testing.T) {
		conf := Configurations{
			ReplyMode:    false,
			PromptFormat: "%v",
			Prompt:       "",
		}

		if err := conf.SetupPrompts(args); err != nil {
			t.Fatalf("SetupPrompts failed: %v", err)
		}

		if strings.Contains(conf.Prompt, "Hello previous query") {
			t.Error("Prompt should not contain previous query when ReplyMode is false")
		}
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

		if err := conf.SetupPrompts(args); err != nil {
			t.Fatalf("SetupPrompts failed: %v", err)
		}

		// Without a %v placeholder we keep prompt as-is.
		if conf.Prompt != "my_test_prompt" {
			t.Errorf("Expected 'my_test_prompt', got %q", conf.Prompt)
		}
	})
}
