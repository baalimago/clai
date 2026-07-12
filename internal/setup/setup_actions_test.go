package setup

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/baalimago/clai/internal/tools"
	"github.com/baalimago/clai/internal/utils"
	pub_models "github.com/baalimago/clai/pkg/text/models"
)

func TestCastPrimitive(t *testing.T) {
	tests := []struct {
		name string

		input any
		want  any
	}{
		{"String to int", "42", 42},
		{"String to float", "3.14", 3.14},
		{"String remains string", "hello", "hello"},
		{"Boolean true", "true", true},
		{"Boolean false", "false", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := castPrimitive(tt.input)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("castPrimitive() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestReconfigureWithEditor(t *testing.T) {
	tests := []struct {
		name    string
		content string

		editor  string
		wantErr bool
	}{
		{
			name:    "No editor set",
			editor:  "",
			content: "",
			wantErr: true,
		},
		{
			name:    "Valid editor",
			editor:  "echo",
			content: "{\"test\": \"value\"}",
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			tmpFile := filepath.Join(tmpDir, "config.json")
			if err := os.WriteFile(tmpFile, []byte(tt.content), 0o644); err != nil {
				t.Fatal(err)
			}
			oldEditor := os.Getenv("EDITOR")
			defer os.Setenv("EDITOR", oldEditor)
			os.Setenv("EDITOR", tt.editor)

			cfg := config{
				name:     "test",
				filePath: tmpFile,
			}
			err := actionReconfigureWithEditor(cfg)
			if (err != nil) != tt.wantErr {
				t.Errorf("reconfigureWithEditor() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestClaiConfigDirFromPath(t *testing.T) {
	tests := []struct {
		name     string
		filePath string
		want     string
	}{
		{
			name:     "profile file",
			filePath: "/home/user/.clai/profiles/myprof.json",
			want:     "/home/user/.clai",
		},
		{
			name:     "shell context file",
			filePath: "/home/user/.clai/shellContexts/minimal.json",
			want:     "/home/user/.clai",
		},
		{
			name:     "mcp server file",
			filePath: "/home/user/.clai/mcpServers/everything.json",
			want:     "/home/user/.clai",
		},
		{
			name:     "top-level config file",
			filePath: "/home/user/.clai/textConfig.json",
			want:     "/home/user/.clai",
		},
		{
			name:     "model file in config dir",
			filePath: "/home/user/.clai/openai_gpt_gpt-4.1.json",
			want:     "/home/user/.clai",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := claiConfigDirFromPath(tt.filePath)
			if got != tt.want {
				t.Fatalf("claiConfigDirFromPath(%q) = %q, want %q", tt.filePath, got, tt.want)
			}
		})
	}
}

func TestGetAvailableModels(t *testing.T) {
	t.Run("returns canonical model strings from config dir", func(t *testing.T) {
		dir := t.TempDir()
		for _, name := range []string{"openai_gpt_gpt-4.1.json", "anthropic_claude_sonnet-4.json", "openrouter_chat_gpt-4.1.json", "textConfig.json", "photoConfig.json", "videoConfig.json"} {
			if err := os.WriteFile(filepath.Join(dir, name), []byte(`{}`), 0o644); err != nil {
				t.Fatalf("WriteFile(%q): %v", name, err)
			}
		}

		got, err := getAvailableModels(dir)
		if err != nil {
			t.Fatalf("getAvailableModels(): %v", err)
		}

		if len(got) != 3 {
			t.Fatalf("getAvailableModels() = %v, want 3 unique models", got)
		}
		for _, wantModel := range []string{"gpt-4.1", "sonnet-4", "or:gpt-4.1"} {
			found := false
			for _, g := range got {
				if g == wantModel {
					found = true
					break
				}
			}
			if !found {
				t.Fatalf("getAvailableModels() = %v, missing %q", got, wantModel)
			}
		}
	})

	t.Run("skips files without vendor_family_version pattern", func(t *testing.T) {
		dir := t.TempDir()
		for _, name := range []string{"textConfig.json", "photoConfig.json", "skills.json"} {
			if err := os.WriteFile(filepath.Join(dir, name), []byte(`{}`), 0o644); err != nil {
				t.Fatalf("WriteFile(%q): %v", name, err)
			}
		}

		got, err := getAvailableModels(dir)
		if err != nil {
			t.Fatalf("getAvailableModels(): %v", err)
		}
		if len(got) != 0 {
			t.Fatalf("getAvailableModels() = %v, want []", got)
		}
	})
}

func TestGetAvailableShellContexts(t *testing.T) {
	t.Run("returns shell context names", func(t *testing.T) {
		dir := t.TempDir()
		shellCtxDir := filepath.Join(dir, "shellContexts")
		if err := os.MkdirAll(shellCtxDir, 0o755); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}
		for _, name := range []string{"minimal.json", "git.json", "full.json"} {
			if err := os.WriteFile(filepath.Join(shellCtxDir, name), []byte(`{}`), 0o644); err != nil {
				t.Fatalf("WriteFile(%q): %v", name, err)
			}
		}

		got, err := getAvailableShellContexts(dir)
		if err != nil {
			t.Fatalf("getAvailableShellContexts(): %v", err)
		}

		want := []string{"full", "git", "minimal"}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("getAvailableShellContexts() = %v, want %v", got, want)
		}
	})

	t.Run("returns empty when dir missing", func(t *testing.T) {
		dir := t.TempDir()
		got, err := getAvailableShellContexts(dir)
		if err != nil {
			t.Fatalf("getAvailableShellContexts(): %v", err)
		}
		if len(got) != 0 {
			t.Fatalf("getAvailableShellContexts() = %v, want []", got)
		}
	})
}

func TestGetModelValue_SelectsFromTable(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"openai_gpt_gpt-4.1.json", "anthropic_claude_sonnet-4.json"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(`{}`), 0o644); err != nil {
			t.Fatalf("WriteFile(%q): %v", name, err)
		}
	}

	// Simulate user selecting index 0 (filename "anthropic_claude_sonnet-4.json" → display "sonnet-4")
	restore := utils.UseReadUserInputForTests(func() (string, error) {
		return "0", nil
	})
	defer restore()

	got, err := getModelValue("gpt-5.2", dir)
	if err != nil {
		t.Fatalf("getModelValue(): %v", err)
	}
	if got != "sonnet-4" {
		t.Fatalf("getModelValue() = %q, want %q", got, "sonnet-4")
	}
}

func TestGetModelValue_EmptyReturnsCurrent(t *testing.T) {
	dir := t.TempDir()
	// Only textConfig — no actual model files
	if err := os.WriteFile(filepath.Join(dir, "textConfig.json"), []byte(`{}`), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	got, err := getModelValue("gpt-5.2", dir)
	if err != nil {
		t.Fatalf("getModelValue(): %v", err)
	}
	if got != "gpt-5.2" {
		t.Fatalf("getModelValue() = %q, want %q (current kept)", got, "gpt-5.2")
	}
}

func TestGetShellContextValue_SelectsFromTable(t *testing.T) {
	dir := t.TempDir()
	shellCtxDir := filepath.Join(dir, "shellContexts")
	if err := os.MkdirAll(shellCtxDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	for _, name := range []string{"minimal.json", "git.json"} {
		if err := os.WriteFile(filepath.Join(shellCtxDir, name), []byte(`{}`), 0o644); err != nil {
			t.Fatalf("WriteFile(%q): %v", name, err)
		}
	}

	// Simulate user selecting index 0 ("git" - alphabetical first)
	restore := utils.UseReadUserInputForTests(func() (string, error) {
		return "0", nil
	})
	defer restore()

	got, err := getShellContextValue("minimal", dir)
	if err != nil {
		t.Fatalf("getShellContextValue(): %v", err)
	}
	if got != "git" {
		t.Fatalf("getShellContextValue() = %q, want %q", got, "git")
	}
}

func TestGetShellContextValue_EmptyReturnsCurrent(t *testing.T) {
	dir := t.TempDir()
	// No shellContexts directory
	got, err := getShellContextValue("minimal", dir)
	if err != nil {
		t.Fatalf("getShellContextValue(): %v", err)
	}
	if got != "minimal" {
		t.Fatalf("getShellContextValue() = %q, want %q (current kept)", got, "minimal")
	}
}

func TestGetNewValue_DispatchesToModelSelector(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "anthropic_claude_sonnet-4.json"), []byte(`{}`), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	restore := utils.UseReadUserInputForTests(func() (string, error) {
		return "0", nil
	})
	defer restore()

	// Only works with 3-part vendor_family_version names
	got, err := getNewValue("model", "gpt-5.2", dir)
	if err != nil {
		t.Fatalf("getNewValue(model): %v", err)
	}
	if got != "sonnet-4" {
		t.Fatalf("getNewValue(model) = %q, want %q", got, "sonnet-4")
	}
}

func TestGetNewValue_DispatchesToShellContextSelector(t *testing.T) {
	dir := t.TempDir()
	shellCtxDir := filepath.Join(dir, "shellContexts")
	if err := os.MkdirAll(shellCtxDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(shellCtxDir, "git.json"), []byte(`{}`), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	restore := utils.UseReadUserInputForTests(func() (string, error) {
		return "0", nil
	})
	defer restore()

	got, err := getNewValue("shell_context", "minimal", dir)
	if err != nil {
		t.Fatalf("getNewValue(shell_context): %v", err)
	}
	if got != "git" {
		t.Fatalf("getNewValue(shell_context) = %q, want %q", got, "git")
	}
}

type mockTool struct {
	name string
	desc string
}

func (m mockTool) Call(pub_models.Input) (string, error) { return "", nil }

func (m mockTool) Specification() pub_models.Specification {
	return pub_models.Specification{Name: m.name, Description: m.desc}
}

func registerMockTools(names ...string) {
	for i, name := range names {
		tools.Registry.Set(name, mockTool{
			name: name,
			desc: fmt.Sprintf("description for %s (%d)", name, i),
		})
	}
}

func TestGetToolsValue_UsesSelectFromTable(t *testing.T) {
	tools.Registry.Reset()
	tools.Init()
	tools.Registry.Reset()
	t.Cleanup(func() { tools.Registry.Reset() })

	registerMockTools("cat", "file_tree", "ls", "rg", "go_test")

	t.Run("single index selection", func(t *testing.T) {
		inputs := []string{"2", "d"}
		inputIdx := 0
		restore := utils.UseReadUserInputForTests(func() (string, error) {
			if inputIdx >= len(inputs) {
				return "", io.EOF
			}
			ret := inputs[inputIdx]
			inputIdx++
			return ret, nil
		})
		defer restore()

		got, err := getToolsValue([]any{})
		if err != nil {
			t.Fatalf("getToolsValue(): %v", err)
		}

		want := []string{"go_test"}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("getToolsValue() = %v, want %v", got, want)
		}
	})

	t.Run("multi index selection with comma", func(t *testing.T) {
		inputs := []string{"0,4", "d"}
		inputIdx := 0
		restore := utils.UseReadUserInputForTests(func() (string, error) {
			if inputIdx >= len(inputs) {
				return "", io.EOF
			}
			ret := inputs[inputIdx]
			inputIdx++
			return ret, nil
		})
		defer restore()

		got, err := getToolsValue([]any{})
		if err != nil {
			t.Fatalf("getToolsValue(): %v", err)
		}

		want := []string{"cat", "rg"}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("getToolsValue() = %v, want %v", got, want)
		}
	})

	t.Run("range selection", func(t *testing.T) {
		inputs := []string{"0:2", "d"}
		inputIdx := 0
		restore := utils.UseReadUserInputForTests(func() (string, error) {
			if inputIdx >= len(inputs) {
				return "", io.EOF
			}
			ret := inputs[inputIdx]
			inputIdx++
			return ret, nil
		})
		defer restore()

		got, err := getToolsValue([]any{})
		if err != nil {
			t.Fatalf("getToolsValue(): %v", err)
		}

		want := []string{"cat", "file_tree", "go_test"}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("getToolsValue() = %v, want %v", got, want)
		}
	})

	t.Run("toggle off a selected tool", func(t *testing.T) {
		inputs := []string{"2", "2", "d"}
		inputIdx := 0
		restore := utils.UseReadUserInputForTests(func() (string, error) {
			if inputIdx >= len(inputs) {
				return "", io.EOF
			}
			ret := inputs[inputIdx]
			inputIdx++
			return ret, nil
		})
		defer restore()

		got, err := getToolsValue([]any{})
		if err != nil {
			t.Fatalf("getToolsValue(): %v", err)
		}

		if len(got) != 0 {
			t.Fatalf("getToolsValue() = %v, want empty (toggled on then off)", got)
		}
	})

	t.Run("clear all action", func(t *testing.T) {
		inputs := []string{"c", "d"}
		inputIdx := 0
		restore := utils.UseReadUserInputForTests(func() (string, error) {
			if inputIdx >= len(inputs) {
				return "", io.EOF
			}
			ret := inputs[inputIdx]
			inputIdx++
			return ret, nil
		})
		defer restore()

		got, err := getToolsValue([]any{"rg", "cat"})
		if err != nil {
			t.Fatalf("getToolsValue(): %v", err)
		}

		if len(got) != 0 {
			t.Fatalf("getToolsValue() = %v, want empty slice (clear all)", got)
		}
	})

	t.Run("back keeps current value", func(t *testing.T) {
		restore := utils.UseReadUserInputForTests(func() (string, error) {
			return "b", nil
		})
		defer restore()

		current := []any{"rg", "ls"}
		got, err := getToolsValue(current)
		if err != nil {
			t.Fatalf("getToolsValue(): %v", err)
		}

		sort.Strings(got)
		want := []string{"ls", "rg"}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("getToolsValue() = %v, want %v (back keeps current)", got, want)
		}
	})

	t.Run("quit propagates error", func(t *testing.T) {
		restore := utils.UseReadUserInputForTests(func() (string, error) {
			return "q", nil
		})
		defer restore()

		_, err := getToolsValue([]any{})
		if !errors.Is(err, utils.ErrUserInitiatedExit) {
			t.Fatalf("getToolsValue() error = %v, want ErrUserInitiatedExit", err)
		}
	})

	t.Run("all action selects every tool", func(t *testing.T) {
		inputs := []string{"a", "d"}
		inputIdx := 0
		restore := utils.UseReadUserInputForTests(func() (string, error) {
			if inputIdx >= len(inputs) {
				return "", io.EOF
			}
			ret := inputs[inputIdx]
			inputIdx++
			return ret, nil
		})
		defer restore()

		got, err := getToolsValue([]any{"rg"})
		if err != nil {
			t.Fatalf("getToolsValue(): %v", err)
		}

		want := []string{"cat", "file_tree", "go_test", "ls", "rg"}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("getToolsValue() = %v, want %v (all tools)", got, want)
		}
	})

	t.Run("non-slice input returns empty", func(t *testing.T) {
		got, err := getToolsValue("not a slice")
		if err != nil {
			t.Fatalf("getToolsValue(): %v", err)
		}
		if len(got) != 0 {
			t.Fatalf("getToolsValue() = %v, want empty slice for non-slice input", got)
		}
	})

	t.Run("marked tools show X prefix", func(t *testing.T) {
		currentlySelected := map[string]bool{"cat": true, "rg": true}

		rowFormatter := toolRowFormatter(currentlySelected)

		catRow, err := rowFormatter(0, "cat")
		if err != nil {
			t.Fatalf("rowFormatter(cat): %v", err)
		}
		if !strings.HasPrefix(catRow, "[X]") {
			t.Fatalf("rowFormatter(cat) = %q, want [X] prefix", catRow)
		}

		ftRow, err := rowFormatter(1, "file_tree")
		if err != nil {
			t.Fatalf("rowFormatter(file_tree): %v", err)
		}
		if !strings.HasPrefix(ftRow, "[ ]") {
			t.Fatalf("rowFormatter(file_tree) = %q, want [ ] prefix", ftRow)
		}
	})
}

