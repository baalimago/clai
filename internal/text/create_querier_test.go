package text

import (
	"fmt"
	"strings"
	"testing"

	"github.com/baalimago/clai/internal/vendors/anthropic"
	"github.com/baalimago/clai/internal/vendors/deepseek"
	"github.com/baalimago/clai/internal/vendors/gemini"
	"github.com/baalimago/clai/internal/vendors/huggingface"
	"github.com/baalimago/clai/internal/vendors/inception"
	"github.com/baalimago/clai/internal/vendors/mistral"
	"github.com/baalimago/clai/internal/vendors/novita"
	"github.com/baalimago/clai/internal/vendors/ollama"
	"github.com/baalimago/clai/internal/vendors/openai"
	"github.com/baalimago/clai/internal/vendors/openrouter"
	"github.com/baalimago/clai/internal/vendors/xai"
	"github.com/baalimago/go_away_boilerplate/pkg/debug"
	"github.com/baalimago/go_away_boilerplate/pkg/testboil"
)

func TestCreateQuerier(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("OPENAI_API_KEY", "key")

	conf := Configurations{
		Model:     "gpt-4",
		ConfigDir: tmp,
	}
	writeModelPriceFixture(t, conf, openai.GptDefault)
	q, err := CreateQuerier(t.Context(), conf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if q == nil {
		t.Fatal("expected querier")
	}
	waitForQuerierCosts(t, q)

	_, err = CreateQuerier(
		t.Context(),
		Configurations{
			Model:     "unknown",
			ConfigDir: tmp,
		},
	)
	if err == nil {
		t.Error("expected error for unknown model")
	}
}

func TestSelectTextQuerier_AllVendors(t *testing.T) {
	cases := []struct {
		name     string
		model    string
		env      map[string]string
		defaults any
	}{
		{
			name:     "huggingface",
			defaults: huggingface.DefaultChat,
			model:    "hf:Qwen/Qwen2.5-72B-Instruct:novita",
			env:      map[string]string{"HF_API_KEY": "k"},
		},
		{
			name:     "anthropic",
			defaults: anthropic.Default,
			model:    "claude-3-opus",
			env:      map[string]string{"ANTHROPIC_API_KEY": "k"},
		},
		{
			name:     "openai",
			defaults: openai.GptDefault,
			model:    "gpt-4.1",
			env:      map[string]string{"OPENAI_API_KEY": "k"},
		},
		{
			name:     "openrouter",
			defaults: openrouter.Default,
			model:    "or:openai/gpt-5.2",
			env:      map[string]string{"OPENROUTER_API_KEY": "k"},
		},
		{
			name:     "deepseek",
			defaults: deepseek.Default,
			model:    "deepseek-chat",
			env:      nil,
		},
		{
			name:     "inception",
			defaults: inception.Default,
			model:    "mercury-pro",
			env:      map[string]string{"INCEPTION_API_KEY": "k"},
		},
		{
			name:     "xai",
			defaults: xai.Default,
			model:    "grok-beta",
			env:      nil,
		},
		{
			name:     "mistral",
			defaults: mistral.Default,
			model:    "mistral-large",
			env:      map[string]string{"MISTRAL_API_KEY": "k"},
		},
		{
			name:     "gemini",
			defaults: gemini.Default,
			model:    "gemini-2.0",
			env:      nil,
		},
		{
			name:     "ollama-pref",
			defaults: ollama.Default,
			model:    "ollama:phi",
			env:      nil,
		},
		{
			name:     "ollama-bare",
			defaults: ollama.Default,
			model:    "ollama",
			env:      nil,
		},
		{
			name:     "novita-pref",
			defaults: novita.Default,
			model:    "novita:orgx/fixture-model",
			env:      nil,
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
			conf := Configurations{
				Model:     tc.model,
				ConfigDir: tmp,
			}
			writeModelPriceFixture(t, conf, tc.defaults)
			q, found, err := selectTextQuerier(
				t.Context(),
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
			waitForQuerierCosts(t, q)
		})
	}
}

func TestSelectTextQuerier_OpenRouterPrefix(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("OPENROUTER_API_KEY", "k")
	t.Setenv("CLAI_CONFIG_DIR", tmp)

	conf := Configurations{
		Model:     "or:openai/gpt-5.2",
		ConfigDir: tmp,
	}
	writeModelPriceFixture(t, conf, openrouter.Default)
	q, found, err := selectTextQuerier(t.Context(), conf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !found {
		t.Fatal("expected found")
	}
	if q == nil {
		t.Fatal("expected querier")
	}
	waitForQuerierCosts(t, q)
	typed, ok := q.(*Querier[*openrouter.OpenRouter])
	if !ok {
		t.Fatalf("expected openrouter querier, got: %T", q)
	}
	if configDir := typed.Model.URL; configDir == "" {
		t.Fatal("expected openrouter model url to be set")
	}
}

func TestSelectTextQuerier_OpenRouterPrefixBeatsProviderSubstring(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("OPENROUTER_API_KEY", "k")
	t.Setenv("CLAI_CONFIG_DIR", tmp)

	conf := Configurations{
		Model:     "or:deepseek/deepseek-v4-pro",
		ConfigDir: tmp,
	}
	writeModelPriceFixture(t, conf, openrouter.Default)
	q, found, err := selectTextQuerier(t.Context(), conf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !found {
		t.Fatal("expected found")
	}
	if q == nil {
		t.Fatal("expected querier")
	}
	waitForQuerierCosts(t, q)
	typed, ok := q.(*Querier[*openrouter.OpenRouter])
	if !ok {
		t.Fatalf("expected openrouter querier, got: %T", q)
	}
	if typed.Model.Model != "or:deepseek/deepseek-v4-pro" {
		t.Fatalf("expected prefixed openrouter model, got: %q", typed.Model.Model)
	}
}

func TestSelectTextQuerier_ErrorPropagation(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("ANTHROPIC_API_KEY", "")

	conf := Configurations{
		Model:     "claude-3-opus",
		ConfigDir: tmp,
	}
	q, found, err := selectTextQuerier(
		t.Context(),
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
	conf := Configurations{
		Model:     "not-a-vendor",
		ConfigDir: tmp,
	}
	q, found, err := selectTextQuerier(
		t.Context(),
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
	t.Setenv("OPENROUTER_API_KEY", "")
	warnings := make(chan string, 4)
	tmp := t.TempDir()
	conf := Configurations{
		Model:     "ollama:deepseek-r1:8b",
		ConfigDir: tmp,
		CostWarnf: func(format string, args ...any) { warnings <- fmt.Sprintf(format, args...) },
	}
	q, found, err := selectTextQuerier(
		t.Context(),
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
	waitForQuerierCosts(t, q)
	if !strings.Contains(fmt.Sprintf("%T", q), "ollama.Ollama") {
		t.Fatalf("expected model of type ollama.Ollama, got: %T ", q)
	}
	typed, ok := q.(*Querier[*ollama.Ollama])
	if !ok {
		t.Fatalf("expected text.Querier, got: %T ", q)
	}

	testboil.FailTestIfDiff(t, typed.Model.Model, conf.Model)
	testboil.FailTestIfDiff(t, typed.Model.URL, "http://localhost:11434/v1/chat/completions")
}
