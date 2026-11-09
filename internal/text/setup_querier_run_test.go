package text

import (
	"slices"
	"strings"
	"testing"

	"github.com/baalimago/clai/internal"
	"github.com/baalimago/clai/internal/jsonl"
	"github.com/baalimago/clai/internal/runenv"
	"github.com/baalimago/clai/internal/skills"
	"github.com/baalimago/clai/internal/vendors/ollama"
	"github.com/baalimago/go_away_boilerplate/pkg/testboil"
)

func Test_SetupQuerier(t *testing.T) {
	testDir := t.TempDir()
	// Issue reported here: https://github.com/baalimago/clai/pull/16#issuecomment-3506586071
	t.Run("deepseek url on ollama:deepseek-r1:8b chat model", func(t *testing.T) {
		t.Setenv("DEBUG", "")
		t.Setenv("OPENROUTER_API_KEY", "")
		t.Setenv("CLAI_CONFIG_DIR", testDir)
		tf := internal.NewTextFlags()
		if err := tf.AgentText.ChatModel.Set("ollama:deepseek-r1:8b"); err != nil {
			t.Fatalf("Set: %v", err)
		}
		got, _, err := SetupQuerier(t.Context(),
			testDir,
			tf, []string{"q", "hello"}, nil, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		waitForQuerierCosts(t, got)

		ollamaModel, ok := got.(*Querier[*ollama.Ollama])
		if !ok {
			t.Fatalf("expected type *Querier[*ollama.Ollama]), got: '%T'", got)
		}

		testboil.FailTestIfDiff(t, ollamaModel.Model.URL, ollama.ChatURL)
	})
}

// TestSkillTrustQuestion pins R3-04 for skills: under -json the trust
// question is plain text and its choices carry the answers; the text form
// keeps "[y/N]".
func TestSkillTrustQuestion(t *testing.T) {
	prompt := skills.TrustPrompt{Name: "review", Description: "Review pending changes"}
	asJSON := skillTrustQuestion(runenv.New(runenv.Config{Format: jsonl.FormatJSON}), prompt)
	if asJSON.Kind != "trust" || strings.Contains(asJSON.Text, "[y/N]") || !strings.HasSuffix(asJSON.Text, "Trust this skill?") ||
		!slices.Equal(asJSON.Choices, []string{"y", "N"}) {
		t.Fatalf("json question = %+v", asJSON)
	}
	if text := skillTrustQuestion(runenv.New(runenv.Config{}), prompt); !strings.HasSuffix(text.Text, "Trust this skill? [y/N]") {
		t.Fatalf("text question = %+v", text)
	}
}
