package text

import (
	"context"
	"testing"

	"github.com/baalimago/clai/internal"
	"github.com/baalimago/clai/internal/vendors/ollama"
	"github.com/baalimago/go_away_boilerplate/pkg/testboil"
)

func Test_SetupQuerier(t *testing.T) {
	testDir := t.TempDir()
	// Issue reported here: https://github.com/baalimago/clai/pull/16#issuecomment-3506586071
	t.Run("deepseek url on ollama:deepseek-r1:8b chat model", func(t *testing.T) {
		t.Setenv("DEBUG", "1")
		t.Setenv("CLAI_CONFIG_DIR", testDir)
		tf := internal.NewTextFlags()
		if err := tf.AgentText.ChatModel.Set("ollama:deepseek-r1:8b"); err != nil {
			t.Fatalf("Set: %v", err)
		}
		got, _, err := SetupQuerier(context.Background(),
			testDir,
			tf, []string{"q", "hello"}, nil)
		if err != nil {
			t.Fatal(err)
		}

		ollamaModel, ok := got.(*Querier[*ollama.Ollama])
		if !ok {
			t.Fatalf("expected type *Querier[*ollama.Ollama]), got: '%T'", got)
		}

		testboil.FailTestIfDiff(t, ollamaModel.Model.URL, ollama.ChatURL)
	})
}
