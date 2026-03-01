package tools

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	pub_models "github.com/baalimago/clai/pkg/text/models"
)

func TestApplyPatchTool_Call(t *testing.T) {
	tempDir := t.TempDir()
	tool := ApplyPatchTool{}

	existingPath := filepath.Join(tempDir, "existing.txt")
	err := os.WriteFile(existingPath, []byte("a\nb\nc\n"), 0o644)
	if err != nil {
		t.Fatalf("setup failed: %v", err)
	}

	updatePatch := fmt.Sprintf(`*** Begin Patch
*** Update File: %s
@@
 a
-b
+B
 c
*** End Patch`, existingPath)

	out, err := tool.Call(pub_models.Input{"patch": updatePatch})
	if err != nil {
		t.Fatalf("update failed: %v", err)
	}
	if out == "" {
		t.Fatalf("expected output for update")
	}

	updated, err := os.ReadFile(existingPath)
	if err != nil {
		t.Fatalf("read updated file failed: %v", err)
	}
	if string(updated) != "a\nB\nc\n" {
		t.Fatalf("unexpected update result: %q", string(updated))
	}

	addPath := filepath.Join(tempDir, "added.txt")
	addPatch := fmt.Sprintf(`*** Begin Patch
*** Add File: %s
+one
+two
*** End Patch`, addPath)
	_, err = tool.Call(pub_models.Input{"patch": addPatch})
	if err != nil {
		t.Fatalf("add failed: %v", err)
	}
	added, err := os.ReadFile(addPath)
	if err != nil {
		t.Fatalf("read added file failed: %v", err)
	}
	if string(added) != "one\ntwo" {
		t.Fatalf("unexpected added content: %q", string(added))
	}

	movePath := filepath.Join(tempDir, "moved.txt")
	movePatch := fmt.Sprintf(`*** Begin Patch
*** Update File: %s
*** Move to: %s
@@
 a
-B
+c
*** End Patch`, existingPath, movePath)
	_, err = tool.Call(pub_models.Input{"patch": movePatch})
	if err != nil {
		t.Fatalf("move failed: %v", err)
	}
	if _, err := os.Stat(existingPath); !os.IsNotExist(err) {
		t.Fatalf("expected old file to be removed, err: %v", err)
	}
	moved, err := os.ReadFile(movePath)
	if err != nil {
		t.Fatalf("read moved file failed: %v", err)
	}
	if string(moved) != "a\nc\nc\n" {
		t.Fatalf("unexpected moved content: %q", string(moved))
	}

	deletePath := filepath.Join(tempDir, "delete.txt")
	err = os.WriteFile(deletePath, []byte("delete me\n"), 0o644)
	if err != nil {
		t.Fatalf("setup delete failed: %v", err)
	}
	deletePatch := fmt.Sprintf(`*** Begin Patch
*** Delete File: %s
*** End Patch`, deletePath)
	_, err = tool.Call(pub_models.Input{"patch": deletePatch})
	if err != nil {
		t.Fatalf("delete failed: %v", err)
	}
	if _, err := os.Stat(deletePath); !os.IsNotExist(err) {
		t.Fatalf("expected file to be deleted, err: %v", err)
	}
}

func TestApplyPatchTool_Errors(t *testing.T) {
	tool := ApplyPatchTool{}

	_, err := tool.Call(pub_models.Input{"patch": 123})
	if err == nil {
		t.Fatalf("expected error for non-string patch")
	}

	_, err = tool.Call(pub_models.Input{})
	if err == nil {
		t.Fatalf("expected error for missing patch")
	}

	_, err = tool.Call(pub_models.Input{"patch": "not a patch"})
	if err == nil {
		t.Fatalf("expected error for invalid patch")
	}
}
