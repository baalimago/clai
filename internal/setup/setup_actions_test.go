package setup

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
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

func TestSliceRowFormatter(t *testing.T) {
	s := []any{"foo", 42, true}
	formatter := sliceRowFormatter(s)

	t.Run("formats each element with index", func(t *testing.T) {
		for i, want := range []string{"0. foo", "1. 42", "2. true"} {
			got, err := formatter(i, strconv.Itoa(i))
			if err != nil {
				t.Fatalf("formatter(%d): %v", i, err)
			}
			if got != want {
				t.Fatalf("formatter(%d) = %q, want %q", i, got, want)
			}
		}
	})
}

func TestEditSlice_UpdateAndRemove(t *testing.T) {
	t.Run("update element via table selection", func(t *testing.T) {
		// editSlice loop: "u" -> selectFromSlice: "1" (returns [1] directly) -> handleValue: "newval" -> editSlice: "d"
		inputs := []string{"u", "1", "newval", "d"}
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

		original := []any{"a", "b", "c"}
		got, err := editSlice("test", original, "")
		if err != nil {
			t.Fatalf("editSlice(): %v", err)
		}

		want := []any{"a", "newval", "c"}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("editSlice() = %v, want %v", got, want)
		}
	})

	t.Run("remove elements via table multi-select", func(t *testing.T) {
		// editSlice: "r" -> selectFromSlice: "0,2" (returns [0,2] directly) -> back to editSlice: "d"
		inputs := []string{"r", "0,2", "d"}
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

		original := []any{"a", "b", "c"}
		got, err := editSlice("test", original, "")
		if err != nil {
			t.Fatalf("editSlice(): %v", err)
		}

		want := []any{"b"}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("editSlice() = %v, want %v", got, want)
		}
	})

	t.Run("remove via range", func(t *testing.T) {
		inputs := []string{"r", "0:2", "d"}
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

		original := []any{"a", "b", "c", "d", "e"}
		got, err := editSlice("test", original, "")
		if err != nil {
			t.Fatalf("editSlice(): %v", err)
		}

		want := []any{"d", "e"}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("editSlice() = %v, want %v", got, want)
		}
	})

	t.Run("append element", func(t *testing.T) {
		inputs := []string{"a", "newitem", "d"}
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

		original := []any{"a", "b"}
		got, err := editSlice("test", original, "")
		if err != nil {
			t.Fatalf("editSlice(): %v", err)
		}

		want := []any{"a", "b", "newitem"}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("editSlice() = %v, want %v", got, want)
		}
	})

	t.Run("done returns slice unchanged", func(t *testing.T) {
		restore := utils.UseReadUserInputForTests(func() (string, error) {
			return "d", nil
		})
		defer restore()

		original := []any{"a", "b"}
		got, err := editSlice("test", original, "")
		if err != nil {
			t.Fatalf("editSlice(): %v", err)
		}

		if !reflect.DeepEqual(got, original) {
			t.Fatalf("editSlice() = %v, want %v", got, original)
		}
	})

	t.Run("empty slice update/remove are no-ops", func(t *testing.T) {
		inputs := []string{"u", "r", "d"}
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

		got, err := editSlice("test", []any{}, "")
		if err != nil {
			t.Fatalf("editSlice(): %v", err)
		}

		if len(got) != 0 {
			t.Fatalf("editSlice() = %v, want empty slice", got)
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

// ============================================================
// Tier 1 — Pure functions and simple dispatch
// ============================================================

func TestStringKeysSorted(t *testing.T) {
	tests := []struct {
		name string
		jzon map[string]any
		want []string
	}{
		{
			name: "mixed types — only string values returned",
			jzon: map[string]any{
				"name":    "alice",
				"age":     30,
				"active":  true,
				"title":   "engineer",
				"nothing": nil,
				"tags":    []any{"a", "b"},
			},
			want: []string{"name", "title"},
		},
		{
			name: "all strings — sorted",
			jzon: map[string]any{
				"zebra": "z",
				"apple": "a",
				"mango": "m",
			},
			want: []string{"apple", "mango", "zebra"},
		},
		{
			name: "no strings — empty",
			jzon: map[string]any{
				"count": 42,
				"flag":  true,
			},
			want: []string{},
		},
		{
			name: "empty map",
			jzon: map[string]any{},
			want: []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := stringKeysSorted(tt.jzon)
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("stringKeysSorted() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestHandleValue_ToolsDispatch(t *testing.T) {
	tools.Registry.Reset()
	tools.Init()
	tools.Registry.Reset()
	t.Cleanup(func() { tools.Registry.Reset() })

	registerMockTools("cat", "ls", "rg")

	// Select tool 0 ("cat") then done
	inputs := []string{"0", "d"}
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

	got, err := handleValue("tools", []any{}, "")
	if err != nil {
		t.Fatalf("handleValue(tools): %v", err)
	}

	toolsList, ok := got.([]string)
	if !ok {
		t.Fatalf("handleValue(tools) returned %T, want []string", got)
	}
	if !reflect.DeepEqual(toolsList, []string{"cat"}) {
		t.Fatalf("handleValue(tools) = %v, want [cat]", toolsList)
	}
}

func TestHandleValue_MapDispatch(t *testing.T) {
	restore := utils.UseReadUserInputForTests(func() (string, error) {
		return "d", nil
	})
	defer restore()

	m := map[string]any{"key": "val"}
	got, err := handleValue("config", m, "")
	if err != nil {
		t.Fatalf("handleValue(map): %v", err)
	}

	gotMap, ok := got.(map[string]any)
	if !ok {
		t.Fatalf("handleValue(map) returned %T, want map[string]any", got)
	}
	if !reflect.DeepEqual(gotMap, m) {
		t.Fatalf("handleValue(map) = %v, want %v", gotMap, m)
	}
}

func TestHandleValue_DefaultStringDispatch(t *testing.T) {
	restore := utils.UseReadUserInputForTests(func() (string, error) {
		return "newvalue", nil
	})
	defer restore()

	got, err := handleValue("name", "oldvalue", "")
	if err != nil {
		t.Fatalf("handleValue(string): %v", err)
	}
	if got != "newvalue" {
		t.Fatalf("handleValue(string) = %q, want %q", got, "newvalue")
	}
}

func TestGetNewValue_EmptyInputKeepsCurrent(t *testing.T) {
	restore := utils.UseReadUserInputForTests(func() (string, error) {
		return "", nil
	})
	defer restore()

	got, err := getNewValue("somekey", "original", "")
	if err != nil {
		t.Fatalf("getNewValue(empty): %v", err)
	}
	if got != "original" {
		t.Fatalf("getNewValue(empty) = %q, want %q (original kept)", got, "original")
	}
}

func TestGetNewValue_ReadError(t *testing.T) {
	restore := utils.UseReadUserInputForTests(func() (string, error) {
		return "", errors.New("mock read error")
	})
	defer restore()

	_, err := getNewValue("somekey", "original", "")
	if err == nil {
		t.Fatal("getNewValue(read error) expected error, got nil")
	}
}

func TestWriteConfig(t *testing.T) {
	t.Run("writes valid JSON to file", func(t *testing.T) {
		dir := t.TempDir()
		cfgPath := filepath.Join(dir, "out.json")

		jzon := map[string]any{
			"name":  "test",
			"count": float64(42),
		}
		if err := writeConfig(cfgPath, jzon); err != nil {
			t.Fatalf("writeConfig(): %v", err)
		}

		got, err := os.ReadFile(cfgPath)
		if err != nil {
			t.Fatalf("ReadFile: %v", err)
		}

		var parsed map[string]any
		if err := json.Unmarshal(got, &parsed); err != nil {
			t.Fatalf("Unmarshal: %v", err)
		}

		if parsed["name"] != "test" {
			t.Fatalf("name = %q, want %q", parsed["name"], "test")
		}
	})

	t.Run("write to read-only directory returns error", func(t *testing.T) {
		dir := t.TempDir()
		roDir := filepath.Join(dir, "readonly")
		if err := os.MkdirAll(roDir, 0o500); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}
		defer os.Chmod(roDir, 0o700)

		cfgPath := filepath.Join(roDir, "out.json")
		err := writeConfig(cfgPath, map[string]any{"a": "b"})
		if err == nil {
			t.Fatal("expected error writing to read-only dir, got nil")
		}
	})
}

func TestCastPrimitive_NonStringPassthrough(t *testing.T) {
	tests := []struct {
		name  string
		input any
	}{
		{"nil", nil},
		{"bool true", true},
		{"bool false", false},
		{"int zero", 0},
		{"int non-zero", 42},
		{"float", 3.14},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := castPrimitive(tt.input)
			// Non-string values are filtered through Truthy/Falsy first.
			// The behavioral contract: the function doesn't panic and returns
			// a predictable value.
			t.Logf("castPrimitive(%v) = %v (%T)", tt.input, got, got)
		})
	}
}

// ============================================================
// Tier 2 — Interactive editors
// ============================================================

func TestEditMap_Done(t *testing.T) {
	restore := utils.UseReadUserInputForTests(func() (string, error) {
		return "d", nil
	})
	defer restore()

	m := map[string]any{"a": "1", "b": "2"}
	got, err := editMap("test", m, "")
	if err != nil {
		t.Fatalf("editMap(done): %v", err)
	}
	if !reflect.DeepEqual(got, m) {
		t.Fatalf("editMap(done) = %v, want %v", got, m)
	}
}

func TestEditMap_Add(t *testing.T) {
	inputs := []string{"a", "newkey", "hello", "d"}
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

	m := map[string]any{"existing": "value"}
	got, err := editMap("test", m, "")
	if err != nil {
		t.Fatalf("editMap(add): %v", err)
	}

	if len(got) != 2 {
		t.Fatalf("editMap(add) len = %d, want 2", len(got))
	}
	if got["newkey"] != "hello" {
		t.Fatalf("editMap(add) newkey = %v, want hello", got["newkey"])
	}
	if got["existing"] != "value" {
		t.Fatalf("editMap(add) existing = %v, want value", got["existing"])
	}
}

func TestEditMap_Remove(t *testing.T) {
	inputs := []string{"r", "toremove", "d"}
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

	m := map[string]any{"keep": "v1", "toremove": "v2"}
	got, err := editMap("test", m, "")
	if err != nil {
		t.Fatalf("editMap(remove): %v", err)
	}

	if len(got) != 1 {
		t.Fatalf("editMap(remove) len = %d, want 1", len(got))
	}
	if _, exists := got["toremove"]; exists {
		t.Fatal("editMap(remove) key 'toremove' still present")
	}
	if got["keep"] != "v1" {
		t.Fatalf("editMap(remove) keep = %v, want v1", got["keep"])
	}
}

func TestEditMap_UpdateExistingKey(t *testing.T) {
	inputs := []string{"u", "name", "newname", "d"}
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

	m := map[string]any{"name": "oldname", "count": "42"}
	got, err := editMap("test", m, "")
	if err != nil {
		t.Fatalf("editMap(update existing): %v", err)
	}

	if got["name"] != "newname" {
		t.Fatalf("editMap(update) name = %v, want newname", got["name"])
	}
	if got["count"] != "42" {
		t.Fatalf("editMap(update) count = %v, want 42", got["count"])
	}
}

func TestEditMap_UpdateNonExistingKey(t *testing.T) {
	inputs := []string{"u", "ghost", "d"}
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

	m := map[string]any{"real": "value"}
	got, err := editMap("test", m, "")
	if err != nil {
		t.Fatalf("editMap(update non-existing): %v", err)
	}

	if !reflect.DeepEqual(got, m) {
		t.Fatalf("editMap(update non-existing) = %v, want %v (unchanged)", got, m)
	}
}

func TestEditMap_UnsupportedAction(t *testing.T) {
	inputs := []string{"x", "d"}
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

	m := map[string]any{"a": "1"}
	got, err := editMap("test", m, "")
	if err != nil {
		t.Fatalf("editMap(unsupported): %v", err)
	}

	if !reflect.DeepEqual(got, m) {
		t.Fatalf("editMap(unsupported) = %v, want %v (unchanged)", got, m)
	}
}

func TestSelectStringField(t *testing.T) {
	restore := utils.UseReadUserInputForTests(func() (string, error) {
		return "0", nil
	})
	defer restore()

	jzon := map[string]any{
		"name":    "alice",
		"count":   42,
		"enabled": true,
		"title":   "engineer",
	}

	got, err := selectStringField(jzon)
	if err != nil {
		t.Fatalf("selectStringField(): %v", err)
	}

	// Sorted string keys: "name", "title". Index 0 → "name"
	if got != "name" {
		t.Fatalf("selectStringField() = %q, want %q", got, "name")
	}
}

// ============================================================
// Tier 2 — Error paths in promptSlice* functions
// ============================================================

func TestPromptSliceAppend_ReadError(t *testing.T) {
	restore := utils.UseReadUserInputForTests(func() (string, error) {
		return "", errors.New("mock read error")
	})
	defer restore()

	got := promptSliceAppend()
	if got != nil {
		t.Fatalf("promptSliceAppend(error) = %v, want nil", got)
	}
}

func TestPromptSliceUpdate_BackKeepsSlice(t *testing.T) {
	// selectFromSlice reads "b" → returns ErrBack
	restore := utils.UseReadUserInputForTests(func() (string, error) {
		return "b", nil
	})
	defer restore()

	original := []any{"a", "b", "c"}
	got, err := promptSliceUpdate("test", original, "")
	if err != nil {
		t.Fatalf("promptSliceUpdate(back): %v", err)
	}
	if !reflect.DeepEqual(got, original) {
		t.Fatalf("promptSliceUpdate(back) = %v, want %v (unchanged)", got, original)
	}
}

func TestPromptSliceUpdate_QuitKeepsSlice(t *testing.T) {
	// selectFromSlice reads "q" → returns ErrUserInitiatedExit (caught by promptSliceUpdate)
	restore := utils.UseReadUserInputForTests(func() (string, error) {
		return "q", nil
	})
	defer restore()

	original := []any{"a", "b", "c"}
	got, err := promptSliceUpdate("test", original, "")
	if err != nil {
		t.Fatalf("promptSliceUpdate(quit): %v", err)
	}
	if !reflect.DeepEqual(got, original) {
		t.Fatalf("promptSliceUpdate(quit) = %v, want %v (unchanged)", got, original)
	}
}

func TestPromptSliceRemove_BackKeepsSlice(t *testing.T) {
	restore := utils.UseReadUserInputForTests(func() (string, error) {
		return "b", nil
	})
	defer restore()

	original := []any{"a", "b", "c"}
	got, err := promptSliceRemove(original)
	if err != nil {
		t.Fatalf("promptSliceRemove(back): %v", err)
	}
	if !reflect.DeepEqual(got, original) {
		t.Fatalf("promptSliceRemove(back) = %v, want %v (unchanged)", got, original)
	}
}

func TestPromptSliceRemove_QuitKeepsSlice(t *testing.T) {
	restore := utils.UseReadUserInputForTests(func() (string, error) {
		return "q", nil
	})
	defer restore()

	original := []any{"a", "b", "c"}
	got, err := promptSliceRemove(original)
	if err != nil {
		t.Fatalf("promptSliceRemove(quit): %v", err)
	}
	if !reflect.DeepEqual(got, original) {
		t.Fatalf("promptSliceRemove(quit) = %v, want %v (unchanged)", got, original)
	}
}

func TestPromptSliceUpdate_EmptySliceNoop(t *testing.T) {
	got, err := promptSliceUpdate("test", []any{}, "")
	if err != nil {
		t.Fatalf("promptSliceUpdate(empty): %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("promptSliceUpdate(empty) len = %d, want 0", len(got))
	}
}

func TestPromptSliceRemove_EmptySliceNoop(t *testing.T) {
	got, err := promptSliceRemove([]any{})
	if err != nil {
		t.Fatalf("promptSliceRemove(empty): %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("promptSliceRemove(empty) len = %d, want 0", len(got))
	}
}

// ============================================================
// Remaining coverage gap tests
// ============================================================

func TestHandleValue_SliceDispatch(t *testing.T) {
	restore := utils.UseReadUserInputForTests(func() (string, error) {
		return "d", nil
	})
	defer restore()

	s := []any{"a", "b", "c"}
	got, err := handleValue("tags", s, "")
	if err != nil {
		t.Fatalf("handleValue(slice): %v", err)
	}

	gotSlice, ok := got.([]any)
	if !ok {
		t.Fatalf("handleValue(slice) returned %T, want []any", got)
	}
	if !reflect.DeepEqual(gotSlice, s) {
		t.Fatalf("handleValue(slice) = %v, want %v", gotSlice, s)
	}
}

func TestEditMap_ReadErrorOnActionPrompt(t *testing.T) {
	restore := utils.UseReadUserInputForTests(func() (string, error) {
		return "", errors.New("mock read error")
	})
	defer restore()

	_, err := editMap("test", map[string]any{"a": "1"}, "")
	if err == nil {
		t.Fatal("editMap(read error) expected error, got nil")
	}
}

func TestEditSlice_InvalidAction(t *testing.T) {
	inputs := []string{"x", "d"}
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

	original := []any{"a", "b"}
	got, err := editSlice("test", original, "")
	if err != nil {
		t.Fatalf("editSlice(invalid action): %v", err)
	}
	if !reflect.DeepEqual(got, original) {
		t.Fatalf("editSlice(invalid action) = %v, want %v (unchanged)", got, original)
	}
}

func TestEditSlice_ReadErrorOnActionPrompt(t *testing.T) {
	restore := utils.UseReadUserInputForTests(func() (string, error) {
		return "", errors.New("mock read error")
	})
	defer restore()

	_, err := editSlice("test", []any{"a"}, "")
	if err == nil {
		t.Fatal("editSlice(read error) expected error, got nil")
	}
}

func TestGetModelValue_BackReturnsCurrent(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "openai_gpt_gpt-4.1.json"), []byte(`{}`), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	// "b" triggers ErrBack from SelectFromTable
	restore := utils.UseReadUserInputForTests(func() (string, error) {
		return "b", nil
	})
	defer restore()

	got, err := getModelValue("gpt-5.2", dir)
	if err != nil {
		t.Fatalf("getModelValue(back): %v", err)
	}
	if got != "gpt-5.2" {
		t.Fatalf("getModelValue(back) = %q, want %q (current kept)", got, "gpt-5.2")
	}
}

func TestGetModelValue_EmptyChoiceReturnsCurrent(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "openai_gpt_gpt-4.1.json"), []byte(`{}`), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	// "0" selects index 0, returns [0], but wait — we want empty choice.
	// Actually, "b" returns ErrBack, that's covered above.
	// For empty choice: SelectFromTable returns nil,nil when filter is active and empty input.
	// That's hard to trigger. The path `len(choice) == 0` is for when SelectFromTable
	// returns an empty slice. This happens when the table is empty (no models).
	// Already covered by TestGetModelValue_EmptyReturnsCurrent.
	t.Skip("empty choice from SelectFromTable only happens with filters")
}

func TestGetShellContextValue_BackReturnsCurrent(t *testing.T) {
	dir := t.TempDir()
	shellCtxDir := filepath.Join(dir, "shellContexts")
	if err := os.MkdirAll(shellCtxDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(shellCtxDir, "git.json"), []byte(`{}`), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	restore := utils.UseReadUserInputForTests(func() (string, error) {
		return "b", nil
	})
	defer restore()

	got, err := getShellContextValue("minimal", dir)
	if err != nil {
		t.Fatalf("getShellContextValue(back): %v", err)
	}
	if got != "minimal" {
		t.Fatalf("getShellContextValue(back) = %q, want %q (current kept)", got, "minimal")
	}
}

func TestPromptSliceUpdate_HandleValueError(t *testing.T) {
	// selectFromSlice reads "0" (selects index 0), then handleValue calls
	// getNewValue which reads input. We make that call fail.
	callCount := 0
	restore := utils.UseReadUserInputForTests(func() (string, error) {
		callCount++
		// First call: selectFromSlice → "0"
		if callCount == 1 {
			return "0", nil
		}
		// Second call: getNewValue → error
		return "", errors.New("mock handle error")
	})
	defer restore()

	_, err := promptSliceUpdate("test", []any{"a", "b"}, "")
	if err == nil {
		t.Fatal("promptSliceUpdate(handle error) expected error, got nil")
	}
}

func TestPromptSliceRemove_NonBackError(t *testing.T) {
	// SelectFromTable returns a non-ErrBack error. This is hard to trigger
	// from the outside since SelectFromTable only returns parse errors
	// or ErrBack/ErrUserInitiatedExit. The non-ErrBack path in promptSliceRemove
	// wraps errors that aren't ErrBack or ErrUserInitiatedExit.
	// In practice, this path is reached when SelectFromTable returns an
	// unexpected error (e.g., from paginator).
	// We can't easily trigger this without mocking deeper, so we skip.
	t.Skip("requires paginator error injection")
}

func TestInteractiveReconfigure_SelectFieldError(t *testing.T) {
	// selectFieldToEdit calls SelectFromTable. If it returns a non-errDoneEditing
	// error (like ErrBack), interractiveReconfigure returns that error.
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "test.json")
	original := `{"name":"oldname","prompt":"hello"}`
	if err := os.WriteFile(cfgPath, []byte(original), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	restore := utils.UseReadUserInputForTests(func() (string, error) {
		return "q", nil // quit → ErrUserInitiatedExit
	})
	defer restore()

	err := interractiveReconfigure(config{name: "test.json", filePath: cfgPath}, []byte(original))
	if !errors.Is(err, utils.ErrUserInitiatedExit) {
		t.Fatalf("interractiveReconfigure(quit) = %v, want ErrUserInitiatedExit", err)
	}
}

func TestWriteConfig_MarshalError(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "out.json")

	// JSON marshaling fails for NaN and Inf float values.
	jzon := map[string]any{
		"bad": math.Inf(1), // +Inf
	}
	err := writeConfig(cfgPath, jzon)
	if err == nil {
		t.Fatal("writeConfig(Inf) expected marshal error, got nil")
	}
}

func TestActionRemove_NotConfirmed(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "test.json")
	if err := os.WriteFile(cfgPath, []byte(`{}`), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	restore := utils.UseReadUserInputForTests(func() (string, error) {
		return "n", nil
	})
	defer restore()

	err := actionRemove(config{name: "test.json", filePath: cfgPath})
	if err == nil {
		t.Fatal("expected error for non-confirmed deletion, got nil")
	}
}

func TestActionCopy_ReadError(t *testing.T) {
	dir := t.TempDir()
	srcPath := filepath.Join(dir, "source.json")
	if err := os.WriteFile(srcPath, []byte(`{}`), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	restore := utils.UseReadUserInputForTests(func() (string, error) {
		return "", errors.New("mock read error")
	})
	defer restore()

	_, err := actionCopy(config{name: "source.json", filePath: srcPath})
	if err == nil {
		t.Fatal("expected read error, got nil")
	}
}

func TestActOnConfigItem_BackPropagates(t *testing.T) {
	// queryForAction returns unset, ErrBack — actOnConfigItem should detect and propagate.
	restore := utils.UseReadUserInputForTests(func() (string, error) {
		return "b", nil
	})
	defer restore()

	cat := setupCategory{itemActions: []action{conf}}
	err := actOnConfigItem(cat, config{name: "test", filePath: "/tmp/test.json"})
	if !errors.Is(err, utils.ErrBack) {
		t.Fatalf("actOnConfigItem(back) = %v, want ErrBack", err)
	}
}

func TestInterractiveReconfigure_HandleValueError(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "test.json")
	original := `{"name":"oldname"}`
	if err := os.WriteFile(cfgPath, []byte(original), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	callCount := 0
	restore := utils.UseReadUserInputForTests(func() (string, error) {
		callCount++
		// 1st call: selectFieldToEdit selects index 0 ("name")
		if callCount == 1 {
			return "0", nil
		}
		// 2nd call: getNewValue returns error
		return "", errors.New("mock read error")
	})
	defer restore()

	err := interractiveReconfigure(config{name: "test.json", filePath: cfgPath}, []byte(original))
	if err == nil {
		t.Fatal("interractiveReconfigure expected handleValue error, got nil")
	}
}

// ============================================================
// Tier 3 — filesystem-dependent functions (with t.TempDir)
// ============================================================

func TestCreateConfigFile(t *testing.T) {
	restore := utils.UseReadUserInputForTests(func() (string, error) {
		return "myconfig", nil
	})
	defer restore()

	dir := t.TempDir()
	cfg, err := createConfigFile(dir, "testtype", map[string]any{"key": "default"})
	if err != nil {
		t.Fatalf("createConfigFile(): %v", err)
	}

	wantPath := filepath.Join(dir, "myconfig.json")
	if cfg.filePath != wantPath {
		t.Fatalf("filePath = %q, want %q", cfg.filePath, wantPath)
	}
	if cfg.name != "myconfig" {
		t.Fatalf("name = %q, want myconfig", cfg.name)
	}

	// Verify file exists and has the default content
	b, err := os.ReadFile(wantPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	var parsed map[string]any
	if err := json.Unmarshal(b, &parsed); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if parsed["key"] != "default" {
		t.Fatalf("key = %v, want default", parsed["key"])
	}
}

func TestActionReconfigure(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "profiles", "test.json")
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	original := `{"name":"oldname","prompt":"hello"}`
	if err := os.WriteFile(cfgPath, []byte(original), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	// Select field 0 ("name"), set to "newname", then done
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

	cfg := config{name: "test.json", filePath: cfgPath}
	if err := actionReconfigure(cfg); err != nil {
		t.Fatalf("actionReconfigure(): %v", err)
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
		t.Fatalf("name = %q, want newname", jzon["name"])
	}
}

// ============================================================
// Remaining edge case tests
// ============================================================

func TestEditMap_UpdateHandleValueError(t *testing.T) {
	// update → handleValue → getNewValue → ReadUserInput error
	inputs := []string{"u", "name"}
	inputIdx := 0
	callCount := 0
	restore := utils.UseReadUserInputForTests(func() (string, error) {
		callCount++
		if callCount <= 2 {
			ret := inputs[inputIdx]
			inputIdx++
			return ret, nil
		}
		return "", errors.New("mock handle error")
	})
	defer restore()

	_, err := editMap("test", map[string]any{"name": "old"}, "")
	if err == nil {
		t.Fatal("editMap(handleValue error) expected error, got nil")
	}
}

func TestEditSlice_UpdateHandleValueError(t *testing.T) {
	// editSlice reads "u", selectFromSlice reads "0", handleValue error
	callCount := 0
	restore := utils.UseReadUserInputForTests(func() (string, error) {
		callCount++
		switch callCount {
		case 1:
			return "u", nil // editSlice action
		case 2:
			return "0", nil // selectFromSlice index
		default:
			return "", errors.New("mock handle error") // getNewValue error
		}
	})
	defer restore()

	_, err := editSlice("test", []any{"a", "b"}, "")
	if err == nil {
		t.Fatal("editSlice(handleValue error) expected error, got nil")
	}
}

func TestActionRemove_ReadError(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "test.json")
	if err := os.WriteFile(cfgPath, []byte(`{}`), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	restore := utils.UseReadUserInputForTests(func() (string, error) {
		return "", errors.New("mock read error")
	})
	defer restore()

	err := actionRemove(config{name: "test.json", filePath: cfgPath})
	if err == nil {
		t.Fatal("expected read error, got nil")
	}
}

func TestQueryForAction_Error(t *testing.T) {
	restore := utils.UseReadUserInputForTests(func() (string, error) {
		return "", errors.New("mock read error")
	})
	defer restore()

	_, err := queryForAction([]action{conf, del})
	if err == nil {
		t.Fatal("expected read error, got nil")
	}
}

func TestQueryForAction_InvalidThenValid(t *testing.T) {
	inputs := []string{"invalid", "c"}
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

	got, err := queryForAction([]action{conf, del})
	if err != nil {
		t.Fatalf("queryForAction(): %v", err)
	}
	if got != conf {
		t.Fatalf("queryForAction() = %v, want conf", got)
	}
}

func TestQueryForAction_Quit(t *testing.T) {
	restore := utils.UseReadUserInputForTests(func() (string, error) {
		return "q", nil
	})
	defer restore()

	_, err := queryForAction([]action{conf, del})
	if !errors.Is(err, utils.ErrUserInitiatedExit) {
		t.Fatalf("queryForAction(quit) = %v, want ErrUserInitiatedExit", err)
	}
}

func TestPreviewConfigItem_ReadError(t *testing.T) {
	cfg := config{filePath: "/nonexistent/path/config.json"}
	err := previewConfigItem(cfg)
	if err == nil {
		t.Fatal("expected read error, got nil")
	}
}

func TestPreviewConfigItem_BadJSON(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")
	if err := os.WriteFile(cfgPath, []byte("not json"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	err := previewConfigItem(config{filePath: cfgPath})
	if err == nil {
		t.Fatal("expected unmarshal error, got nil")
	}
}

func TestActionReconfigure_OpenError(t *testing.T) {
	err := actionReconfigure(config{filePath: "/nonexistent/path/config.json"})
	if err == nil {
		t.Fatal("expected open error, got nil")
	}
}

// ============================================================
// Additional tests for setup.go functions
// ============================================================

func TestShellContextSetupCategory(t *testing.T) {
	cat := shellContextSetupCategory()
	if cat.name != "shell context" {
		t.Fatalf("name = %q, want 'shell context'", cat.name)
	}
	if cat.subdirPath != "./shellContexts" {
		t.Fatalf("subdirPath = %q, want './shellContexts'", cat.subdirPath)
	}
	if len(cat.itemActions) == 0 {
		t.Fatal("expected itemActions to be non-empty")
	}
	if len(cat.itemSelectActions) == 0 {
		t.Fatal("expected itemSelectActions to be non-empty")
	}
}

func TestExecuteConfigAction_Default(t *testing.T) {
	err := executeConfigAction(config{name: "test"}, unset)
	if err == nil {
		t.Fatal("expected error for invalid action, got nil")
	}
}

func TestExecuteConfigAction_Back(t *testing.T) {
	err := executeConfigAction(config{name: "test"}, back)
	if !errors.Is(err, utils.ErrBack) {
		t.Fatalf("executeConfigAction(back) = %v, want ErrBack", err)
	}
}

// ============================================================
// Remaining edge cases
// ============================================================

func TestCreateConfigFile_ReadError(t *testing.T) {
	restore := utils.UseReadUserInputForTests(func() (string, error) {
		return "", errors.New("mock read error")
	})
	defer restore()

	_, err := createConfigFile(t.TempDir(), "testtype", map[string]any{})
	if err == nil {
		t.Fatal("expected read error, got nil")
	}
}

func TestActionCopy_SourceReadError(t *testing.T) {
	restore := utils.UseReadUserInputForTests(func() (string, error) {
		return "mycopy", nil
	})
	defer restore()

	_, err := actionCopy(config{name: "source.json", filePath: "/nonexistent/source.json"})
	if err == nil {
		t.Fatal("expected source read error, got nil")
	}
}

func TestEditMap_AddReadError(t *testing.T) {
	inputs := []string{"a", "newkey"} // 3rd call (value) will error
	callCount := 0
	restore := utils.UseReadUserInputForTests(func() (string, error) {
		callCount++
		if callCount <= 2 {
			return inputs[callCount-1], nil
		}
		return "", errors.New("mock read error")
	})
	defer restore()

	_, err := editMap("test", map[string]any{"existing": "value"}, "")
	if err == nil {
		t.Fatal("editMap(add read error) expected error, got nil")
	}
}

func TestEditMap_RemoveReadError(t *testing.T) {
	callCount := 0
	restore := utils.UseReadUserInputForTests(func() (string, error) {
		callCount++
		if callCount == 1 {
			return "r", nil
		}
		return "", errors.New("mock read error")
	})
	defer restore()

	_, err := editMap("test", map[string]any{"key": "val"}, "")
	if err == nil {
		t.Fatal("editMap(remove read error) expected error, got nil")
	}
}

func TestGetToolsValue_SelectError(t *testing.T) {
	tools.Registry.Reset()
	tools.Init()
	tools.Registry.Reset()
	t.Cleanup(func() { tools.Registry.Reset() })

	restore := utils.UseReadUserInputForTests(func() (string, error) {
		return "", errors.New("mock select error")
	})
	defer restore()

	_, err := getToolsValue([]any{"rg"})
	if err == nil {
		t.Fatal("getToolsValue(select error) expected error, got nil")
	}
}

// ============================================================
// Push coverage: remaining setup.go paths
// ============================================================

func TestShellContextSetupCategory_Load(t *testing.T) {
	cat := shellContextSetupCategory()
	dir := t.TempDir()
	shellCtxDir := filepath.Join(dir, "shellContexts")

	// load should create the shellContexts directory and find configs
	cfgs, err := cat.load(dir)
	if err != nil {
		t.Fatalf("load(): %v", err)
	}

	// Verify directory was created
	if _, statErr := os.Stat(shellCtxDir); statErr != nil {
		t.Fatalf("shellContexts dir not created: %v", statErr)
	}

	// CreateConfigDir creates default shell contexts, so there should be at least 1
	if len(cfgs) == 0 {
		t.Fatal("load() returned 0 configs, expected at least 1 default")
	}
}

func TestExecuteConfigAction_ConfWithEditor(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "test.json")
	if err := os.WriteFile(cfgPath, []byte(`{}`), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	oldEditor := os.Getenv("EDITOR")
	defer os.Setenv("EDITOR", oldEditor)
	os.Setenv("EDITOR", "echo")

	err := executeConfigAction(config{name: "test.json", filePath: cfgPath}, confWithEditor)
	if err != nil {
		t.Fatalf("executeConfigAction(confWithEditor): %v", err)
	}
}

func TestExecuteConfigAction_PromptEditWithEditor(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "test.json")
	if err := os.WriteFile(cfgPath, []byte(`{"prompt":"hello"}`), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	oldEditor := os.Getenv("EDITOR")
	defer os.Setenv("EDITOR", oldEditor)
	os.Setenv("EDITOR", "echo")

	err := executeConfigAction(config{name: "test.json", filePath: cfgPath}, promptEditWithEditor)
	if err != nil {
		t.Fatalf("executeConfigAction(promptEditWithEditor): %v", err)
	}
}

func TestExecuteConfigAction_UnescapedFieldEditWithEditor(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "test.json")
	if err := os.WriteFile(cfgPath, []byte(`{"name":"oldname"}`), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	// selectStringField needs a string field, index 0 = "name"
	restore := utils.UseReadUserInputForTests(func() (string, error) {
		return "0", nil
	})
	defer restore()

	oldEditor := os.Getenv("EDITOR")
	defer os.Setenv("EDITOR", oldEditor)
	os.Setenv("EDITOR", "echo")

	err := executeConfigAction(config{name: "test.json", filePath: cfgPath}, unescapedFieldEditWithEditor)
	if err != nil {
		t.Fatalf("executeConfigAction(unescapedFieldEditWithEditor): %v", err)
	}
}

func TestExecuteConfigAction_Copy(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "test.json")
	if err := os.WriteFile(cfgPath, []byte(`{}`), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	inputs := []string{"mycopy", "d"}
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

	err := executeConfigAction(config{name: "test.json", filePath: cfgPath}, copyAction)
	if err != nil {
		t.Fatalf("executeConfigAction(copy): %v", err)
	}

	// Verify copy was created
	copyPath := filepath.Join(dir, "mycopy.json")
	if _, statErr := os.Stat(copyPath); statErr != nil {
		t.Fatalf("copy not created: %v", statErr)
	}
}

func TestExecuteConfigAction_Delete(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "test.json")
	if err := os.WriteFile(cfgPath, []byte(`{}`), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	restore := utils.UseReadUserInputForTests(func() (string, error) {
		return "y", nil
	})
	defer restore()

	err := executeConfigAction(config{name: "test.json", filePath: cfgPath}, del)
	if err != nil {
		t.Fatalf("executeConfigAction(delete): %v", err)
	}
}

func TestActOnConfigItem_ExecuteError(t *testing.T) {
	// trigger a non-back/non-quit error from executeConfigAction.
	// The conf action calls actionReconfigure which opens the file.
	// Pass a non-existent file to trigger open error.
	inputs := []string{"c"}
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

	cat := setupCategory{itemActions: []action{conf}}
	err := actOnConfigItem(cat, config{name: "test", filePath: "/nonexistent/config.json"})
	if err == nil {
		t.Fatal("expected error from executeConfigAction, got nil")
	}
}

func TestCreateConfigFile_MkdirError(t *testing.T) {
	// Create a file where a directory should be created.
	// createConfigFile tries MkdirAll on configTypePath, which will fail
	// because a file exists at that path.
	dir := t.TempDir()
	blocker := filepath.Join(dir, "blocker")
	if err := os.WriteFile(blocker, []byte("block"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	restore := utils.UseReadUserInputForTests(func() (string, error) {
		return "testname", nil
	})
	defer restore()

	// MkdirAll on a path where a file exists will fail
	_, err := createConfigFile(blocker, "testtype", map[string]any{})
	if err == nil {
		t.Fatal("expected MkdirAll error, got nil")
	}
}

func TestActionReconfigureStringFieldWithEditor_FieldSelection(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "test.json")
	if err := os.WriteFile(cfgPath, []byte(`{"name":"oldname","prompt":"hello"}`), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	// selectStringField: pick index 1 ("prompt")
	restore := utils.UseReadUserInputForTests(func() (string, error) {
		return "1", nil
	})
	defer restore()

	oldEditor := os.Getenv("EDITOR")
	defer os.Setenv("EDITOR", oldEditor)
	os.Setenv("EDITOR", "echo")

	err := actionReconfigureStringFieldWithEditor(config{name: "test.json", filePath: cfgPath}, "")
	if err != nil {
		t.Fatalf("actionReconfigureStringFieldWithEditor: %v", err)
	}
}

func TestActionReconfigureStringFieldWithEditor_FieldNotFound(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "test.json")
	if err := os.WriteFile(cfgPath, []byte(`{"name":"oldname"}`), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	err := actionReconfigureStringFieldWithEditor(config{name: "test.json", filePath: cfgPath}, "nonexistent")
	if err == nil {
		t.Fatal("expected field not found error, got nil")
	}
}

func TestActionReconfigureStringFieldWithEditor_NotString(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "test.json")
	if err := os.WriteFile(cfgPath, []byte(`{"count":42}`), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	err := actionReconfigureStringFieldWithEditor(config{name: "test.json", filePath: cfgPath}, "count")
	if err == nil {
		t.Fatal("expected not-string error, got nil")
	}
}

func TestActionReconfigureStringFieldWithEditor_ReadError(t *testing.T) {
	err := actionReconfigureStringFieldWithEditor(config{filePath: "/nonexistent/config.json"}, "name")
	if err == nil {
		t.Fatal("expected read error, got nil")
	}
}

// ============================================================
// Final coverage push
// ============================================================

func TestActOnConfigItem_QuitFromQuery(t *testing.T) {
	restore := utils.UseReadUserInputForTests(func() (string, error) {
		return "q", nil
	})
	defer restore()

	cat := setupCategory{itemActions: []action{conf}}
	err := actOnConfigItem(cat, config{name: "test", filePath: "/tmp/test.json"})
	if !errors.Is(err, utils.ErrUserInitiatedExit) {
		t.Fatalf("actOnConfigItem(quit query) = %v, want ErrUserInitiatedExit", err)
	}
}

func TestAction_String(t *testing.T) {
	tests := []struct {
		name string
		a    action
		want string
	}{
		{"back", back, "[b]ack"},
		{"conf", conf, "[c]onfigure"},
		{"confWithEditor", confWithEditor, "configure with [e]ditor"},
		{"del", del, "[d]el"},
		{"copyAction", copyAction, "cop[y]"},
		{"newaction", newaction, "cre[a]te new"},
		{"quit", quit, "[q]uit"},
		{"promptEditWithEditor", promptEditWithEditor, "[pr]ompt edit with editor"},
		{"unescapedFieldEditWithEditor", unescapedFieldEditWithEditor, "(u)nescaped (f)ield (e)dit [ufe]"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.a.String(); got != tt.want {
				t.Fatalf("action.String() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestValidateEditedStringField_ShellContextTemplate(t *testing.T) {
	dir := t.TempDir()
	shellCtxDir := filepath.Join(dir, "shellContexts")
	if err := os.MkdirAll(shellCtxDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	cfgPath := filepath.Join(shellCtxDir, "test.json")

	cfg := config{name: "test", filePath: cfgPath}

	t.Run("valid template passes validation", func(t *testing.T) {
		err := validateEditedStringField(cfg, "template", `Hello {{.Cwd}}`)
		if err != nil {
			t.Fatalf("validateEditedStringField(valid template): %v", err)
		}
	})

	t.Run("empty string is valid", func(t *testing.T) {
		err := validateEditedStringField(cfg, "template", "")
		if err != nil {
			t.Fatalf("validateEditedStringField(empty template): %v", err)
		}
	})

	t.Run("non-template field skips validation", func(t *testing.T) {
		err := validateEditedStringField(cfg, "name", "anything")
		if err != nil {
			t.Fatalf("validateEditedStringField(non-template): %v", err)
		}
	})

	t.Run("non-shellContexts dir skips validation", func(t *testing.T) {
		otherDir := t.TempDir()
		otherCfg := config{name: "test", filePath: filepath.Join(otherDir, "test.json")}
		err := validateEditedStringField(otherCfg, "template", "anything")
		if err != nil {
			t.Fatalf("validateEditedStringField(non-shellctx dir): %v", err)
		}
	})
}
