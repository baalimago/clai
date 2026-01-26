package internal

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/baalimago/clai/internal/photo"
	"github.com/baalimago/clai/internal/text"
	"github.com/baalimago/clai/internal/vendors/ollama"
	"github.com/baalimago/go_away_boilerplate/pkg/debug"
	"github.com/baalimago/go_away_boilerplate/pkg/testboil"
)

func TestCreateTextQuerier(t *testing.T) {
	tmp := t.TempDir()
	os.Setenv("OPENAI_API_KEY", "key")
	defer os.Unsetenv("OPENAI_API_KEY")

	conf := text.Configurations{
		Model:     "gpt-4",
		ConfigDir: tmp,
	}
	q, err := CreateTextQuerier(context.Background(), conf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if q == nil {
		t.Fatal("expected querier")
	}

	_, err = CreateTextQuerier(
		context.Background(),
		text.Configurations{
			Model:     "unknown",
			ConfigDir: tmp,
		},
	)
	if err == nil {
		t.Error("expected error for unknown model")
	}
}

func TestNewPhotoQuerier(t *testing.T) {
	tmp := t.TempDir()
	os.Setenv("OPENAI_API_KEY", "key")
	defer os.Unsetenv("OPENAI_API_KEY")
	conf := photo.Configurations{Model: "dall-e-3", Output: photo.Output{Type: photo.URL, Dir: tmp}}
	q, err := CreatePhotoQuerier(conf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if q == nil {
		t.Fatal("expected querier")
	}
}

func TestSelectTextQuerier_AllVendors(t *testing.T) {
	cases := []struct {
		name  string
		model string
		env   map[string]string
	}{
		{
			name:  "huggingface",
			model: "hf:Qwen/Qwen2.5-72B-Instruct",
			env:   map[string]string{"HF_API_KEY": "k"},
		},
		{
			name:  "anthropic",
			model: "claude-3-opus",
			env:   map[string]string{"ANTHROPIC_API_KEY": "k"},
		},
		{
			name:  "openai",
			model: "gpt-4.1",
			env:   map[string]string{"OPENAI_API_KEY": "k"},
		},
		{
			name:  "deepseek",
			model: "deepseek-chat",
			env:   nil,
		},
		{
			name:  "inception",
			model: "mercury-pro",
			env:   map[string]string{"INCEPTION_API_KEY": "k"},
		},
		{
			name:  "xai",
			model: "grok-beta",
			env:   nil,
		},
		{
			name:  "mistral",
			model: "mistral-large",
			env:   map[string]string{"MISTRAL_API_KEY": "k"},
		},
		{
			name:  "gemini",
			model: "gemini-2.0",
			env:   nil,
		},
		{
			name:  "ollama-pref",
			model: "ollama:phi",
			env:   nil,
		},
		{
			name:  "ollama-bare",
			model: "ollama",
			env:   nil,
		},
		{
			name:  "novita-pref",
			model: "novita:gryphe/mythomax-l2-13b",
			env:   nil,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// isolate config directories per vendor case (some models include "/" which
			// becomes part of the config filename and would require nested dirs).
			tmp := t.TempDir()

			for k, v := range tc.env {
				t.Setenv(k, v)
			}
			conf := text.Configurations{
				Model:     tc.model,
				ConfigDir: tmp,
			}
			q, found, err := selectTextQuerier(
				context.Background(),
				conf,
			)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !found {
				t.Fatal("expected found")
			}
			if q == nil {
				t.Fatal("expected querier")
			}
		})
	}
}

func TestSelectTextQuerier_ErrorPropagation(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("ANTHROPIC_API_KEY", "")

	conf := text.Configurations{
		Model:     "claude-3-opus",
		ConfigDir: tmp,
	}
	q, found, err := selectTextQuerier(
		context.Background(),
		conf,
	)
	if err == nil {
		t.Fatal("expected error")
	}
	if !found {
		t.Error("expected found to be true")
	}
	if q != nil {
		t.Error("expected nil querier")
	}
}

func TestSelectTextQuerier_Unknown(t *testing.T) {
	tmp := t.TempDir()
	conf := text.Configurations{
		Model:     "not-a-vendor",
		ConfigDir: tmp,
	}
	q, found, err := selectTextQuerier(
		context.Background(),
		conf,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if found {
		t.Fatalf("expected not found")
	}
	if q != nil {
		t.Fatalf("expected nil querier")
	}
}

func TestSelectTextQuerier_OllamaDeepseekPrefersOllama(t *testing.T) {
	tmp := t.TempDir()
	conf := text.Configurations{
		Model:     "ollama:deepseek-r1:8b",
		ConfigDir: tmp,
	}
	q, found, err := selectTextQuerier(
		context.Background(),
		conf,
	)
	t.Logf("%T,\nd: %v", q, debug.IndentedJsonFmt(q))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !found {
		t.Fatal("expected found")
	}
	if q == nil {
		t.Fatal("expected querier")
	}
	if !strings.Contains(fmt.Sprintf("%T", q), "ollama.Ollama") {
		t.Fatalf("expected model of type ollama.Ollama, got: %T ", q)
	}
	typed, ok := q.(*text.Querier[*ollama.Ollama])
	if !ok {
		t.Fatalf("expected text.Querier, got: %T ", q)
	}

	testboil.FailTestIfDiff(t, typed.Model.Model, conf.Model)
	testboil.FailTestIfDiff(t, typed.Model.URL, "http://localhost:11434/v1/chat/completions")
}
