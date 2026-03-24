package setup

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/baalimago/clai/internal/text"
	"github.com/baalimago/clai/internal/utils"
)

func TestSetupCustomTableActions_CategorySpecific(t *testing.T) {
	tests := []struct {
		name     string
		category setupCategory
		want     []action
		notWant  []action
	}{
		{
			name: "mode files only allow configure from selection",
			category: setupCategory{
				name:        "mode-files",
				itemActions: []action{conf},
			},
			want:    nil,
			notWant: []action{newaction, confWithEditor, promptEditWithEditor, unescapedFieldEditWithEditor},
		},
		{
			name: "model files do not expose prompt specific actions",
			category: setupCategory{
				name:        "model files",
				itemActions: []action{conf, del, confWithEditor},
			},
			want:    nil,
			notWant: []action{newaction, promptEditWithEditor, unescapedFieldEditWithEditor},
		},
		{
			name: "profiles expose new from item selection only",
			category: setupCategory{
				name:              "text generation profiles",
				subdirPath:        t.TempDir(),
				itemSelectActions: []action{newaction},
				itemActions:       []action{conf, del, confWithEditor, promptEditWithEditor, unescapedFieldEditWithEditor},
			},
			want:    []action{newaction},
			notWant: []action{confWithEditor, promptEditWithEditor, unescapedFieldEditWithEditor},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := setupCustomTableActions(tt.category)
			gotActions := make(map[action]bool)
			for _, cta := range got {
				for act, expected := range actionToTableAction {
					if cta.Short == expected.Short && cta.Long == expected.Long && cta.Format == expected.Format {
						gotActions[act] = true
					}
				}
			}

			for _, want := range tt.want {
				if !gotActions[want] {
					t.Fatalf("expected action %v to exist in item selection", want)
				}
			}
			for _, notWant := range tt.notWant {
				if gotActions[notWant] {
					t.Fatalf("did not expect action %v in item selection", notWant)
				}
			}
		})
	}
}

func TestSelectConfigItem_ItemSelectionOnlyShowsPaginationAndNew(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "prof.json")
	b, err := json.MarshalIndent(text.DefaultProfile, "", "\t")
	if err != nil {
		t.Fatalf("failed to marshal default profile: %v", err)
	}
	if err := os.WriteFile(cfgPath, b, 0o644); err != nil {
		t.Fatalf("failed to write profile config: %v", err)
	}

	inputs := []string{"0", "b"}
	inputIdx := 0
	restoreInput := utils.UseReadUserInputForTests(func() (string, error) {
		if inputIdx >= len(inputs) {
			return "", io.EOF
		}
		ret := inputs[inputIdx]
		inputIdx++
		return ret, nil
	})
	defer restoreInput()

	origStdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("failed to create stdout pipe: %v", err)
	}
	os.Stdout = w
	defer func() {
		os.Stdout = origStdout
	}()

	err = selectConfigItem(
		setupCategory{
			name:              "text generation profiles",
			subdirPath:        dir,
			itemSelectActions: []action{newaction},
			itemActions:       []action{conf, del, confWithEditor, promptEditWithEditor, unescapedFieldEditWithEditor},
		},
		[]config{{name: "prof.json", filePath: cfgPath}},
	)

	_ = w.Close()
	outBytes, readErr := io.ReadAll(r)
	if readErr != nil {
		t.Fatalf("failed to read stdout: %v", readErr)
	}
	out := string(outBytes)

	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(out, "cre[a]te new") {
		t.Fatalf("expected create-new action in item selection output, got %q", out)
	}
	selectPromptPos := strings.Index(strings.ToLower(out), "select config")
	if selectPromptPos == -1 {
		t.Fatalf("expected select prompt in output, got %q", out)
	}
	previewPos := strings.Index(out, "Selected config preview:")
	if previewPos == -1 {
		t.Fatalf("expected preview in output, got %q", out)
	}
	itemSelectionChunk := out[selectPromptPos:previewPos]
	if strings.Contains(itemSelectionChunk, "conf with [e]ditor") {
		t.Fatalf("did not expect editor action in item selection output, got %q", itemSelectionChunk)
	}
	if strings.Contains(itemSelectionChunk, "[pr]ompt edit with editor") {
		t.Fatalf("did not expect prompt editor action in item selection output, got %q", itemSelectionChunk)
	}
	if strings.Contains(itemSelectionChunk, "field edit") {
		t.Fatalf("did not expect unescaped field edit action in item selection output, got %q", itemSelectionChunk)
	}
}

func TestActionQueryAfterSelection_DoesNotShowNewButShowsProfileEditors(t *testing.T) {
	inputs := []string{"pr"}
	inputIdx := 0
	restoreInput := utils.UseReadUserInputForTests(func() (string, error) {
		if inputIdx >= len(inputs) {
			return "", io.EOF
		}
		ret := inputs[inputIdx]
		inputIdx++
		return ret, nil
	})
	defer restoreInput()

	origStdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("failed to create stdout pipe: %v", err)
	}
	os.Stdout = w
	defer func() {
		os.Stdout = origStdout
	}()

	selectedAction, err := queryForAction([]action{conf, del, confWithEditor, promptEditWithEditor, unescapedFieldEditWithEditor, back})
	_ = w.Close()
	if err != nil {
		t.Fatalf("failed to query for action: %v", err)
	}
	if selectedAction != promptEditWithEditor {
		t.Fatalf("expected promptEditWithEditor, got %v", selectedAction)
	}

	outBytes, readErr := io.ReadAll(r)
	if readErr != nil {
		t.Fatalf("failed to read stdout: %v", readErr)
	}
	out := string(outBytes)
	if strings.Contains(out, "[n]ew") {
		t.Fatalf("did not expect [n]ew after selecting an item, got %q", out)
	}
	if !strings.Contains(out, "[pr]ompt edit with editor") {
		t.Fatalf("expected prompt edit action in output, got %q", out)
	}
}
