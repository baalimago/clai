package setup

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/baalimago/clai/internal/utils"
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
