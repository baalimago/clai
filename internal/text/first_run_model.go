package text

import (
	"github.com/baalimago/clai/internal/vendors/anthropic"
	"github.com/baalimago/clai/internal/vendors/deepseek"
	"github.com/baalimago/clai/internal/vendors/gemini"
	"github.com/baalimago/clai/internal/vendors/mistral"
	"github.com/baalimago/clai/internal/vendors/xai"
)

// FirstRunModel is the chat model a new textConfig.json starts with: the
// default when OPENAI_API_KEY is set or no vendor key is, otherwise the
// default model of the first vendor whose key is set, so a first query
// works with whichever key the user exported.
func FirstRunModel(getenv func(string) string) string {
	candidates := []struct{ env, model string }{
		{"OPENAI_API_KEY", Default.Model},
		{"ANTHROPIC_API_KEY", anthropic.Default.Model},
		{"GEMINI_API_KEY", gemini.Default.Model},
		{"MISTRAL_API_KEY", mistral.Default.Model},
		{"DEEPSEEK_API_KEY", deepseek.Default.Model},
		{"XAI_API_KEY", xai.Default.Model},
	}
	for _, c := range candidates {
		if getenv(c.env) != "" {
			return c.model
		}
	}
	return Default.Model
}

// FirstRunDefaults is Default with its model picked by FirstRunModel. It is
// what a missing textConfig.json is created from; an existing file keeps
// its own model.
func FirstRunDefaults(getenv func(string) string) Configurations {
	c := Default
	c.Model = FirstRunModel(getenv)
	return c
}
