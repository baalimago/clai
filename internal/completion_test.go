package internal

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/baalimago/go_away_boilerplate/pkg/cmd"
)

func TestLoadCompletionData_NoConfigDir(t *testing.T) {
	t.Parallel()

	// A missing config dir must not break shell completion; the model
	// history, profiles, and shell contexts are simply empty. This guards
	// the regression where removing the unconditional config-dir creation
	// from main.run made `clai __complete` fail on a fresh machine.
	confDir := filepath.Join(t.TempDir(), "missing", ".clai")

	data, err := loadCompletionData(confDir)
	if err != nil {
		t.Fatalf("loadCompletionData: %v", err)
	}
	if data.Models != nil {
		t.Fatalf("models: got %v want nil", data.Models)
	}
	if data.Profiles != nil {
		t.Fatalf("profiles: got %v want nil", data.Profiles)
	}
	if data.ShellContexts != nil {
		t.Fatalf("shell contexts: got %v want nil", data.ShellContexts)
	}
}

func TestLoadCompletionData_ModelsFromConfigHistory(t *testing.T) {
	t.Parallel()

	confDir := t.TempDir()

	files := []string{
		"openai_gpt_gpt-5.2.json",
		"openrouter_chat_openai_gpt-5.2.json",
		"anthropic_claude_claude-3-7-sonnet.json",
		"google_gemini_gemini-2.0-flash.json",
		"ollama_llama3_deepseek-r1:8b.json",
		"novita_meta_llama-3.1-8b-instruct.json",
		"huggingface_hyperbolic_Qwen_Qwen2.5-Coder-32B-Instruct.json",
		"not-a-model.txt",
		"photoConfig.json",
	}

	for _, name := range files {
		if err := os.WriteFile(filepath.Join(confDir, name), []byte("{}"), 0o644); err != nil {
			t.Fatalf("WriteFile(%q): %v", name, err)
		}
	}

	data, err := loadCompletionData(confDir)
	if err != nil {
		t.Fatalf("loadCompletionData: %v", err)
	}

	want := []string{
		"claude-3-7-sonnet",
		"gemini-2.0-flash",
		"gpt-5.2",
		"hf:Qwen/Qwen2.5-Coder-32B-Instruct:hyperbolic",
		"novita:meta/llama-3.1-8b-instruct",
		"ollama:deepseek-r1:8b",
		"or:openai/gpt-5.2",
	}
	if !reflect.DeepEqual(data.Models, want) {
		t.Fatalf("models: got %v want %v", data.Models, want)
	}
}

func Test_toolValueItems_commaSplit(t *testing.T) {
	names := []string{"website_text", "webcam", "cat"}

	t.Run("plain prefix", func(t *testing.T) {
		got := toolValueItems("web", names)
		want := []cmd.CompletionItem{
			{Value: "webcam", Kind: cmd.CompletionKindPlain},
			{Value: "website_text", Kind: cmd.CompletionKindPlain},
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("got %v, want %v", got, want)
		}
	})

	t.Run("continuation after comma keeps the prefix", func(t *testing.T) {
		got := toolValueItems("website_text,we", names)
		want := []cmd.CompletionItem{
			{Value: "website_text,webcam", Kind: cmd.CompletionKindPlain},
			{Value: "website_text,website_text", Kind: cmd.CompletionKindPlain},
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("got %v, want %v", got, want)
		}
	})
}

func Test_flagValueHooks(t *testing.T) {
	t.Run("media dir kinds", func(t *testing.T) {
		for _, name := range []string{"pd", "photo-dir", "vd", "video-dir"} {
			got := MediaFlagValues(name, "")
			if len(got) != 1 || got[0].Kind != cmd.CompletionKindDir {
				t.Fatalf("%v: got %v, want one dir-kinded item", name, got)
			}
		}
		if got := MediaFlagValues("pm", ""); got != nil {
			t.Fatalf("model values must be free text, got %v", got)
		}
	})

	t.Run("profile path is file kind", func(t *testing.T) {
		s := &CompletionSources{}
		got := s.TextFlagValues("prp", "")
		if len(got) != 1 || got[0].Kind != cmd.CompletionKindFile {
			t.Fatalf("got %v, want one file-kinded item", got)
		}
	})

	t.Run("data load failure yields empty, not error", func(t *testing.T) {
		t.Setenv("CLAI_CONFIG_DIR", filepath.Join(t.TempDir(), "nope"))
		s := &CompletionSources{}
		if got := s.TextFlagValues("p", ""); len(got) != 0 {
			t.Fatalf("expected no suggestions on missing config dir, got %v", got)
		}
	})
}
