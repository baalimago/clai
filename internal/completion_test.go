package internal

import (
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"

	"github.com/baalimago/clai/internal/tools"
	pubmodels "github.com/baalimago/clai/pkg/text/models"
)

type staticTool struct {
	spec pubmodels.Specification
}

func (s staticTool) Call(pubmodels.Input) (string, error) {
	return "", nil
}

func (s staticTool) Specification() pubmodels.Specification {
	return s.spec
}

func TestCompletionEngineComplete(t *testing.T) {
	t.Parallel()

	tools.WithTestRegistry(t, func() {
		tools.Registry.Set("rg", staticTool{spec: pubmodels.Specification{Name: "rg", Description: "ripgrep"}})
		tools.Registry.Set("find", staticTool{spec: pubmodels.Specification{Name: "find", Description: "find"}})
		tools.Registry.Set("file_tree", staticTool{spec: pubmodels.Specification{Name: "file_tree", Description: "tree"}})

		engine := newCompletionEngine(completionData{
			Profiles:      []string{"prod", "project"},
			ShellContexts: []string{"minimal", "mixed"},
		})

		testCases := []struct {
			name        string
			line        []string
			wantValues  []string
			wantKinds   []completionResultKind
			wantReplace string
		}{
			{
				name:        "top level after trailing space lists commands and flags",
				line:        []string{"clai", ""},
				wantValues:  []string{"c", "chat", "completion", "confdir", "g", "glob", "h", "help", "p", "photo", "profiles", "q", "query", "re", "replay", "s", "setup", "t", "tools", "v", "version", "video", "-I", "-add-shell-context", "-asc", "-chat-model", "-cm", "-dir-reply", "-dre", "-g", "-glob", "-i", "-p", "-pd", "-photo-dir", "-photo-model", "-photo-prefix", "-pm", "-pp", "-profile", "-profile-path", "-prp", "-r", "-raw", "-re", "-replace", "-reply", "-t", "-tools", "-vd", "-video-dir", "-video-model", "-video-prefix", "-vm", "-vp"},
				wantKinds:   repeatKind(completionResultKindPlain, 55),
				wantReplace: "",
			},
			{
				name:        "dash completes global flags",
				line:        []string{"clai", "-"},
				wantValues:  []string{"-I", "-add-shell-context", "-asc", "-chat-model", "-cm", "-dir-reply", "-dre", "-g", "-glob", "-i", "-p", "-pd", "-photo-dir", "-photo-model", "-photo-prefix", "-pm", "-pp", "-profile", "-profile-path", "-prp", "-r", "-raw", "-re", "-replace", "-reply", "-t", "-tools", "-vd", "-video-dir", "-video-model", "-video-prefix", "-vm", "-vp"},
				wantKinds:   repeatKind(completionResultKindPlain, 33),
				wantReplace: "-",
			},
			{
				name:        "chat subcommands",
				line:        []string{"clai", "chat", ""},
				wantValues:  []string{"continue", "delete", "help", "list"},
				wantKinds:   repeatKind(completionResultKindPlain, 4),
				wantReplace: "",
			},
			{
				name:        "tools lists tool names",
				line:        []string{"clai", "tools", ""},
				wantValues:  []string{"file_tree", "find", "rg"},
				wantKinds:   repeatKind(completionResultKindPlain, 3),
				wantReplace: "",
			},
			{
				name:        "tools flag values",
				line:        []string{"clai", "-t", ""},
				wantValues:  []string{"file_tree", "find", "rg"},
				wantKinds:   repeatKind(completionResultKindPlain, 3),
				wantReplace: "",
			},
			{
				name:        "tools flag comma separated",
				line:        []string{"clai", "-t", "rg,fi"},
				wantValues:  []string{"rg,file_tree", "rg,find"},
				wantKinds:   repeatKind(completionResultKindPlain, 2),
				wantReplace: "rg,fi",
			},
			{
				name:        "profile values",
				line:        []string{"clai", "-p", "pr"},
				wantValues:  []string{"prod", "project"},
				wantKinds:   repeatKind(completionResultKindPlain, 2),
				wantReplace: "pr",
			},
			{
				name:        "long profile values",
				line:        []string{"clai", "-profile", "pr"},
				wantValues:  []string{"prod", "project"},
				wantKinds:   repeatKind(completionResultKindPlain, 2),
				wantReplace: "pr",
			},
			{
				name:        "shell context values",
				line:        []string{"clai", "-asc", "mi"},
				wantValues:  []string{"minimal", "mixed"},
				wantKinds:   repeatKind(completionResultKindPlain, 2),
				wantReplace: "mi",
			},
			{
				name:        "prompt commands stop structural completion",
				line:        []string{"clai", "q", "hello"},
				wantValues:  nil,
				wantKinds:   nil,
				wantReplace: "hello",
			},
			{
				name:        "unknown command is tolerated",
				line:        []string{"clai", "wat"},
				wantValues:  nil,
				wantKinds:   nil,
				wantReplace: "wat",
			},
			{
				name:        "unknown flag is tolerated",
				line:        []string{"clai", "-wat"},
				wantValues:  nil,
				wantKinds:   nil,
				wantReplace: "-wat",
			},
			{
				name:        "profile path returns file kind",
				line:        []string{"clai", "-prp", ""},
				wantValues:  []string{"__files__"},
				wantKinds:   []completionResultKind{completionResultKindFile},
				wantReplace: "",
			},
			{
				name:        "long profile path returns file kind",
				line:        []string{"clai", "-profile-path", ""},
				wantValues:  []string{"__files__"},
				wantKinds:   []completionResultKind{completionResultKindFile},
				wantReplace: "",
			},
			{
				name:        "directory flags return dir kind",
				line:        []string{"clai", "-vd", ""},
				wantValues:  []string{"__dirs__"},
				wantKinds:   []completionResultKind{completionResultKindDir},
				wantReplace: "",
			},
			{
				name:        "long directory flags return dir kind",
				line:        []string{"clai", "-video-dir", ""},
				wantValues:  []string{"__dirs__"},
				wantKinds:   []completionResultKind{completionResultKindDir},
				wantReplace: "",
			},
		}

		for _, tc := range testCases {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()

				got := engine.Complete(completionRequest{Args: tc.line})
				if got.ReplaceToken != tc.wantReplace {
					t.Fatalf("replace token: got %q want %q", got.ReplaceToken, tc.wantReplace)
				}

				gotValues := make([]string, 0, len(got.Items))
				gotKinds := make([]completionResultKind, 0, len(got.Items))
				for _, item := range got.Items {
					gotValues = append(gotValues, item.Value)
					gotKinds = append(gotKinds, item.Kind)
				}

				if len(gotValues) == 0 && tc.wantValues == nil {
					gotValues = nil
				}
				if len(gotKinds) == 0 && tc.wantKinds == nil {
					gotKinds = nil
				}

				if !reflect.DeepEqual(gotValues, tc.wantValues) {
					t.Fatalf("values: got %v want %v", gotValues, tc.wantValues)
				}
				if !reflect.DeepEqual(gotKinds, tc.wantKinds) {
					t.Fatalf("kinds: got %v want %v", gotKinds, tc.wantKinds)
				}
			})
		}
	})
}

