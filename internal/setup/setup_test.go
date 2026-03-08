package setup

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/baalimago/clai/internal/utils"
	"github.com/baalimago/go_away_boilerplate/pkg/testboil"
)

func TestGetConfigs(t *testing.T) {
	// Create a temporary directory for test files
	tempDir, err := os.MkdirTemp("", "test_configs")
	if err != nil {
		t.Fatalf("Failed to create temp directory: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Create test files
	testFiles := []string{
		"config1.json",
		"config2.json",
		"textConfig.json",
		"photoConfig.json",
		"otherFile.txt",
	}
	for _, file := range testFiles {
		_, err := os.Create(filepath.Join(tempDir, file))
		if err != nil {
			t.Fatalf("Failed to create test file %s: %v", file, err)
		}
	}

	tests := []struct {
		name            string
		includeGlob     string
		excludeContains []string
		want            []config
	}{
		{
			name:            "All JSON files",
			includeGlob:     filepath.Join(tempDir, "*.json"),
			excludeContains: []string{},
			want: []config{
				{name: "config1.json", filePath: filepath.Join(tempDir, "config1.json")},
				{name: "config2.json", filePath: filepath.Join(tempDir, "config2.json")},
				{name: "photoConfig.json", filePath: filepath.Join(tempDir, "photoConfig.json")},
				{name: "textConfig.json", filePath: filepath.Join(tempDir, "textConfig.json")},
			},
		},
		{
			name:            "Exclude text and photo configs",
			includeGlob:     filepath.Join(tempDir, "*.json"),
			excludeContains: []string{"textConfig", "photoConfig"},
			want: []config{
				{name: "config1.json", filePath: filepath.Join(tempDir, "config1.json")},
				{name: "config2.json", filePath: filepath.Join(tempDir, "config2.json")},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := getConfigs(tt.includeGlob, tt.excludeContains)
			if err != nil {
				t.Errorf("getConfigs() error = %v", err)
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("getConfigs() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestExecuteConfigAction_TableDriven(t *testing.T) {
	tests := []struct {
		name           string
		action         action
		editor         string
		fileContent    string
		readUserInputs []string
		wantErrSubstr  string
		verify         func(t *testing.T, cfgPath string)
	}{
		{
			name:          "invalid action returns contextual error",
			action:        unset,
			fileContent:   `{"a":1}`,
			wantErrSubstr: `invalid action for config "cfg.json": unset`,
		},
		{
			name:          "back returns ErrBack",
			action:        back,
			fileContent:   `{"a":1}`,
			wantErrSubstr: utils.ErrBack.Error(),
		},
		{
			name:           "delete confirmed removes file",
			action:         del,
			fileContent:    `{"a":1}`,
			readUserInputs: []string{"y"},
			verify: func(t *testing.T, cfgPath string) {
				t.Helper()
				_, err := os.Stat(cfgPath)
				if !os.IsNotExist(err) {
					t.Fatalf("expected %q to be deleted, got err=%v", cfgPath, err)
				}
			},
		},
		{
			name:           "delete denied keeps file and returns wrapped error",
			action:         del,
			fileContent:    `{"a":1}`,
			readUserInputs: []string{"n"},
			wantErrSubstr:  "aborting deletion",
			verify: func(t *testing.T, cfgPath string) {
				t.Helper()
				_, err := os.Stat(cfgPath)
				if err != nil {
					t.Fatalf("expected %q to still exist: %v", cfgPath, err)
				}
			},
		},
		{
			name:          "configure with editor requires EDITOR",
			action:        confWithEditor,
			fileContent:   `{"a":1}`,
			editor:        "",
			wantErrSubstr: "EDITOR is not set",
		},
		{
			name:        "configure with editor updates file",
			action:      confWithEditor,
			fileContent: `{"a":1}`,
			editor:      "#!/bin/sh\ncat > \"$1\" <<'EOF'\n{\"a\":2}\nEOF\n",
			verify: func(t *testing.T, cfgPath string) {
				t.Helper()
				b, err := os.ReadFile(cfgPath)
				if err != nil {
					t.Fatalf("ReadFile(%q): %v", cfgPath, err)
				}
				testboil.FailTestIfDiff(t, strings.TrimSpace(string(b)), `{"a":2}`)
			},
		},
		{
			name:          "prompt editor wraps missing prompt error",
			action:        promptEditWithEditor,
			fileContent:   `{"model":"m"}`,
			editor:        "/bin/true",
			wantErrSubstr: `failed to edit prompt with editor: missing string field "prompt"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			cfgPath := filepath.Join(dir, "cfg.json")
			if err := os.WriteFile(cfgPath, []byte(tt.fileContent), 0o644); err != nil {
				t.Fatalf("WriteFile(%q): %v", cfgPath, err)
			}

			restoreInput := utils.UseReadUserInputForTests(func() (string, error) {
				if len(tt.readUserInputs) == 0 {
					return "", io.EOF
				}
				ret := tt.readUserInputs[0]
				tt.readUserInputs = tt.readUserInputs[1:]
				return ret, nil
			})
			defer restoreInput()

			if tt.editor != "" || tt.action == confWithEditor || tt.action == promptEditWithEditor {
				editor := tt.editor
				if strings.Contains(editor, "\n") {
					editorPath := filepath.Join(dir, "editor.sh")
					if err := os.WriteFile(editorPath, []byte(editor), 0o755); err != nil {
						t.Fatalf("WriteFile(editor): %v", err)
					}
					editor = editorPath
				}
				t.Setenv("EDITOR", editor)
			}

			err := executeConfigAction(config{name: "cfg.json", filePath: cfgPath}, tt.action)
			if tt.wantErrSubstr != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tt.wantErrSubstr)
				}
				if !strings.Contains(err.Error(), tt.wantErrSubstr) {
					t.Fatalf("err=%q, want substring %q", err.Error(), tt.wantErrSubstr)
				}
				if tt.action == back && !errors.Is(err, utils.ErrBack) {
					t.Fatalf("expected ErrBack, got %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("executeConfigAction(): %v", err)
			}
			if tt.verify != nil {
				tt.verify(t, cfgPath)
			}
		})
	}
}

func TestSetupCustomTableActions_TableDriven(t *testing.T) {
	tests := []struct {
		name     string
		category setupCategory
		want     []action
		notWant  []action
	}{
		{
			name: "always includes back",
			category: setupCategory{
				name: "plain",
			},
			want:    []action{back},
			notWant: []action{newaction, pasteConfig},
		},
		{
			name: "profile selection exposes new only",
			category: setupCategory{
				name:              "profiles",
				subdirPath:        t.TempDir(),
				itemSelectActions: []action{newaction},
			},
			want:    []action{back, newaction},
			notWant: []action{pasteConfig},
		},
		{
			name: "mcp selection exposes paste and new",
			category: setupCategory{
				name:              "mcp",
				subdirPath:        t.TempDir(),
				itemSelectActions: []action{newaction, pasteConfig},
			},
			want: []action{back, newaction, pasteConfig},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := setupCustomTableActions(tt.category)

			gotActions := map[action]bool{}
			for _, cta := range got {
				for act, expected := range actionToTableAction {
					if cta.Short == expected.Short && cta.Long == expected.Long && cta.Format == expected.Format {
						gotActions[act] = true
					}
				}
				if cta.Action == nil {
					t.Fatalf("custom table action %+v missing Action func", cta)
				}
			}

			for _, want := range tt.want {
				if !gotActions[want] {
					t.Fatalf("expected action %v in %+v", want, got)
				}
			}
			for _, notWant := range tt.notWant {
				if gotActions[notWant] {
					t.Fatalf("did not expect action %v in %+v", notWant, got)
				}
			}
		})
	}
}

func TestActionReconfigureStringFieldWithEditor_PreservesNonEditedFields_TableDriven(t *testing.T) {
	tests := []struct {
		name       string
		fieldName  string
		inputJSON  map[string]any
		editTo     string
		wantField  string
		wantOthers map[string]any
	}{
		{
			name:      "edits shell-context and keeps prompt",
			fieldName: "shell-context",
			inputJSON: map[string]any{
				"prompt":        "keep\\nme",
				"shell-context": "old\\nctx",
				"model":         "m",
				"use_tools":     true,
			},
			editTo:    "new\nctx\tZ",
			wantField: "new\\nctx\\tZ",
			wantOthers: map[string]any{
				"prompt":    "keep\\nme",
				"model":     "m",
				"use_tools": true,
			},
		},
		{
			name:      "edits plain string and keeps arrays and maps",
			fieldName: "title",
			inputJSON: map[string]any{
				"title": "before",
				"tools": []any{"bash", "rg"},
				"meta":  map[string]any{"a": "b"},
			},
			editTo:    "after",
			wantField: "after",
			wantOthers: map[string]any{
				"tools": []any{"bash", "rg"},
				"meta":  map[string]any{"a": "b"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			b, err := json.MarshalIndent(tt.inputJSON, "", "\t")
			if err != nil {
				t.Fatalf("MarshalIndent(): %v", err)
			}
			cfgPath := filepath.Join(dir, "cfg.json")
			if err := os.WriteFile(cfgPath, b, 0o644); err != nil {
				t.Fatalf("WriteFile(%q): %v", cfgPath, err)
			}

			editorPath := filepath.Join(dir, "editor.sh")
			editorScript := "#!/bin/sh\ncat > \"$1\" <<'EOF'\n" + tt.editTo + "\nEOF\n"
			if err := os.WriteFile(editorPath, []byte(editorScript), 0o755); err != nil {
				t.Fatalf("WriteFile(editor): %v", err)
			}
			t.Setenv("EDITOR", editorPath)

			err = actionReconfigureStringFieldWithEditor(config{name: "cfg.json", filePath: cfgPath}, tt.fieldName)
			if err != nil {
				t.Fatalf("actionReconfigureStringFieldWithEditor(%q): %v", tt.fieldName, err)
			}

			updatedBytes, err := os.ReadFile(cfgPath)
			if err != nil {
				t.Fatalf("ReadFile(%q): %v", cfgPath, err)
			}
			var got map[string]any
			if err := json.Unmarshal(updatedBytes, &got); err != nil {
				t.Fatalf("Unmarshal(%q): %v", cfgPath, err)
			}

			if got[tt.fieldName] != tt.wantField {
				t.Fatalf("field %q = %#v, want %q", tt.fieldName, got[tt.fieldName], tt.wantField)
			}
			for k, want := range tt.wantOthers {
				if !reflect.DeepEqual(got[k], want) {
					t.Fatalf("field %q = %#v, want %#v", k, got[k], want)
				}
			}
		})
	}
}

func TestSetupCustomTableActions_McpIncludesPasteAction(t *testing.T) {
	category := setupCategory{
		name:              "MCP server configuration",
		subdirPath:        t.TempDir(),
		itemSelectActions: []action{newaction, pasteConfig},
	}

	got := setupCustomTableActions(category)

	foundPaste := false
	for _, cta := range got {
		if cta.Short == "p" && cta.Long == "paste" {
			foundPaste = true
			if cta.Action == nil {
				t.Fatalf("paste action missing callback: %+v", cta)
			}
		}
	}

	if !foundPaste {
		t.Fatalf("expected MCP item selection to include paste action, got %+v", got)
	}
}
