package internal

import (
	"context"
	"testing"

	"github.com/baalimago/clai/internal/text"
	"github.com/baalimago/clai/internal/vendors/ollama"
	"github.com/baalimago/go_away_boilerplate/pkg/testboil"
)

func Test_setupTextQuerier(t *testing.T) {
	testDir := t.TempDir()
	// Issue reported here: https://github.com/baalimago/clai/pull/16#issuecomment-3506586071
	t.Run("deepseek url on ollama:deepseek-r1:8b chat model", func(t *testing.T) {
		t.Setenv("DEBUG", "1")
		t.Setenv("CLAI_CONFIG_DIR", testDir)
		got, err := setupTextQuerier(context.Background(),
			QUERY,
			testDir,
			Configurations{
				ChatModel: "ollama:deepseek-r1:8b",
			}, []string{"q", "hello"})
		if err != nil {
			t.Fatal(err)
		}

		ollamaModel, ok := got.(*text.Querier[*ollama.Ollama])
		if !ok {
			t.Fatalf("expected type *text.Querier[*ollama.Ollama]), got: '%T'", got)
		}

		testboil.FailTestIfDiff(t, ollamaModel.Model.URL, ollama.ChatURL)
	})
}
