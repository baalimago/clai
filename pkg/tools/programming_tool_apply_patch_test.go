package tools

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
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

func TestApplyPatchTool_Call_WithUnifiedDiffAndContextHeaders(t *testing.T) {
	tempDir := t.TempDir()
	path := filepath.Join(tempDir, "existing.txt")
	err := os.WriteFile(path, []byte("one\ntwo\nthree\n"), 0o644)
	if err != nil {
		t.Fatalf("write existing file: %v", err)
	}

	patch := fmt.Sprintf(`*** Begin Patch
*** Update File: %s
@@ -1,3 +1,3 @@
 one
-two
+TWO
 three
*** End Patch`, path)

	_, err = ApplyPatchTool{}.Call(pub_models.Input{"patch": patch})
	if err != nil {
		t.Fatalf("call apply_patch with unified diff: %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read patched file: %v", err)
	}
	if string(got) != "one\nTWO\nthree\n" {
		t.Fatalf("unexpected patched content: %q", string(got))
	}
}

func TestApplyPatchTool_Call_WithScopedHunkContext(t *testing.T) {
	tempDir := t.TempDir()
	path := filepath.Join(tempDir, "existing.txt")
	err := os.WriteFile(path, []byte("package internal\n\nfunc answer() int {\n\treturn 41\n}\n"), 0o644)
	if err != nil {
		t.Fatalf("write existing file: %v", err)
	}

	patch := fmt.Sprintf(`*** Begin Patch
*** Update File: %s
@@
 func answer() int {
-	return 41
+	return 42
 }
*** End Patch`, path)

	_, err = ApplyPatchTool{}.Call(pub_models.Input{"patch": patch})
	if err != nil {
		t.Fatalf("call apply_patch with scoped hunk context: %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read patched file: %v", err)
	}
	if string(got) != "package internal\n\nfunc answer() int {\n\treturn 42\n}\n" {
		t.Fatalf("unexpected patched content: %q", string(got))
	}
}

func TestApplyPatchTool_Call_WithRepeatedContextAnchors(t *testing.T) {
	tempDir := t.TempDir()
	path := filepath.Join(tempDir, "existing.txt")
	err := os.WriteFile(path, []byte("target\nold\nshared\nother\nshared\nkeep\n"), 0o644)
	if err != nil {
		t.Fatalf("write existing file: %v", err)
	}

	patch := fmt.Sprintf(`*** Begin Patch
*** Update File: %s
@@
 shared
-keep
+KEPT
*** End Patch`, path)

	_, err = ApplyPatchTool{}.Call(pub_models.Input{"patch": patch})
	if err != nil {
		t.Fatalf("call apply_patch with repeated anchors: %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read patched file: %v", err)
	}
	if string(got) != "target\nold\nshared\nother\nshared\nKEPT\n" {
		t.Fatalf("unexpected patched content: %q", string(got))
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

func TestParseApplyPatch_StripsLeadingPatchCLIEnvelope(t *testing.T) {
	patch := strings.Join([]string{
		"apply_patch <<'EOF'",
		"*** Begin Patch",
		"*** Add File: hello.txt",
		"+hello",
		"*** End Patch",
		"EOF",
	}, "\n")

	ops, err := parseApplyPatch(patch)
	if err != nil {
		t.Fatalf("parse apply_patch with envelope: %v", err)
	}
	if len(ops) != 1 {
		t.Fatalf("expected 1 operation, got %d", len(ops))
	}
	if ops[0].kind != patchKindAdd {
		t.Fatalf("expected add operation, got %q", ops[0].kind)
	}
	if ops[0].path != "hello.txt" {
		t.Fatalf("expected path hello.txt, got %q", ops[0].path)
	}
}