func TestCompletionEngineComplete_CommandShortcuts(t *testing.T) {
	t.Parallel()

	engine := newCompletionEngine(completionData{})

	testCases := []struct {
		name     string
		prefix   string
		wantSeen string
	}{
		{name: "help short alias is suggested", prefix: "h", wantSeen: "h"},
		{name: "setup short alias is suggested", prefix: "s", wantSeen: "s"},
		{name: "query short alias is suggested", prefix: "q", wantSeen: "q"},
		{name: "photo short alias is suggested", prefix: "p", wantSeen: "p"},
		{name: "video short alias is suggested", prefix: "v", wantSeen: "v"},
		{name: "chat short alias is suggested", prefix: "c", wantSeen: "c"},
		{name: "tools short alias is suggested", prefix: "t", wantSeen: "t"},
		{name: "replay short alias is suggested", prefix: "re", wantSeen: "re"},
		{name: "glob short alias is suggested", prefix: "g", wantSeen: "g"},
		{name: "glob long command is suggested", prefix: "g", wantSeen: "glob"},
		{name: "query long command is suggested", prefix: "q", wantSeen: "query"},
		{name: "chat long command is suggested", prefix: "c", wantSeen: "chat"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := engine.Complete(completionRequest{Args: []string{"clai", tc.prefix}})
			gotValues := make([]string, 0, len(got.Items))
			for _, item := range got.Items {
				gotValues = append(gotValues, item.Value)
			}

			if !containsString(gotValues, tc.wantSeen) {
				t.Fatalf("completion values %v do not contain %q", gotValues, tc.wantSeen)
			}
		})
	}
}

