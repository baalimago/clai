package internal

import (
	"context"
	"flag"
	"testing"

	"github.com/baalimago/clai/internal/text"
	"github.com/baalimago/clai/internal/vendors/ollama"
	"github.com/baalimago/go_away_boilerplate/pkg/testboil"
)

func TestGetModeFromArgs(t *testing.T) {
	tests := []struct {
		arg  string
		want Mode
	}{
		{"p", PHOTO},
		{"chat", CHAT},
		{"q", QUERY},
		{"glob", GLOB},
		{"re", REPLAY},
		{"cmd", CMD},
		{"setup", SETUP},
		{"version", VERSION},
	}
	for _, tc := range tests {
		got, err := getModeFromArgs(tc.arg)
		if err != nil {
			t.Errorf("unexpected error for %s: %v", tc.arg, err)
		}
		if got != tc.want {
			t.Errorf("mode for %s = %v, want %v", tc.arg, got, tc.want)
		}
	}
	if _, err := getModeFromArgs("unknown"); err == nil {
		t.Error("expected error for unknown command")
	}
}

func Test_setupTextQuerier(t *testing.T) {
	testDir := t.TempDir()
	// Issue reported here: https://github.com/baalimago/clai/pull/16#issuecomment-3506586071
	t.Run("deepseek url on ollama:deepseek-r1:8b chat model", func(t *testing.T) {
		t.Setenv("DEBUG", "1")
		oldFS := flag.CommandLine
		defer func() { flag.CommandLine = oldFS }()
		fs := flag.NewFlagSet("clai", flag.ContinueOnError)
		_ = fs.Parse([]string{"q", "noop"})
		flag.CommandLine = fs

		got, err := setupTextQuerier(context.Background(),
			QUERY,
			testDir,
			Configurations{
				ChatModel: "ollama:deepseek-r1:8b",
			})
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