func TestInteractiveReconfigure_FieldSelection(t *testing.T) {
	t.Run("selects field by index and edits it", func(t *testing.T) {
		dir := t.TempDir()
		cfgPath := filepath.Join(dir, "test.json")
		original := `{"name":"oldname","prompt":"hello"}`
		if err := os.WriteFile(cfgPath, []byte(original), 0o644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}

		// Input sequence:
		// 1. Select field index 0 ("name", alphabetically first)
		// 2. Enter new value for name
		// 3. Select [d]one
		inputs := []string{"0", "newname", "d"}
		inputIdx := 0
		restore := utils.UseReadUserInputForTests(func() (string, error) {
			if inputIdx >= len(inputs) {
				return "", io.EOF
			}
			ret := inputs[inputIdx]
			inputIdx++
			return ret, nil
		})
		defer restore()

		err := interractiveReconfigure(config{name: "test.json", filePath: cfgPath}, []byte(original))
		if err != nil {
			t.Fatalf("interractiveReconfigure: %v", err)
		}

		got, err := os.ReadFile(cfgPath)
		if err != nil {
			t.Fatalf("ReadFile: %v", err)
		}

		var jzon map[string]any
		if err := json.Unmarshal(got, &jzon); err != nil {
			t.Fatalf("Unmarshal: %v", err)
		}

		if jzon["name"] != "newname" {
			t.Fatalf("name = %q, want %q", jzon["name"], "newname")
		}
		if jzon["prompt"] != "hello" {
			t.Fatalf("prompt = %q, want %q (should be unchanged)", jzon["prompt"], "hello")
		}
	})

	t.Run("done without editing preserves config", func(t *testing.T) {
		dir := t.TempDir()
		cfgPath := filepath.Join(dir, "test.json")
		original := `{"name":"oldname","prompt":"hello"}`
		if err := os.WriteFile(cfgPath, []byte(original), 0o644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}

		restore := utils.UseReadUserInputForTests(func() (string, error) {
			return "d", nil
		})
		defer restore()

		err := interractiveReconfigure(config{name: "test.json", filePath: cfgPath}, []byte(original))
		if err != nil {
			t.Fatalf("interractiveReconfigure: %v", err)
		}

		got, err := os.ReadFile(cfgPath)
		if err != nil {
			t.Fatalf("ReadFile: %v", err)
		}

		var jzon map[string]any
		if err := json.Unmarshal(got, &jzon); err != nil {
			t.Fatalf("Unmarshal: %v", err)
		}

		if jzon["name"] != "oldname" {
			t.Fatalf("name = %q, want %q", jzon["name"], "oldname")
		}
	})

	t.Run("multiple field edits", func(t *testing.T) {
		dir := t.TempDir()
		cfgPath := filepath.Join(dir, "test.json")
		original := `{"name":"oldname","prompt":"hello"}`
		if err := os.WriteFile(cfgPath, []byte(original), 0o644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}

		// Edit name: select 0, type "newname"
		// Then edit prompt: select 1, type "world"
		// Then done: "d"
		inputs := []string{"0", "newname", "1", "world", "d"}
		inputIdx := 0
		restore := utils.UseReadUserInputForTests(func() (string, error) {
			if inputIdx >= len(inputs) {
				return "", io.EOF
			}
			ret := inputs[inputIdx]
			inputIdx++
			return ret, nil
		})
		defer restore()

		err := interractiveReconfigure(config{name: "test.json", filePath: cfgPath}, []byte(original))
		if err != nil {
			t.Fatalf("interractiveReconfigure: %v", err)
		}

		got, err := os.ReadFile(cfgPath)
		if err != nil {
			t.Fatalf("ReadFile: %v", err)
		}

		var jzon map[string]any
		if err := json.Unmarshal(got, &jzon); err != nil {
			t.Fatalf("Unmarshal: %v", err)
		}

		if jzon["name"] != "newname" {
			t.Fatalf("name = %q, want %q", jzon["name"], "newname")
		}
		if jzon["prompt"] != "world" {
			t.Fatalf("prompt = %q, want %q", jzon["prompt"], "world")
		}
	})
}

