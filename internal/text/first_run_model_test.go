package text

import "testing"

func envOf(vars map[string]string) func(string) string {
	return func(k string) string { return vars[k] }
}

func Test_FirstRunModel(t *testing.T) {
	cases := []struct {
		name string
		env  map[string]string
		want string
	}{
		{"no key keeps the default", nil, Default.Model},
		{"an OpenAI key keeps the default", map[string]string{"OPENAI_API_KEY": "k"}, Default.Model},
		{"only an Anthropic key picks Claude", map[string]string{"ANTHROPIC_API_KEY": "k"}, "claude-sonnet-5-5"},
		{"only a Gemini key picks Gemini", map[string]string{"GEMINI_API_KEY": "k"}, "gemini-3.6-flash"},
		{"only a Mistral key picks Mistral", map[string]string{"MISTRAL_API_KEY": "k"}, "mistral-large-latest"},
		{"only a DeepSeek key picks DeepSeek", map[string]string{"DEEPSEEK_API_KEY": "k"}, "deepseek-chat"},
		{"only an xAI key picks Grok", map[string]string{"XAI_API_KEY": "k"}, "grok-code-fast-1"},
		{"OpenAI wins over Anthropic", map[string]string{"ANTHROPIC_API_KEY": "k", "OPENAI_API_KEY": "k"}, Default.Model},
		{"an empty key does not count", map[string]string{"ANTHROPIC_API_KEY": ""}, Default.Model},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := FirstRunModel(envOf(tc.env)); got != tc.want {
				t.Fatalf("FirstRunModel = %q, want %q", got, tc.want)
			}
		})
	}
}

func Test_FirstRunDefaults_onlyChangesModel(t *testing.T) {
	got := FirstRunDefaults(envOf(map[string]string{"ANTHROPIC_API_KEY": "k"}))
	if got.Model != "claude-sonnet-5-5" {
		t.Fatalf("Model = %q", got.Model)
	}
	if got.SystemPrompt != Default.SystemPrompt || got.ToolOutputRuneLimit != Default.ToolOutputRuneLimit {
		t.Fatal("FirstRunDefaults changed more than the model")
	}
	if Default.Model != "gpt-5.2" {
		t.Fatalf("FirstRunDefaults mutated Default: %q", Default.Model)
	}
}
