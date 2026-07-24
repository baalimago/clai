package internal

import (
	"context"
	"testing"

	"github.com/baalimago/clai/internal/text"
	"github.com/baalimago/clai/internal/vendors/ollama"
	"github.com/baalimago/go_away_boilerplate/pkg/testboil"
)

func TestExtractMacroInputs(t *testing.T) {
	t.Run("SETUP with extra args", func(t *testing.T) {
		got := extractMacroInputs(SETUP, []string{"s", "3", "1"})
		if len(got) != 2 || got[0] != "3" || got[1] != "1" {
			t.Fatalf("got %v, want [3 1]", got)
		}
	})
	t.Run("TOOLS with extra args", func(t *testing.T) {
		got := extractMacroInputs(TOOLS, []string{"tools", "ls"})
		if len(got) != 1 || got[0] != "ls" {
			t.Fatalf("got %v, want [ls]", got)
		}
	})
	t.Run("PROFILES with extra args", func(t *testing.T) {
		got := extractMacroInputs(PROFILES, []string{"profiles", "list"})
		if len(got) != 1 || got[0] != "list" {
			t.Fatalf("got %v, want [list]", got)
		}
	})
	t.Run("CHAT returns nil (self-detected in chat.New)", func(t *testing.T) {
		got := extractMacroInputs(CHAT, []string{"chat", "list", "3"})
		if got != nil {
			t.Fatalf("got %v, want nil", got)
		}
	})
	t.Run("QUERY with extra args returns nil", func(t *testing.T) {
		got := extractMacroInputs(QUERY, []string{"q", "hello", "world"})
		if got != nil {
			t.Fatalf("got %v, want nil", got)
		}
	})
	t.Run("no extra args returns nil", func(t *testing.T) {
		got := extractMacroInputs(SETUP, []string{"s"})
		if got != nil {
			t.Fatalf("got %v, want nil", got)
		}
	})
	t.Run("empty args returns nil", func(t *testing.T) {
		got := extractMacroInputs(SETUP, nil)
		if got != nil {
			t.Fatalf("got %v, want nil", got)
		}
	})
}

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
