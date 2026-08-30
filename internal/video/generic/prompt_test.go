package generic

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	pub_models "github.com/baalimago/clai/pkg/text/models"
)

func TestSetupPrompts(t *testing.T) {
	t.Run("formats positional args into the prompt", func(t *testing.T) {
		c := Configurations{PromptFormat: "film: '%v'"}
		if err := c.SetupPrompts([]string{"video", "ocean", "waves"}); err != nil {
			t.Fatalf("SetupPrompts: %v", err)
		}
		if !strings.Contains(c.Prompt, "film: 'ocean waves'") {
			t.Fatalf("expected formatted prompt, got %q", c.Prompt)
		}
	})

	t.Run("reply mode prepends the previous conversation", func(t *testing.T) {
		confDir := t.TempDir()
		t.Setenv("CLAI_CONFIG_DIR", confDir)
		convDir := filepath.Join(confDir, "conversations")
		if err := os.MkdirAll(convDir, 0o755); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}
		prev := pub_models.Chat{ID: "globalScope", Messages: []pub_models.Message{
			{Role: "user", Content: "previous prompt"},
		}}
		b, err := json.Marshal(prev)
		if err != nil {
			t.Fatalf("Marshal: %v", err)
		}
		if err := os.WriteFile(filepath.Join(convDir, "globalScope.json"), b, 0o644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}

		c := Configurations{PromptFormat: "%v", ReplyMode: true}
		if err := c.SetupPrompts([]string{"video", "again"}); err != nil {
			t.Fatalf("SetupPrompts: %v", err)
		}
		if !strings.Contains(c.Prompt, "previous prompt") || !strings.Contains(c.Prompt, "again") {
			t.Fatalf("expected reply context + new prompt, got %q", c.Prompt)
		}
	})
}

func TestSetupPrompts_imagePrompt(t *testing.T) {
	// A base64 blob with a PNG magic prefix is detected as an inline image:
	// it lands in PromptImageB64 and the prompt skips reply/format handling.
	pngBytes := append([]byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A}, make([]byte, 256)...)
	b64 := base64.StdEncoding.EncodeToString(pngBytes)
	c := Configurations{PromptFormat: "film: '%v'"}
	if err := c.SetupPrompts([]string{"video", "animate this", b64}); err != nil {
		t.Fatalf("SetupPrompts: %v", err)
	}
	if c.PromptImageB64 == "" {
		t.Fatal("expected image b64 to be extracted")
	}
	if strings.Contains(c.Prompt, "film:") {
		t.Fatalf("image prompts must skip formatting, got %q", c.Prompt)
	}
}