func TestActionCopy(t *testing.T) {
	t.Run("successful copy creates file and returns new config", func(t *testing.T) {
		dir := t.TempDir()
		srcPath := filepath.Join(dir, "source.json")
		content := `{"key": "value"}`
		if err := os.WriteFile(srcPath, []byte(content), 0o644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}

		restore := utils.UseReadUserInputForTests(func() (string, error) {
			return "mycopy", nil
		})
		defer restore()

		cfg := config{name: "source.json", filePath: srcPath}
		newCfg, err := actionCopy(cfg)
		if err != nil {
			t.Fatalf("actionCopy(): %v", err)
		}

		wantPath := filepath.Join(dir, "mycopy.json")
		if newCfg.filePath != wantPath {
			t.Fatalf("newCfg.filePath = %q, want %q", newCfg.filePath, wantPath)
		}
		if newCfg.name != "mycopy" {
			t.Fatalf("newCfg.name = %q, want %q", newCfg.name, "mycopy")
		}

		copied, err := os.ReadFile(wantPath)
		if err != nil {
			t.Fatalf("ReadFile(copy): %v", err)
		}
		if string(copied) != content {
			t.Fatalf("copy content = %q, want %q", string(copied), content)
		}
	})

	t.Run("empty name returns error", func(t *testing.T) {
		dir := t.TempDir()
		srcPath := filepath.Join(dir, "source.json")
		if err := os.WriteFile(srcPath, []byte(`{}`), 0o644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}

		restore := utils.UseReadUserInputForTests(func() (string, error) {
			return "", nil
		})
		defer restore()

		_, err := actionCopy(config{name: "source.json", filePath: srcPath})
		if err == nil {
			t.Fatal("expected error for empty name, got nil")
		}
	})

	t.Run("target already exists returns error", func(t *testing.T) {
		dir := t.TempDir()
		srcPath := filepath.Join(dir, "source.json")
		if err := os.WriteFile(srcPath, []byte(`{}`), 0o644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
		if err := os.WriteFile(filepath.Join(dir, "existing.json"), []byte(`{}`), 0o644); err != nil {
			t.Fatalf("WriteFile(existing): %v", err)
		}

		restore := utils.UseReadUserInputForTests(func() (string, error) {
			return "existing", nil
		})
		defer restore()

		_, err := actionCopy(config{name: "source.json", filePath: srcPath})
		if err == nil || !strings.Contains(err.Error(), "already exists") {
			t.Fatalf("expected 'already exists' error, got: %v", err)
		}
	})
}