func TestCompletionEngineComplete_FlagShortcuts(t *testing.T) {
	t.Parallel()

	engine := newCompletionEngine(completionData{})

	testCases := []struct {
		name     string
		prefix   string
		wantSeen string
	}{
		{name: "chat model short flag is suggested", prefix: "-cm", wantSeen: "-cm"},
		{name: "chat model long flag is suggested", prefix: "-chat", wantSeen: "-chat-model"},
		{name: "photo model short flag is suggested", prefix: "-pm", wantSeen: "-pm"},
		{name: "photo model long flag is suggested", prefix: "-photo-m", wantSeen: "-photo-model"},
		{name: "photo dir short flag is suggested", prefix: "-pd", wantSeen: "-pd"},
		{name: "photo dir long flag is suggested", prefix: "-photo-d", wantSeen: "-photo-dir"},
		{name: "photo prefix short flag is suggested", prefix: "-pp", wantSeen: "-pp"},
		{name: "photo prefix long flag is suggested", prefix: "-photo-p", wantSeen: "-photo-prefix"},
		{name: "video model short flag is suggested", prefix: "-vm", wantSeen: "-vm"},
		{name: "video model long flag is suggested", prefix: "-video-m", wantSeen: "-video-model"},
		{name: "video dir short flag is suggested", prefix: "-vd", wantSeen: "-vd"},
		{name: "video dir long flag is suggested", prefix: "-video-d", wantSeen: "-video-dir"},
		{name: "video prefix short flag is suggested", prefix: "-vp", wantSeen: "-vp"},
		{name: "video prefix long flag is suggested", prefix: "-video-p", wantSeen: "-video-prefix"},
		{name: "glob short flag is suggested", prefix: "-g", wantSeen: "-g"},
		{name: "glob long flag is suggested", prefix: "-gl", wantSeen: "-glob"},
		{name: "profile short flag is suggested", prefix: "-p", wantSeen: "-p"},
		{name: "profile long flag is suggested", prefix: "-prof", wantSeen: "-profile"},
		{name: "profile path short flag is suggested", prefix: "-prp", wantSeen: "-prp"},
		{name: "profile path long flag is suggested", prefix: "-profile-pa", wantSeen: "-profile-path"},
		{name: "replace short flag is suggested", prefix: "-I", wantSeen: "-I"},
		{name: "replace long flag is suggested", prefix: "-rep", wantSeen: "-replace"},
		{name: "stdin shortcut flag is suggested", prefix: "-i", wantSeen: "-i"},
		{name: "raw short flag is suggested", prefix: "-r", wantSeen: "-r"},
		{name: "raw long flag is suggested", prefix: "-ra", wantSeen: "-raw"},
		{name: "reply short flag is suggested", prefix: "-re", wantSeen: "-re"},
		{name: "reply long flag is suggested", prefix: "-reply", wantSeen: "-reply"},
		{name: "tools short flag is suggested", prefix: "-t", wantSeen: "-t"},
		{name: "tools long flag is suggested", prefix: "-too", wantSeen: "-tools"},
		{name: "append shell context short flag is suggested", prefix: "-asc", wantSeen: "-asc"},
		{name: "append shell context long flag is suggested", prefix: "-add-s", wantSeen: "-add-shell-context"},
		{name: "dir reply short flag is suggested", prefix: "-dre", wantSeen: "-dre"},
		{name: "dir reply long flag is suggested", prefix: "-dir-r", wantSeen: "-dir-reply"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := engine.Complete(completionRequest{Args: []string{"clai", tc.prefix}})
			gotValues := make([]string, 0, len(got.Items))
			for _, item := range got.Items {
				gotValues = append(gotValues, item.Value)
			}

			if !containsString(gotValues, tc.wantSeen) {
				t.Fatalf("completion values %v do not contain %q", gotValues, tc.wantSeen)
			}
		})
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

func TestCompletionEngineComplete_ModelValues(t *testing.T) {
	t.Parallel()

	engine := newCompletionEngine(completionData{
		Models: []string{"gpt-5.2", "gemini-2.0-flash", "or:openai/gpt-5.2"},
	})

	got := engine.Complete(completionRequest{Args: []string{"clai", "-cm", "g"}})

	gotValues := make([]string, 0, len(got.Items))
	gotKinds := make([]completionResultKind, 0, len(got.Items))
	for _, item := range got.Items {
		gotValues = append(gotValues, item.Value)
		gotKinds = append(gotKinds, item.Kind)
	}

	wantValues := []string{"gemini-2.0-flash", "gpt-5.2"}
	wantKinds := []completionResultKind{completionResultKindPlain, completionResultKindPlain}
	if !reflect.DeepEqual(gotValues, wantValues) {
		t.Fatalf("values: got %v want %v", gotValues, wantValues)
	}
	if !reflect.DeepEqual(gotKinds, wantKinds) {
		t.Fatalf("kinds: got %v want %v", gotKinds, wantKinds)
	}
	if got.ReplaceToken != "g" {
		t.Fatalf("replace token: got %q want %q", got.ReplaceToken, "g")
	}
}

func repeatKind(kind completionResultKind, count int) []completionResultKind {
	out := make([]completionResultKind, 0, count)
	for range count {
		out = append(out, kind)
	}
	return out
}

func containsString(values []string, want string) bool {
	return slices.Contains(values, want)
}
