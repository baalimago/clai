package generic

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	pub_models "github.com/baalimago/clai/pkg/text/models"
)

func TestSetupPrompts(t *testing.T) {
	t.Run("formats positional args into the prompt", func(t *testing.T) {
		c := Configurations{PromptFormat: "film: '%v'"}
		if err := c.SetupPrompts(context.Background(), []string{"video", "ocean", "waves"}); err != nil {
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
		if err := c.SetupPrompts(context.Background(), []string{"video", "again"}); err != nil {
			t.Fatalf("SetupPrompts: %v", err)
		}
		if !strings.Contains(c.Prompt, "previous prompt") || !strings.Contains(c.Prompt, "again") {
			t.Fatalf("expected reply context + new prompt, got %q", c.Prompt)
		}
	})
}

func TestSetupPrompts_imagePrompt(t *testing.T) {
	// query.7: base64 in the prompt text stays text; the input image comes
	// from -img or stdin, lands in PromptImageB64 and skips reply/format handling.
	pngBytes := append([]byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A}, make([]byte, 256)...)
	b64 := base64.StdEncoding.EncodeToString(pngBytes)
	c := Configurations{PromptFormat: "film: '%v'"}
	if err := c.SetupPrompts(context.Background(), []string{"video", "animate this", b64}); err != nil {
		t.Fatalf("SetupPrompts: %v", err)
	}
	if c.PromptImageB64 != "" || c.Prompt != "film: 'animate this "+b64+"'" {
		t.Fatalf("prompt text became an image: b64=%d bytes, prompt=%q", len(c.PromptImageB64), c.Prompt)
	}

	path := filepath.Join(t.TempDir(), "still.png")
	if err := os.WriteFile(path, pngBytes, 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	c = Configurations{PromptFormat: "film: '%v'", Images: []string{path}}
	if err := c.SetupPrompts(context.Background(), []string{"video", "animate this"}); err != nil {
		t.Fatalf("SetupPrompts: %v", err)
	}
	if c.PromptImageB64 != b64 || c.Prompt != "animate this" {
		t.Fatalf("-img input: b64 match=%v, prompt=%q", c.PromptImageB64 == b64, c.Prompt)
	}

	c = Configurations{PromptFormat: "%v", Images: []string{path, path}}
	if err := c.SetupPrompts(context.Background(), []string{"video", "two"}); !errors.Is(err, ErrOneInputImage) {
		t.Fatalf("two input images: err = %v, want ErrOneInputImage", err)
	}
}

func TestSetupPrompts_replyDescribesImages(t *testing.T) {
	confDir := t.TempDir()
	t.Setenv("CLAI_CONFIG_DIR", confDir)
	convDir := filepath.Join(confDir, "conversations")
	if err := os.MkdirAll(convDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	png := append([]byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A}, 1, 2, 3)
	b64 := base64.StdEncoding.EncodeToString(png)
	legacy := `{"id":"globalScope","messages":[{"role":"user","content":[` +
		`{"type":"image_url","image_url":{"url":"data:image/png;base64,` + b64 + `"}},{"type":"text","text":"a sketch"}]}]}`
	if err := os.WriteFile(filepath.Join(convDir, "globalScope.json"), []byte(legacy), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	img := pub_models.ImagePart{Data: png}

	c := Configurations{PromptFormat: "%v", ReplyMode: true}
	if err := c.SetupPrompts(context.Background(), []string{"video", "animate it"}); err != nil {
		t.Fatalf("SetupPrompts: %v", err)
	}
	if !strings.Contains(c.Prompt, img.Describe()) || !strings.Contains(c.Prompt, "a sketch") || strings.Contains(c.Prompt, b64) {
		t.Fatalf("reply context = %q, want the image as %s and no base64", c.Prompt, img.Describe())
	}
	if !strings.HasSuffix(c.Prompt, "-------------\nanimate it") || c.PromptImageB64 != "" {
		t.Fatalf("prompt = %q, image b64 %d bytes", c.Prompt, len(c.PromptImageB64))
	}
}
